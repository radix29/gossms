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

// procRunTimeout bounds one run of a tab's procedure. Both of them read
// sys.sysprocesses or the request DMVs and cross-apply dm_exec_sql_text per
// row; on a server bad enough to be worth looking at, that can be slow, and a
// tab that never comes back is worse than one that says it gave up. The same
// bound covers the install, which for sp_WhoIsActive is a 5,500-line CREATE.
const procRunTimeout = 60 * time.Second

// whoIsActiveCredit is the Sessions tab's header line. sp_WhoIsActive is
// somebody else's GPL-3.0 work, so the attribution is ordered author first
// and drawn on the tab that runs it, not only in Help > About — a narrow
// terminal clips the URL off the end rather than the name.
func whoIsActiveCredit() string {
	return "sp_WhoIsActive " + activity.WhoIsActiveVersion() +
		" — © " + activity.WhoIsActiveAuthor + ", " + activity.WhoIsActiveLicense +
		" — " + activity.WhoIsActiveRepo
}

// amProcTab is an Activity Monitor tab backed by a helper stored procedure:
// Block over sp_block, Sessions over sp_WhoIsActive. Nothing here runs on a
// timer — the procedure runs once when the tab is first shown, and again on
// Refresh.
//
// Each tab gets a connection of its own rather than sharing the collector's:
// the procedures can take seconds on a busy server, and a two-second sample
// tick must not queue behind one.
type amProcTab struct {
	am   *ActivityMonitor
	proc *activity.Proc

	// credit is drawn on its own row above the grid, empty when the procedure
	// is goSSMS's own. sp_WhoIsActive is somebody else's GPL-3.0 work, and the
	// attribution belongs where it is being used, not only in Help > About.
	credit string

	conn   *db.ServerConn
	grid   *controls.DataGrid
	result *query.Result // kept only for the column types the XML handoff needs
	loc    activity.ProcLocation
	status string
	busy   bool

	// rect is the whole content area, gridRect the part of it the grid got.
	// They differ by the credit row, which is not the grid's to hit-test.
	rect     core.Rect
	gridRect core.Rect
}

// newProcTab creates a tab over proc. No connection is opened and no query
// runs until the tab is first shown.
func (am *ActivityMonitor) newProcTab(proc *activity.Proc, credit string) *amProcTab {
	return &amProcTab{am: am, proc: proc, credit: credit}
}

// procTab is the procedure-backed tab currently showing, nil on the others.
func (am *ActivityMonitor) procTab() *amProcTab {
	switch am.tab {
	case amTabBlock:
		return am.blk
	case amTabSessions:
		return am.sess
	}
	return nil
}

// newGrid builds the tab's result grid with the same behaviour as a query
// panel's: cell cursor, row numbers, clipboard, and the XML/JSON cell handoff
// that opens a query-plan or sql-text column in its own highlighted panel.
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

// columnType is the declared SQL Server type of the result's col'th column —
// what tells an XML column's value from an ordinary string so it opens as XML.
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

// setStatus is the tab's one-line state, shown in the grid's own status bar
// so it sits with the rows it describes.
func (pt *amProcTab) setStatus(s string) {
	pt.status = s
	if pt.grid != nil {
		pt.grid.SetStatus(s)
	}
}

// activate is the tab's first-showing work: open its own connection, find or
// install the procedure, and run it once. Called from setTab, and cheap on
// every showing after the first.
func (pt *amProcTab) activate() {
	if pt.grid == nil {
		pt.grid = pt.newGrid()
		pt.layout()
	}
	if pt.conn != nil || pt.busy {
		return
	}
	// The panel is only ever opened for a connected server, but its own
	// connection is what this tab clones — without one there is nothing to
	// dial, and saying so beats a button that silently does nothing.
	if pt.am.conn == nil || pt.am.conn.Server == nil {
		pt.setStatus("Not connected")
		return
	}
	pt.busy = true
	pt.setStatus("Connecting...")
	pt.am.buildTools()

	opts := pt.am.conn.Opts
	pt.am.app.safegoRepair("connecting Activity Monitor "+pt.proc.MasterName+" tab", pt.panicRepair, func() {
		conn, err := db.Connect(opts)
		pt.am.app.postAndWake(func() { pt.connected(conn, err) })
	})
}

// panicRepair releases the busy latch after a panic on one of this tab's
// background steps — see App.safegoRepair. Every step below clears busy in
// the callback it posts on completion, which a panic never reaches: buildTools
// dims Refresh and both install actions while busy is set, so the tab would
// be left frozen at "Connecting..." or "Running..." with no way back short of
// closing the panel.
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
		// Closed while the dial was resolving — nothing else references conn,
		// so it leaks for the process's lifetime unless closed here.
		if conn != nil {
			conn.Close()
		}
		return
	}
	if err != nil {
		pt.busy = false
		pt.setStatus(fmt.Sprintf("Connection failed: %v", err))
		pt.am.buildTools()
		return
	}
	pt.am.adopt(conn)
	pt.conn = conn
	pt.setStatus("Locating " + pt.proc.MasterName + "...")
	pt.resolveProc()
}

// resolveProc finds the procedure, installing it in tempdb when neither
// database has it. master wins when it is there — someone installed it
// deliberately, and a master copy survives a restart. The tempdb copy is left
// behind on teardown on purpose: a restart is what removes it.
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

// procResolved records where the procedure is and runs it once.
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

// refresh runs the procedure and puts its result in the grid.
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

// applyResult loads a run's first result set into the grid. Errors go to the
// status line rather than clearing the grid: the previous rows are still the
// last true picture of the server, and blanking them on a failed refresh
// loses it.
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
	// Re-applied per result, like QueryPanel.renderActiveTab, so a change to
	// the Options dialog's max cell length reaches this grid too.
	pt.grid.SetMaxCellWidth(pt.am.app.cfg.MaxCellLength + 2)
	// resetGrid, not SetData: a refresh runs the same procedure again, so the
	// columns are the ones the user just dragged to fit a wide sql_text, while
	// the rows are a different set of sessions — widths are worth keeping, the
	// old cursor row is not.
	resetGrid(pt.grid, set.Columns, set.Rows, 0)
	pt.setStatus(fmt.Sprintf("%d row(s)  %s  (%s)",
		len(set.Rows), time.Now().Format("15:04:05"), qualified))
}

// confirmInstallInMaster asks before writing to master, which is the one
// thing these tabs do that outlives the session and touches a system
// database.
func (pt *amProcTab) confirmInstallInMaster() {
	pt.am.app.confirmDialog.ShowConfirm("Install "+pt.proc.MasterName+" in master",
		"Create "+pt.proc.Qualified(activity.ProcMaster)+"? This writes a stored procedure into a system database.",
		func(confirmed bool) {
			if confirmed {
				pt.installInMaster()
			}
		})
}

// installInMaster creates the procedure in master and switches the tab over
// to it.
func (pt *amProcTab) installInMaster() {
	if pt.conn == nil || pt.busy {
		return
	}
	pt.busy = true
	qualified := pt.proc.Qualified(activity.ProcMaster)
	pt.setStatus("Installing " + qualified + "...")
	pt.am.buildTools()

	conn := pt.conn
	pt.am.app.safegoRepair("installing "+qualified, pt.panicRepair, func() {
		ctx, cancel := context.WithTimeout(conn.Context(), procRunTimeout)
		defer cancel()
		err := pt.proc.Install(ctx, conn.Server.DB(), activity.ProcMaster)
		pt.am.app.postAndWake(func() { pt.masterInstalled(err) })
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

// layout gives the grid the content area, less the credit row when there is
// one. The grid draws its own status bar on its last row, so nothing is
// reserved for one.
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

// draw renders the credit row and the grid. The grid's context menu and value
// popup live outside its own rect and have to paint over everything else, so
// they are a separate call — without it the menu opens, swallows every event,
// and is never drawn.
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
