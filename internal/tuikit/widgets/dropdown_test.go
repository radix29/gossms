package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestDropDown(items []string) *DropDown {
	d := NewDropDown("", items, 20)
	d.Focus(true)
	return d
}

// TestDropDownClosedArrowsNotConsumed is a regression test: Up/Down/Escape
// on a closed dropdown must fall through (return false) rather than being
// swallowed as a no-op, so a caller like propsheet.Form can move focus to
// the next/previous row instead of a closed dropdown eating arrow
// navigation.
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
