package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// fakeMenuScreen is a minimal tcell.Screen fake for ContextMenu tests: Size()
// drives geometry's edge clamp, and SetContent is a no-op sink so Draw's
// fill/box/text calls don't panic on the embedded (nil) Screen — the same
// approach as dialogs.sizedScreen, extended with SetContent since
// ContextMenu.Draw actually paints (ModalDialog.recentre only ever calls
// Size()).
type fakeMenuScreen struct {
	tcell.Screen
	w, h int
}

func (s *fakeMenuScreen) Size() (int, int) { return s.w, s.h }
func (s *fakeMenuScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
}

func TestContextMenuClickNearRightEdgeHitsClampedItem(t *testing.T) {
	var calls int
	cm := &ContextMenu{}
	// Requested at x=35 on a 40-wide screen; width floors at 20, so the
	// menu must shift left to x=20 to fit — see geometry.
	cm.Show(35, 5, []MenuItem{
		{Label: "Open", Action: func() { calls++ }},
		{Label: "Close"},
	})
	cm.Draw(&fakeMenuScreen{w: 40, h: 20})

	// Item 0 ("Open") is actually drawn at column range [20,40), row 6 —
	// clicking there must hit it.
	handled := cm.HandleMouse(tcell.NewEventMouse(25, 6, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true (click landed inside the drawn menu)")
	}
	if calls != 1 {
		t.Fatalf("Action called %d times, want 1 — click at the drawn (clamped) position must hit \"Open\"", calls)
	}
	if cm.Visible() {
		t.Fatalf("menu stayed open after an item click; want it closed")
	}
}

func TestContextMenuClickPastClampedRightEdgeMisses(t *testing.T) {
	var calls int
	cm := &ContextMenu{}
	cm.Show(35, 5, []MenuItem{
		{Label: "Open", Action: func() { calls++ }},
		{Label: "Close"},
	})
	cm.Draw(&fakeMenuScreen{w: 40, h: 20})

	// The clamped menu spans columns [20,40) (see the right-edge test
	// above); a click at column 45 is genuinely outside it and must not
	// fire "Open" — it's a plain outside click, not a hit.
	cm.HandleMouse(tcell.NewEventMouse(45, 6, tcell.Button1, tcell.ModNone))
	if calls != 0 {
		t.Fatalf("Action called %d times, want 0 — (45,6) is outside the clamped, actually-drawn menu", calls)
	}
	if cm.Visible() {
		t.Fatalf("menu stayed open after an outside click; want it closed")
	}
}

func TestContextMenuClickNearBottomEdgeHitsClampedItem(t *testing.T) {
	var calls int
	cm := &ContextMenu{}
	// h = len(items)+2 = 4; requested y=18 on a 20-tall screen would run
	// off the bottom (18+4=22>20), so geometry must shift it up to y=16.
	cm.Show(5, 18, []MenuItem{
		{Label: "Open"},
		{Label: "Close", Action: func() { calls++ }},
	})
	cm.Draw(&fakeMenuScreen{w: 40, h: 20})

	// "Close" (item 1) is drawn at row drawnY+1+1 = 16+2 = 18.
	handled := cm.HandleMouse(tcell.NewEventMouse(6, 18, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true (click landed inside the drawn menu)")
	}
	if calls != 1 {
		t.Fatalf("Action called %d times, want 1 — click at the drawn (clamped) position must hit \"Close\"", calls)
	}
}

func newTestContextMenuWithDisabled(calls *int) *ContextMenu {
	cm := &ContextMenu{}
	cm.Show(0, 0, []MenuItem{
		{Label: "First", Enabled: func() bool { return false }},
		{Label: "Middle", Action: func() { *calls++ }},
		{Label: "Last", Enabled: func() bool { return false }},
	})
	cm.Draw(&fakeMenuScreen{w: 40, h: 20})
	return cm
}

func TestContextMenuClickOnDisabledItemDoesNotFireButCloses(t *testing.T) {
	var calls int
	cm := newTestContextMenuWithDisabled(&calls)

	// "First" (index 0) is drawn at row drawnY+1+0 = 1.
	cm.HandleMouse(tcell.NewEventMouse(2, 1, tcell.Button1, tcell.ModNone))

	if calls != 0 {
		t.Fatalf("Action fired for a disabled item via mouse click, want it not to")
	}
	if cm.Visible() {
		t.Fatalf("menu stayed open after clicking a disabled item; want it closed, same as any other item")
	}
}

func TestContextMenuHoverDoesNotSelectDisabledItem(t *testing.T) {
	var calls int
	cm := newTestContextMenuWithDisabled(&calls)
	cm.hover = 1 // "Middle"

	// Hover over "Last" (index 2, disabled), drawn at row drawnY+1+2 = 3.
	cm.HandleMouse(tcell.NewEventMouse(2, 3, tcell.ButtonNone, tcell.ModNone))

	if cm.hover != 1 {
		t.Fatalf("hover = %d after hovering a disabled item, want 1 (unchanged, still \"Middle\")", cm.hover)
	}
}

func TestContextMenuEnterOnDisabledItemDoesNotFire(t *testing.T) {
	var calls int
	cm := newTestContextMenuWithDisabled(&calls)
	cm.hover = 0 // force onto the disabled "First", bypassing normal nav

	cm.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))

	if calls != 0 {
		t.Fatalf("Action fired for a disabled item via KeyEnter, want it not to")
	}
	if cm.Visible() {
		t.Fatalf("menu stayed open after KeyEnter on a disabled item; want it closed, same as any other item")
	}
}

func TestContextMenuKeyDownSkipsDisabledItemToNextEnabled(t *testing.T) {
	var calls int
	cm := newTestContextMenuWithDisabled(&calls)
	cm.hover = -1

	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if cm.hover != 1 {
		t.Fatalf("hover = %d after first KeyDown, want 1 (\"Middle\", skipping disabled \"First\")", cm.hover)
	}

	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if cm.hover != 1 {
		t.Fatalf("hover = %d after second KeyDown, want 1 (\"Middle\" again — both neighbors are disabled)", cm.hover)
	}
}

func TestContextMenuHoverOutsideDoesNotClose(t *testing.T) {
	cm := &ContextMenu{}
	cm.Show(5, 5, []MenuItem{{Label: "Open"}})
	cm.Draw(&fakeMenuScreen{w: 40, h: 20})

	handled := cm.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if handled {
		t.Fatalf("HandleMouse() = true, want false (event outside the menu, not swallowed)")
	}
	if !cm.Visible() {
		t.Fatalf("menu closed on a hover outside it; want it to stay open")
	}
}

func TestContextMenuClickOutsideCloses(t *testing.T) {
	cm := &ContextMenu{}
	cm.Show(5, 5, []MenuItem{{Label: "Open"}})
	cm.Draw(&fakeMenuScreen{w: 40, h: 20})

	cm.HandleMouse(tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModNone))
	if cm.Visible() {
		t.Fatalf("menu stayed open after a click outside it; want it closed")
	}
}

func TestContextMenuEscapeCloses(t *testing.T) {
	cm := &ContextMenu{}
	cm.Show(5, 5, []MenuItem{{Label: "Open"}})

	cm.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if cm.Visible() {
		t.Fatalf("menu stayed open after Escape; want it closed")
	}
}

func TestContextMenuHiddenIgnoresEvents(t *testing.T) {
	cm := &ContextMenu{}
	if handled := cm.HandleMouse(tcell.NewEventMouse(5, 5, tcell.Button1, tcell.ModNone)); handled {
		t.Fatalf("HandleMouse() = true, want false when the menu isn't visible")
	}
	if handled := cm.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)); handled {
		t.Fatalf("HandleKey() = true, want false when the menu isn't visible")
	}
}
