package controls

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Selection and clipboard (Cut/Paste) for Editor
// ---------------------------------------------------------------------------

// HasSelection reports whether there is a non-empty active selection.
func (e *Editor) HasSelection() bool {
	if !e.selecting {
		return false
	}
	return e.selAnchorRow != e.cursorRow || e.selAnchorCol != e.cursorCol
}

// ClearSelection drops any active selection without affecting the cursor.
func (e *Editor) ClearSelection() { e.selecting = false }

// selectionBounds returns the selection endpoints ordered so the start is
// always at or before the end in document order (anchor and cursor can be
// in either order depending on which direction the user selected in).
func (e *Editor) selectionBounds() (startRow, startCol, endRow, endCol int) {
	ar, ac := e.selAnchorRow, e.selAnchorCol
	cr, cc := e.cursorRow, e.cursorCol
	if ar < cr || (ar == cr && ac <= cc) {
		return ar, ac, cr, cc
	}
	return cr, cc, ar, ac
}

// blockColumnBounds returns the [loCol, hiCol) column range shared by every
// row of a block (column) selection, ordered lo <= hi regardless of which
// of selAnchorCol/cursorCol is numerically smaller. Unlike
// selectionBounds's (row,col) document-order pairing, a block selection's
// row order and column order are independent — e.g. anchor (5,3), cursor
// (2,10) is a selection spanning rows 2-5 at columns 3-10, not "backwards".
func (e *Editor) blockColumnBounds() (loCol, hiCol int) {
	if e.selAnchorCol <= e.cursorCol {
		return e.selAnchorCol, e.cursorCol
	}
	return e.cursorCol, e.selAnchorCol
}

// selectionRangeForLine returns the selected [startCol, endCol) column
// range for lineIdx, and whether that line participates in the selection
// at all.
//
// Linear (stream) selection: for every line except the last one in a
// multi-line selection, endCol is len(line)+1 — the extra column
// represents the line break itself, so the highlighted selection reads as
// continuous across lines.
//
// Block (column) selection: every affected row uses the same
// blockColumnBounds() range, clamped to that row's own length — a row
// shorter than loCol naturally contributes an empty (start==end) range,
// with no special case needed.
func (e *Editor) selectionRangeForLine(lineIdx int) (startCol, endCol int, ok bool) {
	if !e.HasSelection() {
		return 0, 0, false
	}
	if e.selBlock {
		topRow, botRow := core.Min(e.selAnchorRow, e.cursorRow), core.Max(e.selAnchorRow, e.cursorRow)
		if lineIdx < topRow || lineIdx > botRow {
			return 0, 0, false
		}
		loCol, hiCol := e.blockColumnBounds()
		n := len(e.lines[lineIdx])
		return core.Clamp(loCol, 0, n), core.Clamp(hiCol, 0, n), true
	}
	sr, sc, er, ec := e.selectionBounds()
	if lineIdx < sr || lineIdx > er {
		return 0, 0, false
	}
	start := 0
	if lineIdx == sr {
		start = sc
	}
	end := len(e.lines[lineIdx]) + 1
	if lineIdx == er {
		end = ec
	}
	return start, end, true
}

// SelectedText returns the currently selected text, or "" if there is no
// selection. For a block (column) selection, each affected row's
// [loCol,hiCol) slice is joined with "\n", same join convention as a
// linear multi-line selection.
func (e *Editor) SelectedText() string {
	if !e.HasSelection() {
		return ""
	}
	if e.selBlock {
		topRow, botRow := core.Min(e.selAnchorRow, e.cursorRow), core.Max(e.selAnchorRow, e.cursorRow)
		loCol, hiCol := e.blockColumnBounds()
		var sb strings.Builder
		for r := topRow; r <= botRow; r++ {
			if r > topRow {
				sb.WriteByte('\n')
			}
			line := e.lines[r]
			lo := core.Clamp(loCol, 0, len(line))
			hi := core.Clamp(hiCol, 0, len(line))
			sb.WriteString(string(line[lo:hi]))
		}
		return sb.String()
	}
	sr, sc, er, ec := e.selectionBounds()
	if sr == er {
		line := e.lines[sr]
		sc = core.Clamp(sc, 0, len(line))
		ec = core.Clamp(ec, 0, len(line))
		return string(line[sc:ec])
	}
	var sb strings.Builder
	first := e.lines[sr]
	sc = core.Clamp(sc, 0, len(first))
	sb.WriteString(string(first[sc:]))
	for r := sr + 1; r < er; r++ {
		sb.WriteByte('\n')
		sb.WriteString(string(e.lines[r]))
	}
	sb.WriteByte('\n')
	last := e.lines[er]
	ec = core.Clamp(ec, 0, len(last))
	sb.WriteString(string(last[:ec]))
	return sb.String()
}

// deleteSelection removes the currently selected text (if any) and moves
// the cursor to where the selection started. No-op if there is no
// selection. Callers that want the deletion to be undoable should call
// pushUndo() themselves before calling this — it does not push its own,
// since every caller so far already does so as part of a larger edit.
func (e *Editor) deleteSelection() {
	if !e.HasSelection() {
		return
	}
	if e.selBlock {
		topRow, botRow := core.Min(e.selAnchorRow, e.cursorRow), core.Max(e.selAnchorRow, e.cursorRow)
		loCol, hiCol := e.blockColumnBounds()
		for r := topRow; r <= botRow; r++ {
			line := e.lines[r]
			lo := core.Clamp(loCol, 0, len(line))
			hi := core.Clamp(hiCol, 0, len(line))
			e.lines[r] = append(line[:lo], line[hi:]...)
		}
		e.cursorRow, e.cursorCol = topRow, loCol
		e.selecting = false
		e.selBlock = false
		return
	}
	sr, sc, er, ec := e.selectionBounds()
	sc = core.Clamp(sc, 0, len(e.lines[sr]))
	ec = core.Clamp(ec, 0, len(e.lines[er]))
	if sr == er {
		line := e.lines[sr]
		e.lines[sr] = append(line[:sc], line[ec:]...)
	} else {
		first := e.lines[sr]
		last := e.lines[er]
		merged := make([]rune, 0, sc+(len(last)-ec))
		merged = append(merged, first[:sc]...)
		merged = append(merged, last[ec:]...)
		newLines := make([][]rune, 0, len(e.lines)-(er-sr))
		newLines = append(newLines, e.lines[:sr]...)
		newLines = append(newLines, merged)
		newLines = append(newLines, e.lines[er+1:]...)
		e.lines = newLines
	}
	e.cursorRow, e.cursorCol = sr, sc
	e.selecting = false
}

// Cut returns the currently selected text (like SelectedText) and removes
// it, pushing an undo step first — the combined "copy then delete"
// operation Ctrl+X performs. Returns "" if there is no selection, in
// which case nothing is deleted and no undo step is pushed.
func (e *Editor) Cut() string {
	if e.readOnly || !e.HasSelection() {
		return ""
	}
	text := e.SelectedText()
	e.pushUndo()
	e.deleteSelection()
	e.clampCursor()
	e.ensureCursorVisible()
	return text
}

// Paste inserts text at the cursor, replacing the current selection if
// there is one — the behaviour expected of a clipboard paste. Embedded
// newlines in text produce multiple lines, same as typing them would.
func (e *Editor) Paste(text string) {
	if e.readOnly || text == "" {
		return
	}
	e.pushUndo()
	if e.HasSelection() {
		e.deleteSelection()
	}
	lines := strings.Split(strings.ReplaceAll(expandTabs(text), "\r\n", "\n"), "\n")
	for i, line := range lines {
		if i > 0 {
			e.insertNewline()
		}
		for _, r := range line {
			e.insertRune(r)
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}
