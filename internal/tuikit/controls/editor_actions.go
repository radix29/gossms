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
	e.cursorRow = e.doc.Len() - 1
	e.cursorCol = len(e.doc.Line(e.cursorRow))
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
		line := e.doc.Line(sr + i)
		cp := make([]rune, len(line))
		copy(cp, line)
		block[i] = cp
	}
	e.doc.edit(func(lines [][]rune) [][]rune {
		newLines := make([][]rune, 0, len(lines)+n)
		newLines = append(newLines, lines[:er+1]...)
		newLines = append(newLines, block...)
		return append(newLines, lines[er+1:]...)
	})
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
	e.doc.edit(func(lines [][]rune) [][]rune {
		newLines := make([][]rune, 0, len(lines)-(er-sr+1)+1)
		newLines = append(newLines, lines[:sr]...)
		newLines = append(newLines, lines[er+1:]...)
		if len(newLines) == 0 {
			newLines = [][]rune{{}}
		}
		return newLines
	})
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
	e.doc.edit(func(lines [][]rune) [][]rune {
		above := lines[sr-1]
		copy(lines[sr-1:er], lines[sr:er+1])
		lines[er] = above
		return lines
	})
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
	if er >= e.doc.Len()-1 {
		return
	}
	e.pushUndo()
	e.doc.edit(func(lines [][]rune) [][]rune {
		below := lines[er+1]
		copy(lines[sr+1:er+2], lines[sr:er+1])
		lines[sr] = below
		return lines
	})
	e.cursorRow++
	if e.selecting {
		e.selAnchorRow++
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// DefaultIndentWidth is how many spaces IndentLines and the Tab key insert in
// an Editor whose width has not been set. Tabs are never inserted — only
// converted away from by DedentLines/dedentAmount — since Editor's rendering
// has no tab-stop expansion.
//
// internal/config declares a constant that must agree with this one;
// TestIndentWidthDefaultsAgree in internal/tui holds them together.
const DefaultIndentWidth = 4

// defaultIndentWidth is what NewEditor seeds e.indentWidth with. It exists so
// editors built where the application config is out of reach (property-sheet
// T-SQL rows, the Agent job-step command box) still honour the user's indent
// size; every editor that can reach the config calls SetIndentWidth instead.
var defaultIndentWidth = DefaultIndentWidth

// SetDefaultIndentWidth sets the width newly created Editors start with.
// Values outside 1..MaxIndentWidth are ignored. Existing editors keep theirs —
// use SetIndentWidth for those.
func SetDefaultIndentWidth(n int) {
	if n < 1 || n > MaxIndentWidth {
		return
	}
	defaultIndentWidth = n
}

// MaxIndentWidth is the sanity ceiling SetIndentWidth and
// SetDefaultIndentWidth enforce; internal/config clamps to the same range.
const MaxIndentWidth = 16

// SetIndentWidth sets how many spaces Tab, IndentLines, DedentLines and tab
// expansion use in this editor. Values outside 1..MaxIndentWidth are ignored.
//
// Call it at construction, before any SetText: SetText expands tabs at the
// editor's current width, and a caller that snapshots Text() afterwards (see
// docs/ui-rules.md § Editor) would read a later width change as a user edit.
// Changing the width at runtime deliberately does not re-expand existing text.
func (e *Editor) SetIndentWidth(n int) {
	if n < 1 || n > MaxIndentWidth {
		return
	}
	e.indentWidth = n
}

// IndentWidth returns the editor's indent width in spaces.
func (e *Editor) IndentWidth() int { return e.indentWidth }

// expandTabs replaces every literal tab in text with e.indentWidth spaces, so
// content loaded from disk or pasted in renders the same as typed
// indentation (Editor's rendering has no tab-stop expansion, so a raw tab
// would otherwise draw as a single narrow column).
func (e *Editor) expandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", strings.Repeat(" ", e.indentWidth))
}

// sqlIndentKeywords are the clause keywords that, left standing as the last
// token on a line, open a block the next line belongs inside. Only these three
// — the list the behaviour was asked for — and deliberately not BEGIN/END or
// JOIN: anything that needs matching to a closer needs a parser, and a
// half-done one indents wrongly more often than not.
var sqlIndentKeywords = map[string]bool{"select": true, "from": true, "where": true}

// SetSmartIndent turns on the extra indent level smartIndentBonus describes.
// Off by default: SELECT/FROM/WHERE mean nothing in the plain multi-line text
// boxes that also use Editor, so only the SQL editors ask for it.
func (e *Editor) SetSmartIndent(v bool) { e.smartIndent = v }

// smartIndentBonus reports the extra indent Enter adds beyond the current
// line's own leading whitespace: one level when the text to the left of the
// cursor ends with an open parenthesis, or with one of sqlIndentKeywords as
// its last token.
//
// "Last token" is what keeps the indentation from drifting right across a
// query: `SELECT` alone indents the column list that follows, while
// `SELECT a, b` does not, so the next clause starts back at the same column as
// the one above it.
func (e *Editor) smartIndentBonus() int {
	if !e.smartIndent || e.cursorRow < 0 || e.cursorRow >= e.doc.Len() {
		return 0
	}
	line := e.doc.Line(e.cursorRow)
	left := strings.TrimRight(string(line[:min(e.cursorCol, len(line))]), " \t")
	if left == "" {
		return 0
	}
	if left[len(left)-1] == '(' {
		return e.indentWidth
	}
	// The token is whatever follows the last separator; a leading "(" counts as
	// one so "VALUES (SELECT" reads as "SELECT".
	if i := strings.LastIndexAny(left, " \t("); sqlIndentKeywords[strings.ToLower(left[i+1:])] {
		return e.indentWidth
	}
	return 0
}

// leadingIndentForNewLine reports how many leading whitespace runes the line
// the cursor sits on starts with, clamped to the cursor column so that
// splitting inside the indentation copies only what is to the left of the
// cursor — what SSMS's smart indenting does. Runs of tabs cannot occur in the
// buffer (expandTabs), but a tab counts as one rune anyway, mirroring
// dedentAmount.
func (e *Editor) leadingIndentForNewLine() int {
	if e.cursorRow < 0 || e.cursorRow >= e.doc.Len() {
		return 0
	}
	line := e.doc.Line(e.cursorRow)
	limit := min(e.cursorCol, len(line))
	n := 0
	for n < limit && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}

// IndentLines inserts e.indentWidth spaces at column 0 of the current line (or
// every line spanned by the selection). An active selection is preserved,
// its columns shifted right by e.indentWidth on whichever row(s) the
// anchor/cursor sit.
func (e *Editor) IndentLines() {
	e.pushUndo()
	sr, er := e.affectedLineRange()
	for r := sr; r <= er; r++ {
		line := e.doc.Line(r)
		nl := make([]rune, len(line)+e.indentWidth)
		for i := range e.indentWidth {
			nl[i] = ' '
		}
		copy(nl[e.indentWidth:], line)
		e.doc.setLine(r, nl)
		if r == e.cursorRow {
			e.cursorCol += e.indentWidth
		}
		if e.selecting && r == e.selAnchorRow {
			e.selAnchorCol += e.indentWidth
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// DedentLines removes one leading tab, or up to e.indentWidth leading spaces,
// from the current line (or every line spanned by the selection). An active
// selection is preserved, its columns shifted left by however much was
// actually removed from that row.
func (e *Editor) DedentLines() {
	e.pushUndo()
	sr, er := e.affectedLineRange()
	for r := sr; r <= er; r++ {
		line := e.doc.Line(r)
		removed := e.dedentAmount(line)
		if removed == 0 {
			continue
		}
		nl := make([]rune, len(line)-removed)
		copy(nl, line[removed:])
		e.doc.setLine(r, nl)
		if r == e.cursorRow {
			e.cursorCol = max(0, e.cursorCol-removed)
		}
		if e.selecting && r == e.selAnchorRow {
			e.selAnchorCol = max(0, e.selAnchorCol-removed)
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// dedentAmount reports how many leading runes DedentLines should strip from
// line: one leading tab (from content written before tabs were converted to
// spaces, or pasted in from elsewhere), else up to e.indentWidth leading
// spaces.
func (e *Editor) dedentAmount(line []rune) int {
	if len(line) > 0 && line[0] == '\t' {
		return 1
	}
	n := 0
	for n < len(line) && n < e.indentWidth && line[n] == ' ' {
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
		if !isCommentedLine(e.doc.Line(r)) {
			allCommented = false
			break
		}
	}
	e.pushUndo()
	for r := sr; r <= er; r++ {
		before := len(e.doc.Line(r))
		if allCommented {
			e.doc.setLine(r, uncommentLine(e.doc.Line(r)))
		} else {
			e.doc.setLine(r, commentLine(e.doc.Line(r)))
		}
		delta := len(e.doc.Line(r)) - before
		if r == e.cursorRow {
			e.cursorCol = max(0, e.cursorCol+delta)
		}
		if e.selecting && r == e.selAnchorRow {
			e.selAnchorCol = max(0, e.selAnchorCol+delta)
		}
	}
	e.clampCursor()
	e.ensureCursorVisible()
}

// transformSelection applies fn to every rune in the current selection, in
// place, branching on selBlock the same way SelectedText does. No-op if
// there's no selection.
//
// The rewritten lines go back through setLine even though the runes were
// changed in place and the slice header is unchanged: the version counter is
// what tells the highlighters and the wrap cache that the text moved, and an
// in-place edit that skipped it would leave Ctrl+Shift+U recolouring nothing
// — an uppercased keyword keeping its old, non-keyword colour until some
// unrelated edit bumped the version.
func (e *Editor) transformSelection(fn func(rune) rune) {
	if !e.HasSelection() {
		return
	}
	e.pushUndo()
	apply := func(r, lo, hi int) {
		line := e.doc.Line(r)
		for i := lo; i < hi; i++ {
			line[i] = fn(line[i])
		}
		e.doc.setLine(r, line)
	}
	if e.selBlock {
		topRow, botRow := min(e.selAnchorRow, e.cursorRow), max(e.selAnchorRow, e.cursorRow)
		loCol, hiCol := e.blockColumnBounds()
		for r := topRow; r <= botRow; r++ {
			n := len(e.doc.Line(r))
			apply(r, core.Clamp(loCol, 0, n), core.Clamp(hiCol, 0, n))
		}
		return
	}
	sr, sc, er, ec := e.selectionBounds()
	for r := sr; r <= er; r++ {
		n := len(e.doc.Line(r))
		lo, hi := 0, n
		if r == sr {
			lo = core.Clamp(sc, 0, n)
		}
		if r == er {
			hi = core.Clamp(ec, 0, n)
		}
		apply(r, lo, hi)
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
	line := e.doc.Line(e.cursorRow)
	left := core.WordBoundaryLeft(line, e.cursorCol)
	e.doc.setLine(e.cursorRow, append(line[:left], line[e.cursorCol:]...))
	e.cursorCol = left
}

// deleteWordRight removes the word to the right of the cursor (Ctrl+
// Delete), or merges with the next line at end-of-line. Caller (HandleKey)
// is responsible for pushUndo.
func (e *Editor) deleteWordRight() {
	line := e.doc.Line(e.cursorRow)
	if e.cursorCol >= len(line) {
		if e.cursorRow < e.doc.Len()-1 {
			e.deleteChar()
		}
		return
	}
	right := core.WordBoundaryRight(line, e.cursorCol)
	e.doc.setLine(e.cursorRow, append(line[:e.cursorCol], line[right:]...))
}
