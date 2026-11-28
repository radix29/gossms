package tui

import (
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/controls"
)

// buildToolbar assembles the icon-only toolbar embedded in the menu bar
// row, right-aligned (see App.layoutAll/App.draw). Built once; availability
// and toggle state are read live.
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
			Enabled: func() bool { qp := a.activeQueryPanel(); return qp != nil && qp.cancellable() }},
		{Divider: true, Icon: "|"},
		{Icon: "Est.Plan", Tooltip: "Show Estimated Execution Plan", Action: func() { a.showEstimatedExecutionPlan() }, DropRank: 5,
			Enabled: func() bool { return a.activeQueryPanel() != nil }},
		{Icon: "Act.Plan", Tooltip: "Include Actual Execution Plan", Action: func() { a.toggleActualExecutionPlan() }, DropRank: 6,
			On: func() bool { return a.actualPlanEnabled }, Enabled: a.actualPlanAvailable},
		{Icon: "Live", Tooltip: "Include Live Query Statistics", Action: func() { a.toggleLiveQueryStatistics() }, DropRank: 7,
			On: func() bool { return a.liveStatsEnabled }, Enabled: a.actualPlanAvailable},
		{Icon: "Meta", Tooltip: "Show Output Column Metadata", Action: func() { a.toggleOutputColumnMeta() }, DropRank: 8,
			On: func() bool { return a.metaEnabled }},
		{Divider: true, Icon: "|"},
		{Icon: "📈", Tooltip: "Activity Monitor", Action: func() { a.showActivityMonitor() }, DropRank: 4,
			Enabled: func() bool {
				return len(a.connections) > 0 &&
					explorerSupport(a.activeServerConn()).has(oeMonitorTools) &&
					gate.Allows(a.activeServerConn(), "", gate.ViewServerState)
			}},
	}
}
