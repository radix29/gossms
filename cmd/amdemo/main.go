// Command amdemo hosts the Activity Monitor's History and Sample
// dashboards full-screen against deterministic mock data, for visually
// checking the dashboard layout and the tuikit/charts primitives without a
// SQL Server connection. Not part of the release build (see
// .github/workflows/release.yml, which only builds cmd/gossms).
//
// Keys: Tab switches dashboards, arrows/PgUp/PgDn/Home/End scroll, q quits.
package main

import (
	"fmt"
	"os"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

func main() {
	s, err := core.Init()
	if err != nil {
		fmt.Fprintln(os.Stderr, "amdemo:", err)
		os.Exit(1)
	}
	defer s.Fini()

	d := &demo{history: MockHistory()}
	d.sample = MockSample(d.history)
	d.draw(s)

	for ev := range s.EventQ() {
		switch e := ev.(type) {
		case *tcell.EventResize:
			s.Sync()
		case *tcell.EventKey:
			if e.Key() == tcell.KeyCtrlQ || (core.EvRune(e) == 'q' && e.Modifiers() == 0) {
				return
			}
			d.handleKey(e)
		}
		d.draw(s)
		s.Show()
	}
}

// demo holds the two dashboards and the viewport position over whichever is
// showing. The canvases are rendered fresh each draw rather than cached:
// the real panel redraws on every collection tick anyway, so this is the
// same cost the shipped code pays.
type demo struct {
	history dashboard.HistoryView
	sample  dashboard.SampleView

	onSample bool
	scrollX  int
	scrollY  int
}

// canvasSize is the fixed dashboard size for whichever tab is showing.
func (d *demo) canvasSize() (int, int) {
	if d.onSample {
		return dashboard.SampleCanvasW, dashboard.SampleCanvasH
	}
	return dashboard.HistoryCanvasW, dashboard.HistoryCanvasH
}

func (d *demo) handleKey(e *tcell.EventKey) {
	switch e.Key() {
	case tcell.KeyTab, tcell.KeyBacktab:
		d.onSample = !d.onSample
		d.scrollX, d.scrollY = 0, 0
	case tcell.KeyDown:
		d.scrollY++
	case tcell.KeyUp:
		d.scrollY--
	case tcell.KeyRight:
		d.scrollX += 4
	case tcell.KeyLeft:
		d.scrollX -= 4
	case tcell.KeyPgDn:
		d.scrollY += 10
	case tcell.KeyPgUp:
		d.scrollY -= 10
	case tcell.KeyHome:
		d.scrollX, d.scrollY = 0, 0
	case tcell.KeyEnd:
		_, h := d.canvasSize()
		d.scrollY = h
	}
}

// draw renders the active dashboard into a canvas of its fixed size, then
// blits the visible window — the same two-step the Activity Monitor panel
// uses, so what the harness shows is what the panel will show.
func (d *demo) draw(s tcell.Screen) {
	w, h := s.Size()
	view := core.Rect{X: 0, Y: 0, W: w, H: h - 1} // last row is the status line
	core.FillRect(s, core.Rect{X: 0, Y: 0, W: w, H: h}, ' ', theme.StylePanel())

	cw, ch := d.canvasSize()
	// A terminal wider than the canvas gets a wider canvas rather than a
	// stretched one: the extra columns become more time buckets.
	if w > cw {
		cw = w
	}
	c := charts.NewCanvas(cw, ch)
	if d.onSample {
		dashboard.DrawSample(c, c.Rect(), d.sample)
	} else {
		dashboard.DrawHistory(c, c.Rect(), d.history)
	}

	d.scrollX = core.Clamp(d.scrollX, 0, max(cw-view.W, 0))
	d.scrollY = core.Clamp(d.scrollY, 0, max(ch-view.H, 0))
	c.Blit(s, core.Rect{X: d.scrollX, Y: d.scrollY, W: view.W, H: view.H}, view)

	name := "History"
	if d.onSample {
		name = "Sample"
	}
	status := fmt.Sprintf(" %s  canvas %d×%d  view %d×%d  scroll %d,%d   Tab: switch   arrows/PgUp/PgDn: scroll   q: quit",
		name, cw, ch, view.W, view.H, d.scrollX, d.scrollY)
	core.FillRect(s, core.Rect{X: 0, Y: h - 1, W: w, H: 1}, ' ', theme.StyleStatusBar())
	core.DrawTextClipped(s, 0, h-1, w, theme.StyleStatusBar(), status)
}
