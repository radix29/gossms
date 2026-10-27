package core

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func TestGestureArmsOnlyOnButton1(t *testing.T) {
	var g Gesture[int]
	for _, b := range []tcell.ButtonMask{tcell.Button2, tcell.WheelDown, tcell.ButtonNone} {
		g.Arm(tcell.NewEventMouse(0, 0, b, tcell.ModNone), 3)
		if _, held := g.Owner(); held {
			t.Fatalf("Arm on buttons %v claimed the gesture", b)
		}
	}
	g.Arm(tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModNone), 3)
	if z, held := g.Owner(); !held || z != 3 {
		t.Fatalf("Owner() = %d, %v after a Button1 Arm, want 3, true", z, held)
	}
	g.Release()
	if _, held := g.Owner(); held {
		t.Fatal("Release left the gesture held")
	}
}

// Route is the mid-gesture branch every host shares: a held Button1 reaches
// the owner, and a wheel tick is swallowed rather than handed back to
// positional routing.
func TestGestureRouteDeliversButton1AndSwallowsTheWheel(t *testing.T) {
	var g Gesture[string]
	var got []string
	route := func(_ *tcell.EventMouse, z string) { got = append(got, z) }

	motion := tcell.NewEventMouse(9, 9, tcell.Button1, tcell.ModNone)
	if g.Route(motion, route) {
		t.Fatal("Route consumed an event with no gesture held")
	}
	g.Arm(motion, "grid")
	if !g.Route(motion, route) {
		t.Fatal("Route did not consume a held Button1")
	}
	if !g.Route(tcell.NewEventMouse(9, 9, tcell.WheelUp, tcell.ModNone), route) {
		t.Fatal("Route did not swallow a mid-gesture wheel tick")
	}
	if len(got) != 1 || got[0] != "grid" {
		t.Fatalf("route calls = %v, want exactly [grid]", got)
	}
	g.Release()
	if g.Route(motion, route) {
		t.Fatal("Route consumed an event after Release")
	}
}
