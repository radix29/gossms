package sqledit

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/tuikittest"
)

// testCompletionProvider returns a CompletionProvider offering candidates
// whose name is case-insensitively prefixed by the word runes immediately
// before the cursor: the gate is under test here, not a real provider.
func testCompletionProvider(candidates ...string) editor.CompletionProvider {
	return func(req editor.CompletionRequest) ([]editor.CompletionItem, int) {
		lines, row, col := req.Lines, req.Row, req.Col
		if row >= len(lines) {
			return nil, col
		}
		line := lines[row]
		c := core.Clamp(col, 0, len(line))
		start := c
		for start > 0 && core.IsWordRune(line[start-1]) {
			start--
		}
		prefix := strings.ToLower(string(line[start:c]))
		var items []editor.CompletionItem
		for _, cand := range candidates {
			if strings.HasPrefix(strings.ToLower(cand), prefix) {
				items = append(items, editor.CompletionItem{Text: cand, Label: cand})
			}
		}
		return items, start
	}
}

// newSmartEditor is newTestEditor with IndentRule, as the query editor has it.
func newSmartEditor(text string) *editor.Editor {
	e := editor.NewEditor(nil)
	e.SetIndentRule(IndentRule)
	e.SetText(text)
	return e
}

// pressEnter presses Enter with the cursor at (row, col).
func pressEnter(e *editor.Editor, row, col int) {
	setCursor(e, row, col)
	e.HandleKey(tuikittest.Key(tcell.KeyEnter, tcell.ModNone))
}

func typeString(e *editor.Editor, s string) {
	for _, r := range s {
		e.HandleKey(tuikittest.Rune(r, tcell.ModNone))
	}
}

func TestSmartIndentTriggers(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want string // the new line's leading whitespace
	}{
		{"open paren", "INSERT INTO t (", "    "},
		{"paren after indent", "    INSERT INTO t (", "        "},
		{"select alone", "SELECT", "    "},
		{"from alone", "FROM", "    "},
		{"where alone", "WHERE", "    "},
		{"lowercase", "select", "    "},
		{"mixed case", "Where", "    "},
		{"trailing spaces ignored", "SELECT   ", "    "},
		{"keyword indented", "    FROM", "        "},
		{"keyword after paren", "VALUES (SELECT", "    "},
		{"keyword not last", "SELECT a, b", ""},
		{"from not last", "FROM t", ""},
		{"keyword as a suffix", "MySELECT", ""},
		{"unrelated word", "ORDER BY", ""},
		{"closing paren", "SELECT (1)", ""},
		{"empty line", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSmartEditor(tc.line)
			pressEnter(e, 0, len(tc.line))
			got := e.Text()[len(tc.line)+1:]
			if got != tc.want {
				t.Fatalf("new line = %q, want %q", got, tc.want)
			}
		})
	}
}

// The bonus reads the text left of the cursor, so a split before the trigger
// does not take it.
func TestSmartIndentReadsLeftOfCursorOnly(t *testing.T) {
	e := newSmartEditor("SELECT a, b")
	pressEnter(e, 0, len("SELECT")) // split right after the keyword

	if got, want := e.Text(), "SELECT\n     a, b"; got != want {
		t.Fatalf("Text() = %q, want %q — the keyword is last to the cursor's left", got, want)
	}
}

// Clause indentation must not drift right down a query: each clause keyword
// starts back at the column of the one above it.
func TestSmartIndentDoesNotDriftAcrossClauses(t *testing.T) {
	e := newSmartEditor("")
	for _, s := range []string{"SELECT", "a, b", "FROM", "t", "WHERE", "x = 1"} {
		if s == "a, b" || s == "t" || s == "x = 1" {
			// Typed at the indent auto-indent just produced; the clause
			// keywords below go back to column 0 by hand, as a user would.
			typeString(e, s)
		} else {
			for _, col := e.CursorPos(); col > 0; _, col = e.CursorPos() {
				e.HandleKey(tuikittest.Key(tcell.KeyBackspace2, tcell.ModNone))
			}
			typeString(e, s)
		}
		e.HandleKey(tuikittest.Key(tcell.KeyEnter, tcell.ModNone))
	}
	// The trailing "    " is the last Enter carrying the "    x = 1" indent
	// down, which is auto-indent doing its job.
	want := "SELECT\n    a, b\nFROM\n    t\nWHERE\n    x = 1\n    "
	if got := e.Text(); got != want {
		t.Fatalf("Text() =\n%q\nwant\n%q", got, want)
	}
}

// '[' opens a bracket-quoted identifier, so it opens the popup on its own.
func TestEditorCompletionOpenBracketOpens(t *testing.T) {
	e := newTestEditor("")
	Configure(e)
	e.SetCompletionProvider(testCompletionProvider("Customers"))

	typeString(e, "[")
	if !e.OverlayActive() {
		t.Fatal("expected '[' (bracket-quoted identifier start) to open the popup")
	}

	// A digit-led fragment inside brackets still counts as a word start.
	e.SetText("")
	e.SetCompletionProvider(testCompletionProvider("2024data"))
	typeString(e, "[20")
	if !e.OverlayActive() {
		t.Fatal("expected a bracket-quoted fragment starting with a digit to keep the popup open")
	}
}

// '#' and '@' introduce a name the same way '[' does — a temp table or a
// variable, with the host's provider deciding what the name means. Without
// this the popup stays shut until a second character arrives, so the first
// keystroke of "#staging" shows nothing.
func TestEditorCompletionSigilOpens(t *testing.T) {
	for _, sigil := range []string{"#", "@", "##"} {
		// The stub provider matches on word runes alone, so the candidate is
		// spelled without a sigil: the gate is under test, not how a real
		// provider reads one.
		e := newTestEditor("")
		Configure(e)
		e.SetCompletionProvider(testCompletionProvider("staging"))
		typeString(e, sigil)
		if !e.OverlayActive() {
			t.Errorf("expected %q to open the popup", sigil)
		}
		typeString(e, "sta")
		if !e.OverlayActive() {
			t.Errorf("expected %q to keep the popup open", sigil+"sta")
		}
	}
}
