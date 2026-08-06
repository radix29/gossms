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

// Highlighter is a function that receives the whole document and the index
// of the line to highlight, returning that line's ColorRun segments. The
// full buffer (not just the target line) is passed so a highlighter that
// needs cross-line state — a block comment spanning several lines, for
// instance — can look at what precedes idx. Pass nil to disable syntax
// highlighting.
//
// A highlighter that caches such cross-line state must key the cache on
// doc.Version() *and* the *Document itself: the version tells it whether the
// text changed since the last call, and the pointer whether this is even the
// same document. Both built-in highlighters do exactly that — see
// SQLHighlighter.
type Highlighter func(doc *Document, idx int) []ColorRun

// ColorRun describes a coloured segment within an editor line. Start and
// Len are rune indices into the line, not terminal columns — Editor maps
// them to columns when it draws (see drawLineRow).
type ColorRun struct {
	Start int
	Len   int
	Style tcell.Style
}

// editorState is an undo/redo snapshot. bytes is the approximate heap cost
// of lines, computed once at snapshot time so trimUndo never has to walk the
// document again.
type editorState struct {
	lines     [][]rune
	cursorRow int
	cursorCol int
	bytes     int
}

// maxUndoSteps caps the undo stack so unbounded editing doesn't grow it
// forever. Oldest steps are dropped first; the redo stack is unbounded since
// it's cleared on every new edit and can never grow past what undo popped.
const maxUndoSteps = 500

// maxUndoBytes caps the undo stack's total size, because a step count alone
// does not bound memory: each step is a full copy of the buffer's lines, so
// 500 steps of a 20,000-line script is ~2.5 GB. Whichever cap binds first
// wins, and the newest step is always kept even if it alone exceeds this —
// one undo has to work on any document.
const maxUndoBytes = 64 << 20

// sliceHeaderBytes is what one line's []rune header costs on a 64-bit build,
// counted per line so a document of many short lines is not measured as
// nearly free.
const sliceHeaderBytes = 24

// Editor is a multi-line text editor.
//
// Positions inside the text — cursorCol, the selection anchor, ColorRun
// bounds, wrap segments — are rune indices. Everything on screen —
// scrollCol, the cursor's x, a click's x, the horizontal scrollbar — is a
// terminal column. The two are not the same count: a CJK ideograph or emoji
// takes two columns, a combining mark none. core.ColumnOfRune and
// core.RuneIndexAtColumn convert between them, and every conversion goes
// through one of those; treating a rune index as a column is what made a
// wide character shift the rest of its line one column left of where the
// editor thought it was.
//
// One thing stays rune-indexed on purpose: block (column) selection, whose
// rectangle is defined by rune columns, so a block dragged across a line
// containing a wide rune covers a ragged rather than a straight edge.
// Rectangular selection over mixed-width text has no single right answer;
// this is the SSMS-parity choice, not an oversight.
type Editor struct {
	rect core.Rect

	// doc holds the text. It is a pointer because Highlighter is handed the
	// same *Document the Editor mutates, and both must see one version
	// counter — see Document for why the counter has to be unstale-able.
	doc *Document

	cursorRow int
	cursorCol int
	scrollRow int
	scrollCol int // terminal columns scrolled off to the left, not runes
	active    bool
	highlight Highlighter

	// desiredCol is the "goal column" for vertical caret movement — the
	// Notepad++/Scintilla convention where Up/Down/PgUp/PgDn (plain or with
	// Shift, extending a selection) keep aiming for this column even after
	// passing through a shorter line that forced cursorCol to clamp down,
	// so moving back onto a longer line snaps back to where the movement
	// started instead of staying at the shorter line's clamped column. Any
	// other cursor-moving action (typing, Left/Right, Home/End, a mouse
	// click) resets it to the cursor's new column, starting a fresh
	// vertical run.
	//
	// It is a *display* column, not a rune index: the caret has to track the
	// same place on screen down a column of text, which is what the user is
	// aiming at, and rune indices drift from that as soon as a line above
	// contains a wide character.
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

	// styleScratch is drawHighlighted's per-column style map, kept across
	// calls instead of allocated per line. Draw runs on every event the app
	// processes and calls drawHighlighted once per visible row, so a fresh
	// slice per line was one allocation per row per keystroke. Valid only
	// within a single drawHighlighted call — nothing may retain it.
	styleScratch []tcell.Style

	// vlScratch and segScratch are buildVisualLines' buffers. vlScratch also
	// *is* its cache: the flattening it holds stays valid until either the
	// document or the wrap width changes, which the three fields below
	// detect. See buildVisualLines.
	vlScratch  []visualLine
	segScratch []wrapSegment

	vlCacheVersion uint64
	vlCacheWidth   int
	vlCacheValid   bool

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

	// lastClickAt/Row/Col record the previous Button1 press for double-click
	// word selection (see selectWordAt). tcell reports no click count of its
	// own, so the editor pairs presses itself, the same way DataGrid pairs
	// separator presses for its restore-default-width double-click.
	lastClickAt  time.Time
	lastClickRow int
	lastClickCol int

	// blockClip is the text most recently *copied* out of a block (column)
	// selection — see SelectedText. Paste compares its argument against it to
	// tell a block copy round-tripped through the OS clipboard from ordinary
	// multi-line text, which is the only way to know a paste should go back in
	// rectangularly (Notepad++ tracks the same thing internally). Cleared by
	// any non-block copy.
	blockClip string

	// sbDragging is true while the user is dragging the scrollbar thumb
	// (see HandleMouse and drawScrollbar) — a separate flag from
	// mouseDragging since the two gestures target different screen
	// regions and must not be conflated: once a scrollbar drag starts,
	// every subsequent Button1 event controls it regardless of x, instead
	// of being read as a text click/selection-drag.
	sbDragging bool

	// sbDraggingX is sbDragging's horizontal counterpart, for the bar
	// drawn along the editor's bottom row — see hScrollbar.
	sbDraggingX bool

	undoStack []editorState
	redoStack []editorState

	// undoBytes is the sum of undoStack's snapshot sizes, maintained by every
	// push and pop so trimUndo never re-measures the stack.
	undoBytes int

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

	// completionSbDragging is the popup scrollbar's equivalent of
	// completionMouseDown — see Editor's own sbDragging for why this is a
	// separate flag from the click-tracking one above.
	completionSbDragging bool

	// search holds the active find/replace pattern and its match list — see
	// editor_search.go. Zero value means no search, which is every Editor
	// until SetSearch is called on it.
	search editorSearch
}

// NewEditor creates an Editor. Pass a Highlighter or nil.
func NewEditor(h Highlighter) *Editor {
	return new(Editor{
		doc:       newDocument(),
		highlight: h,
	})
}

// Document returns the editor's buffer, for a caller that needs to read the
// text by line without rebuilding it from Text() — and for the tests that
// drive a Highlighter directly. It is the same *Document the editor mutates,
// so its Version() moves under the caller; nothing outside this package can
// write through it.
func (e *Editor) Document() *Document { return e.doc }

// SetHighlighter replaces the syntax highlighter — e.g. switching a query
// editor between SQL and XML highlighting depending on which kind of file
// was just opened into it. Pass nil to disable highlighting.
//
// Pass a Highlighter built for this Editor alone. Both built-in ones
// (SQLHighlighter, XMLHighlighter) close over a cache of the previous line's
// end-of-line comment state, so handing the same Highlighter value to two
// Editors lets one document's carried-over block comment colour the other's.
// Call the constructor per Editor rather than hoisting one into a package
// variable.
func (e *Editor) SetHighlighter(h Highlighter) { e.highlight = h }

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
//
// A Highlighter applies in wrap mode too: drawWrapped fetches runs per
// logical line and resolves each column through styleAt, so the two settings
// compose. (They didn't originally — a wrapped editor rendered as plain text,
// silently, which was harmless only because no wrapping call site had set a
// highlighter yet.)
func (e *Editor) SetWrapMode(v bool) { e.wrapMode = v }

// SetReadOnly makes the editor reject every mutating key — typed
// characters, Enter, Backspace/Delete, Tab/Backtab indent, undo/redo, and
// the line/case/comment actions (Ctrl+D/L/U/Z/Y, Ctrl+/) — while cursor
// movement, Shift/Alt-Shift selection, Ctrl+A, and mouse click-drag
// selection keep working, so the content can still be read and copied.
// Defaults to false.
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
	for i, line := range e.doc.all() {
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
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	e.doc.setLines(lines)
	e.cursorRow, e.cursorCol, e.scrollRow, e.scrollCol = 0, 0, 0, 0
	e.selecting, e.selBlock, e.mouseDragging, e.sbDragging = false, false, false, false
	e.undoStack, e.redoStack, e.undoBytes = nil, nil, 0
	e.closeCompletion()
	e.completionSuppressed = false
}

func (e *Editor) clampCursor() {
	e.cursorRow = core.Clamp(e.cursorRow, 0, e.doc.Len()-1)
	// While a block (column) selection is active, cursorCol doubles as its
	// "virtual column" and is allowed to sit past a short row's actual
	// length — the expected rectangular-selection visual when the
	// selection started on a longer line. It self-heals the moment
	// selBlock goes false, before any insert/delete runs.
	if e.cursorRow < e.doc.Len() && !e.selBlock {
		e.cursorCol = core.Clamp(e.cursorCol, 0, len(e.doc.Line(e.cursorRow)))
	}
}

// cursorLine returns the line the cursor is on, or an empty line if the row
// is somehow out of range — every caller here wants to measure or index it,
// and a nil slice measures to zero rather than panicking.
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

// contentH is how many rows of text the editor actually shows — its full
// height, less the bottom row when that's given over to the horizontal
// scrollbar (see hScrollbar). Every place that treats a height as "how many
// lines fit" uses this rather than rect.H, or the last line would scroll
// under the bar and the cursor could sit on a row that isn't drawn.
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
	// caret step as an ASCII one, which is exactly what the eye expects.
	curCol := e.cursorDisplayCol()
	if curCol < e.scrollCol {
		e.scrollCol = curCol
	}
	if curCol >= e.scrollCol+contentW {
		e.scrollCol = curCol - contentW + 1
	}
}

// insertRune inserts r at the cursor. It goes through setLine rather than
// edit whenever the line count is unchanged, which is every call that isn't
// repairing an out-of-range cursor: this is the typing path, and edit drops
// every cached line width, turning each keystroke into a re-measure of the
// whole buffer.
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

func (e *Editor) pushUndo() {
	st := e.snapshot()
	e.undoStack = append(e.undoStack, st)
	e.undoBytes += st.bytes
	e.trimUndo()
	e.redoStack = nil
}

// trimUndo drops the oldest snapshots until the stack is inside both caps,
// keeping at least one step.
func (e *Editor) trimUndo() {
	drop := 0
	for len(e.undoStack)-drop > 1 &&
		(len(e.undoStack)-drop > maxUndoSteps || e.undoBytes > maxUndoBytes) {
		e.undoBytes -= e.undoStack[drop].bytes
		drop++
	}
	if drop == 0 {
		return
	}
	// Copied into a fresh backing array rather than e.undoStack[drop:]: the
	// latter keeps every dropped snapshot's lines reachable behind the slice
	// header for as long as the editor lives, which is the whole point of
	// dropping them.
	kept := make([]editorState, len(e.undoStack)-drop)
	copy(kept, e.undoStack[drop:])
	e.undoStack = kept
}

func (e *Editor) snapshot() editorState {
	lines := make([][]rune, e.doc.Len())
	bytes := 0
	for i, l := range e.doc.all() {
		nl := make([]rune, len(l))
		copy(nl, l)
		lines[i] = nl
		bytes += len(l)*4 + sliceHeaderBytes
	}
	return editorState{lines, e.cursorRow, e.cursorCol, bytes}
}

func (e *Editor) undo() {
	if len(e.undoStack) == 0 {
		return
	}
	e.redoStack = append(e.redoStack, e.snapshot())
	st := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.undoBytes -= st.bytes
	e.doc.setLines(st.lines)
	e.cursorRow, e.cursorCol = st.cursorRow, st.cursorCol
}

func (e *Editor) redo() {
	if len(e.redoStack) == 0 {
		return
	}
	cur := e.snapshot()
	e.undoStack = append(e.undoStack, cur)
	e.undoBytes += cur.bytes
	e.trimUndo()
	st := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	e.doc.setLines(st.lines)
	e.cursorRow, e.cursorCol = st.cursorRow, st.cursorCol
}
