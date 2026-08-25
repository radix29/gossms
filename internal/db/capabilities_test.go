package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// TestCapabilitiesFailOpenWithoutAProbe pins the rule the whole layer rests
// on: a connection that was never probed — or whose probe failed — must answer
// "not denied" to everything. Gating on Allows would otherwise hide the
// application from a login that may well be a sysadmin.
func TestCapabilitiesFailOpenWithoutAProbe(t *testing.T) {
	for name, sc := range map[string]*ServerConn{
		"nil":        nil,
		"zero value": {},
	} {
		c := sc.Capabilities()
		if c == nil {
			t.Fatalf("%s: Capabilities() = nil, want a usable value", name)
		}
		if !c.Allows("CONTROL SERVER") {
			t.Errorf("%s: Allows = false, want the fail-open answer", name)
		}
		if c.Has("CONTROL SERVER") || c.IsSysadmin() {
			t.Errorf("%s: reports a right it never asked about", name)
		}

		d := sc.DatabaseCapabilities(context.Background(), "anything")
		if d == nil {
			t.Fatalf("%s: DatabaseCapabilities() = nil, want a usable value", name)
		}
		if !d.Accessible {
			t.Errorf("%s: Accessible = false without an answer from the server — "+
				"that is the signal for a database that cannot be opened, not for a probe that did not run", name)
		}
		if !d.Allows("ALTER") {
			t.Errorf("%s: database Allows = false, want the fail-open answer", name)
		}
	}
}

// -- a scripted server, to exercise the cache --------------------------------

type capTestScript struct {
	mu       sync.Mutex
	dbProbes int   // how many HAS_DBACCESS reads happened
	fail     bool  // make the probe fail
	access   int64 // what HAS_DBACCESS answers
}

var capTestCurrent *capTestScript

type capTestDriver struct{}

func (capTestDriver) Open(string) (driver.Conn, error) { return &capTestConn{}, nil }

type capTestConn struct{}

func (c *capTestConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *capTestConn) Close() error                        { return nil }
func (c *capTestConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *capTestConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.ResultNoRows, nil
}

func (c *capTestConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	s := capTestCurrent
	switch {
	case strings.Contains(q, "HAS_DBACCESS"):
		s.mu.Lock()
		s.dbProbes++
		fail, access := s.fail, s.access
		s.mu.Unlock()
		if fail {
			return nil, errors.New("mssql: connection reset")
		}
		return &capTestRows{cols: 1, rows: [][]driver.Value{{access}}}, nil
	case strings.Contains(q, "IS_SRVROLEMEMBER"):
		return &capTestRows{cols: 3, rows: [][]driver.Value{
			{"R", "sysadmin", int64(0)},
			{"P", "VIEW SERVER STATE", int64(0)},
		}}, nil
	case strings.Contains(q, "IS_ROLEMEMBER"):
		return &capTestRows{cols: 3, rows: [][]driver.Value{
			{"R", "db_datareader", int64(1)},
			{"P", "SELECT", int64(1)},
		}}, nil
	case strings.Contains(q, "dm_os_sys_info"):
		return &capTestRows{cols: 2, rows: [][]driver.Value{{int64(1), int64(1)}}}, nil
	}
	// gosmo.NewServer's SERVERPROPERTY half; only the version is read back here.
	return &capTestRows{cols: 13, rows: [][]driver.Value{{
		"FAKE", "Developer Edition", "16.0.4085.2", "RTM", "SQL_Latin1_General_CP1_CI_AS",
		int64(0), int64(0), int64(0), int64(3), "Microsoft SQL Server 2022 ... on Linux",
		"/data", "/log", "/backup",
	}}}, nil
}

type capTestRows struct {
	cols int
	rows [][]driver.Value
	i    int
}

func (r *capTestRows) Columns() []string { return make([]string, r.cols) }
func (r *capTestRows) Close() error      { return nil }
func (r *capTestRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

func init() { sql.Register("captestdb", capTestDriver{}) }

func capTestConnection(t *testing.T, s *capTestScript) *ServerConn {
	t.Helper()
	capTestCurrent = s
	pool, err := sql.Open("captestdb", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	srv, err := gosmo.NewServer(context.Background(), pool)
	if err != nil {
		t.Fatalf("gosmo.NewServer: %v", err)
	}
	sc := &ServerConn{Server: srv}
	sc.ProbeCapabilities()
	return sc
}

func TestDatabaseCapabilitiesAreProbedOnceAndCached(t *testing.T) {
	script := &capTestScript{access: 1}
	sc := capTestConnection(t, script)
	ctx := context.Background()

	first := sc.DatabaseCapabilities(ctx, "HealthClinic")
	if !first.InRole("db_datareader") {
		t.Fatal("first probe did not read the database roles")
	}
	for range 4 {
		sc.DatabaseCapabilities(ctx, "HealthClinic")
	}
	if script.dbProbes != 1 {
		t.Errorf("HAS_DBACCESS read %d times, want 1 — the answer is cached", script.dbProbes)
	}

	// A second database is its own answer, not the first one's.
	sc.DatabaseCapabilities(ctx, "msdb")
	if script.dbProbes != 2 {
		t.Errorf("HAS_DBACCESS read %d times after a second database, want 2", script.dbProbes)
	}

	sc.ClearCapabilityCache()
	sc.DatabaseCapabilities(ctx, "HealthClinic")
	if script.dbProbes != 3 {
		t.Errorf("HAS_DBACCESS read %d times after ClearCapabilityCache, want 3", script.dbProbes)
	}
}

// TestAFailedDatabaseProbeIsNotCached is the other half. Caching a failure
// would leave a database answering "nothing known" for the rest of the
// session over one dropped connection — and because unknown fails open, that
// silently disables every gate the answer was meant to drive.
func TestAFailedDatabaseProbeIsNotCached(t *testing.T) {
	script := &capTestScript{access: 1, fail: true}
	sc := capTestConnection(t, script)
	ctx := context.Background()

	c := sc.DatabaseCapabilities(ctx, "HealthClinic")
	if !c.Accessible {
		t.Error("a failed probe reported the database as inaccessible")
	}
	if c.InRole("db_datareader") {
		t.Error("a failed probe reported a role it never read")
	}

	script.mu.Lock()
	script.fail = false
	script.mu.Unlock()

	if got := sc.DatabaseCapabilities(ctx, "HealthClinic"); !got.InRole("db_datareader") {
		t.Error("the failure was cached: the retry did not reach the server")
	}
}

// TestServerCapabilitiesAreProbedAtConnect pins that the connect-time probe
// actually populates the cache, rather than every caller silently getting the
// fail-open empty value.
func TestServerCapabilitiesAreProbedAtConnect(t *testing.T) {
	sc := capTestConnection(t, &capTestScript{access: 1})

	c := sc.Capabilities()
	if c.Permission("VIEW SERVER STATE") != gosmo.CapabilityDenied {
		t.Errorf("VIEW SERVER STATE = %v, want denied — the probe's answer was not kept",
			c.Permission("VIEW SERVER STATE"))
	}
	if c.Allows("VIEW SERVER STATE") {
		t.Error("Allows = true for an answer the server actually denied")
	}
}

// TestAnInaccessibleDatabaseIsReportedAsSuch is the predicate the tree needs:
// it must be able to tell "cannot be opened" from "not asked".
func TestAnInaccessibleDatabaseIsReportedAsSuch(t *testing.T) {
	sc := capTestConnection(t, &capTestScript{access: 0})

	c := sc.DatabaseCapabilities(context.Background(), "backup_test")
	if c.Accessible {
		t.Error("Accessible = true for HAS_DBACCESS 0")
	}
}
