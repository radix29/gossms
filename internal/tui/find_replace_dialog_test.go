package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// newFindTestApp returns an App with one query panel holding text, plus the
// find dialog wired up the way buildUI does.
func newFindTestApp(t *testing.T, text string) (*App, *QueryPanel) {
	t.Helper()
	a := newTestApp()
	a.findDialog = NewFindReplaceDialog(a)
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.editor.SetText(text)
	return a, qp
}

func TestFindDialogNeedsAQueryPanel(t *testing.T) {
	a := newTestApp()
	a.findDialog = NewFindReplaceDialog(a)

	a.findDialog.ShowFind()
	if a.findDialog.Visible() {
		t.Fatal("Find dialog opened with no query panel to search")
	}
	if !strings.Contains(a.statusText, "No active query panel") {
		t.Fatalf("status = %q, want a 'no active query panel' notice", a.statusText)
	}

	a.findNextInEditor(1)
	if !strings.Contains(a.statusText, "No active query panel") {
		t.Fatalf("F3 status = %q, want a 'no active query panel' notice", a.statusText)
	}
}

func TestFindDialogSeedsFromSingleLineSelection(t *testing.T) {
	a, qp := newFindTestApp(t, "select Total from dbo.Orders\nwhere Total > 0")
	qp.editor.SelectAll() // multi-line
	a.findDialog.ShowFind()
	if got := a.findDialog.fFind.Value(); got != "" {
		t.Fatalf("Find what = %q after a multi-line selection, want it left alone", got)
	}
	a.findDialog.Hide()

	// A single-line selection does seed the field.
	qp.editor.SetText("select Total from dbo.Orders")
	if err := qp.editor.SetSearch(searchFor("Total")); err != nil {
		t.Fatal(err)
	}
	qp.editor.FindNext(1) // selects "Total"
	a.findDialog.ShowFind()
	if got := a.findDialog.fFind.Value(); got != "Total" {
		t.Fatalf("Find what = %q, want the selected word", got)
	}
}

func TestFindDialogEnterFindsFromAField(t *testing.T) {
	a, qp := newFindTestApp(t, "a bb a bb a")
	a.findDialog.ShowFind()
	a.findDialog.fFind.SetValue("bb")

	// Focus is on the Find what field; Enter presses Find Next.
	a.findDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if got := qp.editor.SelectedText(); got != "bb" {
		t.Fatalf("selection = %q after Enter, want the first match", got)
	}
	if !strings.Contains(a.findDialog.status, "Match 1 of 2") {
		t.Fatalf("status = %q, want a match-position readout", a.findDialog.status)
	}

	a.findDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if !strings.Contains(a.findDialog.status, "Match 2 of 2") {
		t.Fatalf("status = %q after a second Enter, want match 2", a.findDialog.status)
	}
}

func TestFindDialogTabReachesButtons(t *testing.T) {
	a, _ := newFindTestApp(t, "x")
	a.findDialog.ShowFind()
	d := a.findDialog

	// Four fields then three buttons in Find mode.
	if got, want := d.focusCount(), 7; got != want {
		t.Fatalf("focusCount = %d, want %d", got, want)
	}
	// While focus is in the fields, Enter presses Find Next (button 0).
	if got := d.btnFocus(); got != 0 {
		t.Fatalf("btnFocus = %d with a field focused, want 0", got)
	}
	for i := 0; i < 4; i++ {
		d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	if got := d.btnFocus(); got != 0 || d.focusIdx != 4 {
		t.Fatalf("after 4 Tabs: focusIdx=%d btnFocus=%d, want the first button", d.focusIdx, got)
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	if got := d.btnFocus(); got != 1 {
		t.Fatalf("btnFocus = %d after another Tab, want 1 (Find Previous)", got)
	}
	// Tab wraps back around to the first field rather than dead-ending.
	for i := 0; i < 2; i++ {
		d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	if d.focusIdx != 0 {
		t.Fatalf("focusIdx = %d after wrapping, want 0", d.focusIdx)
	}
}

func TestFindDialogEscapeKeepsTheSearchForF3(t *testing.T) {
	a, qp := newFindTestApp(t, "a bb a bb a")
	a.findDialog.ShowFind()
	a.findDialog.fFind.SetValue("bb")
	a.findDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))

	a.findDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if a.findDialog.Visible() {
		t.Fatal("Escape did not close the dialog")
	}
	if !qp.editor.HasSearch() {
		t.Fatal("Escape dropped the search — F3 would have nothing to repeat")
	}
	if !a.hasEditorSearch() {
		t.Fatal("hasEditorSearch is false, so Find Next/Previous would be disabled in the menu")
	}

	a.findNextInEditor(1)
	if !strings.Contains(a.statusText, "Match 2 of 2") {
		t.Fatalf("status = %q after F3, want match 2", a.statusText)
	}
}

func TestFindNextOpensTheDialogWhenNothingIsSearchedYet(t *testing.T) {
	a, _ := newFindTestApp(t, "abc")
	a.findNextInEditor(1)
	if !a.findDialog.Visible() {
		t.Fatal("F3 with no search set neither found anything nor opened the dialog")
	}
}

func TestFindWordAtCursorSearchesWholeWord(t *testing.T) {
	a, qp := newFindTestApp(t, "id identity\nid")
	// Cursor at the start of the first "id".
	a.findWordAtCursor()

	if got := qp.editor.SearchOpts(); got.Query != "id" || !got.WholeWord {
		t.Fatalf("search = %+v, want a whole-word search for \"id\"", got)
	}
	// Whole-word: "identity" must not be a hit, so only the two bare "id"s
	// count.
	if got, want := qp.editor.MatchCount(), 2; got != want {
		t.Fatalf("MatchCount = %d, want %d — 'identity' was matched too", got, want)
	}
	// The dialog's own fields track it, so opening it afterwards shows what
	// is actually being searched.
	if got := a.findDialog.fFind.Value(); got != "id" {
		t.Fatalf("dialog Find what = %q, want \"id\"", got)
	}
	if !a.findDialog.cbWord.Checked() {
		t.Fatal("dialog's whole-word option is out of sync with the search it ran")
	}
}

func TestFindWordAtCursorOnWhitespace(t *testing.T) {
	a, qp := newFindTestApp(t, "   ")
	a.findWordAtCursor()
	if qp.editor.HasSearch() {
		t.Fatal("Ctrl+F3 on whitespace started a search anyway")
	}
	if !strings.Contains(a.statusText, "No word at the cursor") {
		t.Fatalf("status = %q, want a 'no word' notice", a.statusText)
	}
}

func TestFindDialogReplaceAllReportsCount(t *testing.T) {
	a, qp := newFindTestApp(t, "a a\na")
	a.findDialog.ShowReplace()
	a.findDialog.fFind.SetValue("a")
	a.findDialog.fReplace.SetValue("z")
	a.findDialog.doReplaceAll()

	if got, want := qp.editor.Text(), "z z\nz"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if !strings.Contains(a.findDialog.status, "Replaced 3") {
		t.Fatalf("status = %q, want a replacement count", a.findDialog.status)
	}
}

// Find mode has no "in selection only" option, so a Replace All in Find
// mode must not silently inherit a ticked checkbox from a previous Replace
// showing — the option is compiled from replaceMode && cbSel.
func TestFindModeIgnoresTheSelectionOnlyOption(t *testing.T) {
	a, qp := newFindTestApp(t, "a a")
	a.findDialog.cbSel.SetChecked(true)
	a.findDialog.ShowFind()
	a.findDialog.fFind.SetValue("a")
	a.findDialog.doFind(1)

	if qp.editor.SearchOpts().InSelection {
		t.Fatal("Find mode compiled an in-selection search from the hidden checkbox")
	}
}

func TestFindDialogReportsAnInvalidRegex(t *testing.T) {
	a, qp := newFindTestApp(t, "abc")
	a.findDialog.ShowFind()
	a.findDialog.fFind.SetValue("(unclosed")
	a.findDialog.cbRegex.SetChecked(true)
	a.findDialog.doFind(1)

	if !strings.HasPrefix(a.findDialog.status, "Invalid regex") || !a.findDialog.statusErr {
		t.Fatalf("status = %q (err=%v), want an invalid-regex error", a.findDialog.status, a.findDialog.statusErr)
	}
	if qp.editor.HasSearch() {
		t.Fatal("an invalid pattern left a search active")
	}
}

func TestFindDialogEmptyTermIsRefused(t *testing.T) {
	a, qp := newFindTestApp(t, "abc")
	a.findDialog.ShowFind()
	a.findDialog.fFind.SetValue("")
	a.findDialog.doFind(1)

	if qp.editor.HasSearch() {
		t.Fatal("an empty term started a search")
	}
	if !a.findDialog.statusErr {
		t.Fatalf("status = %q, want a notice that nothing was entered", a.findDialog.status)
	}
}

// searchFor is the SearchOptions a plain literal search compiles from.
func searchFor(q string) controls.SearchOptions { return controls.SearchOptions{Query: q} }

// Ctrl+F3 compiles a search without opening the dialog, so it has to leave
// the dialog describing that search and no other. It used to sync only Find
// what / whole word / regexp, leaving Match case (and, after a Replace-mode
// showing, "in selection only") saying whatever the user last chose — so the
// dialog claimed a case-sensitive search the editor was not running, and
// optionsChanged saw a difference that wasn't real.
func TestFindWordAtCursorSyncsEveryDialogField(t *testing.T) {
	// SetText leaves the caret at the start, so the first word is the one
	// under it — no cursor movement needed to make this deterministic.
	a, qp := newFindTestApp(t, "Total from dbo.Orders")
	d := a.findDialog

	// Leave every option set the way a previous search would have.
	d.replaceMode = true
	d.cbCase.SetChecked(true)
	d.cbRegex.SetChecked(true)
	d.cbSel.SetChecked(true)
	d.fReplace.SetValue("Amount")

	a.findWordAtCursor()

	if got := d.fFind.Value(); got != "Total" {
		t.Fatalf("Find what = %q, want the word at the cursor", got)
	}
	opts := qp.editor.SearchOpts()
	if opts.Query != "Total" || !opts.WholeWord {
		t.Fatalf("editor search = %+v, want a whole-word search for Total", opts)
	}
	if d.cbCase.Checked() != opts.MatchCase {
		t.Errorf("Match case = %v, editor MatchCase = %v", d.cbCase.Checked(), opts.MatchCase)
	}
	if d.cbWord.Checked() != opts.WholeWord {
		t.Errorf("Match whole word = %v, editor WholeWord = %v", d.cbWord.Checked(), opts.WholeWord)
	}
	if d.cbRegex.Checked() != opts.Regexp {
		t.Errorf("Regular expression = %v, editor Regexp = %v", d.cbRegex.Checked(), opts.Regexp)
	}
	if d.cbSel.Checked() != opts.InSelection {
		t.Errorf("in selection only = %v, editor InSelection = %v", d.cbSel.Checked(), opts.InSelection)
	}
	// The replacement text is the dialog's to own, so it survives — and the
	// compiled search carries it, so the two still agree.
	if got := d.fReplace.Value(); got != "Amount" {
		t.Errorf("Replace with = %q, want the user's text kept", got)
	}
	// The sequence that matters: Ctrl+F3, then open the dialog, then Find
	// Next. optionsChanged short-circuits until a showing captures a target,
	// so opening it is what makes the comparison real — and it must find
	// nothing to recompile, or Find Next restarts from the cursor instead of
	// stepping on to the next match.
	d.ShowFind()
	if d.optionsChanged() {
		t.Error("optionsChanged after Ctrl+F3: a Find Next would recompile and restart from the cursor")
	}
	d.Hide()
}

// A latch must not survive into the next showing (tuikit invariant 4): a
// dialog dismissed mid-drag would reopen routing every click to that field.
func TestFindDialogShowClearsTheDragLatch(t *testing.T) {
	a, _ := newFindTestApp(t, "select 1")
	d := a.findDialog
	d.dragField = d.fFind
	d.ShowFind()
	if d.dragField != nil {
		t.Error("Show left a drag latch armed from the previous showing")
	}
	d.Hide()
}
