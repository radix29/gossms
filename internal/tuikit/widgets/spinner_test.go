package widgets

import (
	"testing"
	"time"
)

// TestSpinnerFramesAreUniformWidth is the defect a static frame listing can't
// show: a frame narrower than its neighbours leaves the tail of the previous
// one on screen, and only while animating. It also pins the widths the
// catalogue advertises — one cell, or three to five.
func TestSpinnerFramesAreUniformWidth(t *testing.T) {
	for _, sp := range Spinners {
		if len(sp.Frames) < 2 {
			t.Errorf("%s: %d frames, want at least 2", sp.Name, len(sp.Frames))
			continue
		}
		w := sp.Width()
		if w != 1 && (w < 3 || w > 5) {
			t.Errorf("%s: width %d, want 1 or 3-5", sp.Name, w)
		}
		for i, f := range sp.Frames {
			if got := len([]rune(f)); got != w {
				// Every catalogue frame is single-width runes, so a rune
				// count and a display width agree; comparing both catches a
				// frame padded with the wrong thing.
				t.Errorf("%s: frame %d %q has %d runes, want %d", sp.Name, i, f, got, w)
			}
		}
		if sp.Period <= 0 {
			t.Errorf("%s: Period %v, want > 0", sp.Name, sp.Period)
		}
	}
}

// TestSpinnerNamesAreUniqueAndResolve pins the name -> spinner lookup a config
// value goes through, including the miss that must not resolve to a spinner
// with no frames.
func TestSpinnerNamesAreUniqueAndResolve(t *testing.T) {
	seen := map[string]bool{}
	for _, sp := range Spinners {
		if sp.Name == "" {
			t.Error("a catalogue spinner has an empty Name")
		}
		if seen[sp.Name] {
			t.Errorf("duplicate spinner name %q", sp.Name)
		}
		seen[sp.Name] = true

		got, ok := SpinnerByName(sp.Name)
		if !ok || got.Name != sp.Name {
			t.Errorf("SpinnerByName(%q) = %q, %v; want %q, true", sp.Name, got.Name, ok, sp.Name)
		}
	}
	if _, ok := SpinnerByName("no-such-spinner"); ok {
		t.Error("SpinnerByName(unknown) = true, want false")
	}
}

// TestSpinnerFrameAdvancesAndWraps walks one spinner a frame at a time across
// a whole cycle and one past it. Asserting the frame *sequence* rather than a
// single lookup is what catches an off-by-one in the index arithmetic, which
// still returns a valid frame.
func TestSpinnerFrameAdvancesAndWraps(t *testing.T) {
	sp := Spinner{Name: "t", Frames: []string{"a", "b", "c"}, Period: 100 * time.Millisecond}

	for i, want := range []string{"a", "b", "c", "a", "b"} {
		// Mid-frame, not on the boundary: a spinner driven by a redraw clock
		// is almost never sampled exactly on one.
		at := time.Duration(i)*sp.Period + sp.Period/2
		if got := sp.Frame(at); got != want {
			t.Errorf("Frame(%v) = %q, want %q", at, got, want)
		}
	}
	// The boundary itself belongs to the later frame.
	if got := sp.Frame(sp.Period); got != "b" {
		t.Errorf("Frame(%v) = %q, want %q", sp.Period, got, "b")
	}
}

// TestSpinnerToleratesDegenerateValues covers the three inputs that would
// otherwise panic or divide by zero, each of which reaches Frame from
// ordinary code: a zero-value Spinner, a Period nobody set, and a negative
// elapsed from subtracting two clocks.
func TestSpinnerToleratesDegenerateValues(t *testing.T) {
	var zero Spinner
	if got := zero.Frame(time.Second); got != "" {
		t.Errorf("zero Spinner Frame = %q, want empty", got)
	}
	if got := zero.Width(); got != 0 {
		t.Errorf("zero Spinner Width = %d, want 0", got)
	}

	noPeriod := Spinner{Frames: []string{"a", "b"}}
	if got := noPeriod.Frame(defaultSpinnerPeriod + 1); got != "b" {
		t.Errorf("Frame with no Period = %q, want %q", got, "b")
	}

	sp := Spinners[0]
	if got := sp.Frame(-time.Second); got != sp.Frames[0] {
		t.Errorf("Frame(negative) = %q, want the first frame %q", got, sp.Frames[0])
	}
}
