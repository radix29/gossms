package tui

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// planHistoryRows is AppDB's msdb history as BackupHistory reads it, newest
// first: a full at 01:00, a differential on it at 03:00, a log at 04:00 that
// continues the chain past the differential, and an older log the
// differential already covers.
func planHistoryRows() fakeResponse {
	at := func(h int) time.Time { return time.Date(2026, 10, 27, h, 0, 0, 0, time.UTC) }
	row := func(typ string, h int, device string, setID int64, first, last, guid, base string) []driver.Value {
		return []driver.Value{"AppDB", "", "", typ, at(h), at(h), int64(1024), device, "sa", "SRV",
			int64(957), int64(160), int64(1), setID, setID, int64(1), false, first, last, guid, base}
	}
	return fakeResponse{match: "msdb.dbo.backupset", cols: 21, rows: [][]driver.Value{
		row("L", 4, `E:\b\log2.trn`, 4, "200", "400", "g4", ""),
		row("I", 3, `E:\b\diff.bak`, 3, "300", "320", "g3", "g1"),
		row("L", 2, `E:\b\log1.trn`, 2, "90", "200", "g2", ""),
		row("D", 1, `E:\b\full.bak`, 1, "100", "120", "g1", ""),
	}}
}

// headerOn is RESTORE HEADERONLY's answer for device alone: one set of
// backupType (1 full, 2 log, 5 differential).
func headerOn(device string, backupType int64) fakeResponse {
	r := headerOnlyResponse([]driver.Value{"set", backupType, int64(1), "AppDB", "SRV", time.Now(), int64(1024)})
	r.match = "RESTORE HEADERONLY FROM DISK = N'" + device + "'"
	return r
}

// planForm is a restore form with AppDB's history loaded and the plan
// entry picked.
func planForm(t *testing.T) *RestoreDialog {
	t.Helper()
	d := restoreFormWithOptions()
	d.ddHistDB = widgets.NewDropDown("Database:   ", []string{"AppDB"}, 40)
	d.cbTail = widgets.NewCheckBox("Tail-log backup first")
	d.cbClose.SetChecked(true)
	d.rbReloc.SetSelected(relocAuto)
	at := func(h int) time.Time { return time.Date(2026, 10, 27, h, 0, 0, 0, time.UTC) }
	d.allHistory = []*gosmo.BackupInfo{
		{BackupSetID: 4, SetType: gosmo.BackupSetLog, BackupFinish: at(4), DeviceName: `E:\b\log2.trn`, Devices: []string{`E:\b\log2.trn`}, FirstLSN: "200", LastLSN: "400"},
		{BackupSetID: 3, SetType: gosmo.BackupSetDifferential, BackupFinish: at(3), DeviceName: `E:\b\diff.bak`, Devices: []string{`E:\b\diff.bak`}, FirstLSN: "300", LastLSN: "320", DifferentialBaseGUID: "g1"},
		{BackupSetID: 2, SetType: gosmo.BackupSetLog, BackupFinish: at(2), DeviceName: `E:\b\log1.trn`, Devices: []string{`E:\b\log1.trn`}, FirstLSN: "90", LastLSN: "200"},
		{BackupSetID: 1, SetType: gosmo.BackupSetDatabase, BackupFinish: at(1), DeviceName: `E:\b\full.bak`, Devices: []string{`E:\b\full.bak`}, FirstLSN: "100", LastLSN: "120", BackupSetGUID: "g1"},
	}
	d.planOffered = true
	pickHistory(d, d.allHistory, 0)
	d.ddHistSet = widgets.NewDropDown("Backup Set: ", []string{planEntryLabel, "a", "b", "c", "d"}, 48)
	d.ddHistSet.SetSelected(0)
	return d
}

// The plan's script: the tail-log backup first, WITH NORECOVERY, then the
// full, the differential and the log after it — each WITH NORECOVERY — and the
// tail log last, WITH RECOVERY. REPLACE and the connection-closing batch go
// with the first RESTORE only, and the log the differential covers is left out.
func TestRestorePlanScript(t *testing.T) {
	sc, _ := newFakeConn(t,
		planHistoryRows(),
		headerOn(`E:\b\full.bak`, 1),
		headerOn(`E:\b\diff.bak`, 5),
		headerOn(`E:\b\log2.trn`, 2),
	)
	d := planForm(t)
	d.sc = sc
	d.cbReplace.SetChecked(true)
	d.cbTail.SetChecked(true)
	if !d.planPicked() || !d.checkPlanFields("AppDB") {
		t.Fatalf("plan not picked or refused: %s", d.status)
	}

	req := d.restoreRequest(d.sourceForRestore(), "AppDB")
	if req.plan == nil || !strings.HasPrefix(req.plan.tailDevice, `C:\Backup\AppDB_LogBackup_`) {
		t.Fatalf("plan request = %+v, want a tail log in the default backup folder", req.plan)
	}
	script, err := planScript(t.Context(), sc.Server, req)
	if err != nil {
		t.Fatalf("planScript: %v", err)
	}
	stmts := strings.Split(script, "\nGO\n")
	stmts = stmts[:len(stmts)-1] // after the last GO
	want := []struct{ has, with string }{
		{"BACKUP LOG [AppDB] TO DISK = N'" + req.plan.tailDevice + "'", "NORECOVERY"},
		{"RESTORE DATABASE [AppDB]\nFROM DISK = N'E:\\b\\full.bak'", "NORECOVERY"},
		{"RESTORE DATABASE [AppDB]\nFROM DISK = N'E:\\b\\diff.bak'", "NORECOVERY"},
		{"RESTORE LOG [AppDB]\nFROM DISK = N'E:\\b\\log2.trn'", "NORECOVERY"},
		{"RESTORE LOG [AppDB]\nFROM DISK = N'" + req.plan.tailDevice + "'", "RECOVERY"},
	}
	if len(stmts) != len(want) {
		t.Fatalf("script has %d batches, want %d:\n%s", len(stmts), len(want), script)
	}
	for i, w := range want {
		if !strings.Contains(stmts[i], w.has) || !strings.Contains(stmts[i], w.with) {
			t.Errorf("batch %d lacks %q with %s:\n%s", i, w.has, w.with, stmts[i])
		}
		first := i == 1
		if got := strings.Contains(stmts[i], "REPLACE"); got != first {
			t.Errorf("batch %d: REPLACE %v, want %v", i, got, first)
		}
		if got := strings.Contains(stmts[i], "SINGLE_USER"); got != first {
			t.Errorf("batch %d: closes connections %v, want %v", i, got, first)
		}
	}
	if strings.Contains(stmts[4], "NORECOVERY") {
		t.Errorf("the last RESTORE is WITH NORECOVERY:\n%s", stmts[4])
	}
	if strings.Contains(script, "log1.trn") {
		t.Errorf("the plan restores the log the differential covers:\n%s", script)
	}
}

// A point in time goes on the logs only — a full or differential read with
// STOPAT fails or ignores it — and stops the plan at the log holding it.
func TestRestorePlanScriptStopAt(t *testing.T) {
	sc, _ := newFakeConn(t,
		planHistoryRows(),
		headerOn(`E:\b\full.bak`, 1),
		headerOn(`E:\b\log1.trn`, 2),
	)
	d := planForm(t)
	d.sc = sc
	d.fStopAt.SetValue("2026-10-27 01:30")
	req := d.restoreRequest(d.sourceForRestore(), "AppDB")
	script, err := planScript(t.Context(), sc.Server, req)
	if err != nil {
		t.Fatalf("planScript: %v", err)
	}
	stmts := strings.Split(script, "\nGO\n")
	if len(stmts) != 3 || !strings.Contains(stmts[0], "full.bak") || !strings.Contains(stmts[1], "log1.trn") {
		t.Fatalf("script is not the full then log1:\n%s", script)
	}
	if strings.Contains(stmts[0], "STOPAT") || !strings.Contains(stmts[1], "STOPAT = '2026-10-27T01:30:00.000'") {
		t.Errorf("STOPAT is not on the log alone:\n%s", script)
	}
}

// A tail-log backup leaves its database RESTORING, so it is refused for a
// restore into another database — before anything runs.
func TestRestorePlanRefusesATailLogForAnotherTarget(t *testing.T) {
	sc, _ := newFakeConn(t)
	d := planForm(t)
	d.sc = sc
	d.cbTail.SetChecked(true)
	if d.checkPlanFields("AppDB_Copy") {
		t.Fatal("a tail log of AppDB restored into AppDB_Copy was not refused")
	}
	if !d.statusErr || !strings.Contains(d.status, "RESTORING") {
		t.Errorf("status = %q, want the RESTORING refusal", d.status)
	}
	d.cbTail.SetChecked(false)
	if !d.checkPlanFields("AppDB_Copy") {
		t.Errorf("a plan into AppDB_Copy without a tail log was refused: %s", d.status)
	}
}

// The status line describes the plan, and Analyze/Files read its full.
func TestRestorePlanPreview(t *testing.T) {
	sc, _ := newFakeConn(t)
	d := planForm(t)
	d.sc = sc
	if got, want := d.restingStatus(), "Restore plan: full 2026-10-27 01:00, differential 2026-10-27 03:00, 1 log."; got != want {
		t.Errorf("status = %q, want %q", got, want)
	}
	if src := d.sourceForRestore(); len(src.devices) != 1 || src.devices[0] != `E:\b\full.bak` {
		t.Errorf("source = %+v, want the plan's full", src)
	}
	d.fStopAt.SetValue("2026-10-27 05:00")
	if got := d.restingStatus(); !strings.Contains(got, "tail-log") {
		t.Errorf("status past the chain = %q, want the tail-log refusal", got)
	}
	d.cbTail.SetChecked(true)
	if got := d.restingStatus(); !strings.HasSuffix(got, "1 log, the tail log.") {
		t.Errorf("status with the tail = %q", got)
	}
	// An entry below the plan entry is the history entry above it in the list.
	d.ddHistSet.SetSelected(2)
	if d.planPicked() || d.historyPick() != d.allHistory[1] {
		t.Errorf("entry 2 picks %+v, want history[1]", d.historyPick())
	}
}

// The plan is read before the tail-log backup: a plan that cannot run must
// not leave the database RESTORING.
func TestRestorePlanReadsHistoryBeforeTheTailLog(t *testing.T) {
	broken := errors.New("history unreadable")
	sc, inst := newFakeConn(t, fakeResponse{match: "msdb.dbo.backupset", err: broken})
	app := newTestApp()
	d := planForm(t)
	d.app, d.sc = app, sc
	d.cbTail.SetChecked(true)

	d.beginRestore(d.sourceForRestore(), "AppDB")
	deadline := time.Now().Add(5 * time.Second)
	for !d.task.Done {
		if time.Now().After(deadline) {
			t.Fatal("the restore task never finished")
		}
		app.drainPending()
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(d.task.Err, broken) {
		t.Errorf("task error = %v, want the history failure", d.task.Err)
	}
	for _, q := range append(inst.Statements(), inst.Reads("BACKUP")...) {
		if strings.Contains(q, "BACKUP LOG") {
			t.Errorf("the tail log was backed up although the plan failed: %s", q)
		}
	}
}

// A point past the newest log in history is the tail log's: the plan runs to
// the end of the chain and the tail is restored to the point.
func TestRestorePlanScriptStopAtInTheTail(t *testing.T) {
	sc, _ := newFakeConn(t,
		planHistoryRows(),
		headerOn(`E:\b\full.bak`, 1),
		headerOn(`E:\b\diff.bak`, 5),
		headerOn(`E:\b\log2.trn`, 2),
	)
	d := planForm(t)
	d.sc = sc
	d.cbTail.SetChecked(true)
	d.fStopAt.SetValue("2026-10-27 05:00")
	req := d.restoreRequest(d.sourceForRestore(), "AppDB")
	script, err := planScript(t.Context(), sc.Server, req)
	if err != nil {
		t.Fatalf("planScript: %v", err)
	}
	stmts := strings.Split(script, "\nGO\n")
	tail := stmts[len(stmts)-2]
	if !strings.Contains(tail, "RESTORE LOG") || !strings.Contains(tail, req.plan.tailDevice) ||
		!strings.Contains(tail, "STOPAT = '2026-10-27T05:00:00.000'") {
		t.Errorf("the tail log is not restored to the point:\n%s", tail)
	}
}
