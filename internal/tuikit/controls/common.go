package controls

// ---------------------------------------------------------------------------
// Helpers shared across more than one file in this package
// ---------------------------------------------------------------------------

// prefixStates caches, for one version of one Document, the lexer state each
// line *begins* in — whether it opens inside a block comment, a CDATA
// section, and so on. Both built-in highlighters use it; see SQLHighlighter
// for the measurements that motivated it.
//
// The state a line starts in depends on every line before it, so answering
// per line is O(document) and answering for a whole viewport is O(H *
// document) — on every event the app processes, since Draw runs on all of
// them. One replay fills the whole array instead, and it is reused until the
// text actually changes.
//
// The cache is keyed on the *Document as well as its version. Two Documents
// number their versions independently starting from zero, so the pointer is
// what stops a highlighter that gets handed a second document from answering
// with the first one's states — which would otherwise take a fresh document
// whose version happened to match and colour it as the old one.
type prefixStates[S comparable] struct {
	doc     *Document
	version uint64
	valid   bool
	states  []S
}

// at returns the state line idx begins in.
//
// zero is the state the first line starts in; step is the one-line
// transition — given a line and the state carried into it, the state carried
// out. step must be the same function the highlighter's own per-line scan
// agrees with, or a line's colour depends on which cache generation drew it,
// which reads as a flicker while scrolling.
func (c *prefixStates[S]) at(doc *Document, idx int, zero S, step func(line []rune, in S) S) S {
	switch {
	case !c.valid || c.doc != doc || len(c.states) != doc.Len():
		// The length test is belt-and-braces: only setLine leaves a non-zero
		// dirtyFrom and setLine cannot change the line count, so the resume
		// branch below is already unreachable for a document that grew or
		// shrank. It costs one comparison and removes the need to re-derive
		// that argument when either side changes.
		c.replay(doc, 0, zero, step, false)
	case c.version == doc.Version():
		// Nothing has changed since the last replay.
	case c.version+1 == doc.Version() && doc.dirtyFrom > 0:
		// Exactly one mutation since, and it was a single-line rewrite, so
		// every state at or before the line it touched is still correct.
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
// stopOnConverge ends the walk as soon as a recomputed state matches the one
// already stored: the carried state has rejoined the previous scan, so no
// later line can differ. That is what makes typing in a large script cost the
// lines whose meaning actually changed rather than the whole document —
// typing outside a comment converges on the very next line, and only an edit
// that opens or closes one propagates further.
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
