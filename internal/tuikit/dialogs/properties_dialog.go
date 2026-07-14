package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

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
