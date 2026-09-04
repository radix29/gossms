//go:build livedb

// Live verification of the named-instance half of the New Database Mirroring
// Endpoint exchange: endpointPrincipalBase's HOST\INSTANCE -> HOST$INSTANCE
// mapping, and the pipeline built on it.
//
// Not reachable from a unit test. The mapping's input is what a real instance
// answers for @@SERVERNAME, and its output is a name SQL Server either accepts
// as an ordinary principal or reads as a Windows one — both facts about a
// server, and both invisible to a fake, which returns whatever it was
// scripted with. Until win10cli gained named instances every server in the
// estate was a default instance, so the branch had never run end to end.
//
//	go test -tags livedb ./internal/tui/ -run TestLiveEndpoint -v \
//	  -live-ep-server win10cli -live-ep-named 'win10cli\sql2016' \
//	  -live-ep-named2 'win10cli\sql2017' \
//	  -live-ep-user sa -live-ep-password PASS
//
// The first test only connects. The second runs the real exchange and needs
// -live-ep-exchange as well: it writes to all three instances (certificates,
// logins, users, one endpoint) and drops everything it created, including the
// database master keys the exchange has to add to an instance that has none.
// It leaves an instance's *existing* endpoint, certificate and master key
// alone, which is also the path it asserts on.
//
// Both named instances must genuinely be named — the tests fail rather than
// skip on a default instance, since that is the case that passes without
// exercising anything.
package tui

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"

	_ "github.com/microsoft/go-mssqldb"
)

var (
	liveEPServer = flag.String("live-ep-server", "", "the instance the dialog is opened from")
	liveEPNamed  = flag.String("live-ep-named", "", "a named instance to add as a peer")
	liveEPNamed2 = flag.String("live-ep-named2", "", "a second named instance to add as a peer")
	liveEPUser   = flag.String("live-ep-user", "", "sysadmin login on all three")
	liveEPPass   = flag.String("live-ep-password", "", "password for -live-ep-user")

	liveEPExchange = flag.Bool("live-ep-exchange", false,
		"run the exchange for real (see TestLiveEndpointExchangeNamesNamedInstances)")
	liveEPPort = flag.Int("live-ep-port", 5023,
		"port for the endpoint the exchange creates")
	liveEPProbePort = flag.Int("live-ep-probe-port", 5024,
		"port for the endpoint the test pre-creates on -live-ep-named2")
)

// liveEPProbeEndpoint is the endpoint the test creates on -live-ep-named2
// before the run, so that instance takes the "already has one, left as it is"
// path. Two instances on one host cannot both listen on the one port the
// dialog offers, and that skip is a shipped path in its own right.
const liveEPProbeEndpoint = "gossms_ep_probe"

// liveEPMasterKeyPass protects a database master key that exists for the
// length of one test and is dropped with it.
const liveEPMasterKeyPass = "g0ssms-Endpoint-Probe!"

// liveEPConn connects to one instance the way the app does.
func liveEPConn(t *testing.T, server string) (*db.ServerConn, context.Context) {
	t.Helper()
	if *liveEPServer == "" || *liveEPNamed == "" || *liveEPUser == "" {
		t.Skip("no -live-ep-server/-live-ep-named/-live-ep-user given")
	}
	sc, err := db.Connect(config.Connection{
		Server:                 server,
		AuthMethod:             config.AuthSQLServer,
		User:                   *liveEPUser,
		Password:               *liveEPPass,
		TrustServerCertificate: true,
	})
	if err != nil {
		t.Fatalf("connect %s: %v", server, err)
	}
	t.Cleanup(sc.Close)
	ctx, cancel := context.WithTimeout(sc.Context(), 120*time.Second)
	t.Cleanup(cancel)
	return sc, ctx
}

// liveEPRaw opens a plain driver connection for the fixture DDL and the
// catalog reads the assertions make. gosmo has no general Exec, and what is
// being checked here is what the pipeline left on the server, not gosmo's own
// view of it.
func liveEPRaw(t *testing.T, server string) *sql.DB {
	t.Helper()
	dsn := "sqlserver://" + *liveEPUser + ":" + *liveEPPass + "@" + strings.ReplaceAll(server, `\`, "/") +
		"?TrustServerCertificate=true&encrypt=false"
	raw, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", server, err)
	}
	t.Cleanup(func() { raw.Close() })
	if err := raw.Ping(); err != nil {
		t.Fatalf("ping %s: %v", server, err)
	}
	return raw
}

// requireNamedInstance fails on a default instance. A default instance has no
// backslash, endpointPrincipalBase is the identity on it, and every assertion
// below would pass without the mapping running at all.
func requireNamedInstance(t *testing.T, sc *db.ServerConn) string {
	t.Helper()
	name := sc.Server.Name()
	if !strings.Contains(name, `\`) {
		t.Fatalf("%s answers @@SERVERNAME %q, which is a default instance; "+
			"-live-ep-named/-live-ep-named2 must name a *named* instance, "+
			"or this test passes without exercising the mapping", sc.Opts.Server, name)
	}
	return name
}

// TestLiveEndpointNamedInstanceReportsABackslashName pins the two halves the
// mapping sits between: what a named instance really answers, and what the
// dialog stores for it.
//
// addInstance is driven rather than assumed, because the name the exchange
// derives every principal from is the instance's own @@SERVERNAME and not the
// one typed into the field — here deliberately in the other case, so a
// pipeline reading the typed name would build different principal names.
func TestLiveEndpointNamedInstanceReportsABackslashName(t *testing.T) {
	sc, _ := liveEPConn(t, *liveEPServer)
	named, _ := liveEPConn(t, *liveEPNamed)
	reported := requireNamedInstance(t, named)

	base := endpointPrincipalBase(reported)
	if strings.Contains(base, `\`) {
		t.Errorf("endpointPrincipalBase(%q) = %q, which still carries a backslash — "+
			"[%s_login] is the spelling of a Windows principal", reported, base, base)
	}
	host, instance, _ := gosmo.ParseServerAddress(reported)
	if want := host + "$" + instance; base != want {
		t.Errorf("endpointPrincipalBase(%q) = %q, want %q", reported, base, want)
	}

	a := newTestApp()
	d := NewNewEndpointDialog(a)
	d.sc = sc
	d.ctx = sc.Context()

	typed := strings.ToLower(*liveEPNamed)
	var got *newEndpointInstance
	var addErr error
	done := false
	d.addInstance(typed, func(inst *newEndpointInstance, err error) {
		got, addErr, done = inst, err, true
	})
	liveEPDrain(t, a, &done)
	if addErr != nil {
		t.Fatalf("add %s: %v", typed, addErr)
	}
	if got.name != reported {
		t.Errorf("adding %q stored the instance as %q, want the name it answers with, %q",
			typed, got.name, reported)
	}
}

// liveEPDrain runs the UI goroutine's share of a postAndWake until done is set.
// The dialog's connect is asynchronous, and its callback runs only on whatever
// drains the pending queue — App.Run in production, this loop here.
func liveEPDrain(t *testing.T, a *App, done *bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for !*done {
		if time.Now().After(deadline) {
			t.Fatal("the dialog's connect never called back")
		}
		a.drainPending()
		time.Sleep(20 * time.Millisecond)
	}
	a.drainPending()
}

// TestLiveEndpointExchangeNamesNamedInstances runs the real exchange across
// one default and two named instances and reads back what it left.
//
// Two named instances rather than one: the principal names are derived from
// the whole instance name and not from the host precisely so that two
// instances on one machine do not share them, and a mapping truncating to the
// host passes every single-named-instance assertion.
func TestLiveEndpointExchangeNamesNamedInstances(t *testing.T) {
	if !*liveEPExchange {
		t.Skip("no -live-ep-exchange: this test writes to all three instances")
	}
	if *liveEPNamed2 == "" {
		t.Skip("no -live-ep-named2")
	}
	local, ctx := liveEPConn(t, *liveEPServer)
	named1, _ := liveEPConn(t, *liveEPNamed)
	named2, _ := liveEPConn(t, *liveEPNamed2)
	requireNamedInstance(t, named1)
	requireNamedInstance(t, named2)

	names := []string{local.Server.Name(), named1.Server.Name(), named2.Server.Name()}
	raws := map[string]*sql.DB{
		names[0]: liveEPRaw(t, *liveEPServer),
		names[1]: liveEPRaw(t, *liveEPNamed),
		names[2]: liveEPRaw(t, *liveEPNamed2),
	}

	// What each instance had before the run, so the cleanup can put it back and
	// the assertions can tell "left alone" from "created".
	before := map[string]*gosmo.DatabaseMirroringEndpoint{}
	for _, sc := range []*db.ServerConn{local, named1, named2} {
		ep, err := sc.Server.DatabaseMirroringEndpointContext(ctx)
		if err != nil {
			t.Fatalf("%s: read endpoint: %v", sc.Server.Name(), err)
		}
		before[sc.Server.Name()] = ep
	}
	if before[names[0]] == nil {
		t.Fatalf("%s has no database mirroring endpoint; -live-ep-server must name an instance that "+
			"already has one, so the run does not try to create a third on this host", names[0])
	}

	// -live-ep-named2 is given an endpoint of its own first: the dialog offers
	// one port for the whole run, and two instances on one host cannot both
	// listen on it. WINDOWS NEGOTIATE because this endpoint is never connected
	// to — it exists to be found and skipped.
	if before[names[2]] == nil {
		liveEPExec(t, raws[names[2]], fmt.Sprintf(
			"CREATE ENDPOINT [%s] STATE = STARTED AS TCP (LISTENER_PORT = %d) "+
				"FOR DATABASE_MIRRORING (ROLE = ALL, AUTHENTICATION = WINDOWS NEGOTIATE, ENCRYPTION = REQUIRED ALGORITHM AES)",
			liveEPProbeEndpoint, *liveEPProbePort))
		t.Cleanup(func() { liveEPExec(t, raws[names[2]], "DROP ENDPOINT ["+liveEPProbeEndpoint+"]") })
	}

	bases := make([]string, len(names))
	for i, n := range names {
		bases[i] = endpointPrincipalBase(n)
	}
	// Registered before the run, not after: a pipeline that fails halfway has
	// still created part of this, and leaving it behind on a real instance is
	// the one outcome worse than the test failing.
	t.Cleanup(func() { liveEPCleanup(t, raws, names, bases, before) })

	a := newTestApp()
	d := NewNewEndpointDialog(a)
	d.sc = local
	d.ctx = ctx
	d.commitInputs = func() {}
	d.endpointName, d.port, d.algorithm = "Hadr_endpoint", *liveEPPort, "AES"
	d.masterKeyPass = liveEPMasterKeyPass
	d.instances = []*newEndpointInstance{{name: names[0], local: true}}
	for _, peer := range []string{*liveEPNamed, *liveEPNamed2} {
		var inst *newEndpointInstance
		var err error
		done := false
		d.addInstance(peer, func(got *newEndpointInstance, e error) { inst, err, done = got, e, true })
		liveEPDrain(t, a, &done)
		if err != nil {
			t.Fatalf("add %s: %v", peer, err)
		}
		d.instances = append(d.instances, inst)
	}

	if err := d.configure(ctx); err != nil {
		t.Fatalf("configure: %v", err)
	}

	// Every principal the exchange creates, on every instance that is not the
	// one it names. A backslash surviving into any of them is the defect the
	// mapping exists to prevent, and it is checked as an absence over the whole
	// server rather than name by name, so a *different* spelling slipping
	// through is caught too.
	for i, n := range names {
		raw := raws[n]
		assertNoBackslashPrincipals(t, raw, n)
		assertCertificate(t, raw, n, bases[i]+"_Cert", true)
		for j := range names {
			if i == j {
				continue
			}
			assertLogin(t, raw, n, bases[j]+"_login")
			assertUser(t, raw, n, bases[j]+"_user")
			assertCertificate(t, raw, n, bases[j]+"_Cert", false)
			assertEndpointConnect(t, raw, n, bases[j]+"_login")
		}
	}

	// The two instances that already had an endpoint keep it, unchanged: an
	// instance can have only one, and the run's whole claim on them is that it
	// added peers to what was there.
	for _, i := range []int{0, 2} {
		ep, err := []*db.ServerConn{local, named1, named2}[i].Server.DatabaseMirroringEndpointContext(ctx)
		if err != nil {
			t.Fatalf("%s: re-read endpoint: %v", names[i], err)
		}
		was := before[names[i]]
		if was == nil {
			was = &gosmo.DatabaseMirroringEndpoint{Name: liveEPProbeEndpoint, Port: *liveEPProbePort}
		}
		if ep.Name != was.Name || ep.Port != was.Port {
			t.Errorf("%s's endpoint changed from %s:%d to %s:%d — an existing endpoint is left as it is",
				names[i], was.Name, was.Port, ep.Name, ep.Port)
		}
	}

	// The one instance that had none gets the endpoint, authenticated by its
	// own certificate under the mapped name.
	ep, err := named1.Server.DatabaseMirroringEndpointContext(ctx)
	if err != nil {
		t.Fatalf("%s: re-read endpoint: %v", names[1], err)
	}
	if ep == nil {
		t.Fatalf("%s still has no endpoint after the run", names[1])
	}
	if !strings.EqualFold(ep.State, "STARTED") {
		t.Errorf("%s's endpoint is %s, not STARTED", names[1], ep.State)
	}
	if ep.Port != *liveEPPort {
		t.Errorf("%s's endpoint is on port %d, want %d", names[1], ep.Port, *liveEPPort)
	}
	if !strings.Contains(strings.ToUpper(ep.ConnectionAuth), "CERTIFICATE") {
		t.Errorf("%s's endpoint authenticates with %q, want CERTIFICATE", names[1], ep.ConnectionAuth)
	}
}

// assertNoBackslashPrincipals is the whole item in one query: a principal or
// certificate named HOST\INSTANCE_something is either refused outright or
// created as something no CONNECT can be granted to.
func assertNoBackslashPrincipals(t *testing.T, raw *sql.DB, server string) {
	t.Helper()
	rows, err := raw.Query(`
		SELECT 'login: ' + name FROM sys.server_principals WHERE name LIKE '%\%' ESCAPE '|' AND name NOT LIKE '%|%%' ESCAPE '|'
		UNION ALL SELECT 'certificate: ' + name FROM master.sys.certificates WHERE name LIKE '%\%'
		UNION ALL SELECT 'user: ' + name FROM master.sys.database_principals WHERE name LIKE '%\%' AND name NOT LIKE '%|%%' ESCAPE '|'`)
	if err != nil {
		t.Fatalf("%s: scan for backslash principals: %v", server, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("%s: scan: %v", server, err)
		}
		if strings.Contains(name, "_login") || strings.Contains(name, "_user") || strings.Contains(name, "_Cert") {
			t.Errorf("%s carries %q — the exchange derived a principal name from the raw HOST\\INSTANCE", server, name)
		}
	}
}

func assertLogin(t *testing.T, raw *sql.DB, server, login string) {
	t.Helper()
	var kind string
	err := raw.QueryRow("SELECT type_desc FROM sys.server_principals WHERE name = @p1", login).Scan(&kind)
	if err == sql.ErrNoRows {
		t.Errorf("%s has no login %s", server, login)
		return
	}
	if err != nil {
		t.Fatalf("%s: read login %s: %v", server, login, err)
	}
	// SQL_LOGIN, not WINDOWS_LOGIN: the name is the point. A backslash makes
	// SQL Server read the same statement as naming a Windows principal, which
	// authenticates from somewhere entirely unrelated to this certificate.
	if kind != "SQL_LOGIN" {
		t.Errorf("%s's %s is a %s, want SQL_LOGIN", server, login, kind)
	}
}

func assertUser(t *testing.T, raw *sql.DB, server, user string) {
	t.Helper()
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM master.sys.database_principals WHERE name = @p1", user).Scan(&n); err != nil {
		t.Fatalf("%s: read user %s: %v", server, user, err)
	}
	if n != 1 {
		t.Errorf("%s has %d users named %s in master, want 1", server, n, user)
	}
}

// assertCertificate checks one certificate is there, and whether it is the
// instance's own (private key, so it can present it) or a peer's public half
// (no private key, and owned by that peer's user).
func assertCertificate(t *testing.T, raw *sql.DB, server, cert string, own bool) {
	t.Helper()
	var pvt, owner string
	err := raw.QueryRow(`SELECT c.pvt_key_encryption_type_desc, ISNULL(p.name, '')
		FROM master.sys.certificates c
		LEFT JOIN master.sys.database_principals p ON p.principal_id = c.principal_id
		WHERE c.name = @p1`, cert).Scan(&pvt, &owner)
	if err == sql.ErrNoRows {
		t.Errorf("%s has no certificate %s", server, cert)
		return
	}
	if err != nil {
		t.Fatalf("%s: read certificate %s: %v", server, cert, err)
	}
	if own {
		if pvt != "ENCRYPTED_BY_MASTER_KEY" {
			t.Errorf("%s's own certificate %s is %s, want ENCRYPTED_BY_MASTER_KEY — an endpoint cannot present a key it has to be given a password for",
				server, cert, pvt)
		}
		return
	}
	if pvt != "NO_PRIVATE_KEY" {
		t.Errorf("%s's imported certificate %s carries %s; only the public half is exchanged", server, cert, pvt)
	}
	if want := strings.TrimSuffix(cert, "_Cert") + "_user"; owner != want {
		t.Errorf("%s's %s is owned by %q, want %q", server, cert, owner, want)
	}
}

// assertEndpointConnect is what the mapping is for: a name SQL Server accepts
// as the grantee of CONNECT on the endpoint.
func assertEndpointConnect(t *testing.T, raw *sql.DB, server, login string) {
	t.Helper()
	var n int
	err := raw.QueryRow(`SELECT COUNT(*)
		FROM sys.server_permissions perm
		JOIN sys.server_principals p ON p.principal_id = perm.grantee_principal_id
		JOIN sys.endpoints e ON e.endpoint_id = perm.major_id
		WHERE perm.class = 105 AND perm.permission_name = 'CONNECT'
		  AND perm.state_desc = 'GRANT' AND p.name = @p1 AND e.type = 4`, login).Scan(&n)
	if err != nil {
		t.Fatalf("%s: read CONNECT for %s: %v", server, login, err)
	}
	if n == 0 {
		t.Errorf("%s granted %s no CONNECT on its mirroring endpoint", server, login)
	}
}

// liveEPCleanup drops everything the run created and nothing it found. The
// order matters: a certificate owned by a user blocks dropping the user, and a
// master key cannot go while a certificate it protects is still there.
func liveEPCleanup(t *testing.T, raws map[string]*sql.DB, names, bases []string, before map[string]*gosmo.DatabaseMirroringEndpoint) {
	t.Helper()
	for i, n := range names {
		raw := raws[n]
		if before[n] == nil {
			liveEPExec(t, raw, "IF EXISTS (SELECT 1 FROM sys.endpoints WHERE name = 'Hadr_endpoint') DROP ENDPOINT [Hadr_endpoint]")
		}
		for j, base := range bases {
			if i == j {
				continue
			}
			liveEPExec(t, raw, dropIf("certificates", base+"_Cert", "DROP CERTIFICATE ["+base+"_Cert]"))
			liveEPExec(t, raw, "USE master; IF DATABASE_PRINCIPAL_ID('"+base+"_user') IS NOT NULL DROP USER ["+base+"_user]")
			liveEPExec(t, raw, "IF SUSER_ID('"+base+"_login') IS NOT NULL DROP LOGIN ["+base+"_login]")
		}
		// The instance's own certificate and master key go only if this run
		// created them: an instance that arrived with an endpoint arrived with
		// both, and they are what it authenticates with.
		if before[n] == nil {
			liveEPExec(t, raw, dropIf("certificates", bases[i]+"_Cert", "DROP CERTIFICATE ["+bases[i]+"_Cert]"))
			liveEPExec(t, raw, "USE master; IF EXISTS (SELECT 1 FROM sys.symmetric_keys WHERE name = '##MS_DatabaseMasterKey##') DROP MASTER KEY")
		}
	}
}

// dropIf wraps a DROP in the existence check its statement has no IF EXISTS
// form for.
func dropIf(catalog, name, stmt string) string {
	return fmt.Sprintf("USE master; IF EXISTS (SELECT 1 FROM sys.%s WHERE name = '%s') EXEC('%s')", catalog, name, stmt)
}

func liveEPExec(t *testing.T, raw *sql.DB, stmt string) {
	t.Helper()
	if _, err := raw.Exec(stmt); err != nil {
		t.Errorf("exec %q: %v", stmt, err)
	}
}
