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
// segment wider than w runes, breaking after the last space at or before the
// width limit when one exists, otherwise hard-breaking exactly at the width
// limit (so a single word longer than w still visibly progresses instead of
// overflowing forever). Always appends at least one segment, even for an
// empty line, so every logical line occupies at least one visual row.
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
		end := start + w
		if end >= n {
			return append(dst, wrapSegment{start, n})
		}
		breakAt := end
		lastSpace := -1
		for i := start; i < end; i++ {
			if line[i] == ' ' || line[i] == '\t' {
				lastSpace = i
			}
		}
		if lastSpace >= start {
			breakAt = lastSpace + 1
		}
		dst = append(dst, wrapSegment{start, breakAt})
		start = breakAt
	}
	return dst
}

// visualLine pairs a wrap segment with the logical line (index into
// e.lines) it belongs to.
type visualLine struct {
	row        int
	start, end int
}

// buildVisualLines flattens the whole document into wrap-mode visual rows at
// the given content width. Recomputed fresh on every call — the result is
// never cached across calls, only the buffers it is built in are.
//
// Two buffers, both reused: vlScratch holds the result, segScratch the
// per-line segments wrapSegments appends into. Draw calls this once per
// pass, HandleMouse twice more per wrap-mode event, and Draw runs on every
// event the app processes — so building it from a nil slice meant regrowing
// two slices from scratch, per event, over the *whole* document however
// little of it is on screen. That is bounded by the document, not the
// viewport: DataGrid's cell viewer (the wrap-mode call site that shows query
// data) opens on a varchar(max)/XML value, where one logical line is
// thousands of segments and only ~15 rows are visible.
//
// The returned slice aliases vlScratch and is invalidated by the next call.
// Nothing may retain it — every caller uses it and drops it within the same
// Draw or HandleMouse.
func (e *Editor) buildVisualLines(w int) []visualLine {
	e.vlScratch = e.vlScratch[:0]
	for li, line := range e.lines {
		e.segScratch = wrapSegments(e.segScratch[:0], line, w)
		for _, seg := range e.segScratch {
			e.vlScratch = append(e.vlScratch, visualLine{row: li, start: seg.start, end: seg.end})
		}
	}
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

// handleMouseWrapped implements HandleMouse's Button1-click/drag and
// wheel-scroll behavior for word-wrap mode, where scrollRow and the
// mouse's Y position map to visual rows (vls, from buildVisualLines)
// rather than directly to logical lines. vls is precomputed by the caller
// (HandleMouse) rather than recomputed here, since it's already needed
// there for the scrollbar hit-test on Button1 events.
func (e *Editor) handleMouseWrapped(ev *tcell.EventMouse, mx, my, contentX int, vls []visualLine) bool {
	if ev.Buttons() == tcell.Button1 {
		vi := core.Clamp(e.scrollRow+(my-e.rect.Y), 0, len(vls)-1)
		vl := vls[vi]
		row := vl.row
		col := vl.start + core.Max(0, mx-contentX)
		if col > vl.end {
			col = vl.end
		}
		if !e.mouseDragging {
			// Fresh click: reposition the cursor. Without Shift, arm a new
			// selection anchor here (HasSelection() stays false until the
			// drag moves away from this point). With Shift, extend the
			// existing selection instead — see the identical Shift+Click
			// handling in HandleMouse (editor_input.go) for the rationale.
			e.mouseDragging = true
			if ev.Modifiers()&tcell.ModShift != 0 {
				if !e.selecting {
					e.selAnchorRow, e.selAnchorCol = e.cursorRow, e.cursorCol
				}
			} else {
				e.selAnchorRow, e.selAnchorCol = row, col
			}
			e.selecting = true
			e.cursorRow, e.cursorCol = row, col
		} else {
			// Continued drag: move the cursor, anchor stays fixed.
			e.cursorRow, e.cursorCol = row, col
		}
		e.desiredCol = col
		return true
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
