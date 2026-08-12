package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// hdr builds a backup header at a given device position.
func hdr(pos int, db string) *gosmo.BackupHeader {
	return &gosmo.BackupHeader{Position: pos, DatabaseName: db}
}

// TestRestoreFileNumberSingleSet pins that a device holding one backup set
// emits no WITH FILE clause — SQL Server's own default is the first set, so
// the clause would be noise on the overwhelmingly common case.
func TestRestoreFileNumberSingleSet(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(1, "AppDB")}}
	if got := d.restoreFileNumber(); got != 0 {
		t.Errorf("restoreFileNumber() = %d, want 0 for a single-set device", got)
	}
}

// TestRestoreFileNumberFollowsSelection is the regression test for the
// shipped behaviour: the dialog always restored backup set 1 no matter which
// set the inspect view was showing, because RestoreOptions had no FILE = n
// and buildRestoreOptions read headers[0] unconditionally. A .bak written
// with NOINIT (full at 1, differential at 2) could not restore the
// differential at all.
func TestRestoreFileNumberFollowsSelection(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{
		hdr(1, "AppDB"), hdr(2, "AppDB"), hdr(3, "AppDB"),
	}}
	for _, want := range []int{1, 2, 3} {
		d.headerIdx = want - 1
		if got := d.restoreFileNumber(); got != want {
			t.Errorf("headerIdx %d: restoreFileNumber() = %d, want %d", d.headerIdx, got, want)
		}
	}
}

// TestRestoreFileNumberUsesPositionNotIndex pins that the number sent to SQL
// Server is the set's own Position, not its slice index. The two only
// coincide when the device's sets are contiguous from 1.
func TestRestoreFileNumberUsesPositionNotIndex(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(4, "AppDB"), hdr(7, "AppDB")}}
	d.headerIdx = 1
	if got := d.restoreFileNumber(); got != 7 {
		t.Errorf("restoreFileNumber() = %d, want 7 (the header's Position, not index+1)", got)
	}
}

// TestSelectHeaderClamps pins that arrowing past either end stays put rather
// than wrapping — holding an arrow must not cycle past the intended set.
func TestSelectHeaderClamps(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(1, "A"), hdr(2, "A")}}
	d.selectHeader(-5)
	if d.headerIdx != 0 {
		t.Errorf("selectHeader(-5) = %d, want 0", d.headerIdx)
	}
	d.selectHeader(99)
	if d.headerIdx != 1 {
		t.Errorf("selectHeader(99) = %d, want 1", d.headerIdx)
	}
}

// TestSelectedHeaderSurvivesAStaleIndex pins the guard: headerIdx is UI
// state and headers is replaced by every Analyze, so a stale index must fall
// back rather than panic.
func TestSelectedHeaderSurvivesAStaleIndex(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(1, "AppDB")}, headerIdx: 9}
	if got := d.selectedHeader(); got == nil || got.DatabaseName != "AppDB" {
		t.Errorf("selectedHeader() = %v, want the first header", got)
	}
}

// TestSelectedHeaderOnNoHeaders pins the nil return. The stale-index guard
// above used to fall back to headers[0], which panics when there is no
// headers[0] — on the UI goroutine, where recoverPanic cannot catch it.
func TestSelectedHeaderOnNoHeaders(t *testing.T) {
	d := &RestoreDialog{}
	if got := d.selectedHeader(); got != nil {
		t.Errorf("selectedHeader() on no headers = %v, want nil", got)
	}
	d.selectHeader(3) // must not leave an index selectedHeader would index with
	if got := d.selectedHeader(); got != nil {
		t.Errorf("selectedHeader() after selectHeader = %v, want nil", got)
	}
	if got := d.restoreFileNumber(); got != 0 {
		t.Errorf("restoreFileNumber() on no headers = %d, want 0", got)
	}
}

// The Files Included panel, the MOVE clauses and the RESTORE itself all name
// a backup set, and all three have to name the *same* one — a file list read
// from a different set describes logical files the restored set does not
// contain, and SQL Server rejects the whole statement. They agree by
// construction only as long as every path derives the number from
// backupSetNumber, so the rule is pinned here directly.
//
// The index bounds matter as much as the values: analyze asks for set 0 on a
// header slice it has just fetched, while restoreFileNumber asks for
// headerIdx, which is UI state left over from the previous device.
func TestBackupSetNumber(t *testing.T) {
	one := []*gosmo.BackupHeader{hdr(1, "AppDB")}
	three := []*gosmo.BackupHeader{hdr(1, "AppDB"), hdr(2, "AppDB"), hdr(3, "Other")}
	gappy := []*gosmo.BackupHeader{hdr(4, "AppDB"), hdr(7, "AppDB")}
	unnumbered := []*gosmo.BackupHeader{hdr(0, "AppDB"), hdr(0, "AppDB")}

	for _, tc := range []struct {
		what    string
		headers []*gosmo.BackupHeader
		i       int
		want    int
	}{
		{"no headers at all", nil, 0, 0},
		{"no headers, stale index", nil, 5, 0},
		// A lone set is always at position 1 (appending numbers 1..n; INIT and
		// FORMAT reinitialise to a single set at 1), so omitting the clause
		// cannot target the wrong set — and keeps WITH FILE = 1 out of the
		// common single-backup statement.
		{"lone set omits the clause", one, 0, 0},
		{"lone set, stale index", one, 3, 0},
		{"first of three", three, 0, 1},
		{"last of three", three, 2, 3},
		// Position, not index+1: the two only coincide on a contiguous device.
		{"non-contiguous device uses Position", gappy, 1, 7},
		{"stale index falls back to the first set", three, 99, 1},
		{"negative index falls back to the first set", three, -1, 1},
		// index+1 is the fallback for a header that reported no position.
		{"unreported position falls back to index+1", unnumbered, 1, 2},
	} {
		if got := backupSetNumber(tc.headers, tc.i); got != tc.want {
			t.Errorf("%s: backupSetNumber(%d headers, i=%d) = %d, want %d",
				tc.what, len(tc.headers), tc.i, got, tc.want)
		}
	}
}

// bfile builds a backup file-list entry.
func bfile(logical, physical, typ string) *gosmo.BackupFile {
	return &gosmo.BackupFile{LogicalName: logical, PhysicalName: physical, Type: typ}
}

// The set of files every relocation case below is built from: a data and a
// log file recorded under the source database's own Windows paths.
func backupSetFiles() []*gosmo.BackupFile {
	return []*gosmo.BackupFile{
		bfile("AppDB", `D:\SQL\DATA\AppDB.mdf`, "D"),
		bfile("AppDB_log", `E:\SQL\LOG\AppDB_log.ldf`, "L"),
	}
}

// TestRelocateFilesAuto pins the behaviour the dialog had before the Files
// view existed, and still defaults to: files are moved to the server's
// default folders — under names derived from the target — only when the
// restore renames the database, since a same-name restore is meant to land
// on the original's own files.
func TestRelocateFilesAuto(t *testing.T) {
	plan := relocPlan{mode: relocAuto}
	defData, defLog := `C:\Data`, `C:\Log`

	if got := relocateFiles(backupSetFiles(), plan, defData, defLog, "AppDB", "AppDB"); got != nil {
		t.Errorf("same-name restore relocated files: %+v, want none", got)
	}
	// Case-insensitively the same name is the same database.
	if got := relocateFiles(backupSetFiles(), plan, defData, defLog, "AppDB", "appdb"); got != nil {
		t.Errorf("case-different name relocated files: %+v, want none", got)
	}

	got := relocateFiles(backupSetFiles(), plan, defData, defLog, "AppDB", "AppDB_Copy")
	want := []gosmo.RelocateFile{
		{LogicalName: "AppDB", PhysicalName: `C:\Data\AppDB_Copy_AppDB.mdf`},
		{LogicalName: "AppDB_log", PhysicalName: `C:\Log\AppDB_Copy_AppDB_log.ldf`},
	}
	assertRelocations(t, got, want)
}

// TestRelocateFilesOriginal pins that the "keep the locations recorded in
// the backup" choice emits no MOVE at all — including for a renamed target,
// where the pre-Files-view dialog always relocated.
func TestRelocateFilesOriginal(t *testing.T) {
	plan := relocPlan{mode: relocOriginal}
	for _, target := range []string{"AppDB", "AppDB_Copy"} {
		if got := relocateFiles(backupSetFiles(), plan, `C:\Data`, `C:\Log`, "AppDB", target); got != nil {
			t.Errorf("target %q: relocated files: %+v, want none", target, got)
		}
	}
}

// TestRelocateFilesFolder pins the explicit relocation: every file moves to
// the named folders whether or not the database is renamed, and the file
// names follow the same rule as relocAuto — the backup's own names for a
// same-name restore, target-derived ones for a rename, so a copy restored
// into the same folder as the original can't collide with it.
func TestRelocateFilesFolder(t *testing.T) {
	plan := relocPlan{mode: relocFolder, dataDir: `F:\NewData`, logDir: `G:\NewLog`}

	assertRelocations(t, relocateFiles(backupSetFiles(), plan, `C:\Data`, `C:\Log`, "AppDB", "AppDB"),
		[]gosmo.RelocateFile{
			{LogicalName: "AppDB", PhysicalName: `F:\NewData\AppDB.mdf`},
			{LogicalName: "AppDB_log", PhysicalName: `G:\NewLog\AppDB_log.ldf`},
		})

	assertRelocations(t, relocateFiles(backupSetFiles(), plan, `C:\Data`, `C:\Log`, "AppDB", "AppDB_Copy"),
		[]gosmo.RelocateFile{
			{LogicalName: "AppDB", PhysicalName: `F:\NewData\AppDB_Copy_AppDB.mdf`},
			{LogicalName: "AppDB_log", PhysicalName: `G:\NewLog\AppDB_Copy_AppDB_log.ldf`},
		})
}

// TestRelocateFilesFolderFallsBackToDefaults pins that a folder field left
// empty means the server's default directory, not a bare file name — which
// RESTORE would reject as a relative path.
func TestRelocateFilesFolderFallsBackToDefaults(t *testing.T) {
	plan := relocPlan{mode: relocFolder, logDir: `G:\NewLog`}
	assertRelocations(t, relocateFiles(backupSetFiles(), plan, `C:\Data`, `C:\Log`, "AppDB", "AppDB"),
		[]gosmo.RelocateFile{
			{LogicalName: "AppDB", PhysicalName: `C:\Data\AppDB.mdf`},
			{LogicalName: "AppDB_log", PhysicalName: `G:\NewLog\AppDB_log.ldf`},
		})
}

// TestRelocateFilesSuppliesAnExtension pins the fallback for a backup whose
// physical name carries no extension: a renamed file is minted from the
// logical name, so it needs one, and data and log files get different ones.
func TestRelocateFilesSuppliesAnExtension(t *testing.T) {
	files := []*gosmo.BackupFile{
		bfile("AppDB", `D:\SQL\DATA\AppDB`, "D"),
		bfile("AppDB_log", `E:\SQL\LOG\AppDB_log`, "L"),
	}
	assertRelocations(t, relocateFiles(files, relocPlan{mode: relocAuto}, `C:\Data`, `C:\Log`, "AppDB", "Copy"),
		[]gosmo.RelocateFile{
			{LogicalName: "AppDB", PhysicalName: `C:\Data\Copy_AppDB.ndf`},
			{LogicalName: "AppDB_log", PhysicalName: `C:\Log\Copy_AppDB_log.ldf`},
		})
}

// needsFileList decides whether buildRestoreOptions runs RESTORE
// FILELISTONLY at all, so it has to agree with relocateFiles: a mode that
// would produce MOVE clauses must not have its file list skipped.
func TestNeedsFileListAgreesWithRelocateFiles(t *testing.T) {
	for _, tc := range []struct {
		plan   relocPlan
		target string
	}{
		{relocPlan{mode: relocAuto}, "AppDB"},
		{relocPlan{mode: relocAuto}, "AppDB_Copy"},
		{relocPlan{mode: relocOriginal}, "AppDB"},
		{relocPlan{mode: relocOriginal}, "AppDB_Copy"},
		{relocPlan{mode: relocFolder, dataDir: `F:\D`, logDir: `F:\L`}, "AppDB"},
		{relocPlan{mode: relocFolder, dataDir: `F:\D`, logDir: `F:\L`}, "AppDB_Copy"},
	} {
		moves := relocateFiles(backupSetFiles(), tc.plan, `C:\Data`, `C:\Log`, "AppDB", tc.target)
		if want := len(moves) > 0; tc.plan.needsFileList("AppDB", tc.target) != want {
			t.Errorf("mode %d target %q: needsFileList = %v but relocateFiles produced %d moves",
				tc.plan.mode, tc.target, !want, len(moves))
		}
	}
}

// TestClipPathLeft pins that a path too long for its column keeps its tail.
// Two files under the same SQL Server default folder are ~70 columns of
// identical prefix, so a tail-clipped path (core.Truncate) renders them
// indistinguishable — the file name is the whole point of the line.
func TestClipPathLeft(t *testing.T) {
	const path = `C:\Program Files\Microsoft SQL Server\MSSQL17.MSSQLSERVER\MSSQL\DATA\AppDB.mdf`
	for _, tc := range []struct {
		w    int
		want string
	}{
		{0, ""},
		{-1, ""},
		{len(path), path},            // fits exactly, no ellipsis
		{len(path) + 5, path},        // room to spare
		{20, `…SSQL\DATA\AppDB.mdf`}, // fills the column, tail first
		{1, "…"},
	} {
		got := clipPathLeft(path, tc.w)
		if got != tc.want {
			t.Errorf("clipPathLeft(w=%d) = %q, want %q", tc.w, got, tc.want)
		}
		if w := core.DisplayWidth(got); w > tc.w && tc.w > 0 {
			t.Errorf("clipPathLeft(w=%d) is %d columns wide", tc.w, w)
		}
	}
}

func assertRelocations(t *testing.T, got, want []gosmo.RelocateFile) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d relocations, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("relocation %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// analyze opens the inspect view on headers[0] and reads that set's file
// list; selectHeader's reload reads the selected set's. On a device whose
// first set is the selected one they must produce the same number, or
// arrowing off set 1 and back onto it would swap the Files Included panel
// for a different set's files.
func TestAnalyzeAndSelectionAgreeOnTheFirstSet(t *testing.T) {
	headers := []*gosmo.BackupHeader{hdr(1, "AppDB"), hdr(2, "AppDB")}
	d := &RestoreDialog{headers: headers, headerIdx: 0}
	if opened, selected := backupSetNumber(headers, 0), d.restoreFileNumber(); opened != selected {
		t.Errorf("analyze reads set %d but the selection reports %d", opened, selected)
	}
}

// filesDialog builds just enough of a RestoreDialog for the Files view's
// focus cycle — the four widgets it rotates through, with the folder fields
// enabled to match relocFolder.
func filesDialog() *RestoreDialog {
	d := &RestoreDialog{}
	d.rbReloc = widgets.NewRadioBox("File Locations:", []string{"auto", "original", "folder"})
	d.fDataDir = widgets.NewInputField("Data folder:", 40, false)
	d.fLogDir = widgets.NewInputField("Log folder: ", 40, false)
	d.btnDefLoc = widgets.NewButton("Default Location", func() {})
	d.rbReloc.SetSelected(relocFolder)
	d.syncRelocState()
	return d
}

// TestFilesFocusSurvivesTheCycleShrinking pins that leaving relocFolder while
// focus sits past the end of the shorter cycle lands back on the radio. The
// cycle goes 4 entries to 1 the moment the folder fields are disabled, and
// handleFilesKey indexes it with filesFocus unguarded — a stale 3 is an
// out-of-range panic on the next keystroke, not a cosmetic wrong highlight.
func TestFilesFocusSurvivesTheCycleShrinking(t *testing.T) {
	d := filesDialog()
	d.setFilesFocus(3)
	if got := d.filesFocus; got != 3 {
		t.Fatalf("setFilesFocus(3) = %d, want 3 — the folder cycle should have four entries", got)
	}

	d.rbReloc.SetSelected(relocOriginal)
	d.syncRelocState()

	cycle := d.filesFocusCycle()
	if len(cycle) != 1 {
		t.Fatalf("cycle has %d entries outside relocFolder, want 1", len(cycle))
	}
	if d.filesFocus >= len(cycle) {
		t.Fatalf("filesFocus = %d, out of range for a %d-entry cycle", d.filesFocus, len(cycle))
	}
	if cycle[d.filesFocus] != focusable(d.rbReloc) {
		t.Errorf("focus landed on %T, want the radio — it is the only thing left to focus", cycle[d.filesFocus])
	}
}

// Tab has nowhere to go when the radio is the whole cycle, and must stay on
// it rather than wrapping to an index the shorter cycle doesn't have.
func TestFilesTabStaysPutOnASingleEntryCycle(t *testing.T) {
	d := filesDialog()
	d.rbReloc.SetSelected(relocAuto)
	d.syncRelocState()
	for i := 0; i < 3; i++ {
		d.handleFilesKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
		if d.filesFocus != 0 {
			t.Fatalf("Tab %d: filesFocus = %d, want 0", i+1, d.filesFocus)
		}
	}
}
