package propsheet

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// band records the on-screen line range a row occupied on the last Draw,
// so HandleMouse can map a click back to a row without re-deriving layout.
type band struct {
	row  int
	y, h int
}

// Form lays out and drives a vertical stack of Rows: focus cycling (Tab/
// Backtab, plus Up/Down for rows that don't consume them themselves),
// scrolling when the rows don't fit the available height, and the
// aggregate Dirty/Revert/Validate/CopyText operations PropertySheet needs.
//
// Scroll is tracked in row-index units (like every other scrollable list
// in this codebase — DataGrid, ListBox, QueryListDialog), not raw display
// lines: a row that's taller than one line (Section, Note, a GridRow)
// scrolls into and out of view as a whole unit. Rows are therefore never
// clipped mid-line; a row too tall for the space left is either shrunk
// into it (Shrinkable — GridRow) or left for the next scroll position
// (see drawHeight).
type Form struct {
	rows        []Row
	focus       int // -1 = no focusable row
	scroll      int // index of the first row considered for drawing
	formFocused bool
	rect        core.Rect
	bands       []band

	// sbDragging is true while the user is dragging the form's own
	// scrollbar thumb (see HandleMouse) — mirrors DataGrid's/TreeView's/
	// ListBox's/Editor's field of the same name and purpose. Once armed it
	// outranks even the focused row, so the form keeps following the thumb
	// when the pointer drifts back over a focused GridRow; it can only be
	// armed by a press on the bar's own column, so a focused GridRow
	// sharing that rightmost column still scrolls its own rows.
	sbDragging bool
}

// NewForm creates a Form from an initial set of rows (order = tab order =
// draw order).
func NewForm(rows ...Row) *Form {
	f := &Form{focus: -1}
	f.Add(rows...)
	return f
}

// Add appends more rows.
func (f *Form) Add(rows ...Row) { f.rows = append(f.rows, rows...) }

// SetBounds positions the form's content area.
func (f *Form) SetBounds(x, y, w, h int) { f.rect = core.Rect{X: x, Y: y, W: w, H: h} }

// Focus sets whether the form as a whole holds focus (drives whether the
// current row is drawn highlighted); the first focusable row is focused
// automatically the first time this is called with true.
func (f *Form) Focus(v bool) {
	f.formFocused = v
	if v && f.focus < 0 {
		f.FocusFirst()
	}
}

// FocusFirst focuses the first focusable row, if any.
func (f *Form) FocusFirst() bool { f.focus = -1; return f.FocusNext() }

// FocusLast focuses the last focusable row, if any.
func (f *Form) FocusLast() bool { f.focus = len(f.rows); return f.FocusPrev() }

// FocusNext moves focus to the next focusable row. Returns false (leaving
// focus unchanged) if there isn't one — the signal PropertySheet uses to
// move focus to the next zone (buttons) instead.
func (f *Form) FocusNext() bool {
	for i := f.focus + 1; i < len(f.rows); i++ {
		if f.rows[i].Focusable() {
			f.setFocus(i)
			return true
		}
	}
	return false
}

// FocusPrev is FocusNext's mirror image.
func (f *Form) FocusPrev() bool {
	for i := f.focus - 1; i >= 0; i-- {
		if f.rows[i].Focusable() {
			f.setFocus(i)
			return true
		}
	}
	return false
}

func (f *Form) setFocus(i int) {
	f.focus = i
	f.ensureVisible(i)
}

// Focused returns the currently focused row, or nil.
func (f *Form) Focused() Row {
	if f.focus < 0 || f.focus >= len(f.rows) {
		return nil
	}
	return f.rows[f.focus]
}

// contentWidth returns the width available to rows, reserving one column
// for the scrollbar when the form doesn't fit.
func (f *Form) contentWidth() int {
	if f.totalHeight(f.rect.W) > f.rect.H {
		return core.Max(0, f.rect.W-1)
	}
	return f.rect.W
}

func (f *Form) totalHeight(w int) int {
	total := 0
	for _, row := range f.rows {
		total += row.Height(w)
	}
	return total
}

// The scrollbar measures the form in content *lines* — the same unit
// totalHeight and f.rect.H are in — while f.scroll is a row index, so the
// three helpers below convert between them. Sizing the thumb in row-index
// units instead is what left the bar drawn but empty and undraggable on
// every page whose rows are few but tall (Permissions: two Sections and
// two grids, 4 rows against a ~20-line form — taller than the form, yet
// fewer rows than it has lines).

// scrollLines returns how many content lines sit above the current scroll
// position, i.e. the scrollbar offset for f.scroll.
func (f *Form) scrollLines(w int) int {
	lines := 0
	for i := 0; i < f.scroll && i < len(f.rows); i++ {
		lines += f.rows[i].Height(w)
	}
	return lines
}

// rowAtLine returns the index of the row containing content line n.
func (f *Form) rowAtLine(n, w int) int {
	y := 0
	for i, row := range f.rows {
		y += row.Height(w)
		if n < y {
			return i
		}
	}
	return core.Max(0, len(f.rows)-1)
}

// maxScroll returns the largest scroll index worth reaching — the one that
// first brings the last row into view. Scrolling past it only pushes
// content off the top, and leaves the scrollbar thumb unable to travel to
// the bottom of its track.
func (f *Form) maxScroll(w int) int {
	used := 0
	for i := len(f.rows) - 1; i >= 0; i-- {
		used += f.rows[i].Height(w)
		if used > f.rect.H {
			return core.Min(i+1, core.Max(0, len(f.rows)-1))
		}
	}
	return 0
}

// ensureVisible scrolls just enough that row idx's start is within view —
// not necessarily its entire height, which a GridRow taller than the
// form's available space could never satisfy (see the comment in Draw).
func (f *Form) ensureVisible(idx int) {
	if idx < f.scroll {
		f.scroll = idx
		return
	}
	w := f.contentWidth()
	for f.scroll < idx && !f.rowFits(idx, w) {
		f.scroll++
	}
}

// rowFits reports whether row idx is drawn at the current scroll position,
// given rows are laid out top-down starting at f.scroll.
func (f *Form) rowFits(idx, w int) bool {
	y := f.rect.Y
	bottom := f.rect.Y + f.rect.H
	for i := f.scroll; i <= idx; i++ {
		h, ok := f.drawHeight(i, w, bottom-y, i == f.scroll)
		if !ok {
			return false
		}
		if i == idx {
			return true
		}
		y += h
	}
	return false
}

// drawHeight returns the height row i draws at when avail lines remain
// above the form's bottom edge, and whether it's drawn at all. A row that
// doesn't fit is clamped into the space left if it's Shrinkable, and
// otherwise left undrawn for the scrollbar to bring into view — except
// when it's the first row on screen, which always draws in whatever space
// there is rather than leaving the page blank.
func (f *Form) drawHeight(i, w, avail int, first bool) (int, bool) {
	h := f.rows[i].Height(w)
	if avail <= 0 {
		return h, false
	}
	if avail >= h {
		return h, true
	}
	if sh, ok := f.rows[i].(Shrinkable); ok && (first || avail >= sh.MinDrawHeight()) {
		return avail, true
	}
	return h, first
}

// Draw renders every row that fits below f.scroll, then rebuilds the
// click-routing bands used by HandleMouse.
func (f *Form) Draw(s tcell.Screen) {
	core.FillRect(s, f.rect, ' ', theme.StyleDialog())
	w := f.contentWidth()
	f.bands = f.bands[:0]
	// Nothing re-clamps f.scroll when the form gets taller (terminal
	// resize), which would otherwise leave the page starting mid-list with
	// dead space below it and the thumb pinned to the bottom of its track.
	f.scroll = core.Clamp(f.scroll, 0, f.maxScroll(w))

	// A row taller than the space left is drawn shrunk into it if it can
	// be (drawHeight), so nothing renders past the form's bottom edge into
	// the hint line and button row the sheet draws afterward. A row that
	// can't shrink that far ends the pass — the form scrollbar brings it
	// into view.
	y := f.rect.Y
	bottom := f.rect.Y + f.rect.H
	for i := f.scroll; i < len(f.rows) && y < bottom; i++ {
		row := f.rows[i]
		h, ok := f.drawHeight(i, w, bottom-y, i == f.scroll)
		if !ok {
			break
		}
		if sh, isShrinkable := row.(Shrinkable); isShrinkable {
			sh.SetDrawHeight(h)
		}
		row.Layout(f.rect.X, y, w)
		row.Draw(s, f.formFocused && i == f.focus)
		f.bands = append(f.bands, band{row: i, y: y, h: h})
		y += h
	}

	if total := f.totalHeight(w); total > f.rect.H {
		p := theme.Active()
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		offset := f.scrollLines(w)
		if f.scroll >= f.maxScroll(w) {
			// Parked on the last row: put the thumb at the very bottom of
			// its track rather than wherever that row's first line happens
			// to fall, so "scrolled all the way down" reads as such.
			offset = total - f.rect.H
		}
		core.DrawScrollbar(s, f.rect.Right()-1, f.rect.Y, f.rect.H, total, f.rect.H, offset, sbStyle, sbThumb)
	}
}

// DrawOverlays draws every row's popup overlay (open dropdowns), if any.
// Call after Draw, once every other element in the dialog has been drawn.
func (f *Form) DrawOverlays(s tcell.Screen) {
	for _, row := range f.rows {
		if od, ok := row.(OverlayDrawer); ok {
			od.DrawOverlay(s)
		}
	}
}

// OverlayActive reports whether the focused row currently has an open
// overlay (SelectRow's dropdown list, GridRow's "Show Value" popup) drawn
// on top of the form — see DrawOverlays. A host laying the form out
// alongside other position-routed elements (PropertySheet's button row and
// page list) must check this and give the form first refusal of every
// click while true, the same "overlay drawn last gets first refusal"
// contract DataGrid.OverlayActive/QueryPanel already follow.
func (f *Form) OverlayActive() bool {
	row := f.Focused()
	if row == nil {
		return false
	}
	oa, ok := row.(OverlayActiver)
	return ok && oa.OverlayActive()
}

// HandleKey forwards to the focused row first; if it doesn't consume the
// event, Tab/Backtab/Up/Down move focus and PgUp/PgDn scroll.
func (f *Form) HandleKey(ev *tcell.EventKey) bool {
	if row := f.Focused(); row != nil {
		if kh, ok := row.(KeyHandler); ok && kh.HandleKey(ev) {
			return true
		}
	}
	switch ev.Key() {
	case tcell.KeyTab, tcell.KeyDown:
		return f.FocusNext()
	case tcell.KeyBacktab, tcell.KeyUp:
		return f.FocusPrev()
	case tcell.KeyPgDn:
		if f.OverlayActive() {
			// Scrolling the row list out from under an open dropdown leaves
			// its list floating at a stale position — DrawOverlays draws
			// every row's overlay every frame regardless of whether that
			// row is still within [f.scroll, ...] and got a fresh Layout
			// this frame. Swallow the key instead of scrolling underneath it.
			return true
		}
		f.scroll = core.Min(f.maxScroll(f.contentWidth()), f.scroll+core.Max(1, f.rect.H/2))
		return true
	case tcell.KeyPgUp:
		if f.OverlayActive() {
			return true
		}
		f.scroll = core.Max(0, f.scroll-core.Max(1, f.rect.H/2))
		return true
	}
	return false
}

// HandleMouse gives the row under the pointer first refusal on wheel
// scroll — a GridRow's DataGrid has its own rows to scroll through and
// must do so instead of the wheel always scrolling the form around it —
// then falls back to scrolling the form itself. For every other button,
// it gives the focused row first refusal (so a click on its open dropdown
// overlay — which visually extends below the row's own band — still
// reaches it), then falls back to whichever row's band contains the click.
func (f *Form) HandleMouse(ev *tcell.EventMouse) bool {
	switch ev.Buttons() {
	case tcell.WheelUp, tcell.WheelDown:
		if row, ok := f.rowAt(ev); ok {
			if mh, isHandler := row.(MouseHandler); isHandler && mh.HandleMouse(ev) {
				return true
			}
		}
		if f.OverlayActive() {
			// Same reasoning as PgUp/PgDn above: don't scroll the row list
			// out from under a dropdown that's open elsewhere on the form.
			return true
		}
		if ev.Buttons() == tcell.WheelUp {
			f.scroll = core.Max(0, f.scroll-3)
		} else {
			f.scroll = core.Min(f.maxScroll(f.contentWidth()), f.scroll+3)
		}
		return true
	}
	// The latch is dropped before anything else can claim the release —
	// the same ordering DataGrid, Editor and TreeView use internally, and
	// for the same reason. Below the focused row's dispatch it was
	// unreachable whenever that row was a GridRow: DataGrid.HandleMouse
	// returns true for any release inside its rect, so a scrollbar drag let
	// go over the grid left sbDragging armed, and every later click in the
	// form was then taken as a scrollbar drag that jumped the scroll to
	// wherever it landed.
	if ev.Buttons() == tcell.ButtonNone {
		f.sbDragging = false
	}
	// An armed scrollbar drag outranks even the focused row: the gesture
	// started on the form's own bar, so every event until release belongs
	// to it, wherever the pointer has drifted to. Without this, the moment
	// a drag wandered back over a focused GridRow that grid claimed the
	// motion events and the form stopped following the thumb. sbDragging is
	// only ever armed by a press on the bar's own column, so this can't
	// steal a gesture that began inside a row.
	if f.sbDragging && f.handleScrollbarDrag(ev, f.contentWidth()) {
		return true
	}
	if row := f.Focused(); row != nil {
		if mh, ok := row.(MouseHandler); ok && mh.HandleMouse(ev) {
			return true
		}
	}
	if ev.Buttons() == tcell.ButtonNone {
		// A plain hover/motion event (no button down) must not fall through
		// to the click-routing below — tcell delivers these continuously as
		// the mouse moves, and band-matching on one would shift focus to
		// whatever row the pointer happens to be over, blurring (and thus
		// closing) an open DropDown the moment the user moves toward its
		// own open list to click an item in it.
		return false
	}
	w := f.contentWidth()
	if f.handleScrollbarDrag(ev, w) {
		return true
	}
	mx, my := ev.Position()
	if mx < f.rect.X || mx >= f.rect.Right() {
		return false
	}
	for _, b := range f.bands {
		if my < b.y || my >= b.y+b.h {
			continue
		}
		row := f.rows[b.row]
		if mh, ok := row.(MouseHandler); ok && mh.HandleMouse(ev) {
			if row.Focusable() {
				f.setFocus(b.row)
			}
			return true
		}
		if row.Focusable() {
			f.setFocus(b.row)
			return true
		}
		return false
	}
	return false
}

// handleScrollbarDrag is Form's own version of core.HandleScrollbarDrag,
// which can't be reused here: that helper takes one h serving as both
// track length and visible count, and Form's scrollbar measures lines
// (track length f.rect.H, total f.totalHeight) while f.scroll counts rows.
// Like the shared helper, sbDragging latches for the whole gesture so the
// drag keeps working once the pointer drifts off the bar's column; the
// ButtonNone branch of HandleMouse clears it on release.
func (f *Form) handleScrollbarDrag(ev *tcell.EventMouse, w int) bool {
	total := f.totalHeight(w)
	if ev.Buttons() != tcell.Button1 || total <= f.rect.H || f.rect.H <= 0 {
		return false
	}
	mx, my := ev.Position()
	if !f.sbDragging && (mx != f.rect.Right()-1 || my < f.rect.Y || my >= f.rect.Y+f.rect.H) {
		return false
	}
	f.sbDragging = true
	line := core.ScrollOffsetForDrag(my-f.rect.Y, f.rect.H, total, f.rect.H)
	f.scroll = core.Min(f.rowAtLine(line, w), f.maxScroll(w))
	return true
}

// rowAt returns the row whose band contains ev's position, if any.
func (f *Form) rowAt(ev *tcell.EventMouse) (Row, bool) {
	mx, my := ev.Position()
	if mx < f.rect.X || mx >= f.rect.Right() {
		return nil, false
	}
	for _, b := range f.bands {
		if my < b.y || my >= b.y+b.h {
			continue
		}
		return f.rows[b.row], true
	}
	return nil, false
}

// Dirty reports whether any row's value differs from its loaded baseline.
func (f *Form) Dirty() bool {
	for _, row := range f.rows {
		if e, ok := row.(Editable); ok && e.Dirty() {
			return true
		}
	}
	return false
}

// Revert restores every row to its loaded baseline.
func (f *Form) Revert() {
	for _, row := range f.rows {
		if e, ok := row.(Editable); ok {
			e.Revert()
		}
	}
}

// Validate runs every dirty row's validator, stopping at the first error.
func (f *Form) Validate() error {
	for _, row := range f.rows {
		e, ok := row.(Editable)
		if !ok || !e.Dirty() {
			continue
		}
		if err := e.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// CopyText returns the focused row's copyable value, if any.
func (f *Form) CopyText() (string, bool) {
	row := f.Focused()
	if row == nil {
		return "", false
	}
	c, ok := row.(Copyable)
	if !ok {
		return "", false
	}
	return c.CopyText(), true
}
