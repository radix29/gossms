package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/query"
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
