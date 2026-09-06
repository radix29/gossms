package widgets

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Spinner is a busy indicator's animation: a fixed-width frame sequence and
// the cadence it reads best at.
//
// Unlike the other widgets here it is a value, not a stateful control: it
// holds no bounds, no start time and no timer, and answers which frame shows
// purely as a function of elapsed time. That keeps it inside the tuikit rule
// that a control never spawns a goroutine — the host already has a redraw
// clock (a ticker, a query's elapsed-timer tick, an animation frame) and
// passes the elapsed duration in.
//
// Every frame of a Spinner has the same display width, so drawing one frame
// over the last never leaves a stale cell behind. Width reports it, and
// TestSpinnerFramesAreUniformWidth pins it for the package's own spinners;
// a caller defining its own Spinner owes itself the same.
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
// frameless Spinner both give the first frame / the empty string rather than
// panicking, since elapsed usually comes from a subtraction of two clocks.
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

// FrameSince is Frame relative to the moment the operation started, the form
// a caller holding a start time wants.
func (sp Spinner) FrameSince(start time.Time) string {
	return sp.Frame(time.Since(start))
}

// Draw paints the frame for elapsed at x, y, padded to Width so a caller that
// hands a Spinner its own frames of unequal width still overwrites the whole
// slot instead of leaving the wider frame's tail on screen.
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

// codexRamp is the dim-to-bright dot ramp OpenAI's Codex CLI draws its
// terminal animations from — read off the glyphs in its binary's animation
// assets, so SpinnerCodex borrows the palette rather than copying frames.
var codexRamp = []string{"·", "○", "◉", "●"}

// The spinners below are the catalogue: four one cell wide, three wider.
// A one-cell spinner fits a status bar or a tree node's "loading" marker
// without moving the text after it; a wider one suits a line of its own.
var (
	// SpinnerBraille chases braille dots round the 2x4 cell. The smoothest
	// and quietest of the one-cell set, and the one most terminals' users
	// already read as "working"; needs a font with braille coverage.
	SpinnerBraille = Spinner{
		Name:   "braille",
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		Period: 130 * time.Millisecond,
	}

	// SpinnerHalfCircle rotates a half-filled circle, reading as a loading
	// pie rather than a spin. The slowest cadence here, which suits an
	// operation expected to take a while.
	SpinnerHalfCircle = Spinner{
		Name:   "halfcircle",
		Frames: []string{"◐", "◓", "◑", "◒"},
		Period: 220 * time.Millisecond,
	}

	// SpinnerPulse grows and shrinks a block in place. Nothing rotates, so
	// it reads as a heartbeat — "still connected", rather than "progressing".
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

	// SpinnerDots3 fills in and clears an ellipsis. The only spinner here
	// that is pure ASCII, so the only one safe where the font or terminal
	// may render the rest as tofu.
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

// codexPhases builds the width-w travelling form of codexRamp: cell i of
// frame p shows the ramp value i phases along, so the bright end walks left
// to right and the dim end trails it. The ramp is walked up and back down so
// the cycle closes without a jump from the brightest glyph straight to the
// dimmest.
func codexPhases(w int) []string {
	cycle := append([]string{}, codexRamp...)
	for i := len(codexRamp) - 2; i > 0; i-- {
		cycle = append(cycle, codexRamp[i])
	}
	frames := make([]string, len(cycle))
	for p := range cycle {
		s := ""
		for i := range w {
			s += cycle[(p+i)%len(cycle)]
		}
		frames[p] = s
	}
	return frames
}
