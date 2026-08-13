package controls

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Block (column) editing for Editor — typing, deleting and pasting across
// every row of a rectangular selection at once, Notepad++ style
// ---------------------------------------------------------------------------

// blockEditing reports whether an edit should apply to every row of a block
// (column) selection rather than to the cursor's row alone.
//
// It stays true after a zero-width block — one whose two columns are equal,
// which is what typing in column mode leaves behind — so a run of typed
// characters keeps going into every row instead of the first character
// collapsing the block and the rest landing on one line. HasSelection is not
// enough on its own for exactly that reason: it reports false once anchor and
// cursor coincide entirely.
func (e *Editor) blockEditing() bool { return e.selecting && e.selBlock }

// blockRows returns the block selection's row range, ordered.
func (e *Editor) blockRows() (top, bot int) {
	return min(e.selAnchorRow, e.cursorRow), max(e.selAnchorRow, e.cursorRow)
}

// setBlockCaret re-arms the block over rows top..bot as a zero-width column
// at col — the state every block edit ends in, so the next typed character
// continues down the same column.
func (e *Editor) setBlockCaret(top, bot, col int) {
	e.selecting, e.selBlock = true, true
	e.selAnchorRow, e.selAnchorCol = top, col
	e.cursorRow, e.cursorCol = bot, col
}

// blockPadLine returns row's line padded with spaces to at least col runes,
// writing the padded line back to the document. A block edit at a column past
// a short row's end has to insert *at that column* to stay rectangular, which
// means the gap has to be made of something — matching SSMS's and Notepad++'s
// column-mode behaviour of filling it with spaces.
func (e *Editor) blockPadLine(row, col int) []rune {
	line := e.doc.Line(row)
	if len(line) >= col {
		return line
	}
	nl := make([]rune, col)
	copy(nl, line)
	for i := len(line); i < col; i++ {
		nl[i] = ' '
	}
	e.doc.setLine(row, nl)
	return nl
}

// blockCollapse removes the block selection's contents (a no-op for a
// zero-width block), leaving a zero-width block at its left column, and
// returns that column.
func (e *Editor) blockCollapse() int {
	top, bot := e.blockRows()
	loCol, hiCol := e.blockColumnBounds()
	if hiCol > loCol {
		for row := top; row <= bot; row++ {
			line := e.doc.Line(row)
			lo := core.Clamp(loCol, 0, len(line))
			hi := core.Clamp(hiCol, 0, len(line))
			e.doc.setLine(row, append(line[:lo], line[hi:]...))
		}
	}
	e.setBlockCaret(top, bot, loCol)
	return loCol
}

// blockInsertRune replaces the block's contents with r on every row it spans,
// then leaves the block armed one column to the right.
func (e *Editor) blockInsertRune(r rune) {
	top, bot := e.blockRows()
	col := e.blockCollapse()
	for row := top; row <= bot; row++ {
		line := e.blockPadLine(row, col)
		nl := make([]rune, len(line)+1)
		copy(nl, line[:col])
		nl[col] = r
		copy(nl[col+1:], line[col:])
		e.doc.setLine(row, nl)
	}
	e.setBlockCaret(top, bot, col+1)
}

// blockBackspace deletes the block's contents, or — when the block is
// zero-width — the rune to its left on every row it spans. Rows too short to
// have a rune at that column are left alone rather than padded: there is
// nothing there to delete.
func (e *Editor) blockBackspace() {
	top, bot := e.blockRows()
	loCol, hiCol := e.blockColumnBounds()
	if hiCol > loCol {
		e.blockCollapse()
		return
	}
	if loCol == 0 {
		return
	}
	for row := top; row <= bot; row++ {
		line := e.doc.Line(row)
		if len(line) < loCol {
			continue
		}
		e.doc.setLine(row, append(line[:loCol-1], line[loCol:]...))
	}
	e.setBlockCaret(top, bot, loCol-1)
}

// blockDelete deletes the block's contents, or — when the block is
// zero-width — the rune at its column on every row it spans.
func (e *Editor) blockDelete() {
	top, bot := e.blockRows()
	loCol, hiCol := e.blockColumnBounds()
	if hiCol > loCol {
		e.blockCollapse()
		return
	}
	for row := top; row <= bot; row++ {
		line := e.doc.Line(row)
		if loCol >= len(line) {
			continue
		}
		e.doc.setLine(row, append(line[:loCol], line[loCol+1:]...))
	}
	e.setBlockCaret(top, bot, loCol)
}

// blockPaste inserts text rectangularly: line i of the pasted text goes into
// the i'th row below the cursor, all at the same column, appending rows at the
// end of the buffer if the paste runs past it. This is what a block copy
// pasted back expects — see Paste for how one is recognised.
//
// The caller pushes the undo step.
func (e *Editor) blockPaste(text string) {
	parts := strings.Split(strings.ReplaceAll(expandTabs(text), "\r\n", "\n"), "\n")
	col := e.cursorCol
	switch {
	case e.blockEditing():
		col = e.blockCollapse()
		e.cursorRow, _ = e.blockRows()
	case e.HasSelection():
		e.deleteSelection()
		col = e.cursorCol
	}
	e.selecting, e.selBlock = false, false
	row := e.cursorRow
	for i, part := range parts {
		r := row + i
		if r >= e.doc.Len() {
			e.doc.edit(func(lines [][]rune) [][]rune { return append(lines, []rune{}) })
		}
		seg := []rune(part)
		line := e.blockPadLine(r, col)
		nl := make([]rune, 0, len(line)+len(seg))
		nl = append(nl, line[:col]...)
		nl = append(nl, seg...)
		nl = append(nl, line[col:]...)
		e.doc.setLine(r, nl)
	}
	e.cursorRow = row + len(parts) - 1
	e.cursorCol = col + len([]rune(parts[len(parts)-1]))
}
