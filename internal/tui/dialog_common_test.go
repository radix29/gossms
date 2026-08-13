package tui

import "testing"

// fakeFocusable records what Focus was last told, so a test can assert which
// entry of a list actually ended up focused rather than only which index was
// returned. The two can disagree — that is the bug worth catching.
type fakeFocusable struct{ focused bool }

func (f *fakeFocusable) Focus(on bool) { f.focused = on }

func focusList(n int) ([]focusable, []*fakeFocusable) {
	real := make([]*fakeFocusable, n)
	list := make([]focusable, n)
	for i := range real {
		real[i] = &fakeFocusable{}
		list[i] = real[i]
	}
	return list, real
}

// focusedIn reports which entries claim focus. More than one is the failure
// setFocusIn's blur-everything-first loop exists to prevent.
func focusedIn(real []*fakeFocusable) []int {
	var on []int
	for i, f := range real {
		if f.focused {
			on = append(on, i)
		}
	}
	return on
}

func TestSetFocusInFocusesExactlyOne(t *testing.T) {
	list, real := focusList(4)

	if got := setFocusIn(list, 2, 0); got != 2 {
		t.Errorf("setFocusIn(_, 2, 0) = %d, want 2", got)
	}
	if on := focusedIn(real); len(on) != 1 || on[0] != 2 {
		t.Errorf("focused = %v, want exactly [2]", on)
	}

	// Moving focus must blur the previous holder, not add a second one.
	setFocusIn(list, 0, 2)
	if on := focusedIn(real); len(on) != 1 || on[0] != 0 {
		t.Errorf("after moving to 0, focused = %v, want exactly [0]", on)
	}
}

// An out-of-range index blurs everything and leaves the caller's index alone.
// RestoreDialog rebuilds a shorter focusable list on a mode change and calls
// setFocus with the old index, so this path is reached in normal use.
func TestSetFocusInOutOfRangeKeepsCallersIndex(t *testing.T) {
	list, real := focusList(3)
	setFocusIn(list, 1, 0)

	for _, i := range []int{-1, 3, 99} {
		if got := setFocusIn(list, i, 7); got != 7 {
			t.Errorf("setFocusIn(_, %d, 7) = %d, want the caller's 7 back", i, got)
		}
		if on := focusedIn(real); on != nil {
			t.Errorf("setFocusIn(_, %d, 7) left %v focused, want nothing", i, on)
		}
	}

	// An empty list is the same answer, without indexing anything.
	if got := setFocusIn(nil, 0, 4); got != 4 {
		t.Errorf("setFocusIn(nil, 0, 4) = %d, want 4", got)
	}
}

func TestIndexOfFocusableIsByIdentity(t *testing.T) {
	list, real := focusList(3)
	if got := indexOfFocusable(list, real[2]); got != 2 {
		t.Errorf("indexOfFocusable(_, real[2]) = %d, want 2", got)
	}
	// A distinct widget that compares equal field-for-field is still absent:
	// the callers hold the very pointers they put in the list.
	if got := indexOfFocusable(list, &fakeFocusable{}); got != -1 {
		t.Errorf("indexOfFocusable of a widget not in the list = %d, want -1", got)
	}
}

// Tab and Backtab wrap at both ends. Both edges are what the seven inline
// copies of this arithmetic each had to get right on their own.
func TestFocusCyclingWrapsBothWays(t *testing.T) {
	const n = 4
	for idx, want := range map[int]int{0: 1, 2: 3, 3: 0} {
		if got := nextFocus(idx, n); got != want {
			t.Errorf("nextFocus(%d, %d) = %d, want %d", idx, n, got, want)
		}
	}
	for idx, want := range map[int]int{3: 2, 1: 0, 0: 3} {
		if got := prevFocus(idx, n); got != want {
			t.Errorf("prevFocus(%d, %d) = %d, want %d", idx, n, got, want)
		}
	}
	// A single-entry cycle stays put in both directions rather than moving
	// to an index it doesn't have.
	if got := nextFocus(0, 1); got != 0 {
		t.Errorf("nextFocus(0, 1) = %d, want 0", got)
	}
	if got := prevFocus(0, 1); got != 0 {
		t.Errorf("prevFocus(0, 1) = %d, want 0", got)
	}
}

func TestScrollToShow(t *testing.T) {
	for _, tt := range []struct {
		name               string
		sel, scroll, dataH int
		want               int
	}{
		{"already visible scrolls nothing", 5, 3, 10, 3},
		{"at the top edge stays", 3, 3, 10, 3},
		{"at the bottom edge stays", 12, 3, 10, 3},
		{"above the viewport scrolls up to sel", 1, 3, 10, 1},
		{"below the viewport scrolls the minimum", 13, 3, 10, 4},
		{"far below jumps so sel is the last row", 40, 3, 10, 31},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := scrollToShow(tt.sel, tt.scroll, tt.dataH); got != tt.want {
				t.Errorf("scrollToShow(%d, %d, %d) = %d, want %d",
					tt.sel, tt.scroll, tt.dataH, got, tt.want)
			}
		})
	}
}

// A finished task leaves Close as the only outcome whichever button has focus.
// Cancel on a task that completed between the draw and the keypress would
// otherwise cancel nothing while looking like it did something.
func TestRunProgressButtonHidesOnAFinishedOrAbsentTask(t *testing.T) {
	for _, tt := range []struct {
		name string
		task *Task
	}{
		{"no task", nil},
		{"finished task", &Task{Done: true}},
	} {
		for _, btn := range []int{0, 1} {
			hidden := false
			runProgressButton(tt.task, btn, func() { hidden = true })
			if !hidden {
				t.Errorf("%s, button %d: did not hide", tt.name, btn)
			}
		}
	}
}

func TestRunProgressButtonCancelsARunningTask(t *testing.T) {
	cancelled := false
	task := &Task{cancel: func() { cancelled = true }}

	hidden := false
	runProgressButton(task, 1, func() { hidden = true })
	if !cancelled {
		t.Error("button 1 on a running task did not cancel it")
	}
	if hidden {
		t.Error("button 1 hid the dialog; Cancel must leave the progress view up")
	}

	// Button 0 is Close, and closing a running task leaves it running — the
	// status bar and Tasks dialog keep tracking it.
	cancelled, hidden = false, false
	runProgressButton(task, 0, func() { hidden = true })
	if cancelled {
		t.Error("button 0 cancelled the task; Close must leave it running")
	}
	if !hidden {
		t.Error("button 0 did not hide the dialog")
	}
}
