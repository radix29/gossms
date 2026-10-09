package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// ModalDialog: base type
// ---------------------------------------------------------------------------

// ModalDialog is the foundation for every pop-up window. Embed it and call
// InitModal from the constructor.
type ModalDialog struct {
	rect       core.Rect
	reqW, reqH int // last size passed to InitModal/SetSize, pre-clamp
	title      string
	visible    bool
	screen     tcell.Screen // needed for Size() during recentre

	// mouseDragging separates a fresh Button1 press on the button row from a resent
	// hold (as Toolbar, TreeView, MenuBar): otherwise a twitching click fires the
	// action on every resend. Reset in ConsumeOutsideClick rather than
	// ButtonClicked, since every embedding dialog's HandleMouse calls the former
	// first while some reach the latter only through a mode-gated branch a release
	// never takes; and again in Show, for the gesture whose release never gets that
	// far.
	mouseDragging bool

	// sbDragging is true while a content scrollbar an embedding dialog draws is
	// dragged (see ScrollbarDrag); separate from mouseDragging, reset alongside it
	// in both places.
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

// SetSize resizes the dialog and recentres it. A dialog whose content grows with
// the screen (see propsheet.PropertySheet) calls it from Show or Draw.
func (d *ModalDialog) SetSize(w, h int) {
	d.reqW, d.reqH = w, h
	d.recentre()
}

// recentre centres the dialog, clamping its size to the terminal first (else the
// right/bottom border and anything docked to it draws off-screen). The clamp is
// recomputed from reqW/reqH, not applied to rect.W/H in place, so a dialog
// shown on a cramped terminal returns to full size on a larger one.
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

// Show makes the dialog visible, recentred.
//
// The drag latches start clear on every showing because their clearing release
// never arrives: a button click closes the dialog on the press, and by the
// matching ButtonNone HandleMouse returns early on !visible and the host has
// dropped the dialog from input routing. A stale latch makes ButtonClicked
// refuse the first click on every reopening. A dialog opening during a held
// gesture is the host's hazard; see App.gestureOverlay.
func (d *ModalDialog) Show() {
	d.recentre()
	d.mouseDragging = false
	d.sbDragging = false
	d.visible = true
}

// Relayout re-fits the dialog to the current screen size. The host calls it on
// every terminal resize for each open dialog: recentre otherwise runs only from
// InitModal/SetSize/Show, so the dialog would keep its old rect, drawing its
// border and button row off-screen while still swallowing every key. A dialog
// whose size depends on the screen overrides this to recompute it and call
// SetSize.
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
// dialog (always visible when called). A ButtonNone event always clears
// mouseDragging here whatever its position, as Toolbar does: every embedding
// dialog's HandleMouse makes this call unconditionally, including for a mouse-up
// outside the rect.
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
// trackY+trackH), the counterpart of core.DrawScrollbar. Returns true and updates
// *scroll for a Button1 press on the bar or any continuation of a started drag
// regardless of x. Call it before the dialog's row hit-testing: the bar can sit
// over a column that would resolve to a content row.
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

	// Fade the drawn UI in place, not a solid overlay, so it stays visible dimmed.
	sw, sh := s.Size()
	core.DimArea(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, p.DialogOverlay, dialogDimNum, dialogDimDen)

	// Dialog background (opaque, over the dimmed UI)
	core.FillRect(s, d.rect, ' ', theme.StyleDialog())

	// Border + title
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogBorder)
	titleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTitle).Bold(true)
	core.DrawBoxTitle(s, d.rect, d.title, borderStyle, titleStyle)

	// Everything the embedding type draws from here is confined to the box (see
	// contentClip); set after the dim and border, which draw outside it.
	if d.clamped() {
		core.SetClip(s, d.contentClip())
	}
}

// clamped reports whether recentre had to shrink the dialog. Embedding types lay
// content out at fixed offsets for the size they asked for, so on a clamped rect
// rows run past the right border and button row, and DrawButtons lands on top.
// The clip and button-row clear are gated on this: a dropdown or completion
// overlay inside a dialog may legitimately extend beyond the box.
func (d *ModalDialog) clamped() bool {
	return d.rect.W < d.reqW || d.rect.H < d.reqH
}

// contentClip is the region an embedding type's content may draw in: the
// interior, border intact. The one exception is its scrollbar on the right
// border column, for which DrawContentScrollbar widens the clip.
func (d *ModalDialog) contentClip() core.Rect { return d.InnerRect() }

// DrawContentScrollbar draws the dialog's vertical scrollbar spanning [trackY,
// trackY+trackH) on the right border column, where ScrollbarDrag hit-tests. That
// column is outside contentClip, so the clip widens to the whole box for the
// draw; core.DrawScrollbar called directly loses its bar on a clamped rect.
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

// ButtonRowStartX returns the x column the button row starts at so it ends flush
// with the right margin. Shared by DrawButtons and ButtonClicked so hit-testing
// matches the drawing; exported so a dialog drawing status at the row's left end
// can stop short of the buttons.
func (d *ModalDialog) ButtonRowStartX(labels []string) int {
	return d.rect.Right() - 2 - d.ButtonRowWidth(labels)
}

// ButtonRowWidth is how many columns a button row occupies, gaps included.
func (d *ModalDialog) ButtonRowWidth(labels []string) int {
	total := 0
	for i, label := range labels {
		if i > 0 {
			total += 2
		}
		total += core.DisplayWidth("[ " + label + " ]")
	}
	return total
}

// DrawButtons renders a row of buttons at ButtonRowY, right-aligned within
// the dialog. activeIdx highlights that button.
func (d *ModalDialog) DrawButtons(s tcell.Screen, labels []string, activeIdx int) {
	d.DrawButtonsGated(s, labels, activeIdx, nil)
}

// DrawButtonsGated draws the button row with every button whose index is true in
// disabled in the disabled foreground (disabled may be nil or shorter). Drawing
// it gated does not gate it: the handler must still refuse the button.
func (d *ModalDialog) DrawButtonsGated(s tcell.Screen, labels []string, activeIdx int, disabled []bool) {
	y := d.ButtonRowY()
	// On a clamped rect, content laid out for the full height reaches this row and
	// the one below; the buttons are right-aligned, so without the clear a content
	// row's tail shows with a button row in the middle. A dialog with a button row
	// draws nothing of its own from ButtonRowY down.
	//
	// The clear belongs to this entry point alone: a split row draws its
	// right-aligned group here first, and a second clear from DrawButtonsAtGated
	// would wipe it.
	if d.clamped() {
		core.FillRect(s, core.Rect{X: d.rect.X + 1, Y: y, W: d.rect.W - 2, H: d.rect.Bottom() - 1 - y}, ' ', theme.StyleDialog())
	}
	d.DrawButtonsAtGated(s, d.ButtonRowStartX(labels), labels, activeIdx, disabled)
}

// DrawButtonsAtGated draws a gated button row starting at column x, for a group
// placed away from the right-aligned default (e.g. a destructive button at the
// left). It clears nothing; the caller draws the right-aligned group first.
func (d *ModalDialog) DrawButtonsAtGated(s tcell.Screen, x int, labels []string, activeIdx int, disabled []bool) {
	p := theme.Active()
	disabledStyle := tcell.StyleDefault.Background(p.ButtonBg).Foreground(p.TextDisabled)
	btnStyle := tcell.StyleDefault.Background(p.ButtonBg).Foreground(p.ButtonFg)
	activeStyle := tcell.StyleDefault.Background(p.ButtonActive).Foreground(color.White)
	col, y := x, d.ButtonRowY()
	for i, label := range labels {
		st := btnStyle
		if i == activeIdx {
			st = activeStyle
		}
		if i < len(disabled) && disabled[i] {
			st = disabledStyle
		}
		text := "[ " + label + " ]"
		core.DrawText(s, col, y, st, text)
		col += core.DisplayWidth(text) + 2
	}
}

// ButtonClicked returns the index of the button clicked, or -1. mouseDragging
// guards against the held-motion Button1 resend so a twitching click fires once.
func (d *ModalDialog) ButtonClicked(ev *tcell.EventMouse, labels []string) int {
	return d.ButtonClickedAt(ev, d.ButtonRowStartX(labels), labels)
}

// ButtonClickedAt is ButtonClicked for a row drawn at column x by
// DrawButtonsAtGated. A call that hits nothing latches nothing, so with two
// groups call order doesn't matter.
func (d *ModalDialog) ButtonClickedAt(ev *tcell.EventMouse, x int, labels []string) int {
	if ev.Buttons() != tcell.Button1 {
		return -1
	}
	mx, my := ev.Position()
	if my != d.ButtonRowY() {
		return -1
	}
	col := x
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
