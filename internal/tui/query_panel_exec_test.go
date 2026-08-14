package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// panickingPanel builds a query panel whose connection has a nil gosmo.Server,
// so the sc.Server.DB() inside the execute goroutine dereferences nil — a real
// panic on the execute path, raised where the driver's own
// makeGoLangTypeName panic would be, rather than an injected one.
func panickingPanel(t *testing.T) (*App, *QueryPanel) {
	t.Helper()
	a := newTestApp()
	p := NewQueryPanel(a, "Query 1")
	p.conn = &db.ServerConn{}
	return a, p
}

// A panic on the execute goroutine must not leave the panel executing
// forever: p.executing is the single-flight latch every Execute entry point
// reads, so a stuck one disables Execute for the panel's whole life.
func TestExecutePanicReleasesTheLatchAndAllowsTheNextRun(t *testing.T) {
	a, p := panickingPanel(t)

	p.startRun("SELECT 1", "")
	drainUntil(t, a, func() bool { return !p.executing }, "the execute latch to clear")

	if p.cancel != nil {
		t.Error("p.cancel survived the panic")
	}
	drainUntil(t, a, func() bool { return strings.Contains(a.statusText, "query execution") },
		"the panic to reach the status bar")

	// The point of the repair: the next run actually starts.
	p.startRun("SELECT 2", "")
	if !p.executing {
		t.Fatal("a second Execute was refused after the first panicked")
	}
	drainUntil(t, a, func() bool { return !p.executing }, "the second run to finish")
}

// The elapsed-time ticker and the query context are stopped by plain
// statements at the end of the goroutine body, which a panic skips. Left
// unfixed, every panicked run leaks a goroutine that wakes the event loop
// once a second for the rest of the process's life.
func TestExecutePanicStopsTheTickerAndCancelsTheContext(t *testing.T) {
	a, p := panickingPanel(t)

	p.startRun("SELECT 1", "")
	drainUntil(t, a, func() bool { return !p.executing }, "the execute latch to clear")

	select {
	case <-p.execDone:
		// closed: tickExecuting has been told to exit
	default:
		t.Fatal("the elapsed-time ticker was never stopped — its goroutine leaks")
	}
}

// The estimated-plan path shares p.executing/p.cancel with Execute, so it
// shares the wedge too.
func TestEstimatedPlanPanicReleasesTheLatch(t *testing.T) {
	a, p := panickingPanel(t)

	p.runEstimatedPlan("SELECT 1")
	drainUntil(t, a, func() bool { return !p.executing }, "the plan latch to clear")

	if p.cancel != nil {
		t.Error("p.cancel survived the panic")
	}
	select {
	case <-p.execDone:
	default:
		t.Fatal("the elapsed-time ticker was never stopped — its goroutine leaks")
	}
}
