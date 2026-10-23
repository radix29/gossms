package tui

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// Every backup goes out WITH INIT, so a generated name reused across backups
// destroyed the one before it: the second log backup from the dialog replaced
// "<db>_log.trn" on disk while msdb still listed the first, and the restore
// chain stopped there. Two showings a second apart must name different files.
func TestBackupAutoDestIsUniquePerSecond(t *testing.T) {
	d := backupForm("AppDB")
	d.backupDir = new(`C:\Backup`)
	at := time.Date(2026, 10, 15, 1, 5, 0, 0, time.Local)
	d.now = func() time.Time { return at }

	for _, c := range []struct {
		typ  int
		want string
	}{
		{0, `C:\Backup\AppDB_FULL_20261015_010500.bak`},
		{1, `C:\Backup\AppDB_DIFF_20261015_010500.bak`},
		{2, `C:\Backup\AppDB_LOG_20261015_010500.trn`},
	} {
		d.rbType.SetSelected(c.typ)
		if got := d.autoDest(); got != c.want {
			t.Errorf("type %d: autoDest = %q, want %q", c.typ, got, c.want)
		}
	}

	first := d.autoDest()
	at = at.Add(time.Second)
	if second := d.autoDest(); second == first {
		t.Errorf("two log backups a second apart both go to %q, and INIT overwrites the first", first)
	}
}

// backupDestDialog is a showing Back Up dialog over a fake connection, with
// its opening loads settled so their answers cannot land on the status line
// mid-test.
func backupDestDialog(t *testing.T, responses ...fakeResponse) (*App, *BackupDialog) {
	t.Helper()
	sc, _ := newFakeConn(t, responses...)
	return backupDialogOn(t, sc)
}

// backupDialogOn is backupDestDialog over a connection the caller built — for
// an edition other than the fake's default Developer.
func backupDialogOn(t *testing.T, sc *db.ServerConn) (*App, *BackupDialog) {
	t.Helper()
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewBackupDialog(a)
	quietLog(t)
	a.screen = nil
	d.show(sc, "AppDB")
	drainUntil(t, a, func() bool {
		return d.dbListRun.Idle() && d.defPaths.Idle() && d.compressDef.Idle()
	}, "the opening loads")
	return a, d
}

func fileExistResponse(fileExists, isDir int64) fakeResponse {
	return fakeResponse{match: "xp_fileexist", cols: 3,
		rows: [][]driver.Value{{fileExists, isDir, int64(1)}}}
}

const typedDest = `C:\Backup\AppDB_keep.bak`

func TestBackupAsksBeforeOverwritingAnExistingFile(t *testing.T) {
	a, d := backupDestDialog(t, fileExistResponse(1, 0))
	ran := 0
	check := func() {
		d.checkDestThen(gosmo.DiskTarget(typedDest), func() { ran++ })
		drainUntil(t, a, func() bool { return d.destCheck.Idle() && a.confirmDialog.Visible() }, "the overwrite question")
	}

	check()
	if ran != 0 {
		t.Fatal("the backup ran over an existing file without asking")
	}
	if !strings.Contains(a.confirmDialog.Message(), typedDest) {
		t.Errorf("the question does not name the file: %q", a.confirmDialog.Message())
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if ran != 0 {
		t.Error("Enter on the opening focus overwrote the file; No must be the default")
	}

	check()
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if ran != 1 {
		t.Errorf("Yes ran the backup %d times, want 1", ran)
	}
}

func TestBackupDestCheckOutcomes(t *testing.T) {
	for _, c := range []struct {
		name    string
		resp    fakeResponse
		wantRun bool
	}{
		{"missing file runs", fileExistResponse(0, 0), true},
		// xp_fileexist needs sysadmin, BACKUP does not: a refused probe must
		// not block a db_backupoperator.
		{"refused probe runs", fakeResponse{match: "xp_fileexist", err: errors.New("EXECUTE permission denied")}, true},
		{"folder is refused", fileExistResponse(0, 1), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, d := backupDestDialog(t, c.resp)
			ran := false
			d.checkDestThen(gosmo.DiskTarget(typedDest), func() { ran = true })
			drainUntil(t, a, d.destCheck.Idle, "the destination check")
			if ran != c.wantRun {
				t.Errorf("ran = %v, want %v (status %q)", ran, c.wantRun, d.status)
			}
			if a.confirmDialog.Visible() {
				t.Error("asked to overwrite a file that is not there")
			}
			if !c.wantRun && !d.statusErr {
				t.Errorf("refused with no error on the status line: %q", d.status)
			}
		})
	}
}

// A blob is not on the server's filesystem; probing it would only ever say
// "missing", so a URL destination is not probed at all.
func TestBackupToURLSkipsTheFileCheck(t *testing.T) {
	_, d := backupDestDialog(t)
	ran := false
	d.checkDestThen(gosmo.DiskTarget(blobDest), func() { ran = true })
	if !ran || !d.destCheck.Idle() {
		t.Errorf("URL destination: ran = %v, probe idle = %v; want it run at once, unprobed", ran, d.destCheck.Idle())
	}
}

// Closing the dialog while the probe is out must not start the backup when the
// answer lands.
func TestBackupHideAbandonsTheDestCheck(t *testing.T) {
	block := make(chan struct{})
	resp := fileExistResponse(0, 0)
	resp.block = block
	a, d := backupDestDialog(t, resp)
	ran := false
	d.checkDestThen(gosmo.DiskTarget(typedDest), func() { ran = true })
	d.Hide()
	close(block)
	if !d.destCheck.Idle() {
		t.Error("Hide left the destination check running")
	}
	waitAndDrain(t, a) // the probe always posts its answer; Done(tok) must drop it
	if ran {
		t.Error("a destination check that landed after Hide started the backup")
	}
}
