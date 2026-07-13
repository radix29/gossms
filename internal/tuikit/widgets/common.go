package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// drawLabel draws a widget's inline label in the standard dialog text
// style. Currently only DropDown uses it (InputField/CheckBox/Button draw
// their own label/text inline).
func drawLabel(s tcell.Screen, x, y int, label string, p *theme.Palette) {
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	core.DrawText(s, x, y, st, label)
}
