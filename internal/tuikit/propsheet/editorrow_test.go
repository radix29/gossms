package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

func newTestEditorRow(text string) *EditorRow {
	r := NewEditorRow("Command", controls.NewEditor(nil), 6)
	r.Layout(0, 0, 60)
	r.SetValue(text)
	return r
}

// An editor row must hand Tab and the arrow keys it can't act on back to the
// Form, or the box is a keyboard trap: Editor answers true to Up at the first
// line, Down at the last, and Tab, which it indents with.
func TestEditorRowReleasesTheKeysAFormNavigatesWith(t *testing.T) {
	r := newTestEditorRow("one\ntwo")

	for _, k := range []tcell.Key{tcell.KeyTab, tcell.KeyBacktab} {
		if r.HandleKey(key(k, tcell.ModNone)) {
			t.Errorf("row consumed %v; Form can never move focus off it", tcell.KeyNames[k])
		}
	}
	if got := r.Value(); got != "one\ntwo" {
		t.Errorf("Tab edited the text: %q", got)
	}
	if r.HandleKey(key(tcell.KeyUp, tcell.ModNone)) {
		t.Error("row consumed Up at the first line")
	}
	if !r.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Error("row released Down with a line below the caret")
	}
	if r.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Error("row consumed Down at the last line")
	}
	if r.HandleKey(key(tcell.KeyEscape, tcell.ModNone)) {
		t.Error("row consumed Escape; the dialog can never close from here")
	}
}

// Typing and Enter belong to the editor, whatever else the form wants them for.
func TestEditorRowKeepsTheKeysThatEdit(t *testing.T) {
	r := newTestEditorRow("")
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, "S", tcell.ModNone),
		key(tcell.KeyEnter, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, "X", tcell.ModNone),
	} {
		if !r.HandleKey(ev) {
			t.Fatalf("row released %v", ev.Key())
		}
	}
	if got := r.Value(); got != "S\nX" {
		t.Fatalf("typed text is %q, want \"S\\nX\"", got)
	}
}

func TestEditorRowDirtyAndRevert(t *testing.T) {
	r := newTestEditorRow("SELECT 1")
	if r.Dirty() {
		t.Fatal("row is dirty as loaded")
	}
	r.Edit("SELECT 2")
	if !r.Dirty() {
		t.Fatal("row is clean after an edit")
	}
	r.Revert()
	if r.Dirty() || r.Value() != "SELECT 1" {
		t.Fatalf("revert left %q, dirty=%v", r.Value(), r.Dirty())
	}
}

// SetValue is the post-load setter and moves the baseline with the value; Edit
// is the user's keystroke. A page's apply gates on Dirty(), so the two must not
// stand in for each other.
func TestEditorRowSetValueIsNotAnEdit(t *testing.T) {
	r := newTestEditorRow("SELECT 1")
	changes := 0
	r.SetOnChange(func(string) { changes++ })

	r.SetValue("EXEC dbo.usp_x")
	if r.Dirty() {
		t.Error("SetValue left the row dirty")
	}
	if changes != 0 {
		t.Errorf("SetValue fired OnChange %d time(s)", changes)
	}
	r.Edit("EXEC dbo.usp_y")
	if !r.Dirty() || changes != 1 {
		t.Errorf("after Edit: dirty=%v, %d change callback(s), want true/1", r.Dirty(), changes)
	}
}

// A tab in the loaded text is expanded by SetText, so a baseline taken from the
// caller's string rather than from the editor never matches what the editor
// holds: an edit undone by hand leaves the row dirty for good, and Revert
// writes back text the page never loaded. It is the bug File > Open shipped, in
// row form.
func TestEditorRowBaselineIsWhatTheEditorHolds(t *testing.T) {
	r := newTestEditorRow("SELECT\t1\r\nFROM t")
	if r.Dirty() {
		t.Fatalf("row is dirty as loaded; value %q", r.Value())
	}
	loaded := r.Value()

	// Type a character and take it back out again: the document version moves,
	// so the row can only answer "clean" by comparing text against the baseline.
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, "X", tcell.ModNone))
	if !r.Dirty() {
		t.Fatal("row is clean after typing into it")
	}
	r.HandleKey(key(tcell.KeyBackspace2, tcell.ModNone))
	if r.Dirty() {
		t.Errorf("row is still dirty after undoing the edit: %q vs loaded %q", r.Value(), loaded)
	}
}

// TestTheEditorsTwoReadOnlyGatesAreIndependent. A page gates the box for its
// own reason (a job step that is not T-SQL) and the form gates it for the
// login's rights; whichever is set last must not cancel the other out. Lifting
// the permission gate over a step the page had already gated is the case that
// used to be unreachable, and the one that would hand the user an editable
// PowerShell command.
func TestTheEditorsTwoReadOnlyGatesAreIndependent(t *testing.T) {
	ed := controls.NewEditor(nil)
	r := NewEditorRow("Command", ed, 6)

	r.SetReadOnly(true)
	if !ed.ReadOnly() {
		t.Fatal("the page's own gate did not reach the editor")
	}
	r.SetDrawReadOnly(true)
	r.SetDrawReadOnly(false)
	if !ed.ReadOnly() {
		t.Error("lifting the form's gate made a row the page had gated editable")
	}

	// The other order: the form gates it first, and the page's answer arrives
	// while that gate is on. Lifting the form's gate must apply it.
	r2 := NewEditorRow("Command", controls.NewEditor(nil), 6)
	r2.SetDrawReadOnly(true)
	r2.SetReadOnly(true)
	if !r2.Editor().ReadOnly() {
		t.Error("the editor became editable under the form's gate")
	}
	r2.SetDrawReadOnly(false)
	if !r2.Editor().ReadOnly() {
		t.Error("the page's gate, set while the form's was on, was forgotten")
	}

	// The case the guard exists for: a page answering "editable" while the
	// form's permission gate is on must not lift that gate — the box would take
	// typing on a page the login may not write.
	r3 := NewEditorRow("Command", controls.NewEditor(nil), 6)
	r3.SetDrawReadOnly(true)
	r3.SetReadOnly(false)
	if !r3.Editor().ReadOnly() {
		t.Error("a page's \"editable\" answer lifted the form's permission gate")
	}
	r3.SetDrawReadOnly(false)
	if r3.Editor().ReadOnly() {
		t.Error("the box stayed read-only after both gates were cleared")
	}

	// And a page that clears its own gate gets an editable box back.
	r.SetReadOnly(false)
	if ed.ReadOnly() {
		t.Error("clearing the page's gate left the editor read-only")
	}
}
