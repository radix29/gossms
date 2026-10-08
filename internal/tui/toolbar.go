package tui

import (
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// buildToolbar assembles the icon-only toolbar embedded in the menu bar
// row, right-aligned (see App.layoutAll/App.draw). Called once at startup and
// again by the toggles (toggleActualExecutionPlan, toggleLiveQueryStatistics,
// toggleOutputColumnMeta), whose buttons carry their ON/OFF state in the
// label itself.
//
// On a terminal too narrow for every button beside the menu labels, the
// toolbar hides them by DropRank (highest first): the toggles, then Est.Plan,
// Activity Monitor, Execute Selection, New Query and Stop, keeping Execute
// longest. Each keeps a menu entry or key (F5 runs the selection), so nothing
// becomes unreachable.
func (a *App) buildToolbar() []controls.ToolbarButton {
	return []controls.ToolbarButton{
		{Icon: "✚", Tooltip: "New Query", Action: func() { a.newQueryPanel() }, DropRank: 2},
		{Divider: true, Icon: "|"},
		{Icon: "▶", Tooltip: "Execute", Action: func() { a.executeActiveQuery() },
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: "▷", Tooltip: "Execute Selection", Action: func() { a.executeSelectedQuery() }, DropRank: 3,
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: "■", Tooltip: "Stop Execution", Action: func() { a.cancelExecutingQuery() }, DropRank: 1,
			Enabled: func() bool { qp := a.activeQueryPanel(); return qp != nil && qp.executing }},
		{Divider: true, Icon: "|"},
		{Icon: "Est.Plan", Tooltip: "Show Estimated Execution Plan", Action: func() { a.showEstimatedExecutionPlan() }, DropRank: 5,
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: actualPlanToggleIcon(a.actualPlanEnabled), Tooltip: "Include Actual Execution Plan", Action: func() { a.toggleActualExecutionPlan() }, DropRank: 6},
		{Icon: liveStatsToggleIcon(a.liveStatsEnabled), Tooltip: "Include Live Query Statistics", Action: func() { a.toggleLiveQueryStatistics() }, DropRank: 7},
		{Icon: metaToggleIcon(a.metaEnabled), Tooltip: "Show Output Column Metadata", Action: func() { a.toggleOutputColumnMeta() }, DropRank: 8},
		{Divider: true, Icon: "|"},
		{Icon: "📈", Tooltip: "Activity Monitor", Action: func() { a.showActivityMonitor() }, DropRank: 4,
			Enabled: func() bool {
				return len(a.connections) > 0 &&
					gate.Allows(a.activeServerConn(), "", gate.ViewServerState)
			}},
	}
}

// actualPlanToggleIcon renders the "Include Actual Execution Plan" toggle
// button's text. Both states are the same display width, so toggling never
// needs the toolbar to relayout its neighbors.
func actualPlanToggleIcon(on bool) string {
	if on {
		return "Act.Plan[ON--]"
	}
	return "Act.Plan[-OFF]"
}

// liveStatsToggleIcon renders the "Include Live Query Statistics" toggle
// button's text. Same equal-width rule as actualPlanToggleIcon.
func liveStatsToggleIcon(on bool) string {
	if on {
		return "Live[ON--]"
	}
	return "Live[-OFF]"
}

// metaToggleIcon renders the "Show Output Column Metadata" toggle button's
// text. Same equal-width rule as actualPlanToggleIcon, for the same reason.
func metaToggleIcon(on bool) string {
	if on {
		return "Meta[ON--]"
	}
	return "Meta[-OFF]"
}
