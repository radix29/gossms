package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/controls/editor"
)

// The dialog itself (modes, options, status line, rescope, drag latch) is
// tested in tuikit's dialogs package. These cover goSSMS's routing: the target
// editor, the no-panel notice and the F3/Shift+F3/Ctrl+F3 entry points.

// newFindTestApp returns an App with one query panel holding text, plus the
// find dialog wired up the way buildUI does.
func newFindTestApp(t *testing.T, text string) (*App, *QueryPanel) {
	t.Helper()
	a := newTestApp()
	installDialog(a, withScreen(NewFindReplaceDialog(a), a.screen))
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.editor.SetText(text)
	return a, qp
}

func TestFindDialogNeedsAQueryPanel(t *testing.T) {
	a := newTestApp()
	installDialog(a, withScreen(NewFindReplaceDialog(a), a.screen))

	a.findDialog().ShowFind()
	if a.findDialog().Visible() {
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

// The dialog searches the active query panel's editor: it seeds from that
// editor's selection, and what it finds is selected there.
func TestFindDialogTargetsTheActiveQueryPanel(t *testing.T) {
	a, qp := newFindTestApp(t, "a bb a bb a")
	qp.editor.SelectRange(0, 2, 0, 4) // the first "bb"
	a.findDialog().ShowFind()
	if !a.findDialog().Visible() {
		t.Fatal("the dialog did not open over a query panel")
	}
	if got := a.findDialog().Fields().Query; got != "bb" {
		t.Fatalf("Find what = %q, want the panel's selection", got)
	}
	a.findDialog().HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if !qp.editor.HasSearch() || qp.editor.SelectedText() != "bb" {
		t.Fatalf("search not run in the panel's editor (has %v, selection %q)",
			qp.editor.HasSearch(), qp.editor.SelectedText())
	}
}

func TestFindDialogEscapeKeepsTheSearchForF3(t *testing.T) {
	a, qp := newFindTestApp(t, "a bb a bb a")
	qp.editor.SelectRange(0, 2, 0, 4)
	a.findDialog().ShowFind()
	a.findDialog().HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	before, _ := qp.editor.MatchPosition()

	a.findDialog().HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if a.findDialog().Visible() {
		t.Fatal("Escape did not close the dialog")
	}
	if !a.hasEditorSearch() {
		t.Fatal("hasEditorSearch is false, so Find Next/Previous would be disabled in the menu")
	}

	a.findNextInEditor(1)
	after, _ := qp.editor.MatchPosition()
	if after == before {
		t.Fatalf("F3 stayed on match %d — it restarted instead of stepping on", after)
	}
	if !strings.Contains(a.statusText, "of 2 — line 1") {
		t.Fatalf("status = %q after F3, want a match-position readout", a.statusText)
	}
}

func TestFindNextOpensTheDialogWhenNothingIsSearchedYet(t *testing.T) {
	a, _ := newFindTestApp(t, "abc")
	a.findNextInEditor(1)
	if !a.findDialog().Visible() {
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
	if f := a.findDialog().Fields(); f.Query != "id" || !f.WholeWord {
		t.Fatalf("dialog fields = %+v, out of sync with the search it ran", f)
	}
}

// WordAtCursor takes Unicode letters, so the whole-word search it starts must
// too: wrapped in RE2's ASCII \b, "Société" had no boundary after its "é" and
// Ctrl+F3 reported no match for the word under the caret.
func TestFindWordAtCursorOnANonASCIIWord(t *testing.T) {
	a, qp := newFindTestApp(t, "Société Sociétés\nx = Société")
	a.findWordAtCursor()

	if got, want := qp.editor.MatchCount(), 2; got != want {
		t.Fatalf("MatchCount = %d, want %d", got, want)
	}
	if line, col, ok := qp.editor.CurrentMatchPos(); !ok || line != 1 || col != 1 {
		t.Fatalf("current match = %d:%d (ok %v), want the word under the caret at 1:1", line, col, ok)
	}
	a.findNextInEditor(1)
	if line, col, ok := qp.editor.CurrentMatchPos(); !ok || line != 2 || col != 5 {
		t.Fatalf("after F3, current match = %d:%d (ok %v), want the next occurrence at 2:5", line, col, ok)
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

// Ctrl+F3 compiles a search without opening the dialog, so it has to leave
// the dialog describing that search and no other — Match case and "in
// selection only" included — while keeping the user's replacement text.
// (That AdoptSearch leaves Find Next nothing to recompile is tuikit's test.)
func TestFindWordAtCursorSyncsEveryDialogField(t *testing.T) {
	// SetText leaves the caret at the start, so the first word is the one
	// under it — no cursor movement needed to make this deterministic.
	a, qp := newFindTestApp(t, "Total from dbo.Orders")
	d := a.findDialog()

	// Leave every option set the way a previous search would have.
	d.AdoptSearch(editor.SearchOptions{
		Query: "x", Replace: "Amount",
		MatchCase: true, Regexp: true, InSelection: true,
	})

	a.findWordAtCursor()

	opts := qp.editor.SearchOpts()
	if opts.Query != "Total" || !opts.WholeWord {
		t.Fatalf("editor search = %+v, want a whole-word search for Total", opts)
	}
	if got := d.Fields(); got != opts {
		t.Errorf("dialog fields = %+v, editor search = %+v", got, opts)
	}
	if got := d.Fields().Replace; got != "Amount" {
		t.Errorf("Replace with = %q, want the user's text kept", got)
	}
}

// Ctrl+F3 with the caret inside (or just past) a word selects that word — the
// search's first match — rather than its next occurrence: FindNext searches
// from the caret, so it skipped the word Ctrl+F3 was pressed on.
func TestFindWordAtCursorSelectsTheWordUnderTheCaret(t *testing.T) {
	for _, col := range []int{8, 10} { // mid-word, just past it
		a, qp := newFindTestApp(t, "select foo, bar from t where foo = 1")
		qp.editor.SelectRange(0, col, 0, col)
		a.findWordAtCursor()
		if line, c, ok := qp.editor.CurrentMatchPos(); !ok || line != 1 || c != 8 {
			t.Errorf("caret at col %d: current match = %d:%d (ok %v), want the word under it at 1:8", col, line, c, ok)
		}
		if !strings.Contains(a.statusText, "Match 1 of 2") {
			t.Errorf("caret at col %d: status = %q, want match 1 of 2", col, a.statusText)
		}
	}
}
