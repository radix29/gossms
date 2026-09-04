package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ctrlZ is the key event PropertySheet routes to RevertPage.
func ctrlZ() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModCtrl) }

// dirtySheet returns a shown sheet whose one page holds an edited text row.
func dirtySheet(t *testing.T) (*PropertySheet, *TextRow) {
	t.Helper()
	p := newTestSheet("General")
	var page, seq int
	p.OnLoadPage = func(pg, s int) { page, seq = pg, s }
	p.Show()

	row := Text("Name", "srv01", 20)
	p.SetPageForm(page, seq, NewForm(row))
	row.Paste("-edited") // what typing into the field amounts to
	if !p.Dirty() {
		t.Fatal("the sheet is not dirty after editing a row — the test proves nothing")
	}
	return p, row
}

// Form.Revert had no caller at all until Ctrl+Z: every RevertFn closure and
// every row's Revert was correct and unreachable. This is the reachability
// half — that the key restores the loaded values.
func TestSheetCtrlZRevertsThePageToItsLoadedValues(t *testing.T) {
	p, row := dirtySheet(t)

	if !p.HandleKey(ctrlZ()) {
		t.Fatal("Ctrl+Z was not consumed by the sheet")
	}

	if got := row.Value(); got != "srv01" {
		t.Errorf("row value after Ctrl+Z = %q, want the loaded %q", got, "srv01")
	}
	if p.Dirty() {
		t.Error("the sheet is still dirty after Ctrl+Z")
	}
	if p.message == "" {
		t.Error("no message shown — a revert to values identical to what was " +
			"typed would otherwise look like the key did nothing")
	}
}

// The pages that matter most here are the grid ones: their pending edits live
// in the page's own state, and RevertFn is the only way Form can reach them.
func TestSheetCtrlZRunsAGridRowsRevertFn(t *testing.T) {
	p := newTestSheet("Files")
	var page, seq int
	p.OnLoadPage = func(pg, s int) { page, seq = pg, s }
	p.Show()

	edited := true
	gridRow := NewGridRow(controls.NewDataGrid(), 6)
	gridRow.DirtyFn = func() bool { return edited }
	gridRow.RevertFn = func() { edited = false }
	p.SetPageForm(page, seq, NewForm(gridRow))

	if !p.HandleKey(ctrlZ()) {
		t.Fatal("Ctrl+Z was not consumed by the sheet")
	}
	if edited {
		t.Error("the grid row's RevertFn never ran — the page's own pending " +
			"edit state survived the revert")
	}
}

// Every key must do something or say why not (docs/ui-rules.md on
// context-gating), and a clean page has nothing to restore.
func TestSheetCtrlZOnACleanPageReportsThereIsNothingToRevert(t *testing.T) {
	p := newTestSheet("General")
	var page, seq int
	p.OnLoadPage = func(pg, s int) { page, seq = pg, s }
	p.Show()
	p.SetPageForm(page, seq, NewForm(Text("Name", "srv01", 20)))

	if !p.HandleKey(ctrlZ()) {
		t.Fatal("Ctrl+Z was not consumed by the sheet")
	}
	if p.message == "" {
		t.Error("Ctrl+Z on a clean page silently did nothing")
	}
	if p.messageIsErr {
		t.Error("having nothing to revert is not an error")
	}
}

// Handled ahead of the zone switch, like F5, so it doesn't depend on where
// focus happens to be — a user who has tabbed to the buttons to press Cancel
// can revert instead.
func TestSheetCtrlZRevertsFromEveryZone(t *testing.T) {
	for _, z := range []focusZone{zonePages, zoneForm, zoneButtons} {
		p, row := dirtySheet(t)
		p.setZone(z)

		if !p.HandleKey(ctrlZ()) {
			t.Fatalf("zone %v: Ctrl+Z was not consumed", z)
		}
		if got := row.Value(); got != "srv01" {
			t.Errorf("zone %v: row value = %q, want the loaded %q", z, got, "srv01")
		}
	}
}

// A page the user never opened has no form, and RevertPage must not reach past
// the slice either — Refresh guards its index the same way.
func TestRevertPageIsSafeOnAnUnloadedOrOutOfRangePage(t *testing.T) {
	p := newTestSheet("General", "Files")
	p.Show() // loads page 0 only, and no form is ever supplied

	for _, page := range []int{-1, 0, 1, 99} {
		if p.RevertPage(page) {
			t.Errorf("RevertPage(%d) reported a revert with no loaded form", page)
		}
	}
}

// editorSheet returns a shown sheet whose one page holds an EditorRow followed
// by a TextRow, with the form focused on whichever index focus names.
func editorSheet(t *testing.T, focus int) (*PropertySheet, *EditorRow, *TextRow) {
	t.Helper()
	p := newTestSheet("Steps")
	var page, seq int
	p.OnLoadPage = func(pg, s int) { page, seq = pg, s }
	p.Show()

	ed := NewEditorRow("Command", controls.NewEditor(nil), 6)
	ed.SetValue("SELECT 1")
	txt := Text("Name", "step1", 20)
	f := NewForm(ed, txt)
	p.SetPageForm(page, seq, f)

	p.setZone(zoneForm)
	f.FocusFirst()
	for range focus {
		f.FocusNext()
	}
	return p, ed, txt
}

// typeIntoEditor drives the editor the way a keystroke does, so the edit lands
// on its own undo stack — SetValue and Edit would not.
func typeIntoEditor(r *EditorRow, ch rune) {
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(ch), tcell.ModNone))
}

// Ctrl+Z inside an EditorRow is the editor's undo, not the page's revert: the
// sheet consumed the key before the form ever saw it, so an undo in a job
// step's T-SQL box reverted the whole page and discarded every other row's
// edits with it.
func TestSheetCtrlZInAnEditorRowUndoesInsteadOfRevertingThePage(t *testing.T) {
	p, ed, txt := editorSheet(t, 0)
	txt.Paste("-edited")
	typeIntoEditor(ed, 'X')
	if ed.Value() == "SELECT 1" {
		t.Fatal("typing did not reach the editor — the test proves nothing")
	}

	if !p.HandleKey(ctrlZ()) {
		t.Fatal("Ctrl+Z was not consumed")
	}

	if got := ed.Value(); got != "SELECT 1" {
		t.Errorf("editor text after Ctrl+Z = %q, want the undone %q", got, "SELECT 1")
	}
	if got := txt.Value(); got != "step1-edited" {
		t.Errorf("the text row was reverted to %q — the page revert ran when "+
			"the editor's undo should have", got)
	}
}

// The other direction: with focus anywhere but an editor, Ctrl+Z is still the
// sheet's page revert. No other row kind claims the key.
func TestSheetCtrlZOutsideAnEditorRowStillRevertsThePage(t *testing.T) {
	p, ed, txt := editorSheet(t, 1)
	txt.Paste("-edited")

	if !p.HandleKey(ctrlZ()) {
		t.Fatal("Ctrl+Z was not consumed")
	}

	if got := txt.Value(); got != "step1" {
		t.Errorf("text row after Ctrl+Z = %q, want the loaded %q", got, "step1")
	}
	if ed.Value() != "SELECT 1" {
		t.Errorf("editor text = %q, want it untouched", ed.Value())
	}
}

// A read-only editor refuses Ctrl+Z (controls.readOnlySafeKey), so a job step
// whose command is not T-SQL keeps the page revert even with the box focused.
func TestSheetCtrlZInAReadOnlyEditorRowRevertsThePage(t *testing.T) {
	p, ed, txt := editorSheet(t, 0)
	ed.SetReadOnly(true)
	txt.Paste("-edited")

	if !p.HandleKey(ctrlZ()) {
		t.Fatal("Ctrl+Z was not consumed")
	}
	if got := txt.Value(); got != "step1" {
		t.Errorf("text row after Ctrl+Z = %q, want the loaded %q", got, "step1")
	}
}
