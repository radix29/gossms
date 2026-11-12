package controls

// Strings is the text this package's controls draw on their own: DataGrid's
// status and error column, its built-in context-menu entries and its value
// viewer. A host may replace any of it (to translate it, say) on the UI
// goroutine before the first Draw; nothing dispatches on it. A grid reads
// Ready when created and the menu entries when the menu opens.
var Strings = struct {
	Ready, Error                      string
	Copy, CopyAll, CopyAllWithHeaders string
	ShowValue                         string
	ViewerHint, ViewerClose           string
}{
	Ready:              "Ready",
	Error:              "Error",
	Copy:               "Copy",
	CopyAll:            "Copy All",
	CopyAllWithHeaders: "Copy All with Headers",
	ShowValue:          "Show Value",
	ViewerHint:         "Esc or Close to dismiss — Shift+arrows to select, Ctrl+C to copy",
	ViewerClose:        "Close",
}

// ---------------------------------------------------------------------------
// Helpers shared across more than one file in this package
// ---------------------------------------------------------------------------

// prefixStates caches, for one version of one Document, the lexer state each line
// *begins* in — whether it opens inside a block comment, a CDATA section, and so
// on. Both built-in highlighters use it (SQL's through Document.sqlStarts); see
// SQLHighlighter for the measurements.
//
// A line's starting state depends on every line before it, so answering per line
// is O(document) and for a whole viewport O(H * document), on every event since
// Draw runs on all of them. One replay fills the whole array instead, reused
// until the text changes.
//
// Keyed on the *Document as well as its version: two Documents number versions
// independently from zero, so the pointer stops a highlighter handed a second
// document from answering with the first one's states.
type prefixStates[S comparable] struct {
	doc     *Document
	version uint64
	valid   bool
	states  []S
}

// at returns the state line idx begins in.
//
// zero is the state the first line starts in; step is the one-line transition —
// given a line and the state carried into it, the state carried out. step must be
// the function the highlighter's own per-line scan agrees with, or a line's colour
// depends on which cache generation drew it (flicker while scrolling).
func (c *prefixStates[S]) at(doc *Document, idx int, zero S, step func(line []rune, in S) S) S {
	switch {
	case !c.valid || c.doc != doc || len(c.states) != doc.Len():
		// The length test is what makes the resume branch below safe for the
		// mutations that can change the line count — replaceRange leaves a
		// dirtyFrom of its span's first line whether or not the span kept its
		// length, and states[] is indexed by line, so a document that grew or
		// shrank has to start over.
		c.replay(doc, 0, zero, step, false)
	case c.version == doc.Version():
		// Nothing has changed since the last replay.
	case c.version+1 == doc.Version() && doc.dirtyFrom > 0:
		// Exactly one mutation since, and it left every line above dirtyFrom
		// alone, so every state at or before that line is still correct.
		c.replay(doc, doc.dirtyFrom, zero, step, true)
	default:
		c.replay(doc, 0, zero, step, false)
	}
	if idx < 0 || idx >= len(c.states) {
		return zero
	}
	return c.states[idx]
}

// replay recomputes states[from:].
//
// stopOnConverge ends the walk as soon as a recomputed state matches the stored
// one: the carried state has rejoined the previous scan, so no later line can
// differ. Typing in a large script then costs the lines whose meaning changed, not
// the whole document: outside a comment it converges on the next line, and only
// an edit that opens or closes one propagates further.
func (c *prefixStates[S]) replay(doc *Document, from int, zero S, step func(line []rune, in S) S, stopOnConverge bool) {
	n := doc.Len()
	if cap(c.states) < n {
		grown := make([]S, n)
		copy(grown, c.states)
		c.states = grown
	}
	c.states = c.states[:n]

	in := zero
	if from > 0 && from < n {
		// states[from] describes lines before `from`, none of which moved.
		in = c.states[from]
	} else {
		from = 0
	}
	for i := from; i < n; i++ {
		if stopOnConverge && i > from && c.states[i] == in {
			break
		}
		c.states[i] = in
		in = step(doc.Line(i), in)
	}
	c.doc, c.version, c.valid = doc, doc.Version(), true
}

// hideToFit is the one overflow policy of Toolbar and ToolRow: it hides the
// entries of shown in order, one at a time, until need(shown) fits in avail,
// and reports whether it did. Only the order differs between the two —
// Toolbar's comes from DropRank, ToolRow's is right to left, so what a
// ToolRow hides is always the row's tail.
func hideToFit(shown []bool, order []int, need func(shown []bool) int, avail int) bool {
	for _, i := range order {
		if need(shown) <= avail {
			return true
		}
		shown[i] = false
	}
	return need(shown) <= avail
}
