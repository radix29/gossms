package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// restoreFormWithOptions is restoreForm plus the option widgets a restore
// request snapshots.
func restoreFormWithOptions() *RestoreDialog {
	d := restoreForm()
	d.rbRecovery = widgets.NewRadioBox("Recovery Options:", []string{"WITH RECOVERY", "WITH NORECOVERY"})
	d.cbReplace = widgets.NewCheckBox("Replace")
	d.cbVerify = widgets.NewCheckBox("Verify")
	d.cbClose = widgets.NewCheckBox("Close")
	return d
}

// pickHistory puts hist behind the Backup History source and selects entry i.
func pickHistory(d *RestoreDialog, hist []*gosmo.BackupInfo, i int) {
	d.history = hist
	labels := make([]string, len(hist))
	for j := range hist {
		labels[j] = "set"
	}
	d.ddHistSet = widgets.NewDropDown("Backup Set: ", labels, 48)
	d.rbSource.SetSelected(1)
	d.ddHistSet.SetSelected(i)
}

// A backup striped over two files is one history entry, and the restore has
// to read every stripe: one alone is refused, "The media set has 2 media
// families but only 1 are provided". The entry used to be two, each naming a
// single stripe.
func TestSourceForRestoreNamesEveryStripe(t *testing.T) {
	d := restoreForm()
	striped := &gosmo.BackupInfo{DeviceName: `E:\b\s1.bak`, Devices: []string{`E:\b\s1.bak`, `E:\b\s2.bak`}, Position: 1}
	pickHistory(d, []*gosmo.BackupInfo{striped}, 0)

	src := d.sourceForRestore()
	if !slices.Equal(src.devices, striped.Devices) {
		t.Errorf("devices = %q, want both stripes %q", src.devices, striped.Devices)
	}
	if got := len(src.targets()); got != 2 {
		t.Errorf("targets() has %d entries, want 2", got)
	}
	if got, want := sourceLabel(src.devices), "s1.bak (2 files)"; got != want {
		t.Errorf("sourceLabel = %q, want %q", got, want)
	}
	if got, want := sourceLabel([]string{`E:\b\one.bak`}), "one.bak"; got != want {
		t.Errorf("sourceLabel of one file = %q, want %q", got, want)
	}
	// The dropdown clips from the right, so there the count leads.
	if got, want := historyDeviceLabel(src.devices), "[2 files] s1.bak"; got != want {
		t.Errorf("historyDeviceLabel = %q, want %q", got, want)
	}
	if got, want := historyDeviceLabel([]string{`E:\b\one.bak`}), "one.bak"; got != want {
		t.Errorf("historyDeviceLabel of one file = %q, want %q", got, want)
	}
}

// The set number a restore sends. A history entry carries its own position —
// on a file written with NOINIT, leaving the clause off restores set 1, the
// oldest backup in it, whichever entry was picked. The inspect view's set
// counts only for the devices it was read from: after Analyze of file A at
// set 3, a history entry on file B used to be restored WITH FILE = 3.
func TestFileNumberForFollowsTheSource(t *testing.T) {
	a := restoreSource{devices: []string{`E:\b\a.bak`}}
	b3 := restoreSource{devices: []string{`E:\b\b.bak`}, position: 3}
	b1 := restoreSource{devices: []string{`E:\b\b.bak`}, position: 1}

	d := &RestoreDialog{}
	if got := d.fileNumberFor(b3); got != 3 {
		t.Errorf("history entry at position 3, nothing analyzed: %d, want 3", got)
	}
	if got := d.fileNumberFor(b1); got != 0 {
		t.Errorf("history entry at position 1: %d, want 0 (no clause reads set 1)", got)
	}

	// Analyze file A and move to its third set.
	d.headers = []*gosmo.BackupHeader{hdr(1, "AppDB"), hdr(2, "AppDB"), hdr(3, "AppDB")}
	d.inspectDevs = a.devices
	d.headerIdx = 2
	if got := d.fileNumberFor(a); got != 3 {
		t.Errorf("the analyzed file at its third set: %d, want 3", got)
	}
	if got := d.fileNumberFor(b1); got != 0 {
		t.Errorf("history entry on another file after analyzing A at set 3: %d, want 0", got)
	}
	if got := d.fileNumberFor(restoreSource{devices: []string{`E:\b\b.bak`}}); got != 0 {
		t.Errorf("a typed path other than the analyzed one: %d, want 0", got)
	}
	// One stripe of a set is not the set.
	d.inspectDevs = []string{`E:\b\a.bak`, `E:\b\a2.bak`}
	if got := d.fileNumberFor(a); got != 0 {
		t.Errorf("a source naming part of the analyzed devices: %d, want 0", got)
	}
}

// Analyze on a history entry opens on that entry's set, not on set 1: the
// entry picked is the backup the user meant, and the inspect view's set is
// what the restore then sends.
func TestHeaderAtFindsTheHistoryEntrysSet(t *testing.T) {
	headers := []*gosmo.BackupHeader{hdr(1, "AppDB"), hdr(2, "AppDB"), hdr(3, "Other")}
	for _, tc := range []struct{ position, want int }{{0, 0}, {1, 0}, {3, 2}, {9, 0}} {
		if got := headerAt(headers, tc.position); got != tc.want {
			t.Errorf("headerAt(position %d) = %d, want %d", tc.position, got, tc.want)
		}
	}
	if got := headerAt(nil, 2); got != 0 {
		t.Errorf("headerAt(no headers) = %d, want 0", got)
	}
}

// fileListOnlyResponse scripts RESTORE FILELISTONLY, by column name as gosmo
// scans it.
func fileListOnlyResponse(rows ...[]driver.Value) fakeResponse {
	names := []string{"LogicalName", "PhysicalName", "Type", "FileGroupName", "Size", "MaxSize"}
	return fakeResponse{match: "RESTORE FILELISTONLY", cols: len(names), colNames: names, rows: rows}
}

// The statement a restore from a striped, appended history entry builds:
// every read and the RESTORE itself name both stripes, and the set is the
// entry's own position.
func TestBuildRestoreOptionsForAStripedHistoryEntry(t *testing.T) {
	finish := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	sc, inst := newFakeConn(t,
		headerOnlyResponse(
			[]driver.Value{"full", int64(1), int64(1), "AppDB", "SRV", finish, int64(1024)},
			[]driver.Value{"log", int64(2), int64(2), "AppDB", "SRV", finish, int64(1024)},
			[]driver.Value{"full2", int64(1), int64(3), "AppDB", "SRV", finish, int64(1024)},
		),
		fileListOnlyResponse(
			[]driver.Value{"AppDB", `D:\SQL\AppDB.mdf`, "D", "PRIMARY", int64(1), int64(0)},
			[]driver.Value{"AppDB_log", `D:\SQL\AppDB_log.ldf`, "L", "", int64(1), int64(0)},
		),
	)
	d := restoreFormWithOptions()
	// The dialog's connection, so relocAuto's default directories are the
	// server's (restoreRequest snapshots them through defaultDirs).
	d.sc = sc
	entry := &gosmo.BackupInfo{DeviceName: `E:\b\s1.bak`, Devices: []string{`E:\b\s1.bak`, `E:\b\s2.bak`}, Position: 3}
	pickHistory(d, []*gosmo.BackupInfo{entry}, 0)
	d.rbReloc.SetSelected(relocAuto)

	req := d.restoreRequest(d.sourceForRestore(), "AppDB_Copy")
	ropts, err := buildRestoreOptions(context.Background(), sc.Server, req)
	if err != nil {
		t.Fatalf("buildRestoreOptions: %v", err)
	}
	stmt, err := sc.Server.BuildRestoreStatement(ropts)
	if err != nil {
		t.Fatalf("BuildRestoreStatement: %v", err)
	}
	for _, want := range []string{
		`FROM DISK = N'E:\b\s1.bak', DISK = N'E:\b\s2.bak'`,
		"FILE = 3",
		`MOVE N'AppDB' TO N'C:\Data\AppDB_Copy_AppDB.mdf'`,
	} {
		if !strings.Contains(stmt, want) {
			t.Errorf("restore statement lacks %q:\n%s", want, stmt)
		}
	}
	files := inst.Reads("RESTORE FILELISTONLY")
	if len(files) != 1 || !strings.Contains(files[0], `DISK = N'E:\b\s2.bak'`) || !strings.Contains(files[0], "FILE = 3") {
		t.Errorf("file list read = %q, want both stripes at set 3", files)
	}
}

// The restore is a Task that outlives Hide, so the dialog can be reopened on
// another server — show() assigning d.sc — while the first restore is still
// verifying. Everything after that point used to be read through d.sc: the
// headers, the file list and the default directories came from the second
// server while the RESTORE ran on the first. Run it with -race: the old
// reads were also a data race on d.sc.
func TestRestoreKeepsTheServerItStartedOn(t *testing.T) {
	finish := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	verifying := make(chan struct{})
	stop := errors.New("stop before the RESTORE")
	sc1, inst1 := newFakeConn(t,
		fakeResponse{match: "RESTORE VERIFYONLY", blockExec: verifying},
		headerOnlyResponse([]driver.Value{"full", int64(1), int64(1), "AppDB", "SRV", finish, int64(1024)}),
		fakeResponse{match: "RESTORE FILELISTONLY", err: stop},
	)
	sc2, inst2 := newFakeConn(t)

	app := newTestApp()
	d := restoreFormWithOptions()
	d.app, d.sc = app, sc1
	d.fFile.SetValue(`E:\b\app.bak`)
	d.cbVerify.SetChecked(true)
	d.rbReloc.SetSelected(relocAuto)

	d.beginRestore(d.sourceForRestore(), "AppDB_Copy")
	deadline := time.Now().Add(5 * time.Second)
	for len(inst1.Statements()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the restore never reached VERIFYONLY")
		}
		time.Sleep(time.Millisecond)
	}
	d.sc = sc2 // what show(sc2) does
	close(verifying)

	for !d.task.Done {
		if time.Now().After(deadline) {
			t.Fatal("the restore task never finished")
		}
		app.drainPending()
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(d.task.Err, stop) {
		t.Errorf("task error = %v, want the scripted file-list failure", d.task.Err)
	}
	if got := inst1.Reads("RESTORE"); len(got) != 2 {
		t.Errorf("the first server got %d RESTORE reads, want HEADERONLY and FILELISTONLY: %q", len(got), got)
	}
	if got := inst2.Reads("RESTORE"); len(got) != 0 {
		t.Errorf("the server the dialog was reopened on got the restore's reads: %q", got)
	}
}
