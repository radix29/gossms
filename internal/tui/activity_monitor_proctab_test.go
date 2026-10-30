package tui

import (
	"database/sql/driver"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// procResult is one run shaped like sp_block's.
func procResult() *query.Result {
	return &query.Result{Sets: []query.ResultSet{{
		Columns:     []string{"blktree", "type", "sql text"},
		ColumnTypes: []string{"nvarchar(10)", "nvarchar(32)", "xml"},
		Rows: [][]string{
			{"0053", "LCK_M_X", "<?query --select 1--?>"},
			{"|------  0057", "LCK_M_S", "<?query --select 2--?>"},
		},
	}}}
}

// hostedProcMonitor is a panel App.panelHosted accepts, showing tab, so
// postAndWake callbacks run.
func hostedProcMonitor(t *testing.T, tab amTab) *ActivityMonitor {
	t.Helper()
	am := newTestActivityMonitor(100, 30)
	am.app.panels.AddPanel(am) // relays out the panel, so re-bound after
	am.SetBounds(0, 0, 100, 30)
	am.app.cfg.MaxCellLength = config.DefaultMaxCellLength
	am.setTab(tab)
	if am.procTab() == nil {
		t.Fatalf("tab %d is not procedure-backed", tab)
	}
	if am.procTab().grid == nil {
		t.Fatalf("showing tab %d built no result grid", tab)
	}
	return am
}

// procTabs are both procedure tabs; the rules below hold for both.
var procTabs = []struct {
	name string
	tab  amTab
}{
	{"Block", amTabBlock},
	{"Sessions", amTabSessions},
}

// A run's rows and row count reach the screen.
func TestActivityMonitorProcTabRendersResult(t *testing.T) {
	for _, tc := range procTabs {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, tc.tab)
			am.procTab().loc = activity.ProcTempDB
			am.procTab().applyResult(procResult())

			rows := amRender(am, 100, 30)
			if !amRowsContain(rows, "blktree") {
				t.Error("the tab drew no column headers")
			}
			if !amRowsContain(rows, "LCK_M_X") {
				t.Error("the tab drew no rows")
			}
			if !amRowsContain(rows, "2 row(s)") {
				t.Error("the tab reported no row count")
			}
		})
	}
}

// A failed run keeps what's on screen.
func TestActivityMonitorProcTabKeepsRowsOnError(t *testing.T) {
	for _, tc := range procTabs {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, tc.tab)
			pt := am.procTab()
			pt.loc = activity.ProcTempDB
			pt.applyResult(procResult())

			pt.applyResult(&query.Result{Messages: []query.Message{{Text: "boom", IsError: true}}})
			rows := amRender(am, 100, 30)
			if !amRowsContain(rows, "LCK_M_X") {
				t.Error("a failed refresh cleared the previous result")
			}
			if !amRowsContain(rows, "boom") {
				t.Error("a failed refresh reported nothing")
			}
		})
	}
}

// Refresh keeps a dragged column width (SetData would recompute and snap it
// back) but not the cursor, since the sessions differ.
func TestActivityMonitorProcTabRefreshKeepsADraggedColumnWidth(t *testing.T) {
	for _, tc := range procTabs {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, tc.tab)
			pt := am.procTab()
			pt.loc = activity.ProcTempDB
			pt.applyResult(procResult())

			pt.grid.SetColumnWidth(2, 40)
			pt.grid.SetSelectedCell(1, 0)

			pt.applyResult(procResult())

			if got := pt.grid.ColumnWidth(2); got != 40 {
				t.Errorf("the dragged width of the sql text column is %d after a refresh, want 40", got)
			}
			if row, _ := pt.grid.SelectedCell(); row != 0 {
				t.Errorf("the cursor sits on row %d after a refresh, want the top row", row)
			}
		})
	}
}

// "Install in master" is offered only when master lacks the procedure.
func TestActivityMonitorProcTabInstallButtonGating(t *testing.T) {
	for _, tc := range procTabs {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, tc.tab)

			toolbar := func() string { return amRender(am, 100, 30)[1] }
			if !strings.Contains(toolbar(), "Install in master") {
				t.Error("the toolbar offered no Install in master button")
			}
			if !strings.Contains(toolbar(), "Refresh") {
				t.Error("the toolbar has no Refresh button")
			}
			if strings.Contains(toolbar(), "Pause") || strings.Contains(toolbar(), "rate:") {
				t.Errorf("toolbar = %q, want no auto-refresh controls", toolbar())
			}

			am.procTab().loc = activity.ProcMaster
			am.buildTools()
			if strings.Contains(toolbar(), "Install in master") {
				t.Error("Install in master survived the procedure already being in master")
			}
		})
	}
}

// The grid gets arrows, but Tab stays the panel's so the keyboard can leave.
func TestActivityMonitorProcTabKeyRouting(t *testing.T) {
	am := hostedProcMonitor(t, amTabBlock)
	am.procTab().loc = activity.ProcTempDB
	am.procTab().applyResult(procResult())

	if !amKey(am, tcell.KeyDown) {
		t.Fatal("Down did not reach the Block grid")
	}
	if row, _ := am.blk.grid.SelectedCell(); row != 1 {
		t.Errorf("Block grid selected row = %d, want 1", row)
	}
	if !amKey(am, tcell.KeyTab) {
		t.Fatal("Tab was not handled on the Block tab")
	}
	if am.tab != amTabHistory {
		t.Errorf("Tab from Block landed on tab %d, want History", am.tab)
	}
}

// Without a connection the tab says so.
func TestActivityMonitorProcTabWithoutConnection(t *testing.T) {
	for _, tc := range procTabs {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, tc.tab)
			pt := am.procTab()
			if pt.status != "Not connected" {
				t.Errorf("status = %q, want %q", pt.status, "Not connected")
			}
			if pt.conn != nil {
				t.Error("the tab opened a connection it had no server for")
			}
		})
	}
}

// sp_WhoIsActive is someone else's GPL-3.0 work; the credit row must be drawn
// and not covered by the grid.
func TestActivityMonitorSessionsCreditsWhoIsActive(t *testing.T) {
	am := hostedProcMonitor(t, amTabSessions)
	am.sess.loc = activity.ProcTempDB
	am.sess.applyResult(procResult())

	rows := amRender(am, 120, 30)
	credit := rows[am.contentRect.Y]
	for _, want := range []string{"sp_WhoIsActive", activity.WhoIsActiveAuthor} {
		if !strings.Contains(credit, want) {
			t.Errorf("the Sessions credit row = %q, missing %q", credit, want)
		}
	}
	if am.sess.gridRect.Y <= am.contentRect.Y {
		t.Error("the grid was laid out over the credit row")
	}
	if !amRowsContain(rows, "blktree") {
		t.Error("the credit row cost the grid its headers")
	}
}

// Sessions runs sp_WhoIsActive under its non-sp_ tempdb name.
func TestActivityMonitorSessionsUsesWhoIsActive(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	if am.sess.proc != activity.WhoIsActiveProc {
		t.Fatal("the Sessions tab is not backed by sp_WhoIsActive")
	}
	if got := am.sess.proc.Exec(activity.ProcTempDB); got != "exec tempdb.dbo.usp_WhoIsActive" {
		t.Errorf("Sessions Exec = %q", got)
	}
}

// A tempdb copy an earlier gossms installed is replaced when its stored
// definition differs, and left alone when it matches: the copy lives until a
// restart, so without this a fixed body never reaches a server that already
// has one (review plan 2026-10-16, U1).
func TestActivityMonitorProcTabReplacesAStaleTempDBCopy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stored  string
		install bool
	}{
		{"stale", "create   procedure dbo.usp_block as select 1", true},
		{"current", activity.BlockProc.Script(activity.ProcTempDB), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, amTabBlock)
			conn, inst := newFakeConn(t,
				fakeResponse{match: "object_id('master.dbo.sp_block', 'P')", cols: 2, rows: [][]driver.Value{{false, true}}},
				fakeResponse{match: "from tempdb.sys.sql_modules", cols: 1, rows: [][]driver.Value{{tc.stored}}},
			)
			pt := am.procTab()
			pt.conn = conn
			pt.resolveProc()
			drainUntil(t, am.app, func() bool { return pt.loc == activity.ProcTempDB }, "the procedure resolved")

			installed := false
			for _, s := range inst.Statements() {
				if strings.Contains(s, "exec tempdb.sys.sp_executesql @stmt") {
					installed = true
				}
			}
			if installed != tc.install {
				t.Errorf("installed = %v, want %v; statements: %v", installed, tc.install, inst.Statements())
			}
		})
	}
}

// A master copy that is not this build's is offered for update, not replaced:
// master's may be a DBA's own. One that matches offers nothing.
func TestActivityMonitorProcTabOffersUpdateForAStaleMasterCopy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored string
		offer  bool
	}{
		{"stale", "create   procedure dbo.sp_block as select 1", true},
		{"current", activity.BlockProc.Script(activity.ProcMaster), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, amTabBlock)
			conn, inst := newFakeConn(t,
				fakeResponse{match: "object_id('master.dbo.sp_block', 'P')", cols: 2, rows: [][]driver.Value{{true, false}}},
				fakeResponse{match: "from master.sys.sql_modules", cols: 1, rows: [][]driver.Value{{tc.stored}}},
			)
			pt := am.procTab()
			pt.conn = conn
			pt.resolveProc()
			drainUntil(t, am.app, func() bool { return pt.loc == activity.ProcMaster }, "the procedure resolved")

			for _, s := range inst.Statements() {
				if strings.Contains(s, "sp_executesql @stmt") {
					t.Errorf("master's copy was replaced without asking: %v", s)
				}
			}
			if pt.masterStale != tc.offer {
				t.Errorf("masterStale = %v, want %v", pt.masterStale, tc.offer)
			}
			am.buildTools()
			bar := amRender(am, 100, 30)[1]
			if got := strings.Contains(bar, "Update in master"); got != tc.offer {
				t.Errorf("Update in master offered = %v, want %v; toolbar %q", got, tc.offer, bar)
			}
			if strings.Contains(bar, "Install in master") {
				t.Errorf("Install in master offered over master's own copy; toolbar %q", bar)
			}
		})
	}
}

// Update in master is hidden without CONTROL SERVER: such a login may not read
// the definition, which reads as differing, so the offer would be unfounded.
func TestActivityMonitorProcTabUpdateInMasterNeedsControlServer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		granted bool
	}{{"denied", false}, {"granted", true}} {
		t.Run(tc.name, func(t *testing.T) {
			am := hostedProcMonitor(t, amTabBlock)
			pt := am.procTab()
			if tc.granted {
				pt.conn = serverRoleConn(t, []string{"CONTROL SERVER"}, nil, nil, []string{"sysadmin"})
			} else {
				pt.conn = serverRoleConn(t, nil, []string{"CONTROL SERVER"}, nil, []string{"sysadmin"})
			}
			pt.loc, pt.masterStale = activity.ProcMaster, true
			am.buildTools()
			if got := strings.Contains(amRender(am, 100, 30)[1], "Update in master"); got != tc.granted {
				t.Errorf("Update in master offered = %v, want %v", got, tc.granted)
			}
		})
	}
}

// killTarget is the Block tab of a hosted panel with row 1 (session 57)
// selected, its own connection answering the session read with session.
func killTarget(t *testing.T, session []driver.Value) (*ActivityMonitor, *amProcTab, *fakeInstance) {
	t.Helper()
	am := hostedProcMonitor(t, amTabBlock)
	am.app.alertDialog = dialogs.NewAlertDialog(nil)
	am.conn = probedConn(t, "", []string{"ALTER ANY CONNECTION"}, nil, nil, nil)
	var rows [][]driver.Value
	if session != nil {
		rows = [][]driver.Value{session}
	}
	sc, inst := newFakeConn(t, fakeResponse{match: "WHERE  s.session_id = @p1", arg: "57", cols: 23, rows: rows})
	pt := am.procTab()
	pt.conn = sc
	pt.loc = activity.ProcTempDB
	pt.applyResult(procResult())
	pt.grid.SetSelectedRow(1)
	return am, pt, inst
}

// sessionRow is a session read row: id 57 from program, client process pid,
// on the reading connection's host.
func sessionRow(program string, pid int) []driver.Value {
	return []driver.Value{int64(57), "appuser", "ws7", program, "appdb", "sleeping",
		int64(0), int64(0), int64(0), nil, nil, "", "",
		int64(0), int64(0), int64(0), int64(0),
		int64(0), nil, int64(0), "", int64(pid), true}
}

// killItem is the grid menu's Kill Process item.
func killItem(t *testing.T, pt *amProcTab) controls.MenuItem {
	t.Helper()
	for _, it := range pt.menuItems() {
		if it.Label == "Kill Process..." {
			return it
		}
	}
	t.Fatal("no Kill Process item")
	return controls.MenuItem{}
}

func killStatements(inst *fakeInstance) []string {
	return slices.DeleteFunc(inst.Statements(), func(s string) bool { return !strings.Contains(s, "KILL") })
}

// KILL needs ALTER ANY CONNECTION, which processadmin carries; a login
// without it gets the item disabled and told what is missing.
func TestKillProcessIsGatedOnAlterAnyConnection(t *testing.T) {
	_, pt, _ := killTarget(t, nil)
	if it := killItem(t, pt); it.Enabled != nil && !it.Enabled() {
		t.Error("Kill Process withheld from a login holding ALTER ANY CONNECTION")
	}
	pt.am.conn = probedConn(t, "", nil, []string{"ALTER ANY CONNECTION"}, nil, nil)
	it := killItem(t, pt)
	if it.Enabled == nil || it.Enabled() {
		t.Error("Kill Process offered to a login without ALTER ANY CONNECTION")
	}
	if it.Note != "needs ALTER ANY CONNECTION" {
		t.Errorf("note = %q", it.Note)
	}
}

// The confirmation names the session the server reports now, opens on No,
// and Yes sends KILL for the selected row's session.
func TestKillProcessConfirmsThenKills(t *testing.T) {
	am, pt, inst := killTarget(t, sessionRow("SSMS", 4242))
	a := am.app
	killItem(t, pt).Action()
	drainUntil(t, a, a.confirmDialog.Visible, "the kill confirmation")
	for _, want := range []string{"session 57", "appuser", "ws7", "appdb"} {
		if !strings.Contains(a.confirmDialog.Message(), want) {
			t.Errorf("confirmation %q does not name %q", a.confirmDialog.Message(), want)
		}
	}
	answerConfirm(t, a, false) // Enter on the opening focus
	if got := killStatements(inst); len(got) != 0 {
		t.Fatalf("Enter on the opening focus killed: %q; No must be the default", got)
	}

	killItem(t, pt).Action()
	drainUntil(t, a, a.confirmDialog.Visible, "the kill confirmation")
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	drainUntil(t, a, func() bool { return a.statusText == "Session 57 killed" }, "the kill to finish")
	if got := killStatements(inst); len(got) != 1 || got[0] != "KILL 57" {
		t.Errorf("statements = %q, want KILL 57", got)
	}
}

// The panel's own connections are refused before the server's Msg 6104 —
// and so is any other Activity Monitor connection of this process, whose
// loss would break a tab. The same program from another process is fair game.
func TestKillProcessRefusesTheMonitorsOwnSession(t *testing.T) {
	for _, c := range []struct {
		name    string
		session []driver.Value
		refused bool
	}{
		{"own", sessionRow(db.RoleActivityMonitor.ApplicationName(), os.Getpid()), true},
		{"another gossms", sessionRow(db.RoleActivityMonitor.ApplicationName(), os.Getpid()+1), false},
		{"own query window", sessionRow(db.RoleQuery.ApplicationName(), os.Getpid()), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			am, pt, inst := killTarget(t, c.session)
			a := am.app
			killItem(t, pt).Action()
			drainUntil(t, a, func() bool { return a.alertDialog.Visible() || a.confirmDialog.Visible() }, "an answer")
			if got := a.alertDialog.Visible(); got != c.refused {
				t.Errorf("refused = %v, want %v (alert %q)", got, c.refused, a.alertDialog.Message())
			}
			if len(killStatements(inst)) != 0 {
				t.Error("KILL sent before a confirmation")
			}
		})
	}

	// From another host the same pid is another process.
	other := sessionRow(db.RoleActivityMonitor.ApplicationName(), os.Getpid())
	other[22] = false
	am, pt, _ := killTarget(t, other)
	killItem(t, pt).Action()
	drainUntil(t, am.app, am.app.confirmDialog.Visible, "the kill confirmation")
}

// A session that ended since the grid was filled is said so, not killed —
// its id may be reused by the time anyone answers.
func TestKillProcessOfAnEndedSession(t *testing.T) {
	am, pt, inst := killTarget(t, nil)
	a := am.app
	killItem(t, pt).Action()
	drainUntil(t, a, a.alertDialog.Visible, "the ended-session alert")
	if !strings.Contains(a.alertDialog.Message(), "Session 57 has ended") {
		t.Errorf("alert = %q", a.alertDialog.Message())
	}
	if a.confirmDialog.Visible() || len(killStatements(inst)) != 0 {
		t.Error("an ended session was offered for killing")
	}
}
