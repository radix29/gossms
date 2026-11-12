package core

import (
	"testing"
	"time"
)

func TestClickTracker(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	near := t0.Add(DoubleClickInterval / 2)
	far := t0.Add(2 * DoubleClickInterval)

	t.Run("same key within interval is a double", func(t *testing.T) {
		var c ClickTracker[int]
		if c.Press(3, t0) {
			t.Fatal("the first press was a double-click")
		}
		if !c.Press(3, near) {
			t.Fatal("a second quick press on the same key was not a double-click")
		}
	})
	t.Run("too slow", func(t *testing.T) {
		var c ClickTracker[int]
		c.Press(3, t0)
		if c.Press(3, far) {
			t.Fatal("two presses far apart paired")
		}
		// The slow press is the new first half.
		if !c.Press(3, far.Add(DoubleClickInterval/2)) {
			t.Fatal("the slow press was not recorded as the next pair's first half")
		}
	})
	t.Run("different key", func(t *testing.T) {
		var c ClickTracker[int]
		c.Press(3, t0)
		if c.Press(4, near) {
			t.Fatal("presses on two keys paired")
		}
	})
	t.Run("third press starts a new pair", func(t *testing.T) {
		var c ClickTracker[int]
		c.Press(3, t0)
		c.Press(3, near)
		if c.Press(3, near.Add(time.Millisecond)) {
			t.Fatal("a third press paired with a consumed double-click")
		}
	})
	t.Run("zero key pairs", func(t *testing.T) {
		// The zero value's key must not be mistaken for "no press": a first press
		// on key 0 must still need a second one.
		var c ClickTracker[int]
		if c.Press(0, t0) {
			t.Fatal("a first press on the zero key was a double-click")
		}
		if !c.Press(0, near) {
			t.Fatal("a double-click on the zero key was missed")
		}
	})
	t.Run("record and reset", func(t *testing.T) {
		var c ClickTracker[[2]int]
		c.Record([2]int{1, 2}, t0)
		if !c.Press([2]int{1, 2}, near) {
			t.Fatal("a press after Record did not pair")
		}
		c.Press([2]int{1, 2}, t0)
		c.Reset()
		if c.Press([2]int{1, 2}, near) {
			t.Fatal("a press after Reset paired")
		}
	})
	t.Run("time going backwards", func(t *testing.T) {
		var c ClickTracker[int]
		c.Press(3, near)
		if c.Press(3, t0) {
			t.Fatal("a press stamped before the previous one paired")
		}
	})
}
