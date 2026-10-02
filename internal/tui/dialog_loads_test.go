package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The async loads in these tests run against a bare ServerConn, so their
// goroutines panic on the nil gosmo.Server and recoverPanic logs it (muted by
// quietLog). Nothing drains a.pending, so no completion ever runs: what is
// under test is which runs are still current, not what they deliver.

// T19: Restore's loads used to share one token, so a history load started
// while OK's "Checking target database..." was out superseded the check, and
// the restore never began.
func TestRestoreHistoryLoadDoesNotDropTheTargetCheck(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewRestoreDialog(a)
	quietLog(t)
	a.screen = nil
	d.show(&db.ServerConn{}, "testdb")
	d.fFile.SetValue(`C:\backups\testdb.bak`)

	d.startRestore()
	check := d.checkRun.seq
	if d.checkRun.Idle() {
		t.Fatal("OK started no target-database check")
	}
	d.loadHistory("otherdb")
	if !d.checkRun.Current(check) || d.checkRun.Idle() {
		t.Error("a history load superseded OK's target-database check")
	}
}

// Every Restore load is abandoned when the dialog closes: superseded, so a
// result still on its way is dropped, and cancelled, so its query stops now.
func TestRestoreHideAbandonsItsLoads(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewRestoreDialog(a)
	quietLog(t)
	a.screen = nil
	d.show(&db.ServerConn{}, "testdb")
	d.fFile.SetValue(`C:\backups\testdb.bak`)

	d.loadHistory("testdb")
	d.startRestore()
	hist, check := d.historyRun.seq, d.checkRun.seq
	d.Hide()
	if d.historyRun.Current(hist) || !d.historyRun.Idle() {
		t.Error("Hide left the history load current or running")
	}
	if d.checkRun.Current(check) || !d.checkRun.Idle() {
		t.Error("Hide left the target-database check current or running")
	}
}

func TestBackupHideAbandonsTheDatabaseList(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewBackupDialog(a)
	quietLog(t)
	a.screen = nil
	d.show(&db.ServerConn{}, "testdb")
	seq := d.dbListRun.seq
	if d.dbListRun.Idle() {
		t.Fatal("show started no database-list load")
	}
	d.Hide()
	if d.dbListRun.Current(seq) || !d.dbListRun.Idle() {
		t.Error("Hide left the database-list load current or running")
	}
}

// New Snapshot's Default File Paths read is for the source and name typed when
// it was pressed. Changing either while it is out used to let it land anyway,
// filling the grid with paths for a snapshot no longer described.
func TestSnapshotNameChangeAbandonsTheDefaultsRead(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewNewSnapshotDialog(a)
	quietLog(t)
	a.screen = nil
	d.show(&db.ServerConn{}, "src")
	d.buildPages(&snapshotPrefetch{sources: []string{"src"}, existing: newNameSet("")})

	form := d.forms[0]
	var name *propsheet.TextRow
	for _, r := range form.Rows() {
		if tr, ok := r.(*propsheet.TextRow); ok && tr.Label() == "Snapshot name" {
			name = tr
		}
	}
	if name == nil {
		t.Fatal("no Snapshot name row")
	}
	// Press Default File Paths, as the Form routes Enter to it.
	var pressed bool
	for _, r := range form.Rows() {
		if br, ok := r.(*propsheet.ButtonsRow); ok {
			b := br.Buttons()[0]
			b.Focus(true)
			pressed = b.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
		}
	}
	if !pressed {
		t.Fatal("could not press Default File Paths")
	}
	if d.defaultsRead.Idle() {
		t.Fatal("Default File Paths started no read")
	}
	seq := d.defaultsRead.seq

	name.Edit("src_other")
	if d.defaultsRead.Current(seq) || !d.defaultsRead.Idle() {
		t.Error("a name change left the defaults read for the old name current or running")
	}
}

// T69: closing Object Dependencies stops its fetch rather than leaving it to
// run to childFetchTimeout.
func TestDependenciesCloseAbandonsTheFetch(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewPropertiesDialog(a)
	_, seq := d.run.Begin(t.Context())
	d.ShowProperties("Dependencies: dbo.t", nil)
	d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if d.Visible() {
		t.Fatal("Escape did not close the dialog")
	}
	if d.run.Current(seq) || !d.run.Idle() {
		t.Error("closing the dialog left its dependency fetch current or running")
	}
}

// T28: a job runWithProgress refuses must still release the caller's latch —
// neither done nor the panic repair runs, so without this the busy flag the
// caller set before asking stays set for good.
func TestRefusedProgressJobRunsRepair(t *testing.T) {
	a := newTestApp()
	a.progressBusy = true // another job's dialog is up
	repaired := false
	a.runWithProgress(progressJob{
		title: "Recycle", sc: &db.ServerConn{}, what: "test",
		repair: func() { repaired = true },
	}, func(context.Context, progressReport) error {
		t.Error("a refused job ran its work")
		return nil
	}, func(error, bool) { t.Error("a refused job ran done") })
	if !repaired {
		t.Error("the refusal did not release the caller's latch")
	}
	if !strings.Contains(a.statusText, "still running") {
		t.Errorf("status = %q, want the refusal", a.statusText)
	}
}

// T61: OK reloads nothing. InvalidateAll dispatches the current page's fetch at
// once, and the dialog OK is closing has no use for it; Apply, which stays
// open, does reload.
func TestPropDialogOKDoesNotReloadThePageItCloses(t *testing.T) {
	for _, c := range []struct {
		name    string
		ok      bool
		reloads int
	}{{"OK", true, 0}, {"Apply", false, 1}} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			d := newSheetDialog(t, []propPage{{title: "General"}},
				map[int]propApply{0: func(context.Context) error { return nil }}, nil)
			d.app = a
			d.ctx = context.Background()
			load := d.OnLoadPage
			loads := 0
			d.OnLoadPage = func(page, seq int) { loads++; load(page, seq) }

			d.runApply(c.ok)
			drainUntil(t, a, func() bool { return !d.Applying() }, "the apply to report back")
			if loads != c.reloads {
				t.Errorf("%s dispatched %d page loads, want %d", c.name, loads, c.reloads)
			}
		})
	}
}

// T61: a backup or restore stopped by its Cancel button reads "cancelled", not
// as a failure carrying the driver's or server's wording of the cancel.
func TestCancelledTaskReadsCancelled(t *testing.T) {
	a := newTestApp()

	stopped, _ := a.startTask(context.Background(), "Backup db1")
	stopped.Cancel()
	a.markTaskDone(stopped, context.Canceled)
	if !stopped.Cancelled || stopped.statusText() != "Backup db1 — cancelled" {
		t.Errorf("cancelled task: Cancelled=%v, statusText %q", stopped.Cancelled, stopped.statusText())
	}
	if a.statusText != "Backup db1 cancelled" {
		t.Errorf("status bar = %q", a.statusText)
	}

	// Finished before the cancel reached it: a success stays a success.
	raced, _ := a.startTask(context.Background(), "Backup db2")
	raced.Cancel()
	a.markTaskDone(raced, nil)
	if raced.Cancelled || raced.statusText() != "Backup db2 — done" {
		t.Errorf("finished-anyway task: Cancelled=%v, statusText %q", raced.Cancelled, raced.statusText())
	}

	// Not cancelled: an error is a failure.
	failed, _ := a.startTask(context.Background(), "Backup db3")
	a.markTaskDone(failed, errors.New("disk full"))
	if failed.Cancelled || failed.statusText() != "Backup db3 — failed: disk full" {
		t.Errorf("failed task: Cancelled=%v, statusText %q", failed.Cancelled, failed.statusText())
	}

	// A panic after a cancel is still a panic.
	panicked, _ := a.startTask(context.Background(), "Backup db4")
	panicked.Cancel()
	a.markTaskDone(panicked, errTaskPanicked)
	if panicked.Cancelled {
		t.Error("a panicked task reads as cancelled")
	}
}
