package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestInputFieldDrawsClustersWhole is K1 for InputField: a cluster is drawn
// in one cell write at the width tcell gives it, so the character after it is
// not drawn over its second cell, and a combining mark is drawn with its base
// rather than dropped.
func TestInputFieldDrawsClustersWhole(t *testing.T) {
	for _, c := range []struct {
		g     string
		width int
	}{
		{"❤️", 2},
		{"🇺🇸", 2},
		{"👨‍👩‍👧", 2},
		{"é", 1},
	} {
		s := dropTestScreen(t, 40, 3)
		f := newTestInputField("x" + c.g + "y")
		f.SetBounds(0, 0)
		f.Draw(s)
		x0 := f.InputX() + 1
		if str, _, _ := s.Get(x0+1, 0); str != c.g {
			t.Errorf("%q: cell after x = %q, want the whole cluster", c.g, str)
		}
		if str, _, _ := s.Get(x0+1+c.width, 0); str != "y" {
			t.Errorf("%q: cell %d = %q, want \"y\"", c.g, 1+c.width, str)
		}
	}
}

// TestInputFieldEditsClustersWhole: Left, Right, Backspace and Delete step
// over a cluster as one character.
func TestInputFieldEditsClustersWhole(t *testing.T) {
	g := "👍🏽"
	f := newTestInputField("x" + g + "y") // cursor at the end, 4
	f.HandleKey(key(tcell.KeyLeft, tcell.ModNone))
	f.HandleKey(key(tcell.KeyLeft, tcell.ModNone))
	if f.cursor != 1 {
		t.Fatalf("two Lefts from the end: cursor %d, want 1", f.cursor)
	}
	f.HandleKey(key(tcell.KeyRight, tcell.ModNone))
	if f.cursor != 3 {
		t.Fatalf("Right over the cluster: cursor %d, want 3", f.cursor)
	}
	f.HandleKey(key(tcell.KeyBackspace2, tcell.ModNone))
	if f.Value() != "xy" || f.cursor != 1 {
		t.Fatalf("Backspace: %q cursor %d, want \"xy\" 1", f.Value(), f.cursor)
	}
	f.SetValue("x" + g + "y")
	f.HandleKey(key(tcell.KeyHome, tcell.ModNone))
	f.HandleKey(key(tcell.KeyRight, tcell.ModNone))
	f.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
	if f.Value() != "xy" {
		t.Fatalf("Delete: %q, want \"xy\"", f.Value())
	}
}
