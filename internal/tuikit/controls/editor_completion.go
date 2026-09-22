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
	// Label is the left column shown in the popup — usually Text, but a
	// provider may show something more readable, such as a plain Label for a
	// bracket-quoted Text.
	Label string
	// Detail is an optional right-aligned, dimmed column ("table", "int, not
	// null").
	Detail string
	// Icon, if non-zero, is drawn as a single-column glyph before Label. Editor
	// assigns it no meaning.
	Icon rune
	// Placeholder marks a row shown but not navigable or committable — a
	// "Loading suggestions..." entry while a provider's data isn't ready.
	Placeholder bool
	// Partial marks a candidate that matched the typed text somewhere other
	// than its start ("ord" in CustomerOrders). A popup the user didn't ask for
	// with Ctrl+Space, holding only partial matches, opens with nothing
	// selected: Enter and Tab then keep their plain meaning, so a keyword typed
	// in full ("BY", "AND") is never swapped for a column that merely contains
	// it (CreatedBy, BrandName). Up/Down select one as usual.
	Partial bool
}

// TextRevision identifies the revision of the text a CompletionRequest
// carries, so a provider keeping a cache across calls can tell whether it may
// resume. Same key prefixStates uses, for the same reason: Doc pins which
// buffer the version counts, since two buffers number their versions
// independently from zero, and DirtyFrom describes one mutation only — a cache
// more than one version behind must start over.
//
// Doc is opaque on purpose: a provider compares it for identity and nothing
// else.
type TextRevision struct {
	// Doc identifies the buffer. Compare it, don't dereference it.
	Doc any
	// Version is the buffer's mutation counter.
	Version uint64
	// DirtyFrom is the lowest line index the last mutation could have changed
	// the meaning of. Meaningful only when Version is exactly one ahead of what
	// the provider last saw for the same Doc.
	DirtyFrom int
}

// CompletionRequest is what Editor tells a provider about where and what to
// complete.
type CompletionRequest struct {
	// Lines is the whole buffer. Read-only: Editor keeps using these slices.
	Lines [][]rune
	// Row, Col is the cursor.
	Row, Col int
	// Text identifies this revision of Lines — see TextRevision.
	Text TextRevision
}

// CompletionProvider returns the candidates for the identifier being typed at
// (Row, Col) in Lines, and the column that identifier starts at — the span
// [replaceFrom, Col) replaced when an item commits. An empty items slice means
// there is nothing to offer here (the cursor is inside a string literal or
// comment), and Editor closes any open popup.
//
// Called after every key that could affect the result. A provider may answer
// each call from scratch; one that caches between calls must key the cache on
// req.Text and rebuild whenever that key cannot justify a resume.
type CompletionProvider func(req CompletionRequest) (items []CompletionItem, replaceFrom int)

// maxCompletionRows caps the popup's visible height; more candidates scroll.
const maxCompletionRows = 10

// maxCompletionLabelW and maxCompletionDetailW cap each column's width, so a
// long identifier or detail can't blow the popup up.
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

// RefreshCompletion re-queries the provider at the cursor if the popup is
// open, for a caller whose backing data arrived asynchronously and wants an
// open "Loading..." placeholder replaced before the next keystroke. No-op
// while closed, so it is safe to call unconditionally.
func (e *Editor) RefreshCompletion() {
	if e.completionOpen {
		e.updateCompletion()
	}
}

// closeCompletion hides the popup, if open. Safe to call unconditionally.
func (e *Editor) closeCompletion() {
	e.completionOpen = false
	e.completionExplicit = false
	e.completionItems = nil
	e.completionSel = 0
	e.completionScroll = 0
	e.completionMouseDown = false
	e.completionSbDragging = false
}

// completionRequest packages the cursor position and the buffer's current
// revision for a provider call. Both call sites go through it so they cannot
// disagree about which revision the lines belong to.
func (e *Editor) completionRequest() CompletionRequest {
	return CompletionRequest{
		Lines: e.doc.all(),
		Row:   e.cursorRow,
		Col:   e.cursorCol,
		Text: TextRevision{
			Doc:       e.doc,
			Version:   e.doc.Version(),
			DirtyFrom: e.doc.dirtyFrom,
		},
	}
}

// updateCompletion re-queries the provider at the cursor and opens, refreshes
// or closes the popup to match. Called after every key that reached Editor's
// normal handling, so typing, deleting and cursor movement keep the popup in
// sync without per-key special-casing.
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
	items, from := e.completionProvider(e.completionRequest())
	if len(items) == 0 {
		e.closeCompletion()
		return
	}
	e.completionItems = items
	e.completionFrom = from
	switch {
	case !e.completionExplicit && !hasPrefixCompletion(items):
		e.completionSel = -1
		e.completionScroll = 0
	case !e.completionOpen || e.completionSel < 0:
		e.completionSel = e.firstSelectableCompletion(0, 1)
		e.completionScroll = 0
	default:
		e.completionSel = core.Clamp(e.completionSel, 0, len(items)-1)
	}
	e.completionOpen = true
	e.ensureCompletionVisible()
}

// hasPrefixCompletion reports whether items holds a selectable candidate that
// matched at its start — see CompletionItem.Partial.
func hasPrefixCompletion(items []CompletionItem) bool {
	for _, it := range items {
		if !it.Placeholder && !it.Partial {
			return true
		}
	}
	return false
}

// canAutoOpenCompletion reports whether the text left of the cursor begins a
// word being typed — the gate HandleKey applies, with typedChar, before a typed
// character opens the popup from closed. The fragment touching the cursor must
// start with a letter or one of the sigils a name can open with: '[' for a
// quoted identifier, '#' or '@' for a name a host's provider may bind. Each
// sigil also opens the popup on its own, since the name it introduces has no
// other first keystroke. A space, a '.', a digit starting a numeric literal or
// an empty line never auto-opens it; Ctrl+Space always can.
//
// What a sigil means is the provider's business — this only decides that a name
// may be starting, and a provider with nothing to offer closes the popup again
// on the same keystroke.
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
		return isNameSigil(line[e.cursorCol-1])
	}
	return unicode.IsLetter(line[start]) || (start > 0 && isNameSigil(line[start-1]))
}

// isNameSigil reports whether r can introduce a name the completion provider
// might know: a bracket-quoted identifier, or T-SQL's '#'/'@'.
func isNameSigil(r rune) bool { return r == '[' || r == '#' || r == '@' }

// currentTokenStart returns the column where the identifier touching the cursor
// begins — used only to recognise that the cursor is still on the token Escape
// was pressed at. A commit's replace span comes from the provider.
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

// triggerCompletionExplicit is Ctrl+Space: query immediately and, if a word
// has been started and exactly one real candidate matches it at its start,
// commit it instead of opening the popup — SSMS's "complete word" behaviour.
// With nothing typed yet the popup always opens, even over a single candidate:
// the user asked to see the list, not for a guess. The popup stays explicit
// until it closes, so partial matches keep a selection while typing narrows it.
func (e *Editor) triggerCompletionExplicit() {
	if e.completionProvider == nil || e.readOnly {
		return
	}
	e.completionSuppressed = false
	items, from := e.completionProvider(e.completionRequest())
	real := 0
	realIdx := -1
	for i, it := range items {
		if !it.Placeholder {
			real++
			realIdx = i
		}
	}
	if real == 1 && from < e.cursorCol && !items[realIdx].Partial {
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
	e.completionExplicit = true
	e.completionSel = e.firstSelectableCompletion(0, 1)
	e.completionScroll = 0
	e.ensureCompletionVisible()
}

// firstSelectableCompletion scans completionItems from start in direction dir
// for the first non-Placeholder row, wrapping once. Returns start unchanged
// when every item is a placeholder.
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
	e.desiredCol = e.cursorDisplayCol()
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
// navigation, commit and dismiss are consumed here; everything else falls
// through to HandleKey's normal processing, which calls updateCompletion after.
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
		if e.completionSel < 0 {
			// Nothing selected (see CompletionItem.Partial): the key keeps its
			// plain meaning, and the popup closes rather than re-anchoring on
			// the new line.
			e.closeCompletion()
			return false
		}
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
	if e.completionSel < 0 {
		e.completionScroll = 0
		return
	}
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
	// bar is drawn over the rightmost popup column, which would otherwise read
	// as a click on whatever item sits in that row.
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
// current completionItems, shared by completionRect and DrawOverlay so they
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
	// scrolled off to the left, or near the right edge, must not put it over
	// the gutter or off-screen.
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
