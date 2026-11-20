package tui

import (
	"errors"
	"testing"
)

// The registry's rules (IDs, Cancel, the running count, the history cap) are
// tuikit's loop.Tasks and tested there (loop/tasks_test.go); what is gossms's
// is the wording.

// TestTaskStatusText pins the status-line shapes the Tasks dialog renders:
// indeterminate running, percentage running, and both finished outcomes.
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
			if got := taskStatusText(&c.task); got != c.want {
				t.Errorf("taskStatusText = %q, want %q", got, c.want)
			}
		})
	}
}
