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
