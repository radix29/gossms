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
// scrolls into and out of view as a whole unit. This trades a little
// precision for guaranteed-simple rendering — no row is ever drawn
// partially clipped at the top or bottom of the form.
type Form struct {
	rows        []Row
	focus       int // -1 = no focusable row
	scroll      int // index of the first row considered for drawing
	formFocused bool
	rect        core.Rect
	bands       []band
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

// rowFits reports whether row idx's start is visible at the current
// scroll position, given rows are laid out top-down starting at f.scroll.
func (f *Form) rowFits(idx, w int) bool {
	y := f.rect.Y
	for i := f.scroll; i <= idx; i++ {
		if i == idx {
			return y < f.rect.Y+f.rect.H
		}
		y += f.rows[i].Height(w)
	}
	return false
}

// Draw renders every row that fits below f.scroll, then rebuilds the
// click-routing bands used by HandleMouse.
func (f *Form) Draw(s tcell.Screen) {
	core.FillRect(s, f.rect, ' ', theme.StyleDialog())
	w := f.contentWidth()
	f.bands = f.bands[:0]

	// A row only needs its *start* within the visible area, not its
	// entire height — a GridRow or Note taller than the remaining space
	// still draws (and may visually run past the form's bottom edge,
	// clipped by the separator/buttons the sheet draws afterward) rather
	// than vanishing outright. Requiring the full row to fit made every
	// grid-based page render as a blank Section header on any terminal
	// shorter than the grid's declared height.
	y := f.rect.Y
	bottom := f.rect.Y + f.rect.H
	for i := f.scroll; i < len(f.rows) && y < bottom; i++ {
		row := f.rows[i]
		h := row.Height(w)
		row.Layout(f.rect.X, y, w)
		row.Draw(s, f.formFocused && i == f.focus)
		f.bands = append(f.bands, band{row: i, y: y, h: h})
		y += h
	}

	if total := f.totalHeight(w); total > f.rect.H {
		p := theme.Active()
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, f.rect.Right()-1, f.rect.Y, f.rect.H, len(f.rows), f.rect.H, f.scroll, sbStyle, sbThumb)
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
		f.scroll = core.Min(core.Max(0, len(f.rows)-1), f.scroll+core.Max(1, f.rect.H/2))
		return true
	case tcell.KeyPgUp:
		f.scroll = core.Max(0, f.scroll-core.Max(1, f.rect.H/2))
		return true
	}
	return false
}

// HandleMouse routes wheel scroll to the form itself, gives the focused
// row first refusal (so a click on its open dropdown overlay — which
// visually extends below the row's own band — still reaches it), then
// falls back to whichever row's band contains the click.
func (f *Form) HandleMouse(ev *tcell.EventMouse) bool {
	switch ev.Buttons() {
	case tcell.WheelUp:
		f.scroll = core.Max(0, f.scroll-3)
		return true
	case tcell.WheelDown:
		f.scroll = core.Min(core.Max(0, len(f.rows)-1), f.scroll+3)
		return true
	}
	if row := f.Focused(); row != nil {
		if mh, ok := row.(MouseHandler); ok && mh.HandleMouse(ev) {
			return true
		}
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
