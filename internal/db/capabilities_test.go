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
	"time"

	gosmo "github.com/radix29/gosmo"
)

// An unprobed or failed-probe connection must answer "not denied" to
// everything, or Allows gating would hide the app from a possible sysadmin.
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

type capTestScript struct {
	mu       sync.Mutex
	dbProbes int   // how many HAS_DBACCESS reads happened
	fail     bool  // make the probe fail
	access   int64 // what HAS_DBACCESS answers
	srvFail  bool  // make the server-scope probe fail

	// started, if set, is signalled when a HAS_DBACCESS read begins, which then
	// waits for release.
	started chan struct{}
	release chan struct{}
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

func (c *capTestConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	s := capTestCurrent
	switch {
	case strings.Contains(q, "HAS_DBACCESS"):
		s.mu.Lock()
		s.dbProbes++
		fail, access, started, release := s.fail, s.access, s.started, s.release
		s.mu.Unlock()
		if started != nil {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if fail {
			return nil, errors.New("mssql: connection reset")
		}
		return &capTestRows{cols: 1, rows: [][]driver.Value{{access}}}, nil
	case strings.Contains(q, "IS_SRVROLEMEMBER"):
		s.mu.Lock()
		srvFail := s.srvFail
		s.mu.Unlock()
		if srvFail {
			return nil, errors.New("mssql: connection reset")
		}
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
	// gosmo.NewServer's SERVERPROPERTY read; only the version is used.
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

	// A second database gets its own answer.
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

// A failed probe isn't cached: unknown fails open, so caching would silently
// disable every gate for the session.
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

// The connect-time probe must populate the cache.
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

// The tree must tell "cannot be opened" from "not asked".
func TestAnInaccessibleDatabaseIsReportedAsSuch(t *testing.T) {
	sc := capTestConnection(t, &capTestScript{access: 0})

	c := sc.DatabaseCapabilities(context.Background(), "backup_test")
	if c.Accessible {
		t.Error("Accessible = true for HAS_DBACCESS 0")
	}
}

// A probe from before ClearCapabilityCache must not cache after it; its answer
// may predate the GRANT.
func TestProbeInFlightAcrossAClearIsNotCached(t *testing.T) {
	script := &capTestScript{access: 1, started: make(chan struct{}), release: make(chan struct{})}
	sc := capTestConnection(t, script)
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		sc.DatabaseCapabilities(ctx, "HealthClinic")
		close(done)
	}()
	<-script.started
	sc.ClearCapabilityCache()
	close(script.release)
	<-done

	script.mu.Lock()
	script.started = nil
	script.mu.Unlock()
	sc.DatabaseCapabilities(ctx, "HealthClinic")
	script.mu.Lock()
	defer script.mu.Unlock()
	if script.dbProbes != 2 {
		t.Errorf("HAS_DBACCESS read %d times, want 2 — the pre-clear answer was cached", script.dbProbes)
	}
}

// A failed server re-probe keeps the previous answer instead of failing every
// gate open.
func TestFailedReprobeKeepsPreviousServerCapabilities(t *testing.T) {
	script := &capTestScript{access: 1}
	sc := capTestConnection(t, script)

	script.mu.Lock()
	script.srvFail = true
	script.mu.Unlock()
	sc.ProbeCapabilities()

	if got := sc.Capabilities().Permission("VIEW SERVER STATE"); got != gosmo.CapabilityDenied {
		t.Errorf("VIEW SERVER STATE after a failed re-probe = %v, want the earlier denied", got)
	}
}

// holdProbes makes every HAS_DBACCESS read in s wait for release, and returns
// the channel each read announces on. Cleanup releases and drains, so a failed
// test doesn't leave probes blocked.
func holdProbes(t *testing.T, s *capTestScript) (started <-chan struct{}, release func()) {
	t.Helper()
	st, rel := make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.started, s.release = st, rel
	s.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(rel) }) }
	t.Cleanup(func() {
		release()
		go func() {
			for range st {
			}
		}()
	})
	return st, release
}

// nextProbe waits for the next HAS_DBACCESS read to start.
func nextProbe(t *testing.T, started <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("no probe started: %s", why)
	}
}

// noProbe asserts no further HAS_DBACCESS read starts.
func noProbe(t *testing.T, started <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-started:
		t.Fatalf("a second probe started: %s", why)
	case <-time.After(100 * time.Millisecond):
	}
}

// waitForWaiters blocks until n callers have joined name's in-flight probe.
func waitForWaiters(t *testing.T, sc *ServerConn, name string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sc.mu.Lock()
		got := 0
		if p := sc.dbProbes[name]; p != nil {
			got = p.waiters
		}
		sc.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d callers joined the probe for %s, want %d — each ran its own", got, name, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// R14: callers after the first must wait for its probe, not duplicate the round
// trips.
func TestConcurrentDatabaseProbesShareOneRoundTrip(t *testing.T) {
	script := &capTestScript{access: 1}
	sc := capTestConnection(t, script)
	started, release := holdProbes(t, script)
	ctx := context.Background()

	const callers = 5
	results := make(chan *gosmo.DatabaseCapabilities, callers)
	for range callers {
		go func() { results <- sc.DatabaseCapabilities(ctx, "HealthClinic") }()
	}
	nextProbe(t, started, "the first caller should probe")
	waitForWaiters(t, sc, "HealthClinic", callers-1)
	release()

	for range callers {
		if c := <-results; !c.InRole("db_datareader") {
			t.Error("a caller that waited did not get the probe's answer")
		}
	}
	noProbe(t, started, "the answer is cached now")
	script.mu.Lock()
	defer script.mu.Unlock()
	if script.dbProbes != 1 {
		t.Errorf("HAS_DBACCESS read %d times by %d concurrent callers, want 1", script.dbProbes, callers)
	}
}

// A waiter on an abandoned probe must ask again rather than inherit the
// failure.
func TestAWaiterOutlivesAnAbandonedProbe(t *testing.T) {
	script := &capTestScript{access: 1}
	sc := capTestConnection(t, script)
	started, release := holdProbes(t, script)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leader := make(chan *gosmo.DatabaseCapabilities, 1)
	go func() { leader <- sc.DatabaseCapabilities(leaderCtx, "HealthClinic") }()
	nextProbe(t, started, "the first caller should probe")

	waiter := make(chan *gosmo.DatabaseCapabilities, 1)
	go func() { waiter <- sc.DatabaseCapabilities(context.Background(), "HealthClinic") }()
	waitForWaiters(t, sc, "HealthClinic", 1)

	cancelLeader()
	if c := <-leader; c.InRole("db_datareader") {
		t.Error("the cancelled caller got an answer its probe never received")
	}
	nextProbe(t, started, "the waiter should re-probe after the leader gave up")
	release()
	if c := <-waiter; !c.InRole("db_datareader") {
		t.Error("the waiter inherited the abandoned probe's failure")
	}
	if !sc.HasDatabaseCapabilities("HealthClinic") {
		t.Error("the waiter's own probe was not cached")
	}
}

// A waiter stops at its own context, not the probe's timeout.
func TestAWaiterStopsAtItsOwnContext(t *testing.T) {
	script := &capTestScript{access: 1}
	sc := capTestConnection(t, script)
	started, release := holdProbes(t, script)

	leader := make(chan struct{})
	go func() {
		sc.DatabaseCapabilities(context.Background(), "HealthClinic")
		close(leader)
	}()
	nextProbe(t, started, "the first caller should probe")

	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan *gosmo.DatabaseCapabilities, 1)
	go func() { waiter <- sc.DatabaseCapabilities(ctx, "HealthClinic") }()
	waitForWaiters(t, sc, "HealthClinic", 1)
	cancel()

	select {
	case c := <-waiter:
		if !c.Accessible || c.InRole("db_datareader") {
			t.Error("a cancelled waiter should get the fail-open unknown answer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled waiter stayed blocked on someone else's probe")
	}
	release()
	<-leader
}

// A post-clear caller must not join a pre-clear probe.
func TestACallerAfterAClearDoesNotJoinAPreClearProbe(t *testing.T) {
	script := &capTestScript{access: 1}
	sc := capTestConnection(t, script)
	started, release := holdProbes(t, script)
	ctx := context.Background()

	pre := make(chan struct{})
	go func() {
		sc.DatabaseCapabilities(ctx, "HealthClinic")
		close(pre)
	}()
	nextProbe(t, started, "the first caller should probe")
	sc.ClearCapabilityCache()

	post := make(chan struct{})
	go func() {
		sc.DatabaseCapabilities(ctx, "HealthClinic")
		close(post)
	}()
	nextProbe(t, started, "a post-clear caller should run its own probe")
	release()
	<-pre
	<-post
	if !sc.HasDatabaseCapabilities("HealthClinic") {
		t.Error("the post-clear probe's answer was not cached")
	}
}

// HasDatabaseCapabilities is true only for a real server answer.
func TestHasDatabaseCapabilities(t *testing.T) {
	script := &capTestScript{access: 1, fail: true}
	sc := capTestConnection(t, script)
	ctx := context.Background()

	if sc.HasDatabaseCapabilities("HealthClinic") {
		t.Error("cached before any probe")
	}
	sc.DatabaseCapabilities(ctx, "HealthClinic")
	if sc.HasDatabaseCapabilities("HealthClinic") {
		t.Error("a failed probe reads as cached, so nothing would ever retry it")
	}
	script.mu.Lock()
	script.fail = false
	script.mu.Unlock()
	sc.DatabaseCapabilities(ctx, "HealthClinic")
	if !sc.HasDatabaseCapabilities("HealthClinic") {
		t.Error("not cached after a probe that succeeded")
	}
	if sc.HasDatabaseCapabilities("msdb") {
		t.Error("one database's answer reads as another's")
	}
	sc.ClearCapabilityCache()
	if sc.HasDatabaseCapabilities("HealthClinic") {
		t.Error("still cached after ClearCapabilityCache")
	}
	if (*ServerConn)(nil).HasDatabaseCapabilities("HealthClinic") {
		t.Error("a nil connection reports a cached answer")
	}
}
