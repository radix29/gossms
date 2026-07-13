package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// maxTaskHistory caps how many finished tasks App.tasks keeps around (for
// the Tasks dialog's history) before the oldest are evicted. Running tasks
// are never evicted.
const maxTaskHistory = 50

// Task tracks one long-running background operation — a backup, a restore,
// an index rebuild — the kind of thing SSMS reports in its status bar and
// lets the user check on or cancel later rather than blocking the UI.
//
// A Task's fields are only ever mutated on the main goroutine, via the
// closures App.postProgress/postTaskDone hand to postEvent — the same
// invariant QueryPanel.result and DetailBrowser rely on elsewhere in this
// codebase (see query_panel.go, detail_browser.go). No mutex is needed as
// long as that invariant holds: the goroutine doing the actual work never
// touches a Task field directly.
type Task struct {
	ID       int
	Label    string
	Progress int // 0-100; -1 = indeterminate (no percentage to show)
	Message  string
	Done     bool
	Err      error
	Started  time.Time
	Finished time.Time

	cancel context.CancelFunc
}

// Cancel requests the task's context be cancelled. Safe to call on an
// already-finished task (a no-op) or multiple times.
func (t *Task) Cancel() {
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
		return t.Label + " — " + core.Itoa(t.Progress) + "% — " + msg
	case t.Err != nil:
		return t.Label + " — failed: " + t.Err.Error()
	default:
		return t.Label + " — done"
	}
}

// startTask registers a new background task under label and returns it
// along with a context the caller's goroutine should run its work under —
// cancelled the moment Task.Cancel is called (from the Tasks dialog, or
// anywhere else that has the Task). The caller reports progress via
// App.postProgress and completion via App.postTaskDone; both must be used
// instead of touching the Task directly, since the work itself runs on a
// background goroutine (see the Task doc comment).
func (a *App) startTask(label string) (*Task, context.Context) {
	a.taskSeq++
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{ID: a.taskSeq, Label: label, Progress: -1, Started: time.Now(), cancel: cancel}
	a.tasks = append(a.tasks, t)
	a.pruneFinishedTasks()
	return t, ctx
}

// postProgress schedules a progress update on t to run on the main
// goroutine, then wakes the event loop so it draws immediately — the same
// postEvent+EventInterrupt handoff every other background operation in
// this codebase uses (see query_panel.go, app_connections.go).
func (a *App) postProgress(t *Task, progress int, message string) {
	a.postEvent(func() {
		t.Progress = progress
		t.Message = message
	})
	a.screen.EventQ() <- tcell.NewEventInterrupt(nil)
}

// postTaskDone marks t finished (err nil on success) on the main goroutine,
// updates the status bar to match (every task consumer wants this, the same
// way query execution always reports its own completion), and wakes the
// event loop.
func (a *App) postTaskDone(t *Task, err error) {
	a.postEvent(func() {
		t.Done = true
		t.Err = err
		t.Finished = time.Now()
		if err != nil {
			a.setStatus(fmt.Sprintf("%s failed: %v", t.Label, err))
		} else {
			a.setStatus(t.Label + " completed")
		}
	})
	a.screen.EventQ() <- tcell.NewEventInterrupt(nil)
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
