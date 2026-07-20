package tui

import "github.com/radix29/gossms/internal/tuikit/controls"

// buildToolbar assembles the icon-only toolbar embedded in the menu bar
// row, right-aligned (see App.layoutAll/App.draw). Icons are chosen in
// todo/icons_toolbar.md. Called once at startup and again by
// toggleActualExecutionPlan whenever the last button's ON/OFF state
// changes — see that method's doc comment.
func (a *App) buildToolbar() []controls.ToolbarButton {
	return []controls.ToolbarButton{
		{Icon: "✚", Tooltip: "New Query", Action: func() { a.newQueryPanel() }},
		{Divider: true, Icon: "|"},
		{Icon: "▶", Tooltip: "Execute", Action: func() { a.executeActiveQuery() },
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: "▷", Tooltip: "Execute Selection", Action: func() { a.executeSelectedQuery() },
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: "■", Tooltip: "Stop Execution", Action: func() { a.cancelExecutingQuery() },
			Enabled: func() bool { qp := a.activeQueryPanel(); return qp != nil && qp.executing }},
		{Divider: true, Icon: "|"},
		{Icon: "≈⎇ Est. Plan", Tooltip: "Show Estimated Execution Plan", Action: func() { a.showEstimatedExecutionPlan() },
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: actualPlanToggleIcon(a.actualPlanEnabled), Tooltip: "Include Actual Execution Plan", Action: func() { a.toggleActualExecutionPlan() }},
		{Divider: true, Icon: "|"},
		{Icon: "📈", Tooltip: "Activity Monitor", Action: func() { a.showActivityMonitor() },
			Enabled: func() bool { return len(a.connections) > 0 }},
	}
}

// actualPlanToggleIcon renders the "Include Actual Execution Plan" toggle
// button's text. Both states are the same display width, so toggling never
// needs the toolbar to relayout its neighbors.
func actualPlanToggleIcon(on bool) string {
	if on {
		return "Act. Plan [ON---]"
	}
	return "Act. Plan [--OFF]"
}
