package controls

import (
	"unicode"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Generic completion ("IntelliSense") popup for Editor. Editor knows nothing
// about SQL — it just calls the provider after every buffer- or cursor-
// affecting key and draws whatever candidates come back. The SQL query
// editor is the only caller that ever sets a provider (see
// internal/tui/completion_provider.go); every other Editor in the app never
// has one, so this entire feature is a no-op for them.
// ---------------------------------------------------------------------------

// CompletionItem is one candidate offered by a CompletionProvider.
type CompletionItem struct {
	// Text is what gets inserted, replacing the span the provider reported
	// via replaceFrom, when this item is committed.
	Text string
	// Label is the left column shown in the popup — usually Text, but a
	// provider is free to show something more readable (e.g. a bracket-
	// quoted Text with a plain Label).
	Label string
	// Detail is an optional right-aligned, dimmed column — e.g. "table",
	// "int, not null".
	Detail string
	// Icon, if non-zero, is drawn as a single-column glyph before Label.
	// Editor assigns it no meaning of its own.
	Icon rune
	// Placeholder marks a row that's shown but can't be navigated to or
	// committed — e.g. a single "Loading suggestions..." entry shown while
	// a provider's backing data isn't ready yet.
	Placeholder bool
}

// CompletionProvider returns the candidates for the identifier being typed
// at (row, col) in lines, and the column that identifier starts at — the
// span [replaceFrom, col) on row that gets replaced when an item commits.
// Returns a nil/empty items slice when there's nothing to offer at this
// position (e.g. the cursor is inside a string literal or comment); Editor
// closes any open popup in that case.
//
// Called again from scratch after every key that could affect the result —
// a provider does not need to track any state of its own between calls.
type CompletionProvider func(lines [][]rune, row, col int) (items []CompletionItem, replaceFrom int)

// maxCompletionRows caps the popup's visible height; more candidates scroll.
const maxCompletionRows = 10

// maxCompletionLabelW and maxCompletionDetailW cap each column's width so a
// very long identifier or detail string can't blow the popup up past a
// reasonable size.
const (
	maxCompletionLabelW  = 40
	maxCompletionDetailW = 24
)

// SetCompletionProvider installs p as the source of completion candidates.
// Pass nil to disable completion entirely (the default) — every existing
// Editor that never calls this keeps behaving exactly as before, including
// Ctrl+Space opening OnRightClick's context menu.
func (e *Editor) SetCompletionProvider(p CompletionProvider) {
	e.completionProvider = p
	e.closeCompletion()
}

// CompletionActive reports whether the popup is currently open — a host
// that lays the editor out alongside another focusable widget must check
// this and give the editor first refusal of every key/mouse event while
// it's true, the same way DataGrid.OverlayActive() works (see
// controls/datagrid_overlay.go and QueryPanel's use of it).
func (e *Editor) CompletionActive() bool { return e.completionOpen }

// RefreshCompletion re-queries the completion provider at the current
// cursor position if the popup is currently open — for a caller whose
// provider's backing data changed asynchronously (e.g. a background
// inventory load just landed) and wants an already-open "Loading..."
// placeholder to refresh with real results without waiting for the next
// keystroke. No-op while the popup is closed, so it's safe to call
// unconditionally whenever the backing data changes.
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
}

// updateCompletion re-queries the provider at the current cursor position
// and opens, refreshes, or closes the popup to match — called after every
// key that reached Editor's normal handling (see HandleKey), so typing,
// deleting, and moving the cursor all keep the popup in sync without any
// per-key special-casing.
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
	items, from := e.completionProvider(e.lines, e.cursorRow, e.cursorCol)
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

// canAutoOpenCompletion reports whether the text immediately left of the
// cursor is the beginning of a word being typed — the gate HandleKey
// applies, alongside typedChar, before letting a typed character open the
// completion popup fresh from a closed state. The word fragment touching
// the cursor must start with a letter, or belong to a bracket-quoted
// identifier (the fragment sits right after an opening '[', or the '['
// itself was just typed). A bare space, a '.', a digit starting a numeric
// literal, or an empty new line never auto-opens the popup; Ctrl+Space
// always can.
func (e *Editor) canAutoOpenCompletion() bool {
	if e.cursorRow >= len(e.lines) || e.cursorCol <= 0 {
		return false
	}
	line := e.lines[e.cursorRow]
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

// currentTokenStart returns the column where the identifier touching the
// cursor on its current line begins — used only to recognise "the cursor is
// still on the token Escape was pressed at" for completionSuppressed; the
// actual replace span for a commit always comes from the provider itself.
func (e *Editor) currentTokenStart() int {
	if e.cursorRow >= len(e.lines) {
		return e.cursorCol
	}
	line := e.lines[e.cursorRow]
	i := core.Clamp(e.cursorCol, 0, len(line))
	for i > 0 && core.IsWordRune(line[i-1]) {
		i--
	}
	return i
}

// triggerCompletionExplicit is Ctrl+Space: query immediately, and if
// exactly one real (non-placeholder) candidate matches, commit it right
// away instead of opening the popup — SSMS's "complete word" behavior.
func (e *Editor) triggerCompletionExplicit() {
	if e.completionProvider == nil || e.readOnly {
		return
	}
	e.completionSuppressed = false
	items, from := e.completionProvider(e.lines, e.cursorRow, e.cursorCol)
	real := 0
	realIdx := -1
	for i, it := range items {
		if !it.Placeholder {
			real++
			realIdx = i
		}
	}
	if real == 1 {
		e.pushUndo()
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

// firstSelectableCompletion scans completionItems from start in the given
// direction (+1/-1) for the first non-Placeholder row, wrapping once; if
// every item is a placeholder, returns start unchanged.
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
	if row >= len(e.lines) {
		return
	}
	line := e.lines[row]
	from = core.Clamp(from, 0, len(line))
	to := core.Clamp(e.cursorCol, from, len(line))
	text := []rune(item.Text)
	nl := make([]rune, 0, len(line)-(to-from)+len(text))
	nl = append(nl, line[:from]...)
	nl = append(nl, text...)
	nl = append(nl, line[to:]...)
	e.lines[row] = nl
	e.cursorCol = from + len(text)
	e.desiredCol = e.cursorCol
	e.ensureCursorVisible()
}

// commitSelectedCompletion pushes one undo step and commits whichever
// candidate is currently selected, then closes the popup. No-op if the
// selection landed on a Placeholder row (nothing real to insert).
func (e *Editor) commitSelectedCompletion() {
	if e.completionSel < 0 || e.completionSel >= len(e.completionItems) {
		e.closeCompletion()
		return
	}
	item := e.completionItems[e.completionSel]
	if item.Placeholder {
		return
	}
	e.pushUndo()
	e.commitCompletionItem(item, e.completionFrom)
	e.closeCompletion()
}

// dismissCompletion closes the popup and suppresses it from reopening at
// the same token until the cursor moves off that token (Escape).
func (e *Editor) dismissCompletion() {
	e.completionSuppressed = true
	e.completionSuppressRow = e.cursorRow
	e.completionSuppressCol = e.currentTokenStart()
	e.closeCompletion()
}

// handleCompletionKey gives the open popup first refusal of a key: list
// navigation, commit, and dismiss are fully consumed here; every other key
// (including plain typing) is left for HandleKey's normal processing,
// which calls updateCompletion afterward to keep the popup in sync.
func (e *Editor) handleCompletionKey(ev *tcell.EventKey) bool {
	// A modified key is never popup navigation — Ctrl+Up/Down resize the
	// host's panels, Ctrl+Shift+Up/Down move lines, Shift+arrows extend a
	// selection. Let them all fall through to normal handling (which still
	// re-syncs the popup afterward).
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

// moveCompletionSel moves the selection by delta rows, skipping Placeholder
// rows and clamping at either end (no wraparound — matches ListBox/DropDown
// arrow-key behavior elsewhere in tuikit).
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
	e.completionScroll = core.Min(e.completionScroll,
		core.Max(0, len(e.completionItems)-maxCompletionRows))
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

// handleCompletionMouse gives the open popup first refusal of a mouse
// event. A click outside the popup closes it but is left unconsumed
// (returns false) so the click still reaches whatever it would normally hit
// underneath — mirrors widgets.DropDown's outside-click behavior.
func (e *Editor) handleCompletionMouse(ev *tcell.EventMouse) bool {
	rect := e.completionRect()
	mx, my := ev.Position()
	switch ev.Buttons() {
	case tcell.WheelUp:
		if rect.Contains(mx, my) {
			e.moveCompletionSel(-1)
			return true
		}
		// Wheel outside the popup scrolls the editor; close first so the
		// popup doesn't ride along anchored to a cursor that may scroll
		// out of view.
		e.closeCompletion()
	case tcell.WheelDown:
		if rect.Contains(mx, my) {
			e.moveCompletionSel(1)
			return true
		}
		e.closeCompletion()
	case tcell.Button2:
		// Right-click: close the popup and let the click fall through to
		// open the context menu, instead of stacking one overlay on the
		// other.
		e.closeCompletion()
	case tcell.Button1:
		if !rect.Contains(mx, my) {
			e.closeCompletion()
			return false
		}
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

// completionColumnWidths computes the label and detail column widths for
// the current completionItems — shared by completionRect (to size the
// popup) and DrawOverlay (to lay out each row), so the two can never
// disagree about how much space detail actually got.
func (e *Editor) completionColumnWidths() (labelW, detailW int) {
	for _, it := range e.completionItems {
		if w := core.DisplayWidth(it.Label); w > labelW {
			labelW = w
		}
		if w := core.DisplayWidth(it.Detail); w > detailW {
			detailW = w
		}
	}
	labelW = core.Min(labelW, maxCompletionLabelW)
	detailW = core.Min(detailW, maxCompletionDetailW)
	return labelW, detailW
}

// completionRect computes the popup's on-screen rect, anchored under (or,
// if there's no room below, above) the start of the token being completed.
func (e *Editor) completionRect() core.Rect {
	if !e.completionOpen {
		return core.Rect{}
	}
	labelW, detailW := e.completionColumnWidths()

	w := 2 + labelW // icon column + space, then label
	if detailW > 0 {
		w += 2 + detailW // gap + detail
	}
	rowCount := core.Min(len(e.completionItems), maxCompletionRows)
	if len(e.completionItems) > maxCompletionRows {
		w++ // scrollbar column
	}
	h := rowCount

	contentX := e.rect.X + e.gutterWidth()
	x := contentX + (e.completionFrom - e.scrollCol)
	// Keep the popup horizontally inside the editor's rect — a token start
	// scrolled off-view to the left, or one near the right edge, must not
	// put it over the gutter or off-screen.
	x = core.Max(e.rect.X, core.Min(x, e.rect.Right()-w))
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

// DrawOverlay renders the open popup, if any. Must be called after every
// other widget sharing screen space with this editor has drawn — the popup
// floats independently of the editor's own rect, so a host that lays the
// editor out alongside another widget must draw this last, the same as
// DataGrid.DrawOverlay / DropDown.DrawOverlay.
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
