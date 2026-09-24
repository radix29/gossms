package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/db"
)

// progressScreenRows renders rows [from, to) of a recordingScreen as text,
// trailing blanks trimmed, columns from x on.
func progressScreenRows(s *recordingScreen, x, from, to int) []string {
	var out []string
	for y := from; y < to; y++ {
		var b strings.Builder
		for cx := x; cx < s.w; cx++ {
			r, ok := s.runes[[2]int{cx, y}]
			if !ok || r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

// doneTask is a finished task with a fixed 1h 2m 3s run, so the elapsed row
// is deterministic.
func doneTask(err error) *Task {
	start := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	return &Task{Progress: 40, Done: true, Err: err, Started: start,
		Finished: start.Add(time.Hour + 2*time.Minute + 3*time.Second)}
}

// progressBar is drawProgressBar's text at width w and pct.
func progressBar(w, pct int) string {
	s := &recordingScreen{w: w + 10, h: 1, runes: map[[2]int]rune{}}
	drawProgressBar(s, 0, 0, w, pct, tcell.StyleDefault)
	return progressScreenRows(s, 0, 0, 1)[0]
}

// The Backup and Restore progress views share drawTaskProgress; each keeps its
// own header rows and wording, and the bar, times and message land on the
// rows they always did — Backup's one "Progress:" label row lower.
func TestBackupProgressView(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewBackupDialog(a)
	quietLog(t) // see TestBackupDialogDragOutOfDestKeepsExtending
	a.screen = nil
	d.show(&db.ServerConn{}, "testdb")
	d.mode = backupModeProgress
	d.taskDB, d.taskType, d.taskDest = "Sales", "Full", `C:\bak\Sales.bak`

	for _, tc := range []struct {
		err  error
		pct  int
		last string
	}{
		{nil, 100, "Backup completed successfully."},
		{errors.New("Operating system error 5(Access is denied.)"), 40, "Failed: Operating system error 5(Access is denied.)"},
	} {
		d.task = doneTask(tc.err)
		s := &recordingScreen{w: 100, h: 40, runes: map[[2]int]rune{}}
		d.drawProgress(s)
		inner := d.InnerRect()
		got := progressScreenRows(s, inner.X+1, inner.Y+1, inner.Y+13)
		want := []string{
			"Database : Sales",
			"Type     : Full",
			`Target   : C:\bak\Sales.bak`,
			"",
			"Progress:",
			"",
			progressBar(inner.W-2, tc.pct),
			"",
			"Elapsed  : 01:02:03",
			"Remaining: --:--:--",
			"",
			tc.last,
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("backup progress view:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

func TestRestoreProgressView(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewRestoreDialog(a)
	a.screen = nil
	d.show(&db.ServerConn{}, "testdb")
	d.mode = restoreModeProgress
	d.taskTarget, d.taskSource = "Sales_copy", `C:\bak\Sales.bak`

	for _, tc := range []struct {
		err  error
		pct  int
		last string
	}{
		{nil, 100, "Restore completed successfully."},
		{errors.New("RESTORE DATABASE is terminating abnormally."), 40, "Failed: RESTORE DATABASE is terminating abnormally."},
	} {
		d.task = doneTask(tc.err)
		s := &recordingScreen{w: 100, h: 40, runes: map[[2]int]rune{}}
		d.drawProgress(s)
		inner := d.InnerRect()
		got := progressScreenRows(s, inner.X+1, inner.Y+1, inner.Y+10)
		want := []string{
			"Database : Sales_copy",
			`Source   : C:\bak\Sales.bak`,
			"",
			progressBar(inner.W-2, tc.pct),
			"",
			"Elapsed  : 01:02:03",
			"Remaining: --:--:--",
			"",
			tc.last,
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("restore progress view:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}
