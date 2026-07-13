package tui

import (
	gosmoversion "github.com/radix29/gosmo/version"
	"github.com/radix29/gossms/internal/tuikit/controls"
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
			{Label: "Disconnect", Action: func() { a.disconnectActive() }},
			{Divider: true},
			{Label: "Open", Shortcut: "Ctrl+O", Action: func() { a.openQueryFile() }},
			{Label: "Close", Shortcut: "Ctrl+W", Action: func() { a.closeActivePanel() }},
			{Label: "Save", Shortcut: "Ctrl+S", Action: func() { a.saveQuery(false) }},
			{Label: "Save As...", Action: func() { a.saveQuery(true) }},
			{Divider: true},
			{Label: "Exit", Shortcut: "Ctrl+Q", Action: func() { a.quit() }},
		}},
		{Label: "Edit", Items: []controls.MenuItem{
			{Label: "Undo", Shortcut: "Ctrl+Z", Action: func() { a.editorUndo() }},
			{Label: "Redo", Shortcut: "Ctrl+Y", Action: func() { a.editorRedo() }},
			{Divider: true},
			{Label: "Cut", Shortcut: "Ctrl+X", Action: func() { a.cutSelection() }},
			{Label: "Copy", Shortcut: "Ctrl+C", Action: func() { a.copySelection() }},
			{Label: "Paste", Shortcut: "Ctrl+V", Action: func() { a.pasteFromClipboard() }},
			{Divider: true},
			{Label: "Select All", Shortcut: "Ctrl+A", Action: func() { a.selectAllInTarget() }},
			{Divider: true},
			{Label: "Duplicate Line", Shortcut: "Ctrl+D", Action: func() { a.editorAction(func(e *controls.Editor) { e.DuplicateLines() }) }},
			{Label: "Delete Line", Shortcut: "Ctrl+L", Action: func() { a.editorAction(func(e *controls.Editor) { e.DeleteLines() }) }},
			{Label: "Move Line Up", Shortcut: "Ctrl+Shift+Up", Action: func() { a.editorAction(func(e *controls.Editor) { e.MoveLinesUp() }) }},
			{Label: "Move Line Down", Shortcut: "Ctrl+Shift+Down", Action: func() { a.editorAction(func(e *controls.Editor) { e.MoveLinesDown() }) }},
			{Label: "Comment/Uncomment Line", Shortcut: "Ctrl+/", Action: func() { a.editorAction(func(e *controls.Editor) { e.ToggleLineComments() }) }},
			{Label: "Uppercase Selection", Shortcut: "Ctrl+Shift+U", Action: func() { a.editorAction(func(e *controls.Editor) { e.UppercaseSelection() }) }},
			{Label: "Lowercase Selection", Shortcut: "Ctrl+U", Action: func() { a.editorAction(func(e *controls.Editor) { e.LowercaseSelection() }) }},
		}},
		{Label: "View", Items: []controls.MenuItem{
			{Label: "Object Explorer", Action: func() { a.focusExplorer() }},
			{Label: "Object Explorer Details", Action: func() { a.showObjectExplorerDetails() }},
			{Label: "Next Panel", Shortcut: "Ctrl+Shift+Right", Action: func() { a.nextPanel() }},
			{Label: "Prev Panel", Shortcut: "Ctrl+Shift+Left", Action: func() { a.prevPanel() }},
			{Divider: true},
			{Label: "Refresh", Shortcut: "F5", Action: func() { a.refreshSelected() }},
		}},
		{Label: "Query", Items: []controls.MenuItem{
			{Label: "New Query", Shortcut: "Ctrl+N", Action: func() { a.newQueryPanel() }},
			{Divider: true},
			{Label: "Execute", Shortcut: "F5", Action: func() { a.executeActiveQuery() }},
			{Label: "Execute at Cursor", Shortcut: "Ctrl+Enter", Action: func() {
				a.editorAction(func(e *controls.Editor) { e.SelectStatementAtCursor() })
			}},
			{Label: "Cancel Executing Query", Action: func() { a.cancelExecutingQuery() }},
			{Divider: true},
			{Label: "Results To Text", Action: func() { a.setResultsMode(ResultsModeText) }},
			{Label: "Results To Grid", Action: func() { a.setResultsMode(ResultsModeGrid) }},
			{Label: "Results To File", Action: func() { a.setResultsMode(ResultsModeFile) }},
		}},
		{Label: "Tools", Items: []controls.MenuItem{
			{Label: "Server Properties", Action: func() { a.showServerProperties() }},
			{Label: "Database Properties", Action: func() { a.showDatabaseProperties() }},
			{Label: "Query List", Action: func() { a.showQueryList() }},
			{Label: "Background Tasks", Action: func() { a.tasksDialog.Show() }},
			{Divider: true},
			{Label: "Options", Action: func() { a.optionsDialog.Show() }},
		}},
		{Label: "Help", Items: []controls.MenuItem{
			{Label: "Help", Shortcut: "F1", Action: func() { a.helpDialog.Show() }},
			{Label: "Key Diagnostics", Action: func() { a.keyDiagDialog.Show() }},
			{Divider: true},
			{Label: "About goSSMS", Action: func() { a.showAbout() }},
		}},
	}
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
		{Key: "Application", Value: version.Name},
		{Key: "Version", Value: version.Version},
		{Key: "Commit", Value: version.Commit},
		{Key: "Built", Value: version.Date},
		{Key: "Runtime", Value: version.Runtime()},
		{Key: "Go Version", Value: "1.26"},
		{Key: "Description", Value: "Go SQL Server Management Studio TUI"},
		{Key: "Author", Value: "radix29"},
		{Key: "Repository", Value: "github.com/radix29/gossms"},
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
	)
	a.propsDialog.ShowGenericProperties("About goSSMS", rows)
}
