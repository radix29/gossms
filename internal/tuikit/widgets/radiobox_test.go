package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestRadioBoxBoundaryArrowsNotConsumed is a regression test: Up at the
// first option (and Down at the last) must fall through (return false)
// instead of being consumed as a no-op, so a caller like propsheet.Form
// can move focus to the next/previous row.
func TestRadioBoxBoundaryArrowsNotConsumed(t *testing.T) {
	r := NewRadioBox("", []string{"one", "two"})
	r.Focus(true)

	if r.HandleKey(key(tcell.KeyUp, tcell.ModNone)) {
		t.Fatal("HandleKey(Up) at first option = true, want false")
	}

	if !r.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Fatal("HandleKey(Down) from first to second option = false, want true")
	}
	if r.Selected() != 1 {
		t.Fatalf("Selected() = %d, want 1", r.Selected())
	}

	if r.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Fatal("HandleKey(Down) at last option = true, want false")
	}
}
