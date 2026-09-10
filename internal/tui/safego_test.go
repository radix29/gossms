package tui

import (
	"runtime/debug"
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

// The stack reportPanic logs is the only record of what a background
// operation was doing when it died, and it is headed by an anonymous func
// literal that names neither. Go 1.27 prints the goroutine's pprof labels in
// that header, so the operation names itself — but only if the label is still
// set while the panic unwinds, which is what this pins down.
func TestSafegoLabelsItsGoroutineWithTheOperation(t *testing.T) {
	a := newTestApp()
	stacks := make(chan string, 1)

	a.safego("loading the thing", func() {
		// Taken while the panic is unwinding, which is exactly where a
		// pprof.Do-style restore would already have dropped the label.
		defer func() { stacks <- string(debug.Stack()) }()
		panic("boom")
	})

	stack := <-stacks
	header, _, _ := strings.Cut(stack, "\n")
	if !strings.Contains(header, `op: "loading the thing"`) {
		t.Errorf("traceback header = %q, want it to name the operation", header)
	}
	drainUntil(t, a, func() bool { return strings.Contains(a.statusText, "loading the thing") },
		"the panic to reach the status bar")
}

// fanOut is shared by two callers that want different things from a panicking
// item: the backfill repairs its row (covered in detail_browser_backfill_test),
// the Log File Viewer passes no onPanic and relies on its seed. Either way one
// panic must cost that item only — every other item still runs, with more items
// than workers so a worker whose item panicked has to survive to take the next
// — and the status bar must name the operation. The panicking items are the
// first maxRowFetchConcurrency, which the queue hands out first, one to each
// worker.
func TestFanOutRecoversEachItemAndRunsTheRest(t *testing.T) {
	a := newTestApp()
	n := maxRowFetchConcurrency * 3
	done := make([]bool, n)

	a.fanOut(n, "reading an error log", func(i int) {
		// The first item each worker takes: without a per-item recovery every
		// worker dies on one, and nothing past them runs.
		if i < maxRowFetchConcurrency {
			panic("boom")
		}
		done[i] = true
	}, nil)

	for i, ok := range done {
		if want := i >= maxRowFetchConcurrency; ok != want {
			t.Errorf("item %d ran = %v, want %v", i, ok, want)
		}
	}
	drainUntil(t, a, func() bool { return strings.Contains(a.statusText, "reading an error log") },
		"the panic to reach the status bar")
}
