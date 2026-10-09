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

// selectWordAt selects the word under (row, col), a double-click's result (see
// HandleMouse). A position with no word (whitespace between words, past the end
// of an empty line) just places the cursor there.
func (e *Editor) selectWordAt(row, col int) {
	e.cursorRow, e.cursorCol = row, col
	e.selecting, e.selBlock = false, false
	start, end, ok := core.WordBoundsAt(e.doc.Line(row), col)
	if !ok {
		e.desiredCol = core.ColumnOfRune(e.doc.Line(row), col)
		return
	}
	e.selecting = true
	e.selAnchorRow, e.selAnchorCol = row, start
	e.cursorCol = end
	e.desiredCol = core.ColumnOfRune(e.doc.Line(row), end)
	e.ensureCursorVisible()
}

// selectionBounds returns the selection endpoints ordered start <= end in
// document order (anchor and cursor can be either way round).
func (e *Editor) selectionBounds() (startRow, startCol, endRow, endCol int) {
	ar, ac := e.selAnchorRow, e.selAnchorCol
	cr, cc := e.cursorRow, e.cursorCol
	if ar < cr || (ar == cr && ac <= cc) {
		return ar, ac, cr, cc
	}
	return cr, cc, ar, ac
}

// blockColumnBounds returns the [loCol, hiCol) column range shared by every row
// of a block (column) selection, ordered lo <= hi. Unlike selectionBounds's
// document-order pairing, a block's row and column orders are independent:
// anchor (5,3), cursor (2,10) spans rows 2-5 at columns 3-10, not "backwards".
func (e *Editor) blockColumnBounds() (loCol, hiCol int) {
	if e.selAnchorCol <= e.cursorCol {
		return e.selAnchorCol, e.cursorCol
	}
	return e.cursorCol, e.selAnchorCol
}

// selectionRangeForLine returns the selected [startCol, endCol) column range for
// lineIdx, and whether that line participates at all.
//
// Linear (stream) selection: for every line but the last of a multi-line
// selection, endCol is len(line)+1, the extra column representing the line break
// so the highlight reads as continuous.
//
// Block (column) selection: every affected row uses the same blockColumnBounds()
// range, clamped to the row's length; a row shorter than loCol yields an empty
// (start==end) range with no special case.
func (e *Editor) selectionRangeForLine(lineIdx int) (startCol, endCol int, ok bool) {
	if !e.HasSelection() {
		return 0, 0, false
	}
	if e.selBlock {
		topRow, botRow := min(e.selAnchorRow, e.cursorRow), max(e.selAnchorRow, e.cursorRow)
		if lineIdx < topRow || lineIdx > botRow {
			return 0, 0, false
		}
		loCol, hiCol := e.blockColumnBounds()
		n := len(e.doc.Line(lineIdx))
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
	end := len(e.doc.Line(lineIdx)) + 1
	if lineIdx == er {
		end = ec
	}
	return start, end, true
}

// SelectedText returns the selected text, or "" if none. For a block (column)
// selection each affected row's [loCol,hiCol) slice is joined with "\n", as for a
// linear multi-line selection. It also records a block selection's text in
// blockClip so a later Paste of the same text can put it back rectangularly;
// every copy path (Edit > Copy, Ctrl+C, the context menu) passes through here.
func (e *Editor) SelectedText() string {
	if !e.HasSelection() {
		return ""
	}
	if e.selBlock {
		topRow, botRow := min(e.selAnchorRow, e.cursorRow), max(e.selAnchorRow, e.cursorRow)
		loCol, hiCol := e.blockColumnBounds()
		if hiCol == loCol {
			// A zero-width block is the caret left behind by typing in column mode, not a
			// selection: it must not copy as a run of newlines.
			e.blockClip = ""
			return ""
		}
		var sb strings.Builder
		for r := topRow; r <= botRow; r++ {
			if r > topRow {
				sb.WriteByte('\n')
			}
			line := e.doc.Line(r)
			lo := core.Clamp(loCol, 0, len(line))
			hi := core.Clamp(hiCol, 0, len(line))
			sb.WriteString(string(line[lo:hi]))
		}
		e.blockClip = sb.String()
		return e.blockClip
	}
	e.blockClip = ""
	sr, sc, er, ec := e.selectionBounds()
	if sr == er {
		line := e.doc.Line(sr)
		sc = core.Clamp(sc, 0, len(line))
		ec = core.Clamp(ec, 0, len(line))
		return string(line[sc:ec])
	}
	var sb strings.Builder
	first := e.doc.Line(sr)
	sc = core.Clamp(sc, 0, len(first))
	sb.WriteString(string(first[sc:]))
	for r := sr + 1; r < er; r++ {
		sb.WriteByte('\n')
		sb.WriteString(string(e.doc.Line(r)))
	}
	sb.WriteByte('\n')
	last := e.doc.Line(er)
	ec = core.Clamp(ec, 0, len(last))
	sb.WriteString(string(last[:ec]))
	return sb.String()
}

// deleteSelection removes the selected text (if any) and moves the cursor to
// where the selection started. No-op without a selection. Callers wanting the
// deletion undoable call pushUndo() first; this doesn't, since every caller does
// so as part of a larger edit.
func (e *Editor) deleteSelection() {
	if !e.HasSelection() {
		return
	}
	if e.selBlock {
		topRow, botRow := min(e.selAnchorRow, e.cursorRow), max(e.selAnchorRow, e.cursorRow)
		loCol, hiCol := e.blockColumnBounds()
		for r := topRow; r <= botRow; r++ {
			line := e.doc.Line(r)
			lo := core.Clamp(loCol, 0, len(line))
			hi := core.Clamp(hiCol, 0, len(line))
			e.doc.setLine(r, append(line[:lo], line[hi:]...))
		}
		e.cursorRow, e.cursorCol = topRow, loCol
		e.selecting = false
		e.selBlock = false
		return
	}
	sr, sc, er, ec := e.selectionBounds()
	sc = core.Clamp(sc, 0, len(e.doc.Line(sr)))
	ec = core.Clamp(ec, 0, len(e.doc.Line(er)))
	if sr == er {
		line := e.doc.Line(sr)
		e.doc.setLine(sr, append(line[:sc], line[ec:]...))
	} else {
		first, last := e.doc.Line(sr), e.doc.Line(er)
		merged := make([]rune, 0, sc+(len(last)-ec))
		merged = append(merged, first[:sc]...)
		merged = append(merged, last[ec:]...)
		e.doc.replaceRange(sr, er-sr+1, [][]rune{merged})
	}
	e.cursorRow, e.cursorCol = sr, sc
	e.selecting = false
}

// Cut returns the selected text (like SelectedText) and removes it, pushing an
// undo step first: Ctrl+X's "copy then delete". Returns "" with nothing deleted
// and no undo step if there is no selection.
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

// Paste inserts text at the cursor, replacing the selection if any. Embedded
// newlines produce multiple lines, as typing them would.
//
// It bypasses the completion popup entirely: it closes it and never re-queries
// the provider. Pasted text is finished text; offering (let alone committing)
// IntelliSense candidates against the token the paste ends on is how pasted SQL
// gets silently rewritten.
//
// Text from this editor's own block (column) selection goes back in as a block,
// one line per row at the cursor's column (see blockPaste). A block copy is
// unmarked once it reaches the OS clipboard, so the only way to recognise it is
// the text matching blockClip.
func (e *Editor) Paste(text string) {
	if e.readOnly || text == "" {
		return
	}
	e.closeCompletion()
	e.pushUndo()
	if e.blockClip != "" && text == e.blockClip {
		e.blockPaste(text)
		e.clampCursor()
		e.ensureCursorVisible()
		return
	}
	if e.HasSelection() {
		e.deleteSelection()
	}
	lines := strings.Split(strings.ReplaceAll(e.expandTabs(text), "\r\n", "\n"), "\n")
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
