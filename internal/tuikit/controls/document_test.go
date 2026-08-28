package controls

import "testing"

// docOf wraps lines in a Document so a test can drive a Highlighter without
// an Editor. Each call returns a *fresh* Document, which matters: the
// built-in highlighters cache against (document, version), so passing a new
// one is how a test forces the full replay it wants to compare against.
func docOf(lines [][]rune) *Document { return new(Document{lines: lines}) }

// TestDocumentVersionChangesOnEveryMutation pins the invariant three caches
// depend on — the highlighters' prefix states, Editor's wrap flattening, and
// maxDisplayWidth. Each entry mutates the buffer through the public editing
// surface; a path that reached the lines without going through
// setLine/setLines/edit would leave the version unchanged here and, in the
// app, redraw the previous document's colours and wrap segments over the new
// text.
func TestDocumentVersionChangesOnEveryMutation(t *testing.T) {
	mutations := map[string]func(e *Editor){
		"typing a rune":     func(e *Editor) { e.insertRune('x') },
		"newline":           func(e *Editor) { e.insertNewline() },
		"backspace":         func(e *Editor) { e.cursorCol = 2; e.backspace() },
		"delete":            func(e *Editor) { e.deleteChar() },
		"paste":             func(e *Editor) { e.Paste("zz") },
		"SetText":           func(e *Editor) { e.SetText("other") },
		"duplicate lines":   func(e *Editor) { e.DuplicateLines() },
		"delete lines":      func(e *Editor) { e.DeleteLines() },
		"move lines down":   func(e *Editor) { e.MoveLinesDown() },
		"move lines up":     func(e *Editor) { e.cursorRow = 1; e.MoveLinesUp() },
		"indent":            func(e *Editor) { e.IndentLines() },
		"dedent":            func(e *Editor) { e.cursorRow = 1; e.DedentLines() },
		"toggle comments":   func(e *Editor) { e.ToggleLineComments() },
		"delete word left":  func(e *Editor) { e.cursorCol = 3; e.deleteWordLeft() },
		"delete word right": func(e *Editor) { e.deleteWordRight() },
		"cut a selection":   func(e *Editor) { e.SelectAll(); e.Cut() },
		"delete selection":  func(e *Editor) { e.SelectAll(); e.deleteSelection() },
		"uppercase":         func(e *Editor) { e.SelectAll(); e.UppercaseSelection() },
		"lowercase":         func(e *Editor) { e.SelectAll(); e.LowercaseSelection() },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			e := NewEditor(nil)
			e.SetBounds(0, 0, 40, 10)
			// Mixed case so both UppercaseSelection and LowercaseSelection
			// actually change something.
			e.SetText("    Select One\n    select two\n    select three")
			before := e.doc.Version()
			textBefore := e.Text()
			mutate(e)
			if e.Text() == textBefore {
				t.Fatalf("mutation left the text unchanged (%q) — the case tests nothing", textBefore)
			}
			if e.doc.Version() == before {
				t.Errorf("text changed but Version() stayed %d — a cache keyed on it "+
					"would keep serving the previous document", before)
			}
		})
	}
}

// TestDocumentVersionChangesOnUndoAndRedo is separate from the table above
// because undo restores the text it started from — the "did the text change"
// guard there would reject it — while still having to bump the version.
// Restoring old text is exactly as invalidating as writing new text: a
// highlighter still holding the post-edit prefix states would colour the
// undone document as though the edit were still in it.
func TestDocumentVersionChangesOnUndoAndRedo(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 40, 10)
	e.SetText("select /* open")

	e.pushUndo()
	e.cursorRow, e.cursorCol = 0, len(e.doc.Line(0))
	e.insertRune('*')
	e.insertRune('/')
	afterEdit := e.doc.Version()

	e.undo()
	if e.doc.Version() == afterEdit {
		t.Errorf("undo left Version() at %d — the restored text would keep the edit's cached state", afterEdit)
	}
	if e.Text() != "select /* open" {
		t.Fatalf("undo produced %q, want the original text", e.Text())
	}
	afterUndo := e.doc.Version()

	e.redo()
	if e.doc.Version() == afterUndo {
		t.Errorf("redo left Version() at %d", afterUndo)
	}
	if e.Text() != "select /* open*/" {
		t.Fatalf("redo produced %q, want the edited text back", e.Text())
	}
}

// TestDocumentMaxDisplayWidthTracksMutations covers the one cache that lives
// on Document itself, including that it is measured in terminal columns
// rather than runes.
func TestDocumentMaxDisplayWidthTracksMutations(t *testing.T) {
	e := NewEditor(nil)
	e.SetText("abc")
	if got := e.doc.maxDisplayWidth(); got != 3 {
		t.Errorf("maxDisplayWidth of %q = %d, want 3", "abc", got)
	}
	// Two CJK ideographs are two runes but four columns; a rune count here
	// would size the horizontal scrollbar to half the text.
	e.SetText("abc\n世界")
	if got := e.doc.maxDisplayWidth(); got != 4 {
		t.Errorf("maxDisplayWidth with a CJK line = %d, want 4 columns (not 2 runes)", got)
	}
	e.cursorRow, e.cursorCol = 0, 3
	e.insertRune('d')
	if got := e.doc.maxDisplayWidth(); got != 4 {
		t.Errorf("maxDisplayWidth after typing = %d, want 4", got)
	}
	e.insertRune('e')
	e.insertRune('f')
	if got := e.doc.maxDisplayWidth(); got != 6 {
		t.Errorf("maxDisplayWidth after the line grew = %d, want 6 — the cache went stale", got)
	}
}

// ---------------------------------------------------------------------------
// touch's bounded invalidation
// ---------------------------------------------------------------------------

// TestReplaceRangeKeepsTheWidthsOfLinesAboveItsSpan pins the promise
// replaceRange makes by calling touch(row) instead of touch(0): every line
// above the span is the same slice it was, so its measured width still holds.
// Dropping the lot is what undo used to do, and re-measuring every rune of a
// 20,000-line script is ~10ms on the Draw that follows — with Ctrl+Z held
// down, once per undo.
//
// Asserted on the cache itself, not on a timing: the mutant is touch(0), which
// produces identical widths and only costs work.
func TestReplaceRangeKeepsTheWidthsOfLinesAboveItsSpan(t *testing.T) {
	d := docOf([][]rune{[]rune("aaaa"), []rune("bb"), []rune("cccccc"), []rune("dd")})
	d.maxDisplayWidth() // warm every entry
	if len(d.lineW) != d.Len() {
		t.Fatalf("maxDisplayWidth left %d cached widths for %d lines", len(d.lineW), d.Len())
	}

	d.replaceRange(2, 1, [][]rune{[]rune("c")})
	if len(d.lineW) != 2 {
		t.Errorf("after replaceRange(2, ...) %d widths survive, want the 2 above the span", len(d.lineW))
	}
	if d.dirtyFrom != 2 {
		t.Errorf("dirtyFrom = %d after replaceRange(2, ...), want 2 — prefixStates resumes from it", d.dirtyFrom)
	}
}

// TestMaxDisplayWidthAfterReplaceRange is the correctness half of the above:
// the surviving prefix must still produce the right answer, and the truncation
// must not keep the *first* line of the span, whose text just changed.
func TestMaxDisplayWidthAfterReplaceRange(t *testing.T) {
	cases := []struct {
		name       string
		row, n     int
		with       []string
		wantBefore int
		wantAfter  int
	}{
		// The widest line is the span's own first line: an off-by-one that
		// kept lineW[row] would still report the old width here.
		{"the widest line shrinks", 1, 1, []string{"b"}, 10, 4},
		{"the widest line grows", 1, 1, []string{"bbbbbbbbbbbbbb"}, 10, 14},
		// The widest line is above the span, so the answer comes from the
		// surviving cache rather than from a re-measure.
		{"a narrow line is replaced", 2, 1, []string{"ccccc"}, 10, 10},
		// Line-count changes, where the tail has to be extended with unknowns.
		{"the span grows", 1, 2, []string{"b", "b", "b"}, 10, 4},
		{"the span shrinks", 1, 2, []string{"bbbbbbbbbbbb"}, 10, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := docOf([][]rune{[]rune("aaaa"), []rune("bbbbbbbbbb"), []rune("cc")})
			if got := d.maxDisplayWidth(); got != tc.wantBefore {
				t.Fatalf("maxDisplayWidth before = %d, want %d", got, tc.wantBefore)
			}
			with := make([][]rune, len(tc.with))
			for i, s := range tc.with {
				with[i] = []rune(s)
			}
			d.replaceRange(tc.row, tc.n, with)
			if got := d.maxDisplayWidth(); got != tc.wantAfter {
				t.Errorf("maxDisplayWidth after = %d, want %d", got, tc.wantAfter)
			}
			if len(d.lineW) != d.Len() {
				t.Errorf("%d cached widths for %d lines", len(d.lineW), d.Len())
			}
		})
	}
}
