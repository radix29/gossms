package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---- Panel actions ----

func (a *App) newQueryPanel() {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc, database := a.selectedConnTarget(); sc != nil {
		a.connectForQueryPanel(qp, sc, database, nil)
	}
}

// newQueryPanelForConn opens a query panel with its own connection cloned from
// sc, in the given database context ("" = the connection's default).
func (a *App) newQueryPanelForConn(sc *db.ServerConn, database string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database, nil)
	}
}

func (a *App) openQueryWithText(sc *db.ServerConn, database, text string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.editor.SetText(text)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database, nil)
	}
}

// openQueryWithTextAndExecute is openQueryWithText plus an immediate run, for
// actions like "View Backup History" that hand the user a live result set
// rather than a query to review. A nil sc opens the panel disconnected.
func (a *App) openQueryWithTextAndExecute(sc *db.ServerConn, database, text string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.editor.SetText(text)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database, func() { qp.Execute() })
	}
}

// openQueryFile runs File > Open: prompts for a path and loads its content into
// a new query panel, never the active one, as SSMS does. The panel's title then
// tracks the file's name.
func (a *App) openQueryFile() {
	a.fileDialog.ShowOpen("Open Query File", "", func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			a.setStatus(fmt.Sprintf("Open failed: %v", err))
			return
		}
		if strings.EqualFold(filepath.Ext(path), sqlPlanExt) {
			a.openPlanFile(path, data)
			return
		}
		a.queryPanelCnt++
		qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
		text, enc, crlf, lossy := decodeTextFile(data)
		qp.editor.SetText(text)
		// Read back from the editor, never from text: SetText expands tabs, so
		// seeding savedText from the source marks a file with one tab in it
		// dirty the moment it opens, and closing it then prompts to save,
		// rewriting a file the user never touched.
		qp.savedText = qp.editor.Text()
		qp.filePath = path
		qp.fileEnc, qp.fileCRLF = enc, crlf
		if strings.EqualFold(filepath.Ext(path), ".xml") {
			qp.editor.SetHighlighter(controls.XMLHighlighter(theme.Active()))
		}
		a.panels.SetActive(a.panels.AddPanel(qp))
		a.focusPanels()
		if lossy {
			a.setStatus("Opened " + path + " — not valid UTF-8; undecodable bytes shown as � and saving will replace them")
		} else {
			a.setStatus("Opened " + path)
		}
		if sc, database := a.selectedConnTarget(); sc != nil {
			a.connectForQueryPanel(qp, sc, database, nil)
		}
	})
}

// sqlPlanExt is the extension SSMS gives a saved execution plan, and what
// File > Open keys off to show one rather than treating it as a script.
const sqlPlanExt = ".sqlplan"

// openPlanFile shows an already-read .sqlplan in its own PlanPanel, through
// decodeTextFile like every other file gossms reads: SSMS writes .sqlplan as
// UTF-16, identified by its BOM (see text_encoding.go).
func (a *App) openPlanFile(path string, data []byte) {
	text, _, _, _ := decodeTextFile(data)
	plan, err := showplan.Parse([]byte(text))
	if err != nil {
		a.setStatus(fmt.Sprintf("Could not parse %s: %v", filepath.Base(path), err))
		return
	}
	pp := NewPlanPanel(a, filepath.Base(path), plan)
	pp.filePath = path
	a.panels.SetActive(a.panels.AddPanel(pp))
	a.focusPanels()
	a.setStatus("Opened " + path)
}

// savePlanPanel runs File > Save / Save As... for a plan panel, writing the
// plan's source XML back out. A panel opened from a file saves straight back to
// it; anything else prompts.
//
// The XML is written as UTF-8 rather than the UTF-16 SSMS emits: it is the
// decoded showplan document, and re-encoding it would need the declaration
// rewritten to match or the file would announce an encoding it isn't in.
func (a *App) savePlanPanel(pp *PlanPanel, saveAs bool) {
	xml := pp.PlanXML()
	if xml == "" {
		a.setStatus("No execution plan to save")
		return
	}
	write := func(path string) {
		if a.writePlanFile(path, xml) {
			pp.filePath = path
		}
	}
	if !saveAs && pp.filePath != "" {
		write(pp.filePath)
		return
	}
	initial := pp.filePath
	if initial == "" {
		initial = "plan" + sqlPlanExt
	}
	a.fileDialog.ShowSave("Save Execution Plan", initial, write)
}

// saveExecutionPlanAs runs File > Save Execution Plan As..., which works from a
// detached plan panel and from a query panel's Execution Plan tab alike —
// unlike Save, which in a query panel means the script.
func (a *App) saveExecutionPlanAs() {
	switch p := a.panels.ActivePanel().(type) {
	case *PlanPanel:
		a.savePlanPanel(p, true)
	case *QueryPanel:
		plan := a.activePlan()
		if plan == nil {
			a.setStatus("No execution plan to save")
			return
		}
		a.fileDialog.ShowSave("Save Execution Plan", p.Title()+sqlPlanExt, func(path string) {
			a.writePlanFile(path, plan.XML)
		})
	default:
		a.setStatus("No execution plan to save")
	}
}

// activePlan returns the plan the active panel is showing — a detached plan
// panel's, or a query panel's Execution Plan tab — or nil when there is none.
func (a *App) activePlan() *showplan.Plan {
	switch p := a.panels.ActivePanel().(type) {
	case *PlanPanel:
		return p.planView.Plan()
	case *QueryPanel:
		if p.planView != nil {
			return p.planView.Plan()
		}
	}
	return nil
}

// writePlanFile writes one plan document to path, reporting success.
func (a *App) writePlanFile(path, xml string) bool {
	if err := fileutil.WriteAtomic(path, []byte(xml), 0o644); err != nil {
		a.setStatus(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	a.setStatus("Saved to " + path)
	return true
}

// closePanelAt removes the panel at index i, first releasing what it owns: an
// Activity Monitor's collector and per-tab connections, or a QueryPanel's
// dedicated connection. Also cancels any in-flight query or plan fetch, which
// would otherwise run to completion server-side and fire its postEvent closure
// against a panel that is no longer hosted.
func (a *App) closePanelAt(i int) {
	if am, ok := a.panels.PanelAt(i).(*ActivityMonitor); ok {
		am.Close()
	}
	if dash, ok := a.panels.PanelAt(i).(*AGDashboard); ok {
		dash.Close()
	}
	if lv, ok := a.panels.PanelAt(i).(*LogViewer); ok {
		lv.Close()
	}
	if qp, ok := a.panels.PanelAt(i).(*QueryPanel); ok {
		if qp.executing && qp.cancel != nil {
			qp.cancel()
		}
		if qp.conn != nil {
			qp.conn.Close()
		}
	}
	a.panels.RemovePanel(i)
}

// closePanelByPointer closes p by locating its current index, for callbacks
// holding only the panel, whose index may have shifted while they were
// pending.
func (a *App) closePanelByPointer(p layout.Panel) {
	if i := a.panels.FindIndex(func(x layout.Panel) bool { return x == p }); i >= 0 {
		a.closePanelAt(i)
	}
}

// panelHosted reports whether p is still one of App's live panels — how an
// async operation that captured a panel pointer detects the panel being closed
// while it was in flight.
func (a *App) panelHosted(p layout.Panel) bool {
	return a.panels.FindIndex(func(x layout.Panel) bool { return x == p }) >= 0
}

// requestClosePanel implements Ctrl+W / File > Close and a tab's [x] button:
// closes the panel at i outright, unless it is a QueryPanel with unsaved
// changes, which prompts to save first. A panel whose layout.Closable reports
// false (Object Explorer Details) can't be closed at all — the tab bar omits
// its [x], and this is Ctrl+W's backstop.
func (a *App) requestClosePanel(i int) {
	if !layout.PanelClosable(a.panels.PanelAt(i)) {
		return
	}
	qp, ok := a.panels.PanelAt(i).(*QueryPanel)
	if !ok || !qp.Dirty() {
		a.closePanelAt(i)
		return
	}
	// Three-way, not Yes/No: "No" here discards the panel's unsaved SQL, so
	// Escape must not silently mean it — see ShowConfirmCancel.
	a.confirmDialog.ShowConfirmCancel("Close Query",
		qp.Title()+" has unsaved changes. Save before closing?",
		func(answer dialogs.ConfirmAnswer) {
			switch answer {
			case dialogs.ConfirmYes:
				a.saveQueryPanel(qp, false, func() { a.closePanelByPointer(qp) })
			case dialogs.ConfirmNo:
				a.closePanelByPointer(qp)
			}
			// ConfirmCancel: the panel stays open, unsaved.
		})
}

// requestQuit implements Ctrl+Q / File > Exit: offers to save every query panel
// with unsaved changes before tearing the screen down, and abandons the quit if
// any prompt is cancelled. quit() itself is unconditional, so without this
// Ctrl+Q discards every dirty panel with no prompt.
func (a *App) requestQuit() (quitting bool) {
	dirty := a.dirtyQueryPanels()
	if len(dirty) == 0 {
		a.quit()
		return true
	}
	a.askSaveBeforeQuit(dirty, 0)
	return false
}

// dirtyQueryPanels lists every open query panel with unsaved changes, in
// tab order.
func (a *App) dirtyQueryPanels() []*QueryPanel {
	var dirty []*QueryPanel
	for i := 0; i < a.panels.Count(); i++ {
		if qp, ok := a.panels.PanelAt(i).(*QueryPanel); ok && qp.Dirty() {
			dirty = append(dirty, qp)
		}
	}
	return dirty
}

// askSaveBeforeQuit walks panels from index i, prompting for each still-dirty
// one and quitting once it runs off the end. Recursion through the dialog's
// callback rather than a loop, since each prompt must be answered — and a Yes
// must finish writing — before the next is asked. Panels are re-checked as they
// are reached: an earlier Save As may have targeted a file another panel also
// shows, and a panel may have been closed from the prompt chain itself.
//
// A cancelled prompt, or a Save backed out of at the file dialog, stops the
// walk and leaves the app open.
func (a *App) askSaveBeforeQuit(panels []*QueryPanel, i int) {
	for i < len(panels) && (!a.panelHosted(panels[i]) || !panels[i].Dirty()) {
		i++
	}
	if i >= len(panels) {
		a.quit()
		return
	}
	qp := panels[i]
	a.confirmDialog.ShowConfirmCancel("Exit goSSMS",
		qp.Title()+" has unsaved changes. Save before exiting?",
		func(answer dialogs.ConfirmAnswer) {
			switch answer {
			case dialogs.ConfirmYes:
				a.saveQueryPanel(qp, false, func() { a.askSaveBeforeQuit(panels, i+1) })
			case dialogs.ConfirmNo:
				a.askSaveBeforeQuit(panels, i+1)
			}
			// ConfirmCancel: abandon the quit; every panel stays as it is.
		})
}

func (a *App) closeActivePanel() {
	if i := a.panels.ActiveIndex(); i >= 0 {
		a.requestClosePanel(i)
	}
}

func (a *App) executeActiveQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.Execute() })
}

// executeSelectedQuery runs the toolbar's "Execute Selection" button.
func (a *App) executeSelectedQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.ExecuteSelection() })
}

// activeQueryPanel returns the active panel as a *QueryPanel, or nil if it
// isn't one — the type assertion every Query-menu action needs.
func (a *App) activeQueryPanel() *QueryPanel {
	if p := a.panels.ActivePanel(); p != nil {
		if qp, ok := p.(*QueryPanel); ok {
			return qp
		}
	}
	return nil
}

// withQueryPanel runs fn on the active query panel, or says there isn't one.
// Every Query-menu action and toolbar button that acts on the editor goes
// through it, so none of them can be the one that quietly does nothing when
// the active panel is a plan or a dashboard — see CLAUDE.md § Application
// rules on context-gating. The Enabled predicates gate the same actions
// ahead of the click; this is what happens when one is reached anyway.
func (a *App) withQueryPanel(fn func(*QueryPanel)) {
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus(noActiveQueryPanelMessage)
		return
	}
	fn(qp)
}

// noActiveQueryPanelMessage is the one wording used everywhere an action needs
// a query panel and the active panel isn't one — the counterpart to
// notConnectedMessage.
const noActiveQueryPanelMessage = "No active query panel"

// showEstimatedExecutionPlan runs the toolbar's "Show Estimated Execution
// Plan" button.
func (a *App) showEstimatedExecutionPlan() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.ShowEstimatedPlan() })
}

// toggleActualExecutionPlan flips whether Execute captures the actual
// (post-run) execution plan alongside a query's results, rebuilding the toolbar
// and Query menu to match.
func (a *App) toggleActualExecutionPlan() {
	a.actualPlanEnabled = !a.actualPlanEnabled
	a.toolbar.SetButtons(a.buildToolbar())
	a.menuBar.SetMenus(a.buildMenus())
	a.layoutAll()
	state := "off"
	if a.actualPlanEnabled {
		state = "on"
	}
	a.setStatus("Include Actual Execution Plan: " + state)
}

// toggleOutputColumnMeta flips "Show Output Column Metadata", which lists each
// result set's columns and declared types in the Messages tab. Applies to the
// next execution; results on screen are not re-rendered.
func (a *App) toggleOutputColumnMeta() {
	a.metaEnabled = !a.metaEnabled
	a.toolbar.SetButtons(a.buildToolbar())
	a.menuBar.SetMenus(a.buildMenus())
	a.layoutAll()
	state := "off"
	if a.metaEnabled {
		state = "on"
	}
	a.setStatus("Show Output Column Metadata: " + state)
}

// openPlanPanel opens a new detached panel showing plan — the Execution Plan
// tab's "[ Expand ]" action. Every call adds a new panel.
func (a *App) openPlanPanel(title string, plan *showplan.Plan) {
	a.panels.SetActive(a.panels.AddPanel(NewPlanPanel(a, title, plan)))
	a.focusPanels()
}

// openPlanComparePanel opens a plan comparison in its own panel, the way
// openPlanPanel opens a single plan.
func (a *App) openPlanComparePanel(title string, left, right *showplan.Plan) {
	a.panels.SetActive(a.panels.AddPanel(NewPlanComparePanel(a, title, left, right)))
	a.focusPanels()
}

// cancelExecutingQuery runs Query > Cancel Executing Query.
func (a *App) cancelExecutingQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.CancelExecution() })
}

// reconnectActiveQuery runs Query > Reconnect.
func (a *App) reconnectActiveQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.Reconnect() })
}

// refreshCompletionCache runs Query > Refresh IntelliSense Cache.
func (a *App) refreshCompletionCache() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.refreshCompletionCache() })
}

// setResultsMode runs Query > Results To Grid/Text/File.
func (a *App) setResultsMode(mode ResultsMode) {
	a.withQueryPanel(func(qp *QueryPanel) { qp.SetResultsMode(mode) })
}

// saveQuery runs File > Save (saveAs=false) or File > Save As... (saveAs=true).
func (a *App) saveQuery(saveAs bool) {
	// A plan panel holds no editable text — its Save is the .sqlplan.
	if pp, ok := a.panels.ActivePanel().(*PlanPanel); ok {
		a.savePlanPanel(pp, saveAs)
		return
	}
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus("No active query to save")
		return
	}
	a.saveQueryPanel(qp, saveAs, nil)
}

// saveQueryPanel saves qp — straight to qp.filePath when it has one and saveAs
// is false, otherwise via a path prompt — calling then only once the write
// succeeds, so requestClosePanel's panel closes only after the changes land.
func (a *App) saveQueryPanel(qp *QueryPanel, saveAs bool, then func()) {
	if !saveAs && qp.filePath != "" {
		if a.writeQueryFile(qp, qp.filePath) && then != nil {
			then()
		}
		return
	}
	initial := qp.filePath
	if initial == "" {
		initial = "query.sql"
	}
	title := "Save Query"
	if saveAs {
		title = "Save Query As"
	}
	a.fileDialog.ShowSave(title, initial, func(path string) {
		if a.writeQueryFile(qp, path) && then != nil {
			then()
		}
	})
}

// writeQueryFile writes qp's editor content to path, reporting whether it
// succeeded so callers can proceed only then.
//
// The bytes are re-encoded in whatever shape the panel's file was opened in; a
// panel never opened from a file writes LF-separated UTF-8, fileEnc/fileCRLF's
// zero value. WriteAtomic for the same reason config.Save uses it: a plain
// os.WriteFile truncates first, so a full disk mid-write leaves the user with
// neither their script nor the file it replaced.
func (a *App) writeQueryFile(qp *QueryPanel, path string) bool {
	if err := fileutil.WriteAtomic(path, encodeTextFile(qp.editor.Text(), qp.fileEnc, qp.fileCRLF), 0o644); err != nil {
		a.setStatus(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	qp.filePath = path
	qp.savedText = qp.editor.Text()
	a.setStatus("Saved to " + path)
	return true
}

// showObjectExplorerDetails runs View > Object Explorer Details, reopening the
// DetailBrowser panel if it was closed.
func (a *App) showObjectExplorerDetails() {
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		_, ok := p.(*DetailBrowser)
		return ok
	})
	if idx < 0 {
		a.detailBrowser = a.newDetailBrowser()
		idx = a.panels.AddPanel(a.detailBrowser)
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showQueryList runs Tools > Query List.
func (a *App) showQueryList() {
	a.queryListDialog.Show()
}

// showActivityMonitor runs Tools > Activity Monitor and the toolbar's 📈 button
// on whichever server the selected Object Explorer node belongs to, falling
// back to the first connection — the same resolution as showServerProperties.
func (a *App) showActivityMonitor() {
	if sc := a.connOrFirst(); sc != nil {
		a.showActivityMonitorFor(sc)
	}
}

// showActivityMonitorFor opens Activity Monitor for a known connection — the
// shared entry point for the Tools menu/toolbar and the Object Explorer server
// node's context menu. One panel per server: reopening raises the existing one
// instead of starting a second collector against the same instance.
func (a *App) showActivityMonitorFor(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		am, ok := p.(*ActivityMonitor)
		return ok && am.conn == sc
	})
	if idx < 0 {
		am := NewActivityMonitor(a, sc)
		idx = a.panels.AddPanel(am)
		// After AddPanel: the connect callback checks panelHosted, which is
		// only true once the panel is in a.panels.
		a.connectForActivityMonitor(am, sc)
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showLogViewerFor opens the Log File Viewer on one log file of sc — the Object
// Explorer entry point for the server node, the SQL Server Logs and Agent Error
// Logs folders, and each log-file leaf. One panel per server, like
// showActivityMonitorFor: a second log file re-points the existing panel rather
// than accumulating a tab per archive, as SSMS's viewer does.
func (a *App) showLogViewerFor(sc *db.ServerConn, logType gosmo.ErrorLogType, logNum int) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		lv, ok := p.(*LogViewer)
		return ok && lv.conn == sc
	})
	if idx < 0 {
		lv := NewLogViewer(a, sc, logType, logNum)
		idx = a.panels.AddPanel(lv)
		lv.Load()
	} else {
		a.panels.PanelAt(idx).(*LogViewer).ShowLog(logType, logNum)
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showQueryStorePanelFor opens the Query Store panel on one database, on the
// report title names — the Object Explorer entry point for the Query Store
// folder and each of its seven report leaves. One panel per (server,
// database), like showLogViewerFor: another report re-points the existing
// panel rather than accumulating a tab per view.
//
// An empty title means "no report in particular", which is what the folder's
// own Open Query Store... passes. It opens a new panel on the first report and
// leaves an existing one where it is — see the branch below.
func (a *App) showQueryStorePanelFor(sc *db.ServerConn, dbName, title string) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		qs, ok := p.(*QueryStorePanel)
		return ok && qs.conn == sc && qs.dbName == dbName
	})
	if idx < 0 {
		qs := NewQueryStorePanel(a, sc, dbName, title)
		idx = a.panels.AddPanel(qs)
		qs.Load()
	} else if title != "" {
		// Only for a leaf, which names its report. queryStoreReportIndex maps
		// an unrecognised title — "" included — to report 0, which is the right
		// answer for a panel being created and the wrong one for a panel
		// already open: Open Query Store... on the folder would throw away the
		// view the user was reading and re-run it as Regressed Queries.
		a.panels.PanelAt(idx).(*QueryStorePanel).ShowReport(title)
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

func (a *App) refreshSelected() { a.explorer.RefreshSelected() }

func (a *App) showServerProperties() {
	if sc := a.connOrFirst(); sc != nil {
		a.showServerPropertiesFor(sc)
	}
}

// showServerPropertiesFor opens Server Properties for a known connection —
// the shared entry point for the Tools menu and the Object Explorer context
// menu.
func (a *App) showServerPropertiesFor(sc *db.ServerConn) {
	a.propDialog.show(sc, "", "Server Properties", "Instance: "+sc.Opts.Server, "Connected: yes",
		func() []propPage { return serverPropPages(sc) })
}

// showNewDatabaseDialog opens New Database for a known connection — the
// Object Explorer context menu on both the server node and the "Databases"
// folder.
func (a *App) showNewDatabaseDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newDatabaseDialog.show(sc)
}

// showDatabaseProperties runs Tools > Database Properties on whichever
// database the selected Object Explorer node belongs to — nodeData.DBName is
// propagated to every node under a database.
func (a *App) showDatabaseProperties() {
	node := a.explorer.Selected()
	if node == nil || node.data.DBName == "" {
		a.setStatus("Select a database (or an object within one) in Object Explorer first")
		return
	}
	a.showDatabasePropertiesFor(resolveConn(node), node.data.DBName)
}

// showDatabasePropertiesFor opens Database Properties for a known connection
// and database — the shared entry point for the Tools menu and the Object
// Explorer context menu.
func (a *App) showDatabasePropertiesFor(sc *db.ServerConn, dbName string) {
	a.propDialog.show(sc, dbName, "Database Properties", "Database: "+dbName, "Server: "+sc.Opts.Server,
		func() []propPage { return databasePropPages(sc, dbName) })
}

// showAGPropertiesFor opens Availability Group Properties for a group on sc,
// from the Object Explorer context menu. sc need not be the group's primary:
// every page follows the primary itself and reports an error when it can't.
func (a *App) showAGPropertiesFor(sc *db.ServerConn, agName string) {
	a.propDialog.show(sc, "", "Availability Group Properties",
		"Availability group: "+agName, "Server: "+sc.Opts.Server,
		func() []propPage { return agPropPages(sc, agName) })
}

// showAGDashboardFor opens the Always On dashboard — the context menu's "Show
// Dashboard", on a group or, with an empty agName, on the Always On root for
// every group at once.
//
// One panel per (connection, group): reopening raises the existing one rather
// than starting a second poller against the same primary. The all-groups view
// is one more such key, so it coexists with any number of per-group panels.
func (a *App) showAGDashboardFor(sc *db.ServerConn, agName string) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		dash, ok := p.(*AGDashboard)
		return ok && dash.conn == sc && strings.EqualFold(dash.agName, agName)
	})
	if idx < 0 {
		idx = a.panels.AddPanel(NewAGDashboard(a, sc, agName))
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showNewLoginDialog opens New Login for a known connection — the Object
// Explorer context menu on Security > Logins.
func (a *App) showNewLoginDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newLoginDialog.show(sc)
}

// showBackupDialog opens Back Up Database for a known connection — the Object
// Explorer context menu on a database node or the "Databases" folder
// (dbName "").
func (a *App) showBackupDialog(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.backupDialog.show(sc, dbName)
}

// showRestoreDialog opens Restore Database for a known connection — the Object
// Explorer context menu on a database node or the "Databases" folder
// (dbName "").
func (a *App) showRestoreDialog(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.restoreDialog.show(sc, dbName)
}

// showBackupHistoryFor opens a new query window scoped to msdb, where the backup
// catalog lives whichever database the menu was opened from, pre-filled with
// backupHistoryQuery(dbName) and run immediately — the context menu's "View
// Backup History...", database node only.
func (a *App) showBackupHistoryFor(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.openQueryWithTextAndExecute(sc, "msdb", backupHistoryQuery(dbName))
}

// showLoginProperties opens Login Properties for a login on sc.
func (a *App) showLoginProperties(sc *db.ServerConn, loginName string) {
	a.propDialog.show(sc, "", "Login Properties", "Login: "+loginName, "Server: "+sc.Opts.Server,
		func() []propPage { return loginPropPages(a.propDialog, sc, loginName) })
}

// showTablePropertiesFor opens Table Properties for a known connection,
// database, and schema-qualified table, from the Object Explorer context menu.
func (a *App) showTablePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Table Properties", "Table: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return tablePropPages(sc, dbName, schema, name) })
}

// showIndexPropertiesFor opens Index Properties for a known connection,
// database, and schema-qualified table/index, from the context menu.
func (a *App) showIndexPropertiesFor(sc *db.ServerConn, dbName, schema, table, index string) {
	a.propDialog.show(sc, dbName, "Index Properties", "Index: "+index, "Table: "+fqn(schema, table),
		func() []propPage { return indexPropPages(a.propDialog, sc, dbName, schema, table, index) })
}

// showStatisticPropertiesFor opens Statistics Properties for a known
// connection, database, and schema-qualified table/statistic.
func (a *App) showStatisticPropertiesFor(sc *db.ServerConn, dbName, schema, table, stat string) {
	a.propDialog.show(sc, dbName, "Statistics Properties", "Statistic: "+stat, "Table: "+fqn(schema, table),
		func() []propPage { return statisticPropPages(a.propDialog, sc, dbName, schema, table, stat) })
}

// showForeignKeyPropertiesFor opens the read-only Foreign Key Properties for a
// known connection, database, and schema-qualified table/foreign key.
func (a *App) showForeignKeyPropertiesFor(sc *db.ServerConn, dbName, schema, table, fk string) {
	a.propDialog.show(sc, dbName, "Foreign Key Properties", "Key: "+fk, "Table: "+fqn(schema, table),
		func() []propPage { return fkPropPages(sc, dbName, schema, table, fk) })
}

// showKeyPropertiesFor opens Primary/Unique Key Properties for a known
// connection, database, and schema-qualified table/key. isPrimaryKey picks the
// dialog title and comes off the tree node's nodeData, which loadKeysChildren
// already knows.
func (a *App) showKeyPropertiesFor(sc *db.ServerConn, dbName, schema, table, key string, isPrimaryKey bool) {
	title := keyTypeName(isPrimaryKey) + " Properties"
	a.propDialog.show(sc, dbName, title, "Key: "+key, "Table: "+fqn(schema, table),
		func() []propPage { return keyPropPages(a.propDialog, sc, dbName, schema, table, key) })
}

// showRolePropertiesFor opens Database Role Properties for a known connection,
// database, and role name.
func (a *App) showRolePropertiesFor(sc *db.ServerConn, dbName, roleName string) {
	a.propDialog.show(sc, dbName, "Database Role Properties", "Role: "+roleName, "Database: "+dbName,
		func() []propPage { return rolePropPages(a.propDialog, sc, dbName, roleName) })
}

// showServerRolePropertiesFor opens Server Role Properties for a known
// connection and role name — a server-level principal with no dbName, like
// showLoginProperties.
func (a *App) showServerRolePropertiesFor(sc *db.ServerConn, roleName string) {
	a.propDialog.show(sc, "", "Server Role Properties", "Role: "+roleName, "Server: "+sc.Opts.Server,
		func() []propPage { return serverRolePropPages(sc, roleName) })
}

// showUserPropertiesFor opens Database User Properties for a known connection,
// database, and user name.
func (a *App) showUserPropertiesFor(sc *db.ServerConn, dbName, userName string) {
	a.propDialog.show(sc, dbName, "Database User Properties", "User: "+userName, "Database: "+dbName,
		func() []propPage { return userPropPages(a.propDialog, sc, dbName, userName) })
}

// showSchemaPropertiesFor opens Schema Properties for a known connection,
// database, and schema name.
func (a *App) showSchemaPropertiesFor(sc *db.ServerConn, dbName, schemaName string) {
	a.propDialog.show(sc, dbName, "Schema Properties", "Schema: "+schemaName, "Database: "+dbName,
		func() []propPage { return schemaPropPages(sc, dbName, schemaName) })
}

// showPartitionFunctionPropertiesFor opens the read-only Partition Function
// Properties for a known connection and database.
func (a *App) showPartitionFunctionPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Partition Function Properties", "Function: "+name, "Database: "+dbName,
		func() []propPage { return partitionFunctionPropPages(sc, dbName, name) })
}

// showPartitionSchemePropertiesFor opens the read-only Partition Scheme
// Properties.
func (a *App) showPartitionSchemePropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Partition Scheme Properties", "Scheme: "+name, "Database: "+dbName,
		func() []propPage { return partitionSchemePropPages(sc, dbName, name) })
}

// showSecurityPolicyPropertiesFor opens the read-only Security Policy
// Properties.
func (a *App) showSecurityPolicyPropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Security Policy Properties", "Policy: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return securityPolicyPropPages(sc, dbName, schema, name) })
}

// showColumnMasterKeyPropertiesFor opens the read-only Column Master Key
// Properties.
func (a *App) showColumnMasterKeyPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Column Master Key Properties", "Key: "+name, "Database: "+dbName,
		func() []propPage { return columnMasterKeyPropPages(sc, dbName, name) })
}

// showColumnEncryptionKeyPropertiesFor opens the read-only Column Encryption
// Key Properties.
func (a *App) showColumnEncryptionKeyPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Column Encryption Key Properties", "Key: "+name, "Database: "+dbName,
		func() []propPage { return columnEncryptionKeyPropPages(sc, dbName, name) })
}

// canSaveActivePanel gates File > Save / Save As...: a query panel saves its
// script, a plan panel its .sqlplan, and nothing else has anything to write.
func (a *App) canSaveActivePanel() bool {
	if a.activeQueryPanel() != nil {
		return true
	}
	_, ok := a.panels.ActivePanel().(*PlanPanel)
	return ok
}
