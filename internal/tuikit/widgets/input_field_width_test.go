package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// InputField: wide (double-width) characters occupy two terminal columns
// ---------------------------------------------------------------------------
//
// InputField held its text as []rune and treated each one as a single cell:
// the caret, the horizontal scroll offset, and the click-to-position math
// were all rune counts, while Draw handed the string to the terminal, which
// laid it out by display width. A CJK or emoji rune in a connection name or a
// filter box put the caret one column left of where it rendered. Each test
// below fails against that model.

// fieldScreen captures the runes a Draw painted.
type fieldScreen struct {
	tcell.Screen
	w, h  int
	runes map[[2]int]rune
}

func newFieldScreen(w, h int) *fieldScreen {
	return &fieldScreen{w: w, h: h, runes: map[[2]int]rune{}}
}
func (s *fieldScreen) Size() (int, int) { return s.w, s.h }
func (s *fieldScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
	s.runes[[2]int{x, y}] = primary
}
func (s *fieldScreen) ShowCursor(x, y int) {}

// TestInputFieldDrawsWideRunesInTwoColumns: with a rune-per-column model the
// "ok" after two ideographs starts at the box's column 2, over the second
// half of the second ideograph.
func TestInputFieldDrawsWideRunesInTwoColumns(t *testing.T) {
	f := NewInputField("", 10, false)
	f.SetBounds(0, 0)
	f.SetValue("世界ok")

	s := newFieldScreen(20, 2)
	f.Draw(s)

	// The box's first text cell is inputX()+1 — column 1 here, past the '['.
	if got := s.runes[[2]int{1, 0}]; got != '世' {
		t.Errorf("first text cell = %q, want 世", got)
	}
	if got := s.runes[[2]int{5, 0}]; got != 'o' {
		t.Errorf("cell 5 = %q, want 'o' — two ideographs occupy the four cells before it", got)
	}
}

// TestInputFieldClickMapsColumnsBackToRunes: clicking the cell where a rune
// renders must put the caret on that rune. Reading the x offset as a rune
// index put it two runes early once two wide runes preceded the click.
func TestInputFieldClickMapsColumnsBackToRunes(t *testing.T) {
	f := NewInputField("", 10, false)
	f.SetBounds(0, 0)
	f.SetValue("世界ok")
	f.Focus(true)

	// Column 5 is where 'o' (rune index 2) renders.
	f.HandleMouse(tcell.NewEventMouse(5, 0, tcell.Button1, tcell.ModNone))
	if f.cursor != 2 {
		t.Errorf("click on 'o' put the caret at rune %d, want 2", f.cursor)
	}
	f.HandleMouse(tcell.NewEventMouse(5, 0, tcell.ButtonNone, tcell.ModNone))

	// Either half of a wide rune snaps to its start.
	f.HandleMouse(tcell.NewEventMouse(4, 0, tcell.Button1, tcell.ModNone))
	if f.cursor != 1 {
		t.Errorf("click on the right half of 界 gave rune %d, want 1", f.cursor)
	}
}

// TestInputFieldScrollsInColumns: a box narrower than the text has to scroll
// far enough in *columns* to bring the caret into view. Counting runes let
// the caret sit off the right edge of a CJK value — the field looked frozen
// while still accepting input.
func TestInputFieldScrollsInColumns(t *testing.T) {
	f := NewInputField("", 6, false)
	f.SetBounds(0, 0)
	f.SetValue("世界世界") // 4 runes, 8 columns, in a 6-column box

	col := core.ColumnOfRune([]rune("世界世界"), f.cursor)
	if col-f.scroll >= 6 || col-f.scroll < 0 {
		t.Errorf("caret at column %d with scroll %d is outside the 6-column box", col, f.scroll)
	}
	if f.scroll != 3 {
		t.Errorf("scroll = %d, want 3 columns (8 columns of text less a 6-wide box, +1 for the caret)", f.scroll)
	}
}

// TestInputFieldClipsAWideRuneAtTheRightEdge: tcell owns both cells of a
// double-width character, so a rune that only half fits is blanked rather
// than drawn — half a glyph makes the terminal paint the whole character
// over the neighbouring cell.
func TestInputFieldClipsAWideRuneAtTheRightEdge(t *testing.T) {
	f := NewInputField("", 3, false)
	f.SetBounds(0, 0)
	f.SetValue("ab")        // caret at the end, no scrolling
	f.value = []rune("ab世") // 世 needs box columns 2-3; only 0-2 exist
	f.cursor = 0
	f.scroll = 0

	s := newFieldScreen(20, 2)
	f.Draw(s)

	if got := s.runes[[2]int{3, 0}]; got != ' ' {
		t.Errorf("last box cell = %q, want a blank — a wide rune must not be drawn half in", got)
	}
}

// TestInputFieldPasswordMaskStaysOneColumnPerRune: the mask is one '*' per
// rune, so a masked wide rune narrows to a single column. The caret and the
// click math both measure the masked form, or they would disagree with what
// is on screen.
func TestInputFieldPasswordMaskStaysOneColumnPerRune(t *testing.T) {
	f := NewInputField("", 10, true)
	f.SetBounds(0, 0)
	f.SetValue("世界ok")
	f.Focus(true)

	s := newFieldScreen(20, 2)
	f.Draw(s)
	for i := range 4 {
		if got := s.runes[[2]int{1 + i, 0}]; got != '*' {
			t.Errorf("masked cell %d = %q, want '*'", i, got)
		}
	}
	if got := s.runes[[2]int{5, 0}]; got != ' ' {
		t.Errorf("cell past the masked value = %q, want a blank", got)
	}

	// Clicking the third mask character targets rune 2, not rune 4.
	f.HandleMouse(tcell.NewEventMouse(3, 0, tcell.Button1, tcell.ModNone))
	if f.cursor != 2 {
		t.Errorf("click on the third mask cell gave rune %d, want 2", f.cursor)
	}
}
