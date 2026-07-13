package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func key(k tcell.Key, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(k, "", mod)
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

// TestFormHandleKeyMovesFocusWhenFocusedRowDoesNotWantTheKey is a
// regression test: a focused TextRow (wrapping widgets.InputField) must
// not swallow Up/Down/Tab as no-ops — Form.HandleKey needs InputField's
// false return to fall through to FocusNext/FocusPrev, otherwise a
// focused text field becomes a keyboard trap that only the mouse escapes.
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

// TestFormRowFitsAllowsARowTallerThanTheWholeForm is a regression test: a
// GridRow (or Note) taller than the form's entire available height must
// still be considered "fits" once its start is in view — the old
// require-the-whole-row check could never be satisfied for such a row,
// which made Draw's identical check skip it forever, rendering pages with
// a big grid (Permissions, Advanced, Files, ...) as a blank Section
// header on any realistically-sized terminal.
func TestFormRowFitsAllowsARowTallerThanTheWholeForm(t *testing.T) {
	tallRow := &fakeTallRow{height: 100}
	f := NewForm(Static("A", "1"), tallRow)
	f.SetBounds(0, 0, 60, 10) // the form is only 10 lines tall

	f.FocusLast() // moves focus to Static("A","1"), tallRow isn't focusable
	if !f.rowFits(1, f.contentWidth()) {
		t.Fatal("rowFits(1, ...) = false for a 100-line row in a 10-line form, want true (only its start must be visible)")
	}
}

// fakeTallRow is a minimal non-focusable Row with a fixed, oversized
// height, used only to exercise Form's fits/scroll logic.
type fakeTallRow struct{ height int }

func (r *fakeTallRow) Height(w int) int            { return r.height }
func (r *fakeTallRow) Layout(x, y, w int)          {}
func (r *fakeTallRow) Draw(s tcell.Screen, _ bool) {}
func (r *fakeTallRow) Focusable() bool             { return false }

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
