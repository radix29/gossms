package widgets

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

func newTestDropDown(items []string) *DropDown {
	d := NewDropDown("", items, 20)
	d.Focus(true)
	return d
}

// TestDropDownClosedArrowsNotConsumed pins down that Up/Down/Escape on a
// closed dropdown fall through (return false) rather than being swallowed
// as a no-op, so a caller like propsheet.Form can move focus to the
// next/previous row.
func TestDropDownClosedArrowsNotConsumed(t *testing.T) {
	d := newTestDropDown([]string{"a", "b", "c"})
	for _, k := range []tcell.Key{tcell.KeyUp, tcell.KeyDown, tcell.KeyEscape} {
		if d.HandleKey(key(k, tcell.ModNone)) {
			t.Fatalf("closed HandleKey(%v) = true, want false", k)
		}
	}
}

// TestDropDownOpenArrowsConsumed confirms open-list navigation (the
// behavior the closed-state fix must not disturb) still works.
func TestDropDownOpenArrowsConsumed(t *testing.T) {
	d := newTestDropDown([]string{"a", "b", "c"})
	d.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) // open
	if !d.IsOpen() {
		t.Fatal("setup: expected dropdown to be open")
	}
	if !d.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Fatal("open HandleKey(Down) = false, want true")
	}
	if d.Selected() != 1 {
		t.Fatalf("Selected() = %d, want 1", d.Selected())
	}
	if !d.HandleKey(key(tcell.KeyEscape, tcell.ModNone)) {
		t.Fatal("open HandleKey(Escape) = false, want true")
	}
	if d.IsOpen() {
		t.Fatal("Escape should have closed the dropdown")
	}
}

// TestDropDownOpenBoundaryArrowsConsumed: at the top/bottom of an open
// list, Up/Down are still consumed (as a no-op) rather than falling
// through — only the closed state changed.
func TestDropDownOpenBoundaryArrowsConsumed(t *testing.T) {
	d := newTestDropDown([]string{"a", "b"})
	d.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) // open, selected = 0
	if !d.HandleKey(key(tcell.KeyUp, tcell.ModNone)) {
		t.Fatal("open HandleKey(Up) at top = false, want true (still consumed)")
	}
	if d.Selected() != 0 {
		t.Fatalf("Selected() = %d, want 0 (unchanged)", d.Selected())
	}
}

// dropTestScreen is a w×h mock terminal, for the tests that need DropDown to
// learn the screen size from Draw.
func dropTestScreen(t *testing.T, w, h int) tcell.Screen {
	t.Helper()
	s, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: vt.Col(w), Y: vt.Row(h)}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	return s
}

func manyItems(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("login%03d", i)
	}
	return items
}

// rowText reads screen row y from x for n cells.
func rowText(s tcell.Screen, x, y, n int) string {
	var b strings.Builder
	for i := range n {
		str, _, _ := s.Get(x+i, y)
		b.WriteString(str)
	}
	return strings.TrimRight(b.String(), " ")
}

// openOn draws d, opens it with Enter and draws the overlay, so the geometry
// HandleMouse uses is the one painted.
func openOn(t *testing.T, s tcell.Screen, d *DropDown) {
	t.Helper()
	d.Draw(s)
	d.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
	if !d.IsOpen() {
		t.Fatal("setup: Enter did not open the list")
	}
	d.Draw(s)
	d.DrawOverlay(s)
}

// TestDropDownListIsClampedToTheScreen is K2: with 200 items the list ran off
// the bottom of the screen and took the selection with it. Below the control
// the list stops at the last screen row, and the selection stays visible as
// Down walks past it.
func TestDropDownListIsClampedToTheScreen(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown(manyItems(200))
	d.SetBounds(0, 2) // 9 rows below, 2 above: opens below
	openOn(t, s, d)
	if y, rows := d.listGeometry(); y != 3 || rows != 9 {
		t.Fatalf("listGeometry() = (%d, %d), want (3, 9)", y, rows)
	}
	for range 15 {
		d.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	}
	d.Draw(s)
	d.DrawOverlay(s)
	// Selection 15 sits on the list's last row, scrolled from item 7.
	if got := rowText(s, 1, 11, 8); got != "login015" {
		t.Fatalf("last list row = %q, want login015 (selection kept in view)", got)
	}
	if got := rowText(s, 1, 3, 8); got != "login007" {
		t.Fatalf("first list row = %q, want login007", got)
	}
	// Item text stops before the scrollbar column.
	if str, _, _ := s.Get(20, 3); str != "│" && str != "█" {
		t.Fatalf("scrollbar column = %q, want a track or thumb cell", str)
	}
}

// TestDropDownOpensAboveWhenThereIsMoreRoom: a control near the bottom of the
// screen opens its list upward, and a click hits the item drawn there.
func TestDropDownOpensAboveWhenThereIsMoreRoom(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown(manyItems(30))
	d.SetBounds(0, 9) // 2 rows below, 9 above
	openOn(t, s, d)
	y, rows := d.listGeometry()
	if y != 0 || rows != 9 {
		t.Fatalf("listGeometry() = (%d, %d), want (0, 9)", y, rows)
	}
	if got := rowText(s, 1, 4, 8); got != "login004" {
		t.Fatalf("list row 4 = %q, want login004", got)
	}
	d.HandleMouse(mouse(3, 4, tcell.Button1))
	d.HandleMouse(mouse(3, 4, tcell.ButtonNone))
	if d.IsOpen() || d.Selected() != 4 {
		t.Fatalf("after click: open=%v selected=%d, want closed on 4", d.IsOpen(), d.Selected())
	}
}

// TestDropDownShortListStillOpensBelow: a list that fits below keeps its old
// place and has no scrollbar.
func TestDropDownShortListStillOpensBelow(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown([]string{"a", "b", "c"})
	d.SetBounds(0, 5)
	openOn(t, s, d)
	if y, rows := d.listGeometry(); y != 6 || rows != 3 {
		t.Fatalf("listGeometry() = (%d, %d), want (6, 3)", y, rows)
	}
	if d.scrolled() {
		t.Fatal("scrolled() = true for a list that fits")
	}
}

// TestDropDownOpenListPagingKeys: PgUp/PgDn move a page, Home/End the ends,
// and a letter jumps to the next item starting with it, wrapping round.
func TestDropDownOpenListPagingKeys(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown(append(manyItems(50), "master", "model", "msdb"))
	d.SetBounds(0, 2) // 9 rows: a page is 8
	openOn(t, s, d)
	steps := []struct {
		ev   *tcell.EventKey
		want int
	}{
		{key(tcell.KeyPgDn, tcell.ModNone), 8},
		{key(tcell.KeyPgDn, tcell.ModNone), 16},
		{key(tcell.KeyPgUp, tcell.ModNone), 8},
		{key(tcell.KeyEnd, tcell.ModNone), 52},
		{key(tcell.KeyHome, tcell.ModNone), 0},
		{tcell.NewEventKey(tcell.KeyRune, "M", tcell.ModNone), 50},
		{tcell.NewEventKey(tcell.KeyRune, "m", tcell.ModNone), 51},
		{tcell.NewEventKey(tcell.KeyRune, "m", tcell.ModNone), 52},
		{tcell.NewEventKey(tcell.KeyRune, "m", tcell.ModNone), 50}, // wraps
		{tcell.NewEventKey(tcell.KeyRune, "z", tcell.ModNone), 50}, // no match: stays
	}
	for i, st := range steps {
		if !d.HandleKey(st.ev) {
			t.Fatalf("step %d: HandleKey = false, want true while open", i)
		}
		if d.Selected() != st.want {
			t.Fatalf("step %d: Selected() = %d, want %d", i, d.Selected(), st.want)
		}
		if top := d.firstShown(); d.Selected() < top || d.Selected() >= top+9 {
			t.Fatalf("step %d: selection %d out of view (top %d)", i, d.Selected(), top)
		}
	}
	// Closed, none of them is taken: a form moves focus on PgDn, and a letter
	// is no edit.
	d.HandleKey(key(tcell.KeyEscape, tcell.ModNone))
	for _, ev := range []*tcell.EventKey{key(tcell.KeyPgDn, tcell.ModNone), key(tcell.KeyHome, tcell.ModNone), tcell.NewEventKey(tcell.KeyRune, "m", tcell.ModNone)} {
		if d.HandleKey(ev) {
			t.Fatalf("closed HandleKey(%v) = true, want false", ev.Name())
		}
	}
}

// TestDropDownWheelScrollsWithoutMovingTheSelection, and a click then picks
// the item drawn under the pointer after the scroll.
func TestDropDownWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown(manyItems(200))
	d.SetBounds(0, 2)
	openOn(t, s, d)
	if !d.HandleMouse(mouse(3, 5, tcell.WheelDown)) {
		t.Fatal("wheel over the open list = false, want true")
	}
	if d.Selected() != 0 || d.firstShown() != 3 {
		t.Fatalf("after wheel: selected=%d top=%d, want 0 and 3", d.Selected(), d.firstShown())
	}
	if d.HandleMouse(mouse(30, 5, tcell.WheelDown)) {
		t.Fatal("wheel beside the list = true, want false (not ours)")
	}
	d.HandleMouse(mouse(3, 3, tcell.Button1))
	if d.Selected() != 3 {
		t.Fatalf("click on the first shown row picked %d, want 3", d.Selected())
	}
}

// TestDropDownScrollbarDragScrollsAndPicksNothing: a press on the scrollbar
// column scrolls, and dragging back across items under the held button picks
// none of them.
func TestDropDownScrollbarDragScrollsAndPicksNothing(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown(manyItems(200))
	d.SetBounds(0, 2)
	openOn(t, s, d)
	bar := d.inputX() + d.Width()
	d.HandleMouse(mouse(bar, 11, tcell.Button1)) // the track's last row
	if d.firstShown() != 200-9 {
		t.Fatalf("after press at the track's end: top = %d, want %d", d.firstShown(), 200-9)
	}
	d.HandleMouse(mouse(5, 3, tcell.Button1)) // drift onto an item, held
	if !d.IsOpen() || d.Selected() != 0 {
		t.Fatalf("drag off the bar: open=%v selected=%d, want still open on 0", d.IsOpen(), d.Selected())
	}
	d.HandleMouse(mouse(5, 3, tcell.ButtonNone))
	d.HandleMouse(mouse(5, 3, tcell.Button1))
	if d.IsOpen() || d.Selected() != d.firstShown() {
		t.Fatalf("fresh click: open=%v selected=%d, want closed on the first shown item", d.IsOpen(), d.Selected())
	}
}

// TestDisabledDropDownRefusesInputAndDrawsGreyed is K10, with InputField's
// contract: greyed, no key or click opens or changes it, disabling closes an
// open list, and SetSelected still works.
func TestDisabledDropDownRefusesInputAndDrawsGreyed(t *testing.T) {
	s := dropTestScreen(t, 40, 12)
	d := newTestDropDown([]string{"alpha", "beta", "gamma"})
	d.SetBounds(0, 2)
	openOn(t, s, d)
	d.SetEnabled(false)
	if d.IsOpen() {
		t.Fatal("disabling left the list open")
	}
	if d.Enabled() {
		t.Fatal("Enabled() = true after SetEnabled(false)")
	}
	for _, k := range []tcell.Key{tcell.KeyEnter, tcell.KeyF4, tcell.KeyDown} {
		if d.HandleKey(key(k, tcell.ModNone)) || d.IsOpen() || d.Selected() != 0 {
			t.Fatalf("disabled dropdown acted on key %v", k)
		}
	}
	if d.HandleMouse(mouse(2, 2, tcell.Button1)) || d.IsOpen() {
		t.Fatal("disabled dropdown opened on a click")
	}
	d.HandleMouse(mouse(2, 2, tcell.ButtonNone))
	d.SetSelected(2)
	if d.Value() != "gamma" {
		t.Fatalf("SetSelected on a disabled dropdown: Value() = %q, want gamma", d.Value())
	}
	d.Draw(s)
	if _, st, _ := s.Get(1, 2); st != theme.StyleInputDisabled() {
		t.Fatal("disabled value is not drawn in the disabled style")
	}

	d.SetEnabled(true)
	if !d.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) || !d.IsOpen() {
		t.Fatal("re-enabled dropdown does not open on Enter")
	}
}
