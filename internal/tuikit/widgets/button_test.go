package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// mouse builds a mouse event at (x, y) with the given buttons held. tcell's
// all-motion tracking reports a continued hold as another Button1 event at the
// new position, which is exactly what the latch tests below replay.
func mouse(x, y int, btn tcell.ButtonMask) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, btn, tcell.ModNone)
}

// TestButtonClickFiresOnceWhileHeld pins the mouseDragging latch. tcell resends
// Buttons()==Button1 on every motion event for as long as the button is down,
// so without the latch a click that so much as twitches before release fires
// OnClick once per resent event. The count is the assertion — a test that only
// checked "OnClick ran" would pass on the very bug the latch exists to stop.
func TestButtonClickFiresOnceWhileHeld(t *testing.T) {
	clicks := 0
	b := NewButton("OK", func() { clicks++ })
	b.SetBounds(10, 5)

	if !b.HandleMouse(mouse(11, 5, tcell.Button1)) {
		t.Fatal("HandleMouse(press on the button) = false, want true")
	}
	// Held, drifting across the button — every one of these is a resend.
	for _, x := range []int{11, 12, 13, 12, 11} {
		b.HandleMouse(mouse(x, 5, tcell.Button1))
	}
	if clicks != 1 {
		t.Errorf("OnClick fired %d times during one held click, want 1", clicks)
	}
}

// TestButtonReleaseRearmsTheLatch pins the other half: a latch that were never
// cleared would swallow every later click on the button.
func TestButtonReleaseRearmsTheLatch(t *testing.T) {
	clicks := 0
	b := NewButton("OK", func() { clicks++ })
	b.SetBounds(10, 5)

	b.HandleMouse(mouse(11, 5, tcell.Button1))
	b.HandleMouse(mouse(11, 5, tcell.ButtonNone))
	b.HandleMouse(mouse(11, 5, tcell.Button1))
	if clicks != 2 {
		t.Errorf("OnClick fired %d times across two press/release cycles, want 2", clicks)
	}
}

// TestButtonReleaseIsNotConsumed pins that a release returns false. App's
// routeRelease broadcasts every release to every latch-bearing widget so their
// latches reset (see internal/tui/app_events.go); a widget that claimed the
// release would stop that broadcast at itself.
func TestButtonReleaseIsNotConsumed(t *testing.T) {
	b := NewButton("OK", func() {})
	b.SetBounds(10, 5)
	b.HandleMouse(mouse(11, 5, tcell.Button1))
	if b.HandleMouse(mouse(11, 5, tcell.ButtonNone)) {
		t.Error("HandleMouse(release) = true, want false so the release keeps propagating")
	}
}

// TestButtonIgnoresClicksOutsideItsBounds covers the hit-test edges, including
// that the column exactly one past the rendered width is outside — Width() is
// "[ label ]", so an off-by-one here would let a click on the neighbouring
// widget press this one too.
func TestButtonIgnoresClicksOutsideItsBounds(t *testing.T) {
	b := NewButton("OK", func() {})
	b.SetBounds(10, 5)
	w := b.Width()

	cases := []struct {
		name string
		x, y int
	}{
		{"one column left", 9, 5},
		{"one column past the right edge", 10 + w, 5},
		{"row above", 11, 4},
		{"row below", 11, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clicks := 0
			b := NewButton("OK", func() { clicks++ })
			b.SetBounds(10, 5)
			if b.HandleMouse(mouse(c.x, c.y, tcell.Button1)) {
				t.Errorf("HandleMouse at (%d,%d) = true, want false", c.x, c.y)
			}
			if clicks != 0 {
				t.Errorf("OnClick fired %d times for a click outside the button, want 0", clicks)
			}
		})
	}

	// The last cell inside the button must still hit.
	clicks := 0
	b2 := NewButton("OK", func() { clicks++ })
	b2.SetBounds(10, 5)
	if !b2.HandleMouse(mouse(10+w-1, 5, tcell.Button1)) || clicks != 1 {
		t.Errorf("click on the button's last column: handled=%v clicks=%d, want true/1",
			clicks == 1, clicks)
	}
}

// TestButtonRefusesKeysItDoesNotHandle pins the keyboard-conventions rule: a
// widget that returns true for a key it doesn't act on traps focus, because the
// host stops looking for anyone else to handle it.
func TestButtonRefusesKeysItDoesNotHandle(t *testing.T) {
	b := NewButton("OK", func() { t.Fatal("OnClick fired for a key the button should refuse") })
	b.Focus(true)
	for _, k := range []tcell.Key{tcell.KeyTab, tcell.KeyEscape, tcell.KeyUp, tcell.KeyLeft} {
		if b.HandleKey(key(k, tcell.ModNone)) {
			t.Errorf("HandleKey(%v) = true, want false", k)
		}
	}
}

// TestButtonIgnoresKeysWhileUnfocused pins that an unfocused button doesn't
// steal Enter from whatever actually has focus.
func TestButtonIgnoresKeysWhileUnfocused(t *testing.T) {
	clicks := 0
	b := NewButton("OK", func() { clicks++ })
	b.Focus(false)
	if b.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) {
		t.Error("HandleKey(Enter) while unfocused = true, want false")
	}
	if clicks != 0 {
		t.Errorf("OnClick fired %d times while unfocused, want 0", clicks)
	}
}

// TestButtonEnterFiresOnClick is the positive case for the two tests above.
func TestButtonEnterFiresOnClick(t *testing.T) {
	clicks := 0
	b := NewButton("OK", func() { clicks++ })
	b.Focus(true)
	if !b.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) {
		t.Fatal("HandleKey(Enter) while focused = false, want true")
	}
	if clicks != 1 {
		t.Errorf("OnClick fired %d times, want 1", clicks)
	}
}

// TestButtonWithNilOnClickDoesNotPanic covers the nil guards on both input
// paths — a Button built without a handler is a legitimate placeholder.
func TestButtonWithNilOnClickDoesNotPanic(t *testing.T) {
	b := NewButton("OK", nil)
	b.SetBounds(0, 0)
	b.Focus(true)
	if !b.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) {
		t.Error("HandleKey(Enter) = false, want true even with a nil OnClick")
	}
	if !b.HandleMouse(mouse(1, 0, tcell.Button1)) {
		t.Error("HandleMouse(press) = false, want true even with a nil OnClick")
	}
}

// TestButtonWidthCountsDisplayColumns pins that Width measures terminal
// columns, not bytes — a multi-byte label sized with len() would make the
// hit-test region far wider than what Draw actually paints.
func TestButtonWidthCountsDisplayColumns(t *testing.T) {
	if got, want := NewButton("OK", nil).Width(), 6; got != want {
		t.Errorf(`NewButton("OK").Width() = %d, want %d for "[ OK ]"`, got, want)
	}
	if got, want := NewButton("é", nil).Width(), 5; got != want {
		t.Errorf(`NewButton("é").Width() = %d, want %d — one column, not two bytes`, got, want)
	}
}
