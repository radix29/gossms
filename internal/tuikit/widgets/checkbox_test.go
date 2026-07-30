package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestCheckBoxTogglesOnceWhileHeld pins the mouseDragging latch, which matters
// more here than on a Button: a toggle fired once per resent Button1 motion
// event doesn't just repeat an action, it lands on whichever state the number
// of resends happened to be odd or even about. Asserting the resulting Checked()
// value, not just the call count, is what makes that visible.
func TestCheckBoxTogglesOnceWhileHeld(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.SetBounds(4, 2)

	if !c.HandleMouse(mouse(5, 2, tcell.Button1)) {
		t.Fatal("HandleMouse(press on the checkbox) = false, want true")
	}
	// An odd number of resends, so without the latch the press plus the
	// resends make an even number of toggles and Checked() lands back on
	// false — the assertion below would then fail, which is the point. An
	// even count here would pass with or without the latch.
	for _, x := range []int{5, 6, 7} {
		c.HandleMouse(mouse(x, 2, tcell.Button1))
	}
	if !c.Checked() {
		t.Error("Checked() = false after one held click, want true — the latch let a resend toggle it back")
	}
}

// TestCheckBoxReleaseRearmsTheLatch pins that a second physical click toggles
// back, i.e. the latch clears on release rather than sticking.
func TestCheckBoxReleaseRearmsTheLatch(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.SetBounds(4, 2)

	c.HandleMouse(mouse(5, 2, tcell.Button1))
	c.HandleMouse(mouse(5, 2, tcell.ButtonNone))
	if !c.Checked() {
		t.Fatal("Checked() = false after the first click, want true")
	}
	c.HandleMouse(mouse(5, 2, tcell.Button1))
	if c.Checked() {
		t.Error("Checked() = true after a second click, want false")
	}
}

// TestCheckBoxReleaseIsNotConsumed — see the Button test of the same name for
// why a release must keep propagating.
func TestCheckBoxReleaseIsNotConsumed(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.SetBounds(4, 2)
	c.HandleMouse(mouse(5, 2, tcell.Button1))
	if c.HandleMouse(mouse(5, 2, tcell.ButtonNone)) {
		t.Error("HandleMouse(release) = true, want false so the release keeps propagating")
	}
}

// TestCheckBoxIgnoresClicksOutsideItsBounds covers the hit-test edges. The
// clickable region is the box glyph plus the label ("[x] label"), so the label
// text is live too — a region sized to the glyph alone would make most of the
// widget dead to the mouse.
func TestCheckBoxIgnoresClicksOutsideItsBounds(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.SetBounds(4, 2)
	w := c.Width()

	for _, p := range []struct {
		name string
		x, y int
	}{
		{"one column left", 3, 2},
		{"one column past the right edge", 4 + w, 2},
		{"row above", 5, 1},
		{"row below", 5, 3},
	} {
		t.Run(p.name, func(t *testing.T) {
			c := NewCheckBox("Encrypt")
			c.SetBounds(4, 2)
			if c.HandleMouse(mouse(p.x, p.y, tcell.Button1)) {
				t.Errorf("HandleMouse at (%d,%d) = true, want false", p.x, p.y)
			}
			if c.Checked() {
				t.Error("Checked() = true after a click outside the checkbox, want false")
			}
		})
	}

	// The far end of the label is still inside.
	c2 := NewCheckBox("Encrypt")
	c2.SetBounds(4, 2)
	if !c2.HandleMouse(mouse(4+w-1, 2, tcell.Button1)) || !c2.Checked() {
		t.Error("a click on the label's last column did not toggle the checkbox")
	}
}

// TestCheckBoxTogglesOnEnterAndSpace pins both accepted keys.
func TestCheckBoxTogglesOnEnterAndSpace(t *testing.T) {
	for _, k := range []struct {
		name string
		ev   *tcell.EventKey
	}{
		{"Enter", key(tcell.KeyEnter, tcell.ModNone)},
		{"Space", tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone)},
	} {
		t.Run(k.name, func(t *testing.T) {
			c := NewCheckBox("Encrypt")
			c.Focus(true)
			if !c.HandleKey(k.ev) {
				t.Fatalf("HandleKey(%s) = false, want true", k.name)
			}
			if !c.Checked() {
				t.Errorf("Checked() = false after %s, want true", k.name)
			}
		})
	}
}

// TestCheckBoxRefusesKeysItDoesNotHandle pins the keyboard-conventions rule:
// consuming a key it doesn't act on would trap focus on the checkbox.
func TestCheckBoxRefusesKeysItDoesNotHandle(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.Focus(true)
	for _, k := range []tcell.Key{tcell.KeyTab, tcell.KeyEscape, tcell.KeyUp, tcell.KeyDown} {
		if c.HandleKey(key(k, tcell.ModNone)) {
			t.Errorf("HandleKey(%v) = true, want false", k)
		}
	}
	if c.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone)) {
		t.Error("HandleKey('x') = true, want false — only Space toggles")
	}
	if c.Checked() {
		t.Error("Checked() = true after keys the checkbox should refuse, want false")
	}
}

// TestCheckBoxIgnoresKeysWhileUnfocused pins that an unfocused checkbox doesn't
// swallow Space from whatever actually has focus.
func TestCheckBoxIgnoresKeysWhileUnfocused(t *testing.T) {
	c := NewCheckBox("Encrypt")
	c.Focus(false)
	if c.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone)) {
		t.Error("HandleKey(Space) while unfocused = true, want false")
	}
	if c.Checked() {
		t.Error("Checked() = true after a key while unfocused, want false")
	}
}

// TestCheckBoxSetCheckedRoundTrips pins the programmatic setter used when a
// page loads a value in from the server.
func TestCheckBoxSetCheckedRoundTrips(t *testing.T) {
	c := NewCheckBox("Encrypt")
	if c.Checked() {
		t.Error("a new CheckBox is checked, want unchecked")
	}
	c.SetChecked(true)
	if !c.Checked() {
		t.Error("Checked() = false after SetChecked(true)")
	}
	c.SetChecked(false)
	if c.Checked() {
		t.Error("Checked() = true after SetChecked(false)")
	}
}

// TestCheckBoxWidthCountsDisplayColumns — see the Button test of the same name.
func TestCheckBoxWidthCountsDisplayColumns(t *testing.T) {
	if got, want := NewCheckBox("Ok").Width(), 6; got != want {
		t.Errorf(`NewCheckBox("Ok").Width() = %d, want %d for "[ ] Ok"`, got, want)
	}
	if got, want := NewCheckBox("é").Width(), 5; got != want {
		t.Errorf(`NewCheckBox("é").Width() = %d, want %d — one column, not two bytes`, got, want)
	}
}
