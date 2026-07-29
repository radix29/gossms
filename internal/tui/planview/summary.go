package planview

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
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
	slices.SortStableFunc(nodes, func(a, b *showplan.Node) int {
		switch v.summarySt.sort {
		case sortByRows:
			return cmp.Compare(nodeRows(b), nodeRows(a))
		case sortByTime:
			return cmp.Compare(nodeTime(b), nodeTime(a))
		default:
			return cmp.Compare(b.Cost(st.SubTreeCost), a.Cost(st.SubTreeCost))
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

// drawSummary draws the operator summary grid into the Tree tab's bottom
// section (its only caller; the Plan tab's detail strip shows Properties
// only, see graph.go). The grid's bounds come from layoutTree, not from
// here — see the note there.
func (v *PlanView) drawSummary(s tcell.Screen, rect core.Rect) {
	v.summarySt.grid.SetBounds(rect.X, rect.Y, rect.W, rect.H)
	v.summarySt.grid.Draw(s)
}

// The summary grid is a controls.DataGrid with the cell cursor enabled
// (see New), so it carries that widget's right-click menu and "Show Value"
// popup, reachable through HandleMouse's Button2 data-cell case and
// HandleKey's Ctrl+Space. Both float free of the grid's own rect, so the
// hooks below exist to satisfy DataGrid.OverlayActive's contract: an
// overlay nothing draws is worse than no overlay at all — it opens
// invisibly and then swallows every key and mouse event until dismissed.

// summaryVisible reports whether the operator summary table is currently on
// screen — it lives only in the Tree tab's bottom section, in bottomSummary
// mode (see cycleBottomMode). Both overlay hooks below gate on it, so the
// grid's popups can't be reached, or drawn, from a tab that isn't showing
// it.
func (v *PlanView) summaryVisible() bool {
	return v.activeTab == TabTree && v.bottomMode == bottomSummary
}

// summaryOverlayActive reports whether the summary grid has its right-click
// menu or "Show Value" popup open — see controls.DataGrid.OverlayActive.
func (v *PlanView) summaryOverlayActive() bool {
	return v.summaryVisible() && v.summarySt.grid.OverlayActive()
}

// drawSummaryOverlay paints the summary grid's popups. Called from
// PlanView.DrawOverlay, never from drawSummary — they have to land after
// every other widget in the frame has drawn (see the "overlays drawn last"
// rule in tuikit/README.md), and the grid draws mid-frame.
func (v *PlanView) drawSummaryOverlay(s tcell.Screen) {
	if v.summaryVisible() {
		v.summarySt.grid.DrawOverlay(s)
	}
}

// summaryHeaderStyleAndText builds the Operator Summary header's style and
// title, varying with whether the table currently has keyboard focus.
func (v *PlanView) summaryHeaderStyleAndText() (tcell.Style, string) {
	hs := theme.StyleMenuBar()
	title := "Operator Summary  ('o' to cycle, c/r/t to sort, Tab to focus)"
	if v.bottomFocused {
		pal := theme.Active()
		hs = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
		title = "Operator Summary  (focused — Tab to return, ↑↓/Enter, Ctrl+Space to show full value)"
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

// syncSummaryCopyHook mirrors v.OnCopyRequest onto the grid, and must run
// before anything that can open the grid's context menu. DataGrid offers its
// "Copy" item only when its own hook is set, so wiring an unconditional
// forwarder at construction would offer Copy even to a host that never asked
// for one — a menu entry that silently does nothing when chosen. Both input
// handlers call this, since either can open the menu.
func (v *PlanView) syncSummaryCopyHook() {
	v.summarySt.grid.OnCopyRequest = v.OnCopyRequest
}

// handleSummaryOverlayKey forwards straight to the grid, deliberately
// bypassing handleSummaryKey's own Enter handling below: while one of the
// grid's popups is open, Enter belongs to it — activating the highlighted
// menu item — not to the jump-to-tree-node shortcut. Routing the overlay
// through handleSummaryKey instead made Enter jump the tree and leave the
// menu open behind it, so "Show Value" could never be chosen with the
// keyboard.
func (v *PlanView) handleSummaryOverlayKey(ev *tcell.EventKey) bool {
	v.syncSummaryCopyHook()
	return v.summarySt.grid.HandleKey(ev)
}

// handleSummaryKey drives the summary grid while it has focus (see
// bottomFocused): Enter jumps the tree selection to the activated row
// and returns focus to the tree; anything else forwards to the grid
// itself (arrow-key/PgUp/PgDn navigation, Ctrl+Space for the cell menu).
func (v *PlanView) handleSummaryKey(ev *tcell.EventKey) bool {
	v.syncSummaryCopyHook()
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
	v.syncSummaryCopyHook()
	return v.summarySt.grid.HandleMouse(ev)
}
