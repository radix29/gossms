package controls

import "github.com/radix29/gossms/internal/tuikit/core"

// ---------------------------------------------------------------------------
// Document — Editor's text buffer and the single chokepoint for mutating it
// ---------------------------------------------------------------------------

// Document is an Editor's text buffer: the lines themselves, plus a version
// counter that every mutation bumps.
//
// The counter exists so work proportional to the *document* can be done once
// per edit instead of once per Draw — and Draw runs on every event the app
// processes, keystrokes included. Three caches are keyed on it: the syntax
// highlighters' block-comment prefix states (sql_highlighter.go,
// xml_highlighter.go), Editor's wrap-mode visual-row flattening
// (buildVisualLines), and maxDisplayWidth below. Each of the three was
// previously an O(document) scan per Draw.
//
// That only works if the version can never be stale, which is why the
// buffer is reachable for writing through exactly two methods — setLine and
// edit — and nothing else. A mutation that reaches the lines any other way
// leaves every cache above rendering the *previous* document: stale colours,
// stale wrap segments, a stale scrollbar. That failure is silent and looks
// like a rendering glitch rather than a missed write, so it is worth the
// indirection. Line returns a slice the caller can write through; a caller
// that does must hand the result back to setLine.
type Document struct {
	lines   [][]rune
	version uint64

	// maxWidth caches the widest line's display width, which drives the
	// horizontal scrollbar and is asked for several times per Draw.
	// maxWidthValid distinguishes "not computed" from "0", which is the
	// correct answer for an empty buffer.
	//
	// lineW is the per-line half of that cache: entry i is line i's display
	// width, or -1 when unknown. It exists because measuring a line is now
	// O(its runes) rather than the O(1) len() a rune count was, so rebuilding
	// maxWidth from scratch would walk every rune in the buffer on every
	// keystroke — 10ms on a 10,000-line script, far worse than the per-Draw
	// cost the version counter was introduced to remove. setLine invalidates
	// one entry, so a keystroke re-measures one line and then scans a slice
	// of ints; only a structural edit (see edit) drops them all.
	maxWidth      int
	maxWidthValid bool
	lineW         []int

	// dirtyFrom is the lowest line index the most recent mutation could have
	// changed the meaning of: the edited line for setLine, 0 for anything
	// that moved lines around. A cache that is exactly one version behind can
	// resume from here instead of replaying the document — see prefixStates.
	// It describes one mutation only, so a cache further behind than that has
	// to start over.
	dirtyFrom int
}

// newDocument returns a Document holding a single empty line — the same
// non-empty invariant Editor relies on everywhere (len(lines) >= 1, so
// lines[cursorRow] is always indexable after clampCursor).
func newDocument() *Document {
	return new(Document{lines: [][]rune{{}}})
}

// Len returns the number of logical lines.
func (d *Document) Len() int { return len(d.lines) }

// Line returns logical line i. The slice aliases the buffer: a caller that
// modifies it in place must pass the result to setLine, or the version will
// not reflect the change.
func (d *Document) Line(i int) []rune { return d.lines[i] }

// Version returns a counter that changes on every mutation and never
// repeats for a given Document. Cache anything derived from the document's
// text against it, together with the *Document itself — two Documents number
// their versions independently, so a cache shared between them must
// distinguish which one it holds.
func (d *Document) Version() uint64 { return d.version }

// all returns the backing slice for read-only use — slicing, iteration,
// copying. Mutating it, or any line reachable from it, without going back
// through setLine or edit is exactly what this type exists to prevent.
func (d *Document) all() [][]rune { return d.lines }

// setLine replaces line i and bumps the version. The line count is unchanged,
// so only that line's cached width is dropped — this is the path typing takes,
// and re-measuring the whole buffer here is what made it expensive.
func (d *Document) setLine(i int, line []rune) {
	d.lines[i] = line
	if i < len(d.lineW) {
		d.lineW[i] = -1
	}
	d.version++
	d.maxWidthValid = false
	d.dirtyFrom = i
}

// setLines replaces the whole buffer and bumps the version.
func (d *Document) setLines(lines [][]rune) {
	d.lines = lines
	d.touch()
}

// edit hands fn the buffer, installs whatever it returns, and bumps the
// version — the general form, for a mutation that setLine and setLines
// can't express: an in-place reorder, or a rebuild that needs to read the
// old buffer while constructing the new one. fn may mutate in place and
// return the same slice.
func (d *Document) edit(fn func(lines [][]rune) [][]rune) {
	d.lines = fn(d.lines)
	d.touch()
}

// touch invalidates every version-keyed cache. Called by setLines and edit,
// which can both change the line count and so cannot keep any per-line
// width; setLine has its own narrower invalidation. Nothing else should
// reach past those to call this directly.
func (d *Document) touch() {
	d.version++
	d.maxWidthValid = false
	d.lineW = d.lineW[:0]
	d.dirtyFrom = 0
}

// maxDisplayWidth returns the display width of the widest line, measured
// over the whole buffer rather than the visible window: a horizontal
// scrollbar sized off only what's on screen would resize itself, and appear
// and vanish, as the editor scrolled vertically.
func (d *Document) maxDisplayWidth() int {
	if d.maxWidthValid {
		return d.maxWidth
	}
	if len(d.lineW) != len(d.lines) {
		d.lineW = append(d.lineW[:0], make([]int, len(d.lines))...)
		for i := range d.lineW {
			d.lineW[i] = -1
		}
	}
	longest := 0
	for i, line := range d.lines {
		if d.lineW[i] < 0 {
			d.lineW[i] = core.RunesWidth(line)
		}
		if d.lineW[i] > longest {
			longest = d.lineW[i]
		}
	}
	d.maxWidth, d.maxWidthValid = longest, true
	return longest
}
