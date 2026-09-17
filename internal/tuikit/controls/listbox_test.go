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
	// Release, then a genuine second press on the already-selected row
	// activates instead of re-selecting — the release in between is what
	// distinguishes this from tcell's resent Button1 while a button stays
	// held (see mouseDragging's doc comment), which must NOT re-activate.
	l.HandleMouse(tcell.NewEventMouse(0, 1, tcell.ButtonNone, tcell.ModNone))
	l.HandleMouse(tcell.NewEventMouse(0, 1, tcell.Button1, tcell.ModNone))
	if activated != 1 {
		t.Fatalf("activated = %d, want 1", activated)
	}
	if selectedCalls != 1 {
		t.Fatalf("OnSelect fired %d times on the activating click, want no extra call", selectedCalls)
	}
}

// TestListBoxHeldButtonOnSameRowDoesNotReActivate covers tcell's all-motion
// mouse tracking resending Buttons()==Button1 on every cursor motion while
// the button stays down: without ListBox's mouseDragging latch, a single
// physical click on an already-selected row that so much as twitches fires
// OnActivate twice.
func TestListBoxHeldButtonOnSameRowDoesNotReActivate(t *testing.T) {
	l := newTestListBox("a", "b", "c")
	l.SetSelected(1)
	var activateCount int
	l.OnActivate = func(i int) { activateCount++ }

	l.HandleMouse(tcell.NewEventMouse(0, 1, tcell.Button1, tcell.ModNone))
	if activateCount != 1 {
		t.Fatalf("activateCount after first press = %d, want 1", activateCount)
	}
	l.HandleMouse(tcell.NewEventMouse(0, 1, tcell.Button1, tcell.ModNone))
	if activateCount != 1 {
		t.Fatalf("activateCount after resent Button1 (no release) = %d, want 1 (still the same physical press)", activateCount)
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

func manyRows(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = string(rune('a' + i%26))
	}
	return items
}

// The wheel must scroll past the selected row. Callers lay out from Draw, so
// SetBounds is re-asserted with the same rect on every frame; when that re-ran
// ensureVisible, scroll snapped straight back onto the selection between one
// frame and the next and the list looked stuck.
func TestListBoxWheelScrollsPastTheSelectionAndSurvivesRelayout(t *testing.T) {
	l := newTestListBox(manyRows(20)...)
	for range 8 {
		l.HandleMouse(tcell.NewEventMouse(1, 1, tcell.WheelDown, tcell.ModNone))
	}
	if l.scroll != 8 {
		t.Fatalf("scroll = %d after 8 wheel clicks, want 8", l.scroll)
	}
	l.SetBounds(0, 0, 20, 5) // the next frame's layout
	if l.scroll != 8 {
		t.Fatalf("re-asserting the same bounds snapped scroll back to %d, want 8", l.scroll)
	}
	if l.Selected() != 0 {
		t.Errorf("scrolling moved the selection to %d", l.Selected())
	}
}

// Bounds that really change still pull the selection back into view — that is
// what SetBounds' re-clamp is for.
func TestListBoxChangedBoundsKeepTheSelectionVisible(t *testing.T) {
	l := newTestListBox(manyRows(20)...)
	l.SetSelected(19)
	if l.scroll != 15 {
		t.Fatalf("scroll = %d with the last of 20 rows selected in 5, want 15", l.scroll)
	}
	l.SetBounds(0, 0, 20, 10)
	if l.scroll != 10 {
		t.Errorf("scroll = %d after the list grew to 10 rows, want 10", l.scroll)
	}
}

// Arrow keys still scroll the selection back into view after a wheel scroll
// left it off screen.
func TestListBoxKeyboardPullsTheSelectionBack(t *testing.T) {
	l := newTestListBox(manyRows(20)...)
	for range 10 {
		l.HandleMouse(tcell.NewEventMouse(1, 1, tcell.WheelDown, tcell.ModNone))
	}
	l.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if l.Selected() != 1 {
		t.Fatalf("selection = %d, want 1", l.Selected())
	}
	if l.scroll != 1 {
		t.Errorf("scroll = %d, want the selection scrolled back into view at 1", l.scroll)
	}
}
