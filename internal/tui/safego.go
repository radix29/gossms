package tui

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"runtime/pprof"
	"sync"
)

// safego runs fn on a new goroutine, turning a panic into a status-bar
// message and a log entry instead of a process-wide crash.
//
// Every background operation in this package goes through it. A panic on a
// background goroutine is not recoverable by the UI goroutine, so without
// this it takes the whole process down — and Run's `defer screen.Fini()`
// never runs, leaving the terminal in raw mode on the alternate screen with
// the user's unsaved query text gone. The panic trace itself lands on the
// alternate screen and disappears with it.
//
// This is not a theoretical risk. go-mssqldb's makeGoLangTypeName panics
// outright on a column type ID it doesn't know, and query.scanResultSet
// calls DatabaseTypeName() on every column of every result set — so a single
// column of a type newer than the pinned driver is enough to reach it.
//
// The recovered panic is reported the way any other background failure is:
// through App.postAndWake, so the message lands on the UI goroutine. The
// stack goes to the log file (see config.LogFilePath) rather than the
// screen, which has no room for it.
func (a *App) safego(what string, fn func()) {
	go func() {
		labelGoroutine(what)
		defer a.recoverPanic(what)
		fn()
	}()
}

// labelGoroutine tags the calling goroutine with what, which Go 1.27 prints
// in the header of every traceback the goroutine appears in — the recovered
// stack reportPanic logs, a goroutine dump, the goroutineleak profile. Without
// it each one is headed by an anonymous func literal that says nothing about
// which background operation it was.
//
// Set directly rather than through pprof.Do, whose deferred restore runs while
// the panic is still unwinding — that is, before the recover below — and would
// strip the label from the one stack that has to carry it.
func labelGoroutine(what string) {
	pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels("op", what)))
}

// safegoRepair is safego for a background operation that latched UI state
// before it started: a busy flag, a "loading" placeholder, a dimmed toolbar.
// repair runs on the UI goroutine if — and only if — fn panicked.
//
// The latch is otherwise cleared by the callback fn posts when it finishes,
// and a panic unwinds straight past that callback, so the flag stays set for
// the object's lifetime: the Log File Viewer's toolbar stays dimmed with
// Refresh, Export and both selectors dead until the panel is closed, an
// Activity Monitor tab sits at "Running..." forever, IntelliSense reports a
// catalog still loading that nothing is loading. safego reports the panic
// either way — what is lost without a repair step is the UI's way out of it.
//
// repair is queued before the panic is reported, so the status bar's last
// word is the panic rather than whatever the repair sets. Same ordering, and
// the same reason, as backfillRow's recovery (detail_browser_backfill.go).
func (a *App) safegoRepair(what string, repair func(), fn func()) {
	go func() {
		labelGoroutine(what)
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			a.postAndWake(repair)
			a.reportPanic(r, what)
		}()
		fn()
	}()
}

// recoverPanic is safego's deferred half, also usable directly by a
// goroutine that can't be expressed as a bare func() — call it as
// `defer a.recoverPanic("what this was doing")`.
func (a *App) recoverPanic(what string) {
	if r := recover(); r != nil {
		a.reportPanic(r, what)
	}
}

// fanOut runs work(i) for every i in [0, n) across a bounded pool of
// maxRowFetchConcurrency goroutines and returns once every call has finished.
// It is the one worker pool in this package — the Detail Browser's per-row
// backfill and the Log File Viewer's per-file reads both run on it — and the
// only place that spawns a goroutine outside safego/safegoRepair, so it takes
// both halves of what they give by hand: the label on the traceback and the
// recover.
//
// Each call is recovered on its own. A panic in one item reports and moves on
// to the next rather than taking its worker — and every item that worker still
// owes — down with it. onPanic (optional) runs on the worker for the item that
// panicked, *before* fanOut can return: a caller that acts on the results the
// moment fanOut returns needs the repair for that item already queued. The
// Detail Browser caches its rows then, and a row whose fetch died would
// otherwise be cached still showing its "…" placeholder, permanently, since
// reselecting the node is a cache hit that never refetches.
//
// The queue is filled and closed before a worker exists, so no send can ever
// block on one. Handing the indices out from this goroutine instead would make
// it depend on a worker still being alive to receive them: if a panic escaped
// the per-item recovery and took every worker with it, the send would block
// forever and hang the caller. n ints is a few KB at the largest sizes this
// runs on.
//
// The pool is a fixed number of workers pulling indices off a channel, rather
// than n goroutines each waiting on a token: both bound the work, but the token
// form parks hundreds of idle goroutines on a folder with hundreds of entries.
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The per-item recovery above is what keeps a worker alive; this
			// is the belt under it, for a panic in onPanic or reportPanic
			// itself. One that escapes both is on a background goroutine where
			// nothing else can catch it — the process dies and Run's
			// screen.Fini() never restores the terminal.
			labelGoroutine(what)
			defer a.recoverPanic(what)
			for i := range idx {
				one(i)
			}
		}()
	}
	wg.Wait()
}

// reportPanic logs r with a stack and puts it on the status bar. Split out
// of recoverPanic for a deferred recovery that has repair work of its own to
// do first — see fanOut, whose onPanic must fill the row a backfill abandoned
// before reporting, or the row is cached blank forever.
func (a *App) reportPanic(r any, what string) {
	stack := string(debug.Stack())
	log.Printf("panic in %s: %v\n%s", what, r, stack)
	msg := fmt.Sprintf("Internal error in %s: %v — see the log for details", what, r)
	a.postAndWake(func() { a.setStatus(msg) })
}
