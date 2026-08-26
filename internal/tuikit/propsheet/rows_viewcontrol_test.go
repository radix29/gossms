package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// focusedText returns a TextRow with its wrapped InputField focused —
// Draw is what normally focuses it, and these tests never draw.
func focusedText(label string) *TextRow {
	r := Text(label, "", 20)
	r.field.Focus(true)
	return r
}

func typeRune(r *TextRow, ch string) {
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone))
}

// A filter box steers what a page displays; it is not something Apply
// writes. Left dirty-tracked, a page whose only interactive rows are
// filters reports unsaved changes it has no way to save, and Refresh
// prompts to discard them.
func TestTextRowUntrackedIsNeverDirty(t *testing.T) {
	r := focusedText("Filter")
	typeRune(r, "x")
	if !r.Dirty() {
		t.Fatal("a tracked TextRow did not report dirty after an edit — test premise is wrong")
	}

	r2 := focusedText("Filter")
	r2.SetDirtyTracked(false)
	typeRune(r2, "x")
	if r2.Value() != "x" {
		t.Fatalf("value = %q, want %q — untracked must still accept input", r2.Value(), "x")
	}
	if r2.Dirty() {
		t.Error("an untracked TextRow reported dirty")
	}
}

func TestSelectRowUntrackedIsNeverDirty(t *testing.T) {
	r := Select("Scope", []string{"Database", "Schema"}, 0)
	r.SetDirtyTracked(false)
	r.dd.SetSelected(1)
	if r.Selected() != 1 {
		t.Fatalf("Selected() = %d, want 1", r.Selected())
	}
	if r.Dirty() {
		t.Error("an untracked SelectRow reported dirty")
	}
}

// OnChange has to fire on the edit, not on focus loss — a filter that only
// applied when the user tabbed away would not keep up with typing.
func TestTextRowOnChangeFiresPerEdit(t *testing.T) {
	r := focusedText("Filter")
	var seen []string
	r.SetOnChange(func(v string) { seen = append(seen, v) })

	typeRune(r, "a")
	typeRune(r, "b")
	// A key that changes nothing must not fire it.
	r.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))

	if len(seen) != 2 || seen[0] != "a" || seen[1] != "ab" {
		t.Errorf("onChange saw %v, want [a ab]", seen)
	}
}

func TestTextRowOnChangeFiresOnPaste(t *testing.T) {
	r := focusedText("Filter")
	fired := 0
	r.SetOnChange(func(string) { fired++ })
	r.Paste("abc")
	if fired != 1 {
		t.Errorf("onChange fired %d times on paste, want 1", fired)
	}
}

// SetItems replaces the choices; the baseline has to move with them, or the
// row compares its selection against an index into a list that is gone.
func TestSelectRowSetItemsResetsBaseline(t *testing.T) {
	r := Select("Default schema", []string{"dbo", "sales"}, 1)
	r.SetItems([]string{"hr", "ops", "dbo"})
	if r.Dirty() {
		t.Error("SetItems left the row dirty")
	}
	if got := r.Value(); got != "hr" {
		t.Errorf("Value() = %q, want the first item of the new list (%q)", got, "hr")
	}
	if got := r.Items(); len(got) != 3 || got[2] != "dbo" {
		t.Errorf("Items() = %v, want the new list", got)
	}
}

// SetSelected and SetItems are the page's own programmatic updates —
// firing onChange from them re-enters whatever set them.
func TestSelectRowOnChangeNotFiredProgrammatically(t *testing.T) {
	r := Select("Default schema", []string{"dbo", "sales"}, 0)
	fired := 0
	r.SetOnChange(func(string) { fired++ })
	r.SetSelected(1)
	r.SetItems([]string{"hr", "ops"})
	if fired != 0 {
		t.Errorf("onChange fired %d times from programmatic updates, want 0", fired)
	}
}

// SetDirtyTracked(false) promises Revert leaves the row alone. It didn't:
// Form.Revert blanked a filter box while the grid it filters stayed narrowed
// on the old term, so the two disagreed about what was on screen.
func TestUntrackedRowsAreNotReverted(t *testing.T) {
	r := focusedText("Filter")
	r.SetDirtyTracked(false)
	typeRune(r, "x")
	r.Revert()
	if r.Value() != "x" {
		t.Errorf("TextRow value = %q after Revert, want %q left alone", r.Value(), "x")
	}

	s := Select("Scope", []string{"Database", "Schema"}, 0)
	s.SetDirtyTracked(false)
	s.dd.SetSelected(1)
	s.Revert()
	if s.Selected() != 1 {
		t.Errorf("SelectRow selection = %d after Revert, want 1 left alone", s.Selected())
	}
}

// A tracked row reverts, and has to say so: onChange is how the row's value
// reaches whatever it drives, and a revert that skipped it left the page
// acting on text the row no longer shows.
func TestRevertFiresOnChange(t *testing.T) {
	r := Text("Name", "orig", 20)
	r.field.Focus(true)
	var seen []string
	r.SetOnChange(func(v string) { seen = append(seen, v) })
	typeRune(r, "x")
	r.Revert()
	if r.Value() != "orig" {
		t.Fatalf("value = %q after Revert, want %q", r.Value(), "orig")
	}
	if len(seen) != 2 || seen[1] != "orig" {
		t.Errorf("onChange saw %v, want the edit then the revert back to \"orig\"", seen)
	}

	// A revert that changes nothing must stay quiet, same as any other
	// no-op edit path.
	seen = nil
	r.Revert()
	if len(seen) != 0 {
		t.Errorf("onChange fired %v on a revert that changed nothing", seen)
	}

	s := Select("Scope", []string{"Database", "Schema"}, 0)
	var picks []string
	s.SetOnChange(func(v string) { picks = append(picks, v) })
	s.dd.SetSelected(1)
	s.Revert()
	if s.Selected() != 0 {
		t.Fatalf("SelectRow selection = %d after Revert, want 0", s.Selected())
	}
	if len(picks) != 1 || picks[0] != "Database" {
		t.Errorf("onChange saw %v, want [Database]", picks)
	}
}

// RadioRow drives other rows on the same page (New Login's Authentication
// group enables the password, object-id and mapped-object rows the source it
// names actually uses), so it has to report a change the way TextRow and
// SelectRow do.
func TestRadioRowOnChangeFiresOnKeyAndEdit(t *testing.T) {
	r := Radio("Authentication", []string{"SQL Server", "Windows", "Certificate"}, 0)
	var seen []int
	r.SetOnChange(func(i int) { seen = append(seen, i) })

	r.rb.Focus(true)
	r.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	r.Edit(2)
	// A key that moves nothing must not fire it: Down at the last option.
	r.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))

	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Errorf("onChange saw %v, want [1 2]", seen)
	}
}

// SetSelected is the page's own post-load setter — firing onChange from it
// re-enters whatever set it.
func TestRadioRowOnChangeNotFiredBySetSelected(t *testing.T) {
	r := Radio("Authentication", []string{"SQL Server", "Windows"}, 0)
	fired := 0
	r.SetOnChange(func(int) { fired++ })
	r.SetSelected(1)
	if fired != 0 {
		t.Errorf("onChange fired %d times from SetSelected, want 0", fired)
	}
}

// Ctrl+Z restores the baseline; the rows the group drives have to follow it
// back, or the page keeps the password fields of a source no longer selected.
func TestRadioRowRevertFiresOnChange(t *testing.T) {
	r := Radio("Authentication", []string{"SQL Server", "Windows"}, 0)
	var seen []int
	r.SetOnChange(func(i int) { seen = append(seen, i) })
	r.Edit(1)
	r.Revert()
	if len(seen) != 2 || seen[1] != 0 {
		t.Errorf("onChange saw %v, want the revert back to 0", seen)
	}
}
