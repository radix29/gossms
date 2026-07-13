package controls

import (
	"strings"
	"unicode"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Notepad++/Scintilla-style line and block actions for Editor. Every
// exported method here is self-contained — it pushes its own undo step and
// leaves the cursor clamped and visible — because each is invoked both from
// Editor.HandleKey and directly from a Menu action closure, which bypasses
// HandleKey entirely.
// ---------------------------------------------------------------------------

// affectedLineRange returns the row range a line/block action should apply
// to: the current selection's row span if there is one (linear or block —
// selectionBounds' row component is always min/max regardless of which
// corner is anchor vs. cursor, so this is correct for both modes), or just
// the cursor's own line otherwise.
func (e *Editor) affectedLineRange() (startRow, endRow int) {
	if e.HasSelection() {
		sr, _, er, _ := e.selectionBounds()
		return sr, er
	}
	return e.cursorRow, e.cursorRow
}

// SelectAll selects the entire buffer as a fresh linear selection.
func (e *Editor) SelectAll() {
	e.selecting = true
	e.selBlock = false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow = len(e.lines) - 1
	e.cursorCol = len(e.lines[e.cursorRow])
}

// Undo and Redo expose the existing undo/redo stack for callers outside
// this package (the Edit menu), which can't reach the unexported undo/redo.
func (e *Editor) Undo() { e.undo() }
func (e *Editor) Redo() { e.redo() }

// DuplicateLines copies the current line (or every line spanned by the
// selection) immediately below itself, moving the cursor into the copy.
// Any active selection collapses.
func (e *Editor) DuplicateLines() {
	e.pushUndo()
	sr, er := e.affectedLineRange()
	e.selecting, e.selBlock = false, false
	n := er - sr + 1
	block := make([][]rune, n)
	for i := 0; i < n; i++ {
		line := e.lines[sr+i]
		cp := make([]rune, len(line))
		copy(cp, line)
		block[i] = cp
	}
	newLines := make([][]rune, 0, len(e.lines)+n)
	newLines = append(newLines, e.lines[:er+1]...)
	newLines = append(newLines, block...)
	newLines = append(newLines, e.lines[er+1:]...)
	e.lines = newLines
	e.cursorRow += n
	e.clampCursor()
	e.ensureCursorVisible()
}

// DeleteLines removes the current line (or every line spanned by the
// selection). Any active selection collapses.
func (e *Editor) DeleteLines() {
	e.pushUndo()
	sr, er := e.affectedLineRange()
	e.selecting, e.selBlock = false, false
	newLines := make([][]rune, 0, len(e.lines)-(er-sr+1)+1)
	newLines = append(newLines, e.lines[:sr]...)
	newLines = append(newLines, e.lines[er+1:]...)
	if len(newLines) == 0 {
		newLines = [][]rune{{}}
	}
	e.lines = newLines
	e.cursorRow, e.cursorCol = sr, 0
	e.clampCursor()
	e.ensureCursorVisible()
}

// MoveLinesUp swaps the current line (or every line spanned by the
// selection) with the line above it. No-op at the top of the buffer. Any
// active selection is preserved and shifts with the moved lines.
func (e *Editor) MoveLinesUp() {
	sr, er := e.affectedLineRange()
	if sr == 0 {
		return
	}
	e.pushUndo()
	above := e.lines[sr-1]
	copy(e.lines[sr-1:er], e.lines[sr:er+1])
	e.lines[er] = above
	e.cursorRow--
	if e.selecting {
		e.selAnchorRow--
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// MoveLinesDown mirrors MoveLinesUp, swapping downward. No-op at the
// bottom of the buffer.
func (e *Editor) MoveLinesDown() {
	sr, er := e.affectedLineRange()
	if er >= len(e.lines)-1 {
		return
	}
	e.pushUndo()
	below := e.lines[er+1]
	copy(e.lines[sr+1:er+2], e.lines[sr:er+1])
	e.lines[sr] = below
	e.cursorRow++
	if e.selecting {
		e.selAnchorRow++
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// indentWidth is how many spaces IndentLines and the Tab key insert. Tabs
// are never inserted — only converted away from by DedentLines/dedentAmount
// — since Editor's rendering has no tab-stop expansion.
const indentWidth = 4

// expandTabs replaces every literal tab in text with indentWidth spaces, so
// content loaded from disk or pasted in renders the same as typed
// indentation (Editor's rendering has no tab-stop expansion, so a raw tab
// would otherwise draw as a single narrow column).
func expandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", strings.Repeat(" ", indentWidth))
}

// IndentLines inserts indentWidth spaces at column 0 of the current line (or
// every line spanned by the selection). An active selection is preserved,
// its columns shifted right by indentWidth on whichever row(s) the
// anchor/cursor sit.
func (e *Editor) IndentLines() {
	e.pushUndo()
	sr, er := e.affectedLineRange()
	for r := sr; r <= er; r++ {
		line := e.lines[r]
		nl := make([]rune, len(line)+indentWidth)
		for i := range indentWidth {
			nl[i] = ' '
		}
		copy(nl[indentWidth:], line)
		e.lines[r] = nl
		if r == e.cursorRow {
			e.cursorCol += indentWidth
		}
		if e.selecting && r == e.selAnchorRow {
			e.selAnchorCol += indentWidth
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// DedentLines removes one leading tab, or up to indentWidth leading spaces,
// from the current line (or every line spanned by the selection). An active
// selection is preserved, its columns shifted left by however much was
// actually removed from that row.
func (e *Editor) DedentLines() {
	e.pushUndo()
	sr, er := e.affectedLineRange()
	for r := sr; r <= er; r++ {
		line := e.lines[r]
		removed := dedentAmount(line)
		if removed == 0 {
			continue
		}
		nl := make([]rune, len(line)-removed)
		copy(nl, line[removed:])
		e.lines[r] = nl
		if r == e.cursorRow {
			e.cursorCol = core.Max(0, e.cursorCol-removed)
		}
		if e.selecting && r == e.selAnchorRow {
			e.selAnchorCol = core.Max(0, e.selAnchorCol-removed)
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// dedentAmount reports how many leading runes DedentLines should strip from
// line: one leading tab (from content written before tabs were converted to
// spaces, or pasted in from elsewhere), else up to indentWidth leading
// spaces.
func dedentAmount(line []rune) int {
	if len(line) > 0 && line[0] == '\t' {
		return 1
	}
	n := 0
	for n < len(line) && n < indentWidth && line[n] == ' ' {
		n++
	}
	return n
}

// isCommentedLine reports whether line, after its leading whitespace,
// starts with the SQL line-comment token "--".
func isCommentedLine(line []rune) bool {
	i := 0
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	return i+1 < len(line) && line[i] == '-' && line[i+1] == '-'
}

// commentLine inserts "-- " at line's first non-whitespace column (or
// appends it if the line is blank).
func commentLine(line []rune) []rune {
	i := 0
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	prefix := []rune("-- ")
	nl := make([]rune, 0, len(line)+len(prefix))
	nl = append(nl, line[:i]...)
	nl = append(nl, prefix...)
	nl = append(nl, line[i:]...)
	return nl
}

// uncommentLine strips a leading "--" (and one following space, if
// present) from line. No-op if line isn't commented.
func uncommentLine(line []rune) []rune {
	if !isCommentedLine(line) {
		return line
	}
	i := 0
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	j := i + 2
	if j < len(line) && line[j] == ' ' {
		j++
	}
	nl := make([]rune, 0, len(line)-(j-i))
	nl = append(nl, line[:i]...)
	nl = append(nl, line[j:]...)
	return nl
}

// ToggleLineComments comments or uncomments the current line (or every
// line spanned by the selection): uncomments only if every affected line
// is already commented, otherwise comments every affected line (a blank
// line in range counts as "not commented," so it gets "-- " prefixed too
// when the range is commented — expected, not a bug). An active selection
// is preserved, its columns approximately shifted by the net length change
// on whichever row the anchor/cursor sit (a cursor inside leading
// whitespace can drift a column or two — an accepted simplification).
func (e *Editor) ToggleLineComments() {
	sr, er := e.affectedLineRange()
	allCommented := true
	for r := sr; r <= er; r++ {
		if !isCommentedLine(e.lines[r]) {
			allCommented = false
			break
		}
	}
	e.pushUndo()
	for r := sr; r <= er; r++ {
		before := len(e.lines[r])
		if allCommented {
			e.lines[r] = uncommentLine(e.lines[r])
		} else {
			e.lines[r] = commentLine(e.lines[r])
		}
		delta := len(e.lines[r]) - before
		if r == e.cursorRow {
			e.cursorCol = core.Max(0, e.cursorCol+delta)
		}
		if e.selecting && r == e.selAnchorRow {
			e.selAnchorCol = core.Max(0, e.selAnchorCol+delta)
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// transformSelection applies fn to every rune in the current selection, in
// place, branching on selBlock the same way SelectedText does. No-op if
// there's no selection.
func (e *Editor) transformSelection(fn func(rune) rune) {
	if !e.HasSelection() {
		return
	}
	e.pushUndo()
	if e.selBlock {
		topRow, botRow := core.Min(e.selAnchorRow, e.cursorRow), core.Max(e.selAnchorRow, e.cursorRow)
		loCol, hiCol := e.blockColumnBounds()
		for r := topRow; r <= botRow; r++ {
			line := e.lines[r]
			lo := core.Clamp(loCol, 0, len(line))
			hi := core.Clamp(hiCol, 0, len(line))
			for i := lo; i < hi; i++ {
				line[i] = fn(line[i])
			}
		}
		return
	}
	sr, sc, er, ec := e.selectionBounds()
	for r := sr; r <= er; r++ {
		line := e.lines[r]
		lo, hi := 0, len(line)
		if r == sr {
			lo = core.Clamp(sc, 0, len(line))
		}
		if r == er {
			hi = core.Clamp(ec, 0, len(line))
		}
		for i := lo; i < hi; i++ {
			line[i] = fn(line[i])
		}
	}
}

// UppercaseSelection and LowercaseSelection convert the case of every rune
// in the current selection, in place. No-op if there's no selection.
func (e *Editor) UppercaseSelection() { e.transformSelection(unicode.ToUpper) }
func (e *Editor) LowercaseSelection() { e.transformSelection(unicode.ToLower) }

// deleteWordLeft removes the word to the left of the cursor (Ctrl+
// Backspace), or merges with the previous line at column 0. Caller
// (HandleKey) is responsible for pushUndo.
func (e *Editor) deleteWordLeft() {
	if e.cursorCol == 0 {
		if e.cursorRow > 0 {
			e.backspace()
		}
		return
	}
	line := e.lines[e.cursorRow]
	left := core.WordBoundaryLeft(line, e.cursorCol)
	e.lines[e.cursorRow] = append(line[:left], line[e.cursorCol:]...)
	e.cursorCol = left
}

// deleteWordRight removes the word to the right of the cursor (Ctrl+
// Delete), or merges with the next line at end-of-line. Caller (HandleKey)
// is responsible for pushUndo.
func (e *Editor) deleteWordRight() {
	line := e.lines[e.cursorRow]
	if e.cursorCol >= len(line) {
		if e.cursorRow < len(e.lines)-1 {
			e.deleteChar()
		}
		return
	}
	right := core.WordBoundaryRight(line, e.cursorCol)
	e.lines[e.cursorRow] = append(line[:e.cursorCol], line[right:]...)
}
