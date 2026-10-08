package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// TestHelpDialogLosesNoTail pins B20: at every terminal width the help is
// wrapped to the width it is drawn at, so no row is wider than the text area
// (DrawTextClipped would cut it) and the wrapped rows still carry every word of
// helpLines, in order.
func TestHelpDialogLosesNoTail(t *testing.T) {
	want := strings.Fields(strings.Join(helpLines, " "))
	for _, sz := range [][2]int{{40, 20}, {62, 28}, {80, 24}, {100, 40}, {200, 50}} {
		a := newTestApp()
		a.screen = &fakeSizedScreen{w: sz[0], h: sz[1]}
		d := NewHelpDialog(a)
		d.Show()

		r := d.Rect()
		if r.X < 0 || r.Right() > sz[0] || r.Y < 0 || r.Y+r.H > sz[1] {
			t.Errorf("%dx%d: dialog %+v is off-screen", sz[0], sz[1], r)
		}
		// The last help row must sit above the Close button row, or the button
		// row overdraws it and the help's final line is never seen.
		if last := d.InnerRect().Y + d.dataH(); last >= d.ButtonRowY() {
			t.Errorf("%dx%d: last help row %d overlaps the button row %d", sz[0], sz[1], last, d.ButtonRowY())
		}
		textW := d.InnerRect().W - 2
		for i, l := range d.lines {
			if w := core.DisplayWidth(l); w > textW {
				t.Errorf("%dx%d: row %d is %d columns, text area %d: %q", sz[0], sz[1], i, w, textW, l)
			}
		}
		if got := strings.Fields(strings.Join(d.lines, " ")); !slices.Equal(got, want) {
			t.Errorf("%dx%d: wrapped help lost or reordered words", sz[0], sz[1])
		}
	}
}

// TestHelpDialogWideTerminalNeedsNoWrap: a terminal wider than the widest help
// line shows every line unwrapped, as written.
func TestHelpDialogWideTerminalNeedsNoWrap(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 200, h: 50}
	d := NewHelpDialog(a)
	if len(d.lines) != len(helpLines) {
		t.Fatalf("at 200 columns: %d rows, want %d (no wrapping)", len(d.lines), len(helpLines))
	}
}

// TestWrapHelpLinesHangsUnderDescription: a wrapped entry continues under its
// description column, not under the key or at the margin.
func TestWrapHelpLinesHangsUnderDescription(t *testing.T) {
	got, _ := wrapHelpLines([]string{"  Ctrl+X      one two three four five"}, 30)
	want := []string{
		"  Ctrl+X      one two three",
		"              four five",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestWrapHelpLinesKeepsStyleOfSourceLine: every row of a wrapped line is
// styled as its source line — a heading's continuation stays a heading even
// when it hangs indented, and an entry's continuation never becomes one.
func TestWrapHelpLinesKeepsStyleOfSourceLine(t *testing.T) {
	got, heads := wrapHelpLines([]string{
		"Heading  with a gap that wraps here",
		"  Ctrl+X      one two three four five",
	}, 30)
	want := []bool{true, true, false, false}
	if !slices.Equal(heads, want) {
		t.Errorf("heads %v for rows %q, want %v", heads, got, want)
	}
}
