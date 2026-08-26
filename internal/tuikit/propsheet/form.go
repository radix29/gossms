package propsheet

import (
	"slices"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// band records the on-screen line range a row occupied on the last Draw, so
// HandleMouse can map a click back to a row without re-deriving layout.
type band struct {
	row  int
	y, h int
}

// Form lays out and drives a vertical stack of Rows: focus cycling (Tab/Backtab,
// plus Up/Down for rows that don't consume them), scrolling when the rows don't
// fit, and the aggregate Dirty/Revert/Validate/CopyText operations
// PropertySheet needs.
//
// Scroll is tracked in row-index units, like every other scrollable list here,
// not raw display lines: a row taller than one line (Section, Note, GridRow)
// scrolls in and out as a whole unit. A row too tall for the space left is
// either shrunk into it (Shrinkable) or left for the next scroll position (see
// drawHeight).
type Form struct {
	rows        []Row
	focus       int // -1 = no focusable row
	scroll      int // index of the first row considered for drawing
	formFocused bool
	rect        core.Rect
	bands       []band

	// readOnly makes every row unfocusable and refuses to route a click into
	// one, so the form can be read and scrolled but not edited. See
	// SetReadOnly.
	readOnly bool

	// sbDragging is true while the form's own scrollbar thumb is being dragged.
	// Once armed it outranks even the focused row, so the form keeps following
	// the thumb when the pointer drifts back over a focused GridRow. Only a press
	// on the bar's own column arms it.
	sbDragging bool
}

// NewForm creates a Form from an initial set of rows; order is tab order and
// draw order.
func NewForm(rows ...Row) *Form {
	f := &Form{focus: -1}
	f.Add(rows...)
	return f
}

// Add appends more rows.
func (f *Form) Add(rows ...Row) {
	f.rows = append(f.rows, rows...)
	f.applyReadOnlyDraw(rows)
}

// Prepend inserts rows ahead of the existing ones, for a caveat that has to be
// read before the page it applies to. Appending one instead puts it below a
// grid the sheet has to be scrolled past, and a Note is not focusable, so Tab
// never brings it into view.
//
// Only safe before the form is interacted with: focus is an index into rows.
func (f *Form) Prepend(rows ...Row) {
	f.rows = append(append([]Row{}, rows...), f.rows...)
	f.applyReadOnlyDraw(rows)
}

// SetReadOnly makes the form unfocusable and unclickable: no row can take
// focus, and a press is not routed into one, so nothing on it can be edited.
// Wheel scrolling and PgUp/PgDn still work — the page is meant to be read.
// Every row implementing ReadOnlyDrawer also switches to its flat rendering,
// so the page stops offering controls it will not accept input into.
//
// This is how a Properties page whose reads succeed but whose writes would be
// refused is presented. Nothing can become dirty, so Apply and Script Changes
// have nothing to send, which is the property that makes it safe rather than
// merely discouraging.
func (f *Form) SetReadOnly(v bool) {
	f.readOnly = v
	if v {
		f.focus = -1
	}
	f.applyReadOnlyDraw(f.rows)
}

// applyReadOnlyDraw pushes the form's read-only state into the given rows.
// Called from Add and Prepend as well as SetReadOnly: a page that builds rows
// after the gate has decided would otherwise draw them editable.
func (f *Form) applyReadOnlyDraw(rows []Row) {
	for _, row := range rows {
		if rd, ok := row.(ReadOnlyDrawer); ok {
			rd.SetDrawReadOnly(f.readOnly)
		}
	}
}

// ReadOnly reports whether SetReadOnly is in force.
func (f *Form) ReadOnly() bool { return f.readOnly }

// focusableAt reports whether row i can take focus right now — its own answer,
// unless the whole form is read-only.
func (f *Form) focusableAt(i int) bool { return f.rows[i].Focusable() && !f.readOnly }

// Rows returns the form's rows in order, for a caller that needs to find one
// after the form is built.
func (f *Form) Rows() []Row { return f.rows }

// SetBounds positions the form's content area.
func (f *Form) SetBounds(x, y, w, h int) { f.rect = core.Rect{X: x, Y: y, W: w, H: h} }

// Focus sets whether the form as a whole holds focus, which drives whether the
// current row draws highlighted. The first focusable row is focused
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

// FocusNext moves focus to the next focusable row, returning false and leaving
// focus unchanged if there isn't one — the signal PropertySheet uses to move on
// to the button zone.
func (f *Form) FocusNext() bool {
	for i := f.focus + 1; i < len(f.rows); i++ {
		if f.focusableAt(i) {
			f.setFocus(i)
			return true
		}
	}
	return false
}

// FocusPrev is FocusNext's mirror image.
func (f *Form) FocusPrev() bool {
	for i := f.focus - 1; i >= 0; i-- {
		if f.focusableAt(i) {
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

// contentWidth returns the width available to rows, reserving a column for the
// scrollbar when the form doesn't fit.
func (f *Form) contentWidth() int {
	if f.totalHeight(f.rect.W) > f.rect.H {
		return max(0, f.rect.W-1)
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

// The scrollbar measures the form in content *lines*, the unit totalHeight and
// f.rect.H use, while f.scroll is a row index — the three helpers below convert
// between them. Sizing the thumb in row-index units leaves the bar drawn but
// empty and undraggable on any page whose rows are few but tall.

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
	return max(0, len(f.rows)-1)
}

// maxScroll returns the largest scroll index worth reaching — the one that first
// brings the last row into view. Past it, content only leaves the top and the
// thumb can't reach the bottom of its track.
func (f *Form) maxScroll(w int) int {
	used := 0
	for i, row := range slices.Backward(f.rows) {
		used += row.Height(w)
		if used > f.rect.H {
			return min(i+1, max(0, len(f.rows)-1))
		}
	}
	return 0
}

// ensureVisible scrolls just enough that row idx's start is in view — not its
// whole height, which a GridRow taller than the form could never satisfy.
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

// drawHeight returns the height row i draws at when avail lines remain above the
// form's bottom edge, and whether it is drawn at all. A row that doesn't fit is
// clamped into the space left if Shrinkable, otherwise left for the scrollbar to
// bring into view — except the first row on screen, which always draws in
// whatever space there is rather than leaving the page blank.
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
	// Nothing else re-clamps f.scroll when the form gets taller (a terminal
	// resize), which would leave the page starting mid-list with dead space
	// below and the thumb pinned to the bottom of its track.
	f.scroll = core.Clamp(f.scroll, 0, f.maxScroll(w))

	// A row taller than the space left is drawn shrunk into it where it can be
	// (drawHeight), so nothing renders past the form's bottom edge into the hint
	// line and button row. A row that can't shrink that far ends the pass; the
	// scrollbar brings it into view.
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
			// Parked on the last row: put the thumb at the bottom of its track
			// rather than wherever that row's first line falls.
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

// OverlayActive reports whether the focused row has an open overlay (SelectRow's
// dropdown, GridRow's "Show Value" popup) drawn on top of the form. A host
// laying the form out alongside other position-routed elements must check this
// and give the form first refusal of every click while true — the same "overlay
// drawn last gets first refusal" contract DataGrid.OverlayActive follows.
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
			// Scrolling the row list out from under an open dropdown leaves its
			// list floating at a stale position: DrawOverlays draws every row's
			// overlay every frame, whether or not that row got a fresh Layout.
			// Swallow the key instead.
			return true
		}
		f.scroll = min(f.maxScroll(f.contentWidth()), f.scroll+max(1, f.rect.H/2))
		return true
	case tcell.KeyPgUp:
		if f.OverlayActive() {
			return true
		}
		f.scroll = max(0, f.scroll-max(1, f.rect.H/2))
		return true
	}
	return false
}

// HandleMouse gives the row under the pointer first refusal on wheel scroll — a
// GridRow's DataGrid has its own rows to scroll — then falls back to scrolling
// the form. For every other button it gives the focused row first refusal, so a
// click on its open dropdown overlay (which extends below the row's own band)
// still reaches it, then falls back to whichever row's band contains the
// click.
func (f *Form) HandleMouse(ev *tcell.EventMouse) bool {
	switch ev.Buttons() {
	case tcell.WheelUp, tcell.WheelDown:
		if row, ok := f.rowAt(ev); ok {
			if mh, isHandler := row.(MouseHandler); isHandler && mh.HandleMouse(ev) {
				return true
			}
		}
		if f.OverlayActive() {
			// As with PgUp/PgDn above: don't scroll the row list out from under
			// a dropdown open elsewhere on the form.
			return true
		}
		if ev.Buttons() == tcell.WheelUp {
			f.scroll = max(0, f.scroll-3)
		} else {
			f.scroll = min(f.maxScroll(f.contentWidth()), f.scroll+3)
		}
		return true
	}
	// The latch is dropped before anything else can claim the release, the
	// ordering DataGrid, Editor and TreeView use. Below the focused row's
	// dispatch it is unreachable whenever that row is a GridRow —
	// DataGrid.HandleMouse returns true for any release inside its rect — so a
	// scrollbar drag let go over the grid leaves sbDragging armed and every later
	// click reads as a drag that jumps the scroll.
	if ev.Buttons() == tcell.ButtonNone {
		f.sbDragging = false
	}
	// An armed scrollbar drag outranks even the focused row: the gesture started
	// on the form's own bar, so every event until release belongs to it wherever
	// the pointer drifted. Without this, a drag wandering back over a focused
	// GridRow lets that grid claim the motion events. sbDragging is only ever
	// armed by a press on the bar's column, so this can't steal a gesture that
	// began inside a row.
	if f.sbDragging && f.handleScrollbarDrag(ev, f.contentWidth()) {
		return true
	}
	if row := f.Focused(); row != nil {
		if mh, ok := row.(MouseHandler); ok && mh.HandleMouse(ev) {
			return true
		}
	}
	if ev.Buttons() == tcell.ButtonNone {
		// A plain hover/motion event (no button down) must not fall through to
		// the click-routing below: tcell delivers these continuously, and
		// band-matching on one shifts focus to whatever row the pointer is over,
		// closing an open DropDown as the user moves toward its list.
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
		if f.readOnly {
			// Claimed, not routed: the press landed on the form, and letting it
			// fall through would put it wherever the sheet draws underneath.
			return true
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

// handleScrollbarDrag is Form's own version of core.HandleScrollbarDrag, which
// can't be reused: that helper takes one h as both track length and visible
// count, while Form's scrollbar measures lines (f.rect.H, f.totalHeight) and
// f.scroll counts rows. sbDragging latches for the whole gesture so the drag
// survives the pointer drifting off the bar's column; HandleMouse's ButtonNone
// branch clears it.
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
	f.scroll = min(f.rowAtLine(line, w), f.maxScroll(w))
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
