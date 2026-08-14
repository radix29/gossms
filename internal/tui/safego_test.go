package tui

import (
	"strings"
	"testing"
	"time"
)

// safego reports a panic and stops there, which is all that's needed for work
// that latched nothing. A background operation that dimmed a toolbar or set a
// "loading" flag before starting clears it in the callback it posts on
// completion — and a panic unwinds straight past that callback, so the latch
// survives for the object's lifetime with nothing on screen to say why. That
// is what safegoRepair's repair step exists to undo, and it must not cost the
// report: the status bar still has to name the operation that died.
func TestSafegoRepairReleasesTheLatchAndStillReportsThePanic(t *testing.T) {
	a := newTestApp()
	busy := true

	a.safegoRepair("reading the thing", func() { busy = false }, func() {
		panic("boom")
	})

	drainUntil(t, a, func() bool { return !busy }, "the repair step to run")
	drainUntil(t, a, func() bool { return strings.Contains(a.statusText, "reading the thing") },
		"the panic to reach the status bar")
}

// The repair belongs to the panic and nothing else. An operation that returns
// normally clears its own latch in its own callback, and running the repair
// as well would clear one a newer operation had since taken for itself.
func TestSafegoRepairDoesNotRunWhenNothingPanicked(t *testing.T) {
	a := newTestApp()
	repaired := false
	done := make(chan struct{})

	a.safegoRepair("reading the thing", func() { repaired = true }, func() { close(done) })

	<-done
	// Give a repair that should not have been queued time to be queued, so
	// this fails rather than passing on timing.
	time.Sleep(50 * time.Millisecond)
	a.drainPending()

	if repaired {
		t.Error("the repair step ran after a clean return")
	}
	if a.statusText != "" {
		t.Errorf("statusText = %q, want empty — nothing failed", a.statusText)
	}
}
