package layout

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// comboScreen records what Draw actually paints, so the tests below can ask
// which panel is on a given row instead of recomputing the geometry they are
// supposed to be checking. Same minimal-fake approach as
// controls.fakeMenuScreen.
type comboScreen struct {
	tcell.Screen
	w, h  int
	cells map[[2]int]rune
}

func newComboScreen(w, h int) *comboScreen {
	return &comboScreen{w: w, h: h, cells: map[[2]int]rune{}}
}

func (s *comboScreen) Size() (int, int) { return s.w, s.h }

func (s *comboScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
	s.cells[[2]int{x, y}] = primary
}

// row returns the text painted on row y between columns [x, x+w).
func (s *comboScreen) row(x, y, w int) string {
	var b strings.Builder
	for i := range w {
		r, ok := s.cells[[2]int{x + i, y}]
		if !ok {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// openCombo fills pm with n panels, sizes it, and clicks the tab bar's [v]
// arrow. The release first is what makes the click a fresh press rather than
// a continued hold (see mouseDragging).
func openCombo(t *testing.T, n, w, h int) (*PanelManager, *comboScreen) {
	t.Helper()
	pm := NewPanelManager()
	for i := range n {
		pm.AddPanel(&fakePanel{title: "Panel " + string(rune('A'+i%26)) + string(rune('0'+i/26)), closable: true})
	}
	pm.SetBounds(0, 0, w, h)
	pm.HandleMouse(tcell.NewEventMouse(w-2, 0, tcell.ButtonNone, tcell.ModNone))
	if !pm.HandleMouse(tcell.NewEventMouse(w-2, 0, tcell.Button1, tcell.ModNone)) {
		t.Fatal("click on the combo arrow was not handled")
	}
	if !pm.comboOpen {
		t.Fatal("combo did not open")
	}
	return pm, newComboScreen(w, h)
}

// The drop-down used to draw one row per panel from the first content row
// down, so with more panels than rows it ran off the bottom of its own rect
// and off the screen. It must stop at the bottom of the panel manager.
func TestComboListStopsAtTheBottomOfThePanelManager(t *testing.T) {
	pm, s := openCombo(t, 30, 80, 12)
	pm.Draw(s)

	listX, listW, listH := pm.comboGeom()
	if listH != pm.contentH() {
		t.Fatalf("comboGeom height = %d, want %d (the rows below the tab bar)", listH, pm.contentH())
	}
	last := pm.rect.Y + pm.rect.H - 1
	if got := s.row(listX, last, listW); got == "" {
		t.Errorf("nothing drawn on the last usable row %d; the list is short by a row", last)
	}
	if got := s.row(listX, last+1, listW); got != "" {
		t.Errorf("row %d is below the panel manager but has %q painted on it", last+1, got)
	}
}

// Arrowing down past the last visible row must scroll the list, not walk the
// selection off the end of it. Before, active moved and the highlighted row
// simply wasn't on screen any more.
func TestComboArrowingPastTheVisibleWindowScrollsIt(t *testing.T) {
	pm, s := openCombo(t, 30, 80, 12)
	for range 29 {
		pm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	if pm.active != 29 {
		t.Fatalf("active = %d, want 29", pm.active)
	}
	pm.Draw(s)

	listX, listW, listH := pm.comboGeom()
	if pm.comboScroll != len(pm.panels)-listH {
		t.Fatalf("comboScroll = %d, want %d — the list did not follow the selection",
			pm.comboScroll, len(pm.panels)-listH)
	}
	want := pm.panels[29].Title()
	found := false
	for row := range listH {
		if strings.Contains(s.row(listX, pm.contentY()+row, listW), want) {
			found = true
		}
	}
	if !found {
		t.Errorf("active panel %q is not drawn anywhere in the open list", want)
	}
}

// A click selects the panel drawn under it, which past the first screenful is
// not the panel at the clicked row index.
func TestComboClickSelectsThePanelDrawnUnderIt(t *testing.T) {
	pm, s := openCombo(t, 30, 80, 12)
	for range 29 {
		pm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	pm.setActiveIndex(0)
	pm.Draw(s)

	listX, listW, listH := pm.comboGeom()
	// The list stays where it was scrolled to; only the highlight moved.
	y := pm.contentY() + listH - 1
	drawn := s.row(listX, y, listW)

	pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.ButtonNone, tcell.ModNone))
	if !pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("click inside the open list was not handled")
	}
	if pm.comboOpen {
		t.Error("list stayed open after selecting an entry")
	}
	if got := pm.ActivePanel().Title(); !strings.Contains(drawn, got) {
		t.Errorf("clicking row %d activated %q, but %q is what was drawn there", y, got, drawn)
	}
	if pm.active != len(pm.panels)-1 {
		t.Errorf("active = %d, want %d (the last row of a fully scrolled list)", pm.active, len(pm.panels)-1)
	}
}

// The wheel scrolls the open list and never runs off either end.
func TestComboWheelScrollsWithinBounds(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	listX, _, listH := pm.comboGeom()
	for range 100 {
		pm.HandleMouse(tcell.NewEventMouse(listX+1, pm.contentY(), tcell.WheelDown, tcell.ModNone))
	}
	if want := len(pm.panels) - listH; pm.comboScroll != want {
		t.Errorf("comboScroll after scrolling to the end = %d, want %d", pm.comboScroll, want)
	}
	for range 100 {
		pm.HandleMouse(tcell.NewEventMouse(listX+1, pm.contentY(), tcell.WheelUp, tcell.ModNone))
	}
	if pm.comboScroll != 0 {
		t.Errorf("comboScroll after scrolling back = %d, want 0", pm.comboScroll)
	}
}

// A short list is unchanged: no scrollbar column stolen from the labels, and
// every panel drawn.
func TestComboShortListDrawsEveryPanelAndNoScrollbar(t *testing.T) {
	pm, s := openCombo(t, 3, 80, 20)
	pm.Draw(s)
	listX, listW, listH := pm.comboGeom()
	if listH != 3 {
		t.Fatalf("comboGeom height = %d, want 3", listH)
	}
	for i, panel := range pm.panels {
		if got := s.row(listX, pm.contentY()+i, listW); !strings.Contains(got, panel.Title()) {
			t.Errorf("row %d = %q, want it to contain %q", i, got, panel.Title())
		}
	}
	if r := s.cells[[2]int{listX + listW - 1, pm.contentY()}]; r == '│' || r == '█' {
		t.Errorf("a scrollbar was drawn for a list that fits (%q)", r)
	}
}

// A press that selects a panel from the drop-down belongs to the drop-down
// until it is released. Selecting closes the list, so from the next resent
// Button1 on there is no branch left to match it — it used to fall straight
// through to the panel the click had just activated, landing a press at the
// list entry's coordinates in a query editor that had never seen the press
// begin (caret moved, or a selection drag started).
func TestComboClickDoesNotLeakTheHeldPressIntoThePanel(t *testing.T) {
	pm, _ := openCombo(t, 6, 80, 12)
	listX, _, _ := pm.comboGeom()
	y := pm.contentY() + 3

	pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.ButtonNone, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.Button1, tcell.ModNone))
	if pm.active != 3 {
		t.Fatalf("active = %d, want 3 — the click did not select the row it was on", pm.active)
	}
	selected := pm.ActivePanel().(*fakePanel)
	selected.mouse = nil

	// The same press, resent by all-motion tracking: once because the
	// cursor twitched, once more as it drifts a column.
	pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.Button1, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(listX+2, y, tcell.Button1, tcell.ModNone))
	for _, b := range selected.mouse {
		if b == tcell.Button1 {
			t.Fatalf("the newly activated panel received %v from the press that selected it; got %v",
				tcell.Button1, selected.mouse)
		}
	}

	// The release ends the gesture, and it is forwarded so any latch inside
	// the panel clears. The next press is a fresh one and must get through.
	pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.ButtonNone, tcell.ModNone))
	selected.mouse = nil
	pm.HandleMouse(tcell.NewEventMouse(listX+1, y, tcell.Button1, tcell.ModNone))
	if len(selected.mouse) == 0 || selected.mouse[0] != tcell.Button1 {
		t.Errorf("a fresh press after the release did not reach the panel; got %v", selected.mouse)
	}
}

// Global keys reach the application before the panel manager — Ctrl+N opens a
// panel and activates it, Ctrl+Tab cycles — so the active index can move while
// the list is on screen. The list has to follow it; it used to scroll only for
// its own Up/Down, leaving the highlighted row outside the visible window with
// nothing on screen indicating which panel was active.
func TestComboFollowsAnActivePanelChangedFromOutside(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	_, _, listH := pm.comboGeom()
	if pm.comboScroll != 0 {
		t.Fatalf("comboScroll = %d at open with panel 0 active, want 0", pm.comboScroll)
	}

	pm.SetActive(29)
	if pm.active < pm.comboScroll || pm.active >= pm.comboScroll+listH {
		t.Errorf("after SetActive(29) the active row is outside the window [%d,%d)",
			pm.comboScroll, pm.comboScroll+listH)
	}

	pm.Prev()
	if pm.active < pm.comboScroll || pm.active >= pm.comboScroll+listH {
		t.Errorf("after Prev() the active row is outside the window [%d,%d)",
			pm.comboScroll, pm.comboScroll+listH)
	}

	// Ctrl+N's shape: append, then activate what was appended.
	pm.SetActive(pm.AddPanel(&fakePanel{title: "Query New", closable: true}))
	if pm.active < pm.comboScroll || pm.active >= pm.comboScroll+listH {
		t.Errorf("after adding and activating a panel the active row is outside the window [%d,%d)",
			pm.comboScroll, pm.comboScroll+listH)
	}

	pm.SetActive(0)
	if pm.comboScroll != 0 {
		t.Errorf("comboScroll = %d after activating the first panel, want 0", pm.comboScroll)
	}

	// Removing panels shrinks the list under a scrolled offset; nothing may
	// be left pointing past the end of it.
	pm.SetActive(len(pm.panels) - 1)
	for range 25 {
		pm.RemovePanel(len(pm.panels) - 1)
	}
	if _, _, h := pm.comboGeom(); pm.comboScroll > pm.comboScrollMax(h) {
		t.Errorf("comboScroll = %d after removing panels, past the maximum %d",
			pm.comboScroll, pm.comboScrollMax(h))
	}
}

// The scroll offset is maintained only while the list is showing: a closed
// combo has nothing to keep in view, and opening it recomputes the offset from
// the active panel anyway.
func TestComboScrollUntouchedWhileClosed(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	pm.comboOpen = false
	pm.comboScroll = 7
	pm.SetActive(29)
	if pm.comboScroll != 7 {
		t.Errorf("comboScroll = %d after SetActive with the list closed, want it left at 7", pm.comboScroll)
	}
}

// The drop-down's scrollbar is draggable, like every other scrollbar in the
// app. It was drawn but inert when the capped list went in, which made it the
// only DrawScrollbar site with no HandleScrollbarDrag behind it.
func TestComboScrollbarDragScrollsTheList(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	listX, listW, listH := pm.comboGeom()
	barX := listX + listW - 1

	pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY(), tcell.ButtonNone, tcell.ModNone))
	if !pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY()+3, tcell.Button1, tcell.ModNone)) {
		t.Fatal("a press on the scrollbar was not handled")
	}
	want := core.ScrollOffsetForDrag(3, listH, len(pm.panels), listH)
	if pm.comboScroll != want {
		t.Errorf("comboScroll after pressing row 3 of the bar = %d, want %d", pm.comboScroll, want)
	}
	if !pm.comboOpen {
		t.Error("the drop-down closed on a scrollbar press")
	}

	// The latch owns the mouse for the rest of the gesture, including past
	// the panel manager's own bounds — which is why the drag runs ahead of
	// the bounds check.
	pm.HandleMouse(tcell.NewEventMouse(200, pm.contentY()+8, tcell.Button1, tcell.ModNone))
	if want := core.ScrollOffsetForDrag(8, listH, len(pm.panels), listH); pm.comboScroll != want {
		t.Errorf("comboScroll after dragging off the bar = %d, want %d", pm.comboScroll, want)
	}

	// The release ends it: the next press somewhere else is not a drag.
	pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY()+8, tcell.ButtonNone, tcell.ModNone))
	if pm.comboSbDragging {
		t.Error("comboSbDragging survived the release")
	}
}

// The bar sits in the list's last column, so the drag has to be tested before
// the row hit-test — otherwise grabbing the bar picks the panel behind it and
// dismisses the list in the same click.
func TestComboScrollbarPressDoesNotSelectTheRowBehindIt(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	listX, listW, _ := pm.comboGeom()

	pm.HandleMouse(tcell.NewEventMouse(listX+listW-1, pm.contentY(), tcell.ButtonNone, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(listX+listW-1, pm.contentY()+3, tcell.Button1, tcell.ModNone))

	if pm.active != 0 {
		t.Errorf("active = %d after a scrollbar press, want 0 — it selected the row behind the bar", pm.active)
	}
	if !pm.comboOpen {
		t.Error("the drop-down closed on a scrollbar press")
	}
}

// An open drop-down is an overlay and gets first refusal of the wheel, the
// same way HandleKey consumes every key while it is open. Without it a wheel
// aimed anywhere but the list fell through to the active panel, scrolling the
// query editor underneath a list still floating over it.
func TestComboWheelOutsideTheListScrollsItAndNotThePanel(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	panel := pm.ActivePanel().(*fakePanel)
	panel.mouse = nil

	if !pm.HandleMouse(tcell.NewEventMouse(5, pm.contentY()+4, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("a wheel event outside the list was not handled while the drop-down was open")
	}
	if pm.comboScroll != 1 {
		t.Errorf("comboScroll = %d after one wheel outside the list, want 1", pm.comboScroll)
	}
	if len(panel.mouse) != 0 {
		t.Errorf("the panel underneath received %v from a wheel over an open drop-down", panel.mouse)
	}

	// Closed again, the same wheel is the panel's.
	pm.comboOpen = false
	pm.HandleMouse(tcell.NewEventMouse(5, pm.contentY()+4, tcell.WheelDown, tcell.ModNone))
	if len(panel.mouse) == 0 {
		t.Error("the panel did not receive the wheel once the drop-down was closed")
	}
}

// Now that the list is capped and scrolls, walking it one row at a time is the
// only way to reach the far end without these.
func TestComboPageHomeAndEndMoveTheSelection(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	_, _, listH := pm.comboGeom()

	key := func(k tcell.Key) {
		if !pm.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone)) {
			t.Helper()
			t.Fatalf("key %v was not handled while the drop-down was open", k)
		}
	}

	key(tcell.KeyPgDn)
	if pm.active != listH {
		t.Errorf("active after PgDn = %d, want %d", pm.active, listH)
	}
	if pm.active < pm.comboScroll || pm.active >= pm.comboScroll+listH {
		t.Errorf("PgDn left the selection outside the visible window: active %d, scroll %d, height %d",
			pm.active, pm.comboScroll, listH)
	}
	key(tcell.KeyPgUp)
	if pm.active != 0 {
		t.Errorf("active after PgUp back = %d, want 0", pm.active)
	}

	key(tcell.KeyEnd)
	if want := len(pm.panels) - 1; pm.active != want {
		t.Errorf("active after End = %d, want %d", pm.active, want)
	}
	if want := len(pm.panels) - listH; pm.comboScroll != want {
		t.Errorf("comboScroll after End = %d, want %d", pm.comboScroll, want)
	}
	key(tcell.KeyHome)
	if pm.active != 0 || pm.comboScroll != 0 {
		t.Errorf("after Home active = %d, comboScroll = %d, want 0 and 0", pm.active, pm.comboScroll)
	}
	// Clamped, not wrapped: the ends are the ends.
	key(tcell.KeyPgUp)
	if pm.active != 0 {
		t.Errorf("PgUp at the top moved to %d, want it to stay at 0", pm.active)
	}
}

// A close while the scrollbar is still held must not carry the drag latch
// into the next showing of the list. Escape mid-drag left comboSbDragging
// set, and HandleScrollbarDrag's "already dragging" arm then took the first
// Button1 of the *next* opening — anywhere on screen, not just on the bar —
// and jumped the scroll to whatever row the pointer was over.
func TestComboScrollbarLatchDoesNotSurviveTheListClosing(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	listX, listW, _ := pm.comboGeom()
	barX := listX + listW - 1

	// Grab the bar and keep the button down.
	pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY(), tcell.ButtonNone, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY()+3, tcell.Button1, tcell.ModNone))
	if !pm.comboSbDragging {
		t.Fatal("the press on the scrollbar did not latch")
	}

	// Escape closes the list with the button still held — no ButtonNone.
	pm.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if pm.comboOpen {
		t.Fatal("Escape did not close the drop-down")
	}
	if pm.comboSbDragging {
		t.Fatal("comboSbDragging survived the close — the next opening will " +
			"treat any Button1 as a continued scrollbar drag")
	}

	// Reopen and press well away from the bar, on a list row. It must select
	// that row, not scroll to it.
	pm.HandleMouse(tcell.NewEventMouse(78, 0, tcell.ButtonNone, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(78, 0, tcell.Button1, tcell.ModNone))
	if !pm.comboOpen {
		t.Fatal("the drop-down did not reopen")
	}
	before := pm.comboScroll
	pm.HandleMouse(tcell.NewEventMouse(listX+2, pm.contentY()+4, tcell.ButtonNone, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(listX+2, pm.contentY()+4, tcell.Button1, tcell.ModNone))
	if pm.comboScroll != before {
		t.Errorf("comboScroll = %d, want %d — a plain row click scrolled the list",
			pm.comboScroll, before)
	}
	if pm.active != before+4 {
		t.Errorf("active = %d, want %d — the click did not select the row under it",
			pm.active, before+4)
	}
}

// Enter is the other close that can happen with the bar still held. Between
// them, Escape and Enter are the *only* ways to strand the latch: while it is
// set, the scrollbar branch runs ahead of everything else and owns every
// Button1, so no mouse-driven close can be reached mid-drag, and the release
// that ends a real drag clears it anyway. Both go through setComboOpen, which
// is what makes the clearing impossible to forget at one of the seven sites.
func TestComboEnterMidDragDoesNotStrandTheScrollbarLatch(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	listX, listW, _ := pm.comboGeom()
	barX := listX + listW - 1

	pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY(), tcell.ButtonNone, tcell.ModNone))
	pm.HandleMouse(tcell.NewEventMouse(barX, pm.contentY()+3, tcell.Button1, tcell.ModNone))
	if !pm.comboSbDragging {
		t.Fatal("the press on the scrollbar did not latch")
	}

	pm.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if pm.comboOpen {
		t.Fatal("Enter did not close the drop-down")
	}
	if pm.comboSbDragging {
		t.Error("comboSbDragging survived the close")
	}
}

// Opening the list is a setComboOpen too, and it must not clear the gesture
// latch on its way through: mouseDragging belongs to the press that claimed
// the gesture, not to the list. Clearing it here would release a still-held
// press to the panel underneath — the thing HandleMouse's catch-all exists to
// stop.
func TestComboOpeningKeepsTheGestureLatch(t *testing.T) {
	pm, _ := openCombo(t, 30, 80, 12)
	if !pm.mouseDragging {
		t.Fatal("the click that opened the combo did not latch the gesture")
	}
}
