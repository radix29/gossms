package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// procRunTimeout bounds one procedure run. Both read sysprocesses or request
// DMVs and cross-apply dm_exec_sql_text per row, which can be slow on a
// troubled server; giving up beats never returning. Also bounds the install
// (sp_WhoIsActive is a 5,500-line CREATE).
const procRunTimeout = 60 * time.Second

// whoIsActiveCredit is the Sessions tab's header line. sp_WhoIsActive is
// someone else's GPL-3.0 work, so it's credited author first on the tab that
// runs it; a narrow terminal clips the URL, not the name.
func whoIsActiveCredit() string {
	return "sp_WhoIsActive " + activity.WhoIsActiveVersion() +
		" — © " + activity.WhoIsActiveAuthor + ", " + activity.WhoIsActiveLicense +
		" — " + activity.WhoIsActiveRepo
}

// amProcTab is an Activity Monitor tab backed by a helper procedure: Block
// (sp_block), Sessions (sp_WhoIsActive). Runs once when first shown and on
// Refresh; no timer.
//
// Each tab has its own connection so a slow procedure doesn't delay the
// 2-second sample tick.
type amProcTab struct {
	am   *ActivityMonitor
	proc *activity.Proc

	// credit is drawn on its own row above the grid; empty for goSSMS's own
	// procedure.
	credit string

	conn   *db.ServerConn
	grid   *controls.DataGrid
	result *query.Result // kept only for the column types the XML handoff needs
	loc    activity.ProcLocation
	status string
	busy   bool

	// rect is the content area, gridRect the grid's part; they differ by the
	// credit row.
	rect     core.Rect
	gridRect core.Rect
}

// newProcTab creates a tab over proc; nothing connects or runs until first
// shown.
func (am *ActivityMonitor) newProcTab(proc *activity.Proc, credit string) *amProcTab {
	return &amProcTab{am: am, proc: proc, credit: credit}
}

// procTab is the showing procedure tab, nil otherwise.
func (am *ActivityMonitor) procTab() *amProcTab {
	switch am.tab {
	case amTabBlock:
		return am.blk
	case amTabSessions:
		return am.sess
	}
	return nil
}

// newGrid builds the result grid with query-panel behaviour: cell cursor, row
// numbers, clipboard, and XML/JSON cells opening in their own highlighted
// panel.
func (pt *amProcTab) newGrid() *controls.DataGrid {
	g := controls.NewDataGrid()
	g.SetCellCursor(true)
	g.SetRowNumbers(true)
	g.SetStatusStyle(resultsStatusStyle)
	g.OnCopyRequest = pt.am.app.copyWithStatus
	g.SetMaxCellWidth(pt.am.app.cfg.MaxCellLength + 2)
	g.OnShowValue = func(col int, column, value string) bool {
		return pt.am.app.openCellValuePanel(pt.columnType(col), column, value)
	}
	return g
}

// columnType is column col's declared SQL Server type, which identifies XML
// columns.
func (pt *amProcTab) columnType(col int) string {
	if pt.result == nil || len(pt.result.Sets) == 0 {
		return ""
	}
	types := pt.result.Sets[0].ColumnTypes
	if col < 0 || col >= len(types) {
		return ""
	}
	return types[col]
}

// setStatus shows the tab's state in the grid's status bar.
func (pt *amProcTab) setStatus(s string) {
	pt.status = s
	if pt.grid != nil {
		pt.grid.SetStatus(s)
	}
}

// activate is first-show work: open a connection, find or install the
// procedure, run it once. Called from setTab; cheap afterwards.
func (pt *amProcTab) activate() {
	if pt.grid == nil {
		pt.grid = pt.newGrid()
		pt.layout()
	}
	if pt.conn != nil || pt.busy {
		return
	}
	// The tab clones the panel's connection; without one, say so.
	if pt.am.conn == nil || pt.am.conn.Server == nil {
		pt.setStatus("Not connected")
		return
	}
	pt.busy = true
	pt.setStatus("Connecting...")
	pt.am.buildTools()

	opts := pt.am.conn.Opts
	pt.am.app.safegoRepair("connecting Activity Monitor "+pt.proc.MasterName+" tab", pt.panicRepair, func() {
		conn, err := db.ConnectContext(context.Background(), opts, db.RoleActivityMonitor)
		pt.am.app.postAndWake(func() { pt.connected(conn, err) })
	})
}

// panicRepair releases the busy latch after a panic in a background step (see
// App.safegoRepair). Each step clears busy in its completion callback, which a
// panic skips, leaving the tab frozen with Refresh and install dimmed.
func (pt *amProcTab) panicRepair() {
	if !pt.am.app.panelHosted(pt.am) {
		return
	}
	pt.busy = false
	pt.setStatus("Stopped unexpectedly — see the log for details")
	pt.am.buildTools()
}

// connected adopts the tab's connection and moves on to the procedure.
func (pt *amProcTab) connected(conn *db.ServerConn, err error) {
	if !pt.am.app.panelHosted(pt.am) {
		// Closed while dialling; nothing else references conn, so close it
		// here.
		if conn != nil {
			conn.Close()
		}
		return
	}
	if err != nil {
		pt.busy = false
		pt.setStatus("Connection failed: " + firstErrorLine(err.Error()))
		pt.am.buildTools()
		return
	}
	pt.am.adopt(conn)
	pt.conn = conn
	pt.setStatus("Locating " + pt.proc.MasterName + "...")
	pt.resolveProc()
}

// resolveProc finds the procedure, installing into tempdb when neither database
// has it. master wins when present (deliberately installed, survives restarts).
// The tempdb copy is left on teardown; a restart removes it.
func (pt *amProcTab) resolveProc() {
	conn := pt.conn
	pt.busy = true
	pt.am.app.safegoRepair("preparing "+pt.proc.MasterName, pt.panicRepair, func() {
		ctx, cancel := context.WithTimeout(conn.Context(), procRunTimeout)
		defer cancel()
		loc, err := pt.proc.Find(ctx, conn.Server.DB())
		if err == nil && loc == activity.ProcNone {
			if err = pt.proc.Install(ctx, conn.Server.DB(), activity.ProcTempDB); err == nil {
				loc = activity.ProcTempDB
			}
		}
		pt.am.app.postAndWake(func() { pt.procResolved(loc, err) })
	})
}

// procResolved records where the procedure is and runs it.
func (pt *amProcTab) procResolved(loc activity.ProcLocation, err error) {
	if !pt.am.app.panelHosted(pt.am) {
		return
	}
	pt.busy = false
	pt.loc = loc
	if err != nil {
		pt.setStatus(err.Error())
		pt.am.buildTools()
		return
	}
	pt.setStatus(pt.proc.Qualified(loc))
	pt.am.buildTools()
	pt.refresh()
}

// refresh runs the procedure into the grid.
func (pt *amProcTab) refresh() {
	if pt.conn == nil {
		pt.activate()
		return
	}
	if pt.busy {
		return
	}
	if pt.loc == activity.ProcNone {
		pt.setStatus(pt.proc.MasterName + " is not available")
		return
	}
	pt.busy = true
	qualified := pt.proc.Qualified(pt.loc)
	pt.setStatus("Running " + qualified + "...")
	pt.am.buildTools()

	conn, script := pt.conn, pt.proc.Exec(pt.loc)
	pt.am.app.safegoRepair("running "+qualified, pt.panicRepair, func() {
		ctx, cancel := context.WithTimeout(conn.Context(), procRunTimeout)
		defer cancel()
		res := query.Execute(ctx, conn.Server.DB(), "", script)
		pt.am.app.postAndWake(func() { pt.applyResult(res) })
	})
}

// applyResult loads the first result set into the grid. Errors go to the status
// line, keeping the previous rows as the last true picture.
func (pt *amProcTab) applyResult(res *query.Result) {
	if !pt.am.app.panelHosted(pt.am) {
		return
	}
	pt.busy = false
	pt.am.buildTools()
	qualified := pt.proc.Qualified(pt.loc)
	if res.HasErrors() {
		for _, m := range res.Messages {
			if m.IsError {
				pt.setStatus(m.Text)
				return
			}
		}
		pt.setStatus(qualified + " failed")
		return
	}
	pt.result = res
	if len(res.Sets) == 0 {
		pt.grid.SetData(nil, nil)
		pt.setStatus("No result returned")
		return
	}
	set := res.Sets[0]
	// Re-applied per result, like QueryPanel.renderActiveTab, so Options' max
	// cell length reaches this grid.
	pt.grid.SetMaxCellWidth(pt.am.app.cfg.MaxCellLength + 2)
	// resetGrid, not SetData: same columns, so keep dragged widths; different
	// sessions, so don't keep the cursor row.
	resetGrid(pt.grid, set.Columns, set.Rows, 0)
	pt.setStatus(fmt.Sprintf("%d row(s)  %s  (%s)",
		len(set.Rows), time.Now().Format("15:04:05"), qualified))
}

// confirmInstallInMaster asks before writing to master, the one thing these
// tabs do that outlives the session and touches a system database.
func (pt *amProcTab) confirmInstallInMaster() {
	pt.am.app.confirmDialog.ShowConfirm("Install "+pt.proc.MasterName+" in master",
		"Create "+pt.proc.Qualified(activity.ProcMaster)+"? This writes a stored procedure into a system database.",
		func(confirmed bool) {
			if confirmed {
				pt.installInMaster()
			}
		})
}

// installInMaster creates the procedure in master and switches the tab to it.
func (pt *amProcTab) installInMaster() {
	if pt.conn == nil || pt.busy {
		return
	}
	pt.busy = true
	qualified := pt.proc.Qualified(activity.ProcMaster)
	pt.setStatus("Installing " + qualified + "...")
	pt.am.buildTools()

	conn := pt.conn
	pt.am.app.runWithProgress(progressJob{
		title:   "Install " + pt.proc.MasterName + " in master",
		message: "Creating " + qualified + "...",
		what:    "installing " + qualified,
		sc:      conn,
		timeout: procRunTimeout,
		repair:  pt.panicRepair,
	}, func(ctx context.Context, _ progressReport) error {
		return pt.proc.Install(ctx, conn.Server.DB(), activity.ProcMaster)
	}, func(err error, cancelled bool) {
		if cancelled {
			err = fmt.Errorf("Install of %s cancelled", qualified)
		}
		pt.masterInstalled(err)
	})
}

// masterInstalled reports the install and re-runs against the new copy.
func (pt *amProcTab) masterInstalled(err error) {
	if !pt.am.app.panelHosted(pt.am) {
		return
	}
	pt.busy = false
	if err != nil {
		pt.setStatus(err.Error())
		pt.am.buildTools()
		return
	}
	pt.loc = activity.ProcMaster
	pt.am.app.setStatus(pt.proc.Qualified(activity.ProcMaster) + " installed")
	pt.am.buildTools()
	pt.refresh()
}

// layout gives the grid the content area, minus the credit row. The grid draws
// its own status bar.
func (pt *amProcTab) layout() {
	pt.rect = pt.am.contentRect
	r := pt.rect
	if pt.credit != "" && r.H > 1 {
		r.Y++
		r.H--
	}
	pt.gridRect = r
	if pt.grid != nil {
		pt.grid.SetBounds(r.X, r.Y, r.W, r.H)
	}
}

// draw renders the credit row and grid. The grid's menu and value popup extend
// outside its rect and must paint over everything, hence a separate call.
func (pt *amProcTab) draw(s tcell.Screen) {
	if pt.grid == nil {
		return
	}
	if pt.credit != "" && pt.rect.W > 2 && pt.rect.H > 1 {
		pal := theme.Active()
		core.DrawTextClipped(s, pt.rect.X+1, pt.rect.Y, pt.rect.W-2,
			theme.StylePanel().Foreground(pal.TextDim), pt.credit)
	}
	pt.grid.Focus(pt.am.active)
	pt.grid.Draw(s)
	pt.grid.DrawOverlay(s)
}
