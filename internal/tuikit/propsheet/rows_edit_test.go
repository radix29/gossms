package propsheet

import "testing"

// TestEditDirtiesAndSetValueDoesNot pins the one distinction every apply
// closure in the application depends on. A page writes a row only when it is
// Dirty(), so the post-load setter and the user-edit setter cannot be
// interchangeable: setting a row with Edit and getting a clean row means the
// page silently declines to write it, and setting one with SetValue and
// getting a dirty row means the page writes something nobody asked for.
func TestEditDirtiesAndSetValueDoesNot(t *testing.T) {
	t.Run("TextRow", func(t *testing.T) {
		r := Text("Name", "before", 20)
		if r.Dirty() {
			t.Fatal("a freshly built row is already dirty")
		}
		r.SetValue("after")
		if r.Dirty() {
			t.Error("SetValue left the row dirty: a page would write a value the user never typed")
		}
		if r.Value() != "after" {
			t.Errorf("SetValue did not take: %q", r.Value())
		}
		r.Edit("edited")
		if !r.Dirty() {
			t.Error("Edit left the row clean: a page would decline to write the user's change")
		}
		if r.Value() != "edited" {
			t.Errorf("Edit did not take: %q", r.Value())
		}
	})

	t.Run("SelectRow", func(t *testing.T) {
		r := Select("Mode", []string{"OFF", "ON"}, 0)
		if r.Dirty() {
			t.Fatal("a freshly built row is already dirty")
		}
		r.SetSelected(1)
		if r.Dirty() {
			t.Error("SetSelected left the row dirty")
		}
		r.Edit(0)
		if !r.Dirty() {
			t.Error("Edit left the row clean")
		}
		if r.Value() != "OFF" {
			t.Errorf("Edit did not take: %q", r.Value())
		}
	})
}

// TestEditFiresOnChange covers the other half of behaving like a keystroke:
// a row whose OnChange drives dependent controls must see an Edit, or the
// page's dependent state is left describing the previous selection.
func TestEditFiresOnChange(t *testing.T) {
	var got []string
	r := Select("Mode", []string{"OFF", "ON"}, 0)
	r.SetOnChange(func(v string) { got = append(got, v) })

	r.SetSelected(1)
	if len(got) != 0 {
		t.Errorf("SetSelected fired OnChange: %q — it is programmatic and would re-enter whatever set it", got)
	}
	r.Edit(0)
	if len(got) != 1 || got[0] != "OFF" {
		t.Errorf("Edit fired OnChange %q, want one call with \"OFF\"", got)
	}
	// Selecting what is already selected is not a change.
	r.Edit(0)
	if len(got) != 1 {
		t.Errorf("a no-op Edit fired OnChange: %q", got)
	}
}

// TestEditIgnoresAnOutOfRangeIndex mirrors SetSelected's guard: DropDown
// rejects the index, so accepting it into the baseline comparison would leave
// a permanently dirty row reporting unsaved changes forever.
func TestEditIgnoresAnOutOfRangeIndex(t *testing.T) {
	r := Select("Mode", []string{"OFF", "ON"}, 0)
	r.Edit(7)
	if r.Value() != "OFF" {
		t.Errorf("an out-of-range Edit changed the value to %q", r.Value())
	}
	if r.Dirty() {
		t.Error("an out-of-range Edit left the row dirty")
	}
}

// TestEditOnADisabledTextRowDoesNothing: a disabled row is not editable, and
// a page that greys one out has decided its value must not be written.
func TestEditOnADisabledTextRowDoesNothing(t *testing.T) {
	r := Text("Name", "before", 20)
	r.SetEnabled(false)
	r.Edit("after")
	if r.Value() != "before" || r.Dirty() {
		t.Errorf("Edit changed a disabled row: value %q dirty %v", r.Value(), r.Dirty())
	}
}
