package planview

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// graphState holds the Plan (graph) tab's own view state: the current
// layout, scroll position, and whether the detail strip is open.
type graphState struct {
	layout     *graphLayout
	scrollX    int
	scrollY    int
	detailOpen bool
}

// rebuildGraphLayout re-lays-out the current statement's operator tree
// and resets scroll — called whenever the plan or statement changes.
func (v *PlanView) rebuildGraphLayout() {
	st := v.currentStatement()
	if st == nil || st.Root == nil {
		v.graphSt.layout = nil
		return
	}
	v.graphSt.layout = layoutGraph(st.Root)
	v.graphSt.scrollX, v.graphSt.scrollY = 0, 0
}

// layoutGraphTab computes the Plan tab's canvas rect and, while the detail
// strip is open, the Properties block below it — sized by graphSplit, a
// draggable divider defaulting to a 70/30 canvas/strip ratio.
func (v *PlanView) layoutGraphTab() {
	r := v.contentRect
	zero := func() {
		v.graphCanvasRect = core.Rect{}
		v.graphPropsHeaderRect, v.graphPropsRect = core.Rect{}, core.Rect{}
	}
	if r.H <= 0 || r.W <= 0 {
		zero()
		return
	}
	if !v.graphSt.detailOpen {
		zero()
		v.graphCanvasRect = r
		return
	}
	v.graphSplit.SetBounds(r.X, r.Y, r.W, r.H)
	v.graphCanvasRect = v.graphSplit.FirstRect()
	propsRect := v.graphSplit.SecondRect()
	v.graphPropsHeaderRect, v.graphPropsRect = core.Rect{}, core.Rect{}
	if propsRect.H > 1 {
		v.graphPropsHeaderRect = core.Rect{X: propsRect.X, Y: propsRect.Y, W: propsRect.W, H: 1}
		v.graphPropsRect = core.Rect{X: propsRect.X, Y: propsRect.Y + 1, W: propsRect.W, H: propsRect.H - 1}
	} else {
		v.graphPropsRect = propsRect
	}
}

// drawGraphTab renders the operator graph and, while open, the draggable
// splitter bar and Properties block beneath it.
func (v *PlanView) drawGraphTab(s tcell.Screen) {
	if v.graphSt.layout == nil || len(v.graphSt.layout.tiles) == 0 {
		v.drawMessage(s, "No plan tree for this statement")
		return
	}
	v.drawGraphCanvas(s)
	if !v.graphSt.detailOpen {
		return
	}
	v.graphSplit.Draw(s)
	n, st := v.selectedNode(), v.currentStatement()
	total := len(detailLines(n, st))
	canUp := v.graphPropsScroll > 0
	canDown := v.graphPropsScroll+v.graphPropsRect.H < total
	drawDetailsHeader(s, v.graphPropsHeaderRect, "Properties", canUp, canDown)
	drawDetails(s, v.graphPropsRect, n, st, v.graphPropsScroll)
}

// drawGraphCanvas draws every edge then every tile, scrolled by
// graphSt.scrollX/Y and clipped to graphCanvasRect.
func (v *PlanView) drawGraphCanvas(s tcell.Screen) {
	r := v.graphCanvasRect
	bg := theme.StylePanel()
	core.FillRect(s, r, ' ', bg)
	if r.H <= 0 || r.W <= 0 {
		return
	}
	pal := theme.Active()
	for _, e := range v.graphSt.layout.edges {
		v.drawEdge(s, e, pal)
	}
	for _, t := range v.graphSt.layout.tiles {
		v.drawTile(s, t, pal)
	}
	if v.graphSt.layout.canvasW > r.W {
		sbStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Border)
		sbThumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
		core.DrawScrollbar(s, r.X, r.Bottom()-1, r.W, v.graphSt.layout.canvasW, r.W, v.graphSt.scrollX, sbStyle, sbThumb)
	}
	if v.graphSt.layout.canvasH > r.H {
		sbStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Border)
		sbThumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
		core.DrawScrollbar(s, r.Right()-1, r.Y, r.H, v.graphSt.layout.canvasH, r.H, v.graphSt.scrollY, sbStyle, sbThumb)
	}
}

// drawEdge renders one parent→child connector as three clipped line
// segments plus an arrowhead pointing into the parent.
func (v *PlanView) drawEdge(s tcell.Screen, e edge, pal *theme.Palette) {
	r := v.graphCanvasRect
	style := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.TextDim)
	x1, y1 := r.X+e.x1-v.graphSt.scrollX, r.Y+e.y1-v.graphSt.scrollY
	x2, y2 := r.X+e.x2-v.graphSt.scrollX, r.Y+e.y2-v.graphSt.scrollY
	midX := r.X + e.midX - v.graphSt.scrollX

	hlineClipped(s, r, core.Min(x1, midX), y1, absInt(midX-x1), style)
	top, h := y1, y2-y1
	if h < 0 {
		top, h = y2, -h
	}
	vlineClipped(s, r, midX, top, h+1, style)
	hlineClipped(s, r, core.Min(midX, x2), y2, absInt(x2-midX), style)
	putClipped(s, r, x1, y1, '◄', style)
}

// drawTile renders one operator's card. A tile is only drawn when it's
// fully within the viewport — partial-glyph clipping isn't worth the
// complexity for a coarsely-scrolled fixed-size card.
func (v *PlanView) drawTile(s tcell.Screen, t tile, pal *theme.Palette) {
	r := v.graphCanvasRect
	screenRect := core.Rect{
		X: r.X + t.rect.X - v.graphSt.scrollX, Y: r.Y + t.rect.Y - v.graphSt.scrollY,
		W: t.rect.W, H: t.rect.H,
	}
	if !rectFullyIn(r, screenRect) {
		return
	}
	selected := t.node.ID == v.selectedID
	costPct := v.nodeCostPct(t.node)
	borderStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Border)
	switch {
	case selected:
		borderStyle = theme.StyleActiveBorder()
	case costPct >= expensiveCostThreshold:
		borderStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Error)
	case len(t.node.Warnings) > 0:
		borderStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
	}
	if selected {
		drawDoubleBox(s, screenRect, borderStyle)
	} else {
		core.DrawBox(s, screenRect, borderStyle)
	}

	inner := screenRect.Inner(1)
	textStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Text)
	core.DrawTextClipped(s, inner.X, inner.Y, inner.W, textStyle, t.node.PhysicalOp)

	line2 := t.node.LogicalOp
	if line2 == "" || line2 == t.node.PhysicalOp {
		line2 = ""
		if !t.node.Object.IsZero() {
			line2 = t.node.Object.Table
		}
	}
	if line2 != "" {
		core.DrawTextClipped(s, inner.X, inner.Y+1, inner.W, textStyle, line2)
	}

	metrics := fmt.Sprintf("%.0f%%  %s", costPct*100, v.tileRowsText(t.node))
	if t.node.Parallel {
		metrics += "  ⇄"
	}
	core.DrawTextClipped(s, inner.X, inner.Y+2, inner.W, textStyle, metrics)

	// Corner badge: at most one of error/warning, same priority as the
	// border color switch above. Right-aligned by the glyph's own display
	// width (not a fixed 1-column offset) since ❌ is double-width and ⚠
	// isn't — a fixed offset would push ❌ into the border column.
	var badge string
	var badgeStyle tcell.Style
	switch {
	case costPct >= expensiveCostThreshold:
		badge = "❌"
		badgeStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Error)
	case len(t.node.Warnings) > 0:
		badge = "⚠"
		badgeStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
	}
	if badge != "" {
		core.DrawText(s, inner.Right()-core.DisplayWidth(badge), inner.Y, badgeStyle, badge)
	}
}

// drawDoubleBox draws a double-line box border — the selected tile's
// distinct look, matching the mockup's "╔══╗" selection style.
func drawDoubleBox(s tcell.Screen, r core.Rect, style tcell.Style) {
	x, y, w, h := r.X, r.Y, r.W, r.H
	s.SetContent(x, y, '╔', nil, style)
	s.SetContent(x+w-1, y, '╗', nil, style)
	s.SetContent(x, y+h-1, '╚', nil, style)
	s.SetContent(x+w-1, y+h-1, '╝', nil, style)
	for col := x + 1; col < x+w-1; col++ {
		s.SetContent(col, y, '═', nil, style)
		s.SetContent(col, y+h-1, '═', nil, style)
	}
	for row := y + 1; row < y+h-1; row++ {
		s.SetContent(x, row, '║', nil, style)
		s.SetContent(x+w-1, row, '║', nil, style)
	}
}

// putClipped, hlineClipped, and vlineClipped draw single-width
// box-drawing cells, silently discarding anything outside viewport —
// the graph canvas scrolls a virtual plane larger than its own rect, so
// every edge segment needs manual clipping (core's DrawHLine/DrawVLine
// don't clip; they always write exactly where told).
func putClipped(s tcell.Screen, viewport core.Rect, x, y int, ch rune, style tcell.Style) {
	if x < viewport.X || x >= viewport.Right() || y < viewport.Y || y >= viewport.Bottom() {
		return
	}
	s.SetContent(x, y, ch, nil, style)
}

func hlineClipped(s tcell.Screen, viewport core.Rect, x, y, w int, style tcell.Style) {
	for i := 0; i < w; i++ {
		putClipped(s, viewport, x+i, y, '─', style)
	}
}

func vlineClipped(s tcell.Screen, viewport core.Rect, x, y, h int, style tcell.Style) {
	for i := 0; i < h; i++ {
		putClipped(s, viewport, x, y+i, '│', style)
	}
}

// rectFullyIn reports whether inner lies entirely within outer.
func rectFullyIn(outer, inner core.Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y &&
		inner.Right() <= outer.Right() && inner.Bottom() <= outer.Bottom()
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// parentOfNode returns the parent of the node with the given ID within
// root's subtree, or nil if id is root or not found.
func parentOfNode(root *showplan.Node, id int) *showplan.Node {
	if root == nil {
		return nil
	}
	for _, c := range root.Children {
		if c.ID == id {
			return root
		}
		if p := parentOfNode(c, id); p != nil {
			return p
		}
	}
	return nil
}

// ensureTileVisible scrolls the graph canvas so the given operator's
// tile is fully on-screen.
func (v *PlanView) ensureTileVisible(id int) {
	if v.graphSt.layout == nil {
		return
	}
	rect, ok := v.graphSt.layout.rects[id]
	if !ok {
		return
	}
	r := v.graphCanvasRect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if rect.X < v.graphSt.scrollX {
		v.graphSt.scrollX = rect.X
	}
	if rect.Right() > v.graphSt.scrollX+r.W {
		v.graphSt.scrollX = rect.Right() - r.W
	}
	if rect.Y < v.graphSt.scrollY {
		v.graphSt.scrollY = rect.Y
	}
	if rect.Bottom() > v.graphSt.scrollY+r.H {
		v.graphSt.scrollY = rect.Bottom() - r.H
	}
	v.graphSt.scrollX = core.Max(0, v.graphSt.scrollX)
	v.graphSt.scrollY = core.Max(0, v.graphSt.scrollY)
}

// graphSelectParent, graphSelectFirstChild, graphSelectSibling, and
// graphSelectRoot implement the Plan tab's ←/→/↑↓/Home navigation.
func (v *PlanView) graphSelectParent() {
	st := v.currentStatement()
	if st == nil {
		return
	}
	if p := parentOfNode(st.Root, v.selectedID); p != nil {
		v.selectNode(p.ID)
		v.ensureTileVisible(p.ID)
	}
}

func (v *PlanView) graphSelectFirstChild() {
	n := v.selectedNode()
	if n == nil || len(n.Children) == 0 {
		return
	}
	v.selectNode(n.Children[0].ID)
	v.ensureTileVisible(n.Children[0].ID)
}

// graphSelectSibling moves to the previous/next sibling; if there is
// none in that direction, it falls back to the nearest tile in the same
// column (depth) above/below the current one.
func (v *PlanView) graphSelectSibling(delta int) {
	st := v.currentStatement()
	if st == nil || v.graphSt.layout == nil {
		return
	}
	p := parentOfNode(st.Root, v.selectedID)
	siblings := []*showplan.Node{st.Root}
	if p != nil {
		siblings = p.Children
	}
	for i, c := range siblings {
		if c.ID != v.selectedID {
			continue
		}
		if next := i + delta; next >= 0 && next < len(siblings) {
			v.selectNode(siblings[next].ID)
			v.ensureTileVisible(siblings[next].ID)
			return
		}
		break
	}

	cur, ok := v.graphSt.layout.rects[v.selectedID]
	if !ok {
		return
	}
	bestID, bestDist := -1, 0
	for _, t := range v.graphSt.layout.tiles {
		if t.node.ID == v.selectedID || t.rect.X != cur.X {
			continue
		}
		dy := t.rect.Y - cur.Y
		if (delta < 0 && dy >= 0) || (delta > 0 && dy <= 0) {
			continue
		}
		if dist := absInt(dy); bestID == -1 || dist < bestDist {
			bestID, bestDist = t.node.ID, dist
		}
	}
	if bestID != -1 {
		v.selectNode(bestID)
		v.ensureTileVisible(bestID)
	}
}

func (v *PlanView) graphSelectRoot() {
	st := v.currentStatement()
	if st == nil || st.Root == nil {
		return
	}
	v.selectNode(st.Root.ID)
	v.ensureTileVisible(st.Root.ID)
}

// scrollGraphProps shifts the Plan tab's Properties block scroll offset by
// delta rows, clamped to the current selection's line count.
func (v *PlanView) scrollGraphProps(delta int) {
	total := len(detailLines(v.selectedNode(), v.currentStatement()))
	max := core.Max(0, total-v.graphPropsRect.H)
	v.graphPropsScroll = core.Clamp(v.graphPropsScroll+delta, 0, max)
}

// handleGraphTabKey handles the Plan tab's navigation, detail-strip toggle,
// and — while the strip is open — the Operator Summary table's own key
// handling, mirroring handleTreeTabKey's bottomFocused pattern.
func (v *PlanView) handleGraphTabKey(ev *tcell.EventKey) bool {
	// Ctrl+Up/Down resizes the canvas/Properties split — see
	// layout.Splitter.HandleKey. Safe to try unconditionally: it only acts
	// on a Ctrl (not Ctrl+Shift) chord, which nothing below binds.
	if v.graphSt.detailOpen && v.graphSplit.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyLeft:
		v.graphSelectParent()
		return true
	case tcell.KeyRight:
		v.graphSelectFirstChild()
		return true
	case tcell.KeyUp:
		v.graphSelectSibling(-1)
		return true
	case tcell.KeyDown:
		v.graphSelectSibling(1)
		return true
	case tcell.KeyHome:
		v.graphSelectRoot()
		return true
	case tcell.KeyEnter:
		v.graphSt.detailOpen = !v.graphSt.detailOpen
		v.layoutGraphTab()
		return true
	}
	return false
}

// handleGraphTabMouse handles wheel scrolling over the canvas (Shift+wheel
// for horizontal) and the Properties block, dragging graphSplit, and
// clicking a tile to select it.
func (v *PlanView) handleGraphTabMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if v.graphSt.detailOpen {
		if ev.Buttons() == tcell.ButtonNone {
			// The splitter needs its own drag-release event to clear
			// sp.dragging, the same reason handleTreeTabMouse forwards
			// ButtonNone to treeSplit unconditionally (not position-gated —
			// see that case's comment for the shipped bug this avoids).
			if v.graphSplit.HandleMouse(ev) {
				v.layoutGraphTab()
				return true
			}
		}
		switch ev.Buttons() {
		case tcell.WheelUp:
			if v.graphPropsRect.Contains(mx, my) {
				v.scrollGraphProps(-1)
				return true
			}
		case tcell.WheelDown:
			if v.graphPropsRect.Contains(mx, my) {
				v.scrollGraphProps(1)
				return true
			}
		case tcell.Button1:
			if v.graphSplit.HandleMouse(ev) {
				v.layoutGraphTab()
				return true
			}
		}
	}
	if !v.graphCanvasRect.Contains(mx, my) {
		return false
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		if ev.Modifiers()&tcell.ModShift != 0 {
			v.graphSt.scrollX = core.Max(0, v.graphSt.scrollX-4)
		} else {
			v.graphSt.scrollY = core.Max(0, v.graphSt.scrollY-1)
		}
		return true
	case tcell.WheelDown:
		if ev.Modifiers()&tcell.ModShift != 0 {
			v.graphSt.scrollX += 4
		} else {
			v.graphSt.scrollY++
		}
		return true
	case tcell.Button1:
		cx := mx - v.graphCanvasRect.X + v.graphSt.scrollX
		cy := my - v.graphCanvasRect.Y + v.graphSt.scrollY
		for _, t := range v.graphSt.layout.tiles {
			if t.rect.Contains(cx, cy) {
				v.selectNode(t.node.ID)
				break
			}
		}
		return true
	}
	return false
}
