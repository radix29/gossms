package controls

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Editor
// ---------------------------------------------------------------------------

// Highlighter is a function that receives every line in the document and
// the index of the one to highlight, returning that line's ColorRun
// segments. The full buffer (not just the target line) is passed so a
// highlighter that needs cross-line state — a multi-line block comment
// spanning several lines, for instance — can look at what precedes idx.
// Pass nil to disable syntax highlighting.
type Highlighter func(lines [][]rune, idx int) []ColorRun

// ColorRun describes a coloured segment within an editor line.
type ColorRun struct {
	Start int
	Len   int
	Style tcell.Style
}

// editorState is an undo/redo snapshot.
type editorState struct {
	lines     [][]rune
	cursorRow int
	cursorCol int
}

// maxUndoSteps caps the undo stack so unbounded editing doesn't grow it
// forever — each step is a full copy of the buffer's lines, so bounding
// the step count also bounds memory to a fixed multiple of one snapshot's
// size. Oldest steps are dropped first; the redo stack is unbounded since
// it's cleared on every new edit and can never grow past what undo popped.
const maxUndoSteps = 500

// Editor is a multi-line text editor.
//
// Note: unlike the rest of tuikit (TreeView, DataGrid, MenuBar, dialogs —
// all of which use core.DisplayWidth for correct rendering of wide/CJK
// characters), Editor's cursor, selection, and rendering are rune-indexed:
// one rune occupies exactly one column. Wide-character string literals
// remain editable but may render with minor column drift.
type Editor struct {
	rect      core.Rect
	lines     [][]rune
	cursorRow int
	cursorCol int
	scrollRow int
	scrollCol int
	active    bool
	highlight Highlighter

	// desiredCol is the "goal column" for vertical caret movement — the
	// Notepad++/Scintilla convention where Up/Down/PgUp/PgDn (plain or with
	// Shift, extending a selection) keep aiming for this column even after
	// passing through a shorter line that forced cursorCol to clamp down,
	// so moving back onto a longer line snaps back to where the movement
	// started instead of staying at the shorter line's clamped column. Any
	// other cursor-moving action (typing, Left/Right, Home/End, a mouse
	// click) resets it to the new cursorCol, starting a fresh vertical run.
	desiredCol int

	// OnRightClick, if set, is called with the click position when the
	// user right-clicks (Button2) inside the content area — the app layer
	// uses it to pop up a Cut/Copy/Paste context menu. The editor itself
	// leaves the cursor and any active selection untouched, so the menu's
	// Copy/Cut act on whatever was already selected.
	OnRightClick func(x, y int)

	// hideGutter suppresses the line-number gutter (and reclaims its width
	// for content). Zero value is false, so every existing NewEditor call
	// site — the SQL query editor — keeps its gutter automatically; only a
	// caller that explicitly wants a plain multi-line text box opts out via
	// SetGutterVisible(false).
	hideGutter bool

	// wrapMode enables word-wrap rendering (see SetWrapMode). Zero value
	// is false, so the SQL query editor's horizontal-scroll behavior is
	// completely unaffected; it's an opt-in for plain text boxes.
	wrapMode bool

	// readOnly rejects every mutating key (see SetReadOnly) while still
	// allowing cursor movement, selection, and copy — a "view text, can't
	// change it" mode used by DataGrid's full-cell-content popup.
	readOnly bool

	// Selection: selecting is true while a Shift+move- or mouse-drag-driven
	// selection is active; selAnchor{Row,Col} is the fixed end,
	// cursorRow/cursorCol is the moving end. A selection where anchor ==
	// cursor is treated as empty (HasSelection reports false).
	// mouseDragging distinguishes a fresh Button1 click (start a new
	// selection anchor) from a continued drag (keep the anchor, move the
	// cursor) — see HandleMouse.
	//
	// selBlock switches the *interpretation* of the same anchor/cursor
	// pair from a linear (stream) selection to a rectangular (column)
	// selection: every row between the anchor's and cursor's row is
	// affected, each at the same [loCol,hiCol) column range — see
	// blockColumnBounds. Entered via Alt+Shift+Arrow or Alt+drag, never in
	// wrapMode (rectangular selection assumes fixed rune columns, which
	// word-wrap breaks).
	selecting     bool
	selBlock      bool
	selAnchorRow  int
	selAnchorCol  int
	mouseDragging bool

	undoStack []editorState
	redoStack []editorState

	// Completion: see editor_completion.go. completionProvider is nil for
	// every Editor except the SQL query editor, so every other Editor's
	// behavior (Ctrl+Space opening OnRightClick's menu, no popup ever
	// appearing) is completely unaffected.
	completionProvider CompletionProvider
	completionOpen     bool
	completionItems    []CompletionItem
	completionSel      int
	completionScroll   int
	completionFrom     int // column where the replaced span starts; valid only while completionOpen

	// completionSuppressed, set by Escape, stops the popup reopening at the
	// same token the user just dismissed it at — completionSuppressRow/Col
	// pin that token's start position; moving off it (row change, or the
	// token's own start column shifting) clears the suppression, same as
	// SSMS's "Escape closes IntelliSense for this word" behavior.
	completionSuppressed  bool
	completionSuppressRow int
	completionSuppressCol int

	// completionMouseDown distinguishes a fresh Button1 press on the popup
	// from a continued hold over the same row — mirrors mouseDragging's
	// purpose for the editor body. Without it, tcell's all-motion mouse
	// tracking resends Buttons()==Button1 on every cursor motion while the
	// button stays down, so a single click on an already-selected item can
	// call commitSelectedCompletion() more than once.
	completionMouseDown bool
}

// NewEditor creates an Editor. Pass a Highlighter or nil.
func NewEditor(h Highlighter) *Editor {
	return new(Editor{
		lines:     [][]rune{{}},
		highlight: h,
	})
}

// SetGutterVisible shows or hides the line-number gutter. Editors default
// to a visible gutter (matching the SQL query editor); pass false for
// plain multi-line text boxes — e.g. the connection-string editor — where
// line numbers aren't meaningful.
func (e *Editor) SetGutterVisible(v bool) { e.hideGutter = !v }

// gutterWidth returns the on-screen width reserved for the line-number
// gutter: gutterW when shown, 0 when hidden via SetGutterVisible(false).
func (e *Editor) gutterWidth() int {
	if e.hideGutter {
		return 0
	}
	return gutterW
}

// SetWrapMode enables word-wrap rendering: long lines soft-wrap at word
// boundaries to fit the content width instead of scrolling horizontally,
// and scrollRow/scrolling become vertical-only — scrollCol is unused in
// this mode. Defaults to off; used by plain multi-line text boxes like
// the connection-string editor.
//
// KeyUp/KeyDown/PgUp/PgDn move between logical lines (actual newlines),
// not wrapped visual rows; Left/Right/Home/End/click move the cursor
// within a wrapped line.
func (e *Editor) SetWrapMode(v bool) { e.wrapMode = v }

// SetReadOnly makes the editor reject every mutating key — typed
// characters, Enter, Backspace/Delete, Tab/Backtab indent, undo/redo, and
// the line/case/comment actions (Ctrl+D/L/U/Z/Y, Ctrl+/) — while cursor
// movement, Shift/Alt-Shift selection, Ctrl+A, and mouse click-drag
// selection keep working, so the content can still be read and copied.
// Defaults to false (every existing editable use of Editor is unaffected).
func (e *Editor) SetReadOnly(v bool) { e.readOnly = v }

// SetBounds positions the editor.
func (e *Editor) SetBounds(x, y, w, h int) { e.rect = core.Rect{X: x, Y: y, W: w, H: h} }

// SetActive sets focus state. Losing focus closes the completion popup, if
// open — it would otherwise linger on screen while keys route elsewhere.
func (e *Editor) SetActive(v bool) {
	if !v && e.completionOpen {
		e.closeCompletion()
	}
	e.active = v
}

// Bounds returns the editor's current screen rect, set by SetBounds — lets
// a caller outside the package hit-test a screen coordinate against the
// editor without duplicating its geometry (e.g. Object Explorer's
// drag-and-drop drop-target check in app_events.go).
func (e *Editor) Bounds() core.Rect { return e.rect }

// Focus sets focus state, mirroring the widgets package's Focus(bool)
// convention so Editor can be Tab-cycled alongside InputField, DropDown,
// and CheckBox by callers that key off that method name.
func (e *Editor) Focus(v bool) { e.SetActive(v) }

// Text returns the editor content.
func (e *Editor) Text() string {
	var sb strings.Builder
	for i, line := range e.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(line))
	}
	return sb.String()
}

// SetText replaces content, resetting the cursor, any active selection,
// and the undo/redo history — all of which refer to the old document and
// would otherwise dangle (a stale selection anchor past the new buffer's
// end makes SelectedText panic; a stale undo step would restore text that
// was never actually typed into this document).
func (e *Editor) SetText(text string) {
	parts := strings.Split(strings.ReplaceAll(expandTabs(text), "\r\n", "\n"), "\n")
	e.lines = make([][]rune, len(parts))
	for i, p := range parts {
		e.lines[i] = []rune(p)
	}
	e.cursorRow, e.cursorCol, e.scrollRow, e.scrollCol = 0, 0, 0, 0
	e.selecting, e.selBlock, e.mouseDragging = false, false, false
	e.undoStack, e.redoStack = nil, nil
	e.closeCompletion()
	e.completionSuppressed = false
}

func (e *Editor) clampCursor() {
	e.cursorRow = core.Clamp(e.cursorRow, 0, len(e.lines)-1)
	// While a block (column) selection is active, cursorCol doubles as its
	// "virtual column" and is allowed to sit past a short row's actual
	// length — the expected rectangular-selection visual when the
	// selection started on a longer line. It self-heals the moment
	// selBlock goes false, before any insert/delete runs.
	if e.cursorRow < len(e.lines) && !e.selBlock {
		e.cursorCol = core.Clamp(e.cursorCol, 0, len(e.lines[e.cursorRow]))
	}
}

func (e *Editor) ensureCursorVisible() {
	if e.wrapMode {
		contentW := e.rect.W - e.gutterWidth()
		vls := e.buildVisualLines(contentW)
		vi := visualIndexForCursor(vls, e.cursorRow, e.cursorCol)
		if vi < e.scrollRow {
			e.scrollRow = vi
		}
		if vi >= e.scrollRow+e.rect.H {
			e.scrollRow = vi - e.rect.H + 1
		}
		if e.scrollRow < 0 {
			e.scrollRow = 0
		}
		return
	}
	if e.cursorRow < e.scrollRow {
		e.scrollRow = e.cursorRow
	}
	if e.cursorRow >= e.scrollRow+e.rect.H {
		e.scrollRow = e.cursorRow - e.rect.H + 1
	}
	contentW := e.rect.W - e.gutterWidth()
	if e.cursorCol < e.scrollCol {
		e.scrollCol = e.cursorCol
	}
	if e.cursorCol >= e.scrollCol+contentW {
		e.scrollCol = e.cursorCol - contentW + 1
	}
}

func (e *Editor) insertRune(r rune) {
	if e.cursorRow >= len(e.lines) {
		e.lines = append(e.lines, []rune{})
	}
	line := e.lines[e.cursorRow]
	nl := make([]rune, len(line)+1)
	copy(nl, line[:e.cursorCol])
	nl[e.cursorCol] = r
	copy(nl[e.cursorCol+1:], line[e.cursorCol:])
	e.lines[e.cursorRow] = nl
	e.cursorCol++
}

func (e *Editor) insertNewline() {
	if e.cursorRow >= len(e.lines) {
		e.lines = append(e.lines, []rune{})
	}
	line := e.lines[e.cursorRow]
	before := make([]rune, e.cursorCol)
	copy(before, line[:e.cursorCol])
	after := make([]rune, len(line)-e.cursorCol)
	copy(after, line[e.cursorCol:])
	e.lines[e.cursorRow] = before
	nl := make([][]rune, len(e.lines)+1)
	copy(nl, e.lines[:e.cursorRow+1])
	nl[e.cursorRow+1] = after
	copy(nl[e.cursorRow+2:], e.lines[e.cursorRow+1:])
	e.lines = nl
	e.cursorRow++
	e.cursorCol = 0
}

func (e *Editor) backspace() {
	if e.cursorRow == 0 && e.cursorCol == 0 {
		return
	}
	if e.cursorCol > 0 {
		line := e.lines[e.cursorRow]
		e.lines[e.cursorRow] = append(line[:e.cursorCol-1], line[e.cursorCol:]...)
		e.cursorCol--
	} else {
		prev := e.lines[e.cursorRow-1]
		cur := e.lines[e.cursorRow]
		e.cursorCol = len(prev)
		e.lines[e.cursorRow-1] = append(prev, cur...)
		e.lines = append(e.lines[:e.cursorRow], e.lines[e.cursorRow+1:]...)
		e.cursorRow--
	}
}

func (e *Editor) deleteChar() {
	if e.cursorRow >= len(e.lines) {
		return
	}
	line := e.lines[e.cursorRow]
	if e.cursorCol < len(line) {
		e.lines[e.cursorRow] = append(line[:e.cursorCol], line[e.cursorCol+1:]...)
	} else if e.cursorRow < len(e.lines)-1 {
		e.lines[e.cursorRow] = append(line, e.lines[e.cursorRow+1]...)
		e.lines = append(e.lines[:e.cursorRow+1], e.lines[e.cursorRow+2:]...)
	}
}

func (e *Editor) pushUndo() {
	lines := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		nl := make([]rune, len(l))
		copy(nl, l)
		lines[i] = nl
	}
	e.undoStack = append(e.undoStack, editorState{lines, e.cursorRow, e.cursorCol})
	if len(e.undoStack) > maxUndoSteps {
		e.undoStack = e.undoStack[1:]
	}
	e.redoStack = nil
}

func (e *Editor) snapshot() editorState {
	lines := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		nl := make([]rune, len(l))
		copy(nl, l)
		lines[i] = nl
	}
	return editorState{lines, e.cursorRow, e.cursorCol}
}

func (e *Editor) undo() {
	if len(e.undoStack) == 0 {
		return
	}
	e.redoStack = append(e.redoStack, e.snapshot())
	st := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.lines, e.cursorRow, e.cursorCol = st.lines, st.cursorRow, st.cursorCol
}

func (e *Editor) redo() {
	if len(e.redoStack) == 0 {
		return
	}
	e.undoStack = append(e.undoStack, e.snapshot())
	st := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	e.lines, e.cursorRow, e.cursorCol = st.lines, st.cursorRow, st.cursorCol
}
