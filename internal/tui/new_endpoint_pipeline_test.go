package tui

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// freshEndpointInstanceResponses script an instance that has nothing yet — no
// master key, no certificate, no login, no endpoint. Every read is scripted
// explicitly: the fake errors on an unscripted query rather than answering
// empty, so a "not found" here is a decision, not an omission.
func freshEndpointInstanceResponses() []fakeResponse {
	return []fakeResponse{
		{match: "sys.symmetric_keys", cols: 1, rows: [][]driver.Value{{int64(0)}}},
		{match: "FROM   sys.certificates", cols: 8},
		{match: "FROM sys.server_principals", cols: 7},
		{match: "sys.database_mirroring_endpoints", cols: 7},
	}
}

// newEndpointDialogForTest builds the dialog over two scripted instances, with
// peerServerFor answering from them instead of opening a real peer connection.
func newEndpointDialogForTest(t *testing.T) (*NewEndpointDialog, *fakeInstance, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	local, localInst := newFakeConn(t, freshEndpointInstanceResponses()...)
	local.Opts = config.Connection{Server: "UBUSQL1"}
	remote, remoteInst := newFakeConn(t, freshEndpointInstanceResponses()...)

	d := NewNewEndpointDialog(a)
	d.sc = local
	d.ctx = context.Background()
	d.endpointName, d.port, d.algorithm, d.masterKeyPass = "Hadr_endpoint", 5022, "AES", "pw"
	d.instances = []*newEndpointInstance{
		{name: "UBUSQL1", local: true},
		{name: "UBUSQL2"},
	}
	d.commitInputs = func() {}
	d.peerServerFor = func(_ context.Context, inst *newEndpointInstance) (*gosmo.Server, error) {
		if inst.local {
			return local.Server, nil
		}
		return remote.Server, nil
	}
	return d, localInst, remoteInst
}

// TestEndpointConfigureCollectsPerInstance is the rule the whole rework rests
// on: every write goes through the peer's own context, so the collector a
// statement lands in is what says which instance it runs on.
//
// Run the phases against the flat ctx instead and every statement lands in one
// collector, the grouping collapses, and the script silently claims all of it
// runs here — which is what the shell's Script Changes did.
func TestEndpointConfigureCollectsPerInstance(t *testing.T) {
	d, _, _ := newEndpointDialogForTest(t)

	scriptCtx, outer := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}

	if len(outer.Statements) != 0 {
		t.Errorf("%d statements landed in the pipeline-wide collector, want 0 — they belong to an instance:\n%s",
			len(outer.Statements), strings.Join(outer.Statements, "\n"))
	}
	if len(d.scriptedGroups) != 2 {
		t.Fatalf("configure produced %d groups, want one per instance (2)", len(d.scriptedGroups))
	}
	for _, g := range d.scriptedGroups {
		if len(g.stmts) == 0 {
			t.Errorf("%s collected no statements", g.instance)
		}
	}
	// Each instance's certificate is created on that instance, never on the
	// other — the one assertion that fails if the grouping is wrong rather than
	// merely absent.
	for _, g := range d.scriptedGroups {
		own := "[" + endpointPrincipalBase(g.instance) + "_Cert]"
		joined := strings.Join(g.stmts, "\n")
		if !strings.Contains(joined, "CREATE CERTIFICATE "+own) {
			t.Errorf("%s's group does not create its own certificate %s:\n%s", g.instance, own, joined)
		}
		for _, other := range d.scriptedGroups {
			if other.instance == g.instance {
				continue
			}
			foreign := "CREATE CERTIFICATE [" + endpointPrincipalBase(other.instance) + "_Cert] WITH"
			if strings.Contains(joined, foreign) {
				t.Errorf("%s's group creates %s's own certificate:\n%s", g.instance, other.instance, joined)
			}
		}
	}
}

// TestEndpointConfigureReportsThePendingCertificates pins the honest half at
// the source. On a fresh pair nothing was really created, so no public key can
// be read and the exchange cannot be scripted — the run has to say which
// instance it could not read and which import each one is therefore missing.
func TestEndpointConfigureReportsThePendingCertificates(t *testing.T) {
	d, _, _ := newEndpointDialogForTest(t)

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}

	for _, g := range d.scriptedGroups {
		if !g.certPending {
			t.Errorf("%s is not marked as having no certificate yet", g.instance)
		}
		if len(g.certSkipped) != 1 {
			t.Errorf("%s skipped %v imports, want exactly the one peer", g.instance, g.certSkipped)
			continue
		}
		if g.certSkipped[0] == g.instance {
			t.Errorf("%s recorded skipping its own certificate", g.instance)
		}
	}
	// And it survives into what the user actually reads.
	if out := annotateEndpointScript(d.scriptedGroups); !strings.Contains(out, "THIS SCRIPT IS INCOMPLETE") {
		t.Errorf("the rendered script does not warn that it is partial:\n%s", out)
	}
}

// TestEndpointScriptingWritesNothingToTheServer is what Script Changes has to
// promise. Reads are legitimate and required — a certificate's public key can
// only be read from the instance holding it — but not one statement may reach
// either instance.
func TestEndpointScriptingWritesNothingToTheServer(t *testing.T) {
	d, localInst, remoteInst := newEndpointDialogForTest(t)

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}

	for name, inst := range map[string]*fakeInstance{"local": localInst, "remote": remoteInst} {
		if got := inst.Statements(); len(got) != 0 {
			t.Errorf("Script Changes executed %d statements against the %s instance:\n%s",
				len(got), name, strings.Join(got, "\n"))
		}
	}
}

// TestEndpointConfigureUsesEveryListedInstance guards the loop against acting
// only on the connection the dialog was opened from — the local instance is
// first in the list, so a pipeline that ignored the rest would still produce a
// plausible-looking script.
func TestEndpointConfigureUsesEveryListedInstance(t *testing.T) {
	d, _, _ := newEndpointDialogForTest(t)
	var asked []string
	inner := d.peerServerFor
	d.peerServerFor = func(ctx context.Context, inst *newEndpointInstance) (*gosmo.Server, error) {
		asked = append(asked, inst.name)
		return inner(ctx, inst)
	}

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}
	if len(asked) != 2 || asked[0] != "UBUSQL1" || asked[1] != "UBUSQL2" {
		t.Errorf("configure resolved %v, want both listed instances in order", asked)
	}
}

// defaultPeerServer must still route the local instance to this connection and
// everything else through Peer; peerServerFor exists to be overridden in a
// test, not to change what the dialog does.
func TestDefaultPeerServerRoutesTheLocalInstanceHere(t *testing.T) {
	a := newTestApp()
	sc := &db.ServerConn{Opts: config.Connection{Server: "UBUSQL1"}}
	d := NewNewEndpointDialog(a)
	d.sc = sc

	got, err := d.defaultPeerServer(context.Background(), &newEndpointInstance{name: "UBUSQL1", local: true})
	if err != nil {
		t.Fatalf("defaultPeerServer for the local instance: %v", err)
	}
	if got != sc.Server {
		t.Error("the local instance was not routed to this connection's server")
	}
}

// TestEndpointScriptGrantsOnlyToLoginsItCreates is a defect the first version
// of this shipped: the exchange was skipped for want of a certificate, so
// UBUSQL2_login was never created — and the endpoint phase went on to emit
// GRANT CONNECT ... TO [UBUSQL2_login] anyway. Run as one batch the script
// fails on a login that does not exist, and it fails *before* the endpoint
// statements above it have taken effect.
//
// Everything a partial script contains has to be runnable; what it cannot do
// belongs in the notes, not in a statement that errors.
func TestEndpointScriptGrantsOnlyToLoginsItCreates(t *testing.T) {
	d, _, _ := newEndpointDialogForTest(t)

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}

	for _, g := range d.scriptedGroups {
		created := map[string]bool{}
		var grantedTo []string
		for _, stmt := range g.stmts {
			if _, after, ok := strings.Cut(stmt, "CREATE LOGIN ["); ok {
				if name, _, ok := strings.Cut(after, "]"); ok {
					created[name] = true
				}
			}
			if _, after, ok := strings.Cut(stmt, "ON ENDPOINT::"); ok {
				if _, to, ok := strings.Cut(after, " TO ["); ok {
					if name, _, ok := strings.Cut(to, "]"); ok {
						grantedTo = append(grantedTo, name)
					}
				}
			}
		}
		for _, login := range grantedTo {
			if !created[login] {
				t.Errorf("%s grants CONNECT to %q, which its own script never creates:\n%s",
					g.instance, login, strings.Join(g.stmts, "\n"))
			}
		}
	}
	// The script must still be worth running: the endpoint itself is created,
	// and its certificate with it.
	for _, g := range d.scriptedGroups {
		if !strings.Contains(strings.Join(g.stmts, "\n"), "CREATE ENDPOINT") {
			t.Errorf("%s's partial script does not create its endpoint", g.instance)
		}
	}
}

// TestEndpointDialogOverridesTheShellScript pins the wiring this whole item
// started from. newObjectDialog.init sets OnScript unconditionally, so the
// Script Changes button was already live and running the shell's version:
// every statement in one batch with nothing saying only some of it runs here.
// Nothing failed if the override was absent, which is exactly why it was.
func TestEndpointDialogOverridesTheShellScript(t *testing.T) {
	a := newTestApp()
	d := NewNewEndpointDialog(a)

	if d.OnScript == nil {
		t.Fatal("OnScript is nil — the Script Changes button would do nothing")
	}
	want := reflect.ValueOf(d.runScript).Pointer()
	shell := reflect.ValueOf(d.newObjectDialog.runScript).Pointer()
	if got := reflect.ValueOf(d.OnScript).Pointer(); got != want {
		which := "something else"
		if got == shell {
			which = "the shell's flat runScript"
		}
		t.Errorf("OnScript is %s, want NewEndpointDialog.runScript", which)
	}
}

// configuredEndpointInstanceResponses script an instance that already has
// everything: a master key, its own certificate with a private key, and a
// started endpoint. This is the second-press case — the certificates exist, so
// their public keys can be read and the exchange really can be scripted.
func configuredEndpointInstanceResponses(certName string, thumb []byte) []fakeResponse {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return []fakeResponse{
		{match: "sys.symmetric_keys", cols: 1, rows: [][]driver.Value{{int64(1)}}},
		// The instance's own certificate. Answered for every by-name read, so
		// a peer's certificate reads as "already imported and identical" —
		// which is only true when the thumbprints match, and this scripts the
		// case where they do.
		{match: "FROM   sys.certificates", cols: 8, rows: [][]driver.Value{
			{certName, int64(256), int64(1), "subject", "MASTER KEY", at, at, thumb},
		}},
		{match: "CERTENCODED", cols: 1, rows: [][]driver.Value{{[]byte{0xAB, 0xCD}}}},
		// No login and no user yet: those are what a second press still has to
		// create, and skipping them silently is the failure mode below.
		{match: "FROM sys.server_principals", cols: 7},
		{match: "FROM   sys.database_principals", cols: 9},
		{match: "sys.database_mirroring_endpoints", cols: 8, rows: [][]driver.Value{
			{"AGEP", int64(5022), "STARTED", "ALL", true, "AES", "CERTIFICATE", "sa"},
		}},
	}
}

// TestEndpointConfigureScriptsTheExchangeOnceCertificatesExist is the case a
// fresh pair cannot reach and the one the user gets on the second press: with
// the certificates in place the public keys are readable and the whole
// exchange is scriptable, so the script must carry no warning at all.
func TestEndpointConfigureScriptsTheExchangeOnceCertificatesExist(t *testing.T) {
	a := newTestApp()
	thumb := []byte{0x01, 0x02}
	local, _ := newFakeConn(t, configuredEndpointInstanceResponses("ubusql1_Cert", thumb)...)
	local.Opts = config.Connection{Server: "ubusql1"}
	remote, _ := newFakeConn(t, configuredEndpointInstanceResponses("ubusql2_Cert", thumb)...)

	d := NewNewEndpointDialog(a)
	d.sc = local
	d.ctx = context.Background()
	d.endpointName, d.port, d.algorithm, d.masterKeyPass = "AGEP", 5022, "AES", "pw"
	d.instances = []*newEndpointInstance{{name: "ubusql1", local: true}, {name: "ubusql2"}}
	d.commitInputs = func() {}
	d.peerServerFor = func(_ context.Context, inst *newEndpointInstance) (*gosmo.Server, error) {
		if inst.local {
			return local.Server, nil
		}
		return remote.Server, nil
	}

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}

	for _, g := range d.scriptedGroups {
		if g.certPending || len(g.certSkipped) > 0 {
			t.Errorf("%s still reports a gap with the certificates in place: pending=%v skipped=%v",
				g.instance, g.certPending, g.certSkipped)
		}
	}
	out := annotateEndpointScript(d.scriptedGroups)
	if strings.Contains(out, "INCOMPLETE") {
		t.Errorf("the second press still warns that the script is partial:\n%s", out)
	}
	// The login and the CONNECT grant it needs both belong to the same
	// instance, and the grant is only runnable because the login precedes it.
	for _, g := range d.scriptedGroups {
		joined := strings.Join(g.stmts, "\n")
		if !strings.Contains(joined, "CREATE LOGIN") {
			t.Errorf("%s's script does not create the peer's login:\n%s", g.instance, joined)
		}
		if !strings.Contains(joined, "GRANT CONNECT ON ENDPOINT::[AGEP]") {
			t.Errorf("%s's script does not grant the peer CONNECT:\n%s", g.instance, joined)
		}
	}
}

// TestEndpointScriptSkipsAUserThatAlreadyExists is the live finding. The
// pipeline used to create the user unconditionally and tolerate the
// already-exists error — a strategy that works when the statement actually
// runs and fails under WithScript, where it never does. The emitted script
// then opened with a CREATE USER that errors. Seen on ubusql1/ubusql2, where
// both users were already there.
func TestEndpointScriptSkipsAUserThatAlreadyExists(t *testing.T) {
	a := newTestApp()
	thumb := []byte{0x01, 0x02}
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	existingUser := fakeResponse{match: "FROM   sys.database_principals", cols: 9, rows: [][]driver.Value{
		{int64(5), "SQL_USER", "dbo", at, at, "INSTANCE", []byte{0x09}, "ubusql2_login", false},
	}}
	withUser := func(name string) []fakeResponse {
		out := []fakeResponse{existingUser}
		for _, r := range configuredEndpointInstanceResponses(name, thumb) {
			if r.match == "FROM   sys.database_principals" {
				continue
			}
			out = append(out, r)
		}
		return out
	}
	local, _ := newFakeConn(t, withUser("ubusql1_Cert")...)
	local.Opts = config.Connection{Server: "ubusql1"}
	remote, _ := newFakeConn(t, withUser("ubusql2_Cert")...)

	d := NewNewEndpointDialog(a)
	d.sc = local
	d.ctx = context.Background()
	d.endpointName, d.port, d.algorithm, d.masterKeyPass = "AGEP", 5022, "AES", "pw"
	d.instances = []*newEndpointInstance{{name: "ubusql1", local: true}, {name: "ubusql2"}}
	d.commitInputs = func() {}
	d.peerServerFor = func(_ context.Context, inst *newEndpointInstance) (*gosmo.Server, error) {
		if inst.local {
			return local.Server, nil
		}
		return remote.Server, nil
	}

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := d.configure(scriptCtx); err != nil {
		t.Fatalf("configure under WithScript: %v", err)
	}
	for _, g := range d.scriptedGroups {
		for _, stmt := range g.stmts {
			if strings.Contains(stmt, "CREATE USER") {
				t.Errorf("%s's script creates a user that already exists — it will fail when run:\n%s",
					g.instance, stmt)
			}
		}
	}
}
