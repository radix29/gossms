package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

func key(k tcell.Key, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(k, "", mod)
}

// fakeScreen is a minimal tcell.Screen fake — Size() and SetContent() are
// all Form.Draw and the rows it draws actually call. Written cells are
// recorded so a test can assert what actually reached the screen.
type fakeScreen struct {
	tcell.Screen
	w, h  int
	cells map[[2]int]rune
}

func (s *fakeScreen) Size() (int, int) { return s.w, s.h }
func (s *fakeScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
	if s.cells == nil {
		s.cells = map[[2]int]rune{}
	}
	s.cells[[2]int{x, y}] = primary
}

// count returns how many cells in column x hold r.
func (s *fakeScreen) count(x int, r rune) int {
	n := 0
	for pos, got := range s.cells {
		if pos[0] == x && got == r {
			n++
		}
	}
	return n
}

func TestFormFocusNextSkipsNonFocusableAndDisabled(t *testing.T) {
	disabled := Text("Disabled", "x", 10)
	disabled.SetEnabled(false)
	f := NewForm(
		Section("Heading"),
		Static("Name", "value"),
		disabled,
		Check("Enable", false),
	)
	f.SetBounds(0, 0, 60, 20)
	f.Focus(true) // focuses the first focusable row

	if _, ok := f.Focused().(*StaticRow); !ok {
		t.Fatalf("first focus = %T, want *StaticRow (Section must be skipped)", f.Focused())
	}
	if !f.FocusNext() {
		t.Fatal("FocusNext() from Static = false, want true (Check should be reachable, skipping disabled Text)")
	}
	if _, ok := f.Focused().(*CheckRow); !ok {
		t.Fatalf("focus after skip = %T, want *CheckRow (disabled Text must be skipped)", f.Focused())
	}
	if f.FocusNext() {
		t.Fatal("FocusNext() from the last row = true, want false")
	}
}

// TestFormScrollbarDragScrolls confirms dragging the form's own scrollbar
// scrolls it instead of being read as a click that shifts focus to whatever
// row sits under it.
func TestFormScrollbarDragScrolls(t *testing.T) {
	rows := make([]Row, 30)
	for i := range rows {
		rows[i] = Static("Label", "value")
	}
	f := NewForm(rows...)
	f.SetBounds(0, 0, 40, 10)

	w := f.contentWidth()
	if f.totalHeight(w) <= f.rect.H {
		t.Fatalf("test needs totalHeight > rect.H (%d) to exercise scrolling", f.rect.H)
	}

	sbX := f.rect.Right() - 1
	if !f.HandleMouse(tcell.NewEventMouse(sbX, f.rect.Y+f.rect.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the scrollbar column should be handled")
	}
	if !f.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if f.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if f.focus != -1 {
		t.Errorf("focus = %d, clicking the scrollbar must not shift row focus", f.focus)
	}

	f.HandleMouse(tcell.NewEventMouse(sbX, f.rect.Y, tcell.ButtonNone, tcell.ModNone))
	if f.sbDragging {
		t.Error("sbDragging should reset on release")
	}
}

// TestFormScrollbarWorksWithFewButTallRows pins down that the scrollbar
// measures the form in content lines, not row indices: the Permissions
// page shape — two Sections and two grids, 4 rows in a 20-line form that
// needs 24 lines — is taller than the form yet has fewer rows than the
// form has lines, so row-index math drew the bar with no thumb at all and
// refused every drag on it.
func TestFormScrollbarWorksWithFewButTallRows(t *testing.T) {
	f := NewForm(
		Section("Principals"),
		NewGridRow(controls.NewDataGrid(), 10),
		Section("Permissions"),
		NewGridRow(controls.NewDataGrid(), 10),
	)
	f.SetBounds(0, 0, 40, 20)

	w := f.contentWidth()
	if f.totalHeight(w) <= f.rect.H {
		t.Fatalf("test needs totalHeight (%d) > rect.H (%d)", f.totalHeight(w), f.rect.H)
	}
	if len(f.rows) >= f.rect.H {
		t.Fatalf("test needs fewer rows (%d) than the form has lines (%d)", len(f.rows), f.rect.H)
	}

	sbX := f.rect.Right() - 1
	if !f.HandleMouse(tcell.NewEventMouse(sbX, f.rect.Y+f.rect.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("press on the scrollbar column = false, want it handled as a drag")
	}
	if f.scroll == 0 {
		t.Error("scroll = 0 after dragging the thumb to the bottom of the track")
	}
	if f.scroll > f.maxScroll(w) {
		t.Errorf("scroll = %d, past maxScroll %d", f.scroll, f.maxScroll(w))
	}
	f.HandleMouse(tcell.NewEventMouse(sbX, f.rect.Y, tcell.ButtonNone, tcell.ModNone))

	// A thumb has to be drawn too, not just an empty trough — same
	// total/visible pair, same bug.
	f.scroll = 0
	screen := &fakeScreen{w: 80, h: 40}
	f.Draw(screen)
	if screen.count(sbX, '█') == 0 {
		t.Error("scrollbar column has no thumb cells, only an empty trough")
	}
}

// TestFormScrollbarDragOutranksFocusedRow pins down that once a drag on
// the form's scrollbar is armed, the focused row can't take the follow-up
// motion events off it: a focused GridRow consumes any Button1 inside its
// own rect, which stalled the thumb the moment the pointer drifted back
// over the grid mid-drag.
func TestFormScrollbarDragOutranksFocusedRow(t *testing.T) {
	grid := controls.NewDataGrid()
	grid.SetData([]string{"Name"}, [][]string{{"a"}, {"b"}, {"c"}})
	f := NewForm(Section("One"), NewGridRow(grid, 10), Section("Two"), NewGridRow(controls.NewDataGrid(), 10))
	f.SetBounds(0, 0, 40, 18)
	f.Focus(true) // focuses the first GridRow

	sbX := f.rect.Right() - 1
	f.HandleMouse(tcell.NewEventMouse(sbX, f.rect.Y+f.rect.H-1, tcell.Button1, tcell.ModNone))
	if f.scroll == 0 {
		t.Fatal("press near the bottom of the track did not scroll")
	}

	// Still held, pointer now over the focused grid rather than the bar.
	if !f.HandleMouse(tcell.NewEventMouse(f.rect.X+5, f.rect.Y+1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("mid-drag motion over the grid = false, want the armed drag to claim it")
	}
	if f.scroll != 0 {
		t.Errorf("scroll = %d after dragging the thumb back to the top, want 0 — the focused grid ate the event", f.scroll)
	}
}

func TestFormFocusPrev(t *testing.T) {
	f := NewForm(Static("A", "1"), Static("B", "2"), Static("C", "3"))
	f.SetBounds(0, 0, 60, 20)
	f.FocusLast()
	if got := f.Focused().(*StaticRow); got.Value() != "3" {
		t.Fatalf("FocusLast() landed on %q, want the C row", got.Value())
	}
	f.FocusPrev()
	if got := f.Focused().(*StaticRow); got.Value() != "2" {
		t.Fatalf("FocusPrev() landed on %q, want the B row", got.Value())
	}
	f.FocusPrev()
	f.FocusPrev() // walking off the top
	if f.FocusPrev() {
		t.Fatal("FocusPrev() from the first row = true, want false")
	}
}

// TestFormHandleKeyMovesFocusWhenFocusedRowDoesNotWantTheKey pins down that
// a focused TextRow (wrapping widgets.InputField) doesn't swallow
// Up/Down/Tab as no-ops: Form.HandleKey needs InputField's false return to
// fall through to FocusNext/FocusPrev, or a focused text field becomes a
// keyboard trap only the mouse escapes.
func TestFormHandleKeyMovesFocusWhenFocusedRowDoesNotWantTheKey(t *testing.T) {
	f := NewForm(Text("A", "one", 10), Text("B", "two", 10))
	f.SetBounds(0, 0, 60, 20)
	f.Focus(true)
	if _, ok := f.Focused().(*TextRow); !ok {
		t.Fatalf("initial focus = %T, want *TextRow", f.Focused())
	}

	if !f.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Fatal("HandleKey(Down) = false, want true (should move focus)")
	}
	second := f.Focused().(*TextRow)
	if second.Value() != "two" {
		t.Fatalf("focus after Down = %q, want the B row", second.Value())
	}

	if !f.HandleKey(key(tcell.KeyUp, tcell.ModNone)) {
		t.Fatal("HandleKey(Up) = false, want true (should move focus back)")
	}
	first := f.Focused().(*TextRow)
	if first.Value() != "one" {
		t.Fatalf("focus after Up = %q, want the A row", first.Value())
	}
}

// TestFormRowFitsAllowsAShrinkableRowTallerThanTheWholeForm pins down that
// a GridRow taller than the form's entire available height still counts as
// "fits": a require-the-whole-row check can never be satisfied for such a
// row, so Draw's identical check would skip it forever, rendering pages
// with a big grid (Permissions, Advanced, Files, ...) as a blank Section
// header on any realistically-sized terminal. It draws clamped instead.
func TestFormRowFitsAllowsAShrinkableRowTallerThanTheWholeForm(t *testing.T) {
	tallRow := NewGridRow(controls.NewDataGrid(), 100)
	f := NewForm(Static("A", "1"), tallRow)
	f.SetBounds(0, 0, 60, 10) // the form is only 10 lines tall

	f.FocusFirst() // focuses Static("A","1")
	if !f.rowFits(1, f.contentWidth()) {
		t.Fatal("rowFits(1, ...) = false for a 100-line grid row in a 10-line form, want true (it draws clamped)")
	}
	h, ok := f.drawHeight(1, f.contentWidth(), 9, false)
	if !ok || h != 9 {
		t.Fatalf("drawHeight for a 100-line grid row with 9 lines left = (%d, %v), want (9, true)", h, ok)
	}
}

// TestFormDrawKeepsEveryRowInsideItsBounds is the bug this clamping exists
// for: a grid row drawn at its full declared height spilled past the form's
// bottom edge, over the sheet's hint line and button row (reproducible on
// Server Properties > Advanced).
func TestFormDrawKeepsEveryRowInsideItsBounds(t *testing.T) {
	grid := controls.NewDataGrid()
	grid.SetData([]string{"Name", "Value"}, [][]string{{"a", "1"}, {"b", "2"}})
	f := NewForm(Section("Heading"), NewGridRow(grid, 20))
	f.SetBounds(0, 0, 60, 10)

	f.Draw(&fakeScreen{w: 80, h: 40})

	for _, b := range f.bands {
		if b.y+b.h > 10 {
			t.Fatalf("row %d occupies lines %d..%d, past the form's 10-line bottom edge", b.row, b.y, b.y+b.h-1)
		}
	}
	if len(f.bands) != 2 {
		t.Fatalf("drew %d rows, want 2 (heading + clamped grid)", len(f.bands))
	}
}

func TestFormEnsureVisibleScrollsToKeepFocusInView(t *testing.T) {
	rows := make([]Row, 30)
	for i := range rows {
		rows[i] = Static("Row", "v")
	}
	f := NewForm(rows...)
	f.SetBounds(0, 0, 60, 10) // only 10 lines visible, 30 one-line rows

	f.FocusLast()
	if f.scroll == 0 {
		t.Fatal("scroll is still 0 after focusing the last of 30 rows in a 10-line form")
	}
	if !f.rowFits(f.focus, f.contentWidth()) {
		t.Fatal("focused row is not fully visible after ensureVisible")
	}
}

func TestFormDirtyRevert(t *testing.T) {
	text := Text("Name", "orig", 10)
	check := Check("Enable", false)
	f := NewForm(text, check)
	if f.Dirty() {
		t.Fatal("freshly built form reports Dirty() = true")
	}
	text.SetValue("orig") // baseline set via constructor already, this is a no-op sanity check
	text.field.SetValue("changed")
	if !f.Dirty() {
		t.Fatal("Dirty() = false after changing a TextRow's value")
	}
	f.Revert()
	if f.Dirty() {
		t.Fatal("Dirty() = true after Revert()")
	}
	if got := text.Value(); got != "orig" {
		t.Fatalf("Value() after Revert() = %q, want %q", got, "orig")
	}
}

func TestIntRowValidatesRange(t *testing.T) {
	row := Int("Max", 10, 0, 100, "MB")
	f := NewForm(row)
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate() on an unchanged Int row = %v, want nil (not dirty)", err)
	}
	row.field.SetValue("500")
	if err := f.Validate(); err == nil {
		t.Fatal("Validate() with 500 (max 100) = nil, want a range error")
	}
	row.field.SetValue("50")
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate() with 50 (in range) = %v, want nil", err)
	}
	row.field.SetValue("not-a-number")
	if err := f.Validate(); err == nil {
		t.Fatal("Validate() with a non-numeric value = nil, want an error")
	}
}

func TestFormCopyTextPerRowKind(t *testing.T) {
	static := Static("Name", "srv01")
	text := Text("Owner", "sa", 10)
	check := Check("Flag", true)
	radio := Radio("Mode", []string{"A", "B"}, 1)

	for _, tc := range []struct {
		name string
		row  Row
		want string
	}{
		{"static", static, "srv01"},
		{"text", text, "sa"},
		{"check", check, "true"},
		{"radio", radio, "B"},
	} {
		f := NewForm(tc.row)
		f.SetBounds(0, 0, 60, 10)
		f.Focus(true)
		got, ok := f.CopyText()
		if !ok {
			t.Errorf("%s: CopyText() ok = false, want true", tc.name)
		}
		if got != tc.want {
			t.Errorf("%s: CopyText() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFormTabMovesFocusAndReportsWalkOff(t *testing.T) {
	f := NewForm(Static("A", "1"), Static("B", "2"))
	f.SetBounds(0, 0, 60, 10)
	f.Focus(true)
	if !f.HandleKey(key(tcell.KeyTab, tcell.ModNone)) {
		t.Fatal("Tab from first row = false, want true (moved to second row)")
	}
	// Tab off the last row: Form itself can't move further, so HandleKey
	// must return false — the signal PropertySheet uses to shift zones.
	if f.HandleKey(key(tcell.KeyTab, tcell.ModNone)) {
		t.Fatal("Tab off the last row = true, want false")
	}
}

func TestGridRowDirtyDelegatesToHooks(t *testing.T) {
	dirty := false
	reverted := false
	gr := &GridRow{
		DirtyFn:  func() bool { return dirty },
		RevertFn: func() { reverted = true },
	}
	if gr.Dirty() {
		t.Fatal("Dirty() = true before DirtyFn returns true")
	}
	dirty = true
	if !gr.Dirty() {
		t.Fatal("Dirty() = false after DirtyFn returns true")
	}
	gr.Revert()
	if !reverted {
		t.Fatal("Revert() did not call RevertFn")
	}
}
