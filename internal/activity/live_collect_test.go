//go:build livedb

// Live coverage of every query this package sends (ARCH-4). internal/activity
// issues its own DMV reads rather than going through gosmo, so gosmo's
// TestLiveVersionSweep never sees them, and the scripted driver in
// fakedb_test.go answers whatever it is asked — it proves the scans, never
// that a column, counter name or view exists on a given version.
//
// The failure this exists to catch is quiet: a counter renamed or missing on
// one version reads as 0, not as an error, so each test asserts the reading
// is *there*, not merely that nothing failed. Run it on 13, 14, 17 and MI:
//
//	go test -tags livedb ./internal/activity/ -run TestLive -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
//
// Writes are throwaway: a login, and helper procedures under test-only names,
// all dropped on the way out.
package activity

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"
)

var liveDSN = flag.String("livedb", "", "SQL Server DSN for the live Activity Monitor tests")

func liveDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	if *liveDSN == "" {
		t.Skip("no -livedb DSN given")
	}
	db, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(func() { cancel(); db.Close() })
	return db, ctx
}

// requireCounters fails for every name in want that set does not carry. A
// counter is looked up the way value() looks it up — instance-prefix stripped
// — so a named instance's "MSSQL$INST:" object names are exercised too.
func requireCounters(t *testing.T, set counterSet, want []string) {
	t.Helper()
	have := map[string]counterValue{}
	for k, v := range set {
		have[k.counter] = v
	}
	known := []int{cntrRawGauge, cntrPerSecond, cntrPerSecondAlt, cntrFraction, cntrAverageBulk, cntrBase}
	for _, name := range want {
		v, ok := have[name]
		if !ok {
			t.Errorf("counter %q is not in sys.dm_os_performance_counters here — it would read as 0", name)
			continue
		}
		if !slices.Contains(known, v.typ) {
			t.Errorf("counter %q has cntr_type %d, which value() does not decode — it would read raw", name, v.typ)
		}
	}
}

func TestLiveCollectReadsEverything(t *testing.T) {
	db, ctx := liveDB(t)

	first, err := Collect(ctx, db)
	if err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	time.Sleep(time.Second)
	cur, err := Collect(ctx, db)
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}

	requireCounters(t, cur.Counters, counterNames)
	if len(cur.Waits) == 0 {
		t.Error("no wait types survived the benign filter")
	}
	var masterData, masterLog bool
	for _, f := range cur.Files {
		if f.database == "master" {
			masterData = masterData || !f.isLog
			masterLog = masterLog || f.isLog
		}
	}
	if !masterData || !masterLog {
		t.Errorf("file I/O lacks master's data (%v) or log (%v) file", masterData, masterLog)
	}
	if !slices.ContainsFunc(cur.Memory, func(m MemoryComponent) bool { return m.Name == memBuffer }) {
		t.Errorf("memory composition %v has no buffer pool slice", cur.Memory)
	}
	if cur.Sched.Schedulers == 0 || len(cur.Load) != cur.Sched.Schedulers {
		t.Errorf("schedulers = %d, load factors = %d, want the same non-zero count", cur.Sched.Schedulers, len(cur.Load))
	}
	if cur.Sessions.UserSessions < 1 {
		t.Errorf("user sessions = %v, want at least this one", cur.Sessions.UserSessions)
	}
	if c := cur.CPU; c.SQLPct < 0 || c.SQLPct > 100 || c.OtherPct < 0 || c.SQLPct+c.OtherPct > 100 {
		t.Errorf("CPU split %+v is not a percentage", c)
	}

	s := Derive(first, cur)
	// Collect itself sends eight batches, so the rate cannot be zero if the
	// counter is being read and decoded.
	if s.BatchesSec <= 0 {
		t.Errorf("Batch Requests/sec = %v, want > 0 across two Collects", s.BatchesSec)
	}
	if s.TotalServerMemoryMB <= 0 || s.TargetServerMemMB <= 0 {
		t.Errorf("server memory total %v / target %v MB, want both > 0", s.TotalServerMemoryMB, s.TargetServerMemMB)
	}
	if s.PageLifeExpectancy <= 0 {
		t.Errorf("page life expectancy = %v, want > 0", s.PageLifeExpectancy)
	}
	if s.BufferCacheHitPct <= 0 || s.BufferCacheHitPct > 100 {
		t.Errorf("buffer cache hit = %v%%, want (0, 100]", s.BufferCacheHitPct)
	}
	if s.PlanCacheHitPct < 0 || s.PlanCacheHitPct > 100 {
		t.Errorf("plan cache hit = %v%%, want [0, 100]", s.PlanCacheHitPct)
	}
	t.Logf("batches/s %.1f, PLE %.0f, buffer hit %.1f%%, plan hit %.1f%%, memory %.0f/%.0f MB, %d schedulers, CPU %+v",
		s.BatchesSec, s.PageLifeExpectancy, s.BufferCacheHitPct, s.PlanCacheHitPct,
		s.TotalServerMemoryMB, s.TargetServerMemMB, s.Sched.Schedulers, s.CPU)
}

func TestLiveCollectTempDB(t *testing.T) {
	db, ctx := liveDB(t)

	// A session holding a temp table, so the object and session reads have
	// something of this test's own to find. A pinned connection: a pooled one
	// would be reset, dropping the table, before the reads ran.
	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	var spid int
	if err := holder.QueryRowContext(ctx, "CREATE TABLE #live_activity (c char(8000));"+
		"INSERT #live_activity SELECT TOP (200) 'x' FROM sys.all_objects;"+
		"SELECT @@SPID").Scan(&spid); err != nil {
		t.Fatalf("temp table: %v", err)
	}

	first, err := collectTempDB(ctx, db)
	if err != nil {
		t.Fatalf("first collectTempDB: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	cur, err := collectTempDB(ctx, db)
	if err != nil {
		t.Fatalf("second collectTempDB: %v", err)
	}
	requireCounters(t, cur.counters, tempdbCounterNames)

	s := deriveTempDB(first, cur)
	// The counter half of the tab, read through value() as the tab reads it:
	// requireCounters matches names only, so a broken object-prefix strip on a
	// named instance would still pass it.
	if s.ActiveTempTables < 1 {
		t.Errorf("Active Temp Tables = %v, want at least this test's #live_activity", s.ActiveTempTables)
	}
	if s.Space.TotalMB <= 0 {
		t.Errorf("tempdb total = %v MB, want > 0", s.Space.TotalMB)
	}
	if len(s.DataFiles()) == 0 || !slices.ContainsFunc(s.Files, func(f TempDBFile) bool { return f.Type == "LOG" }) {
		t.Errorf("tempdb files %+v, want at least one ROWS and one LOG", s.Files)
	}
	if s.Cores <= 0 {
		t.Errorf("cores = %d", s.Cores)
	}
	if s.Objects[TempDBUserTemp].Count < 1 {
		t.Errorf("local temp tables = %+v, want this test's #live_activity", s.Objects[TempDBUserTemp])
	}
	if !slices.ContainsFunc(s.Sessions, func(x TempDBSession) bool { return x.SessionID == spid && x.UserMB > 0 }) {
		t.Errorf("session %d holding ~1.6 MB of temp table is not in the session list %+v", spid, s.Sessions)
	}
	t.Logf("tempdb %.0f MB (%.0f free), %d files, %d cores, %d sessions holding space",
		s.Space.TotalMB, s.Space.FreeMB, len(s.Files), s.Cores, len(s.Sessions))
}

// withLogin creates a throwaway SQL login and returns a pool connected as it.
// The login is dropped at cleanup, after the pool is closed.
func withLogin(t *testing.T, ctx context.Context, admin *sql.DB) (*sql.DB, string) {
	t.Helper()
	name := fmt.Sprintf("gossms_live_am_%d", time.Now().UnixNano()%1_000_000_000)
	const password = "Live!Activity#2026x"
	if _, err := admin.ExecContext(ctx, "CREATE LOGIN ["+name+"] WITH PASSWORD = N'"+password+"', CHECK_POLICY = OFF"); err != nil {
		t.Fatalf("create login: %v", err)
	}
	u, err := url.Parse(*liveDSN)
	if err != nil {
		t.Fatalf("the -livedb DSN must be a sqlserver:// URL for this test: %v", err)
	}
	u.User = url.UserPassword(name, password)
	db, err := sql.Open("sqlserver", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		// A pooled session of the login's may still be open server-side, and
		// DROP LOGIN refuses a logged-in login; kill any first.
		kill := `DECLARE @s nvarchar(max) = N'';
SELECT @s += N'KILL ' + CAST(session_id AS nvarchar(10)) + N';' FROM sys.dm_exec_sessions WHERE login_name = @p1;
EXEC (@s);`
		if _, err := admin.ExecContext(context.Background(), kill, name); err != nil {
			t.Logf("kill %s's sessions: %v", name, err)
		}
		if _, err := admin.ExecContext(context.Background(), "DROP LOGIN ["+name+"]"); err != nil {
			t.Errorf("drop login %s: %v — drop it by hand", name, err)
		}
	})
	return db, name
}

// The collectors' prologue asks for VIEW SERVER STATE and nothing else, so
// that one grant must be enough for every read the Sample, History and TempDB
// tabs make — and without it the collector must stop with ErrNoPermission, not
// draw an idle server.
func TestLiveViewServerStateIsTheWholeGate(t *testing.T) {
	admin, ctx := liveDB(t)
	db, name := withLogin(t, ctx, admin)

	var got []error
	c := NewCollector(db, func(Sample) { t.Error("a sample arrived without VIEW SERVER STATE") },
		func(err error) { got = append(got, err) })
	c.Run(ctx, time.Second) // returns once the prologue refuses
	if len(got) != 1 || !errors.Is(got[0], ErrNoPermission) {
		t.Fatalf("errors without the permission = %v, want exactly ErrNoPermission", got)
	}

	if _, err := admin.ExecContext(ctx, "GRANT VIEW SERVER STATE TO ["+name+"]"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	// Permissions are cached per session; a fresh pool sees the grant.
	db.Close()
	u, _ := url.Parse(*liveDSN)
	u.User = url.UserPassword(name, "Live!Activity#2026x")
	db, err := sql.Open("sqlserver", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if ok, err := HasViewServerState(ctx, db); err != nil || !ok {
		t.Fatalf("HasViewServerState after the grant = %v, %v", ok, err)
	}
	snap, err := Collect(ctx, db)
	if err != nil {
		t.Fatalf("Collect as a VIEW SERVER STATE-only login: %v", err)
	}
	requireCounters(t, snap.Counters, counterNames)
	if len(snap.Files) == 0 || len(snap.Memory) == 0 {
		t.Errorf("file I/O (%d) or memory (%d) came back empty for the lesser login", len(snap.Files), len(snap.Memory))
	}
	td, err := collectTempDB(ctx, db)
	if err != nil {
		t.Fatalf("collectTempDB as a VIEW SERVER STATE-only login: %v", err)
	}
	if td.sample.Space.TotalMB <= 0 || len(td.sample.Files) == 0 {
		t.Errorf("tempdb space %v MB / %d files for the lesser login, want both non-zero", td.sample.Space.TotalMB, len(td.sample.Files))
	}
}

// drainAll reads every result set a batch returns, failing on the first error
// — a procedure's runtime error can arrive after its first set.
func drainAll(ctx context.Context, db *sql.DB, batch string) (sets int, err error) {
	rows, err := db.QueryContext(ctx, batch)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for {
		sets++
		for rows.Next() {
		}
		if !rows.NextResultSet() {
			break
		}
	}
	return sets, rows.Err()
}

// The Block and Sessions tabs install their procedure, look it up, and run
// it. Both scripts are exercised under test-only names so a copy the author
// installed by hand is neither found nor replaced.
func TestLiveProcsInstallFindAndRun(t *testing.T) {
	db, ctx := liveDB(t)

	for _, real := range []*Proc{BlockProc, WhoIsActiveProc} {
		p := &Proc{
			MasterName: "sp_gossms_live_" + strings.TrimPrefix(real.MasterName, "sp_"),
			TempDBName: "usp_gossms_live_" + strings.TrimPrefix(real.TempDBName, "usp_"),
			script:     real.script,
		}
		t.Run(real.MasterName, func(t *testing.T) {
			drop := func(l ProcLocation) {
				stmt := "exec " + l.Database() + ".sys.sp_executesql N'DROP PROCEDURE IF EXISTS dbo." + p.Name(l) + "'"
				if _, err := db.ExecContext(context.Background(), stmt); err != nil {
					t.Errorf("drop %s: %v — drop it by hand", p.Qualified(l), err)
				}
			}
			defer drop(ProcTempDB)
			defer drop(ProcMaster)

			if loc, err := p.Find(ctx, db); err != nil || loc != ProcNone {
				t.Fatalf("Find before install = %v, %v, want ProcNone", loc, err)
			}
			for _, l := range []ProcLocation{ProcTempDB, ProcMaster} {
				if err := p.Install(ctx, db, l); err != nil {
					t.Fatalf("Install %v: %v", l.Database(), err)
				}
				if loc, err := p.Find(ctx, db); err != nil || loc != l {
					t.Fatalf("Find after installing in %s = %v, %v", l.Database(), loc, err)
				}
				sets, err := drainAll(ctx, db, p.Exec(l))
				if err != nil {
					t.Fatalf("%s: %v", p.Exec(l), err)
				}
				if sets < 1 {
					t.Errorf("%s returned no result set", p.Exec(l))
				}
			}
			// Install leaves the pooled connection's database as it found it.
			var dbName string
			if err := db.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&dbName); err != nil {
				t.Fatal(err)
			}
			if dbName == "tempdb" {
				t.Errorf("a pooled connection was left in %s after Install", dbName)
			}
		})
	}
}
