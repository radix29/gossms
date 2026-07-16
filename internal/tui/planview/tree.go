package planview

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// bottomSectionHeight caps how many rows the Properties/Summary bottom
// section takes, even on a very tall terminal — it's a supplementary
// view, not the main content.
const bottomSectionHeight = 12

// bottomMode selects what, if anything, the Tree tab's bottom section
// shows — cycled by 'o'.
type bottomMode int

const (
	bottomHidden bottomMode = iota
	bottomProperties
	bottomSummary
)

// treeRow is one visible row of the flattened operator tree.
type treeRow struct {
	node    *showplan.Node
	depth   int
	lastSib bool // true if this is the last child of its parent
	// continuation has length depth-1 (empty for depth 0 and 1): for each
	// ancestor level strictly between the root and this node's immediate
	// parent, whether that ancestor still has a pending sibling — i.e.
	// whether a "│" (true) or blank (false) belongs in that column.
	continuation []bool
}

// treeState holds the Tree tab's own view state: the flattened row list,
// scroll position, and which operators are collapsed.
type treeState struct {
	rows      []treeRow
	scroll    int
	collapsed map[int]bool // NodeID -> collapsed; absent = expanded (default)
}

// rebuildTreeRows re-flattens the current statement's operator tree,
// respecting collapsed state — called whenever the plan, statement, or
// any expand/collapse state changes.
func (v *PlanView) rebuildTreeRows() {
	v.treeSt.rows = v.treeSt.rows[:0]
	st := v.currentStatement()
	if st == nil || st.Root == nil {
		return
	}
	var walk func(n *showplan.Node, depth int, lastSib bool, cont []bool)
	walk = func(n *showplan.Node, depth int, lastSib bool, cont []bool) {
		v.treeSt.rows = append(v.treeSt.rows, treeRow{node: n, depth: depth, lastSib: lastSib, continuation: cont})
		if v.treeSt.collapsed[n.ID] {
			return
		}
		var childCont []bool
		if depth > 0 { // the root contributes no continuation column — see the doc above
			childCont = append(append([]bool(nil), cont...), !lastSib)
		}
		for i, c := range n.Children {
			walk(c, depth+1, i == len(n.Children)-1, childCont)
		}
	}
	walk(st.Root, 0, true, nil)
	if v.treeSt.scroll > len(v.treeSt.rows) {
		v.treeSt.scroll = 0
	}
}

// layoutTree computes the Tree tab's internal layout: a 1-row statement
// metrics header, a tree|details split, and — while bottomMode isn't
// hidden — a bottom Properties/Summary section beneath it.
func (v *PlanView) layoutTree() {
	r := v.contentRect
	if r.H <= 0 || r.W <= 0 {
		v.treeHeaderRect, v.treePaneRect, v.detailsPaneRect = core.Rect{}, core.Rect{}, core.Rect{}
		v.detailsHeaderRect, v.detailsContentRect = core.Rect{}, core.Rect{}
		v.bottomHeaderRect, v.bottomRect = core.Rect{}, core.Rect{}
		return
	}
	y := r.Y
	v.treeHeaderRect = core.Rect{X: r.X, Y: y, W: r.W, H: 1}
	y++

	remaining := r.Bottom() - y
	bottomTotal := 0
	if v.bottomMode != bottomHidden && remaining > 4 {
		bottomTotal = core.Clamp(remaining/3, 4, bottomSectionHeight)
	}
	midH := remaining - bottomTotal

	v.treeSplit.SetBounds(r.X, y, r.W, midH)
	v.treePaneRect = v.treeSplit.FirstRect()
	v.detailsPaneRect = v.treeSplit.SecondRect()
	v.detailsHeaderRect, v.detailsContentRect = core.Rect{}, core.Rect{}
	if dr := v.detailsPaneRect; dr.H > 1 {
		v.detailsHeaderRect = core.Rect{X: dr.X, Y: dr.Y, W: dr.W, H: 1}
		v.detailsContentRect = core.Rect{X: dr.X, Y: dr.Y + 1, W: dr.W, H: dr.H - 1}
	} else {
		v.detailsContentRect = dr
	}
	y += midH

	v.bottomHeaderRect, v.bottomRect = core.Rect{}, core.Rect{}
	if bottomTotal > 1 {
		v.bottomHeaderRect = core.Rect{X: r.X, Y: y, W: r.W, H: 1}
		v.bottomRect = core.Rect{X: r.X, Y: y + 1, W: r.W, H: bottomTotal - 1}
	}
}

// expensiveCostThreshold is the cost-percentage cutoff, shared by the Plan
// tab's tiles and the Tree tab's rows, above which an operator is flagged
// as expensive (❌ badge, red border/text) — matching real SSMS's own
// "expensive operator" highlight convention.
const expensiveCostThreshold = 0.80

// nodeCostPct returns n's own cost as a fraction of the current
// statement's total — 0 if there's no current statement.
func (v *PlanView) nodeCostPct(n *showplan.Node) float64 {
	st := v.currentStatement()
	if st == nil {
		return 0
	}
	return n.Cost(st.SubTreeCost)
}

// tileRowsText renders a graph tile's row-count line: the actual count
// when available, unless showEstimated ('p') asks for the estimate
// regardless — an estimated-only node always shows its estimate.
func (v *PlanView) tileRowsText(n *showplan.Node) string {
	if !v.showEstimated && n.Runtime != nil {
		return fmt.Sprintf("%d rows", n.Runtime.Rows)
	}
	return "~" + formatCount(n.EstRows) + " rows"
}

// drawTreeTab renders the statement metrics header, the tree|details
// split, and the bottom section (if shown).
func (v *PlanView) drawTreeTab(s tcell.Screen) {
	st := v.currentStatement()
	if st == nil || st.Root == nil {
		v.drawMessage(s, "No plan tree for this statement")
		return
	}
	v.drawTreeHeader(s, st)
	v.drawTreePane(s)
	v.treeSplit.Draw(s)
	n := v.selectedNode()
	total := len(detailLines(n, st))
	canUp := v.detailsScroll > 0
	canDown := v.detailsScroll+v.detailsContentRect.H < total
	drawDetailsHeader(s, v.detailsHeaderRect, "Operator Details", canUp, canDown)
	drawDetails(s, v.detailsContentRect, n, st, v.detailsScroll)
	v.drawBottomSection(s)
}

// drawTreeHeader renders the statement-level metrics row.
func (v *PlanView) drawTreeHeader(s tcell.Screen, st *showplan.Statement) {
	hs := tcell.StyleDefault.Background(theme.Active().PanelBg).Foreground(theme.Active().TextHighlight)
	core.FillRect(s, v.treeHeaderRect, ' ', hs)

	cpu, elapsed := "—", "—"
	if st.TimeStats != nil {
		cpu = fmt.Sprintf("%d ms", st.TimeStats.CPUMS)
		elapsed = fmt.Sprintf("%d ms", st.TimeStats.ElapsedMS)
	}
	mem := "—"
	if st.MemoryGrant != nil {
		mem = fmt.Sprintf("%d KB", st.MemoryGrant.GrantedKB)
	}
	text := fmt.Sprintf("Est Cost: %.4f  DOP: %d  CPU: %s  Elapsed: %s  Mem: %s  Hash: %s",
		st.SubTreeCost, st.DOP, cpu, elapsed, mem, orDash(st.QueryHash))
	core.DrawTextClipped(s, v.treeHeaderRect.X+1, v.treeHeaderRect.Y, v.treeHeaderRect.W-2, hs, text)
}

// drawTreePane renders the visible tree rows, highlighting the selected
// operator and color-coding expensive/warned ones.
func (v *PlanView) drawTreePane(s tcell.Screen) {
	pal := theme.Active()
	bg := theme.StylePanel()
	r := v.treePaneRect
	core.FillRect(s, r, ' ', bg)
	if r.H <= 0 {
		return
	}
	for row := 0; row < r.H; row++ {
		idx := v.treeSt.scroll + row
		if idx >= len(v.treeSt.rows) {
			break
		}
		tr := v.treeSt.rows[idx]
		y := r.Y + row
		style := bg
		switch {
		case tr.node.ID == v.selectedID:
			style = theme.StyleSelected()
		case v.nodeCostPct(tr.node) >= expensiveCostThreshold:
			style = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Error)
		case len(tr.node.Warnings) > 0:
			style = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
		}
		core.FillRect(s, core.Rect{X: r.X, Y: y, W: r.W, H: 1}, ' ', style)
		core.DrawTextClipped(s, r.X, y, r.W, style, v.treeRowText(tr))
	}
	if len(v.treeSt.rows) > r.H {
		sbStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Border)
		sbThumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
		core.DrawScrollbar(s, r.Right()-1, r.Y, r.H, len(v.treeSt.rows), r.H, v.treeSt.scroll, sbStyle, sbThumb)
	}
}

// treeRowText builds one row's text: ancestor continuation bars, this
// node's own connector, an expand/collapse chevron (only for operators
// with children), the operator name, cost%, and status/parallelism icons.
func (v *PlanView) treeRowText(tr treeRow) string {
	var sb strings.Builder
	for _, cont := range tr.continuation {
		if cont {
			sb.WriteString("│ ")
		} else {
			sb.WriteString("  ")
		}
	}
	if tr.depth > 0 {
		if tr.lastSib {
			sb.WriteString("└─ ")
		} else {
			sb.WriteString("├─ ")
		}
	}
	if len(tr.node.Children) > 0 {
		if v.treeSt.collapsed[tr.node.ID] {
			sb.WriteString("▶ ")
		} else {
			sb.WriteString("▼ ")
		}
	}
	sb.WriteString(tr.node.PhysicalOp)
	if tr.node.LogicalOp != "" && tr.node.LogicalOp != tr.node.PhysicalOp {
		sb.WriteString(" (")
		sb.WriteString(tr.node.LogicalOp)
		sb.WriteByte(')')
	}
	fmt.Fprintf(&sb, " (%.0f%%)", v.nodeCostPct(tr.node)*100)
	switch {
	case v.nodeCostPct(tr.node) >= expensiveCostThreshold:
		sb.WriteString(" ❌")
	case len(tr.node.Warnings) > 0:
		sb.WriteString(" ⚠")
	}
	if tr.node.Parallel {
		sb.WriteString(" ⇄")
	}
	if !tr.node.Object.IsZero() {
		sb.WriteString("  ")
		sb.WriteString(tr.node.Object.Short())
	}
	return sb.String()
}

// drawBottomSection renders the Properties/Summary title row and the
// active bottom view, while bottomMode isn't hidden.
func (v *PlanView) drawBottomSection(s tcell.Screen) {
	if v.bottomMode == bottomHidden || v.bottomHeaderRect.H == 0 {
		return
	}
	hs := theme.StyleMenuBar()
	title := "Properties  ('o' to cycle)"
	if v.bottomMode == bottomSummary {
		hs, title = v.summaryHeaderStyleAndText()
	}
	core.FillRect(s, v.bottomHeaderRect, ' ', hs)
	text := title
	if v.bottomMode == bottomProperties {
		total := len(nodePropsForDisplay(v.selectedNode()))
		canUp := v.propsSt.scroll > 0
		canDown := v.propsSt.scroll+v.bottomRect.H < total
		text = detailsHeaderText(title, v.bottomHeaderRect.W-2, canUp, canDown)
	}
	core.DrawTextClipped(s, v.bottomHeaderRect.X+1, v.bottomHeaderRect.Y, v.bottomHeaderRect.W-2, hs, text)
	switch v.bottomMode {
	case bottomProperties:
		drawProperties(s, v.bottomRect, v.selectedNode(), v.propsSt.scroll)
	case bottomSummary:
		v.drawSummary(s, v.bottomRect)
	}
}

// cycleBottomMode advances the bottom section hidden → properties →
// summary → hidden, and re-lays-out to match.
func (v *PlanView) cycleBottomMode() {
	switch v.bottomMode {
	case bottomHidden:
		v.bottomMode = bottomProperties
	case bottomProperties:
		v.bottomMode = bottomSummary
	default:
		v.bottomMode = bottomHidden
	}
	v.bottomFocused = false
	v.layoutTree()
}

// selectedRowIndex returns the flat-row index of the current selection,
// or -1 if it isn't in the (possibly collapsed) visible list.
func (v *PlanView) selectedRowIndex() int {
	for i, r := range v.treeSt.rows {
		if r.node.ID == v.selectedID {
			return i
		}
	}
	return -1
}

// moveTreeSelection shifts the selection by delta rows in the flattened
// list, clamping at both ends.
func (v *PlanView) moveTreeSelection(delta int) {
	if len(v.treeSt.rows) == 0 {
		return
	}
	idx := v.selectedRowIndex()
	if idx < 0 {
		idx = 0
	} else {
		idx = core.Clamp(idx+delta, 0, len(v.treeSt.rows)-1)
	}
	v.selectNode(v.treeSt.rows[idx].node.ID)
}

// ensureTreeRowVisible scrolls the tree pane so the current selection is
// on-screen — called whenever the selection changes (see selectNode).
func (v *PlanView) ensureTreeRowVisible() {
	idx := v.selectedRowIndex()
	h := v.treePaneRect.H
	if idx < 0 || h <= 0 {
		return
	}
	if idx < v.treeSt.scroll {
		v.treeSt.scroll = idx
	}
	if idx >= v.treeSt.scroll+h {
		v.treeSt.scroll = idx - h + 1
	}
}

// expandSelected, collapseSelected, and toggleSelectedExpand change the
// selected operator's collapsed state, if it has children, and
// re-flatten the row list to match.
func (v *PlanView) expandSelected() {
	n := v.selectedNode()
	if n == nil || len(n.Children) == 0 || !v.treeSt.collapsed[n.ID] {
		return
	}
	delete(v.treeSt.collapsed, n.ID)
	v.rebuildTreeRows()
}

func (v *PlanView) collapseSelected() {
	n := v.selectedNode()
	if n == nil || len(n.Children) == 0 || v.treeSt.collapsed[n.ID] {
		return
	}
	v.treeSt.collapsed[n.ID] = true
	v.rebuildTreeRows()
}

func (v *PlanView) toggleSelectedExpand() {
	n := v.selectedNode()
	if n == nil || len(n.Children) == 0 {
		return
	}
	if v.treeSt.collapsed[n.ID] {
		delete(v.treeSt.collapsed, n.ID)
	} else {
		v.treeSt.collapsed[n.ID] = true
	}
	v.rebuildTreeRows()
}

// handleTreeTabKey handles navigation, expand/collapse, and the bottom
// section's own key handling while the Tree tab is active.
//
// The Summary table's sort keys (c/r/t) work regardless of which pane
// has focus — they don't collide with anything the tree itself binds.
// Tab switches focus between the tree and the summary table (Properties
// has nothing to navigate, so it doesn't participate); while the summary
// has focus, arrow/PgUp/PgDn/Enter drive its own grid and row-jump
// instead of the tree's — see trySummarySort/bottomFocused.
func (v *PlanView) handleTreeTabKey(ev *tcell.EventKey) bool {
	if core.EvRune(ev) == 'o' {
		v.cycleBottomMode()
		return true
	}
	if v.bottomMode == bottomSummary {
		if v.trySummarySort(ev) {
			return true
		}
		if ev.Key() == tcell.KeyTab {
			v.bottomFocused = !v.bottomFocused
			return true
		}
		if v.bottomFocused {
			return v.handleSummaryKey(ev)
		}
	}
	switch ev.Key() {
	case tcell.KeyUp:
		v.moveTreeSelection(-1)
		return true
	case tcell.KeyDown:
		v.moveTreeSelection(1)
		return true
	case tcell.KeyPgUp:
		v.moveTreeSelection(-core.Max(1, v.treePaneRect.H))
		return true
	case tcell.KeyPgDn:
		v.moveTreeSelection(core.Max(1, v.treePaneRect.H))
		return true
	case tcell.KeyHome:
		if len(v.treeSt.rows) > 0 {
			v.selectNode(v.treeSt.rows[0].node.ID)
		}
		return true
	case tcell.KeyEnd:
		if len(v.treeSt.rows) > 0 {
			v.selectNode(v.treeSt.rows[len(v.treeSt.rows)-1].node.ID)
		}
		return true
	case tcell.KeyRight:
		v.expandSelected()
		return true
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		v.collapseSelected()
		return true
	case tcell.KeyEnter:
		v.toggleSelectedExpand()
		return true
	}
	return false
}

// handleTreeTabMouse handles tree pane scrolling/clicks, the tree|details
// splitter, and forwards to the summary grid while it's shown.
func (v *PlanView) handleTreeTabMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	switch ev.Buttons() {
	case tcell.WheelUp:
		if v.treePaneRect.Contains(mx, my) && v.treeSt.scroll > 0 {
			v.treeSt.scroll--
			return true
		}
		if v.detailsPaneRect.Contains(mx, my) {
			v.scrollDetails(-1)
			return true
		}
		if v.bottomMode == bottomProperties && v.bottomRect.Contains(mx, my) {
			v.scrollBottomProps(-1)
			return true
		}
	case tcell.WheelDown:
		if v.treePaneRect.Contains(mx, my) && v.treeSt.scroll < len(v.treeSt.rows)-v.treePaneRect.H {
			v.treeSt.scroll++
			return true
		}
		if v.detailsPaneRect.Contains(mx, my) {
			v.scrollDetails(1)
			return true
		}
		if v.bottomMode == bottomProperties && v.bottomRect.Contains(mx, my) {
			v.scrollBottomProps(1)
			return true
		}
	case tcell.ButtonNone:
		// The splitter needs to see its own drag-release event to clear
		// sp.dragging — otherwise it stays stuck true and the next plain
		// click anywhere in the tab (not just on the bar) gets misread as a
		// drag continuation, silently moving the divider instead of
		// selecting whatever was clicked. This was a real, shipped bug.
		if v.treeSplit.HandleMouse(ev) {
			v.layoutTree()
			return true
		}
	case tcell.Button1:
		if v.treeSplit.HandleMouse(ev) {
			v.layoutTree()
			return true
		}
		if v.treePaneRect.Contains(mx, my) {
			v.bottomFocused = false
			idx := v.treeSt.scroll + (my - v.treePaneRect.Y)
			if idx >= 0 && idx < len(v.treeSt.rows) {
				n := v.treeSt.rows[idx].node
				if n.ID == v.selectedID {
					v.toggleSelectedExpand()
				} else {
					v.selectNode(n.ID)
				}
			}
			return true
		}
		if v.bottomMode == bottomSummary && v.bottomRect.Contains(mx, my) {
			v.bottomFocused = true
		}
	}
	if v.bottomMode == bottomSummary && v.bottomRect.Contains(mx, my) {
		return v.handleSummaryMouse(ev)
	}
	return false
}

// orDash returns s, or "—" if it's empty — for metrics that may be
// absent from an estimated-only plan.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
