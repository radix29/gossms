package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// app_show_panels.go opens the non-query panels — Object Explorer Details, the
// query list, Activity Monitor, the Log Viewer and Query Store — each reusing
// an already-open panel for the same target rather than adding a second one.
// The property dialogs are in app_show_properties.go.

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
