package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

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
}

func (c *monitorCatalog) HasViewServerState(context.Context) (bool, error) { return !c.noVSS, c.vssErr }

func (c *monitorCatalog) ActiveSessions(_ context.Context, scope gosmo.SessionScope) ([]*gosmo.ActiveSession, error) {
	c.reads++
	c.gotScope = scope
	return c.sessions, nil
}

func runMonitor(t *testing.T, cat *monitorCatalog, argv ...string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
	}
	tree := &spec.Tree{Commands: MonitorCommands(d)}
	inv, perr := args.Parse(tree, append(append([]string{"monitor"}, argv[0], "--server", "srv", "--auth", "windows"), argv[1:]...), args.Options{})
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
	for _, c := range []string{"sessions", "blocking"} {
		cat := &monitorCatalog{noVSS: true, sessions: []*gosmo.ActiveSession{sess(51, 0)}}
		_, err := runMonitor(t, cat, c)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied {
			t.Errorf("%s: err = %v, want PERMISSION_DENIED", c, err)
		}
		if cat.reads != 0 {
			t.Errorf("%s: read sessions %d times without the permission", c, cat.reads)
		}
	}
	cat := &monitorCatalog{vssErr: errors.New("connection reset")}
	if _, err := runMonitor(t, cat, "blocking"); err == nil || cat.reads != 0 {
		t.Errorf("a failed permission check: err = %v, reads = %d", err, cat.reads)
	}
}
