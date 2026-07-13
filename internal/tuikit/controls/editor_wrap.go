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

// wrapSegments splits line into visual segments no wider than w runes,
// breaking after the last space at or before the width limit when one
// exists, otherwise hard-breaking exactly at the width limit (so a
// single word longer than w still visibly progresses instead of
// overflowing forever). Always returns at least one segment, even for
// an empty line, so every logical line occupies at least one visual row.
func wrapSegments(line []rune, w int) []wrapSegment {
	if w < 1 {
		w = 1
	}
	n := len(line)
	if n == 0 {
		return []wrapSegment{{0, 0}}
	}
	var segs []wrapSegment
	start := 0
	for start < n {
		end := start + w
		if end >= n {
			segs = append(segs, wrapSegment{start, n})
			break
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
		segs = append(segs, wrapSegment{start, breakAt})
		start = breakAt
	}
	return segs
}

// visualLine pairs a wrap segment with the logical line (index into
// e.lines) it belongs to.
type visualLine struct {
	row        int
	start, end int
}

// buildVisualLines flattens the whole document into wrap-mode visual
// rows at the given content width. Recomputed fresh on every call, not
// cached.
func (e *Editor) buildVisualLines(w int) []visualLine {
	var out []visualLine
	for li, line := range e.lines {
		for _, seg := range wrapSegments(line, w) {
			out = append(out, visualLine{row: li, start: seg.start, end: seg.end})
		}
	}
	return out
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
// mouse's Y position map to visual rows (buildVisualLines) rather than
// directly to logical lines.
func (e *Editor) handleMouseWrapped(ev *tcell.EventMouse, mx, my, contentX int) bool {
	contentW := e.rect.W - e.gutterWidth()
	vls := e.buildVisualLines(contentW)

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
