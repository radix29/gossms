package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// PanelTitleHeight is the rows DrawPanelTitle's heading takes.
const PanelTitleHeight = 1

// DrawPanelTitle draws one chart panel's heading across the top of r and
// returns the rect left for the chart itself: below the heading, inset one
// column each side. A rect with no area returns the zero Rect.
func DrawPanelTitle(s tcell.Screen, r core.Rect, title string) core.Rect {
	if r.W <= 0 || r.H <= 0 {
		return core.Rect{}
	}
	style := theme.StyleChartTitle()
	core.FillRect(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: PanelTitleHeight}, ' ', style)
	core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, style, title)
	return core.Rect{X: r.X + 1, Y: r.Y + PanelTitleHeight, W: r.W - 2, H: r.H - PanelTitleHeight}
}
