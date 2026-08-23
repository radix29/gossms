package controls

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Editor
// ---------------------------------------------------------------------------

// Highlighter receives the whole document and the index of the line to
// highlight, returning that line's ColorRun segments. The full buffer is passed
// so a highlighter needing cross-line state — a block comment spanning several
// lines — can look at what precedes idx. nil disables highlighting.
//
// A highlighter caching such state must key the cache on doc.Version() *and*
// the *Document itself: the version says whether the text changed, the pointer
// whether this is even the same document. Both built-in highlighters do.
type Highlighter func(doc *Document, idx int) []ColorRun

// ColorRun describes a coloured segment within an editor line. Start and Len
// are rune indices into the line, not terminal columns — Editor maps them to
// columns when it draws.
type ColorRun struct {
	Start int
	Len   int
	Style tcell.Style
}

// Editor is a multi-line text editor.
//
// Positions inside the text — cursorCol, the selection anchor, ColorRun bounds,
// wrap segments — are rune indices. Everything on screen — scrollCol, the
// cursor's x, a click's x, the horizontal scrollbar — is a terminal column. The
// two differ: a CJK ideograph or emoji takes two columns, a combining mark
// none. Every conversion goes through core.ColumnOfRune or
// core.RuneIndexAtColumn; treating a rune index as a column shifts the rest of
// a line one column left of where the editor thinks it is.
//
// Block (column) selection stays rune-indexed on purpose, so a block dragged
// across a line containing a wide rune has a ragged rather than straight edge —
// the SSMS-parity choice for a case with no single right answer.
type Editor struct {
	rect core.Rect

	// doc holds the text. A pointer because Highlighter is handed the same
	// *Document the Editor mutates, and both must see one version counter.
	doc *Document

	cursorRow int
	cursorCol int
	scrollRow int
	scrollCol int // terminal columns scrolled off to the left, not runes
	active    bool
	highlight Highlighter

	// desiredCol is the "goal column" for vertical caret movement: Up/Down/PgUp/
	// PgDn keep aiming for it even after a shorter line clamped cursorCol down,
	// so moving back onto a longer line snaps to where the movement started.
	// Any other cursor-moving action resets it.
	//
	// A *display* column, not a rune index: the caret tracks the same place on
	// screen down a column of text, and rune indices drift from that as soon as
	// a line above contains a wide character.
	desiredCol int

	// OnRightClick, if set, is called with the click position on a Button2 press
	// inside the content area — the app layer pops up a Cut/Copy/Paste menu. The
	// editor leaves the cursor and selection untouched, so the menu's Copy/Cut
	// act on whatever was already selected.
	OnRightClick func(x, y int)

	// hideGutter suppresses the line-number gutter and reclaims its width for
	// content. False by default, so the SQL query editor keeps its gutter; plain
	// multi-line text boxes opt out via SetGutterVisible(false).
	hideGutter bool

	// wrapMode enables word-wrap rendering, off by default — an opt-in for
	// plain text boxes, leaving the SQL editor's horizontal scrolling alone.
	wrapMode bool

	// readOnly rejects every mutating key while still allowing cursor
	// movement, selection, and copy — used by DataGrid's cell-content popup.
	readOnly bool

	// styleScratch is drawHighlighted's per-column style map, kept across calls
	// rather than allocated per line: Draw runs on every event and calls
	// drawHighlighted once per visible row. Valid only within one
	// drawHighlighted call — nothing may retain it.
	styleScratch []tcell.Style

	// vlScratch and segScratch are buildVisualLines' buffers. vlScratch also
	// *is* its cache: the flattening it holds stays valid until the document
	// or the wrap width changes, which the three fields below detect.
	vlScratch  []visualLine
	segScratch []wrapSegment

	vlCacheVersion uint64
	vlCacheWidth   int
	vlCacheValid   bool

	// Selection: selecting is true while a Shift+move or mouse-drag selection is
	// active; selAnchor{Row,Col} is the fixed end, cursorRow/cursorCol the moving
	// end, and anchor == cursor counts as empty. mouseDragging distinguishes a
	// fresh Button1 click (new anchor) from a continued drag (keep the anchor,
	// move the cursor).
	//
	// selBlock reinterprets the same anchor/cursor pair as a rectangular (column)
	// selection: every row between them at the same [loCol,hiCol) range — see
	// blockColumnBounds. Entered via Alt+Shift+Arrow or Alt+drag, never in
	// wrapMode, which breaks the fixed rune columns it assumes.
	selecting     bool
	selBlock      bool
	selAnchorRow  int
	selAnchorCol  int
	mouseDragging bool

	// lastClickAt/Row/Col record the previous Button1 press for double-click word
	// selection. tcell reports no click count, so the editor pairs presses
	// itself, as DataGrid does for its separator double-click.
	lastClickAt  time.Time
	lastClickRow int
	lastClickCol int

	// blockClip is the text most recently copied out of a block (column)
	// selection. Paste compares its argument against it to tell a block copy that
	// round-tripped through the OS clipboard from ordinary multi-line text — the
	// only way to know a paste should go back in rectangularly. Cleared by any
	// non-block copy.
	blockClip string

	// sbDragging latches a scrollbar-thumb drag. Separate from mouseDragging:
	// once set, every Button1 event controls the bar regardless of x, instead of
	// reading as a text click or selection drag.
	sbDragging bool

	// sbDraggingX is sbDragging's counterpart for the bar along the editor's
	// bottom row.
	sbDraggingX bool

	undoStack []editorState
	redoStack []editorState

	// undoBytes is the sum of undoStack's step sizes, maintained by every push
	// and pop so trimUndo never re-measures the stack.
	undoBytes int

	// stepOpen says the newest undo step is still missing its newLen because
	// the edit it covers has not run yet — see finalizeStep.
	stepOpen bool

	// Completion: see editor_completion.go. completionProvider is nil for every
	// Editor but the SQL query editor, where Ctrl+Space opens OnRightClick's menu
	// instead and no popup ever appears.
	completionProvider CompletionProvider
	completionOpen     bool
	completionItems    []CompletionItem
	completionSel      int
	completionScroll   int
	completionFrom     int // column where the replaced span starts; valid only while completionOpen

	// completionSuppressed, set by Escape, stops the popup reopening at the token
	// it was just dismissed at; completionSuppressRow/Col pin that token's start.
	// Moving off it — a row change, or the start column shifting — clears the
	// suppression, matching SSMS.
	completionSuppressed  bool
	completionSuppressRow int
	completionSuppressCol int

	// completionMouseDown distinguishes a fresh Button1 press on the popup from a
	// continued hold over the same row. Without it, tcell's all-motion tracking
	// resends Button1 on every cursor motion while held, so one click on an
	// already-selected item calls commitSelectedCompletion twice.
	completionMouseDown bool

	// completionSbDragging is the popup scrollbar's equivalent of
	// completionMouseDown, separate for the same reason as sbDragging.
	completionSbDragging bool

	// search holds the active find/replace pattern and its match list. The zero
	// value means no search.
	search editorSearch
}

// NewEditor creates an Editor. Pass a Highlighter or nil.
func NewEditor(h Highlighter) *Editor {
	return new(Editor{
		doc:       newDocument(),
		highlight: h,
	})
}

// Document returns the editor's buffer, for a caller reading the text by line
// without rebuilding it from Text(). It is the same *Document the editor
// mutates, so its Version() moves under the caller; nothing outside this package
// can write through it.
func (e *Editor) Document() *Document { return e.doc }

// SetHighlighter replaces the syntax highlighter — switching a query editor
// between SQL and XML for the file just opened, say. nil disables it.
//
// Pass a Highlighter built for this Editor alone. Both built-in ones cache the
// previous line's end-of-line comment state, so sharing one value between two
// Editors lets one document's carried-over block comment colour the other's.
func (e *Editor) SetHighlighter(h Highlighter) { e.highlight = h }

// SetGutterVisible shows or hides the line-number gutter, visible by default.
// Pass false for plain multi-line text boxes, where line numbers mean nothing.
func (e *Editor) SetGutterVisible(v bool) { e.hideGutter = !v }

// gutterWidth returns the width reserved for the line-number gutter: gutterW
// when shown, 0 when hidden.
func (e *Editor) gutterWidth() int {
	if e.hideGutter {
		return 0
	}
	return gutterW
}

// SetWrapMode enables word-wrap rendering: long lines soft-wrap at word
// boundaries to fit the content width instead of scrolling horizontally, and
// scrolling becomes vertical-only. Off by default; used by plain multi-line text
// boxes like the connection-string editor.
//
// KeyUp/KeyDown/PgUp/PgDn move between logical lines (actual newlines), not
// wrapped visual rows; Left/Right/Home/End/click move within a wrapped line.
//
// A Highlighter applies in wrap mode too: drawWrapped fetches runs per logical
// line and resolves each column through styleAt.
func (e *Editor) SetWrapMode(v bool) { e.wrapMode = v }

// SetReadOnly makes the editor reject every mutating key — typed characters,
// Enter, Backspace/Delete, Tab/Backtab indent, undo/redo, and the line/case/
// comment actions — while cursor movement, selection and Ctrl+A keep working.
// Off by default.
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

// Bounds returns the editor's current screen rect, so a caller outside the
// package can hit-test against it without duplicating its geometry — Object
// Explorer's drag-and-drop target check does.
func (e *Editor) Bounds() core.Rect { return e.rect }

// Focus sets focus state, mirroring the widgets package's Focus(bool)
// convention so Editor can be Tab-cycled alongside InputField, DropDown and
// CheckBox.
func (e *Editor) Focus(v bool) { e.SetActive(v) }

// Text returns the editor content.
func (e *Editor) Text() string {
	var sb strings.Builder
	for i, line := range e.doc.all() {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(line))
	}
	return sb.String()
}

// SetText replaces content, resetting the cursor, selection and undo/redo
// history — all of which refer to the old document and would otherwise dangle:
// a stale selection anchor past the new buffer's end makes SelectedText panic,
// and a stale undo step restores text never typed into this document.
func (e *Editor) SetText(text string) {
	parts := strings.Split(strings.ReplaceAll(expandTabs(text), "\r\n", "\n"), "\n")
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	e.doc.setLines(lines)
	e.cursorRow, e.cursorCol, e.scrollRow, e.scrollCol = 0, 0, 0, 0
	e.selecting, e.selBlock, e.mouseDragging, e.sbDragging, e.sbDraggingX = false, false, false, false, false
	e.undoStack, e.redoStack, e.undoBytes, e.stepOpen = nil, nil, 0, false
	e.closeCompletion()
	e.completionSuppressed = false
}

func (e *Editor) clampCursor() {
	e.cursorRow = core.Clamp(e.cursorRow, 0, e.doc.Len()-1)
	// While a block (column) selection is active, cursorCol doubles as its
	// virtual column and may sit past a short row's length — the expected
	// rectangular visual when the selection started on a longer line. It
	// self-heals the moment selBlock goes false, before any insert or delete.
	if e.cursorRow < e.doc.Len() && !e.selBlock {
		e.cursorCol = core.Clamp(e.cursorCol, 0, len(e.doc.Line(e.cursorRow)))
	}
}

// cursorLine returns the line the cursor is on, or nil if the row is out of
// range — callers measure or index it, and nil measures to zero.
func (e *Editor) cursorLine() []rune {
	if e.cursorRow < 0 || e.cursorRow >= e.doc.Len() {
		return nil
	}
	return e.doc.Line(e.cursorRow)
}

// cursorDisplayCol is the terminal column the caret sits at within its line,
// which is what scrolling, the caret's x, and desiredCol all work in.
func (e *Editor) cursorDisplayCol() int {
	return core.ColumnOfRune(e.cursorLine(), e.cursorCol)
}

// contentH is how many rows of text the editor shows — its full height, less
// the bottom row when that goes to the horizontal scrollbar. Everything asking
// "how many lines fit" uses this rather than rect.H, or the last line scrolls
// under the bar and the cursor can sit on a row that isn't drawn.
func (e *Editor) contentH() int {
	if e.hScrollbarVisible() {
		return e.rect.H - 1
	}
	return e.rect.H
}

func (e *Editor) ensureCursorVisible() {
	contentH := e.contentH()
	if e.wrapMode {
		contentW := e.rect.W - e.gutterWidth()
		vls := e.buildVisualLines(contentW)
		vi := visualIndexForCursor(vls, e.cursorRow, e.cursorCol)
		if vi < e.scrollRow {
			e.scrollRow = vi
		}
		if vi >= e.scrollRow+contentH {
			e.scrollRow = vi - contentH + 1
		}
		if e.scrollRow < 0 {
			e.scrollRow = 0
		}
		return
	}
	if e.cursorRow < e.scrollRow {
		e.scrollRow = e.cursorRow
	}
	if e.cursorRow >= e.scrollRow+contentH {
		e.scrollRow = e.cursorRow - contentH + 1
	}
	contentW := e.rect.W - e.gutterWidth()
	// In display columns: a line of wide characters scrolls twice as far per
	// caret step as an ASCII one, which is what the eye expects.
	curCol := e.cursorDisplayCol()
	if curCol < e.scrollCol {
		e.scrollCol = curCol
	}
	if curCol >= e.scrollCol+contentW {
		e.scrollCol = curCol - contentW + 1
	}
}

// insertRune inserts r at the cursor, going through setLine rather than edit
// whenever the line count is unchanged. This is the typing path, and edit drops
// every cached line width, turning each keystroke into a re-measure of the whole
// buffer.
func (e *Editor) insertRune(r rune) {
	if e.cursorRow >= e.doc.Len() {
		e.doc.edit(func(lines [][]rune) [][]rune { return append(lines, []rune{}) })
	}
	line := e.doc.Line(e.cursorRow)
	nl := make([]rune, len(line)+1)
	copy(nl, line[:e.cursorCol])
	nl[e.cursorCol] = r
	copy(nl[e.cursorCol+1:], line[e.cursorCol:])
	e.doc.setLine(e.cursorRow, nl)
	e.cursorCol++
}

func (e *Editor) insertNewline() {
	e.doc.edit(func(lines [][]rune) [][]rune {
		if e.cursorRow >= len(lines) {
			lines = append(lines, []rune{})
		}
		line := lines[e.cursorRow]
		before := make([]rune, e.cursorCol)
		copy(before, line[:e.cursorCol])
		after := make([]rune, len(line)-e.cursorCol)
		copy(after, line[e.cursorCol:])
		lines[e.cursorRow] = before
		nl := make([][]rune, len(lines)+1)
		copy(nl, lines[:e.cursorRow+1])
		nl[e.cursorRow+1] = after
		copy(nl[e.cursorRow+2:], lines[e.cursorRow+1:])
		return nl
	})
	e.cursorRow++
	e.cursorCol = 0
}

func (e *Editor) backspace() {
	if e.cursorRow == 0 && e.cursorCol == 0 {
		return
	}
	if e.cursorCol > 0 {
		line := e.doc.Line(e.cursorRow)
		e.doc.setLine(e.cursorRow, append(line[:e.cursorCol-1], line[e.cursorCol:]...))
		e.cursorCol--
		return
	}
	e.cursorCol = len(e.doc.Line(e.cursorRow - 1))
	e.doc.edit(func(lines [][]rune) [][]rune {
		lines[e.cursorRow-1] = append(lines[e.cursorRow-1], lines[e.cursorRow]...)
		return append(lines[:e.cursorRow], lines[e.cursorRow+1:]...)
	})
	e.cursorRow--
}

func (e *Editor) deleteChar() {
	if e.cursorRow >= e.doc.Len() {
		return
	}
	line := e.doc.Line(e.cursorRow)
	if e.cursorCol < len(line) {
		e.doc.setLine(e.cursorRow, append(line[:e.cursorCol], line[e.cursorCol+1:]...))
		return
	}
	if e.cursorRow < e.doc.Len()-1 {
		e.doc.edit(func(lines [][]rune) [][]rune {
			lines[e.cursorRow] = append(lines[e.cursorRow], lines[e.cursorRow+1]...)
			return append(lines[:e.cursorRow+1], lines[e.cursorRow+2:]...)
		})
	}
}
