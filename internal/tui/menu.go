package tui

import (
	gosmoversion "github.com/radix29/gosmo/version"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/version"
)

// buildMenus assembles the top-level application menu bar (File, Edit,
// View, Query, Tools, Help). This is the single place the menu structure
// is defined; MenuBar itself (in tuikit/controls) knows nothing about
// gossms — it only renders and navigates whatever []controls.Menu it's
// given here.
func (a *App) buildMenus() []controls.Menu {
	return []controls.Menu{
		{Label: "File", Items: []controls.MenuItem{
			{Label: "Connect...", Shortcut: "Ctrl+Shift+O", Action: func() { a.connectDialog.Show() }},
			{Label: "Disconnect", Action: func() { a.disconnectActive() },
				Enabled: func() bool { return a.selectedServerConn() != nil }},
			{Divider: true},
			{Label: "Open", Shortcut: "Ctrl+O", Action: func() { a.openQueryFile() }},
			{Label: "Close", Shortcut: "Ctrl+W", Action: func() { a.closeActivePanel() },
				Enabled: func() bool {
					p := a.panels.ActivePanel()
					return p != nil && layout.PanelClosable(p)
				}},
			{Label: "Save", Shortcut: "Ctrl+S", Action: func() { a.saveQuery(false) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Save As...", Action: func() { a.saveQuery(true) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Divider: true},
			{Label: "Exit", Shortcut: "Ctrl+Q", Action: func() { a.requestQuit() }},
		}},
		{Label: "Edit", Items: []controls.MenuItem{
			{Label: "Undo", Shortcut: "Ctrl+Z", Action: func() { a.editorUndo() },
				Enabled: func() bool { _, ok := a.activeClipboardTarget().(*controls.Editor); return ok }},
			{Label: "Redo", Shortcut: "Ctrl+Y", Action: func() { a.editorRedo() },
				Enabled: func() bool { _, ok := a.activeClipboardTarget().(*controls.Editor); return ok }},
			{Divider: true},
			{Label: "Cut", Shortcut: "Ctrl+X", Action: func() { a.cutSelection() },
				Enabled: func() bool { t := a.activeClipboardTarget(); return t != nil && t.HasSelection() }},
			{Label: "Copy", Shortcut: "Ctrl+C", Action: func() { a.copySelection() },
				Enabled: func() bool { t := a.activeClipboardTarget(); return t != nil && t.HasSelection() }},
			{Label: "Paste", Shortcut: "Ctrl+V", Action: func() { a.pasteFromClipboard() },
				Enabled: func() bool { return a.activeClipboardTarget() != nil }},
			{Divider: true},
			{Label: "Select All", Shortcut: "Ctrl+A", Action: func() { a.selectAllInTarget() },
				Enabled: func() bool { return a.activeClipboardTarget() != nil }},
			{Divider: true},
			{Label: "Find...", Shortcut: "Ctrl+F", Action: func() { a.findDialog.ShowFind() },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Replace...", Action: func() { a.findDialog.ShowReplace() },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Find Next", Shortcut: "F3", Action: func() { a.findNextInEditor(1) },
				Enabled: a.hasEditorSearch},
			{Label: "Find Previous", Shortcut: "Shift+F3", Action: func() { a.findNextInEditor(-1) },
				Enabled: a.hasEditorSearch},
			{Divider: true},
			{Label: "Duplicate Line", Shortcut: "Ctrl+D", Action: func() { a.editorAction(func(e *controls.Editor) { e.DuplicateLines() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Delete Line", Shortcut: "Ctrl+L", Action: func() { a.editorAction(func(e *controls.Editor) { e.DeleteLines() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Move Line Up", Shortcut: "Ctrl+Shift+Up", Action: func() { a.editorAction(func(e *controls.Editor) { e.MoveLinesUp() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Move Line Down", Shortcut: "Ctrl+Shift+Down", Action: func() { a.editorAction(func(e *controls.Editor) { e.MoveLinesDown() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Comment/Uncomment Line", Shortcut: "Ctrl+/", Action: func() { a.editorAction(func(e *controls.Editor) { e.ToggleLineComments() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Uppercase Selection", Shortcut: "Ctrl+Shift+U", Action: func() { a.editorAction(func(e *controls.Editor) { e.UppercaseSelection() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Lowercase Selection", Shortcut: "Ctrl+U", Action: func() { a.editorAction(func(e *controls.Editor) { e.LowercaseSelection() }) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
		}},
		{Label: "View", Items: []controls.MenuItem{
			{Label: "Object Explorer", Action: func() { a.focusExplorer() }},
			{Label: "Object Explorer Details", Action: func() { a.showObjectExplorerDetails() }},
			{Label: "Next Panel", Shortcut: "Ctrl+Shift+Right", Action: func() { a.nextPanel() },
				Enabled: func() bool { return a.panels.Count() > 1 }},
			{Label: "Prev Panel", Shortcut: "Ctrl+Shift+Left", Action: func() { a.prevPanel() },
				Enabled: func() bool { return a.panels.Count() > 1 }},
			{Divider: true},
			{Label: "Refresh", Shortcut: "F5", Action: func() { a.refreshSelected() },
				Enabled: func() bool { return a.explorer.Selected() != nil }},
		}},
		{Label: "Query", Items: []controls.MenuItem{
			{Label: "New Query", Shortcut: "Ctrl+N", Action: func() { a.newQueryPanel() }},
			{Divider: true},
			{Label: "Execute", Shortcut: "F5", Action: func() { a.executeActiveQuery() },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Execute at Cursor", Shortcut: "Ctrl+Enter", Action: func() {
				a.editorAction(func(e *controls.Editor) { e.SelectStatementAtCursor() })
			}, Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Cancel Executing Query", Action: func() { a.cancelExecutingQuery() },
				Enabled: func() bool { qp := a.activeQueryPanel(); return qp != nil && qp.executing }},
			{Label: "Reconnect", Action: func() { a.reconnectActiveQuery() },
				Enabled: func() bool {
					qp := a.activeQueryPanel()
					return qp != nil && qp.conn != nil && !qp.executing
				}},
			{Divider: true},
			{Label: "Refresh IntelliSense Cache", Shortcut: "Ctrl+R", Action: func() { a.refreshCompletionCache() },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Divider: true},
			{Label: "Estimated Execution Plan", Action: func() { a.showEstimatedExecutionPlan() },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: actualExecutionPlanMenuLabel(a.actualPlanEnabled), Action: func() { a.toggleActualExecutionPlan() }},
			{Label: outputColumnMetaMenuLabel(a.metaEnabled), Action: func() { a.toggleOutputColumnMeta() }},
			{Divider: true},
			{Label: "Results To Text", Action: func() { a.setResultsMode(ResultsModeText) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Results To Grid", Action: func() { a.setResultsMode(ResultsModeGrid) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
			{Label: "Results To File", Action: func() { a.setResultsMode(ResultsModeFile) },
				Enabled: func() bool { return a.activeQueryPanel() != nil }},
		}},
		{Label: "Tools", Items: []controls.MenuItem{
			{Label: "Server Properties", Action: func() { a.showServerProperties() },
				Enabled: func() bool { return len(a.connections) > 0 }},
			{Label: "Database Properties", Action: func() { a.showDatabaseProperties() },
				Enabled: func() bool {
					node := a.explorer.Selected()
					return node != nil && node.data.DBName != ""
				}},
			{Label: "Activity Monitor", Action: func() { a.showActivityMonitor() },
				Enabled: func() bool { return len(a.connections) > 0 }},
			{Label: "Query List", Action: func() { a.showQueryList() }},
			{Label: "Background Tasks", Action: func() { a.tasksDialog.Show() }},
			{Divider: true},
			{Label: "Options", Action: func() { a.optionsDialog.Show() }},
		}},
		{Label: "Help", Items: []controls.MenuItem{
			{Label: "Help", Shortcut: "F1", Action: func() { a.helpDialog.Show() }},
			{Label: "Key Diagnostics", Action: func() { a.keyDiagDialog.Show() }},
			{Divider: true},
			{Label: "Check for Updates", Action: func() { a.checkForUpdates() }},
			{Label: "About goSSMS", Action: func() { a.showAbout() }},
		}},
	}
}

// actualExecutionPlanMenuLabel mirrors actualPlanToggleIcon's state text
// for the Query menu item — MenuItem has no separate "checked" visual, so
// state is folded into the label itself instead of adding one (the toolbar
// button, always visible in the same row, is the primary indicator).
func actualExecutionPlanMenuLabel(on bool) string {
	if on {
		return "Actual Execution Plan (ON)"
	}
	return "Actual Execution Plan (OFF)"
}

// outputColumnMetaMenuLabel folds the "Show Output Column Metadata" toggle
// state into its Query menu label, for the same reason as
// actualExecutionPlanMenuLabel.
func outputColumnMetaMenuLabel(on bool) string {
	if on {
		return "Output Column Metadata (ON)"
	}
	return "Output Column Metadata (OFF)"
}

// editorAction runs fn against the active query panel's editor, if any —
// used by Edit menu entries (Duplicate Line, Move Line, Comment/Uncomment,
// Uppercase/Lowercase, Undo/Redo) that are meaningful only for the SQL
// editor, unlike Cut/Copy/Paste/Select All which also work on dialog
// input fields via activeClipboardTarget.
func (a *App) editorAction(fn func(e *controls.Editor)) {
	if qp := a.activeQueryPanel(); qp != nil {
		fn(qp.editor)
	}
}

// editorUndo and editorRedo act on whichever *controls.Editor Cut/Copy/
// Paste would currently act on (the query editor, or a dialog's
// connection-string Editor field) — unlike the other new Edit-menu
// entries above, which are query-editor-only. InputField keeps no undo
// history, so a focused InputField simply has no effect here.
func (a *App) editorUndo() {
	if e, ok := a.activeClipboardTarget().(*controls.Editor); ok {
		e.Undo()
	}
}

func (a *App) editorRedo() {
	if e, ok := a.activeClipboardTarget().(*controls.Editor); ok {
		e.Redo()
	}
}

// showAbout displays the About goSSMS properties dialog. gosmo's own
// Commit/Built rows are included only when gosmo actually recorded them
// for this binary — see gosmo/version's doc comment: a plain semver
// dependency version or a local `replace ... => ../gosmo` dev checkout
// carries no decodable commit info, so those stay omitted rather than
// showing a bare "unknown".
func (a *App) showAbout() {
	rows := []PropertyRow{
		PropertySection("Application"),
		{Key: "Name", Value: version.Name},
		{Key: "Description", Value: "Go SQL Server Management Studio TUI"},
		{Key: "Version", Value: version.Version},
		{Key: "Commit", Value: version.Commit},
		{Key: "Built", Value: version.Date},
		{Key: "Author", Value: "radix29"},
		{Key: "Repository", Value: "github.com/radix29/gossms"},

		PropertySection("License"),
		{Key: "License", Value: version.License},
		{Key: "Copyright", Value: version.Copyright},
		{Key: "Source", Value: "https://github.com/radix29/gossms"},

		PropertySection("Runtime"),
		{Key: "Platform", Value: version.Runtime()},
		{Key: "Go Version", Value: "1.26"},

		PropertySection("Components"),
		{Key: "DB Framework", Value: "github.com/radix29/gosmo " + gosmoversion.Version},
	}
	if gosmoversion.Commit != "unknown" {
		rows = append(rows, PropertyRow{Key: "DB Framework Commit", Value: gosmoversion.Commit})
	}
	if gosmoversion.Date != "unknown" {
		rows = append(rows, PropertyRow{Key: "DB Framework Built", Value: gosmoversion.Date})
	}
	rows = append(rows,
		PropertyRow{Key: "TUI Library", Value: "internal/tuikit (embedded)"},
		PropertyRow{Key: "TUI Backend", Value: "github.com/gdamore/tcell/v3"},

		// The Activity Monitor's Sessions tab installs and runs somebody
		// else's GPL-3.0 procedure, so it is listed as a component like any
		// other bundled dependency. Author and licence are deliberately not
		// repeated here: whoIsActiveCredit draws both on the Sessions tab
		// itself, and the embedded whoisactive.sql carries the upstream
		// copyright header alongside LICENSE.sp_whoisactive. Dropping either
		// of those is what would cost the attribution — not this list.
		PropertyRow{Key: "Sessions Procedure", Value: "sp_WhoIsActive " + activity.WhoIsActiveVersion()},
		PropertyRow{Key: "Sessions Procedure Source", Value: activity.WhoIsActiveRepo},
	)
	// Sized to the whole list so the About box doesn't open scrolled on a
	// normal terminal; recentre clamps it on a small one.
	a.propsDialog.ShowGenericPropertiesSized("About goSSMS", rows, 82, len(rows)+7)
}
