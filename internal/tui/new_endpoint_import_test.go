package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// The certificate exchange, driven against the scripted driver rather than a
// pair of live instances. importPeerCertificate is the one phase whose bugs are
// silent: every statement it emits succeeds against an instance that already
// has the login, the user or a certificate under the same name, and the run
// reports success while the endpoints then refuse each other's connections
// with nothing saying why. It had no unit test at all — the whole pipeline was
// covered under WithScript, where no public key can be read and the exchange is
// skipped by design.

// endpointCertRow is a sys.certificates row as gosmo scans it: name, id,
// principal, subject, private-key protection, dates, thumbprint.
func endpointCertRow(name string, thumb []byte, keyType string) []driver.Value {
	return []driver.Value{name, int64(256), int64(1), name + " subject", keyType,
		time.Now(), time.Now().Add(24 * time.Hour), thumb}
}

// endpointPeerFor builds one peer the way configure does — ensureCertificate
// first, so cert and encoded hold what this instance presents — over an
// instance scripted as already having a master key and its own certificate.
// extra is scripted ahead of those, so a test can answer the lookups
// importPeerCertificate makes on this peer.
func endpointPeerFor(t *testing.T, d *NewEndpointDialog, name string, extra ...fakeResponse) (*endpointPeer, *fakeInstance) {
	t.Helper()
	own := d.certificateName(name)
	resp := append(extra,
		fakeResponse{match: "sys.symmetric_keys", cols: 1, rows: [][]driver.Value{{int64(1)}}},
		fakeResponse{match: "FROM   sys.certificates", arg: own, cols: 8, rows: [][]driver.Value{
			endpointCertRow(own, []byte(name+"-thumb"), "ENCRYPTED_BY_MASTER_KEY"),
		}},
		fakeResponse{match: "CERTENCODED", cols: 1, rows: [][]driver.Value{{[]byte(name + "-public-key")}}},
	)
	sc, inst := newFakeConn(t, resp...)
	p := &endpointPeer{
		inst:   &newEndpointInstance{name: name},
		server: sc.Server,
		master: sc.Server.Database("master"),
		ctx:    context.Background(),
	}
	if err := d.ensureCertificate(p.ctx, p); err != nil {
		t.Fatalf("ensureCertificate for %s: %v", name, err)
	}
	if len(p.encoded) == 0 || p.cert == nil {
		t.Fatalf("%s has no certificate to present after ensureCertificate", name)
	}
	return p, inst
}

// absentLoginAndUser is an instance that has neither the peer's login nor its
// user — the fresh case, where both are created.
func absentLoginAndUser() []fakeResponse {
	return []fakeResponse{
		{match: "FROM sys.server_principals", cols: 7},
		{match: "sys.database_principals", cols: 9},
	}
}

// newImportDialog is the dialog with only the fields the exchange reads.
func newImportDialog(t *testing.T) *NewEndpointDialog {
	t.Helper()
	d := NewNewEndpointDialog(newTestApp())
	d.ctx = context.Background()
	return d
}

// TestImportPeerCertificateInstallsTheOtherInstancesKey is the phase's whole
// job: on p, a login and user for the *other* instance, owning the *other*
// instance's public certificate. The hex is asserted against the peer's own
// encoded bytes — an exchange that imported p's own certificate here, or
// installed this one on the wrong instance, emits an equally plausible
// statement and leaves both endpoints unable to authenticate.
func TestImportPeerCertificateInstallsTheOtherInstancesKey(t *testing.T) {
	d := newImportDialog(t)
	local, localInst := endpointPeerFor(t, d, "UBUSQL1", append(absentLoginAndUser(),
		fakeResponse{match: "FROM   sys.certificates", arg: "UBUSQL2_Cert", cols: 8})...)
	remote, remoteInst := endpointPeerFor(t, d, "UBUSQL2")
	before := len(remoteInst.Statements())

	if err := d.importPeerCertificate(local.ctx, local, remote); err != nil {
		t.Fatalf("importPeerCertificate: %v", err)
	}

	joined := strings.Join(localInst.StatementsIn("master"), "\n---\n")
	all := strings.Join(localInst.Statements(), "\n---\n")
	for _, want := range []string{"CREATE LOGIN [UBUSQL2_login]"} {
		if !strings.Contains(all, want) {
			t.Errorf("%s did not reach the instance:\n%s", want, all)
		}
	}
	for _, want := range []string{"CREATE USER [UBUSQL2_user]", "CREATE CERTIFICATE [UBUSQL2_Cert]"} {
		// In master, not merely somewhere: the endpoint's certificates and the
		// users that own them live there and nowhere else.
		if !strings.Contains(joined, want) {
			t.Errorf("%s did not run in master:\n%s", want, joined)
		}
	}
	// The bytes are the peer's, and they are the ones ensureCertificate read
	// off the peer — not a re-read here, and not this instance's own key.
	wantHex := "FROM BINARY = 0x" + strings.ToUpper(hexOf(remote.encoded))
	if !strings.Contains(joined, wantHex) {
		t.Errorf("the imported certificate is not %s's public key (%s):\n%s", remote.inst.name, wantHex, joined)
	}
	if strings.Contains(joined, strings.ToUpper(hexOf(local.encoded))) {
		t.Errorf("the instance imported its own certificate:\n%s", joined)
	}
	if got := remoteInst.Statements(); len(got) != before {
		t.Errorf("the exchange wrote to the peer it was reading from:\n%s", strings.Join(got[before:], "\n"))
	}
}

// A login and user that are already there are not created again. CREATE USER is
// not idempotent, and a run that emitted it anyway fails on the second pass
// over a pair that is half configured — which is the ordinary case, since this
// dialog is how the first half got there.
func TestImportCreatesNeitherLoginNorUserTwice(t *testing.T) {
	d := newImportDialog(t)
	local, localInst := endpointPeerFor(t, d, "UBUSQL1",
		fakeResponse{match: "FROM sys.server_principals", cols: 7, rows: [][]driver.Value{
			{"UBUSQL2_login", []byte{1, 2}, "SQL_LOGIN", false, "master", time.Now(), time.Now()},
		}},
		fakeResponse{match: "sys.database_principals", cols: 9, rows: [][]driver.Value{
			{int64(5), "SQL_USER", "dbo", time.Now(), time.Now(), "INSTANCE", []byte{1, 2}, "UBUSQL2_login", false},
		}},
		fakeResponse{match: "FROM   sys.certificates", arg: "UBUSQL2_Cert", cols: 8})
	remote, _ := endpointPeerFor(t, d, "UBUSQL2")

	if err := d.importPeerCertificate(local.ctx, local, remote); err != nil {
		t.Fatalf("importPeerCertificate: %v", err)
	}

	joined := strings.Join(localInst.Statements(), "\n---\n")
	for _, unwanted := range []string{"CREATE LOGIN", "CREATE USER"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("%s ran over a principal that already exists:\n%s", unwanted, joined)
		}
	}
	// The certificate is still imported: only the principals were there.
	if !strings.Contains(joined, "CREATE CERTIFICATE [UBUSQL2_Cert]") {
		t.Errorf("the peer's certificate was not imported:\n%s", joined)
	}
}

// The same certificate already imported is a no-op, not an error and not a
// second CREATE.
func TestImportSkipsACertificateAlreadyThere(t *testing.T) {
	d := newImportDialog(t)
	local, localInst := endpointPeerFor(t, d, "UBUSQL1", append(absentLoginAndUser(),
		fakeResponse{match: "FROM   sys.certificates", arg: "UBUSQL2_Cert", cols: 8, rows: [][]driver.Value{
			endpointCertRow("UBUSQL2_Cert", []byte("UBUSQL2-thumb"), "NO_PRIVATE_KEY"),
		}})...)
	remote, _ := endpointPeerFor(t, d, "UBUSQL2")

	if err := d.importPeerCertificate(local.ctx, local, remote); err != nil {
		t.Fatalf("importPeerCertificate over an existing import: %v", err)
	}
	if joined := strings.Join(localInst.Statements(), "\n---\n"); strings.Contains(joined, "CREATE CERTIFICATE") {
		t.Errorf("the certificate was imported a second time:\n%s", joined)
	}
}

// The same name holding a *different* certificate is the reinstalled-peer case:
// a fresh key pair under the old name. Skipping on the name alone reports
// success and leaves the endpoint refusing the peer, so it has to be an error
// that names both instances and the certificate to drop.
func TestImportRefusesADifferentCertificateOfTheSameName(t *testing.T) {
	d := newImportDialog(t)
	local, localInst := endpointPeerFor(t, d, "UBUSQL1", append(absentLoginAndUser(),
		fakeResponse{match: "FROM   sys.certificates", arg: "UBUSQL2_Cert", cols: 8, rows: [][]driver.Value{
			endpointCertRow("UBUSQL2_Cert", []byte("a-previous-installation"), "NO_PRIVATE_KEY"),
		}})...)
	remote, _ := endpointPeerFor(t, d, "UBUSQL2")

	err := d.importPeerCertificate(local.ctx, local, remote)
	if err == nil {
		t.Fatal("a certificate with the same name and a different key was accepted")
	}
	for _, want := range []string{"UBUSQL1", "UBUSQL2", "UBUSQL2_Cert"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s — the user has to know which certificate to drop where", err, want)
		}
	}
	if joined := strings.Join(localInst.Statements(), "\n---\n"); strings.Contains(joined, "CREATE CERTIFICATE") {
		t.Errorf("the import ran anyway:\n%s", joined)
	}
}

// A lookup that fails for any reason other than absence is reported as itself.
// Treating every failure as absence reports the CREATE LOGIN error instead of
// the permission or connection error that really stopped the pipeline — on a
// dialog where that distinction is the whole diagnosis.
func TestImportReportsALookupFailureRatherThanCreating(t *testing.T) {
	d := newImportDialog(t)
	// No sys.server_principals response: the fake refuses the query, which is
	// what a login the caller may not read looks like from here.
	local, localInst := endpointPeerFor(t, d, "UBUSQL1")
	remote, _ := endpointPeerFor(t, d, "UBUSQL2")

	err := d.importPeerCertificate(local.ctx, local, remote)
	if err == nil {
		t.Fatal("a failed login lookup was treated as an absent login")
	}
	if !strings.Contains(err.Error(), "look up login UBUSQL2_login") {
		t.Errorf("error = %q, want it to report the lookup, not a create", err)
	}
	if joined := strings.Join(localInst.Statements(), "\n---\n"); joined != "" {
		t.Errorf("something was created after a failed lookup:\n%s", joined)
	}
}

// Under scripting the peer's certificate was collected, not created, so there
// is no public key to import. The skip is recorded on the instance whose script
// is missing the import — not on the one whose certificate is missing — because
// that is the script the user will run.
func TestImportRecordsThePeerItCouldNotCopy(t *testing.T) {
	d := newImportDialog(t)
	local, localInst := endpointPeerFor(t, d, "UBUSQL1")
	remote, _ := endpointPeerFor(t, d, "UBUSQL2")
	remote.encoded = nil

	if err := d.importPeerCertificate(local.ctx, local, remote); err != nil {
		t.Fatalf("a peer with no readable certificate is not an error: %v", err)
	}
	if got := local.certSkipped; len(got) != 1 || got[0] != "UBUSQL2" {
		t.Errorf("local.certSkipped = %v, want the peer it could not copy", got)
	}
	if len(remote.certSkipped) != 0 {
		t.Errorf("the skip was recorded on the peer instead: %v", remote.certSkipped)
	}
	if joined := strings.Join(localInst.Statements(), "\n---\n"); joined != "" {
		t.Errorf("statements ran for an import that could not happen:\n%s", joined)
	}
}

// hexOf renders bytes the way CREATE CERTIFICATE ... FROM BINARY does.
func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}
