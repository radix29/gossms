package tui

import (
	"fmt"
	"os"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// ---- Panel actions ----

func (a *App) newQueryPanel() {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc, database := a.selectedConnTarget(); sc != nil {
		a.connectForQueryPanel(qp, sc, database)
	}
}

// newQueryPanelForConn opens a query panel with its own dedicated
// connection cloned from sc, running in the given database context
// ("" = the connection's default database).
func (a *App) newQueryPanelForConn(sc *db.ServerConn, database string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database)
	}
}

func (a *App) openQueryWithText(sc *db.ServerConn, database, text string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.editor.SetText(text)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database)
	}
}

// openQueryFile runs File > Open: prompts for a path, reads it, and loads
// the content into a brand new query panel (never into the currently
// active one, matching "Open...opens a file as a new Query"). The panel's
// title then tracks the opened file's name, via QueryPanel.Title().
func (a *App) openQueryFile() {
	a.fileDialog.ShowOpen("Open Query File", "", func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			a.setStatus(fmt.Sprintf("Open failed: %v", err))
			return
		}
		a.queryPanelCnt++
		qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
		qp.editor.SetText(string(data))
		qp.savedText = string(data)
		qp.filePath = path
		a.panels.SetActive(a.panels.AddPanel(qp))
		a.focusPanels()
		a.setStatus("Opened " + path)
		if sc, database := a.selectedConnTarget(); sc != nil {
			a.connectForQueryPanel(qp, sc, database)
		}
	})
}

// closePanelAt removes the panel at index i, closing its dedicated
// connection first if it's a QueryPanel — every query panel owns its own
// connection (see connectForQueryPanel), so nothing else references it. Also
// cancels an in-flight query/plan fetch, if any — otherwise it keeps running
// server-side until completion and its postEvent completion closure fires
// against a panel that's no longer hosted (guarded there via panelHosted).
func (a *App) closePanelAt(i int) {
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

// closePanelByPointer closes p by locating its current index — used from
// callbacks (a confirm dialog, a save's completion) that only hold the
// panel itself, since the index may have shifted while they were pending.
func (a *App) closePanelByPointer(p layout.Panel) {
	if i := a.panels.FindIndex(func(x layout.Panel) bool { return x == p }); i >= 0 {
		a.closePanelAt(i)
	}
}

// panelHosted reports whether p is still one of App's live panels — used by
// an async operation (e.g. connectForQueryPanel) that captured a panel
// pointer before its background goroutine started, to detect that the
// panel was closed while the operation was still in flight.
func (a *App) panelHosted(p layout.Panel) bool {
	return a.panels.FindIndex(func(x layout.Panel) bool { return x == p }) >= 0
}

// requestClosePanel implements Ctrl+W / File > Close and a tab's [x]
// button: closes the panel at i outright, unless it's a QueryPanel with
// unsaved changes, in which case it asks whether to save first. A panel
// that implements layout.Closable and returns false (Object Explorer
// Details) can't be closed at all — the tab bar already omits its [x], this
// is just Ctrl+W's own backstop.
func (a *App) requestClosePanel(i int) {
	if c, ok := a.panels.PanelAt(i).(layout.Closable); ok && !c.Closable() {
		return
	}
	qp, ok := a.panels.PanelAt(i).(*QueryPanel)
	if !ok || !qp.Dirty() {
		a.closePanelAt(i)
		return
	}
	a.confirmDialog.ShowConfirm("Close Query",
		qp.Title()+" has unsaved changes. Save before closing?",
		func(save bool) {
			if !save {
				a.closePanelByPointer(qp)
				return
			}
			a.saveQueryPanel(qp, false, func() { a.closePanelByPointer(qp) })
		})
}

func (a *App) closeActivePanel() {
	if i := a.panels.ActiveIndex(); i >= 0 {
		a.requestClosePanel(i)
	}
}

func (a *App) executeActiveQuery() {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.Execute()
	}
}

// executeSelectedQuery runs the toolbar's "Execute Selection" button.
func (a *App) executeSelectedQuery() {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.ExecuteSelection()
	} else {
		a.setStatus("No active query panel")
	}
}

// activeQueryPanel returns the active panel as a *QueryPanel, or nil if the
// active panel isn't a query panel (or there is none). Centralises the
// type-assertion every Query-menu action needs.
func (a *App) activeQueryPanel() *QueryPanel {
	if p := a.panels.ActivePanel(); p != nil {
		if qp, ok := p.(*QueryPanel); ok {
			return qp
		}
	}
	return nil
}

// showEstimatedExecutionPlan runs the toolbar's "Show Estimated Execution
// Plan" button.
func (a *App) showEstimatedExecutionPlan() {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.ShowEstimatedPlan()
	} else {
		a.setStatus("No active query panel")
	}
}

// toggleActualExecutionPlan flips whether Execute captures the actual
// (post-run) execution plan alongside a query's normal results — see
// App.actualPlanEnabled and QueryPanel.setResultPlan. Rebuilds the toolbar
// and Query menu so both immediately reflect the new state, mirroring how
// buildToolbar/buildMenus are populated once at startup (see Run) — nothing
// else currently mutates either afterward.
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

// openPlanPanel opens a new detached panel showing plan, titled title —
// the Execution Plan tab's "[ Expand ]" button's action (see
// QueryPanel.newPlanView). Every call adds a brand-new panel, same as
// newQueryPanel; nothing is reused across calls.
func (a *App) openPlanPanel(title string, plan *showplan.Plan) {
	a.panels.SetActive(a.panels.AddPanel(NewPlanPanel(title, plan)))
	a.focusPanels()
}

// cancelExecutingQuery runs Query > Cancel Executing Query.
func (a *App) cancelExecutingQuery() {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.CancelExecution()
	} else {
		a.setStatus("No active query panel")
	}
}

// refreshCompletionCache runs Query > Refresh IntelliSense Cache.
func (a *App) refreshCompletionCache() {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.refreshCompletionCache()
	} else {
		a.setStatus("No active query panel")
	}
}

// setResultsMode runs Query > Results To Grid/Text/File.
func (a *App) setResultsMode(mode ResultsMode) {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.SetResultsMode(mode)
	} else {
		a.setStatus("No active query panel")
	}
}

// saveQuery runs File > Save (saveAs=false) or File > Save As... (saveAs=true).
func (a *App) saveQuery(saveAs bool) {
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus("No active query to save")
		return
	}
	a.saveQueryPanel(qp, saveAs, nil)
}

// saveQueryPanel saves qp — straight to qp.filePath if it has one and
// saveAs is false, otherwise via a Save/Save As path prompt — and calls
// then only once the write actually succeeds. then is nil for a plain
// File > Save/Save As; requestClosePanel passes one so the panel only
// closes after its unsaved changes have safely landed on disk.
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
// succeeded — callers that only want to proceed on success (see
// saveQueryPanel) check the return value instead of assuming it worked.
func (a *App) writeQueryFile(qp *QueryPanel, path string) bool {
	if err := os.WriteFile(path, []byte(qp.editor.Text()), 0644); err != nil {
		a.setStatus(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	qp.filePath = path
	qp.savedText = qp.editor.Text()
	a.setStatus("Saved to " + path)
	return true
}

// showObjectExplorerDetails runs View > Object Explorer Details, reopening
// the DetailBrowser panel if the user had closed it.
func (a *App) showObjectExplorerDetails() {
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		_, ok := p.(*DetailBrowser)
		return ok
	})
	if idx < 0 {
		idx = a.panels.AddPanel(NewDetailBrowser("Object Explorer Details"))
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showQueryList runs Tools > Query List.
func (a *App) showQueryList() {
	a.queryListDialog.Show()
}

// showActivityMonitor runs Tools > Activity Monitor and the toolbar's 📈
// button, using whichever server the currently selected Object Explorer
// node belongs to (falling back to the first connection) — same
// resolution as showServerProperties.
func (a *App) showActivityMonitor() {
	var sc *db.ServerConn
	if node := a.explorer.Selected(); node != nil {
		sc = resolveConn(node)
	}
	if sc == nil {
		if len(a.connections) == 0 {
			a.setStatus("Not connected — use File > Connect")
			return
		}
		sc = a.connections[0]
	}
	a.showActivityMonitorFor(sc)
}

// showActivityMonitorFor opens Activity Monitor for a known connection —
// the shared entry point for the Tools menu/toolbar (via
// showActivityMonitor, which resolves sc first) and the Object Explorer
// server node's context menu (which already has sc in hand). Not
// implemented yet.
func (a *App) showActivityMonitorFor(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.setStatus("Activity Monitor is not yet implemented")
}

func (a *App) refreshSelected() { a.explorer.RefreshSelected() }

func (a *App) showServerProperties() {
	var sc *db.ServerConn
	if node := a.explorer.Selected(); node != nil {
		sc = resolveConn(node)
	}
	if sc == nil {
		if len(a.connections) == 0 {
			a.setStatus("Not connected — use File > Connect")
			return
		}
		sc = a.connections[0]
	}
	a.showServerPropertiesFor(sc)
}

// showServerPropertiesFor opens Server Properties for a known connection —
// the shared entry point for the Tools menu (via showServerProperties,
// which resolves sc first) and the Object Explorer context menu (which
// already has sc in hand).
func (a *App) showServerPropertiesFor(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, "", "Server Properties", "Instance: "+sc.Opts.Server, "Connected: yes", serverPropPages(sc))
}

// showNewDatabaseDialog opens New Database for a known connection — the
// shared entry point for the Object Explorer context menu on both the
// server node and the "Databases" folder node.
func (a *App) showNewDatabaseDialog(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.newDatabaseDialog.show(sc)
}

// showDatabaseProperties runs Tools > Database Properties, using whichever
// database the currently selected Object Explorer node belongs to (the
// node itself, or any of its descendants — nodeData.DBName is propagated
// to every node under a database).
func (a *App) showDatabaseProperties() {
	node := a.explorer.Selected()
	if node == nil || node.data.DBName == "" {
		a.setStatus("Select a database (or an object within one) in Object Explorer first")
		return
	}
	a.showDatabasePropertiesFor(resolveConn(node), node.data.DBName)
}

// showDatabasePropertiesFor opens Database Properties for a known
// connection and database name — the shared entry point for the Tools
// menu and the Object Explorer context menu.
func (a *App) showDatabasePropertiesFor(sc *db.ServerConn, dbName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, dbName, "Database Properties", "Database: "+dbName, "Server: "+sc.Opts.Server,
		databasePropPages(sc, dbName))
}

// showNewLoginDialog opens New Login for a known connection — the Object
// Explorer context menu's entry point for Security > Logins (mirrors
// showNewDatabaseDialog).
func (a *App) showNewLoginDialog(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.newLoginDialog.show(sc)
}

// showBackupDialog opens Back Up Database for a known connection — the
// shared entry point for the Object Explorer context menu on both an
// individual database node and the "Databases" folder node (dbName "").
func (a *App) showBackupDialog(sc *db.ServerConn, dbName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.backupDialog.show(sc, dbName)
}

// showRestoreDialog opens Restore Database for a known connection — the
// shared entry point for the Object Explorer context menu on both an
// individual database node and the "Databases" folder node (dbName "").
func (a *App) showRestoreDialog(sc *db.ServerConn, dbName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.restoreDialog.show(sc, dbName)
}

// showLoginProperties opens Login Properties for a login on sc.
func (a *App) showLoginProperties(sc *db.ServerConn, loginName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, "", "Login Properties", "Login: "+loginName, "Server: "+sc.Opts.Server,
		loginPropPages(sc, loginName))
}

// showTablePropertiesFor opens Table Properties for a known connection,
// database, and schema-qualified table — the Object Explorer context
// menu's entry point (mirrors showLoginProperties/showDatabasePropertiesFor).
func (a *App) showTablePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, dbName, "Table Properties", "Table: "+fqn(schema, name), "Database: "+dbName,
		tablePropPages(sc, dbName, schema, name))
}

// showRolePropertiesFor opens Database Role Properties for a known
// connection, database, and role name — the Object Explorer context
// menu's entry point (mirrors showTablePropertiesFor).
func (a *App) showRolePropertiesFor(sc *db.ServerConn, dbName, roleName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, dbName, "Database Role Properties", "Role: "+roleName, "Database: "+dbName,
		rolePropPages(sc, dbName, roleName))
}

// showUserPropertiesFor opens Database User Properties for a known
// connection, database, and user name — the Object Explorer context
// menu's entry point (mirrors showRolePropertiesFor).
func (a *App) showUserPropertiesFor(sc *db.ServerConn, dbName, userName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, dbName, "Database User Properties", "User: "+userName, "Database: "+dbName,
		userPropPages(sc, dbName, userName))
}

// showSchemaPropertiesFor opens Schema Properties for a known connection,
// database, and schema name — the Object Explorer context menu's entry
// point (mirrors showRolePropertiesFor).
func (a *App) showSchemaPropertiesFor(sc *db.ServerConn, dbName, schemaName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, dbName, "Schema Properties", "Schema: "+schemaName, "Database: "+dbName,
		schemaPropPages(sc, dbName, schemaName))
}
