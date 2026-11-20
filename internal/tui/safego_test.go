package tui

import (
	"strings"
	"testing"
)

// safego reports a panic and stops there, which is all that's needed for work
// that latched nothing. A background operation that dimmed a toolbar or set a
// "loading" flag before starting clears it in the callback it posts on
// completion — and a panic unwinds straight past that callback, so the latch
// survives for the object's lifetime with nothing on screen to say why. That
// is what safegoRepair's repair step exists to undo, and it must not cost the
// report: the status bar still has to name the operation that died.
//
// The mechanism's own rules (no repair after a clean return, the traceback
// label, fanOut's per-item recovery) are tested in tuikit's
// loop/runner_test.go; this pins gossms's wiring of it end to end.
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
