package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// ModalDialog — base type
// ---------------------------------------------------------------------------

// ModalDialog is the foundation for all pop-up windows.
// Embed this struct and call InitModal() from your constructor.
type ModalDialog struct {
	rect       core.Rect
	reqW, reqH int // last size passed to InitModal/SetSize, pre-clamp
	title      string
	visible    bool
	screen     tcell.Screen // needed for Size() during recentre
}

// InitModal sets up the dialog for the given screen, title, and size.
// Call this from the embedding type's constructor.
func (d *ModalDialog) InitModal(s tcell.Screen, title string, w, h int) {
	d.screen = s
	d.title = title
	d.reqW, d.reqH = w, h
	d.recentre()
}

// SetSize resizes the dialog and recentres it. For dialogs whose content
// grows with the screen (see propsheet.PropertySheet) call this from Show
// or Draw with a size computed from the current screen dimensions.
func (d *ModalDialog) SetSize(w, h int) {
	d.reqW, d.reqH = w, h
	d.recentre()
}

// recentre repositions the dialog in the centre of the screen, clamping its
// size to fit first — a dialog wider or taller than the terminal (a narrow
// window, or a fixed-size dialog like ConfirmDialog on a small screen)
// would otherwise draw its right/bottom border, and anything docked to it
// (e.g. DrawButtons' right-aligned button row), off-screen entirely. The
// clamp is recomputed from reqW/reqH (the last size actually requested via
// InitModal/SetSize) rather than applied to rect.W/H in place, so a dialog
// shown small on a cramped terminal still returns to its full requested
// size the next time it's shown on a larger one, instead of the clamp
// sticking permanently.
func (d *ModalDialog) recentre() {
	if d.screen == nil {
		return
	}
	sw, sh := d.screen.Size()
	d.rect.W = core.Min(d.reqW, sw)
	d.rect.H = core.Min(d.reqH, sh)
	d.rect.X = core.Max(0, (sw-d.rect.W)/2)
	d.rect.Y = core.Max(0, (sh-d.rect.H)/2)
}

// Show makes the dialog visible and recentres it.
func (d *ModalDialog) Show() {
	d.recentre()
	d.visible = true
}

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

// ConsumeOutsideClick returns true if the mouse event originated outside
// the dialog. The dialog is always visible when this is called.
func (d *ModalDialog) ConsumeOutsideClick(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	mx, my := ev.Position()
	return !d.rect.Contains(mx, my)
}

// dialogDimNum/dialogDimDen fade the underlying UI toward the overlay colour
// behind an open dialog; 3/5 leaves it at ~40% of its own colour — clearly
// inactive but still legible.
const dialogDimNum, dialogDimDen = 3, 5

// DrawBase fades the underlying UI and draws the dialog box onto the screen.
// Embedding types should call DrawBase first, then render their own content
// within InnerRect().
func (d *ModalDialog) DrawBase(s tcell.Screen) {
	p := theme.Active()

	// Fade the already-drawn UI in place instead of painting a solid overlay,
	// so the inactive interface stays visible but dimmed behind the dialog.
	sw, sh := s.Size()
	core.DimArea(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, p.DialogOverlay, dialogDimNum, dialogDimDen)

	// Dialog background (opaque, over the dimmed UI)
	core.FillRect(s, d.rect, ' ', theme.StyleDialog())

	// Border + title
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogBorder)
	titleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTitle).Bold(true)
	core.DrawBoxTitle(s, d.rect, d.title, borderStyle, titleStyle)
}

// InnerRect returns the usable interior rectangle (excluding border).
func (d *ModalDialog) InnerRect() core.Rect { return d.rect.Inner(1) }

// ButtonRowY returns the y coordinate of the standard button row
// (2 rows from the bottom, above the border).
func (d *ModalDialog) ButtonRowY() int { return d.rect.Y + d.rect.H - 3 }

// DrawSeparator draws a horizontal line one row above ButtonRowY.
func (d *ModalDialog) DrawSeparator(s tcell.Screen) {
	p := theme.Active()
	sep := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, d.rect.X+1, d.ButtonRowY()-1, d.rect.W-2, sep)
}

// buttonRowStartX returns the x column the button row should start at so
// the whole row ends flush with the dialog's right-side margin. Shared by
// DrawButtons and ButtonClicked so hit-testing always matches what was
// actually drawn.
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
	activeStyle := tcell.StyleDefault.Background(p.ButtonActive).Foreground(tcell.ColorWhite)
	col := d.buttonRowStartX(labels)
	y := d.ButtonRowY()
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

// ButtonClicked returns the index of the button clicked, or -1.
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
			return i
		}
		col += w + 2
	}
	return -1
}
