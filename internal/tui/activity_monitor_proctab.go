package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tui/gate"
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
	// masterStale is set when loc is ProcMaster and master's copy is not this
	// build's (activity.Proc.Current), which offers Update in master.
	masterStale bool
	status      string
	busy        bool

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
	g.OnMenuItems = pt.menuItems
	return g
}

// menuItems is the grid's own part of its cell menu, for the selected row's
// session: Show Live Execution Plan, and Kill Process. Both are disabled with a
// note on a row that names none. The plan is offered whatever the session is
// doing — whether its query is profiled is only known by asking, and the plan
// panel says why it is empty.
func (pt *amProcTab) menuItems() []controls.MenuItem {
	spid, ok := pt.selectedSession()
	plan := controls.MenuItem{Label: "Show Live Execution Plan"}
	kill := controls.MenuItem{Label: "Kill Process..."}
	sc := pt.am.conn
	if !ok || sc == nil || sc.Server == nil {
		for _, it := range []*controls.MenuItem{&plan, &kill} {
			it.Enabled = func() bool { return false }
			it.Note = "no session"
		}
		return []controls.MenuItem{plan, kill}
	}
	plan.Action = func() { pt.am.app.openLivePlanPanel(sc, spid) }
	kill.Action = func() { pt.lookUpSessionToKill(spid) }
	// Gated on the panel's connection, whose capabilities the app probed; the
	// tab's own, which the KILL is sent on, is the same login.
	return []controls.MenuItem{plan, gate.Item(kill, sc, "", gate.AlterAnyConnection)}
}

// lookUpSessionToKill reads the session afresh before asking: the grid may be
// minutes old and a session id is reused once its session ends, so the
// confirmation names the login, host and database the KILL will actually hit.
// Block's grid has no login or database column anyway. It runs on the tab's
// own connection, which is also the one the KILL is sent on.
func (pt *amProcTab) lookUpSessionToKill(spid int) {
	conn := pt.conn
	if conn == nil || conn.Server == nil {
		return
	}
	pt.am.app.setStatus(fmt.Sprintf("Reading session %d...", spid))
	pt.am.app.safego("reading session "+strconv.Itoa(spid), func() {
		ctx, cancel := context.WithTimeout(conn.Server.Context(), procRunTimeout)
		defer cancel()
		as, err := conn.Server.SessionByID(ctx, spid)
		pt.am.app.postAndWake(func() { pt.confirmKill(spid, as, err) })
	})
}

// confirmKill asks before the KILL, or says why there is none to ask about.
func (pt *amProcTab) confirmKill(spid int, as *gosmo.ActiveSession, err error) {
	if !pt.am.app.panelHosted(pt.am) || pt.conn == nil {
		return
	}
	app := pt.am.app
	app.setStatus("")
	switch {
	case errors.Is(err, gosmo.ErrNotFound):
		app.alertDialog.ShowAlert("Kill Process", fmt.Sprintf("Session %d has ended.", spid))
		pt.refresh()
		return
	case err != nil:
		app.alertDialog.ShowAlert("Kill Process", fmt.Sprintf("Session %d could not be read: %v", spid, err))
		return
	case isOwnActivityMonitorSession(as):
		// Every Activity Monitor connection of this process, not only the one
		// the KILL would go out on: the server refuses that one (Msg 6104), and
		// killing another leaves a tab or the dashboard failing on its next run.
		app.alertDialog.ShowAlert("Kill Process", fmt.Sprintf(
			"Session %d is this Activity Monitor's own connection and cannot be killed from here.", spid))
		return
	}
	app.confirmDialog.ShowConfirmDefaultNo("Kill Process", killPrompt(as), func(confirmed bool) {
		if confirmed {
			pt.kill(spid)
		}
	})
}

// isOwnActivityMonitorSession reports whether as is one of this process's
// Activity Monitor connections: the program name the role reports, from this
// process id on the reading connection's own host (gosmo.BlockedRequests
// identifies a client process the same way).
func isOwnActivityMonitorSession(as *gosmo.ActiveSession) bool {
	return as.ProgramName == db.RoleActivityMonitor.ApplicationName() &&
		as.HostProcessID == os.Getpid() && as.FromCallerHost
}

// killPrompt names what is about to be killed. A system session (no login)
// says so rather than showing blanks.
func killPrompt(as *gosmo.ActiveSession) string {
	orNone := func(s string) string {
		if s == "" {
			return "(none)"
		}
		return s
	}
	return fmt.Sprintf("Kill session %d?\n\nLogin: %s\nHost: %s\nProgram: %s\nDatabase: %s\n\n"+
		"Any open transaction it has is rolled back, which can take as long as the work it undoes.",
		as.SessionID, orNone(as.LoginName), orNone(as.HostName), orNone(as.ProgramName), orNone(as.DatabaseName))
}

// kill sends KILL on the tab's connection, then refreshes the tab.
func (pt *amProcTab) kill(spid int) {
	conn := pt.conn
	if conn == nil || conn.Server == nil {
		return
	}
	pt.am.app.runWithProgress(progressJob{
		title:   "Kill Process",
		message: fmt.Sprintf("Killing session %d...", spid),
		what:    "killing session " + strconv.Itoa(spid),
		sc:      conn,
	}, func(ctx context.Context, _ progressReport) error {
		return conn.Server.KillSession(ctx, spid)
	}, func(err error, cancelled bool) {
		if !pt.am.app.panelHosted(pt.am) {
			return
		}
		switch {
		case cancelled:
			// The KILL may have reached the server before the cancel; the
			// refresh shows which.
			pt.am.app.setStatus(fmt.Sprintf("Kill of session %d cancelled", spid))
		case err != nil:
			pt.am.app.setStatus(fmt.Sprintf("Failed to kill session %d: %v", spid, err))
		default:
			pt.am.app.setStatus(fmt.Sprintf("Session %d killed", spid))
		}
		pt.refresh()
	})
}

// selectedSession is the session id of the grid's selected row: Sessions'
// session_id column, or the number that ends Block's blktree cell (the tree's
// indent drawing comes first).
func (pt *amProcTab) selectedSession() (int, bool) {
	if pt.result == nil || len(pt.result.Sets) == 0 || pt.grid == nil {
		return 0, false
	}
	set := pt.result.Sets[0]
	row := pt.grid.SelectedRow()
	if row < 0 || row >= len(set.Rows) {
		return 0, false
	}
	for i, c := range set.Columns {
		if (c == "session_id" || c == "blktree") && i < len(set.Rows[row]) && !set.IsNull(row, i) {
			return sessionIDIn(set.Rows[row][i])
		}
	}
	return 0, false
}

// sessionIDIn reads the session id a cell ends with, past any tree drawing
// and padding: "57", "  57", "|         |------  57".
func sessionIDIn(cell string) (int, bool) {
	fields := strings.Fields(cell)
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimLeft(fields[len(fields)-1], "|-"))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
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

	opts := pt.am.conn.CloneOpts()
	// The dial is scoped to the panel's connection, per ARCHITECTURE.md
	// § Threading model: closing the Activity Monitor's connection aborts this
	// clone's attempt rather than leaving it to the 15 s connect timeout. The
	// tab's own connection is unaffected once the dial has returned —
	// Connect roots it at Background.
	parent := pt.am.conn.Server.Context()
	pt.am.app.safegoRepair("connecting Activity Monitor "+pt.proc.MasterName+" tab", pt.panicRepair, func() {
		conn, err := db.Connect(parent, opts, db.RoleActivityMonitor)
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
// The tempdb copy is left on teardown; a restart removes it. A tempdb copy
// that is not this build's is replaced; master's may be hand-installed, so a
// copy that differs is only flagged (masterStale) for Update in master.
func (pt *amProcTab) resolveProc() {
	conn := pt.conn
	pt.busy = true
	pt.am.app.safegoRepair("preparing "+pt.proc.MasterName, pt.panicRepair, func() {
		ctx, cancel := context.WithTimeout(conn.Server.Context(), procRunTimeout)
		defer cancel()
		loc, err := pt.proc.Find(ctx, conn.Server.DB())
		stale := false
		if err == nil && loc == activity.ProcNone {
			if err = pt.proc.Install(ctx, conn.Server.DB(), activity.ProcTempDB); err == nil {
				loc = activity.ProcTempDB
			}
		} else if err == nil && loc == activity.ProcTempDB {
			// The tempdb copy is gossms's own and lives until a restart, so an
			// earlier gossms's body would outlive every fix to it (U1's Block
			// tab showed no parallel blocking). Replace it when it differs;
			// if that fails (another login installed it), the old copy still
			// runs, so the error is not the tab's.
			if cur, cerr := pt.proc.Current(ctx, conn.Server.DB(), loc); cerr == nil && !cur {
				_ = pt.proc.Install(ctx, conn.Server.DB(), loc)
			}
		} else if err == nil && loc == activity.ProcMaster {
			// An earlier gossms's master copy keeps its old body forever,
			// but master's may be a DBA's own, so it is offered for
			// update rather than replaced. A failed check offers nothing.
			cur, cerr := pt.proc.Current(ctx, conn.Server.DB(), loc)
			stale = cerr == nil && !cur
		}
		pt.am.app.postAndWake(func() { pt.procResolved(loc, stale, err) })
	})
}

// procResolved records where the procedure is and runs it.
func (pt *amProcTab) procResolved(loc activity.ProcLocation, stale bool, err error) {
	if !pt.am.app.panelHosted(pt.am) {
		return
	}
	pt.busy = false
	pt.loc = loc
	pt.masterStale = stale
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
		ctx, cancel := context.WithTimeout(conn.Server.Context(), procRunTimeout)
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
	// Preserving the view, not SetSource: same columns, so keep dragged
	// widths; different sessions, so don't keep the cursor row. The set itself
	// is the source, so its NULLs draw dimmed.
	pt.grid.SetSourcePreservingView(set.Columns, set)
	pt.grid.SetSelectedRow(0)
	pt.setStatus(fmt.Sprintf("%d row(s)  %s  (%s)",
		len(set.Rows), time.Now().Format("15:04:05"), qualified))
}

// confirmInstallInMaster asks before writing to master, the one thing these
// tabs do that outlives the session and touches a system database. Over a
// stale master copy it is Update in master, which overwrites that copy.
func (pt *amProcTab) confirmInstallInMaster() {
	title := "Install " + pt.proc.MasterName + " in master"
	text := "Create " + pt.proc.Qualified(activity.ProcMaster) + "? This writes a stored procedure into a system database."
	if pt.loc == activity.ProcMaster {
		title = "Update " + pt.proc.MasterName + " in master"
		text = "Replace " + pt.proc.Qualified(activity.ProcMaster) +
			" with this build's version? Its current definition differs and is overwritten."
	}
	pt.am.app.confirmDialog.ShowConfirm(title, text,
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
		pt.masterInstalled(err, cancelled)
	})
}

// masterInstalled reports the install and re-runs against the new copy. The
// cancelled message is built here, not as an error, so it can start with a
// capital the way every other status line does (staticcheck ST1005).
func (pt *amProcTab) masterInstalled(err error, cancelled bool) {
	if !pt.am.app.panelHosted(pt.am) {
		return
	}
	pt.busy = false
	if cancelled {
		pt.setStatus("Install of " + pt.proc.Qualified(activity.ProcMaster) + " cancelled")
		pt.am.buildTools()
		return
	}
	if err != nil {
		pt.setStatus(err.Error())
		pt.am.buildTools()
		return
	}
	pt.loc = activity.ProcMaster
	pt.masterStale = false
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
