package tui

import "github.com/radix29/gossms/internal/tuikit/controls"

// buildToolbar assembles the icon-only toolbar embedded in the menu bar
// row, right-aligned (see App.layoutAll/App.draw). Icons are chosen in
// todo/icons_toolbar.md. "Show Estimated Execution Plan" and "Show
// Execution Plan" have no Action yet — see todo/todo.txt.
func (a *App) buildToolbar() []controls.ToolbarButton {
	return []controls.ToolbarButton{
		{Icon: "✚", Tooltip: "New Query", Action: func() { a.newQueryPanel() }},
		{Divider: true, Icon: "|"},
		{Icon: "▶", Tooltip: "Execute", Action: func() { a.executeActiveQuery() }},
		{Icon: "▷", Tooltip: "Execute Selection", Action: func() { a.executeSelectedQuery() }},
		{Icon: "■", Tooltip: "Stop Execution", Action: func() { a.cancelExecutingQuery() }},
		{Divider: true, Icon: "|"},
		{Icon: "≈⎇", Tooltip: "Show Estimated Execution Plan"},
		{Icon: "⎇", Tooltip: "Show Execution Plan"},
	}
}
