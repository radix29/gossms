package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// ModalDialog — base type
// ---------------------------------------------------------------------------

// ModalDialog is the foundation for every pop-up window. Embed it and call
// InitModal from the constructor.
type ModalDialog struct {
	rect       core.Rect
	reqW, reqH int // last size passed to InitModal/SetSize, pre-clamp
	title      string
	visible    bool
	screen     tcell.Screen // needed for Size() during recentre

	// mouseDragging distinguishes a fresh Button1 press on the button row from a
	// continued hold — Toolbar, TreeView and MenuBar have the same field.
	// Without it, tcell's all-motion tracking resends Button1 on every motion
	// while the button is down, so a click that twitches fires the action on
	// every resend. Reset in ConsumeOutsideClick rather than ButtonClicked,
	// since every embedding dialog's HandleMouse calls the former first while
	// some reach the latter only through a mode-gated branch a release never
	// takes — and again in Show, for the one gesture whose release never gets
	// that far.
	mouseDragging bool

	// sbDragging is true while a content scrollbar an embedding dialog draws is
	// being dragged (see ScrollbarDrag) — separate from mouseDragging, which
	// targets the button row rather than the scrollbar column. Reset alongside
	// it, in both places.
	sbDragging bool
}

// InitModal sets up the dialog for the given screen, title and size. Call it
// from the embedding type's constructor.
func (d *ModalDialog) InitModal(s tcell.Screen, title string, w, h int) {
	d.screen = s
	d.title = title
	d.reqW, d.reqH = w, h
	d.recentre()
}

// SetSize resizes the dialog and recentres it. For a dialog whose content grows
// with the screen (see propsheet.PropertySheet), call it from Show or Draw with
// a size computed from the current screen dimensions.
func (d *ModalDialog) SetSize(w, h int) {
	d.reqW, d.reqH = w, h
	d.recentre()
}

// recentre repositions the dialog in the centre of the screen, clamping its size
// to fit first: a dialog wider or taller than the terminal would otherwise draw
// its right/bottom border, and anything docked to it, off-screen. The clamp is
// recomputed from reqW/reqH — the last size requested via InitModal/SetSize —
// rather than applied to rect.W/H in place, so a dialog shown small on a cramped
// terminal returns to its full size on a larger one.
func (d *ModalDialog) recentre() {
	if d.screen == nil {
		return
	}
	sw, sh := d.screen.Size()
	d.rect.W = min(d.reqW, sw)
	d.rect.H = min(d.reqH, sh)
	d.rect.X = max(0, (sw-d.rect.W)/2)
	d.rect.Y = max(0, (sh-d.rect.H)/2)
}

// Show makes the dialog visible, recentred on the screen.
//
// The drag latches start clear on every showing, because the release that would
// otherwise clear them never arrives: a button click closes the dialog on the
// *press*, and by the time the matching ButtonNone comes in HandleMouse returns
// early on !visible and the host has dropped the dialog from its input routing.
// A latch left set from one showing to the next makes ButtonClicked refuse the
// first click on every reopening. Reopening is not a continuation of the gesture
// that closed it. A dialog that opens *during* a held gesture is the host's
// hazard — see App.gestureOverlay.
func (d *ModalDialog) Show() {
	d.recentre()
	d.mouseDragging = false
	d.sbDragging = false
	d.visible = true
}

// Relayout re-fits the dialog to the current screen size. The host calls it on
// every terminal resize for each open dialog: recentre otherwise runs only from
// InitModal/SetSize/Show, so a dialog open across a resize keeps the rect it was
// centred into and draws its border and button row off-screen while still
// swallowing every key.
//
// A dialog whose size depends on the screen overrides this to recompute that
// size first and then call SetSize, which recentres.
func (d *ModalDialog) Relayout() { d.recentre() }

// Hide dismisses the dialog.
func (d *ModalDialog) Hide() { d.visible = false }

// Visible reports whether the dialog is currently shown.
func (d *ModalDialog) Visible() bool { return d.visible }

// Rect returns the dialog's bounding rectangle.
func (d *ModalDialog) Rect() core.Rect { return d.rect }

// SetTitle updates the dialog title.
func (d *ModalDialog) SetTitle(t string) { d.title = t }

// ContainsMouse reports whether (mx,my) is inside the dialog box.
func (d *ModalDialog) ContainsMouse(mx, my int) bool {
	return d.rect.Contains(mx, my)
}

// ConsumeOutsideClick returns true if the mouse event originated outside the
// dialog. The dialog is always visible when this is called.
//
// A ButtonNone (release/hover) event always clears mouseDragging here whatever
// its position, the way Toolbar resets its own field before any bounds check:
// this is the one call every embedding dialog's HandleMouse makes
// unconditionally, including for a mouse-up outside the dialog's rect.
func (d *ModalDialog) ConsumeOutsideClick(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		d.mouseDragging = false
		d.sbDragging = false
	}
	if !d.visible {
		return false
	}
	mx, my := ev.Position()
	return !d.rect.Contains(mx, my)
}

// ScrollbarDrag handles a click or drag on a vertical scrollbar an embedding
// dialog draws at trackX (normally Rect().Right()-1) spanning [trackY,
// trackY+trackH) — the mouse-side counterpart of core.DrawScrollbar. Returns
// true and updates *scroll for a Button1 press on the bar, or any continuation
// of a started drag regardless of x, since the mouse can drift off the bar's
// column. Call it before the dialog's own row hit-testing, since the bar can sit
// over a column that would otherwise resolve to a content row.
func (d *ModalDialog) ScrollbarDrag(ev *tcell.EventMouse, trackX, trackY, trackH, total int, scroll *int) bool {
	return core.HandleScrollbarDrag(ev, trackX, trackY, trackH, total, &d.sbDragging, scroll)
}

// dialogDimNum/dialogDimDen fade the underlying UI toward the overlay colour
// behind an open dialog — clearly inactive but still legible.
const dialogDimNum, dialogDimDen = 3, 5

// DrawBase fades the underlying UI and draws the dialog box. Embedding types
// call it first, then render their own content within InnerRect().
func (d *ModalDialog) DrawBase(s tcell.Screen) {
	p := theme.Active()

	// Fade the already-drawn UI in place rather than painting a solid overlay, so
	// the inactive interface stays visible but dimmed.
	sw, sh := s.Size()
	core.DimArea(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, p.DialogOverlay, dialogDimNum, dialogDimDen)

	// Dialog background (opaque, over the dimmed UI)
	core.FillRect(s, d.rect, ' ', theme.StyleDialog())

	// Border + title
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogBorder)
	titleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTitle).Bold(true)
	core.DrawBoxTitle(s, d.rect, d.title, borderStyle, titleStyle)

	// Everything the embedding type draws from here is confined to the box — see
	// contentClip. Set after the dim and the border, which draw outside it.
	if d.clamped() {
		core.SetClip(s, d.contentClip())
	}
}

// clamped reports whether recentre had to shrink the dialog to fit the terminal.
// Every embedding type lays its content out at fixed offsets for the size it
// *asked* for, so on a clamped rect the rows run past the right border and the
// button row, and DrawButtons lands on top of them. The clip and the button-row
// clear below are gated on this: a dropdown or completion overlay opened inside
// a dialog may legitimately extend beyond the box.
func (d *ModalDialog) clamped() bool {
	return d.rect.W < d.reqW || d.rect.H < d.reqH
}

// contentClip is the region an embedding type's content may draw in: the
// interior, border intact. The one thing a dialog legitimately draws outside it
// is its scrollbar, on the right border column — DrawContentScrollbar widens the
// clip for that.
func (d *ModalDialog) contentClip() core.Rect { return d.InnerRect() }

// DrawContentScrollbar draws the dialog's vertical scrollbar spanning [trackY,
// trackY+trackH) on the right border column, where ScrollbarDrag hit-tests for
// it. That column is outside contentClip, so the clip widens to the whole box
// for the draw: a dialog calling core.DrawScrollbar there directly loses its bar
// on a clamped rect.
func (d *ModalDialog) DrawContentScrollbar(s tcell.Screen, trackY, trackH, total, offset int) {
	if c, ok := s.(*core.ClipScreen); ok {
		saved := c.Clip()
		c.SetClip(d.rect)
		defer c.SetClip(saved)
	}
	p := theme.Active()
	sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
	core.DrawScrollbar(s, d.rect.Right()-1, trackY, trackH, total, trackH, offset, sbStyle, sbThumb)
}

// InnerRect returns the usable interior rectangle (excluding border).
func (d *ModalDialog) InnerRect() core.Rect { return d.rect.Inner(1) }

// ButtonRowY returns the y coordinate of the standard button row, two rows from
// the bottom, above the border.
func (d *ModalDialog) ButtonRowY() int { return d.rect.Y + d.rect.H - 3 }

// DrawSeparator draws a horizontal line one row above ButtonRowY.
func (d *ModalDialog) DrawSeparator(s tcell.Screen) {
	p := theme.Active()
	sep := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, d.rect.X+1, d.ButtonRowY()-1, d.rect.W-2, sep)
}

// buttonRowStartX returns the x column the button row starts at so the row ends
// flush with the dialog's right margin. Shared by DrawButtons and ButtonClicked
// so hit-testing matches what was drawn.
func (d *ModalDialog) buttonRowStartX(labels []string) int {
	total := 0
	for i, label := range labels {
		if i > 0 {
			total += 2
		}
		total += core.DisplayWidth("[ " + label + " ]")
	}
	return d.rect.Right() - 2 - total
}

// DrawButtons renders a row of buttons at ButtonRowY, right-aligned within
// the dialog. activeIdx highlights that button.
func (d *ModalDialog) DrawButtons(s tcell.Screen, labels []string, activeIdx int) {
	p := theme.Active()
	btnStyle := tcell.StyleDefault.Background(p.ButtonBg).Foreground(p.ButtonFg)
	activeStyle := tcell.StyleDefault.Background(p.ButtonActive).Foreground(color.White)
	col := d.buttonRowStartX(labels)
	y := d.ButtonRowY()
	// On a clamped rect, content laid out for the full height reaches this row
	// and the one below; the buttons are right-aligned, so without the clear what
	// shows is the tail of a content row with a button row in the middle of it. A
	// dialog with a button row draws nothing of its own from ButtonRowY down.
	if d.clamped() {
		core.FillRect(s, core.Rect{X: d.rect.X + 1, Y: y, W: d.rect.W - 2, H: d.rect.Bottom() - 1 - y}, ' ', theme.StyleDialog())
	}
	for i, label := range labels {
		st := btnStyle
		if i == activeIdx {
			st = activeStyle
		}
		text := "[ " + label + " ]"
		core.DrawText(s, col, y, st, text)
		col += core.DisplayWidth(text) + 2
	}
}

// ButtonClicked returns the index of the button clicked, or -1. mouseDragging
// guards against tcell's held-motion Button1 resend, so a click that twitches
// before release fires once.
func (d *ModalDialog) ButtonClicked(ev *tcell.EventMouse, labels []string) int {
	if ev.Buttons() != tcell.Button1 {
		return -1
	}
	mx, my := ev.Position()
	if my != d.ButtonRowY() {
		return -1
	}
	col := d.buttonRowStartX(labels)
	for i, label := range labels {
		text := "[ " + label + " ]"
		w := core.DisplayWidth(text)
		if mx >= col && mx < col+w {
			if d.mouseDragging {
				return -1
			}
			d.mouseDragging = true
			return i
		}
		col += w + 2
	}
	return -1
}
