package tui

import (
	"fmt"
	"os"

	"github.com/radix29/gossms/internal/db"
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
	a.pathPrompt.Prompt("Open Query File", "", func(path string) {
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
// connection (see connectForQueryPanel), so nothing else references it.
func (a *App) closePanelAt(i int) {
	if qp, ok := a.panels.PanelAt(i).(*QueryPanel); ok && qp.conn != nil {
		qp.conn.Close()
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

// cancelExecutingQuery runs Query > Cancel Executing Query.
func (a *App) cancelExecutingQuery() {
	if qp := a.activeQueryPanel(); qp != nil {
		qp.CancelExecution()
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
	a.pathPrompt.Prompt(title, initial, func(path string) {
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

func (a *App) refreshSelected() { a.explorer.RefreshSelected() }

func (a *App) showServerProperties() {
	var sc *db.ServerConn
	if node := a.explorer.Selected(); node != nil {
		sc = resolveConn(node)
	}
	if sc == nil {
		if len(a.connections) == 0 {
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

// showLoginProperties opens Login Properties for a login on sc.
func (a *App) showLoginProperties(sc *db.ServerConn, loginName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, "", "Login Properties", "Login: "+loginName, "Server: "+sc.Opts.Server,
		loginPropPages(sc, loginName))
}
