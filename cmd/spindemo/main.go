// Command spindemo animates the candidate busy spinners side by side, to pick
// one by eye. Not in the release build.
//
// Keys: q / Ctrl+Q quits, space pauses, . single-steps while paused, + / -
// change speed, r resets the clock.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// tickMS is finer than the fastest spinner's PeriodMS so each spinner advances
// on its own schedule, not a shared frame rate.
const tickMS = 25

// speeds are percentages of each spinner's PeriodMS; defaultSpeed indexes 100%.
// Slow speeds are included because half speed exposes a bad frame better than
// double does.
var speeds = []int{25, 50, 75, 100, 150, 200, 300, 400}

const defaultSpeed = 3

func main() {
	s, err := core.Init()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spindemo:", err)
		os.Exit(1)
	}
	defer s.Fini()

	d := &demo{speed: defaultSpeed}
	tick := time.NewTicker(tickMS * time.Millisecond)
	defer tick.Stop()

	d.draw(s)
	s.Show()
	for {
		select {
		case ev, ok := <-s.EventQ():
			if !ok {
				return
			}
			switch e := ev.(type) {
			case *tcell.EventResize:
				s.Sync()
			case *tcell.EventKey:
				if e.Key() == tcell.KeyCtrlQ || e.Key() == tcell.KeyEscape ||
					(core.EvRune(e) == 'q' && e.Modifiers() == 0) {
					return
				}
				d.handleKey(e)
			}
		case <-tick.C:
			if !d.paused {
				d.advance(tickMS)
			}
		}
		d.draw(s)
		s.Show()
	}
}

// demo owns the animation clock. Elapsed time is accumulated, not read from
// time.Now, so pausing and single-stepping work like running.
type demo struct {
	elapsed int64 // scaled animation milliseconds
	speed   int   // index into speeds, scaling how fast elapsed accumulates
	paused  bool
}

func (d *demo) advance(ms int64) { d.elapsed += ms * int64(speeds[d.speed]) / 100 }

func (d *demo) handleKey(e *tcell.EventKey) {
	switch core.EvRune(e) {
	case ' ':
		d.paused = !d.paused
	case '.':
		d.advance(tickMS)
	case '+', '=':
		d.speed = core.Clamp(d.speed+1, 0, len(speeds)-1)
	case '-', '_':
		d.speed = core.Clamp(d.speed-1, 0, len(speeds)-1)
	case 'r':
		d.elapsed = 0
		d.speed = defaultSpeed
	}
}

// nameCol fits the longest spinner name plus padding.
const nameCol = 12

func (d *demo) draw(s tcell.Screen) {
	s.Clear()
	w, h := s.Size()

	title := theme.StyleChartTitle()
	label := theme.StyleDefault()
	dim := theme.StyleDisabled()
	spin := theme.StyleSelected()

	core.DrawTextClipped(s, 1, 0, w-2, title, "goSSMS spinner catalogue")
	status := fmt.Sprintf("speed %d%%   %s   t=%dms", speeds[d.speed], map[bool]string{true: "PAUSED", false: "running"}[d.paused], d.elapsed)
	core.DrawTextRight(s, 0, 0, w-1, dim, status)

	y := 2
	y = d.drawGroup(s, y, w, "Single cell (width 1)", singles(), label, dim, spin)
	y++
	d.drawGroup(s, y, w, "Multi cell (width 3-5)", multis(), label, dim, spin)

	help := "q quit   space pause   . step   +/- speed   r reset"
	if h > 1 {
		core.DrawTextClipped(s, 1, h-1, w-2, dim, help)
	}
}

// drawGroup lays out one group of spinners and returns the next free row. Each
// row shows the live animation, then the whole frame set at rest.
func (d *demo) drawGroup(s tcell.Screen, y, w int, heading string, group []widgets.Spinner, label, dim, spin tcell.Style) int {
	if y >= 0 {
		core.DrawTextClipped(s, 1, y, w-2, theme.StyleChartSection(), heading)
	}
	y += 2

	// widest is the animation column width, so notes align across widths 1–5.
	widest := 0
	for _, sp := range group {
		widest = max(widest, sp.Width())
	}

	for _, sp := range group {
		x := 1
		core.DrawText(s, x, y, label, core.PadRight(sp.Name, nameCol))
		x += nameCol

		// Pad to the group's widest frame so nothing after it shifts.
		sp.Draw(s, x, y, spin, time.Duration(d.elapsed)*time.Millisecond)
		core.DrawText(s, x+sp.Width(), y, spin, strings.Repeat(" ", widest-sp.Width()))
		x += widest + 2

		core.DrawText(s, x, y, dim, fmt.Sprintf("%4dms", sp.Period.Milliseconds()))
		x += 8

		core.DrawTextClipped(s, x, y, w-x-1, dim, notes[sp.Name])
		y++

		frames := "   " + strings.Join(sp.Frames, " ")
		core.DrawTextClipped(s, 1+nameCol, y, w-2-nameCol, theme.StyleDisabled(), frames)
		y += 2
	}
	return y
}

// singles and multis split the catalogue by width: a one-cell spinner fits
// inline in a status bar, a wider one wants its own line.
func singles() []widgets.Spinner { return byWidth(func(w int) bool { return w == 1 }) }
func multis() []widgets.Spinner  { return byWidth(func(w int) bool { return w > 1 }) }

func byWidth(keep func(int) bool) []widgets.Spinner {
	var out []widgets.Spinner
	for _, sp := range widgets.Spinners {
		if keep(sp.Width()) {
			out = append(out, sp)
		}
	}
	return out
}

// notes describe each spinner. Demo-only prose, so not on widgets.Spinner.
var notes = map[string]string{
	"braille":    "braille dots - smooth, quiet, needs a braille-capable font",
	"halfcircle": "half-filled circle rotating - reads as a loading pie",
	"pulse":      "block growing/shrinking in place - heartbeat, not a spin",
	"codex":      "Codex CLI's dot ramp swelling/fading in one cell",
	"dots3":      "ASCII ellipsis filling in and clearing - the only pure-ASCII one",
	"bounce4":    "dot bouncing left/right, easy to eye-track",
	"codex3":     "Codex dot ramp travelling with a fading tail",
}
