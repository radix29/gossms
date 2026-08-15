package core

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// recScreen records every cell written to it. Size is the only other Screen
// method ClipScreen and the drawing helpers reach.
type recScreen struct {
	tcell.Screen
	w, h  int
	cells map[[2]int]rune
}

func newRecScreen(w, h int) *recScreen {
	return &recScreen{w: w, h: h, cells: map[[2]int]rune{}}
}

func (s *recScreen) Size() (int, int) { return s.w, s.h }

func (s *recScreen) SetContent(x, y int, primary rune, _ []rune, _ tcell.Style) {
	s.cells[[2]int{x, y}] = primary
}

func (s *recScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	head, rest := splitGrapheme(str, 1)
	if head == "" {
		return "", 0
	}
	s.SetContent(x, y, []rune(head)[0], nil, style)
	return rest, DisplayWidth(head)
}

func (s *recScreen) at(x, y int) rune { return s.cells[[2]int{x, y}] }

func TestClipScreenDropsWritesOutsideTheClip(t *testing.T) {
	rec := newRecScreen(20, 10)
	cs := NewClipScreen(rec)
	cs.SetClip(Rect{X: 5, Y: 2, W: 4, H: 3})

	DrawText(cs, 3, 3, tcell.StyleDefault, "abcdefgh")

	if got := rec.at(4, 3); got != 0 {
		t.Errorf("column 4 (left of the clip) = %q, want nothing drawn", got)
	}
	if got := rec.at(5, 3); got != 'c' {
		t.Errorf("column 5 = %q, want 'c'", got)
	}
	if got := rec.at(8, 3); got != 'f' {
		t.Errorf("column 8 (last clipped column) = %q, want 'f'", got)
	}
	if got := rec.at(9, 3); got != 0 {
		t.Errorf("column 9 (right of the clip) = %q, want nothing drawn", got)
	}
	// A row outside the clip is dropped entirely.
	DrawText(cs, 5, 6, tcell.StyleDefault, "xyz")
	if got := rec.at(5, 6); got != 0 {
		t.Errorf("row 6 (below the clip) = %q, want nothing drawn", got)
	}
}

func TestClipScreenResetClipWidensToTheWholeScreen(t *testing.T) {
	rec := newRecScreen(20, 10)
	cs := NewClipScreen(rec)
	cs.SetClip(Rect{X: 5, Y: 2, W: 4, H: 3})
	cs.ResetClip()

	DrawText(cs, 0, 0, tcell.StyleDefault, "ab")
	if rec.at(0, 0) != 'a' || rec.at(1, 0) != 'b' {
		t.Errorf("cells at (0,0)/(1,0) = %q/%q, want 'a'/'b' after ResetClip",
			rec.at(0, 0), rec.at(1, 0))
	}
}

// DimArea walks a row by the width Put reports, so a Put that lands outside
// the clip still has to report the grapheme it skipped — reporting 0 for
// every cell would leave the walk on the same column forever.
func TestClipScreenPutReportsWidthOutsideTheClip(t *testing.T) {
	rec := newRecScreen(20, 10)
	cs := NewClipScreen(rec)
	cs.SetClip(Rect{X: 10, Y: 0, W: 4, H: 4})

	rest, w := cs.Put(0, 0, "ab", tcell.StyleDefault)
	if rest != "b" || w != 1 {
		t.Errorf("Put outside the clip = (%q, %d), want (\"b\", 1)", rest, w)
	}
	if rec.at(0, 0) != 0 {
		t.Errorf("cell at (0,0) = %q, want nothing drawn", rec.at(0, 0))
	}
}

func TestClipScreenFillCoversOnlyTheClip(t *testing.T) {
	rec := newRecScreen(20, 10)
	cs := NewClipScreen(rec)
	cs.SetClip(Rect{X: 2, Y: 1, W: 2, H: 2})

	cs.Fill('#', tcell.StyleDefault)

	if rec.at(2, 1) != '#' || rec.at(3, 2) != '#' {
		t.Error("Fill should cover the clip rectangle")
	}
	if rec.at(1, 1) != 0 || rec.at(4, 1) != 0 || rec.at(2, 3) != 0 {
		t.Error("Fill should not write outside the clip rectangle")
	}
}
