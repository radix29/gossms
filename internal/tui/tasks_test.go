package tui

import (
	"context"
	"errors"
	"testing"
)

// TestStartTaskAssignsIncrementingIDs pins the registry bookkeeping that
// every other part of tasks.go depends on: each startTask call registers
// the task and hands out a strictly increasing ID.
func TestStartTaskAssignsIncrementingIDs(t *testing.T) {
	a := newTestApp()

	t1, ctx1 := a.startTask("Backup")
	t2, _ := a.startTask("Rebuild")

	if t1.ID == t2.ID {
		t.Fatalf("both tasks got ID %d, want distinct IDs", t1.ID)
	}
	if len(a.tasks) != 2 || a.tasks[0] != t1 || a.tasks[1] != t2 {
		t.Fatalf("a.tasks = %v, want [t1, t2]", a.tasks)
	}
	if ctx1.Err() != nil {
		t.Errorf("fresh task's context already done: %v", ctx1.Err())
	}
}

// TestTaskCancelStopsItsContext confirms Cancel actually cancels the
// context startTask handed back — the mechanism a real consumer (Backup,
// Rebuild) checks via ctx.Err() to stop its work early.
func TestTaskCancelStopsItsContext(t *testing.T) {
	a := newTestApp()
	task, ctx := a.startTask("Backup")

	task.Cancel()
	if ctx.Err() != context.Canceled {
		t.Errorf("ctx.Err() after Cancel = %v, want context.Canceled", ctx.Err())
	}

	// Cancelling twice, or cancelling after the task is already marked
	// done, must not panic — the Tasks dialog calls this unconditionally.
	task.Cancel()
	task.Done = true
	task.Cancel()
}

// TestTaskCancelOnNilCancelIsNoop covers a Task built without going through
// startTask (e.g. a zero-value Task{}, which the Tasks dialog's tests might
// construct directly) — Cancel must not panic just because there's no
// context to cancel.
func TestTaskCancelOnNilCancelIsNoop(t *testing.T) {
	task := &Task{Label: "no context"}
	task.Cancel()
}

// TestRunningTaskCount confirms finished tasks don't count as running.
func TestRunningTaskCount(t *testing.T) {
	a := newTestApp()
	t1, _ := a.startTask("Backup")
	_, _ = a.startTask("Rebuild")

	if got := a.runningTaskCount(); got != 2 {
		t.Fatalf("runningTaskCount = %d, want 2", got)
	}
	t1.Done = true
	if got := a.runningTaskCount(); got != 1 {
		t.Errorf("runningTaskCount after finishing one = %d, want 1", got)
	}
}

// TestPruneFinishedTasksKeepsRunningAndCapsHistory confirms
// pruneFinishedTasks never evicts a running task even when it's the
// oldest, and only trims finished tasks once the registry exceeds
// maxTaskHistory.
func TestPruneFinishedTasksKeepsRunningAndCapsHistory(t *testing.T) {
	a := newTestApp()

	// One running task registered first — must survive pruning no matter
	// how many finished tasks pile up after it.
	oldestRunning, _ := a.startTask("still running")

	for i := 0; i < maxTaskHistory+10; i++ {
		task, _ := a.startTask("finished")
		task.Done = true
	}

	if len(a.tasks) > maxTaskHistory+1 {
		t.Fatalf("len(a.tasks) = %d, want at most %d (history cap + the running task)", len(a.tasks), maxTaskHistory+1)
	}
	found := false
	for _, task := range a.tasks {
		if task == oldestRunning {
			found = true
		}
	}
	if !found {
		t.Error("oldest running task was pruned, want it kept regardless of age")
	}
}

// TestTaskStatusText pins the three status-line shapes the Tasks dialog and
// status bar render: indeterminate running, percentage running, and both
// finished outcomes.
func TestTaskStatusText(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want string
	}{
		{"indeterminate running", Task{Label: "Rebuild", Progress: -1}, "Rebuild — running..."},
		{"indeterminate with message", Task{Label: "Rebuild", Progress: -1, Message: "on [dbo].[Orders]"}, "Rebuild — on [dbo].[Orders]"},
		{"percentage running", Task{Label: "Backup", Progress: 40, Message: "40 percent processed."}, "Backup — 40% — 40 percent processed."},
		{"done", Task{Label: "Backup", Done: true}, "Backup — done"},
		{"failed", Task{Label: "Backup", Done: true, Err: errors.New("disk full")}, "Backup — failed: disk full"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.task.statusText(); got != c.want {
				t.Errorf("statusText() = %q, want %q", got, c.want)
			}
		})
	}
}
