package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// monitorCatalog serves sessions to ActiveSessions, recording the scope;
// noVSS answers HasViewServerState false, vssErr fails it.
type monitorCatalog struct {
	Catalog
	sessions []*gosmo.ActiveSession
	noVSS    bool
	vssErr   error

	reads    int
	gotScope gosmo.SessionScope

	// monitor waits: each WaitStats call serves the next of waits and each
	// StartTime the next of uptimes, from start.
	waits     [][]gosmo.WaitStat
	uptimes   []time.Duration
	start     time.Time
	waitReads int
	upReads   int

	// monitor top-queries / plan cached: TopQueries serves queries (or
	// fails with topErr), CachedPlan plan (or planErr); each records what
	// it was asked.
	queries   []*gosmo.CachedQueryStat
	topErr    error
	gotTopOps *gosmo.TopQueriesOptions
	plan      string
	planErr   error
	gotPlan   string // "0x<handle> start end"

	// monitor querystore: see qsFake.
	qs *qsFake

	// monitor deadlocks: see dlFake.
	dl *dlFake
}

func (c *monitorCatalog) TopQueries(_ context.Context, opts gosmo.TopQueriesOptions) ([]*gosmo.CachedQueryStat, error) {
	c.gotTopOps = &opts
	return c.queries, c.topErr
}

func (c *monitorCatalog) CachedPlan(_ context.Context, h []byte, start, end int) (string, error) {
	c.gotPlan = fmt.Sprintf("0x%X %d %d", h, start, end)
	return c.plan, c.planErr
}

func (c *monitorCatalog) WaitStats(context.Context) ([]gosmo.WaitStat, error) {
	c.waitReads++
	return c.waits[c.waitReads-1], nil
}

func (c *monitorCatalog) StartTime(context.Context) (time.Time, time.Duration, error) {
	c.upReads++
	return c.start, c.uptimes[c.upReads-1], nil
}

func (c *monitorCatalog) HasViewServerState(context.Context) (bool, error) { return !c.noVSS, c.vssErr }

func (c *monitorCatalog) ActiveSessions(_ context.Context, scope gosmo.SessionScope) ([]*gosmo.ActiveSession, error) {
	c.reads++
	c.gotScope = scope
	return c.sessions, nil
}

func runMonitor(t *testing.T, cat *monitorCatalog, argv ...string) (*output.Result, error) {
	t.Helper()
	return runGroup(t, cat, "monitor", argv...)
}

// runGroup runs group's command argv[0] with argv[1:] against cat,
// connecting with a fixed server and Windows auth.
func runGroup(t *testing.T, cat *monitorCatalog, group string, argv ...string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
	}
	// --force is the CLI's global option (tree.go), which --plan-file and
	// --deadlock-file follow.
	tree := &spec.Tree{Commands: slices.Concat(MonitorCommands(d), PlanCommands(d)),
		Globals: []spec.Option{{Name: optForce, Type: spec.TypeBool}}}
	inv, perr := args.Parse(tree, append(append([]string{group}, argv[0], "--server", "srv", "--auth", "windows"), argv[1:]...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	return inv.Command.Run(context.Background(), inv)
}

// sess is a session spid blocked by blocker (0 for none).
func sess(spid, blocker int) *gosmo.ActiveSession {
	return &gosmo.ActiveSession{SessionID: spid, BlockingSessionID: blocker, LoginName: fmt.Sprint("login", spid)}
}

// chainShape is rows as "head/depth/spid[*]" strings, * on a cycle member.
func chainShape(rows []chainRow) []string {
	var out []string
	for _, r := range rows {
		s := fmt.Sprintf("%d/%d/%d", r.Head, r.Depth, r.Session.SessionID)
		if r.Cycle {
			s += "*"
		}
		out = append(out, s)
	}
	return out
}

func TestBlockingChains(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions []*gosmo.ActiveSession
		want     []string
	}{
		{"no blocking", []*gosmo.ActiveSession{sess(51, 0), sess(52, 0)}, nil},
		{"single chain", []*gosmo.ActiveSession{sess(53, 52), sess(51, 0), sess(52, 51), sess(60, 0)},
			[]string{"51/0/51", "51/1/52", "51/2/53"}},
		{"fan-out, by spid at each level", []*gosmo.ActiveSession{sess(51, 0), sess(70, 51), sess(55, 51), sess(56, 55)},
			[]string{"51/0/51", "51/1/55", "51/2/56", "51/1/70"}},
		{"two chains by head spid", []*gosmo.ActiveSession{sess(80, 0), sess(81, 80), sess(51, 0), sess(52, 51)},
			[]string{"51/0/51", "51/1/52", "80/0/80", "80/1/81"}},
		{"blocker not read heads its own chain", []*gosmo.ActiveSession{sess(52, 99), sess(53, 52), sess(54, -2)},
			[]string{"52/0/52", "52/1/53", "54/0/54"}},
		{"cycle, reported once from its lowest spid", []*gosmo.ActiveSession{sess(62, 61), sess(61, 63), sess(63, 62)},
			[]string{"61/0/61*", "61/1/62*", "61/2/63*"}},
		{"tree hanging off a cycle", []*gosmo.ActiveSession{sess(70, 71), sess(71, 70), sess(72, 71), sess(73, 72), sess(50, 0)},
			[]string{"70/0/70*", "70/1/71*", "70/2/72", "70/3/73"}},
		{"cycle entered from outside its lowest spid", []*gosmo.ActiveSession{sess(90, 91), sess(91, 92), sess(92, 91)},
			[]string{"91/0/91*", "91/1/90", "91/1/92*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chainShape(blockingChains(tc.sessions)); !slices.Equal(got, tc.want) {
				t.Errorf("chains = %q, want %q", got, tc.want)
			}
		})
	}
}

// An idle head blocker shows the batch its connection sent last; a parallel
// request's blocked worker is a waiter like any other, its wait the worker's.
func TestMonitorBlockingIdleHeadAndParallelWorker(t *testing.T) {
	head := sess(51, 0)
	head.OpenTransactionCount = 1
	head.RecentText = "BEGIN TRAN; UPDATE t SET v = 1"
	waiter := sess(52, 51)
	waiter.Command, waiter.CommandText = "SELECT", "SELECT * FROM t"
	waiter.WaitType, waiter.WaitTimeMS, waiter.WaitResource = "LCK_M_S", 1500, "KEY: 5:72057594043236352 (8194443284a0)"
	par := sess(53, 51)
	par.Command, par.CommandText, par.RecentText = "SELECT", "SELECT COUNT(*) FROM t OPTION (MAXDOP 4)", "older batch"
	par.WaitType, par.WaitTimeMS, par.WaitResource = "LCK_M_S", 900, "keylock hobtid=72057594043236352 dbid=5 mode=X"
	cat := &monitorCatalog{sessions: []*gosmo.ActiveSession{par, sess(60, 0), waiter, head}}

	res, err := runMonitor(t, cat, "blocking")
	if err != nil {
		t.Fatal(err)
	}
	if cat.gotScope != gosmo.AllSessions {
		t.Errorf("scope = %v, want AllSessions", cat.gotScope)
	}
	set := res.Sets[0]
	col := func(row int, name string) string {
		i := slices.IndexFunc(set.Columns, func(c output.Column) bool { return c.Name == name })
		if i < 0 {
			t.Fatalf("no column %s", name)
		}
		if c := set.Rows[row][i]; c != nil {
			return *c
		}
		return "NULL"
	}
	if len(set.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (the head and its two waiters)", len(set.Rows))
	}
	for i, want := range []string{
		"51 0 0 51 NULL NULL 1 BEGIN TRAN; UPDATE t SET v = 1",
		"51 1 0 52 51 LCK_M_S 0 SELECT * FROM t",
		"51 1 0 53 51 LCK_M_S 0 SELECT COUNT(*) FROM t OPTION (MAXDOP 4)",
	} {
		got := strings.Join([]string{col(i, "head"), col(i, "depth"), col(i, "cycle"), col(i, "spid"),
			col(i, "blocked_by"), col(i, "wait_type"), col(i, "open_tran"), col(i, "text")}, " ")
		if got != want {
			t.Errorf("row %d = %q, want %q", i, got, want)
		}
	}
	if got := col(2, "wait_resource"); got != par.WaitResource {
		t.Errorf("parallel worker's wait_resource = %q, want the worker's %q", got, par.WaitResource)
	}
}

func TestMonitorBlockingNoneIsEmpty(t *testing.T) {
	res, err := runMonitor(t, &monitorCatalog{sessions: []*gosmo.ActiveSession{sess(51, 0)}}, "blocking")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sets) != 1 || res.Sets[0].Name != "blocking" || len(res.Sets[0].Rows) != 0 {
		t.Errorf("sets = %+v, want one empty blocking set", res.Sets)
	}
}

func TestMonitorSessionsFilters(t *testing.T) {
	mk := func(spid int, cmd, dbName, login, program string) *gosmo.ActiveSession {
		return &gosmo.ActiveSession{SessionID: spid, Command: cmd, DatabaseName: dbName, LoginName: login, ProgramName: program}
	}
	all := []*gosmo.ActiveSession{
		mk(51, "SELECT", "Sales", "app", "web"),
		mk(52, "", "Sales", "app", "web"),
		mk(53, "UPDATE", "master", "App", "WEB"),
		mk(54, "SELECT", "sales", "dba", "ssms"),
	}
	for _, tc := range []struct {
		argv []string
		want []string
	}{
		{nil, []string{"51", "52", "53", "54"}},
		{[]string{"--active"}, []string{"51", "53", "54"}},
		{[]string{"--session-database", "SALES"}, []string{"51", "52", "54"}},
		{[]string{"--login", "app", "--program", "Web"}, []string{"51", "52", "53"}},
		{[]string{"--active", "--session-database", "sales", "--login", "APP"}, []string{"51"}},
	} {
		cat := &monitorCatalog{sessions: all}
		res, err := runMonitor(t, cat, append([]string{"sessions"}, tc.argv...)...)
		if err != nil {
			t.Fatalf("%q: %v", tc.argv, err)
		}
		var got []string
		for _, r := range res.Sets[0].Rows {
			got = append(got, *r[0])
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q: spids = %q, want %q", tc.argv, got, tc.want)
		}
		if cat.gotScope != gosmo.UserSessionsOnly {
			t.Errorf("%q: scope = %v, want UserSessionsOnly", tc.argv, cat.gotScope)
		}
	}
	cat := &monitorCatalog{sessions: all}
	if _, err := runMonitor(t, cat, "sessions", "--include-system"); err != nil {
		t.Fatal(err)
	}
	if cat.gotScope != gosmo.AllSessions {
		t.Errorf("--include-system: scope = %v, want AllSessions", cat.gotScope)
	}
}

// An idle session's row shows its connection's last batch; a running one's
// its request's, and NULL for what it lacks.
func TestMonitorSessionsRow(t *testing.T) {
	idle := &gosmo.ActiveSession{SessionID: 51, LoginName: "app", Status: "sleeping", OpenTransactionCount: 2,
		RecentText: "BEGIN TRAN", CPUTime: 7, Reads: 1, Writes: 2, LogicalReads: 3, TotalElapsedMS: 40}
	res, err := runMonitor(t, &monitorCatalog{sessions: []*gosmo.ActiveSession{idle}}, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	var got []string
	for _, c := range set.Rows[0] {
		if c == nil {
			got = append(got, "NULL")
		} else {
			got = append(got, *c)
		}
	}
	want := []string{"51", "app", "", "", "NULL", "sleeping", "NULL", "2", "7", "1", "2", "3", "40", "NULL", "NULL", "0", "NULL", "BEGIN TRAN"}
	if !slices.Equal(got, want) {
		t.Errorf("row = %q\nwant  %q", got, want)
	}
	if set.Name != "sessions" || len(set.Columns) != len(want) {
		t.Errorf("set %q has %d columns, want sessions with %d", set.Name, len(set.Columns), len(want))
	}
}

// Without VIEW SERVER STATE both commands are exit 9 and read no session: the
// read would come back empty, not refused.
func TestMonitorNeedsViewServerState(t *testing.T) {
	for _, c := range []string{"sessions", "blocking", "waits", "top-queries"} {
		cat := &monitorCatalog{noVSS: true, sessions: []*gosmo.ActiveSession{sess(51, 0)}}
		_, err := runMonitor(t, cat, c)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied {
			t.Errorf("%s: err = %v, want PERMISSION_DENIED", c, err)
		}
		if cat.reads+cat.waitReads+cat.upReads != 0 || cat.gotTopOps != nil {
			t.Errorf("%s: read without the permission", c)
		}
	}
	cat := &monitorCatalog{vssErr: errors.New("connection reset")}
	if _, err := runMonitor(t, cat, "blocking"); err == nil || cat.reads != 0 {
		t.Errorf("a failed permission check: err = %v, reads = %d", err, cat.reads)
	}
}

// cells is a set's rows as space-joined cells, NULL for a null one.
func cells(set output.Set) []string {
	var out []string
	for _, r := range set.Rows {
		var row []string
		for _, c := range r {
			if c == nil {
				row = append(row, "NULL")
			} else {
				row = append(row, *c)
			}
		}
		out = append(out, strings.Join(row, " "))
	}
	return out
}

func field(res *output.Result, key string) string {
	for _, f := range res.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return "<missing>"
}

func ws(waitType string, tasks, ms, signal int64) gosmo.WaitStat {
	return gosmo.WaitStat{WaitType: waitType, WaitingTasks: tasks, WaitTimeMs: ms, SignalWaitTimeMs: signal}
}

var waitStart = time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)

// The totals since start: benign and empty wait types left out, most wait
// time first, shares of everything counted, categories summed.
func TestMonitorWaitsCumulative(t *testing.T) {
	cat := &monitorCatalog{start: waitStart, uptimes: []time.Duration{time.Hour}, waits: [][]gosmo.WaitStat{{
		ws("LAZYWRITER_SLEEP", 10, 900000, 0),
		ws("XE_TIMER_EVENT", 10, 800000, 0),
		ws("LCK_M_X", 4, 600, 0),
		ws("PAGEIOLATCH_SH", 100, 300, 10),
		ws("SOS_SCHEDULER_YIELD", 50, 100, 100),
		ws("LCK_M_S", 0, 0, 0),
	}}}
	res, err := runMonitor(t, cat, "waits")
	if err != nil {
		t.Fatal(err)
	}
	if got := field(res, "since"); got != "2026-10-30 08:00:00.000" {
		t.Errorf("since = %q", got)
	}
	if got := field(res, "elapsedMs"); got != "3600000" {
		t.Errorf("elapsedMs = %q", got)
	}
	if len(res.Sets) != 2 || res.Sets[0].Name != "waits" || res.Sets[1].Name != "categories" {
		t.Fatalf("sets = %+v", res.Sets)
	}
	want := []string{
		"LCK_M_X Locking 4 600 0 600 60.00 150.00",
		"PAGEIOLATCH_SH Disk IO 100 300 10 290 30.00 3.00",
		"SOS_SCHEDULER_YIELD CPU 50 100 100 0 10.00 2.00",
	}
	if got := cells(res.Sets[0]); !slices.Equal(got, want) {
		t.Errorf("waits =\n%q\nwant\n%q", got, want)
	}
	wantCats := []string{
		"Locking 4 600 0 600 60.00",
		"Disk IO 100 300 10 290 30.00",
		"CPU 50 100 100 0 10.00",
		"Latches 0 0 0 0 0.00",
		"Memory 0 0 0 0 0.00",
		"Log 0 0 0 0 0.00",
		"Network 0 0 0 0 0.00",
		"Other 0 0 0 0 0.00",
	}
	if got := cells(res.Sets[1]); !slices.Equal(got, wantCats) {
		t.Errorf("categories =\n%q\nwant\n%q", got, wantCats)
	}
	if cat.waitReads != 1 {
		t.Errorf("read wait stats %d times, want 1", cat.waitReads)
	}
}

// --all keeps the benign waits; --top cuts the waits set but not the shares
// or the categories.
func TestMonitorWaitsAllAndTop(t *testing.T) {
	stats := []gosmo.WaitStat{ws("LAZYWRITER_SLEEP", 1, 700, 0), ws("LCK_M_X", 1, 200, 0), ws("WRITELOG", 1, 100, 0)}
	cat := &monitorCatalog{start: waitStart, uptimes: []time.Duration{time.Hour}, waits: [][]gosmo.WaitStat{stats}}
	res, err := runMonitor(t, cat, "waits", "--all", "--top", "1")
	if err != nil {
		t.Fatal(err)
	}
	if got := cells(res.Sets[0]); !slices.Equal(got, []string{"LAZYWRITER_SLEEP Other 1 700 0 700 70.00 700.00"}) {
		t.Errorf("waits = %q", got)
	}
	if got := cells(res.Sets[1])[0]; got != "Other 1 700 0 700 70.00" {
		t.Errorf("first category = %q", got)
	}
}

// --interval reads twice and reports the difference: since is the server's
// time at the first read, elapsedMs the server time between; a wait type
// new in the second read counts from zero, one whose totals fell is left
// out with a warning; signal time is clamped to the wait time it is part of.
func TestMonitorWaitsInterval(t *testing.T) {
	var slept time.Duration
	defer func(f func(context.Context, time.Duration) error) { sleepCtx = f }(sleepCtx)
	sleepCtx = func(_ context.Context, d time.Duration) error { slept = d; return nil }

	cat := &monitorCatalog{start: waitStart, uptimes: []time.Duration{time.Hour, time.Hour + 5020*time.Millisecond},
		waits: [][]gosmo.WaitStat{
			{ws("LCK_M_X", 4, 1000, 0), ws("WRITELOG", 10, 500, 5), ws("CXPACKET", 9, 900, 0)},
			{ws("LCK_M_X", 6, 1300, 0), ws("WRITELOG", 10, 500, 5), ws("CXPACKET", 1, 10, 0), ws("PAGEIOLATCH_EX", 2, 100, 0),
				ws("LATCH_EX", 1, 0, 1)},
		}}
	res, err := runMonitor(t, cat, "waits", "--interval", "5")
	if err != nil {
		t.Fatal(err)
	}
	if slept != 5*time.Second {
		t.Errorf("slept %v, want 5s", slept)
	}
	if got := field(res, "since"); got != "2026-10-30 09:00:00.000" {
		t.Errorf("since = %q, want the first read's server time", got)
	}
	if got := field(res, "elapsedMs"); got != "5020" {
		t.Errorf("elapsedMs = %q", got)
	}
	want := []string{
		"LCK_M_X Locking 2 300 0 300 75.00 150.00",
		"PAGEIOLATCH_EX Disk IO 2 100 0 100 25.00 50.00",
		"LATCH_EX Latches 1 0 0 0 0.00 0.00",
	}
	if got := cells(res.Sets[0]); !slices.Equal(got, want) {
		t.Errorf("waits =\n%q\nwant\n%q", got, want)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "cleared") {
		t.Errorf("warnings = %q, want one about cleared statistics", res.Warnings)
	}
}

// Nothing counted: an empty waits set, every category 0 with a NULL share.
func TestMonitorWaitsNone(t *testing.T) {
	cat := &monitorCatalog{start: waitStart, uptimes: []time.Duration{time.Hour}, waits: [][]gosmo.WaitStat{{ws("LAZYWRITER_SLEEP", 1, 7, 0)}}}
	res, err := runMonitor(t, cat, "waits")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sets[0].Rows) != 0 {
		t.Errorf("waits = %q, want none", cells(res.Sets[0]))
	}
	if got := cells(res.Sets[1])[0]; got != "CPU 0 0 0 0 NULL" {
		t.Errorf("first category = %q", got)
	}
}

// Ctrl+C during the interval ends the command without the second read.
func TestMonitorWaitsIntervalCancelled(t *testing.T) {
	defer func(f func(context.Context, time.Duration) error) { sleepCtx = f }(sleepCtx)
	sleepCtx = func(context.Context, time.Duration) error { return context.Canceled }
	cat := &monitorCatalog{start: waitStart, uptimes: []time.Duration{time.Hour}, waits: [][]gosmo.WaitStat{{}}}
	if _, err := runMonitor(t, cat, "waits", "--interval", "3"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if cat.waitReads != 1 {
		t.Errorf("read wait stats %d times, want 1", cat.waitReads)
	}
}

func TestMonitorWaitsUsage(t *testing.T) {
	for _, argv := range [][]string{{"--top", "-1"}, {"--interval", "-1"}, {"--interval", "301"}} {
		cat := &monitorCatalog{}
		_, err := runMonitor(t, cat, append([]string{"waits"}, argv...)...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if cat.waitReads != 0 {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}
