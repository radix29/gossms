package tui

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"runtime/pprof"
	"sync"
)

// safego runs fn on a new goroutine, turning a panic into a status-bar message
// and a log entry instead of a process-wide crash.
//
// Every background operation in this package goes through it. A panic on a
// background goroutine is not recoverable by the UI goroutine, so it would take
// the whole process down, Run's `defer screen.Fini()` never runs, and the
// terminal is left in raw mode on the alternate screen with the user's unsaved
// query text gone (the trace disappears with the screen).
//
// Not theoretical: go-mssqldb's makeGoLangTypeName panics on a column type ID
// it doesn't know, and query.scanResultSet calls DatabaseTypeName() on every
// column of every result set, so one column of a type newer than the pinned
// driver reaches it.
//
// The recovered panic is reported like any other background failure, through
// App.postAndWake onto the UI goroutine. The stack goes to the log file (see
// config.LogFilePath), as the screen has no room for it.
func (a *App) safego(what string, fn func()) {
	a.safegoRepair(what, nil, fn)
}

// labelGoroutine tags the calling goroutine with what, which Go 1.27 prints in
// the header of every traceback it appears in (the recovered stack reportPanic
// logs, a goroutine dump, the goroutineleak profile). Without it each is headed
// by an anonymous func literal naming no background operation.
//
// Set directly, not through pprof.Do, whose deferred restore runs while the
// panic is still unwinding (before the recover below) and would strip the label
// from the one stack that has to carry it.
func labelGoroutine(what string) {
	pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels("op", what)))
}

// safegoRepair is safego for a background operation that latched UI state
// before starting: a busy flag, a "loading" placeholder, a dimmed toolbar.
// repair runs on the UI goroutine if, and only if, fn panicked; nil is safego.
//
// The latch is otherwise cleared by the callback fn posts on finishing, and a
// panic unwinds past it, so the flag stays set for the object's lifetime: the
// Log File Viewer's toolbar stays dimmed with Refresh, Export and both
// selectors dead until the panel closes, an Activity Monitor tab sits at
// "Running..." forever, IntelliSense reports a catalog still loading that
// nothing is loading. safego reports the panic either way; what is lost without
// a repair step is the UI's way out.
//
// repair is queued before the panic is reported, so the status bar's last word
// is the panic rather than whatever repair sets. Same ordering and reason as
// backfillRow's recovery (detail_browser_backfill.go).
func (a *App) safegoRepair(what string, repair func(), fn func()) {
	go func() {
		labelGoroutine(what)
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if repair != nil {
				a.postAndWake(repair)
			}
			a.reportPanic(r, what)
		}()
		fn()
	}()
}

// recoverPanic is the bare deferred recovery, for a goroutine that can't be a
// bare func(): `defer a.recoverPanic("what this was doing")`.
func (a *App) recoverPanic(what string) {
	if r := recover(); r != nil {
		a.reportPanic(r, what)
	}
}

// fanOut runs work(i) for every i in [0, n) across a bounded pool of
// maxRowFetchConcurrency goroutines and returns once every call has finished.
// It is the one worker pool in this package (the Detail Browser's per-row
// backfill and the Log File Viewer's per-file reads both use it) and the only
// place that spawns a goroutine outside safego/safegoRepair, so it provides
// both halves of what they give by hand: the traceback label and the recover.
//
// Each call is recovered on its own: a panic in one item reports and moves on
// rather than taking its worker, and every item that worker still owes, down.
// onPanic (optional) runs on the worker for the panicked item *before* fanOut
// can return, because a caller acting on the results at once needs the repair
// already queued. The Detail Browser caches its rows then, and a row whose
// fetch died would otherwise be cached with its "..." placeholder permanently,
// since reselecting the node is a cache hit that never refetches.
//
// The queue is filled and closed before a worker exists, so no send can block
// on one. Handing indices out from this goroutine would depend on a worker
// being alive to receive them: if a panic escaped the per-item recovery and took
// every worker down, the send would block forever and hang the caller. n ints
// is a few KB at the largest sizes this runs on.
//
// A fixed number of workers pulling indices off a channel, rather than n
// goroutines each waiting on a token: both bound the work, but the token form
// parks hundreds of idle goroutines on a folder with hundreds of entries.
func (a *App) fanOut(n int, what string, work func(i int), onPanic func(i int)) {
	if n <= 0 {
		return
	}
	idx := make(chan int, n)
	for i := range n {
		idx <- i
	}
	close(idx)

	one := func(i int) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if onPanic != nil {
				onPanic(i)
			}
			a.reportPanic(r, what)
		}()
		work(i)
	}

	var wg sync.WaitGroup
	for range min(n, maxRowFetchConcurrency) {
		wg.Go(func() {
			// The per-item recovery above keeps a worker alive; this is the belt under it,
			// for a panic in onPanic or reportPanic itself. One escaping both is on a
			// background goroutine where nothing else can catch it: the process dies and
			// Run's screen.Fini() never restores the terminal.
			labelGoroutine(what)
			defer a.recoverPanic(what)
			for i := range idx {
				one(i)
			}
		})
	}
	wg.Wait()
}

// reportPanic logs r with a stack and puts it on the status bar. Split out of
// recoverPanic for a deferred recovery with repair work to do first: see
// fanOut, whose onPanic must fill the row a backfill abandoned before
// reporting, or the row is cached blank forever.
func (a *App) reportPanic(r any, what string) {
	stack := string(debug.Stack())
	log.Printf("panic in %s: %v\n%s", what, r, stack)
	msg := fmt.Sprintf("Internal error in %s: %v — see the log for details", what, r)
	a.postAndWake(func() { a.setStatus(msg) })
}
