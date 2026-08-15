package controls

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Keyboard and mouse input for Editor (non-wrap-mode; see editor_wrap.go for
// wrap-mode mouse handling)
// ---------------------------------------------------------------------------

// readOnlySafeKey reports whether ev is one of the movement/selection keys
// SetReadOnly(true) still lets through. It inspects Key() only: Shift (extend
// selection) and Ctrl (word-jump) don't change a key's Key() value, so
// Shift+Left still passes, while every mutating key is rejected.
func readOnlySafeKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyCtrlA:
		return true
	}
	return false
}

// HandleKey handles keyboard input.
func (e *Editor) HandleKey(ev *tcell.EventKey) bool {
	// An open completion popup gets first refusal of list-navigation, commit
	// and dismiss keys. Everything else falls through to the normal handling
	// below, which calls updateCompletion at the end to keep it in sync.
	if e.completionOpen && e.handleCompletionKey(ev) {
		return true
	}
	if e.readOnly && !readOnlySafeKey(ev) {
		return false
	}
	hadSelection := e.HasSelection()

	mods := ev.Modifiers()
	ctrlHeld := mods&tcell.ModCtrl != 0
	shiftHeld := mods&tcell.ModShift != 0
	altHeld := mods&tcell.ModAlt != 0

	// typedChar marks a plainly typed character — the only key that can start
	// a completion session from closed, subject to canAutoOpenCompletion's
	// word-start gate. Everything else only re-syncs an already-open popup;
	// deleting or undoing never summons IntelliSense.
	typedChar := false
	switch ev.Key() {
	case tcell.KeyEnter, tcell.KeyBackspace, tcell.KeyBackspace2, tcell.KeyDelete,
		tcell.KeyTab, tcell.KeyCtrlZ, tcell.KeyCtrlY:
	default:
		if r := core.EvRune(ev); r != 0 && !ctrlHeld && !altHeld {
			typedChar = true
		}
	}

	isArrowKey := false
	isMovementKey := false
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
		isArrowKey = true
		isMovementKey = true
	case tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn:
		isMovementKey = true
	}

	// Move Line (Ctrl+Shift+Up/Down) and rectangular block selection
	// (Alt+Shift+Arrow) both ride on movement keys + Shift, so they are carved
	// out of the plain "extend selection" combo below.
	moveLineCombo := (ev.Key() == tcell.KeyUp || ev.Key() == tcell.KeyDown) && ctrlHeld && shiftHeld && !altHeld
	blockCombo := isArrowKey && altHeld && shiftHeld && !e.wrapMode
	linearExtendCombo := isMovementKey && shiftHeld && !altHeld && !moveLineCombo

	extending := blockCombo || linearExtendCombo
	if extending && !e.selecting {
		e.selecting = true
		e.selBlock = blockCombo
		e.selAnchorRow, e.selAnchorCol = e.cursorRow, e.cursorCol
	}
	// dropSelection decides, after the switch below, whether to clear the
	// selection. True by default — any key that isn't a compatible extension
	// drops it — and flipped false by any case managing it itself.
	dropSelection := !extending

	switch ev.Key() {
	case tcell.KeyUp:
		if moveLineCombo {
			e.MoveLinesUp()
			dropSelection = false
		} else if e.cursorRow > 0 {
			e.cursorRow--
			e.cursorCol = e.colForDesired()
		}
	case tcell.KeyDown:
		if moveLineCombo {
			e.MoveLinesDown()
			dropSelection = false
		} else if e.cursorRow < e.doc.Len()-1 {
			e.cursorRow++
			e.cursorCol = e.colForDesired()
		}
	case tcell.KeyLeft:
		if ctrlHeld {
			if e.cursorCol > 0 {
				e.cursorCol = core.WordBoundaryLeft(e.doc.Line(e.cursorRow), e.cursorCol)
			} else if e.cursorRow > 0 && !e.selBlock {
				// Column selection never crosses lines via Left/Right — only
				// Up/Down changes a block's row range. Applies to the
				// word-jump above as well as the plain move below.
				e.cursorRow--
				e.cursorCol = len(e.doc.Line(e.cursorRow))
			}
		} else if e.cursorCol > 0 {
			e.cursorCol--
		} else if e.cursorRow > 0 && !e.selBlock {
			e.cursorRow--
			e.cursorCol = len(e.doc.Line(e.cursorRow))
		}
	case tcell.KeyRight:
		if ctrlHeld {
			if e.cursorRow < e.doc.Len() && e.cursorCol < len(e.doc.Line(e.cursorRow)) {
				e.cursorCol = core.WordBoundaryRight(e.doc.Line(e.cursorRow), e.cursorCol)
			} else if e.cursorRow < e.doc.Len()-1 && !e.selBlock {
				e.cursorRow++
				e.cursorCol = 0
			}
		} else if e.cursorRow < e.doc.Len() && e.cursorCol < len(e.doc.Line(e.cursorRow)) {
			e.cursorCol++
		} else if e.cursorRow < e.doc.Len()-1 && !e.selBlock {
			e.cursorRow++
			e.cursorCol = 0
		}
	case tcell.KeyHome:
		if ctrlHeld {
			e.cursorRow = 0
		}
		e.cursorCol = 0
	case tcell.KeyEnd:
		if ctrlHeld {
			e.cursorRow = e.doc.Len() - 1
		}
		if e.cursorRow < e.doc.Len() {
			e.cursorCol = len(e.doc.Line(e.cursorRow))
		}
	case tcell.KeyCtrlA:
		e.SelectAll()
		dropSelection = false
	case tcell.KeyPgUp:
		e.cursorRow = max(0, e.cursorRow-e.contentH())
		e.cursorCol = e.colForDesired()
	case tcell.KeyPgDn:
		e.cursorRow = min(e.doc.Len()-1, e.cursorRow+e.contentH())
		e.cursorCol = e.colForDesired()
	case tcell.KeyEnter:
		e.pushUndoLocal()
		if hadSelection {
			e.deleteSelection()
		}
		e.insertNewline()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		e.pushUndoLocal()
		switch {
		case e.blockEditing():
			// Block edits keep the block armed for the next key, so they take
			// over from the plain-selection cases below rather than routing
			// through deleteSelection, which drops it.
			e.blockBackspace()
			dropSelection = false
		case hadSelection:
			e.deleteSelection()
		case ctrlHeld:
			e.deleteWordLeft()
		default:
			e.backspace()
		}
	case tcell.KeyDelete:
		e.pushUndoLocal()
		switch {
		case e.blockEditing():
			e.blockDelete()
			dropSelection = false
		case hadSelection:
			e.deleteSelection()
		case ctrlHeld:
			e.deleteWordRight()
		default:
			e.deleteChar()
		}
	case tcell.KeyTab:
		if shiftHeld {
			e.DedentLines()
			dropSelection = false
			break
		}
		if e.blockEditing() {
			e.pushUndoLocal()
			for range indentWidth {
				e.blockInsertRune(' ')
			}
			dropSelection = false
			break
		}
		if hadSelection {
			sr, _, er, _ := e.selectionBounds()
			if sr != er {
				e.IndentLines()
				dropSelection = false
				break
			}
		}
		e.pushUndoLocal()
		if hadSelection {
			e.deleteSelection()
		}
		for range indentWidth {
			e.insertRune(' ')
		}
	case tcell.KeyBacktab:
		// Some terminals report Shift+Tab as this key rather than
		// KeyTab+ModShift. Backtab always implies Shift was held.
		e.DedentLines()
		dropSelection = false
	case tcell.KeyCtrlZ:
		e.undo()
	case tcell.KeyCtrlY:
		e.redo()
	case tcell.KeyCtrlD:
		e.DuplicateLines()
	case tcell.KeyCtrlL:
		e.DeleteLines()
	case tcell.KeyCtrlU:
		if shiftHeld {
			e.UppercaseSelection()
		} else {
			e.LowercaseSelection()
		}
		dropSelection = false
	case tcell.KeyUS:
		// Defensive: tcell v3 doesn't surface the 0x1F byte as KeyUS (see the
		// default case), but a protocol that did would still toggle comments.
		e.ToggleLineComments()
		dropSelection = false
	default:
		r := core.EvRune(ev)
		// Ctrl+/ emits the 0x1F byte on legacy terminals, which tcell v3
		// decodes as KeyRune '_' with ModCtrl (Ctrl+_), not KeyUS; a modern
		// keyboard protocol instead reports it as rune '/'. Accept both.
		if ctrlHeld && (r == '/' || r == '_') {
			e.ToggleLineComments()
			dropSelection = false
			break
		}
		// Ctrl+Space is SSMS's IntelliSense trigger where a completion
		// provider is installed; otherwise it is the keyboard equivalent of a
		// right-click, opening OnRightClick's Cut/Copy/Paste menu at the text
		// cursor rather than a click position.
		if ctrlHeld && r == ' ' {
			if e.completionProvider != nil {
				e.triggerCompletionExplicit()
			} else if e.OnRightClick != nil {
				x, y := e.cursorScreenPos()
				e.OnRightClick(x, y)
			}
			dropSelection = false
			break
		}
		if r != 0 && !ctrlHeld && !altHeld {
			e.pushUndoLocal()
			if e.blockEditing() {
				// One character into every row the block spans, leaving it
				// armed a column further right so the next keystroke follows.
				e.blockInsertRune(r)
				dropSelection = false
				break
			}
			if hadSelection {
				e.deleteSelection()
			}
			e.insertRune(r)
		} else {
			if dropSelection {
				e.selecting = false
				e.selBlock = false
			}
			return false
		}
	}
	if dropSelection {
		e.selecting = false
		e.selBlock = false
	}
	e.clampCursor()
	// Vertical movement preserves the goal column instead of resetting it;
	// every other cursor-moving key re-anchors it to where the cursor ended.
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
	default:
		e.desiredCol = e.cursorDisplayCol()
	}
	e.ensureCursorVisible()
	// A typed character may only open the popup from closed when the cursor
	// sits at the end of a word starting with a letter or '['. While it is
	// already open, every key reaching here re-syncs it.
	if e.completionOpen || (typedChar && e.canAutoOpenCompletion()) {
		e.updateCompletion()
	}
	return true
}

// HandleMouse handles mouse events.
func (e *Editor) HandleMouse(ev *tcell.EventMouse) bool {
	// Same reasoning as HandleKey's completionOpen check: the popup floats
	// independently of the editor's rect, so it gets first refusal before any
	// position-based routing can misinterpret a click meant for it.
	if e.completionOpen && e.handleCompletionMouse(ev) {
		return true
	}
	// Always process a release first, regardless of position, so a drag ending
	// outside the editor's bounds still terminates cleanly instead of leaving
	// mouseDragging stuck true.
	if ev.Buttons() == tcell.ButtonNone {
		wasDragging := e.mouseDragging || e.sbDragging || e.sbDraggingX
		e.mouseDragging = false
		e.sbDragging = false
		e.sbDraggingX = false
		return wasDragging
	}

	// A horizontal-scrollbar drag keeps control once started, even after the
	// pointer leaves the editor, so it is checked before the bounds test —
	// unlike the vertical bar, whose track runs the full content height and is
	// far harder to drag off. Mirrors DataGrid.
	if e.hScrollbarDrag(ev) {
		return true
	}

	mx, my := ev.Position()
	contentX := e.rect.X + e.gutterWidth()
	if mx < contentX || !e.rect.Contains(mx, my) {
		return false
	}
	// Right-click (Button2 is Secondary in tcell v3): hand off to the app layer
	// for a context menu, in both wrap modes, without disturbing the cursor or
	// selection.
	if ev.Buttons() == tcell.Button2 {
		if e.OnRightClick != nil {
			e.OnRightClick(mx, my)
		}
		return true
	}

	// Scrollbar drag/click takes priority over the text-click handling below:
	// the bar is drawn over the rightmost content column, which would otherwise
	// read as a click positioning the cursor at that line's end. total mirrors
	// what drawScrollbar passed to core.DrawScrollbar — visual rows in wrap
	// mode, logical lines otherwise. vls is reused below by handleMouseWrapped
	// rather than recomputing the same O(document) slice for this event.
	var vls []visualLine
	haveVLS := false
	if ev.Buttons() == tcell.Button1 {
		total := e.doc.Len()
		if e.wrapMode {
			vls = e.buildVisualLines(e.rect.W - e.gutterWidth())
			haveVLS = true
			total = len(vls)
		}
		if core.HandleScrollbarDrag(ev, e.rect.Right()-1, e.rect.Y, e.contentH(), total, &e.sbDragging, &e.scrollRow) {
			return true
		}
	}

	if e.wrapMode {
		if !haveVLS {
			vls = e.buildVisualLines(e.rect.W - e.gutterWidth())
		}
		return e.handleMouseWrapped(ev, mx, my, contentX, vls)
	}
	if ev.Buttons() == tcell.Button1 {
		row := core.Clamp(e.scrollRow+min(my-e.rect.Y, e.contentH()-1), 0, e.doc.Len()-1)
		col := e.runeColAtScreenX(row, mx-contentX)
		if !e.mouseDragging {
			// Fresh click: reposition the cursor. Without Shift, arm a new
			// anchor here (HasSelection() stays false until the drag moves
			// off this point). With Shift, keep whatever anchor is active —
			// or the pre-click cursor — and move only the cursor, the
			// click-to-extend behaviour most editors give Shift+Click. Alt on
			// the press picks block vs. linear selection for the whole drag,
			// best-effort since terminals vary in reporting it.
			e.mouseDragging = true
			// A second unmodified press on the same spot selects the word
			// under it. mouseDragging is latched above, so the resends while
			// the button stays down land in the drag branch and extend from
			// the word instead of re-selecting it. pressIsDouble runs for
			// every fresh press, modified or not, so a Shift- or Alt-clicked
			// press still counts as "the previous press" for the next one —
			// which is why the modifier test lives inside it, not in an &&
			// here that would discard the press before consulting it.
			if e.pressIsDouble(row, col, ev.When(), ev.Modifiers()) {
				e.selectWordAt(row, col)
				return true
			}
			if ev.Modifiers()&tcell.ModShift != 0 {
				if !e.selecting {
					e.selAnchorRow, e.selAnchorCol = e.cursorRow, e.cursorCol
				}
			} else {
				e.selAnchorRow, e.selAnchorCol = row, col
			}
			e.selecting = true
			e.selBlock = ev.Modifiers()&tcell.ModAlt != 0
			e.cursorRow, e.cursorCol = row, col
		} else {
			// Continued drag: move the cursor, anchor and mode stay fixed.
			e.cursorRow, e.cursorCol = row, col
		}
		e.desiredCol = core.ColumnOfRune(e.doc.Line(row), col)
		return true
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		// Shift+wheel is the desktop convention for horizontal scroll, and
		// some terminals report it that way rather than as
		// WheelLeft/WheelRight below, so honour both. Matches DataGrid.
		if ev.Modifiers()&tcell.ModShift != 0 {
			e.scrollColBy(-horizontalWheelChars)
		} else if e.scrollRow > 0 {
			e.scrollRow--
		}
		return true
	case tcell.WheelDown:
		if ev.Modifiers()&tcell.ModShift != 0 {
			e.scrollColBy(horizontalWheelChars)
		} else if e.scrollRow < e.doc.Len()-1 {
			e.scrollRow++
		}
		return true
	case tcell.WheelLeft:
		e.scrollColBy(-horizontalWheelChars)
		return true
	case tcell.WheelRight:
		e.scrollColBy(horizontalWheelChars)
		return true
	}
	return false
}

// doubleClickInterval is how close two presses on the same text position must
// be to count as a double-click. Same value as DataGrid's
// resizeDoubleClickInterval, so the app has one double-click speed.
const doubleClickInterval = 500 * time.Millisecond

// pressIsDouble reports whether an unmodified press at (row, col) follows a
// previous press at the same position closely enough to count as a
// double-click, and records this press for the next call.
//
// mod is taken here rather than tested by the caller because only the false
// path records the press: a modified press must reach the recording branch, or
// it leaves no "previous press" for the one after it.
func (e *Editor) pressIsDouble(row, col int, at time.Time, mod tcell.ModMask) bool {
	double := mod == tcell.ModNone &&
		row == e.lastClickRow && col == e.lastClickCol &&
		!e.lastClickAt.IsZero() && at.Sub(e.lastClickAt) <= doubleClickInterval
	if double {
		// Don't let a third press pair with this one as well.
		e.lastClickAt = time.Time{}
		return true
	}
	e.lastClickRow, e.lastClickCol, e.lastClickAt = row, col, at
	return false
}

// hScrollbarDrag handles a Button1 press or drag on the horizontal scrollbar.
// Unlike DataGrid's equivalent it can delegate to core.HandleScrollbarDragH
// as-is: that helper treats the track's width as the visible count, and here
// the two are the same number of rune columns. Latches sbDraggingX for the rest
// of the gesture so the thumb follows the pointer off the bar's row. Returns
// false for a non-qualifying event, so the caller can chain it ahead of its own
// hit-testing.
func (e *Editor) hScrollbarDrag(ev *tcell.EventMouse) bool {
	// A text-selection drag in progress owns the rest of its gesture: the track
	// spans the full content width, so a selection dragged past the last line
	// would otherwise land on the bar's row and yank the view sideways.
	if e.mouseDragging && !e.sbDraggingX {
		return false
	}
	x, y, w, total, _, ok := e.hScrollbar()
	if !ok {
		return false
	}
	return core.HandleScrollbarDragH(ev, x, y, w, total, &e.sbDraggingX, &e.scrollCol)
}

// SetCursorFromScreen moves the cursor to the document position under (x, y)
// and clears any selection — HandleMouse's fresh-click targeting math, exposed
// for callers placing the cursor without synthesizing a mouse event, such as
// Object Explorer's drag-and-drop.
func (e *Editor) SetCursorFromScreen(x, y int) {
	contentX := e.rect.X + e.gutterWidth()
	row := core.Clamp(e.scrollRow+min(y-e.rect.Y, e.contentH()-1), 0, e.doc.Len()-1)
	col := e.runeColAtScreenX(row, x-contentX)
	e.cursorRow, e.cursorCol = row, col
	e.selecting, e.selBlock, e.mouseDragging, e.sbDragging, e.sbDraggingX = false, false, false, false, false
	e.desiredCol = core.ColumnOfRune(e.doc.Line(row), col)
	e.ensureCursorVisible()
}

// runeColAtScreenX converts an x offset within the content area into a rune
// index on the given row, clamped to the line's end. dx is a terminal-column
// offset and the result a rune index, so this is where a wide character earlier
// on the line is accounted for — reading dx as a rune index directly put the
// caret left of the click on any line containing one.
func (e *Editor) runeColAtScreenX(row, dx int) int {
	if row < 0 || row >= e.doc.Len() {
		return 0
	}
	line := e.doc.Line(row)
	return min(core.RuneIndexAtColumn(line, max(0, e.scrollCol+dx)), len(line))
}

// colForDesired maps desiredCol — a display column, see Editor.desiredCol —
// onto a rune index on the cursor's current row, clamped to its end. This is
// the goal-column half of vertical caret movement.
func (e *Editor) colForDesired() int {
	line := e.cursorLine()
	return min(core.RuneIndexAtColumn(line, e.desiredCol), len(line))
}

// horizontalWheelChars is how many characters one horizontal wheel tick
// scrolls — meaningful only outside wrapMode, where scrollCol is a character
// offset rather than unused.
const horizontalWheelChars = 4

// scrollColBy shifts scrollCol by delta (negative scrolls left), clamped so
// it can't scroll past showing at least the last character of the buffer's
// longest line.
func (e *Editor) scrollColBy(delta int) {
	e.scrollCol = core.Clamp(e.scrollCol+delta, 0, max(0, e.doc.maxDisplayWidth()-1))
}
