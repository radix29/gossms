package controls

import (
	"unicode"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Generic completion ("IntelliSense") popup for Editor. Editor knows nothing
// about SQL: it calls the provider after every buffer- or cursor-affecting key
// and draws whatever comes back. Only the SQL query editor sets a provider (see
// internal/tui/completion_provider.go), so this is a no-op for every other
// Editor.
// ---------------------------------------------------------------------------

// CompletionItem is one candidate offered by a CompletionProvider.
type CompletionItem struct {
	// Text is what gets inserted on commit, replacing the span the provider
	// reported via replaceFrom.
	Text string
	// Label is the left column shown in the popup — usually Text, but a provider
	// may show something more readable, such as a plain Label for a
	// bracket-quoted Text.
	Label string
	// Detail is an optional right-aligned, dimmed column ("table", "int, not
	// null").
	Detail string
	// Icon, if non-zero, is drawn as a single-column glyph before Label. Editor
	// assigns it no meaning.
	Icon rune
	// Placeholder marks a row that is shown but can't be navigated to or
	// committed — a "Loading suggestions..." entry while a provider's backing
	// data isn't ready.
	Placeholder bool
}

// CompletionProvider returns the candidates for the identifier being typed at
// (row, col) in lines, and the column that identifier starts at — the span
// [replaceFrom, col) replaced when an item commits. An empty items slice means
// there is nothing to offer here (the cursor is inside a string literal or
// comment), and Editor closes any open popup.
//
// Called from scratch after every key that could affect the result, so a
// provider needs no state of its own between calls.
type CompletionProvider func(lines [][]rune, row, col int) (items []CompletionItem, replaceFrom int)

// maxCompletionRows caps the popup's visible height; more candidates scroll.
const maxCompletionRows = 10

// maxCompletionLabelW and maxCompletionDetailW cap each column's width, so a
// very long identifier or detail can't blow the popup up.
const (
	maxCompletionLabelW  = 40
	maxCompletionDetailW = 24
)

// SetCompletionProvider installs p as the source of completion candidates. nil
// (the default) disables completion, and Ctrl+Space then opens OnRightClick's
// context menu instead.
func (e *Editor) SetCompletionProvider(p CompletionProvider) {
	e.completionProvider = p
	e.closeCompletion()
}

// CompletionActive reports whether the popup is open. A host laying the editor
// out alongside another focusable widget must check this and give the editor
// first refusal of every key and mouse event while true, as with
// DataGrid.OverlayActive.
func (e *Editor) CompletionActive() bool { return e.completionOpen }

// RefreshCompletion re-queries the provider at the current cursor position if
// the popup is open — for a caller whose backing data changed asynchronously and
// wants an open "Loading..." placeholder replaced with real results before the
// next keystroke. No-op while closed, so it is safe to call unconditionally.
func (e *Editor) RefreshCompletion() {
	if e.completionOpen {
		e.updateCompletion()
	}
}

// closeCompletion hides the popup, if open. Safe to call unconditionally.
func (e *Editor) closeCompletion() {
	e.completionOpen = false
	e.completionItems = nil
	e.completionSel = 0
	e.completionScroll = 0
	e.completionMouseDown = false
	e.completionSbDragging = false
}

// updateCompletion re-queries the provider at the current cursor position and
// opens, refreshes or closes the popup to match. Called after every key that
// reached Editor's normal handling, so typing, deleting and cursor movement keep
// the popup in sync without per-key special-casing.
func (e *Editor) updateCompletion() {
	if e.completionProvider == nil || e.readOnly {
		return
	}
	if e.completionSuppressed {
		if e.cursorRow != e.completionSuppressRow || e.currentTokenStart() != e.completionSuppressCol {
			e.completionSuppressed = false
		} else {
			return
		}
	}
	items, from := e.completionProvider(e.doc.all(), e.cursorRow, e.cursorCol)
	if len(items) == 0 {
		e.closeCompletion()
		return
	}
	e.completionItems = items
	e.completionFrom = from
	if !e.completionOpen {
		e.completionSel = e.firstSelectableCompletion(0, 1)
		e.completionScroll = 0
	} else {
		e.completionSel = core.Clamp(e.completionSel, 0, len(items)-1)
	}
	e.completionOpen = true
	e.ensureCompletionVisible()
}

// canAutoOpenCompletion reports whether the text left of the cursor is the
// beginning of a word being typed — the gate HandleKey applies, with typedChar,
// before a typed character opens the popup from closed. The fragment touching
// the cursor must start with a letter or belong to a bracket-quoted identifier
// (right after an opening '[', or the '[' just typed). A space, a '.', a digit
// starting a numeric literal or an empty line never auto-opens it; Ctrl+Space
// always can.
func (e *Editor) canAutoOpenCompletion() bool {
	if e.cursorRow >= e.doc.Len() || e.cursorCol <= 0 {
		return false
	}
	line := e.doc.Line(e.cursorRow)
	if e.cursorCol > len(line) {
		return false
	}
	start := e.cursorCol
	for start > 0 && core.IsWordRune(line[start-1]) {
		start--
	}
	if start == e.cursorCol {
		return line[e.cursorCol-1] == '['
	}
	return unicode.IsLetter(line[start]) || (start > 0 && line[start-1] == '[')
}

// currentTokenStart returns the column where the identifier touching the cursor
// begins — used only to recognise that the cursor is still on the token Escape
// was pressed at. A commit's replace span always comes from the provider.
func (e *Editor) currentTokenStart() int {
	if e.cursorRow >= e.doc.Len() {
		return e.cursorCol
	}
	line := e.doc.Line(e.cursorRow)
	i := core.Clamp(e.cursorCol, 0, len(line))
	for i > 0 && core.IsWordRune(line[i-1]) {
		i--
	}
	return i
}

// triggerCompletionExplicit is Ctrl+Space: query immediately and, if exactly one
// real candidate matches, commit it instead of opening the popup — SSMS's
// "complete word" behaviour.
func (e *Editor) triggerCompletionExplicit() {
	if e.completionProvider == nil || e.readOnly {
		return
	}
	e.completionSuppressed = false
	items, from := e.completionProvider(e.doc.all(), e.cursorRow, e.cursorCol)
	real := 0
	realIdx := -1
	for i, it := range items {
		if !it.Placeholder {
			real++
			realIdx = i
		}
	}
	if real == 1 {
		e.pushUndoLocal()
		e.commitCompletionItem(items[realIdx], from)
		e.closeCompletion()
		return
	}
	if len(items) == 0 {
		e.closeCompletion()
		return
	}
	e.completionItems = items
	e.completionFrom = from
	e.completionOpen = true
	e.completionSel = e.firstSelectableCompletion(0, 1)
	e.completionScroll = 0
	e.ensureCompletionVisible()
}

// firstSelectableCompletion scans completionItems from start in direction dir
// for the first non-Placeholder row, wrapping once. Returns start unchanged when
// every item is a placeholder.
func (e *Editor) firstSelectableCompletion(start, dir int) int {
	n := len(e.completionItems)
	if n == 0 {
		return 0
	}
	i := core.Clamp(start, 0, n-1)
	for range n {
		if !e.completionItems[i].Placeholder {
			return i
		}
		i += dir
		if i < 0 {
			i = n - 1
		} else if i >= n {
			i = 0
		}
	}
	return start
}

// commitCompletionItem replaces [completionFrom, cursorCol) on the current
// row with item.Text and leaves the cursor right after the inserted text.
func (e *Editor) commitCompletionItem(item CompletionItem, from int) {
	if item.Placeholder {
		return
	}
	row := e.cursorRow
	if row >= e.doc.Len() {
		return
	}
	line := e.doc.Line(row)
	from = core.Clamp(from, 0, len(line))
	to := core.Clamp(e.cursorCol, from, len(line))
	text := []rune(item.Text)
	nl := make([]rune, 0, len(line)-(to-from)+len(text))
	nl = append(nl, line[:from]...)
	nl = append(nl, text...)
	nl = append(nl, line[to:]...)
	e.doc.setLine(row, nl)
	e.cursorCol = from + len(text)
	e.desiredCol = e.cursorCol
	e.ensureCursorVisible()
}

// commitSelectedCompletion pushes one undo step, commits the selected candidate
// and closes the popup. No-op on a Placeholder row.
func (e *Editor) commitSelectedCompletion() {
	if e.completionSel < 0 || e.completionSel >= len(e.completionItems) {
		e.closeCompletion()
		return
	}
	item := e.completionItems[e.completionSel]
	if item.Placeholder {
		return
	}
	e.pushUndoLocal()
	e.commitCompletionItem(item, e.completionFrom)
	e.closeCompletion()
}

// dismissCompletion closes the popup and stops it reopening at the same token
// until the cursor moves off it (Escape).
func (e *Editor) dismissCompletion() {
	e.completionSuppressed = true
	e.completionSuppressRow = e.cursorRow
	e.completionSuppressCol = e.currentTokenStart()
	e.closeCompletion()
}

// handleCompletionKey gives the open popup first refusal of a key: list
// navigation, commit and dismiss are consumed here, and everything else falls
// through to HandleKey's normal processing, which calls updateCompletion
// afterwards.
func (e *Editor) handleCompletionKey(ev *tcell.EventKey) bool {
	// A modified key is never popup navigation: Ctrl+Up/Down resize the host's
	// panels, Ctrl+Shift+Up/Down move lines, Shift+arrows extend a selection.
	// They fall through to normal handling, which re-syncs the popup after.
	if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt|tcell.ModShift) != 0 {
		return false
	}
	switch ev.Key() {
	case tcell.KeyUp:
		e.moveCompletionSel(-1)
		return true
	case tcell.KeyDown:
		e.moveCompletionSel(1)
		return true
	case tcell.KeyPgUp:
		e.moveCompletionSel(-maxCompletionRows)
		return true
	case tcell.KeyPgDn:
		e.moveCompletionSel(maxCompletionRows)
		return true
	case tcell.KeyTab, tcell.KeyEnter:
		e.commitSelectedCompletion()
		return true
	case tcell.KeyEscape:
		e.dismissCompletion()
		return true
	}
	return false
}

// moveCompletionSel moves the selection by delta rows, skipping Placeholder rows
// and clamping at either end, as ListBox and DropDown do.
func (e *Editor) moveCompletionSel(delta int) {
	n := len(e.completionItems)
	if n == 0 {
		return
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	i := core.Clamp(e.completionSel+delta, 0, n-1)
	for i >= 0 && i < n && e.completionItems[i].Placeholder {
		i += dir
	}
	if i < 0 || i >= n {
		i = e.firstSelectableCompletion(core.Clamp(e.completionSel+delta, 0, n-1), -dir)
	}
	e.completionSel = i
	e.ensureCompletionVisible()
}

func (e *Editor) ensureCompletionVisible() {
	e.completionScroll = min(e.completionScroll,
		max(0, len(e.completionItems)-maxCompletionRows))
	if e.completionSel < e.completionScroll {
		e.completionScroll = e.completionSel
	}
	if e.completionSel >= e.completionScroll+maxCompletionRows {
		e.completionScroll = e.completionSel - maxCompletionRows + 1
	}
}

// ---------------------------------------------------------------------------
// Mouse
// ---------------------------------------------------------------------------

// handleCompletionMouse gives the open popup first refusal of a mouse event. A
// click outside closes it but returns false, so the click still reaches whatever
// is underneath — as widgets.DropDown does.
func (e *Editor) handleCompletionMouse(ev *tcell.EventMouse) bool {
	rect := e.completionRect()
	mx, my := ev.Position()
	if ev.Buttons() == tcell.ButtonNone {
		e.completionMouseDown = false
		e.completionSbDragging = false
	}

	// Scrollbar drag/click takes priority over the item hit-testing below: the
	// bar is drawn over the rightmost popup column, which would otherwise read as
	// a click on whatever item sits in that row.
	if core.HandleScrollbarDrag(ev, rect.Right()-1, rect.Y, rect.H, len(e.completionItems), &e.completionSbDragging, &e.completionScroll) {
		return true
	}

	switch ev.Buttons() {
	case tcell.WheelUp:
		if rect.Contains(mx, my) {
			e.moveCompletionSel(-1)
			return true
		}
		// Wheel outside the popup scrolls the editor; close first so the popup
		// doesn't ride along anchored to a cursor scrolling out of view.
		e.closeCompletion()
	case tcell.WheelDown:
		if rect.Contains(mx, my) {
			e.moveCompletionSel(1)
			return true
		}
		e.closeCompletion()
	case tcell.Button2:
		// Right-click: close the popup and let the click fall through to the
		// context menu rather than stacking one overlay on the other.
		e.closeCompletion()
	case tcell.Button1:
		if !rect.Contains(mx, my) {
			e.closeCompletion()
			return false
		}
		if e.completionMouseDown {
			// Still the same physical press — don't re-commit on every resend.
			return true
		}
		e.completionMouseDown = true
		idx := e.completionScroll + (my - rect.Y)
		if idx < 0 || idx >= len(e.completionItems) || e.completionItems[idx].Placeholder {
			return true
		}
		if idx == e.completionSel {
			e.commitSelectedCompletion()
		} else {
			e.completionSel = idx
		}
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

// completionColumnWidths computes the label and detail column widths for the
// current completionItems, shared by completionRect and DrawOverlay so the two
// can't disagree about how much space detail got.
func (e *Editor) completionColumnWidths() (labelW, detailW int) {
	for _, it := range e.completionItems {
		if w := core.DisplayWidth(it.Label); w > labelW {
			labelW = w
		}
		if w := core.DisplayWidth(it.Detail); w > detailW {
			detailW = w
		}
	}
	labelW = min(labelW, maxCompletionLabelW)
	detailW = min(detailW, maxCompletionDetailW)
	return labelW, detailW
}

// completionRect computes the popup's on-screen rect, anchored under the start
// of the token being completed, or above it when there is no room below.
func (e *Editor) completionRect() core.Rect {
	if !e.completionOpen {
		return core.Rect{}
	}
	labelW, detailW := e.completionColumnWidths()

	w := 2 + labelW // icon column + space, then label
	if detailW > 0 {
		w += 2 + detailW // gap + detail
	}
	rowCount := min(len(e.completionItems), maxCompletionRows)
	if len(e.completionItems) > maxCompletionRows {
		w++ // scrollbar column
	}
	h := rowCount

	contentX := e.rect.X + e.gutterWidth()
	x := contentX + (e.completionFrom - e.scrollCol)
	// Keep the popup horizontally inside the editor's rect: a token start
	// scrolled off to the left, or near the right edge, must not put it over the
	// gutter or off-screen.
	x = max(e.rect.X, min(x, e.rect.Right()-w))
	y := e.cursorRow - e.scrollRow + e.rect.Y + 1

	// Flip above the cursor line when there isn't room below.
	if y+h > e.rect.Y+e.rect.H {
		above := e.cursorRow - e.scrollRow + e.rect.Y - h
		if above >= 0 {
			y = above
		}
	}
	return core.Rect{X: x, Y: y, W: w, H: h}
}

// DrawOverlay renders the open popup, if any. The popup floats independently of
// the editor's rect, so a host laying the editor out alongside another widget
// must draw this last, as with DataGrid.DrawOverlay.
func (e *Editor) DrawOverlay(s tcell.Screen) {
	if !e.completionOpen {
		return
	}
	rect := e.completionRect()
	p := theme.Active()
	base := theme.StyleDialog()
	core.FillRect(s, rect, ' ', base)

	labelW, detailW := e.completionColumnWidths()

	for row := 0; row < rect.H; row++ {
		idx := e.completionScroll + row
		if idx >= len(e.completionItems) {
			break
		}
		item := e.completionItems[idx]
		y := rect.Y + row
		st := base
		switch {
		case item.Placeholder:
			st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		case idx == e.completionSel:
			st = theme.StyleSelected()
		}
		core.FillRect(s, core.Rect{X: rect.X, Y: y, W: rect.W, H: 1}, ' ', st)

		x := rect.X
		if item.Icon != 0 {
			s.SetContent(x, y, item.Icon, nil, st)
		}
		x += 2
		core.DrawTextClipped(s, x, y, labelW, st, item.Label)
		if detailW > 0 && item.Detail != "" {
			detailSt := st
			if idx != e.completionSel {
				detailSt = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
			}
			core.DrawTextClipped(s, x+labelW+2, y, detailW, detailSt, item.Detail)
		}
	}

	if len(e.completionItems) > rect.H {
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, rect.Right()-1, rect.Y, rect.H, len(e.completionItems), rect.H, e.completionScroll, sbStyle, sbThumb)
	}
}
