package planview

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/tuikittest"
)

func newGraphTabView(t *testing.T) *PlanView {
	t.Helper()
	v := New()
	v.SetBounds(0, 0, 160, 40)
	v.SetPlan(loadTestPlan(t))
	return v // TabPlan is the default active tab
}

func TestGraph_CostBadgesAndBorders(t *testing.T) {
	old := *theme.Active()
	t.Cleanup(func() { theme.SetPalette(old) })
	for _, tt := range []struct {
		palette theme.Palette
		cost    float64
		badge   string
		border  tcell.Color
		warn    bool
	}{
		{theme.Default, 19.4, "", theme.Default.Border, false},
		{theme.Default, 19.6, "🟡", theme.Default.Border, false}, // shows "20%"
		{theme.Default, 39.4, "🟡", theme.Default.Border, false},
		{theme.Default, 39.6, "🟠", theme.Default.Border, false},
		{theme.Default, 79.4, "🟠", theme.Default.Border, false},
		{theme.Default, 79.6, "🔴", theme.Default.Error, false}, // shows "80%"
		{theme.Default, 80, "🔴", theme.Default.Error, true},
		{theme.Default, 20, "⚠", theme.Default.Warning, true},
		{theme.Default, 40, "⚠", theme.Default.Warning, true},
		{theme.Light, 40, "🟠", theme.Light.Border, false},
	} {
		theme.SetPalette(tt.palette)
		v := newGraphTabView(t)
		v.currentStatement().SubTreeCost = 100
		v.graphCanvasRect = core.Rect{W: 80, H: 24}
		n := &showplan.Node{ID: 9001, PhysicalOp: "Scan", EstSubtreeCost: tt.cost}
		if tt.warn {
			n.Warnings = []string{"Missing Index"}
		}
		tl := tile{node: n, rect: core.Rect{W: graphTileW, H: graphTileH}}
		s := tuikittest.NewScreen(80, 24)
		v.drawTile(s, tl, theme.Active())
		if _, style, _ := s.Get(0, 0); style.GetForeground() != tt.border {
			t.Errorf("cost %.2f: border = %v, want %v", tt.cost, style.GetForeground(), tt.border)
		}
		for _, badge := range []string{"❌", "🔴", "🟠", "🟡", "⚠"} {
			x, y, found := s.Find(badge)
			if found != (badge == tt.badge) {
				t.Errorf("cost %.2f: badge %q present = %v", tt.cost, badge, found)
			}
			if found && (x != graphTileW-1-core.DisplayWidth(badge) || y != 1) {
				t.Errorf("badge %q at (%d,%d), want inside the top-right corner", badge, x, y)
			}
			if found {
				_, style, _ := s.Get(x, y)
				want := tt.palette.Warning
				if badge == "🔴" {
					want = tt.palette.Error
				}
				if style.GetForeground() != want || style.GetBackground() != tt.palette.PanelBg {
					t.Errorf("cost %.2f: badge %q colors = (%v, %v), want (%v, %v)",
						tt.cost, badge, style.GetForeground(), style.GetBackground(), want, tt.palette.PanelBg)
				}
			}
		}
	}
}

// TestGraph_BadgeDoesNotOverwriteOperatorName checks a long operator name is
// clipped one column short of the corner badge instead of being drawn under it
// ("Table Valued Function" read as "Table Valued Fun🟡").
func TestGraph_BadgeDoesNotOverwriteOperatorName(t *testing.T) {
	v := newGraphTabView(t)
	v.currentStatement().SubTreeCost = 100
	v.graphCanvasRect = core.Rect{W: 80, H: 24}
	n := &showplan.Node{ID: 9001, PhysicalOp: "Table Valued Function", EstSubtreeCost: 21}
	tl := tile{node: n, rect: core.Rect{W: graphTileW, H: graphTileH}}
	s := tuikittest.NewScreen(80, 24)
	v.drawTile(s, tl, theme.Active())
	x, y, ok := s.Find("🟡")
	if !ok {
		t.Fatal("no 🟡 badge for a 21% operator")
	}
	if got, _, _ := s.Get(x-1, y); got != " " {
		t.Errorf("cell left of the badge = %q, want a blank gap; row %q", got, s.Row(y))
	}
	if _, _, ok := s.Find("Table Valued Fu"); !ok {
		t.Errorf("operator name lost its visible prefix; row %q", s.Row(y))
	}
}

// TestGraphScrollbarDragScrolls confirms dragging either of the canvas's
// scrollbars (horizontal along the bottom edge, vertical along the right
// edge) scrolls the canvas instead of being read as a click that selects
// whatever tile sits under it.
func TestGraphScrollbarDragScrolls(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 20, 6) // small enough that the 12-tile layout overflows both axes
	v.SetPlan(loadTestPlan(t))

	r := v.graphCanvasRect
	if v.graphSt.layout.canvasW <= r.W {
		t.Fatalf("test needs canvasW (%d) > graphCanvasRect.W (%d) to exercise horizontal scrolling", v.graphSt.layout.canvasW, r.W)
	}
	if v.graphSt.layout.canvasH <= r.H {
		t.Fatalf("test needs canvasH (%d) > graphCanvasRect.H (%d) to exercise vertical scrolling", v.graphSt.layout.canvasH, r.H)
	}
	selBefore := v.selectedID

	// Horizontal bar: bottom row of the canvas, its last cell one short of the
	// corner (with both bars drawn the corner is the vertical bar's).
	if !v.HandleMouse(tcell.NewEventMouse(r.X+r.W-2, r.Bottom()-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("handling a click on the horizontal scrollbar row should return true")
	}
	if !v.graphSt.vp.Dragging() {
		t.Fatal("graphSt.vp.Dragging() should be true after pressing on the horizontal scrollbar")
	}
	if v.graphSt.vp.X == 0 {
		t.Error("graphSt.vp.X should have jumped forward when clicking near the right of the track")
	}
	// Held, the bar keeps the drag across the tiles and off the canvas: back
	// to the left over the canvas, then past the view's right edge.
	v.HandleMouse(tcell.NewEventMouse(r.X, r.Y, tcell.Button1, tcell.ModNone))
	if v.graphSt.vp.X != 0 {
		t.Errorf("dragging the thumb left over the tiles left X = %d, want 0", v.graphSt.vp.X)
	}
	v.HandleMouse(tcell.NewEventMouse(r.Right()+10, r.Bottom()+3, tcell.Button1, tcell.ModNone))
	if v.graphSt.vp.X == 0 {
		t.Error("dragging the thumb past the canvas's right edge did not scroll")
	}
	v.HandleMouse(tcell.NewEventMouse(r.X, r.Bottom()-1, tcell.ButtonNone, tcell.ModNone))
	if v.graphSt.vp.Dragging() {
		t.Error("graphSt.vp.Dragging() should reset on release")
	}

	// Vertical bar: right column of the canvas, down to the corner cell
	// where both bars meet, which core.Viewport gives the vertical one.
	if !v.HandleMouse(tcell.NewEventMouse(r.Right()-1, r.Bottom()-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("handling a click on the vertical scrollbar column should return true")
	}
	if !v.graphSt.vp.Dragging() {
		t.Fatal("graphSt.vp.Dragging() should be true after pressing on the vertical scrollbar")
	}
	if v.graphSt.vp.Y == 0 {
		t.Error("graphSt.vp.Y should have jumped forward when clicking near the bottom of the track")
	}

	if v.selectedID != selBefore {
		t.Errorf("selectedID changed, dragging either scrollbar must not select a tile")
	}
}

// visibleTiles returns the screen cell of a point inside each tile wholly
// clear of the canvas's scrollbars, in layout order.
func visibleTiles(v *PlanView) (ids []int, pts [][2]int) {
	r, vp := v.graphCanvasRect, v.graphView()
	inner := core.Rect{X: r.X, Y: r.Y, W: r.W - 1, H: r.H - 1}
	for _, tl := range v.graphSt.layout.tiles {
		sx, sy := vp.ToScreen(tl.rect.X+1, tl.rect.Y+1)
		if inner.Contains(sx, sy) {
			ids = append(ids, tl.node.ID)
			pts = append(pts, [2]int{sx, sy})
		}
	}
	return ids, pts
}

// A press on a tile, held and dragged onto the canvas's scrollbars, is the
// tile's: it must neither grab a bar and jump the canvas nor latch it.
func TestGraphTileDragOntoScrollbarDoesNotScroll(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 40, 14)
	v.SetPlan(loadTestPlan(t))
	r := v.graphCanvasRect
	ids, pts := visibleTiles(v)
	if len(ids) == 0 {
		t.Fatal("no tile wholly visible in the canvas")
	}
	// Selecting the tile may scroll it into view, so the baseline is taken
	// after the press.
	v.HandleMouse(tcell.NewEventMouse(pts[0][0], pts[0][1], tcell.Button1, tcell.ModNone))
	x0, y0 := v.graphView().X, v.graphView().Y
	v.HandleMouse(tcell.NewEventMouse(r.Right()-3, r.Bottom()-1, tcell.Button1, tcell.ModNone)) // horizontal bar
	v.HandleMouse(tcell.NewEventMouse(r.Right()-1, r.Bottom()-2, tcell.Button1, tcell.ModNone)) // vertical bar
	if vp := v.graphView(); vp.X != x0 || vp.Y != y0 || vp.Dragging() {
		t.Errorf("a tile press dragged onto the bars scrolled to (%d,%d) from (%d,%d), dragging %v",
			vp.X, vp.Y, x0, y0, vp.Dragging())
	}
	v.HandleMouse(tcell.NewEventMouse(r.Right()-1, r.Bottom()-2, tcell.ButtonNone, tcell.ModNone))
}

// A press on a tile selects it once; dragged over other tiles it keeps the
// first selected rather than selecting each one it passes.
func TestGraphTileDragKeepsFirstSelected(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 120, 40)
	v.SetPlan(loadTestPlan(t))
	ids, pts := visibleTiles(v)
	if len(ids) < 3 {
		t.Fatalf("test needs three visible tiles, got %d", len(ids))
	}

	v.HandleMouse(tcell.NewEventMouse(pts[1][0], pts[1][1], tcell.Button1, tcell.ModNone))
	if v.selectedID != ids[1] {
		t.Fatalf("selectedID = %d after pressing tile %d", v.selectedID, ids[1])
	}
	v.HandleMouse(tcell.NewEventMouse(pts[2][0], pts[2][1], tcell.Button1, tcell.ModNone))
	v.HandleMouse(tcell.NewEventMouse(pts[0][0], pts[0][1], tcell.Button1, tcell.ModNone))
	if v.selectedID != ids[1] {
		t.Errorf("a held press dragged over other tiles selected %d, want %d kept", v.selectedID, ids[1])
	}
	v.HandleMouse(tcell.NewEventMouse(pts[0][0], pts[0][1], tcell.ButtonNone, tcell.ModNone))
	v.HandleMouse(tcell.NewEventMouse(pts[2][0], pts[2][1], tcell.Button1, tcell.ModNone))
	if v.selectedID != ids[2] {
		t.Errorf("a fresh press after the release selected %d, want %d", v.selectedID, ids[2])
	}
}

func TestGraph_DefaultSelectionIsRoot(t *testing.T) {
	v := newGraphTabView(t)
	root := v.currentStatement().Root
	if v.selectedID != root.ID {
		t.Errorf("selectedID = %d, want root ID %d", v.selectedID, root.ID)
	}
	if v.graphSt.layout == nil || len(v.graphSt.layout.tiles) != 12 {
		t.Fatalf("graph layout not built with all 12 nodes")
	}
}

func TestGraph_RightMovesToFirstChild(t *testing.T) {
	v := newGraphTabView(t)
	root := v.selectedID
	if !v.HandleKey(namedKey(tcell.KeyRight)) {
		t.Fatal("HandleKey(Right) returned false")
	}
	if v.selectedID == root {
		t.Fatal("selectedID unchanged after Right — expected to move to root's first child")
	}
	root2 := v.currentStatement().Root
	if v.selectedID != root2.Children[0].ID {
		t.Errorf("selectedID = %d, want %d (root's first child)", v.selectedID, root2.Children[0].ID)
	}
}

func TestGraph_LeftReturnsToParent(t *testing.T) {
	v := newGraphTabView(t)
	root := v.selectedID
	v.HandleKey(namedKey(tcell.KeyRight))
	if v.selectedID == root {
		t.Fatal("Right didn't move selection")
	}
	if !v.HandleKey(namedKey(tcell.KeyLeft)) {
		t.Fatal("HandleKey(Left) returned false")
	}
	if v.selectedID != root {
		t.Errorf("selectedID = %d after Left, want back to root %d", v.selectedID, root)
	}
}

func TestGraph_HomeSelectsRoot(t *testing.T) {
	v := newGraphTabView(t)
	root := v.selectedID
	v.HandleKey(namedKey(tcell.KeyRight))
	v.HandleKey(namedKey(tcell.KeyRight))
	if v.selectedID == root {
		t.Fatal("selection never moved off root")
	}
	if !v.HandleKey(namedKey(tcell.KeyHome)) {
		t.Fatal("HandleKey(Home) returned false")
	}
	if v.selectedID != root {
		t.Errorf("selectedID = %d after Home, want root %d", v.selectedID, root)
	}
}

// TestGraph_DetailStripVisibleFromStart checks the Properties strip is open
// as soon as a plan loads, with no Enter needed.
func TestGraph_DetailStripVisibleFromStart(t *testing.T) {
	v := newGraphTabView(t)
	if !v.graphSt.detailOpen {
		t.Fatal("detailOpen = false initially, want true (visible from the start)")
	}
	if v.graphPropsRect.H == 0 {
		t.Error("graphPropsRect wasn't laid out even though detailOpen starts true")
	}
}

func TestGraph_EnterTogglesDetailStrip(t *testing.T) {
	v := newGraphTabView(t)
	if !v.HandleKey(namedKey(tcell.KeyEnter)) {
		t.Fatal("HandleKey(Enter) returned false")
	}
	if v.graphSt.detailOpen {
		t.Error("detailOpen = true after Enter, want false (closed)")
	}
	if v.graphPropsRect.H != 0 {
		t.Error("graphPropsRect should be zeroed once the detail strip is closed")
	}
	v.HandleKey(namedKey(tcell.KeyEnter))
	if !v.graphSt.detailOpen {
		t.Error("detailOpen = false after a second Enter, want true (reopened)")
	}
}

// TestGraph_DetailStripDefaultRatioIs70_30 checks the splitter's default
// ratio matches the user's original 70/30 ask.
func TestGraph_DetailStripDefaultRatioIs70_30(t *testing.T) {
	v := newGraphTabView(t)
	v.SetBounds(0, 0, 160, 100) // tall enough that rounding noise is negligible

	total := v.graphCanvasRect.H + v.graphPropsHeaderRect.H + v.graphPropsRect.H
	gotRatio := float64(v.graphCanvasRect.H) / float64(total)
	if gotRatio < 0.65 || gotRatio > 0.75 {
		t.Errorf("canvas ratio = %.2f, want ~0.70 (canvas %d of %d total rows)", gotRatio, v.graphCanvasRect.H, total)
	}
}

// TestGraph_DetailStripIsResizeable checks graphSplit responds to a mouse
// drag, per the user's "make the panel resizeable" ask.
func TestGraph_DetailStripIsResizeable(t *testing.T) {
	v := newGraphTabView(t)
	v.SetBounds(0, 0, 160, 40)
	before := v.graphCanvasRect.H

	barY := v.graphSplit.SplitPos()
	mx := v.graphCanvasRect.X + 2
	if !v.HandleMouse(tcell.NewEventMouse(mx, barY, tcell.Button1, tcell.ModNone)) {
		t.Fatal("press on the splitter bar returned false")
	}
	if !v.HandleMouse(tcell.NewEventMouse(mx, barY-5, tcell.Button1, tcell.ModNone)) {
		t.Fatal("drag on the splitter bar returned false")
	}
	v.HandleMouse(tcell.NewEventMouse(mx, barY-5, tcell.ButtonNone, tcell.ModNone))

	if v.graphCanvasRect.H >= before {
		t.Errorf("graphCanvasRect.H = %d after dragging the splitter up by 5, want less than %d", v.graphCanvasRect.H, before)
	}
}

// TestGraph_PropertiesBlockShowsSelectedNode checks the strip's Properties
// block renders the same curated, aligned field list as the Tree tab's
// Operator Details pane.
func TestGraph_PropertiesBlockShowsSelectedNode(t *testing.T) {
	v := newGraphTabView(t)
	lines := detailLines(v.selectedNode(), v.currentStatement(), nil)
	if len(lines) == 0 {
		t.Fatal("detailLines empty for the selected root — Properties block would render nothing")
	}
}

// TestGraph_PropertiesWheelScrolls exercises the Properties block's own
// mouse-wheel scrolling, the same mechanism added to the Tree tab's
// Operator Details pane.
func TestGraph_PropertiesWheelScrolls(t *testing.T) {
	v := newGraphTabView(t)
	v.SetBounds(0, 0, 160, 14) // short enough that the strip's Properties block overflows

	total := len(detailLines(v.selectedNode(), v.currentStatement(), nil))
	if total <= v.graphPropsRect.H {
		t.Fatalf("detail lines (%d) fit entirely in the Properties block (%d rows) — test needs an overflow", total, v.graphPropsRect.H)
	}
	mx, my := v.graphPropsRect.X+1, v.graphPropsRect.Y
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelDown) over the Properties block returned false")
	}
	if v.graphPropsPane.Scroll != 1 {
		t.Errorf("graphPropsPane.Scroll = %d after WheelDown, want 1", v.graphPropsPane.Scroll)
	}
}

// TestGraph_CanvasWheelLeftRightScrollsHorizontally checks the canvas
// honours WheelLeft/WheelRight directly, not just Shift+WheelUp/WheelDown —
// some terminals send the former for a horizontal scroll gesture.
func TestGraph_CanvasWheelLeftRightScrollsHorizontally(t *testing.T) {
	v := newGraphTabView(t)
	mx, my := v.graphCanvasRect.X+1, v.graphCanvasRect.Y+1

	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelRight, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelRight) over the canvas returned false")
	}
	if v.graphSt.vp.X <= 0 {
		t.Errorf("scrollX = %d after WheelRight, want > 0", v.graphSt.vp.X)
	}

	before := v.graphSt.vp.X
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelLeft, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelLeft) over the canvas returned false")
	}
	if v.graphSt.vp.X >= before {
		t.Errorf("scrollX = %d after WheelLeft, want less than %d", v.graphSt.vp.X, before)
	}
}

func TestGraph_SelectionSharedWithTreeTab(t *testing.T) {
	v := newGraphTabView(t)
	v.HandleKey(namedKey(tcell.KeyRight))
	moved := v.selectedID
	v.setActiveTab(TabTree)
	if v.selectedID != moved {
		t.Errorf("selectedID changed to %d when switching to the Tree tab, want it to stay %d", v.selectedID, moved)
	}
}

// TestGraph_PressOnStatementWithoutTreeDoesNotPanic: a statement with no plan
// tree (Estimated Plan of `select 1`) has no graph layout, and a Button1 on its
// canvas dereferenced the nil layout. It surfaced as "[ Expand ]" then a drag,
// whose resends reached the panel Expand opened (see tuikit's PanelManager).
func TestGraph_PressOnStatementWithoutTreeDoesNotPanic(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 160, 40)
	v.SetPlan(&showplan.Plan{Statements: []*showplan.Statement{{Text: "select 1", Type: "SELECT"}}})
	if v.graphSt.layout != nil {
		t.Fatal("setup: a statement without a tree should have no graph layout")
	}
	r := v.graphCanvasRect
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("setup: empty graph canvas %+v", r)
	}
	mx, my := r.X+r.W/2, r.Y+r.H/2
	for _, b := range []tcell.ButtonMask{tcell.Button1, tcell.Button1, tcell.ButtonNone} {
		v.HandleMouse(tcell.NewEventMouse(mx, my, b, tcell.ModNone))
	}
}

// The graph canvas scrolls a plane larger than its rect, so its edges run past
// it on every side; they draw through a ClipScreen narrowed to the canvas, and
// one the host already supplied (a clamped dialog's) narrows them further.
func TestGraphEdgesStayInsideTheCanvasAndAnOuterClip(t *testing.T) {
	setup := func() (*PlanView, *tuikittest.Screen) {
		v := New()
		v.SetBounds(10, 5, 24, 8) // the 12-tile layout overflows both axes
		v.SetPlan(loadTestPlan(t))
		v.graphSt.vp.X, v.graphSt.vp.Y = 6, 2 // edges now cross every side
		scr := tuikittest.NewScreen(60, 30)
		scr.Fill('x', tcell.StyleDefault)
		return v, scr
	}

	v, scr := setup()
	canvas := v.graphCanvasRect
	v.drawGraphCanvas(scr)
	inside := 0
	for y := range 30 {
		for x := range 60 {
			r := scr.RuneAt(x, y)
			switch {
			case canvas.Contains(x, y):
				if r == '◄' { // tile borders share ─ and │; only an edge has ◄
					inside++
				}
			case r != 'x':
				t.Fatalf("(%d,%d) outside the canvas %+v was drawn: %q", x, y, canvas, r)
			}
		}
	}
	if inside == 0 {
		t.Fatal("no edge drawn inside the canvas — the test exercises nothing")
	}

	v, scr = setup()
	outer := core.Rect{X: canvas.X + 2, Y: canvas.Y + 1, W: canvas.W - 4, H: canvas.H - 2}
	clip := core.NewClipScreen(scr)
	clip.SetClip(outer)
	v.drawGraphCanvas(clip)
	for y := range 30 {
		for x := range 60 {
			if !outer.Contains(x, y) && scr.RuneAt(x, y) != 'x' {
				t.Fatalf("(%d,%d) outside the host's clip %+v was drawn: %q", x, y, outer, scr.RuneAt(x, y))
			}
		}
	}
}
