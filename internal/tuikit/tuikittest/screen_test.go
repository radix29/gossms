package tuikittest

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func TestFindReportsScreenColumnsPastWideGlyphs(t *testing.T) {
	s := NewScreen(12, 3)
	core.DrawText(s, 1, 2, tcell.StyleDefault, "日本 OK")
	x, y, ok := s.Find("OK")
	if !ok || x != 6 || y != 2 {
		t.Errorf("Find(OK) = %d,%d,%v, want 6,2,true", x, y, ok)
	}
	if got := s.RowOf("本"); got != 2 {
		t.Errorf("RowOf(本) = %d, want 2", got)
	}
	if got := s.RowOf("nope"); got != -1 {
		t.Errorf("RowOf(nope) = %d, want -1", got)
	}
	if got, want := s.Snapshot(), "\n\n 日本 OK"; got != want {
		t.Errorf("Snapshot() = %q, want %q", got, want)
	}
}

func TestEventBuildersCombineModifiers(t *testing.T) {
	if ev := Key(tcell.KeyLeft, tcell.ModCtrl, tcell.ModShift); ev.Modifiers() != tcell.ModCtrl|tcell.ModShift {
		t.Errorf("Key modifiers = %v", ev.Modifiers())
	}
	if ev := Rune('a'); ev.Key() != tcell.KeyRune || ev.Str() != "a" || ev.Modifiers() != 0 {
		t.Errorf("Rune('a') = %v %q %v", ev.Key(), ev.Str(), ev.Modifiers())
	}
	if ev := Release(3, 4); ev.Buttons() != tcell.ButtonNone {
		t.Errorf("Release buttons = %v", ev.Buttons())
	}
	if x, y := Press(3, 4).Position(); x != 3 || y != 4 {
		t.Errorf("Press position = %d,%d", x, y)
	}
}
