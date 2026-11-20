package planview

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/theme"
)

// graphState holds the Plan tab's view state: layout, scroll, detail strip.
type graphState struct {
	layout     *graphLayout
	detailOpen bool

	// vp is the canvas scroll and its scrollbar drag latches; graphView sets
	// its geometry. The bars are drawn inside the canvas rect.
	vp core.Viewport
}

// graphView is the graph canvas's viewport, its geometry refreshed from
// graphCanvasRect and the current layout, and the offset clamped to them.
func (v *PlanView) graphView() *core.Viewport {
	vp := &v.graphSt.vp
	vp.View, vp.BarsInside = v.graphCanvasRect, true
	vp.ContentW, vp.ContentH = 0, 0
	if l := v.graphSt.layout; l != nil {
		vp.ContentW, vp.ContentH = l.canvasW, l.canvasH
	}
	vp.Clamp()
	return vp
}

// rebuildGraphLayout re-lays-out the statement's tree and resets scroll, on
// plan or statement change.
func (v *PlanView) rebuildGraphLayout() {
	st := v.currentStatement()
	if st == nil || st.Root == nil {
		v.graphSt.layout = nil
		return
	}
	v.graphSt.layout = layoutGraph(st.Root, v.graphTileHeight())
	v.graphSt.vp.X, v.graphSt.vp.Y = 0, 0
}

// layoutGraphTab computes the canvas rect and, while the strip is open, the
// Properties block below it, sized by graphSplit (draggable, default 70/30).
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

// drawGraphTab renders the graph and, while open, the splitter and Properties.
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
	live := v.liveCountersPtr(n)
	drawDetails(s, v.graphPropsRect, &v.graphPropsPane, n, st, live) // clamps first, as in drawTreeTab
	total := len(detailLines(n, st, live))
	p := &v.graphPropsPane
	drawDetailsHeader(s, v.graphPropsHeaderRect, "Properties", p.CanUp(), p.CanDown(total, v.graphPropsRect.H))
}

// drawGraphCanvas draws edges then tiles, scrolled and clipped to the canvas.
func (v *PlanView) drawGraphCanvas(s tcell.Screen) {
	r := v.graphCanvasRect
	bg := theme.StylePanel()
	core.FillRect(s, r, ' ', bg)
	if r.H <= 0 || r.W <= 0 {
		return
	}
	// Clamped before the edges and tiles use the offset: a resize can shrink
	// the view under an older one.
	vp := v.graphView()
	pal := theme.Active()
	// Edges run off the canvas, which scrolls a plane larger than its rect,
	// and core's DrawHLine/DrawVLine write exactly where told: they draw
	// through a ClipScreen narrowed to the canvas. Wrapping s rather than
	// narrowing it keeps a clip s already carries (a clamped dialog's), with
	// nothing to restore.
	edges := core.NewClipScreen(s)
	edges.SetClip(r)
	for _, e := range v.graphSt.layout.edges {
		v.drawEdge(edges, e, pal)
	}
	for _, t := range v.graphSt.layout.tiles {
		v.drawTile(s, t, pal)
	}
	sbStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Border)
	sbThumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
	vp.DrawScrollbars(s, sbStyle, sbThumb)
}

// drawEdge renders one connector as three segments plus an arrowhead into the
// parent. s clips to the canvas (drawGraphCanvas).
func (v *PlanView) drawEdge(s tcell.Screen, e edge, pal *theme.Palette) {
	r := v.graphCanvasRect
	style := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.TextDim)
	sx, sy := v.graphSt.vp.X, v.graphSt.vp.Y
	x1, y1 := r.X+e.x1-sx, r.Y+e.y1-sy
	x2, y2 := r.X+e.x2-sx, r.Y+e.y2-sy
	midX := r.X + e.midX - sx

	core.DrawHLine(s, min(x1, midX), y1, absInt(midX-x1), style)
	top, h := y1, y2-y1
	if h < 0 {
		top, h = y2, -h
	}
	core.DrawVLine(s, midX, top, h+1, style)
	core.DrawHLine(s, min(midX, x2), y2, absInt(x2-midX), style)
	core.PutRune(s, x1, y1, '◄', style)
}

// drawTile renders one operator's card, only when fully within the viewport
// (partial clipping isn't worth it for a fixed-size card).
func (v *PlanView) drawTile(s tcell.Screen, t tile, pal *theme.Palette) {
	r := v.graphCanvasRect
	screenRect := core.Rect{
		X: r.X + t.rect.X - v.graphSt.vp.X, Y: r.Y + t.rect.Y - v.graphSt.vp.Y,
		W: t.rect.W, H: t.rect.H,
	}
	if !rectFullyIn(r, screenRect) {
		return
	}
	selected := t.node.ID == v.selectedID
	costPct := v.nodeCostPct(t.node)
	live, liveOK := v.liveFor(t.node)
	borderStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Border)
	textStyle := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Text)
	switch {
	case selected:
		borderStyle = theme.StyleActiveBorder()
	case v.liveOn:
		// In live mode the border shows the operator's run state; the badge
		// below still marks the rest.
		borderStyle = borderStyle.Foreground(liveStateColor(pal, live, liveOK))
	case costAtLeast(costPct, expensiveCostThreshold):
		borderStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Error)
	case len(t.node.Warnings) > 0:
		borderStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
	}
	// A double-line border is the selected tile's distinct look.
	box := core.SingleBox
	if selected {
		box = core.DoubleBox
	}
	core.DrawBoxWith(s, screenRect, borderStyle, box)

	if v.liveOn && (!liveOK || live.State == showplan.LiveNotStarted) {
		textStyle = textStyle.Foreground(pal.TextDim)
	}

	// Corner badge: at most one of cost/warning, same priority as the border
	// color above. Right-aligned by the glyph's display width, not a fixed
	// offset: the circle glyphs are double-width and would hit the border.
	var badge string
	var badgeStyle tcell.Style
	switch {
	case costAtLeast(costPct, expensiveCostThreshold):
		badge = "🔴"
		badgeStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Error)
	case len(t.node.Warnings) > 0:
		badge = "⚠"
		badgeStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
	case costAtLeast(costPct, expensiveCostThreshold/2):
		badge = "🟠"
		badgeStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
	case costAtLeast(costPct, expensiveCostThreshold/4):
		badge = "🟡"
		badgeStyle = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
	}

	// The operator name stops one column short of the badge rather than
	// running under it: overdrawn, "Table Valued Function" read as
	// "Table Valued Fun🟡", the badge glued to a cut word.
	inner := screenRect.Inner(1)
	nameW := inner.W
	if badge != "" {
		nameW -= core.DisplayWidth(badge) + 1
	}
	core.DrawTextClipped(s, inner.X, inner.Y, nameW, textStyle, t.node.DisplayOp())

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

	if v.liveOn {
		v.drawLiveTileLines(s, inner, textStyle, t.node, costPct, live, liveOK)
	} else {
		metrics := fmt.Sprintf("%.0f%%  %s", costPct*100, v.tileRowsText(t.node))
		if t.node.Parallel {
			metrics += "  ⇄"
		}
		core.DrawTextClipped(s, inner.X, inner.Y+2, inner.W, textStyle, metrics)
	}

	if badge != "" {
		core.DrawText(s, inner.Right()-core.DisplayWidth(badge), inner.Y, badgeStyle, badge)
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
	v.graphView().EnsureVisible(rect)
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

// graphSelectSibling moves to the previous/next sibling, else the nearest tile
// above/below in the same column.
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
	n := v.selectedNode()
	total := len(detailLines(n, v.currentStatement(), v.liveCountersPtr(n)))
	v.graphPropsPane.ScrollBy(delta, total, v.graphPropsRect.H)
}

// handleGraphTabKey handles Plan tab navigation, the strip toggle, and (strip
// open) the summary table's keys, mirroring handleTreeTabKey's bottomFocused.
func (v *PlanView) handleGraphTabKey(ev *tcell.EventKey) bool {
	// Ctrl+Up/Down resizes the split (layout.Splitter.HandleKey). Safe to try
	// unconditionally: it acts only on a Ctrl (not Ctrl+Shift) chord, which
	// nothing below binds.
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

// handleGraphTabMouse handles wheel scrolling (Shift+wheel or WheelLeft/Right
// for horizontal), dragging graphSplit, and clicking a tile.
func (v *PlanView) handleGraphTabMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	vp := v.graphView()
	if ev.Buttons() == tcell.ButtonNone {
		vp.Release()
	}
	if v.graphSt.detailOpen {
		if ev.Buttons() == tcell.ButtonNone {
			// The splitter needs its own release to clear sp.dragging (why
			// handleTreeTabMouse forwards ButtonNone to treeSplit unconditionally).
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
	// A latched scrollbar drag keeps the bar even once the pointer leaves
	// the canvas.
	if vp.Dragging() {
		return vp.HandleScrollbars(ev)
	}
	if !v.graphCanvasRect.Contains(mx, my) {
		return false
	}
	// Shift+wheel scrolls sideways too (core.WheelDelta), as DataGrid and
	// Editor do.
	if wheel, _ := vp.Wheel(ev, core.WheelCells, core.WheelRows); wheel {
		return true
	}
	switch ev.Buttons() {
	case tcell.Button1:
		// Scrollbar drag/click outranks tile selection: the bars are drawn over
		// the canvas's bottom row/right column (see drawGraphCanvas), so a click
		// would otherwise select the tile underneath.
		if vp.HandleScrollbars(ev) {
			return true
		}
		// No layout on a statement without a plan tree ("No plan tree for this
		// statement"); a press there has nothing to select.
		if v.graphSt.layout == nil {
			return true
		}
		cx, cy := vp.ToCanvas(mx, my)
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
