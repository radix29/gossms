package planview

import (
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// summarySort selects which column the operator summary table (the
// bottom section's "Summary" mode) is sorted by, descending.
type summarySort int

const (
	sortByCost summarySort = iota
	sortByRows
	sortByTime
)

var summaryColumns = []string{"Cost%", "Rows", "Time", "Operator", "Object", "Status"}

// summaryState holds the operator summary table's own grid, sort mode,
// and the node list backing it (parallel to the grid's rows, so Enter
// can resolve a selected row back to a *showplan.Node).
type summaryState struct {
	grid *controls.DataGrid
	sort summarySort
	rows []*showplan.Node
}

// rebuildSummaryRows re-sorts and re-renders the operator summary table
// for the current statement — called on load, statement switch, and
// whenever the sort mode changes.
func (v *PlanView) rebuildSummaryRows() {
	st := v.currentStatement()
	if st == nil || st.Root == nil {
		v.summarySt.rows = nil
		v.summarySt.grid.SetData(summaryColumns, nil)
		return
	}
	nodes := st.Nodes()
	sort.SliceStable(nodes, func(i, j int) bool {
		switch v.summarySt.sort {
		case sortByRows:
			return nodeRows(nodes[i]) > nodeRows(nodes[j])
		case sortByTime:
			return nodeTime(nodes[i]) > nodeTime(nodes[j])
		default:
			return nodes[i].Cost(st.SubTreeCost) > nodes[j].Cost(st.SubTreeCost)
		}
	})
	v.summarySt.rows = nodes
	rows := make([][]string, len(nodes))
	for i, n := range nodes {
		rows[i] = summaryRow(n, st.SubTreeCost)
	}
	v.summarySt.grid.SetData(summaryColumns, rows)
}

// summaryRow renders one operator as a summary table row.
func summaryRow(n *showplan.Node, stmtTotal float64) []string {
	status := "OK"
	if len(n.Warnings) > 0 {
		status = "⚠ " + n.Warnings[0]
	}
	obj := ""
	if !n.Object.IsZero() {
		obj = n.Object.Short()
	}
	return []string{
		fmt.Sprintf("%.1f", n.Cost(stmtTotal)*100),
		fmt.Sprintf("%d", nodeRows(n)),
		fmt.Sprintf("%d ms", nodeTime(n)),
		n.PhysicalOp,
		obj,
		status,
	}
}

// nodeRows returns actual rows when available, else the estimate.
func nodeRows(n *showplan.Node) int64 {
	if n.Runtime != nil {
		return n.Runtime.Rows
	}
	return int64(n.EstRows)
}

// nodeTime returns actual elapsed time when available, else 0 (an
// estimated-only plan has no time metric).
func nodeTime(n *showplan.Node) int64 {
	if n.Runtime != nil {
		return n.Runtime.ElapsedMS
	}
	return 0
}

// drawSummary re-bounds and draws the operator summary grid into rect — the
// Tree tab's bottom section (its only caller; the Plan tab's detail strip
// shows Properties only, see graph.go).
func (v *PlanView) drawSummary(s tcell.Screen, rect core.Rect) {
	v.summarySt.grid.SetBounds(rect.X, rect.Y, rect.W, rect.H)
	v.summarySt.grid.Draw(s)
}

// summaryHeaderStyleAndText builds the Operator Summary header's style and
// title, varying with whether the table currently has keyboard focus.
func (v *PlanView) summaryHeaderStyleAndText() (tcell.Style, string) {
	hs := theme.StyleMenuBar()
	title := "Operator Summary  ('o' to cycle, c/r/t to sort, Tab to focus)"
	if v.bottomFocused {
		pal := theme.Active()
		hs = tcell.StyleDefault.Background(pal.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
		title = "Operator Summary  (focused — Tab to return, ↑↓/Enter)"
	}
	return hs, title
}

// trySummarySort applies a sort-column key (c/r/t) if ev is one,
// regardless of whether the summary table currently has focus — these
// don't collide with anything the tree itself binds, so there's no
// reason to require Tab-ing into the table first just to re-sort it.
func (v *PlanView) trySummarySort(ev *tcell.EventKey) bool {
	switch core.EvRune(ev) {
	case 'c':
		v.summarySt.sort = sortByCost
	case 'r':
		v.summarySt.sort = sortByRows
	case 't':
		v.summarySt.sort = sortByTime
	default:
		return false
	}
	v.rebuildSummaryRows()
	return true
}

// handleSummaryKey drives the summary grid while it has focus (see
// bottomFocused): Enter jumps the tree selection to the activated row
// and returns focus to the tree; anything else forwards to the grid
// itself (arrow-key/PgUp/PgDn navigation).
func (v *PlanView) handleSummaryKey(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyEnter {
		if row := v.summarySt.grid.SelectedRow(); row >= 0 && row < len(v.summarySt.rows) {
			v.selectNode(v.summarySt.rows[row].ID)
			v.bottomFocused = false
		}
		return true
	}
	return v.summarySt.grid.HandleKey(ev)
}

// handleSummaryMouse forwards to the summary grid.
func (v *PlanView) handleSummaryMouse(ev *tcell.EventMouse) bool {
	return v.summarySt.grid.HandleMouse(ev)
}
