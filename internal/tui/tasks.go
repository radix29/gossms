package tui

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/loop"
)

// Task is one long-running background operation (a backup, restore, full-text
// population, an Extended Events feed, Generate Scripts) of the kind SSMS
// reports in its status bar and lets the user check on or cancel later rather
// than blocking the UI.
//
// The registry — IDs, the context and Cancel, the history cap, and the rule
// that a run is Cancelled only if it failed after a Cancel asked for while it
// ran, never when it panicked — is tuikit's loop.Tasks (App.tasks; its doc and
// loop/tasks_test.go hold the rules). A worker starts with a.tasks.Start,
// reports through a.tasks.Progress and a.tasks.Done, and never touches a Task
// field; a GoRepair repair, already on the UI goroutine, calls
// a.tasks.Finish(t, loop.ErrTaskPanicked). What stays here is the wording.
type Task = loop.Task

// maxTaskHistory caps how many tasks App.tasks keeps for the Tasks dialog's
// history before the oldest finished ones are evicted (loop.Tasks.Keep).
const maxTaskHistory = 50

// wireTasks connects the task registry to this App: results come back through
// postAndWake and each finish words the status bar. NewApp and newTestApp call
// it before any task can start.
func (a *App) wireTasks() {
	a.tasks.Post = a.postAndWake
	a.tasks.Keep = maxTaskHistory
	a.tasks.OnFinish = a.taskFinished
}

// taskFinished is the registry's OnFinish: the status bar says how t ended, as
// query execution reports its own completion.
func (a *App) taskFinished(t *Task) {
	switch {
	case t.Cancelled:
		a.setStatus(t.Label + " cancelled" + taskRepairNote(t))
	case t.Err != nil:
		a.setStatus(fmt.Sprintf("%s failed: %v", t.Label, withPermissionAdvice(t.Err)))
	default:
		a.setStatus(t.Label + " completed")
	}
}

// taskStatusText renders the one-line summary the Tasks dialog shows for t.
func taskStatusText(t *Task) string {
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
		return t.Label + " — cancelled" + taskRepairNote(t)
	case t.Err != nil:
		return t.Label + " — failed: " + t.Err.Error()
	default:
		return t.Label + " — done"
	}
}

// taskRepairNote is what a cancelled task must still say: " — " and the
// failure of the repair gosmo runs after a write cut short (a
// *gosmo.RepairError, e.g. the database left SINGLE_USER by a cancelled
// Restore with Close existing connections), or "" when there was none.
// "cancelled" alone would hide the one thing about the server's state the
// user has to act on.
func taskRepairNote(t *Task) string {
	if re, ok := errors.AsType[*gosmo.RepairError](t.Err); ok {
		return " — " + re.Error()
	}
	return ""
}
