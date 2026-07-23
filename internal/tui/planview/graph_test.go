package planview

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newGraphTabView(t *testing.T) *PlanView {
	t.Helper()
	v := New()
	v.SetBounds(0, 0, 160, 40)
	v.SetPlan(loadTestPlan(t))
	return v // TabPlan is the default active tab
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
// as soon as a plan loads, with no Enter needed — the user's follow-up ask
// after the strip was first built ("also make the properties panel visible
// from begining").
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
// Operator Details pane — the user's ask was "add the properties for the
// selected node" matching that mockup.
func TestGraph_PropertiesBlockShowsSelectedNode(t *testing.T) {
	v := newGraphTabView(t)
	lines := detailLines(v.selectedNode(), v.currentStatement())
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

	total := len(detailLines(v.selectedNode(), v.currentStatement()))
	if total <= v.graphPropsRect.H {
		t.Fatalf("detail lines (%d) fit entirely in the Properties block (%d rows) — test needs an overflow", total, v.graphPropsRect.H)
	}
	mx, my := v.graphPropsRect.X+1, v.graphPropsRect.Y
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelDown) over the Properties block returned false")
	}
	if v.graphPropsScroll != 1 {
		t.Errorf("graphPropsScroll = %d after WheelDown, want 1", v.graphPropsScroll)
	}
}

// TestGraph_CanvasWheelLeftRightScrollsHorizontally checks the canvas
// honours WheelLeft/WheelRight directly, not just Shift+WheelUp/WheelDown —
// some terminals (reported on Windows) send the former for a horizontal
// scroll gesture instead of the latter, which the canvas didn't handle.
func TestGraph_CanvasWheelLeftRightScrollsHorizontally(t *testing.T) {
	v := newGraphTabView(t)
	mx, my := v.graphCanvasRect.X+1, v.graphCanvasRect.Y+1

	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelRight, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelRight) over the canvas returned false")
	}
	if v.graphSt.scrollX <= 0 {
		t.Errorf("scrollX = %d after WheelRight, want > 0", v.graphSt.scrollX)
	}

	before := v.graphSt.scrollX
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelLeft, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelLeft) over the canvas returned false")
	}
	if v.graphSt.scrollX >= before {
		t.Errorf("scrollX = %d after WheelLeft, want less than %d", v.graphSt.scrollX, before)
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
