package controls

import (
	"slices"

	"github.com/radix29/gossms/internal/tuikit/core"
)

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
// (buildVisualLines), and maxDisplayWidth below. Each would otherwise be an
// O(document) scan per Draw.
//
// That only works if the version can never be stale, which is why the
// buffer is reachable for writing through exactly three methods — setLine,
// edit and replaceRange — and nothing else. Each ends in a version bump and
// a cache invalidation of its own; setLines is edit's whole-buffer form, and
// setMeasuredLines is setLines with the width cache seeded. A
// new mutator belongs alongside them and must do the same. A mutation that
// reaches the lines any other way
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

	// dirtyTo bounds that range above: the line after the last one the
	// mutation touched, or -1 when the mutation could have changed every line
	// from dirtyFrom down. Only setLine gives it a bound, and that bound is
	// always dirtyFrom+1 — one line, same line count.
	//
	// prefixStates doesn't need it: a state replay carries forward and stops
	// where it converges, so an over-wide dirty range costs time, not
	// correctness. A cache indexed by line and spliced in place — the search
	// match list — does: replaceRange leaves dirtyFrom > 0 with the line
	// count unchanged whenever a span is replaced by one of equal length
	// (every same-size undo), and rescanning only dirtyFrom would then keep
	// stale entries for the rest of the span.
	dirtyTo int

	// splices records the reach of the most recent mutations, one slot per
	// version (slot version%len), for changedSince. It is the dirty range a
	// cache *more* than one version behind needs: Enter with auto-indent is a
	// split plus a setLine per space, all before the wrap cache next looks.
	splices [spliceLogLen]splice
}

// spliceLogLen bounds how far behind a cache can fall and still catch up
// through changedSince rather than rebuilding. A paste is one mutation per
// rune, so it outruns this and rebuilds once — the cost of any edit before
// the log existed.
const spliceLogLen = 64

// splice describes one mutation: lines [row, row+old) of the previous buffer
// became lines [row, row+new) of this one, and every other line is the same
// line — those after the span shifted by new-old. old < 0 means unknown: the
// mutation could have moved any line anywhere (edit, setLines).
type splice struct{ row, old, new int }

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
	d.dirtyFrom, d.dirtyTo = i, i+1
	d.logSplice(splice{row: i, old: 1, new: 1})
}

// setLines replaces the whole buffer and bumps the version.
func (d *Document) setLines(lines [][]rune) {
	d.lines = lines
	d.touch(0)
}

// setMeasuredLines is setLines for a buffer whose line widths are already
// known (LineBuffer), seeding the width cache instead of leaving it to the
// next Draw to rebuild. lineW is copied, not shared: touch truncates the
// cache in place and maxDisplayWidth appends into the same array, so a later
// mutation would otherwise overwrite the buffer's widths (clipping the slice
// does not help — the truncation keeps its capacity). A copy of a million
// ints is a few milliseconds; the measuring it replaces was 370.
func (d *Document) setMeasuredLines(lines [][]rune, lineW []int, maxW int) {
	d.setLines(lines)
	d.lineW = slices.Clone(lineW)
	d.maxWidth, d.maxWidthValid = maxW, true
}

// edit hands fn the buffer, installs whatever it returns, and bumps the
// version — the general form, for a mutation that setLine and setLines
// can't express: an in-place reorder, or a rebuild that needs to read the
// old buffer while constructing the new one. fn may mutate in place and
// return the same slice.
func (d *Document) edit(fn func(lines [][]rune) [][]rune) {
	d.lines = fn(d.lines)
	d.touch(0)
}

// replaceRange substitutes the n lines at row with the lines in with, which
// the Document takes ownership of. It is the general splice undo and redo are
// applied through: a step covers one contiguous span, and restoring it in a
// single mutation is what keeps the version counter moving once per undo
// rather than once per line. Editor's line-count-changing keystrokes (Enter,
// a joining Backspace or Delete, deleting a multi-line selection) go through
// it too rather than edit, so the caches resume at the span instead of
// starting over: in wrap mode, edit re-segments the whole document per Enter.
func (d *Document) replaceRange(row, n int, with [][]rune) {
	// slices.Replace, not a fresh buffer built by hand: an undo whose span is
	// the same length it replaces — the common one, since most edits don't
	// change the line count — then costs a copy of the span instead of a copy
	// of the whole document plus an allocation the size of it.
	d.lines = slices.Replace(d.lines, row, row+n, with...)
	// Not edit's touch(0): the lines before row are the same slices they were,
	// so their cached widths still hold. touch(0) drops the whole per-line cache,
	// so undoing a one-line edit in a 20,000-line script would re-measure every
	// rune in the buffer on the next Draw.
	d.touch(row)
	d.logSplice(splice{row: row, old: n, new: len(with)})
}

// touch invalidates every version-keyed cache from line `from` down. Called
// by setLines and edit with 0, since both can move any line anywhere, and by
// replaceRange with the first line of its span; setLine has its own narrower
// invalidation. Nothing else should reach past those to call this directly.
//
// `from` must be the lowest line the mutation could have changed the meaning
// of, never merely the lowest one whose *text* changed: the surviving widths
// and the resume in prefixStates.at both take it as a promise that every
// earlier line is untouched. It is therefore never past the end of the new
// buffer either — the lines before a splice survive it — which is why
// maxDisplayWidth only ever has to extend lineW. When in doubt, 0 is always
// correct.
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
// newN-oldN past the span). ok is false when that can't be known — v is more
// than spliceLogLen mutations old, or one of them was an edit or setLines —
// and the caller rebuilds from scratch. v == Version() is an empty span.
//
// Successive spans merge into their union, so two edits far apart report
// everything between them as changed: correct, merely wider than needed.
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
		// Union of the span so far ([row, row+newN) in current lines) and
		// s's ([s.row, s.row+s.old)), taken before s applies. Its end is at
		// or past the span's, where lines sit (newN-oldN) below their
		// position at v, which maps it back to v's numbering.
		lo := min(row, s.row)
		hi := max(row+newN, s.row+s.old)
		row, oldN, newN = lo, hi-(newN-oldN)-lo, hi+(s.new-s.old)-lo
	}
	return row, oldN, newN, true
}

// maxDisplayWidth returns the display width of the widest line, measured
// over the whole buffer rather than the visible window: a horizontal
// scrollbar sized off only what's on screen would resize itself, and appear
// and vanish, as the editor scrolled vertically.
func (d *Document) maxDisplayWidth() int {
	if d.maxWidthValid {
		return d.maxWidth
	}
	// Extend rather than rebuild: touch truncates to the first line its
	// mutation could have changed, so whatever entries are still here describe
	// lines that did not move, and only the tail is unknown. (The old form
	// allocated a whole []int per call purely to append it away.)
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
