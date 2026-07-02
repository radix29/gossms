// Package dialogs provides the ModalDialog base type and generic re-usable
// dialog implementations (AlertDialog, ConfirmDialog, PropertiesDialog).
//
// Every dialog embeds ModalDialog which:
//   - Draws a full-screen dim overlay before its own box
//   - Intercepts all mouse clicks outside its border (focus trap)
//   - Cannot lose focus until explicitly dismissed
//
// Application-specific dialogs (ConnectDialog, HelpDialog, etc.) live in
// the tui package and embed ModalDialog from here.
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
	rect    core.Rect
	title   string
	visible bool
	screen  tcell.Screen // needed for Size() during recentre
}

// InitModal sets up the dialog for the given screen, title, and size.
// Call this from the embedding type's constructor.
func (d *ModalDialog) InitModal(s tcell.Screen, title string, w, h int) {
	d.screen = s
	d.title = title
	d.rect.W = w
	d.rect.H = h
	d.recentre()
}

// recentre repositions the dialog in the centre of the screen.
func (d *ModalDialog) recentre() {
	if d.screen == nil {
		return
	}
	sw, sh := d.screen.Size()
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

// DrawBase draws the dim overlay and the dialog box onto the screen.
// Embedding types should call DrawBase first, then render their own content
// within InnerRect().
func (d *ModalDialog) DrawBase(s tcell.Screen) {
	// Full-screen dim overlay
	sw, sh := s.Size()
	overlayStyle := tcell.StyleDefault.Background(theme.Active().DialogOverlay).Foreground(theme.Active().TextDim)
	core.FillRect(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, ' ', overlayStyle)

	// Dialog background
	core.FillRect(s, d.rect, ' ', theme.StyleDialog())

	// Border + title
	p := theme.Active()
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

// DrawButtons renders a row of buttons at ButtonRowY.
// activeIdx highlights that button. Returns the x position after the last button.
func (d *ModalDialog) DrawButtons(s tcell.Screen, labels []string, activeIdx int) {
	p := theme.Active()
	btnStyle := tcell.StyleDefault.Background(p.ButtonBg).Foreground(p.ButtonFg)
	activeStyle := tcell.StyleDefault.Background(p.ButtonActive).Foreground(tcell.ColorWhite)
	col := d.rect.X + 2
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
// Uses the same display-width geometry as DrawButtons so hit-testing always
// matches what was actually drawn.
func (d *ModalDialog) ButtonClicked(ev *tcell.EventMouse, labels []string) int {
	if ev.Buttons() != tcell.Button1 {
		return -1
	}
	mx, my := ev.Position()
	if my != d.ButtonRowY() {
		return -1
	}
	col := d.rect.X + 2
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

// ---------------------------------------------------------------------------
// PropertiesDialog — generic key/value viewer
// ---------------------------------------------------------------------------

// PropertyRow is a single key/value pair.
type PropertyRow struct {
	Key   string
	Value string
}

// PropertiesDialog renders a scrollable table of PropertyRows.
// It embeds ModalDialog to inherit all focus-trap behaviour.
type PropertiesDialog struct {
	ModalDialog
	rows   []PropertyRow
	scroll int
}

// NewPropertiesDialog creates a PropertiesDialog.
func NewPropertiesDialog(s tcell.Screen) *PropertiesDialog {
	d := new(PropertiesDialog{})
	d.InitModal(s, "Properties", 60, 24)
	return d
}

// Show populates the dialog with the given title and rows, then shows it.
func (d *PropertiesDialog) ShowProperties(title string, rows []PropertyRow) {
	d.SetTitle(title)
	d.rows = rows
	d.scroll = 0
	d.ModalDialog.Show()
}

// Draw renders the properties dialog.
func (d *PropertiesDialog) Draw(s tcell.Screen) {
	if !d.visible {
		return
	}
	d.DrawBase(s)
	p := theme.Active()

	inner := d.InnerRect()
	const keyW = 22
	dataH := inner.H - 3 // header row + separator + close button

	// Header
	hdrStyle := tcell.StyleDefault.Background(p.GridHeader).Foreground(p.Text).Bold(true)
	core.FillRect(s, core.Rect{X: inner.X, Y: inner.Y + 1, W: inner.W, H: 1}, ' ', hdrStyle)
	core.DrawTextClipped(s, inner.X+1, inner.Y+1, keyW, hdrStyle, "Property")
	s.SetContent(inner.X+keyW+2, inner.Y+1, '|', nil, hdrStyle)
	core.DrawTextClipped(s, inner.X+keyW+4, inner.Y+1, inner.W-keyW-4, hdrStyle, "Value")

	keyStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive).Bold(true)
	valStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)

	for row := 0; row < dataH; row++ {
		idx := d.scroll + row
		if idx >= len(d.rows) {
			break
		}
		pr := d.rows[idx]
		y := inner.Y + 2 + row
		core.FillRect(s, core.Rect{X: inner.X, Y: y, W: inner.W, H: 1}, ' ', valStyle)
		core.DrawTextClipped(s, inner.X+1, y, keyW, keyStyle, pr.Key)
		s.SetContent(inner.X+keyW+2, y, '|', nil, valStyle)
		core.DrawTextClipped(s, inner.X+keyW+4, y, inner.W-keyW-4, valStyle, pr.Value)
	}

	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"Close"}, 0)
}

// HandleKey handles keyboard events.
func (d *PropertiesDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	inner := d.InnerRect()
	dataH := inner.H - 3
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		d.Hide()
	case tcell.KeyUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.KeyDown:
		if d.scroll+dataH < len(d.rows) {
			d.scroll++
		}
	}
	return true
}

// HandleMouse handles mouse events.
func (d *PropertiesDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"Close"}) == 0 {
		d.Hide()
	}
	return true
}

// ---------------------------------------------------------------------------
// AlertDialog — single-button info message
// ---------------------------------------------------------------------------

// AlertDialog shows a message with a single OK button.
type AlertDialog struct {
	ModalDialog
	message string
}

// NewAlertDialog creates an AlertDialog.
func NewAlertDialog(s tcell.Screen) *AlertDialog {
	d := new(AlertDialog{})
	d.InitModal(s, "Alert", 44, 9)
	return d
}

// ShowAlert shows a message.
func (d *AlertDialog) ShowAlert(title, message string) {
	d.SetTitle(title)
	d.message = message
	d.ModalDialog.Show()
}

// Draw renders the alert.
func (d *AlertDialog) Draw(s tcell.Screen) {
	if !d.visible {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	core.DrawTextClipped(s, inner.X+1, inner.Y+2, inner.W-2, msgStyle, d.message)
	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"OK"}, 0)
}

// HandleKey handles keyboard events.
func (d *AlertDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyEnter {
		d.Hide()
	}
	return true
}

// HandleMouse handles mouse events.
func (d *AlertDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"OK"}) == 0 {
		d.Hide()
	}
	return true
}

// ---------------------------------------------------------------------------
// ConfirmDialog — two-button yes/no
// ---------------------------------------------------------------------------

// ConfirmDialog shows a question with Yes and No buttons.
type ConfirmDialog struct {
	ModalDialog
	message   string
	btnFocus  int
	OnConfirm func(confirmed bool)
}

// NewConfirmDialog creates a ConfirmDialog.
func NewConfirmDialog(s tcell.Screen) *ConfirmDialog {
	d := new(ConfirmDialog{})
	d.InitModal(s, "Confirm", 48, 9)
	return d
}

// ShowConfirm shows the dialog with the given title, message, and callback.
func (d *ConfirmDialog) ShowConfirm(title, message string, onConfirm func(bool)) {
	d.SetTitle(title)
	d.message = message
	d.btnFocus = 0
	d.OnConfirm = onConfirm
	d.ModalDialog.Show()
}

// Draw renders the confirm dialog.
func (d *ConfirmDialog) Draw(s tcell.Screen) {
	if !d.visible {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	core.DrawTextClipped(s, inner.X+1, inner.Y+2, inner.W-2, msgStyle, d.message)
	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"Yes", "No"}, d.btnFocus)
}

// HandleKey handles keyboard events.
func (d *ConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.finish(false)
	case tcell.KeyEnter:
		d.finish(d.btnFocus == 0)
	case tcell.KeyTab, tcell.KeyRight:
		d.btnFocus = (d.btnFocus + 1) % 2
	case tcell.KeyLeft:
		d.btnFocus = (d.btnFocus - 1 + 2) % 2
	}
	return true
}

// HandleMouse handles mouse events.
func (d *ConfirmDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"Yes", "No"}); i >= 0 {
		d.finish(i == 0)
	}
	return true
}

func (d *ConfirmDialog) finish(confirmed bool) {
	d.Hide()
	if d.OnConfirm != nil {
		d.OnConfirm(confirmed)
	}
}
