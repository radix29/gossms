package controls

import (
	"slices"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Document: Editor's text buffer and the single chokepoint for mutating it
// ---------------------------------------------------------------------------

// Document is an Editor's text buffer: the lines themselves, plus a version
// counter that every mutation bumps.
//
// The counter lets work proportional to the *document* be done once per edit
// instead of once per Draw (which runs on every event, keystrokes included).
// Three caches key on it: the syntax highlighters' block-comment prefix states
// (sql_highlighter.go, xml_highlighter.go), Editor's wrap-mode visual-row
// flattening (buildVisualLines), and maxDisplayWidth below.
//
// The version must never be stale, so the buffer is writable through exactly
// three methods: setLine, edit and replaceRange. Each ends in a version bump
// and cache invalidation; setLines is edit's whole-buffer form, and
// setMeasuredLines is setLines with the width cache seeded. A new mutator
// belongs alongside them and must do the same. A mutation reaching the lines
// any other way leaves every cache rendering the *previous* document (stale
// colours, wrap segments, scrollbar), a silent failure that looks like a
// rendering glitch. Line returns a slice the caller can write through; a caller
// that does must hand the result back to setLine.
type Document struct {
	lines   [][]rune
	version uint64

	// maxWidth caches the widest line's display width, which drives the horizontal
	// scrollbar and is asked for several times per Draw. maxWidthValid
	// distinguishes "not computed" from "0", the correct answer for an empty
	// buffer.
	//
	// lineW is the per-line half of that cache: entry i is line i's display width,
	// or -1 when unknown. Measuring a line is O(its runes), so rebuilding maxWidth
	// from scratch would walk every rune on every keystroke (10ms on a 10,000-line
	// script). setLine invalidates one entry, so a keystroke re-measures one line
	// and scans a slice of ints; only a structural edit (see edit) drops them all.
	maxWidth      int
	maxWidthValid bool
	lineW         []int

	// dirtyFrom is the lowest line index the most recent mutation could have
	// changed the meaning of: the edited line for setLine, 0 for anything that
	// moved lines. A cache exactly one version behind can resume from here instead
	// of replaying the document (see prefixStates). It describes one mutation
	// only; a cache further behind has to start over.
	dirtyFrom int

	// dirtyTo bounds that range above: the line after the last one the mutation
	// touched, or -1 when it could have changed every line from dirtyFrom down.
	// Only setLine gives a bound, always dirtyFrom+1 (one line, same line count).
	//
	// prefixStates doesn't need it: a state replay stops where it converges, so an
	// over-wide range costs time, not correctness. A cache indexed by line and
	// spliced in place (the search match list) does: replaceRange leaves
	// dirtyFrom > 0 with the line count unchanged whenever a span is replaced by
	// one of equal length (every same-size undo), and rescanning only dirtyFrom
	// would keep stale entries for the rest of the span.
	dirtyTo int

	// splices records the reach of the most recent mutations, one slot per version
	// (slot version%len), for changedSince: the dirty range a cache *more* than one
	// version behind needs (Enter with auto-indent is a split plus a setLine per
	// space, all before the wrap cache next looks).
	splices [spliceLogLen]splice
}

// spliceLogLen bounds how far behind a cache can fall and still catch up
// through changedSince rather than rebuilding. A paste is one mutation per
// rune, so it outruns this and rebuilds once.
const spliceLogLen = 64

// splice describes one mutation: lines [row, row+old) of the previous buffer
// became lines [row, row+new) of this one, and every other line is the same
// (those after the span shifted by new-old). old < 0 means unknown: the
// mutation could have moved any line anywhere (edit, setLines).
type splice struct{ row, old, new int }

// newDocument returns a Document holding a single empty line: the non-empty
// invariant Editor relies on (len(lines) >= 1, so lines[cursorRow] is always
// indexable after clampCursor).
func newDocument() *Document {
	return new(Document{lines: [][]rune{{}}})
}

// Len returns the number of logical lines.
func (d *Document) Len() int { return len(d.lines) }

// Line returns logical line i. The slice aliases the buffer: a caller that
// modifies it in place must pass the result to setLine, or the version won't
// reflect the change.
func (d *Document) Line(i int) []rune { return d.lines[i] }

// Version returns a counter that changes on every mutation and never repeats
// for a given Document. Cache anything derived from the text against it
// together with the *Document itself: two Documents number versions
// independently.
func (d *Document) Version() uint64 { return d.version }

// all returns the backing slice for read-only use. Mutating it, or any line
// reachable from it, without going through setLine or edit is what this type
// exists to prevent.
func (d *Document) all() [][]rune { return d.lines }

// setLine replaces line i and bumps the version. The line count is unchanged,
// so only that line's cached width is dropped; this is the path typing takes,
// and re-measuring the whole buffer here was expensive.
func (d *Document) setLine(i int, line []rune) {
	d.lines[i] = line
	if i < len(d.lineW) {
		d.lineW[i] = -1
	}
	d.version++
	d.maxWidthValid = false
	d.dirtyFrom, d.dirtyTo = i, i+1
	d.logSplice(splice{row: i, old: 1, new: 1})
}

// setLines replaces the whole buffer and bumps the version.
func (d *Document) setLines(lines [][]rune) {
	d.lines = lines
	d.touch(0)
}

// setMeasuredLines is setLines for a buffer whose line widths are already known
// (LineBuffer), seeding the width cache rather than leaving it to the next
// Draw. lineW is copied, not shared: touch truncates the cache in place and
// maxDisplayWidth appends into the same array, so a later mutation would
// overwrite the buffer's widths (clipping the slice doesn't help; truncation
// keeps capacity). A copy of a million ints is a few ms; the measuring it
// replaces was 370.
func (d *Document) setMeasuredLines(lines [][]rune, lineW []int, maxW int) {
	d.setLines(lines)
	d.lineW = slices.Clone(lineW)
	d.maxWidth, d.maxWidthValid = maxW, true
}

// edit hands fn the buffer, installs whatever it returns, and bumps the
// version: the general form for a mutation setLine and setLines can't express
// (an in-place reorder, or a rebuild that reads the old buffer). fn may mutate
// in place and return the same slice.
func (d *Document) edit(fn func(lines [][]rune) [][]rune) {
	d.lines = fn(d.lines)
	d.touch(0)
}

// replaceRange substitutes the n lines at row with the lines in with, which the
// Document takes ownership of. It is the general splice undo and redo apply
// through: a step covers one contiguous span, and restoring it in one mutation
// keeps the version moving once per undo rather than once per line. Editor's
// line-count-changing keystrokes (Enter, a joining Backspace or Delete,
// deleting a multi-line selection) use it too rather than edit, so caches
// resume at the span (in wrap mode edit re-segments the whole document per
// Enter).
func (d *Document) replaceRange(row, n int, with [][]rune) {
	// slices.Replace, not a hand-built fresh buffer: an undo over a same-length
	// span (the common case) then costs a copy of the span, not of the whole
	// document plus an allocation the size of it.
	d.lines = slices.Replace(d.lines, row, row+n, with...)
	// Not edit's touch(0): the lines before row are the same slices, so their
	// cached widths hold. touch(0) drops the whole per-line cache, so undoing a
	// one-line edit in a 20,000-line script would re-measure every rune on the next
	// Draw.
	d.touch(row)
	d.logSplice(splice{row: row, old: n, new: len(with)})
}

// touch invalidates every version-keyed cache from line `from` down. Called by
// setLines and edit with 0 (both can move any line anywhere) and by
// replaceRange with the first line of its span; setLine has its own narrower
// invalidation. Nothing else should call this directly.
//
// `from` must be the lowest line the mutation could have changed the meaning
// of, not merely the lowest whose *text* changed: the surviving widths and the
// resume in prefixStates.at take it as a promise that every earlier line is
// untouched. It is never past the end of the new buffer (lines before a splice
// survive it), so maxDisplayWidth only ever extends lineW. When in doubt, 0 is
// always correct.
func (d *Document) touch(from int) {
	d.version++
	d.maxWidthValid = false
	if from < len(d.lineW) {
		d.lineW = d.lineW[:from]
	}
	d.dirtyFrom, d.dirtyTo = from, -1
	d.logSplice(splice{old: -1})
}

// logSplice records the current version's mutation for changedSince. touch
// logs "unknown" first and replaceRange overwrites it with its exact span.
func (d *Document) logSplice(s splice) {
	d.splices[d.version%spliceLogLen] = s
}

// changedSince returns one span covering every mutation after version v, in
// splice's terms: lines [row, row+oldN) of the buffer at v are lines
// [row, row+newN) now, and every line outside them is unchanged (shifted by
// newN-oldN past the span). ok is false when that can't be known (v is more
// than spliceLogLen mutations old, or one was an edit or setLines) and the
// caller rebuilds. v == Version() is an empty span.
//
// Successive spans merge into their union, so two far-apart edits report
// everything between as changed: correct, merely wider than needed.
func (d *Document) changedSince(v uint64) (row, oldN, newN int, ok bool) {
	if v > d.version || d.version-v > spliceLogLen {
		return 0, 0, 0, false
	}
	for ver := v + 1; ver <= d.version; ver++ {
		s := d.splices[ver%spliceLogLen]
		if s.old < 0 {
			return 0, 0, 0, false
		}
		if ver == v+1 {
			row, oldN, newN = s.row, s.old, s.new
			continue
		}
		// Union of the span so far ([row, row+newN) in current lines) and s's
		// ([s.row, s.row+s.old)), taken before s applies. Its end is at or past the
		// span's, where lines sit (newN-oldN) below their position at v, which maps it
		// back to v's numbering.
		lo := min(row, s.row)
		hi := max(row+newN, s.row+s.old)
		row, oldN, newN = lo, hi-(newN-oldN)-lo, hi+(s.new-s.old)-lo
	}
	return row, oldN, newN, true
}

// maxDisplayWidth returns the display width of the widest line, measured over
// the whole buffer, not the visible window: a horizontal scrollbar sized off
// the screen would resize and appear/vanish as the editor scrolled vertically.
func (d *Document) maxDisplayWidth() int {
	if d.maxWidthValid {
		return d.maxWidth
	}
	// Extend rather than rebuild: touch truncates to the first line its mutation
	// could have changed, so remaining entries describe lines that did not move and
	// only the tail is unknown.
	for len(d.lineW) < len(d.lines) {
		d.lineW = append(d.lineW, -1)
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
