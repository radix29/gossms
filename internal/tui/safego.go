package tui

import (
	"fmt"
	"log"
	"runtime/debug"
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
		defer a.recoverPanic(what)
		fn()
	}()
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

// reportPanic logs r with a stack and puts it on the status bar. Split out
// of recoverPanic for a deferred recovery that has repair work of its own to
// do first — see backfillRows, whose recovery must fill the row it abandoned
// before reporting, or the row is cached blank forever.
func (a *App) reportPanic(r any, what string) {
	stack := string(debug.Stack())
	log.Printf("panic in %s: %v\n%s", what, r, stack)
	msg := fmt.Sprintf("Internal error in %s: %v — see the log for details", what, r)
	a.postAndWake(func() { a.setStatus(msg) })
}
