package widgets

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Spinner is a busy indicator's animation: a fixed-width frame sequence and the
// cadence it reads best at.
//
// Unlike the other widgets it is a value: no bounds, start time or timer, just
// a function of elapsed time. That keeps to the tuikit rule that a control never
// spawns a goroutine; the host's redraw clock (ticker, elapsed-timer tick)
// passes the elapsed duration in.
//
// Every frame has the same display width, so drawing one over the last leaves
// no stale cell. Width reports it and TestSpinnerFramesAreUniformWidth pins it
// for the package's spinners; a caller's own Spinner owes the same.
type Spinner struct {
	Name   string        // stable identifier, for a config value or a picker
	Frames []string      // animation frames, all of the same display width
	Period time.Duration // time each frame is held
}

// defaultSpinnerPeriod backs a Spinner built without one, so a zero Period
// animates slowly rather than dividing by zero.
const defaultSpinnerPeriod = 120 * time.Millisecond

// Width is the display width of every frame, or 0 for a Spinner with none.
func (sp Spinner) Width() int {
	if len(sp.Frames) == 0 {
		return 0
	}
	return core.DisplayWidth(sp.Frames[0])
}

// Frame returns the frame showing after elapsed time. Negative elapsed and a
// frameless Spinner give the first frame / "" rather than panicking (elapsed
// usually comes from subtracting two clocks).
func (sp Spinner) Frame(elapsed time.Duration) string {
	if len(sp.Frames) == 0 {
		return ""
	}
	if elapsed < 0 {
		elapsed = 0
	}
	period := sp.Period
	if period <= 0 {
		period = defaultSpinnerPeriod
	}
	return sp.Frames[int(elapsed/period)%len(sp.Frames)]
}

// Draw paints the frame for elapsed at x, y, padded to Width so a caller's own
// unequal-width frames still overwrite the whole slot.
func (sp Spinner) Draw(s tcell.Screen, x, y int, style tcell.Style, elapsed time.Duration) {
	if len(sp.Frames) == 0 {
		return
	}
	core.DrawText(s, x, y, style, core.PadRight(sp.Frame(elapsed), sp.Width()))
}

// DrawSince is Draw relative to a start time.
func (sp Spinner) DrawSince(s tcell.Screen, x, y int, style tcell.Style, start time.Time) {
	sp.Draw(s, x, y, style, time.Since(start))
}

// codexRamp is the dim-to-bright dot ramp of OpenAI's Codex CLI animations,
// read off its binary's glyphs: SpinnerCodex borrows the palette, not frames.
var codexRamp = []string{"·", "○", "◉", "●"}

// The catalogue: four spinners one cell wide, three wider. A one-cell spinner
// fits a status bar or tree "loading" marker without shifting following text.
var (
	// SpinnerBraille chases braille dots round the 2x4 cell: the smoothest and
	// quietest one-cell spinner, widely read as "working"; needs braille font
	// coverage.
	SpinnerBraille = Spinner{
		Name:   "braille",
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		Period: 130 * time.Millisecond,
	}

	// SpinnerHalfCircle rotates a half-filled circle, reading as a loading pie. The
	// slowest cadence, suiting a long operation.
	SpinnerHalfCircle = Spinner{
		Name:   "halfcircle",
		Frames: []string{"◐", "◓", "◑", "◒"},
		Period: 220 * time.Millisecond,
	}

	// SpinnerPulse grows and shrinks a block in place: a heartbeat ("still
	// connected") rather than progress.
	SpinnerPulse = Spinner{
		Name:   "pulse",
		Frames: []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂"},
		Period: 110 * time.Millisecond,
	}

	// SpinnerCodex swells and fades a dot through codexRamp: the round,
	// cell-centred reading of SpinnerPulse.
	SpinnerCodex = Spinner{
		Name:   "codex",
		Frames: []string{"·", "○", "◉", "●", "◉", "○"},
		Period: 180 * time.Millisecond,
	}

	// SpinnerDots3 fills in and clears an ellipsis. The only pure-ASCII spinner,
	// safe where the font may render the rest as tofu.
	SpinnerDots3 = Spinner{
		Name:   "dots3",
		Frames: []string{"   ", ".  ", ".. ", "..."},
		Period: 420 * time.Millisecond,
	}

	// SpinnerBounce4 walks a dot out and back rather than wrapping it, so
	// the eye tracks the motion instead of resyncing each cycle.
	SpinnerBounce4 = Spinner{
		Name:   "bounce4",
		Frames: []string{"●∙∙∙", "∙●∙∙", "∙∙●∙", "∙∙∙●", "∙∙●∙", "∙●∙∙"},
		Period: 170 * time.Millisecond,
	}

	// SpinnerCodex3 runs codexRamp across three cells a phase apart, so the
	// bright dot travels with a fading tail: the wide reading of SpinnerCodex.
	SpinnerCodex3 = Spinner{
		Name:   "codex3",
		Frames: codexPhases(3),
		Period: 160 * time.Millisecond,
	}
)

// Spinners is the catalogue in presentation order — one-cell first — for a
// picker or a demo to walk. SpinnerByName looks one up.
var Spinners = []Spinner{
	SpinnerBraille, SpinnerHalfCircle, SpinnerPulse, SpinnerCodex,
	SpinnerDots3, SpinnerBounce4, SpinnerCodex3,
}

// SpinnerByName returns the named catalogue spinner. The second result is
// false for an unknown name, so a stale config value falls back rather than
// animating an empty string.
func SpinnerByName(name string) (Spinner, bool) {
	for _, sp := range Spinners {
		if sp.Name == name {
			return sp, true
		}
	}
	return Spinner{}, false
}

// codexPhases builds the width-w travelling form of codexRamp: cell i of frame
// p shows the ramp value i phases along, so the bright end walks left to right
// with the dim end trailing. The ramp goes up and back down so the cycle closes
// without jumping from brightest to dimmest.
func codexPhases(w int) []string {
	cycle := append([]string{}, codexRamp...)
	for i := len(codexRamp) - 2; i > 0; i-- {
		cycle = append(cycle, codexRamp[i])
	}
	frames := make([]string, len(cycle))
	for p := range cycle {
		var s strings.Builder
		for i := range w {
			s.WriteString(cycle[(p+i)%len(cycle)])
		}
		frames[p] = s.String()
	}
	return frames
}
