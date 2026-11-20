package tui

import (
	"fmt"
	"log"

	"github.com/radix29/tuikit/loop"
)

// The mechanism — the traceback label, the recover, the repair-before-report
// order and the bounded per-item-recovering pool — is tuikit's loop.Runner
// (its doc and loop/runner_test.go hold the rules). What stays here is the
// reporting: where the stack goes and what the status bar says.
//
// Every background operation in this package goes through safego/safegoRepair
// (or fanOut; fanOut's doc names the one bare goroutine). The UI goroutine
// cannot recover a panic on a background one, so it would take the process
// down: Run's `defer screen.Fini()` never runs, the terminal is left in raw mode
// on the alternate screen, and the user's unsaved query text and the trace are
// gone.
//
// Not theoretical: go-mssqldb's makeGoLangTypeName panics on an unknown column
// type ID, and query.scanResultSet calls DatabaseTypeName() on every column, so a
// column type newer than the pinned driver reaches it.

// runner is the loop.Runner every method below delegates to. Built per call,
// not cached on App: it holds no state, so a fresh one costs an allocation and
// needs no lazy initialisation for safego calls from background goroutines to
// race on, and a bare &App{} in a test has one.
func (a *App) runner() *loop.Runner {
	return &loop.Runner{Post: a.postAndWake, Report: a.reportPanic, Workers: maxRowFetchConcurrency}
}

// safego runs fn on a new goroutine, turning a panic into a status-bar message and
// a log entry instead of a process-wide crash.
func (a *App) safego(what string, fn func()) { a.runner().Go(what, fn) }

// safegoRepair is safego for a background operation that latched UI state before
// starting: a busy flag, a "loading" placeholder, a dimmed toolbar. repair runs on
// the UI goroutine if, and only if, fn panicked; nil is safego.
//
// The latch is otherwise cleared by the callback fn posts on finishing, and a
// panic unwinds past it, so the flag stays set for the object's lifetime: the Log
// File Viewer's toolbar stays dimmed until the panel closes, an Activity Monitor
// tab sits at "Running..." forever, IntelliSense reports a catalog loading that
// nothing is loading. safego reports the panic either way; without a repair step
// the UI loses its way out.
//
// repair is queued before the panic is reported, so the status bar's last word is
// the panic rather than whatever repair sets. Same ordering and reason as
// backfillRow's recovery (detail_browser_backfill.go).
func (a *App) safegoRepair(what string, repair func(), fn func()) {
	a.runner().GoRepair(what, repair, fn)
}

// fanOut runs work(i) for every i in [0, n) across a bounded pool of
// maxRowFetchConcurrency goroutines and returns once every call has finished
// (loop.Runner.FanOut). It is the one worker pool in this package (the Detail
// Browser's per-row backfill and the Log File Viewer's per-file reads both use
// it) and one of two places that spawn a goroutine outside safego/safegoRepair;
// it labels and recovers its workers itself. The other is App.FlushSaves' waiter
// on savesInFlight, which runs after the event loop is gone and does nothing
// that can panic.
//
// Each call is recovered on its own. onPanic (optional) runs on the worker for
// the panicked item *before* fanOut can return, because a caller acting on the
// results at once needs the repair already queued. The Detail Browser caches its
// rows then, and a row whose fetch died would otherwise stay cached with its
// "..." placeholder, since reselecting the node is a cache hit that never
// refetches.
func (a *App) fanOut(n int, what string, work func(i int), onPanic func(i int)) {
	a.runner().FanOut(n, what, work, onPanic)
}

// reportPanic is the loop.Runner Report callback: it logs r with the stack the
// panicking goroutine captured, and puts it on the status bar.
func (a *App) reportPanic(what string, r any, stack []byte) {
	log.Printf("panic in %s: %v\n%s", what, r, stack)
	msg := fmt.Sprintf("Internal error in %s: %v — see the log for details", what, r)
	a.postAndWake(func() { a.setStatus(msg) })
}
