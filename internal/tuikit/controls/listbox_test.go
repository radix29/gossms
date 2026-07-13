package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestListBox(items ...string) *ListBox {
	l := NewListBox()
	l.SetBounds(0, 0, 20, 5)
	l.SetItems(items)
	l.Focus(true)
	return l
}

func TestListBoxNavigation(t *testing.T) {
	l := newTestListBox("a", "b", "c")
	var selected []int
	l.OnSelect = func(i int) { selected = append(selected, i) }

	l.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if got := l.Selected(); got != 1 {
		t.Fatalf("Selected() after Down = %d, want 1", got)
	}
	l.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	l.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) // past the end
	if got := l.Selected(); got != 2 {
		t.Fatalf("Selected() at end = %d, want 2 (clamped)", got)
	}
	l.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone))
	if got := l.Selected(); got != 1 {
		t.Fatalf("Selected() after Up = %d, want 1", got)
	}
	if len(selected) == 0 {
		t.Fatal("OnSelect never fired")
	}
}

func TestListBoxEnterActivates(t *testing.T) {
	l := newTestListBox("a", "b")
	var activated int = -1
	l.OnActivate = func(i int) { activated = i }
	l.SetSelected(1)
	l.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if activated != 1 {
		t.Fatalf("activated = %d, want 1", activated)
	}
}

func TestListBoxClickSameRowActivates(t *testing.T) {
	l := newTestListBox("a", "b", "c")
	l.SetSelected(0)
	var activated int = -1
	var selectedCalls int
	l.OnActivate = func(i int) { activated = i }
	l.OnSelect = func(i int) { selectedCalls++ }

	// First click on a different row selects.
	l.HandleMouse(tcell.NewEventMouse(0, 1, tcell.Button1, tcell.ModNone))
	if l.Selected() != 1 || selectedCalls != 1 {
		t.Fatalf("after click on row 1: selected=%d calls=%d, want 1,1", l.Selected(), selectedCalls)
	}
	// Clicking the already-selected row activates instead of re-selecting.
	l.HandleMouse(tcell.NewEventMouse(0, 1, tcell.Button1, tcell.ModNone))
	if activated != 1 {
		t.Fatalf("activated = %d, want 1", activated)
	}
	if selectedCalls != 1 {
		t.Fatalf("OnSelect fired %d times on the activating click, want no extra call", selectedCalls)
	}
}

func TestListBoxSetItemsClampsSelection(t *testing.T) {
	l := newTestListBox("a", "b", "c")
	l.SetSelected(2)
	l.SetItems([]string{"x"})
	if got := l.Selected(); got != 0 {
		t.Fatalf("Selected() after shrinking items = %d, want 0", got)
	}
}

func TestListBoxEmptySelectedIsMinusOne(t *testing.T) {
	l := NewListBox()
	if got := l.Selected(); got != -1 {
		t.Fatalf("Selected() on empty list = %d, want -1", got)
	}
}
