package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// PropertiesDialog — generic key/value viewer
// ---------------------------------------------------------------------------

// Default dialog size, restored by every ShowProperties call so a caller
// that widened the dialog with ShowPropertiesSized doesn't leave the next,
// unrelated showing oversized — the same instance is reused for every
// feature that uses this dialog.
const (
	propsDefaultW = 60
	propsDefaultH = 24

	// Minimum width of the Property column; ShowPropertiesSized widens it to
	// the longest key it was given, up to a third of the dialog.
	propsKeyW = 22
)

// PropertyRow is a single key/value pair, or — with Section set — a group
// heading whose Key spans the full width and whose Value is ignored.
type PropertyRow struct {
	Key     string
	Value   string
	Section bool
}

// PropertySection returns a group-heading row for the given caption.
func PropertySection(caption string) PropertyRow {
	return PropertyRow{Key: caption, Section: true}
}

// PropertiesDialog renders a scrollable table of PropertyRows.
// It embeds ModalDialog to inherit all focus-trap behaviour.
type PropertiesDialog struct {
	ModalDialog
	rows   []PropertyRow
	scroll int
	keyW   int
}

// NewPropertiesDialog creates a PropertiesDialog.
func NewPropertiesDialog(s tcell.Screen) *PropertiesDialog {
	d := new(PropertiesDialog{keyW: propsKeyW})
	d.InitModal(s, "Properties", propsDefaultW, propsDefaultH)
	return d
}

// ShowProperties populates the dialog with the given title and rows at the
// default size, then shows it.
func (d *PropertiesDialog) ShowProperties(title string, rows []PropertyRow) {
	d.ShowPropertiesSized(title, rows, propsDefaultW, propsDefaultH)
}

// ShowPropertiesSized is ShowProperties with an explicit dialog size, for
// content that doesn't fit the default 60x24 (the About box). recentre
// clamps w/h to the terminal, so asking for more than the screen has is
// safe.
func (d *PropertiesDialog) ShowPropertiesSized(title string, rows []PropertyRow, w, h int) {
	d.SetTitle(title)
	d.rows = rows
	d.scroll = 0
	d.keyW = propsKeyW
	for _, r := range rows {
		if r.Section {
			continue
		}
		if kw := core.DisplayWidth(r.Key); kw > d.keyW {
			d.keyW = kw
		}
	}
	d.keyW = min(d.keyW, max(propsKeyW, w/3))
	d.SetSize(w, h)
	d.ModalDialog.Show()
}

// propsDataH is how many rows fit between the header and the button row.
// The dialog spends five of its inner lines on chrome — a blank line, the
// column header, the separator, the button row, and the blank line under it
// — so a naive inner.H-3 draws the last two rows straight under the
// separator and buttons, where DrawSeparator/DrawButtons overwrite them and
// the scroll clamp then refuses to bring them into view at all.
func propsDataH(inner core.Rect) int { return max(0, inner.H-5) }

// Draw renders the properties dialog.
func (d *PropertiesDialog) Draw(s tcell.Screen) {
	if !d.visible {
		return
	}
	d.DrawBase(s)
	p := theme.Active()

	inner := d.InnerRect()
	keyW := d.keyW
	dataH := propsDataH(inner)

	// Header
	hdrStyle := tcell.StyleDefault.Background(p.GridHeader).Foreground(p.Text).Bold(true)
	core.FillRect(s, core.Rect{X: inner.X, Y: inner.Y + 1, W: inner.W, H: 1}, ' ', hdrStyle)
	core.DrawTextClipped(s, inner.X+1, inner.Y+1, keyW, hdrStyle, "Property")
	s.SetContent(inner.X+keyW+2, inner.Y+1, '|', nil, hdrStyle)
	core.DrawTextClipped(s, inner.X+keyW+4, inner.Y+1, inner.W-keyW-4, hdrStyle, "Value")

	keyStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive).Bold(true)
	valStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	secStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive).Bold(true)
	ruleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)

	for row := 0; row < dataH; row++ {
		idx := d.scroll + row
		if idx >= len(d.rows) {
			break
		}
		pr := d.rows[idx]
		y := inner.Y + 2 + row
		if pr.Section {
			// A rule rather than a background band: GridHeader and DialogBg
			// are the same colour in several themes, so a filled row would be
			// indistinguishable from an ordinary one.
			core.FillRect(s, core.Rect{X: inner.X, Y: y, W: inner.W, H: 1}, '─', ruleStyle)
			core.DrawTextClipped(s, inner.X+1, y, inner.W-2, secStyle, " "+pr.Key+" ")
			continue
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: y, W: inner.W, H: 1}, ' ', valStyle)
		core.DrawTextClipped(s, inner.X+1, y, keyW, keyStyle, pr.Key)
		s.SetContent(inner.X+keyW+2, y, '|', nil, valStyle)
		core.DrawTextClipped(s, inner.X+keyW+4, y, inner.W-keyW-4, valStyle, pr.Value)
	}
	if len(d.rows) > dataH {
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, d.Rect().Right()-1, inner.Y+2, dataH, len(d.rows), dataH, d.scroll, sbStyle, sbThumb)
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
	dataH := propsDataH(inner)
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
		return true
	}
	inner := d.InnerRect()
	dataH := propsDataH(inner)
	if d.ScrollbarDrag(ev, d.Rect().Right()-1, inner.Y+2, dataH, len(d.rows), &d.scroll) {
		return true
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.WheelDown:
		if d.scroll+dataH < len(d.rows) {
			d.scroll++
		}
	}
	return true
}
