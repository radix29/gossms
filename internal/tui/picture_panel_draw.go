package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// picture_panel_draw.go paints PicturePanel: title bar, toolbar with the
// picture's facts, then the art, centred when it is smaller than the view and
// scrolled with scrollbars when a zoom makes it larger.

// Draw renders the panel.
func (pp *PicturePanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.MenuText)
	if pp.active {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.SelectedText).Bold(true)
	}
	core.FillRect(s, core.Rect{X: pp.rect.X, Y: pp.rect.Y, W: pp.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, pp.rect.X+1, pp.rect.Y, pp.rect.W-2, titleStyle, pp.title)
	pp.drawToolbar(s)
	pp.drawView(s)
}

// drawToolbar paints the toolbar cells in Activity Monitor's tooltip scheme,
// then the picture's facts in the room left over.
func (pp *PicturePanel) drawToolbar(s tcell.Screen) {
	if pp.toolRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, pp.toolRect, ' ', barStyle)
	pp.refreshToolLabels()
	next := pp.tools.Layout(pp.toolRect, "")
	for i, t := range pp.tools.Cells {
		if t.Rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if pp.toolDisabled(i) {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, t.Rect, ' ', style)
		core.DrawText(s, t.Rect.X+1, t.Rect.Y, style, t.Label)
	}
	if !pp.tools.More.Rect.IsZero() {
		style := theme.StyleTooltip()
		core.FillRect(s, pp.tools.More.Rect, ' ', style)
		core.DrawText(s, pp.tools.More.Rect.X+1, pp.tools.More.Rect.Y, style, pp.tools.More.Label)
	}
	if w := pp.toolRect.Right() - next; w > 0 {
		core.DrawTextClipped(s, next, pp.toolRect.Y, w, barStyle, pp.infoText())
	}
}

// infoText is the toolbar's summary: format, pixel size, byte size and zoom.
func (pp *PicturePanel) infoText() string {
	if !pp.ready() {
		return ""
	}
	b := pp.flat.Bounds()
	parts := []string{
		fmt.Sprintf("%s %d×%d", strings.ToUpper(pp.format), b.Dx(), b.Dy()),
		formatBytes(int64(len(pp.data))),
	}
	zoom := fmt.Sprintf("%d%%", pp.zoomPercent())
	if !pp.zoomed() {
		zoom = "Fit " + zoom
	}
	return strings.Join(append(parts, zoom), " · ")
}

// drawView paints the picture area: a message while there is no art, the art
// otherwise.
func (pp *PicturePanel) drawView(s tcell.Screen) {
	r := pp.viewRect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	ground := theme.StyleDefault()
	core.FillRect(s, r, ' ', ground)
	msg := ""
	switch {
	case pp.loading:
		msg = "Decoding picture..."
	case pp.errText != "":
		msg = pp.errText
	}
	art := pp.render()
	if art == nil && msg == "" {
		msg = pp.renderFail
	}
	if art == nil {
		core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, ground.Foreground(pal.TextDim), msg)
		return
	}
	area := pp.artArea()
	// Centred on whichever axis the art is narrower than the view.
	offX, offY := area.X, area.Y
	if art.Cols < area.W {
		offX += (area.W - art.Cols) / 2
	}
	if art.Rows < area.H {
		offY += (area.H - art.Rows) / 2
	}
	// ASCII art carries no background (alpha 0): its glyphs were picked to hit
	// the target brightness over black, gotuipic's default ground, so that is
	// what goes behind them in every theme.
	black := tcell.NewRGBColor(0, 0, 0)
	for y := 0; y < area.H && pp.scrollY+y < art.Rows; y++ {
		row := (pp.scrollY + y) * art.Cols
		for x := 0; x < area.W && pp.scrollX+x < art.Cols; x++ {
			i := row + pp.scrollX + x
			c := art.Cells[i]
			st := tcell.StyleDefault.
				Foreground(pictureColor(c.FG, black)).
				Background(pictureColor(c.BG, black))
			s.Put(offX+x, offY+y, pp.glyphs[i], st)
		}
	}
	if pp.zoomed() {
		track := tcell.StyleDefault.Background(pal.GridHeader).Foreground(pal.Border)
		thumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
		core.DrawScrollbar(s, area.Right(), area.Y, area.H, art.Rows, area.H, pp.scrollY, track, thumb)
		core.DrawScrollbarH(s, area.X, area.Bottom(), area.W, art.Cols, area.W, pp.scrollX, track, thumb)
		core.PutRune(s, area.Right(), area.Bottom(), ' ', track)
	}
}
