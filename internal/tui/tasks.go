package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// errTaskPanicked is what a task is finished with when its goroutine
// panicked — the panic itself is already logged and reported, so this only
// has to give the task a terminal state and a reason in the Tasks dialog.
var errTaskPanicked = errors.New("stopped unexpectedly — see the log for details")

// maxTaskHistory caps how many finished tasks App.tasks keeps around (for
// the Tasks dialog's history) before the oldest are evicted. Running tasks
// are never evicted.
const maxTaskHistory = 50

// Task tracks one long-running background operation (a backup, restore or index
// rebuild) of the kind SSMS reports in its status bar and lets the user check
// on or cancel later rather than blocking the UI.
//
// A Task's fields are only mutated on the main goroutine, via the closures
// App.postProgress/postTaskDone hand to postAndWake (the invariant
// QueryPanel.result and DetailBrowser rely on; see query_panel.go,
// detail_browser.go). No mutex is needed while that holds: the goroutine doing
// the work never touches a Task field directly.
type Task struct {
	ID       int
	Label    string
	Progress int // 0-100; -1 = indeterminate (no percentage to show)
	Message  string
	Done     bool
	Err      error
	Started  time.Time
	Finished time.Time
	// Cancelled is set when the task finished with an error after Cancel was asked
	// for while it ran. The error is then the cancel itself, as the driver or
	// server words it ("context canceled", "BACKUP DATABASE is terminating
	// abnormally"), and reporting it as a failure blames the server for what the
	// user did. A run that finished despite the cancel keeps its success; a panic
	// is still a failure. Err keeps the error.
	Cancelled bool

	cancel context.CancelFunc
	// cancelAsked records a Cancel while the task was running — markTaskDone's
	// own Cancel, releasing a finished task's context, does not count.
	cancelAsked bool
}

// Cancel requests the task's context be cancelled. Safe to call on an
// already-finished task (a no-op) or multiple times.
func (t *Task) Cancel() {
	if !t.Done {
		t.cancelAsked = true
	}
	if t.cancel != nil {
		t.cancel()
	}
}

// statusText renders the one-line summary the Tasks dialog and status bar
// show for this task.
func (t *Task) statusText() string {
	switch {
	case !t.Done:
		msg := t.Message
		if msg == "" {
			msg = "running..."
		}
		if t.Progress < 0 {
			return t.Label + " — " + msg
		}
		return t.Label + " — " + strconv.Itoa(t.Progress) + "% — " + msg
	case t.Cancelled:
		return t.Label + " — cancelled"
	case t.Err != nil:
		return t.Label + " — failed: " + t.Err.Error()
	default:
		return t.Label + " — done"
	}
}

// startTask registers a new background task under label and returns it with a
// context the caller's goroutine should run its work under, cancelled when
// Task.Cancel is called (from the Tasks dialog or elsewhere) or when parent is
// cancelled. Callers whose work is scoped to a *db.ServerConn should pass
// sc.Server.Context() as parent, so disconnecting cancels a long-running task
// (e.g. a RESTORE) instead of leaving it unbounded; others can pass
// context.Background(). The caller reports progress via App.postProgress and
// completion via App.postTaskDone, never by touching the Task directly, since
// the work runs on a background goroutine (see the Task doc comment).
func (a *App) startTask(parent context.Context, label string) (*Task, context.Context) {
	a.taskSeq++
	ctx, cancel := context.WithCancel(parent)
	t := &Task{ID: a.taskSeq, Label: label, Progress: -1, Started: time.Now(), cancel: cancel}
	a.tasks = append(a.tasks, t)
	a.pruneFinishedTasks()
	return t, ctx
}

// postProgress schedules a progress update on t to run on the main
// goroutine, then wakes the event loop so it draws immediately — the same
// postAndWake handoff every other background operation in this codebase
// uses (see query_panel.go, app_connections.go).
func (a *App) postProgress(t *Task, progress int, message string) {
	a.postAndWake(func() {
		t.Progress = progress
		t.Message = message
	})
}

// postTaskDone marks t finished (err nil on success) on the main goroutine,
// releases its context (the work is done whether or not Cancel was called),
// updates the status bar to match (as query execution reports its own
// completion), and wakes the event loop.
func (a *App) postTaskDone(t *Task, err error) {
	a.postAndWake(func() { a.markTaskDone(t, err) })
}

// markTaskDone is postTaskDone's body, for a caller that is already on the
// main goroutine — a safegoRepair step, which is posted there itself and
// must not add a second hop that would land after the panic report.
func (a *App) markTaskDone(t *Task, err error) {
	t.Done = true
	t.Err = err
	t.Cancelled = err != nil && !errors.Is(err, errTaskPanicked) && t.cancelAsked
	t.Finished = time.Now()
	t.Cancel()
	switch {
	case t.Cancelled:
		a.setStatus(t.Label + " cancelled")
	case err != nil:
		a.setStatus(fmt.Sprintf("%s failed: %v", t.Label, withPermissionAdvice(err)))
	default:
		a.setStatus(t.Label + " completed")
	}
}

// runningTaskCount reports how many tasks are still in flight, for the
// status bar summary.
func (a *App) runningTaskCount() int {
	n := 0
	for _, t := range a.tasks {
		if !t.Done {
			n++
		}
	}
	return n
}

// pruneFinishedTasks trims the oldest finished tasks once the registry
// exceeds maxTaskHistory, so a long session doesn't grow it unbounded.
// Running tasks are never evicted regardless of how many there are.
func (a *App) pruneFinishedTasks() {
	if len(a.tasks) <= maxTaskHistory {
		return
	}
	kept := a.tasks[:0]
	excess := len(a.tasks) - maxTaskHistory
	dropped := 0
	for _, t := range a.tasks {
		if !t.Done || dropped >= excess {
			kept = append(kept, t)
		} else {
			dropped++
		}
	}
	a.tasks = kept
}
