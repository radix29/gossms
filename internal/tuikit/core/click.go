package core

import "time"

// DoubleClickInterval is how close two presses on the same target must fall to
// count as a double-click. Every tuikit control uses it, so a host has one
// double-click speed.
const DoubleClickInterval = 500 * time.Millisecond

// ClickTracker recognises a double-click: two fresh Button1 presses on the same
// target within DoubleClickInterval. tcell reports presses, not clicks, so the
// pairing is done here. K identifies the target (a row index, a text position,
// a column separator); a press on a different target starts a new pair.
//
// The caller passes only fresh presses: a resend while the button is held
// (tcell's all-motion tracking) must be filtered out first, or the resend
// pairs with its own press. Times come from the event (ev.When()), not the wall
// clock, so a UI goroutine that falls behind does not split a quick
// double-click, and a test can build two presses any distance apart.
//
// The zero value has no press to pair with.
type ClickTracker[K comparable] struct {
	key K
	at  time.Time
}

// Press reports whether a press on key at time at is the second half of a
// double-click, and records it otherwise. A recognised double-click is
// consumed: a third press starts a new pair rather than firing again.
func (c *ClickTracker[K]) Press(key K, at time.Time) bool {
	if key == c.key && !c.at.IsZero() && at.Sub(c.at) >= 0 && at.Sub(c.at) <= DoubleClickInterval {
		c.Reset()
		return true
	}
	c.Record(key, at)
	return false
}

// Record makes this press the one the next pairs with, without testing it as a
// double-click: a modified press (Shift-click) is never itself a double-click,
// but the plain press after it can still complete one.
func (c *ClickTracker[K]) Record(key K, at time.Time) { c.key, c.at = key, at }

// Reset forgets the previous press, so the next one cannot complete a pair: for
// a press that is never half of a double-click, or after the targets changed
// under the pointer (new items, a new data set).
func (c *ClickTracker[K]) Reset() { c.at = time.Time{} }
