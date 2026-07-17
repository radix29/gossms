package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Keyboard and mouse input for Editor (non-wrap-mode; see editor_wrap.go for
// wrap-mode mouse handling)
// ---------------------------------------------------------------------------

// readOnlySafeKey reports whether ev is one of the movement/selection keys
// SetReadOnly(true) still allows through to HandleKey. It only inspects
// Key() — modifiers like Shift (extend selection) or Ctrl (word-jump) on
// one of these keys don't change its Key() value, so Shift+Left etc. still
// pass — every mutating key (typed runes, Enter, Backspace/Delete, Tab,
// undo/redo, line/case/comment actions, …) is rejected instead.
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
	// The completion popup, if open, gets first refusal of list-navigation,
	// commit, and dismiss keys — see handleCompletionKey's doc comment.
	// Every other key (typing, Backspace/Delete, arrows that should close
	// it, ...) falls through to the normal handling below, which calls
	// updateCompletion at the end to keep the popup in sync.
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

	// typedChar marks a plainly typed character — the only kind of key that
	// can start a fresh completion session from closed, subject to
	// canAutoOpenCompletion's word-start gate below. Everything else —
	// Backspace/Delete, Enter, Tab, undo/redo, cursor movement — only
	// re-syncs a popup that's already open; deleting or undoing never
	// summons IntelliSense on its own.
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
	// (Alt+Shift+Arrow) both ride on movement keys + Shift, so they're
	// carved out of the plain "extend selection" combo below and handled
	// as their own cases further down.
	moveLineCombo := (ev.Key() == tcell.KeyUp || ev.Key() == tcell.KeyDown) && ctrlHeld && shiftHeld && !altHeld
	blockCombo := isArrowKey && altHeld && shiftHeld && !e.wrapMode
	linearExtendCombo := isMovementKey && shiftHeld && !altHeld && !moveLineCombo

	extending := blockCombo || linearExtendCombo
	if extending && !e.selecting {
		e.selecting = true
		e.selBlock = blockCombo
		e.selAnchorRow, e.selAnchorCol = e.cursorRow, e.cursorCol
	}
	// dropSelection decides, after the switch below runs, whether to clear
	// the selection — starts true (matching "any key that isn't a
	// compatible extension drops the selection") and is flipped to false
	// by any case that manages the selection's lifecycle itself.
	dropSelection := !extending

	switch ev.Key() {
	case tcell.KeyUp:
		if moveLineCombo {
			e.MoveLinesUp()
			dropSelection = false
		} else if e.cursorRow > 0 {
			e.cursorRow--
			e.cursorCol = core.Min(e.desiredCol, len(e.lines[e.cursorRow]))
		}
	case tcell.KeyDown:
		if moveLineCombo {
			e.MoveLinesDown()
			dropSelection = false
		} else if e.cursorRow < len(e.lines)-1 {
			e.cursorRow++
			e.cursorCol = core.Min(e.desiredCol, len(e.lines[e.cursorRow]))
		}
	case tcell.KeyLeft:
		if ctrlHeld {
			if e.cursorCol > 0 {
				e.cursorCol = core.WordBoundaryLeft(e.lines[e.cursorRow], e.cursorCol)
			} else if e.cursorRow > 0 {
				e.cursorRow--
				e.cursorCol = len(e.lines[e.cursorRow])
			}
		} else if e.cursorCol > 0 {
			e.cursorCol--
		} else if e.cursorRow > 0 && !e.selBlock {
			// Column selection never crosses lines via Left/Right — only
			// Up/Down changes a block selection's row range.
			e.cursorRow--
			e.cursorCol = len(e.lines[e.cursorRow])
		}
	case tcell.KeyRight:
		if ctrlHeld {
			if e.cursorRow < len(e.lines) && e.cursorCol < len(e.lines[e.cursorRow]) {
				e.cursorCol = core.WordBoundaryRight(e.lines[e.cursorRow], e.cursorCol)
			} else if e.cursorRow < len(e.lines)-1 {
				e.cursorRow++
				e.cursorCol = 0
			}
		} else if e.cursorRow < len(e.lines) && e.cursorCol < len(e.lines[e.cursorRow]) {
			e.cursorCol++
		} else if e.cursorRow < len(e.lines)-1 && !e.selBlock {
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
			e.cursorRow = len(e.lines) - 1
		}
		if e.cursorRow < len(e.lines) {
			e.cursorCol = len(e.lines[e.cursorRow])
		}
	case tcell.KeyCtrlA:
		e.SelectAll()
		dropSelection = false
	case tcell.KeyPgUp:
		e.cursorRow = core.Max(0, e.cursorRow-e.rect.H)
		e.cursorCol = core.Min(e.desiredCol, len(e.lines[e.cursorRow]))
	case tcell.KeyPgDn:
		e.cursorRow = core.Min(len(e.lines)-1, e.cursorRow+e.rect.H)
		e.cursorCol = core.Min(e.desiredCol, len(e.lines[e.cursorRow]))
	case tcell.KeyEnter:
		e.pushUndo()
		if hadSelection {
			e.deleteSelection()
		}
		e.insertNewline()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		e.pushUndo()
		switch {
		case hadSelection:
			e.deleteSelection()
		case ctrlHeld:
			e.deleteWordLeft()
		default:
			e.backspace()
		}
	case tcell.KeyDelete:
		e.pushUndo()
		switch {
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
		if hadSelection {
			sr, _, er, _ := e.selectionBounds()
			if sr != er {
				e.IndentLines()
				dropSelection = false
				break
			}
		}
		e.pushUndo()
		if hadSelection {
			e.deleteSelection()
		}
		for range indentWidth {
			e.insertRune(' ')
		}
	case tcell.KeyBacktab:
		// Some terminals report Shift+Tab as this distinct key instead of
		// KeyTab+ModShift (see the identical precedent for Ctrl+Tab at
		// app_events.go). Backtab always implies Shift was held.
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
		// Defensive fallback: tcell v3 doesn't actually surface the 0x1F
		// byte as KeyUS (see the default case), but a terminal or protocol
		// that ever did would still toggle comments here.
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
		// Ctrl+Space is SSMS's IntelliSense trigger when a completion
		// provider is installed (the SQL query editor); otherwise it's the
		// keyboard equivalent of right-clicking the editor — opens the same
		// Cut/Copy/Paste context menu as OnRightClick's mouse path,
		// positioned at the text cursor instead of a click position.
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
			e.pushUndo()
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
	// Vertical movement (see desiredCol's doc comment) reads and preserves
	// the goal column instead of resetting it; every other key that can
	// move the cursor re-anchors it to wherever the cursor ended up.
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
	default:
		e.desiredCol = e.cursorCol
	}
	e.ensureCursorVisible()
	// A typed character may only pop the popup open fresh (from closed)
	// when the cursor sits at the end of a word being typed that starts
	// with a letter or '[' — see canAutoOpenCompletion. While the popup is
	// already open, every key that reaches here re-syncs it regardless.
	if e.completionOpen || (typedChar && e.canAutoOpenCompletion()) {
		e.updateCompletion()
	}
	return true
}

// HandleMouse handles mouse events.
func (e *Editor) HandleMouse(ev *tcell.EventMouse) bool {
	// Same reasoning as the completionOpen check at the top of HandleKey:
	// the popup floats independently of the editor's own rect, so it must
	// get first refusal of every mouse event before any position-based
	// routing below gets a chance to misinterpret a click meant for it.
	if e.completionOpen && e.handleCompletionMouse(ev) {
		return true
	}
	// Always process a release first, regardless of position, so a drag
	// that ends outside the editor's bounds (e.g. dragged into the
	// results pane below) still terminates cleanly instead of leaving
	// mouseDragging stuck true.
	if ev.Buttons() == tcell.ButtonNone {
		wasDragging := e.mouseDragging
		e.mouseDragging = false
		return wasDragging
	}

	mx, my := ev.Position()
	contentX := e.rect.X + e.gutterWidth()
	if mx < contentX || !e.rect.Contains(mx, my) {
		return false
	}
	// Right-click (tcell v3: Button2 is Secondary): hand off to the app
	// layer for a context menu, in both wrap and non-wrap mode, without
	// disturbing the cursor or selection.
	if ev.Buttons() == tcell.Button2 {
		if e.OnRightClick != nil {
			e.OnRightClick(mx, my)
		}
		return true
	}
	if e.wrapMode {
		return e.handleMouseWrapped(ev, mx, my, contentX)
	}
	if ev.Buttons() == tcell.Button1 {
		row := core.Clamp(e.scrollRow+(my-e.rect.Y), 0, len(e.lines)-1)
		col := core.Max(0, e.scrollCol+(mx-contentX))
		if row < len(e.lines) && col > len(e.lines[row]) {
			col = len(e.lines[row])
		}
		if !e.mouseDragging {
			// Fresh click: reposition the cursor. Without Shift, arm a new
			// selection anchor here (HasSelection() stays false until the
			// drag moves away from this point). With Shift, extend the
			// existing selection instead — keep whatever anchor is already
			// active (or the pre-click cursor position, if there wasn't one
			// yet) and just move the cursor to the click, the
			// "click-to-extend" behavior most text editors give
			// Shift+Click. Alt held on the press decides block (column) vs.
			// linear selection for the whole drag either way — best-effort,
			// since terminal reporting of Alt as a mouse modifier varies.
			e.mouseDragging = true
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
		e.desiredCol = col
		return true
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		// Shift+wheel is the common desktop convention for horizontal
		// scroll; some terminals report it as WheelUp/WheelDown with a
		// Shift modifier rather than as WheelLeft/WheelRight below, so
		// honour both — matches DataGrid's identical convention.
		if ev.Modifiers()&tcell.ModShift != 0 {
			e.scrollColBy(-horizontalWheelChars)
		} else if e.scrollRow > 0 {
			e.scrollRow--
		}
		return true
	case tcell.WheelDown:
		if ev.Modifiers()&tcell.ModShift != 0 {
			e.scrollColBy(horizontalWheelChars)
		} else if e.scrollRow < len(e.lines)-1 {
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

// SetCursorFromScreen moves the cursor to the document position under the
// screen coordinate (x, y) and clears any active selection — the same
// targeting math HandleMouse's Button1 case uses for a fresh click, exposed
// for callers that need to place the cursor without synthesizing a mouse
// event (e.g. Object Explorer's drag-and-drop, which already knows the drop
// point from the release event it's handling at the App level).
func (e *Editor) SetCursorFromScreen(x, y int) {
	contentX := e.rect.X + e.gutterWidth()
	row := core.Clamp(e.scrollRow+(y-e.rect.Y), 0, len(e.lines)-1)
	col := core.Max(0, e.scrollCol+(x-contentX))
	if row < len(e.lines) && col > len(e.lines[row]) {
		col = len(e.lines[row])
	}
	e.cursorRow, e.cursorCol = row, col
	e.selecting, e.selBlock, e.mouseDragging = false, false, false
	e.desiredCol = col
	e.ensureCursorVisible()
}

// horizontalWheelChars is how many characters a single horizontal wheel
// tick (WheelLeft/WheelRight, or Shift+WheelUp/WheelDown) scrolls — only
// meaningful outside wrapMode, where scrollCol is a character offset
// rather than unused.
const horizontalWheelChars = 4

// scrollColBy shifts scrollCol by delta (negative scrolls left), clamped so
// it can't scroll past showing at least the last character of the buffer's
// longest line.
func (e *Editor) scrollColBy(delta int) {
	longest := 0
	for _, l := range e.lines {
		if len(l) > longest {
			longest = len(l)
		}
	}
	e.scrollCol = core.Clamp(e.scrollCol+delta, 0, core.Max(0, longest-1))
}
