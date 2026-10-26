package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
	gosmo "github.com/radix29/gosmo"
)

// restoreDialogOnScreen shows a Restore dialog for AppDB on a 100x40 mock
// screen, with no connection.
func restoreDialogOnScreen(t *testing.T) (*RestoreDialog, tcell.Screen) {
	t.Helper()
	s, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: 100, Y: 40}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	a := newTestApp()
	a.screen = s
	d := NewRestoreDialog(a)
	d.show(nil, "AppDB")
	return d, s
}

// screenRow is row y of s between columns x0 and x1.
func screenRow(s tcell.Screen, y, x0, x1 int) string {
	var b strings.Builder
	for x := x0; x < x1; x++ {
		str, _, _ := s.Get(x, y)
		b.WriteString(str)
	}
	return b.String()
}

// Each Recovery Options entry maps to its own state, pinned by the label drawn
// on screen: a round trip through recoveryStates alone would agree with two
// swapped entries.
func TestRecoveryOptionsMapByLabel(t *testing.T) {
	d, s := restoreDialogOnScreen(t)
	want := map[string]gosmo.RestoreRecovery{
		"WITH RECOVERY":   gosmo.RestoreWithRecovery,
		"WITH NORECOVERY": gosmo.RestoreWithNoRecovery,
		"WITH STANDBY":    gosmo.RestoreWithStandBy,
	}
	if n := d.rbRecovery.Height() - 1; n != len(recoveryStates) || n != len(want) {
		t.Fatalf("radio has %d options, recoveryStates %d, want %d", n, len(recoveryStates), len(want))
	}
	d.Draw(s)
	r := d.Rect()
	firstOption := d.fStopAt.RectY() - 5 // the label, three options, a blank, the standby row
	for i := range recoveryStates {
		// The option's own text, before the checkbox column beside it.
		row := screenRow(s, firstOption+i, r.X, d.InnerRect().X+1+restoreOptionsColX)
		label := strings.TrimSpace(row[strings.Index(row, ")")+1:])
		label = strings.TrimSuffix(label, "│")
		label = strings.TrimSpace(label)
		w, ok := want[label]
		if !ok {
			t.Errorf("option %d reads %q, not a known label", i, row)
			continue
		}
		d.rbRecovery.SetSelected(i)
		d.fStandby.SetValue(`E:\b\undo.bak`)
		req := d.restoreRequest(restoreSource{devices: []string{`E:\b\app.bak`}}, "AppDB")
		if req.recovery != w {
			t.Errorf("%s: recovery = %q, want %q", label, req.recovery, w)
		}
		// The undo file goes only with STANDBY: gosmo refuses it with anything else.
		if (req.standbyFile != "") != (w == gosmo.RestoreWithStandBy) {
			t.Errorf("%s: standby file = %q", label, req.standbyFile)
		}
	}
}

func TestParseStopAt(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // formatted as gosmo sends it; "" for nil
		bad  bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"2026-10-26 14:30:05", "2026-10-26T14:30:05.000", false},
		{" 2026-10-26T14:30:05 ", "2026-10-26T14:30:05.000", false},
		{"2026-10-26 14:30:05.25", "2026-10-26T14:30:05.250", false},
		{"2026-10-26 14:30", "2026-10-26T14:30:00.000", false},
		{"2026-10-26", "", true},
		{"26/10/2026 14:30", "", true},
		{"2026-13-01 00:00:00", "", true},
	} {
		got, err := parseStopAt(tc.in)
		if (err != nil) != tc.bad {
			t.Errorf("parseStopAt(%q) error = %v, want error %v", tc.in, err, tc.bad)
			continue
		}
		var s string
		if got != nil {
			s = got.Format("2006-01-02T15:04:05.000")
		}
		if s != tc.want {
			t.Errorf("parseStopAt(%q) = %q, want %q", tc.in, s, tc.want)
		}
	}
}

// A log set restored WITH STANDBY to a point in time: the statement carries
// the undo file and STOPAT as typed, the server's wall clock, with no zone
// shift.
func TestBuildRestoreOptionsStandbyToAPointInTime(t *testing.T) {
	finish := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	sc, _ := newFakeConn(t,
		headerOnlyResponse(
			[]driver.Value{"full", int64(1), int64(1), "AppDB", "SRV", finish, int64(1024)},
			[]driver.Value{"log", int64(2), int64(2), "AppDB", "SRV", finish, int64(1024)},
		),
	)
	d := restoreFormWithOptions()
	d.sc = sc
	full := &gosmo.BackupInfo{DeviceName: `E:\b\app.bak`, Position: 1, SetType: gosmo.BackupSetDatabase}
	log := &gosmo.BackupInfo{DeviceName: `E:\b\app.bak`, Position: 2, SetType: gosmo.BackupSetLog}
	pickHistory(d, []*gosmo.BackupInfo{full, log}, 1)
	d.rbRecovery.SetSelected(recoverStandby)
	d.fStandby.SetValue(` E:\b\ROLLBACK_UNDO_AppDB.BAK `)
	d.fStopAt.SetValue("2026-09-24 09:45:30.5")
	if !d.checkRecoveryFields() {
		t.Fatalf("valid fields refused: %s", d.status)
	}

	req := d.restoreRequest(d.sourceForRestore(), "AppDB")
	ropts, err := buildRestoreOptions(context.Background(), sc.Server, req)
	if err != nil {
		t.Fatalf("buildRestoreOptions: %v", err)
	}
	stmt, err := sc.Server.BuildRestoreStatement(ropts)
	if err != nil {
		t.Fatalf("BuildRestoreStatement: %v", err)
	}
	for _, want := range []string{
		"RESTORE LOG [AppDB]",
		"FILE = 2",
		`STANDBY = N'E:\b\ROLLBACK_UNDO_AppDB.BAK'`,
		"STOPAT = '2026-09-24T09:45:30.500'",
	} {
		if !strings.Contains(stmt, want) {
			t.Errorf("restore statement lacks %q:\n%s", want, stmt)
		}
	}
	if strings.Contains(stmt, "NORECOVERY") || strings.Contains(stmt, "\n     RECOVERY") {
		t.Errorf("a STANDBY restore carries another recovery clause:\n%s", stmt)
	}
}

// A point in time on a full set is refused once the header says what the set
// is, before the file list or Verify: a STOPAT there is not the point-in-time
// restore the user asked for.
func TestRestoreRefusesAPointInTimeOnAFullSet(t *testing.T) {
	finish := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	sc, inst := newFakeConn(t,
		headerOnlyResponse([]driver.Value{"full", int64(1), int64(1), "AppDB", "SRV", finish, int64(1024)}),
		fileListOnlyResponse(
			[]driver.Value{"AppDB", `D:\SQL\AppDB.mdf`, "D", "PRIMARY", int64(1), int64(0)},
		),
	)
	d := restoreFormWithOptions()
	d.sc = sc
	d.fFile.SetValue(`E:\b\app.bak`)
	d.rbReloc.SetSelected(relocAuto)
	d.fStopAt.SetValue("2026-09-24 09:45:30")

	_, err := buildRestoreOptions(context.Background(), sc.Server, d.restoreRequest(d.sourceForRestore(), "AppDB_Copy"))
	if err == nil || !strings.Contains(err.Error(), "transaction log backup") || !strings.Contains(err.Error(), "database backup") {
		t.Fatalf("err = %v, want the point-in-time refusal naming the set's type", err)
	}
	if got := inst.Reads("RESTORE FILELISTONLY"); len(got) != 0 {
		t.Errorf("the file list was read after the refusal: %q", got)
	}

	// Without the point in time the same set restores.
	d.fStopAt.SetValue("")
	if _, err := buildRestoreOptions(context.Background(), sc.Server, d.restoreRequest(d.sourceForRestore(), "AppDB_Copy")); err != nil {
		t.Errorf("full set without a point in time: %v", err)
	}
}

// STANDBY with no undo file and an unparsable point in time are refused on
// the form, with the field to fix focused, even when Restore was pressed in
// the inspect view, which draws no status line.
func TestCheckRecoveryFieldsRefusesOnTheForm(t *testing.T) {
	d, _ := restoreDialogOnScreen(t)

	d.rbRecovery.SetSelected(recoverStandby)
	d.syncSourceState()
	d.fStandby.SetValue("  ")
	d.mode = restoreModeInspect
	if d.checkRecoveryFields() {
		t.Fatal("STANDBY with a blank undo file passed")
	}
	if d.mode != restoreModeForm || !d.statusErr || !strings.Contains(d.status, "standby file") {
		t.Errorf("mode %d, status %q (err %v), want the form showing the refusal", d.mode, d.status, d.statusErr)
	}
	if d.focusable[d.focusIdx] != d.fStandby {
		t.Error("the standby file field is not focused")
	}

	d.fStandby.SetValue(`E:\b\undo.bak`)
	d.fStopAt.SetValue("yesterday")
	if d.checkRecoveryFields() {
		t.Fatal("an unparsable point in time passed")
	}
	if !strings.HasPrefix(d.status, "Point in time:") || d.focusable[d.focusIdx] != d.fStopAt {
		t.Errorf("status %q, focused %T, want the point-in-time refusal on its field", d.status, d.focusable[d.focusIdx])
	}

	d.fStopAt.SetValue("")
	if !d.checkRecoveryFields() {
		t.Errorf("valid fields refused: %s", d.status)
	}
}

// The standby file is in the Tab ring, and takes clicks, only while WITH
// STANDBY is picked; a hidden field left in the ring takes invisible keys.
func TestStandbyFieldFollowsTheRecoveryOption(t *testing.T) {
	d, _ := restoreDialogOnScreen(t)
	inRing := func() bool { return indexOfFocusable(d.focusable, d.fStandby) >= 0 }

	if inRing() {
		t.Fatal("the standby file is in the ring under WITH RECOVERY")
	}
	if indexOfFocusable(d.focusable, d.fStopAt) < 0 {
		t.Fatal("the point in time is not in the ring")
	}
	d.focusTo(d.rbRecovery)
	for range recoverStandby {
		d.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	if d.rbRecovery.Selected() != recoverStandby || !inRing() {
		t.Fatalf("after arrowing to WITH STANDBY: selected %d, in ring %v", d.rbRecovery.Selected(), inRing())
	}
	if d.focusable[d.focusIdx] != d.rbRecovery {
		t.Error("focus left the recovery options when the ring grew")
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone))
	if inRing() {
		t.Error("the standby file stayed in the ring after leaving WITH STANDBY")
	}
}

// The form's new rows fit: the point-in-time row ends above the status line's
// two rows, the standby field and the hint end inside the right border, and the
// checkbox column clears the recovery options beside it.
func TestRestoreRecoveryRowsFitTheForm(t *testing.T) {
	d, s := restoreDialogOnScreen(t)
	d.rbRecovery.SetSelected(recoverStandby)
	d.syncSourceState()
	d.Draw(s)

	if y, top := d.fStopAt.RectY(), d.ButtonRowY()-3; y >= top {
		t.Errorf("point-in-time row %d reaches the status line at %d", y, top)
	}
	r := d.Rect()
	border := r.X + r.W - 1
	for _, y := range []int{d.fStandby.RectY(), d.fStopAt.RectY()} {
		if got := lastNonSpace(t, s, y, border); got >= border-1 {
			t.Errorf("row %d runs to column %d, against the border at %d", y, got, border)
		}
		if str, _, _ := s.Get(border, y); str != "│" {
			t.Errorf("border cell on row %d = %q", y, str)
		}
	}
	if cell, _, _ := s.Get(lastNonSpace(t, s, d.fStandby.RectY(), border), d.fStandby.RectY()); cell != "]" {
		t.Errorf("standby row ends %q, want ]", cell)
	}
	rowText := func(y int) string { return screenRow(s, y, r.X, r.X+r.W) }
	if got := rowText(d.fStopAt.RectY()); !strings.Contains(got, stopAtHint) {
		t.Errorf("point-in-time row lacks its hint: %q", got)
	}
	if got := rowText(d.fStopAt.RectY() - 5); !strings.Contains(got, "WITH RECOVERY") || !strings.Contains(got, "Replace existing") {
		t.Errorf("RECOVERY row does not hold the Replace checkbox beside it: %q", got)
	}
}
