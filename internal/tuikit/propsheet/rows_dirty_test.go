package propsheet

import "testing"

// DropDown and RadioBox silently ignore an out-of-range index. If the row
// stored that rejected index as its dirty baseline, Selected() != orig with
// nothing the user could do to reconcile them: the page would report unsaved
// changes forever, and Apply would issue a write nobody asked for. The
// baseline must come from reading the widget back.
func TestSelectRowOutOfRangeSelectionIsNotDirty(t *testing.T) {
	items := []string{"NONE", "ROW", "PAGE"}

	t.Run("constructor", func(t *testing.T) {
		for _, bad := range []int{-1, 3, 99} {
			r := Select("Data compression", items, bad)
			if r.Dirty() {
				t.Errorf("Select(..., %d): Dirty() = true on a freshly built row (Selected()=%d)",
					bad, r.Selected())
			}
		}
	})

	t.Run("SetSelected", func(t *testing.T) {
		for _, bad := range []int{-1, 3, 99} {
			r := Select("Data compression", items, 1)
			r.SetSelected(bad)
			if r.Dirty() {
				t.Errorf("SetSelected(%d): Dirty() = true (Selected()=%d)", bad, r.Selected())
			}
		}
	})

	// An in-range SetSelected must still rebase, and a real user edit after it
	// must still register — the fix must not make the row permanently clean.
	t.Run("a real edit is still dirty", func(t *testing.T) {
		r := Select("Data compression", items, 0)
		r.SetSelected(2)
		if r.Dirty() {
			t.Fatal("SetSelected(2) should rebase, not report dirty")
		}
		r.dd.SetSelected(1) // as the user's own keystroke would
		if !r.Dirty() {
			t.Error("an edit away from the baseline must report dirty")
		}
	})
}

// An empty item list is the degenerate case: every index is out of range, so
// the row must simply never be dirty rather than being stuck dirty.
func TestSelectRowEmptyItemsIsNeverDirty(t *testing.T) {
	r := Select("Empty", nil, 0)
	if r.Dirty() {
		t.Error("a row with no items must not be dirty")
	}
	r.SetSelected(5)
	if r.Dirty() {
		t.Error("a row with no items must not be dirty after SetSelected")
	}
}

// RadioRow has the same baseline rule as SelectRow.
func TestRadioRowOutOfRangeSelectionIsNotDirty(t *testing.T) {
	options := []string{"Emoji", "Symbols", "None"}

	for _, bad := range []int{-1, 3, 99} {
		if r := Radio("Icons", options, bad); r.Dirty() {
			t.Errorf("Radio(..., %d): Dirty() = true on a freshly built row (Selected()=%d)",
				bad, r.Selected())
		}
		r := Radio("Icons", options, 1)
		r.SetSelected(bad)
		if r.Dirty() {
			t.Errorf("SetSelected(%d): Dirty() = true (Selected()=%d)", bad, r.Selected())
		}
	}
}
