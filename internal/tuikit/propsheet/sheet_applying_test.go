package propsheet

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// cellScreen is a screen fake recording the rune and style of every cell
// drawn, with just enough surface for PropertySheet.Draw: Size, the Get/Put
// pair DimArea walks with, and SetContent.
type cellScreen struct {
	tcell.Screen
	w, h  int
	runes map[[2]int]rune
	style map[[2]int]tcell.Style
}

func newCellScreen(w, h int) *cellScreen {
	return &cellScreen{w: w, h: h, runes: map[[2]int]rune{}, style: map[[2]int]tcell.Style{}}
}

func (s *cellScreen) Size() (int, int) { return s.w, s.h }

func (s *cellScreen) SetContent(x, y int, primary rune, _ []rune, st tcell.Style) {
	s.runes[[2]int{x, y}] = primary
	s.style[[2]int{x, y}] = st
}

func (s *cellScreen) Get(int, int) (string, tcell.Style, int) {
	return " ", tcell.StyleDefault, 1
}

func (s *cellScreen) Put(x, y int, str string, st tcell.Style) (string, int) {
	if str == "" {
		return "", 0
	}
	r := []rune(str)
	s.SetContent(x, y, r[0], nil, st)
	return string(r[1:]), 1
}

func (s *cellScreen) row(y int) string {
	var b strings.Builder
	for x := range s.w {
		r := s.runes[[2]int{x, y}]
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// While an Apply/OK is in flight the button row greys every button — the sheet
// refuses them all, see TestSheetApplyingBlocksButtonsUntilCleared — and a
// spinner with its label runs at the row's left end. Clearing the flag brings
// the live buttons back and takes the spinner away.
func TestSheetApplyingGreysButtonsAndShowsSpinner(t *testing.T) {
	scr := newCellScreen(100, 32)
	p := NewPropertySheet(scr, "Test Properties")
	p.SetPages([]string{"General"})
	p.Show()

	fg := func(x, y int) tcell.Style { return scr.style[[2]int{x, y}] }
	disabledFg := theme.Active().TextDisabled

	p.StartApplying("Scripting...")
	p.Draw(scr)
	y := p.ButtonRowY()
	row := scr.row(y)
	if !strings.Contains(row, "Scripting...") {
		t.Fatalf("button row while applying = %q, want the spinner label", row)
	}
	for _, label := range sheetButtonLabels {
		x := strings.Index(row, "[ "+label+" ]")
		if x < 0 {
			t.Fatalf("button row %q has no %q", row, label)
		}
		if got := fg(x, y).GetForeground(); got != disabledFg {
			t.Errorf("%q foreground while applying = %v, want disabled %v", label, got, disabledFg)
		}
	}

	p.SetApplying(false)
	scr = newCellScreen(100, 32)
	p.Draw(scr)
	row = scr.row(y)
	if strings.Contains(row, "Scripting...") {
		t.Errorf("button row after SetApplying(false) = %q, spinner label still drawn", row)
	}
	x := strings.Index(row, "[ OK ]")
	if got := fg(x, y).GetForeground(); got == disabledFg {
		t.Errorf("OK still drawn disabled after SetApplying(false)")
	}
}

// With OnCancelApply set, Cancel is the one button drawn live while applying
// — it stops the run — until it has been pressed, when it greys too and the
// label says the run is winding down.
func TestSheetApplyingLeavesCancelLiveWhenCancellable(t *testing.T) {
	scr := newCellScreen(100, 32)
	p := NewPropertySheet(scr, "Test Properties")
	p.SetPages([]string{"General"})
	p.Show()
	p.OnCancelApply = func() {}
	disabledFg := theme.Active().TextDisabled

	greyed := func() map[string]bool {
		scr = newCellScreen(100, 32)
		p.Draw(scr)
		y := p.ButtonRowY()
		row := scr.row(y)
		out := map[string]bool{}
		for _, label := range sheetButtonLabels {
			x := strings.Index(row, "[ "+label+" ]")
			if x < 0 {
				t.Fatalf("button row %q has no %q", row, label)
			}
			out[label] = scr.style[[2]int{x, y}].GetForeground() == disabledFg
		}
		return out
	}

	p.StartApplying("Applying...")
	for label, grey := range greyed() {
		if want := label != "Cancel"; grey != want {
			t.Errorf("while applying: %q greyed = %v, want %v", label, grey, want)
		}
	}

	p.cancel()
	for label, grey := range greyed() {
		if !grey {
			t.Errorf("after Cancel: %q drawn live, want every button greyed", label)
		}
	}
	if row := scr.row(p.ButtonRowY()); !strings.Contains(row, "Cancelling...") {
		t.Errorf("button row after Cancel = %q, want Cancelling...", row)
	}
}
