package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Word-wrap mode for Editor: soft-wrap segmentation, visual-row/cursor
// mapping, and wrap-mode mouse handling
// ---------------------------------------------------------------------------

// wrapSegment is one soft-wrapped visual row: the [start,end) rune range
// of a logical line that fits within the wrap width.
type wrapSegment struct {
	start, end int
}

// wrapSegments appends line's visual segments to dst and returns it: no
// segment wider than w *terminal columns*, breaking after the last space at
// or before the width limit when one exists, otherwise hard-breaking at the
// last rune that fits (so a single word longer than w still visibly
// progresses instead of overflowing forever). Always appends at least one
// segment, even for an empty line, so every logical line occupies at least
// one visual row.
//
// Columns, not rune counts: a segment of CJK text holds half as many runes
// as the same number of ASCII ones, and measuring it in runes overflowed
// every wrapped row of such a line past the right edge.
//
// It appends rather than returning a fresh slice so buildVisualLines can
// build the whole document into one reused buffer — see Editor.vlScratch.
func wrapSegments(dst []wrapSegment, line []rune, w int) []wrapSegment {
	if w < 1 {
		w = 1
	}
	n := len(line)
	if n == 0 {
		return append(dst, wrapSegment{0, 0})
	}
	start := 0
	for start < n {
		// Widest prefix of line[start:] that fits in w columns, remembering
		// the last space inside it to break after.
		end, width, lastSpace := start, 0, -1
		for end < n {
			rw := core.RuneWidth(line[end])
			if width+rw > w {
				break
			}
			if line[end] == ' ' || line[end] == '\t' {
				lastSpace = end
			}
			width += rw
			end++
		}
		if end >= n {
			return append(dst, wrapSegment{start, n})
		}
		breakAt := end
		if lastSpace >= start {
			breakAt = lastSpace + 1
		}
		if breakAt == start {
			// A single rune wider than the whole wrap width (a wide rune in
			// a one-column area). Emit it alone rather than looping forever
			// on a zero-length segment; it overflows by one column, which
			// beats hanging.
			breakAt = start + 1
		}
		dst = append(dst, wrapSegment{start, breakAt})
		start = breakAt
	}
	return dst
}

// visualLine pairs a wrap segment with the logical line (index into the
// document) it belongs to.
type visualLine struct {
	row        int
	start, end int
}

// buildVisualLines flattens the whole document into wrap-mode visual rows at
// the given content width, and memoises the result against the document's
// version and that width.
//
// The memo is the point. Draw calls this once per pass, HandleMouse twice
// more per wrap-mode event, and Draw runs on *every* event the app processes —
// so without it the whole document is re-segmented several times per keystroke,
// mouse-move tick and timer tick, however little of it is on screen. That cost
// is bounded by the document, not the viewport, and wrap mode's large call site
// is DataGrid's cell viewer, where one logical line of a varchar(max)/XML value
// is thousands of segments behind a ~15-row window. That viewer is read-only,
// so its version never moves and it segments exactly once.
//
// Correctness rests entirely on Document.Version() being impossible to leave
// stale — see Document. A mutation that bypassed setLine/edit would leave
// this returning segments for the previous text, which renders as the old
// document with the new document's cursor in it.
//
// The returned slice aliases vlScratch and stays valid until the document or
// the width changes. Every caller uses it within one Draw or HandleMouse
// anyway; none may retain it across an edit.
func (e *Editor) buildVisualLines(w int) []visualLine {
	if e.vlCacheValid && e.vlCacheWidth == w && e.vlCacheVersion == e.doc.Version() {
		return e.vlScratch
	}
	e.vlScratch = e.vlScratch[:0]
	for li, line := range e.doc.all() {
		e.segScratch = wrapSegments(e.segScratch[:0], line, w)
		for _, seg := range e.segScratch {
			e.vlScratch = append(e.vlScratch, visualLine{row: li, start: seg.start, end: seg.end})
		}
	}
	e.vlCacheValid, e.vlCacheWidth, e.vlCacheVersion = true, w, e.doc.Version()
	return e.vlScratch
}

// visualIndexForCursor returns the index into vls (from buildVisualLines)
// of the visual row containing the cursor. A cursor sitting exactly at a
// wrap boundary is placed at the start of the next visual row — matching
// where a user would expect to see it appear after typing past the wrap
// point — except at the true end of a logical line, where there's no
// next row, so it stays at the end of the last one.
func visualIndexForCursor(vls []visualLine, row, col int) int {
	for i, vl := range vls {
		if vl.row != row {
			continue
		}
		lastOfLine := i == len(vls)-1 || vls[i+1].row != row
		if col >= vl.start && (col < vl.end || (lastOfLine && col == vl.end)) {
			return i
		}
	}
	if len(vls) == 0 {
		return 0
	}
	return len(vls) - 1
}

// wrapGoal is the goal x for wrap-mode vertical movement, a display column
// relative to the start of the visual row, with the cursor position and
// document version it was left at. It is sticky only while nothing else has
// moved the cursor: moveVisualRows reuses x when the cursor is still exactly
// where the last vertical move put it, so no other cursor-moving path has to
// remember to reset it (desiredCol's approach, with its dozen setters).
type wrapGoal struct {
	row, col, x int
	version     uint64
	set         bool
}

// moveVisualRows moves the cursor delta visual rows (negative is up) in wrap
// mode, aiming for the same on-screen column — Up/Down/PgUp/PgDn's wrap-mode
// behaviour. Moving by logical lines instead jumped over every continuation row
// of a wrapped line. The goal column survives a pass over a shorter row (see
// wrapGoal), as desiredCol does outside wrap mode.
//
// A non-final row of a wrapped line never takes the caret at its end: that
// index belongs to the next row (visualIndexForCursor), so landing there would
// show the caret one row further than the key moved it.
func (e *Editor) moveVisualRows(delta int) {
	vls := e.buildVisualLines(e.rect.W - e.gutterWidth())
	if len(vls) == 0 {
		return
	}
	vi := visualIndexForCursor(vls, e.cursorRow, e.cursorCol)
	cur := vls[vi]
	line := e.doc.Line(cur.row)
	x := core.ColumnOfRune(line, e.cursorCol) - core.ColumnOfRune(line, cur.start)
	g := e.wrapGoal
	if g.set && g.row == e.cursorRow && g.col == e.cursorCol && g.version == e.doc.Version() {
		x = g.x
	}

	ti := core.Clamp(vi+delta, 0, len(vls)-1)
	if ti == vi {
		// Up on the first row, Down on the last: as outside wrap mode, the
		// caret stays put rather than snapping to a remembered goal column.
		return
	}
	t := vls[ti]
	tl := e.doc.Line(t.row)
	col := t.start + core.RuneIndexAtColumn(tl[t.start:t.end], x)
	if ti < len(vls)-1 && vls[ti+1].row == t.row {
		col = min(col, max(t.start, t.end-1))
	} else {
		col = min(col, t.end)
	}
	e.cursorRow, e.cursorCol = t.row, col
	e.wrapGoal = wrapGoal{row: t.row, col: col, x: x, version: e.doc.Version(), set: true}
}

// wrappedPosAt maps screen (mx, my) to the document position under it in wrap
// mode, where my picks a visual row of vls rather than a logical line. Shared by
// handleMouseWrapped and SetCursorFromScreen.
func (e *Editor) wrappedPosAt(vls []visualLine, mx, my, contentX int) (row, col int) {
	vl := vls[core.Clamp(e.scrollRow+(my-e.rect.Y), 0, len(vls)-1)]
	// The click's x is a terminal column within the segment; converting it
	// back to a rune index is what stops a wide character earlier in the
	// segment from putting the caret in the wrong place.
	line := e.doc.Line(vl.row)
	col = vl.start + core.RuneIndexAtColumn(line[vl.start:vl.end], max(0, mx-contentX))
	return vl.row, min(col, vl.end)
}

// handleMouseWrapped implements HandleMouse's Button1-click/drag and
// wheel-scroll behavior for word-wrap mode, where scrollRow and the
// mouse's Y position map to visual rows (vls, from buildVisualLines)
// rather than directly to logical lines. vls is precomputed by the caller
// (HandleMouse) rather than recomputed here, since it's already needed
// there for the scrollbar hit-test on Button1 events. Once (row, col) is
// derived, the press goes to applyMousePress — the body shared with
// HandleMouse's unwrapped branch.
func (e *Editor) handleMouseWrapped(ev *tcell.EventMouse, mx, my, contentX int, vls []visualLine) bool {
	if ev.Buttons() == tcell.Button1 {
		row, col := e.wrappedPosAt(vls, mx, my, contentX)
		return e.applyMousePress(row, col, ev)
	}
	if ev.Buttons() == tcell.WheelUp && e.scrollRow > 0 {
		e.scrollRow--
		return true
	}
	if ev.Buttons() == tcell.WheelDown && e.scrollRow < len(vls)-1 {
		e.scrollRow++
		return true
	}
	return false
}
