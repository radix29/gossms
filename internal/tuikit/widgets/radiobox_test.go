package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestRadioBoxBoundaryArrowsNotConsumed pins down that Up at the first
// option (and Down at the last) falls through (return false) instead of
// being consumed as a no-op, so a caller like propsheet.Form can move focus
// to the next/previous row.
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

// See TestDisabledCheckBoxRefusesInput for the contract. SetSelected still
// works, which is what lets the Back Up Database dialog pin the group to Full
// on a Managed Instance rather than merely freezing whatever the user picked.
func TestDisabledRadioBoxRefusesInput(t *testing.T) {
	r := NewRadioBox("Backup Type:", []string{"Full", "Differential", "Transaction Log"})
	r.SetBounds(4, 2)
	r.Focus(true)
	r.SetSelected(1)
	r.SetEnabled(false)

	if r.Enabled() {
		t.Fatal("SetEnabled(false) did not take")
	}
	if r.HandleKey(key(tcell.KeyDown, tcell.ModNone)) {
		t.Error("a disabled radio group consumed Down")
	}
	// Row 2 is the label, so the options start at row 3.
	if r.HandleMouse(mouse(5, 5, tcell.Button1)) {
		t.Error("a disabled radio group consumed a click")
	}
	if got := r.Selected(); got != 1 {
		t.Errorf("selection moved to %d on a disabled group", got)
	}

	r.SetSelected(0)
	if got := r.Selected(); got != 0 {
		t.Errorf("SetSelected was refused on a disabled group: got %d", got)
	}

	r.SetEnabled(true)
	if !r.HandleKey(key(tcell.KeyDown, tcell.ModNone)) || r.Selected() != 1 {
		t.Error("re-enabling did not restore input")
	}
}
