package tui

import (
	"database/sql/driver"
	"errors"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
)

// recoveryStatusResp answers Server.DatabaseRecoveryStatuses: name, last log
// backup LSN, primary file's differential base LSN, whether that file's row
// was visible.
func recoveryStatusResp(rows ...[]driver.Value) gRule {
	return gRule{"sys.database_recovery_status", gRows(rows...)}
}

func recoveryStatusRow(name, logLSN, baseLSN string, known bool) []driver.Value {
	return []driver.Value{name, logLSN, baseLSN, known}
}

func recoveryRow(id int, name string, model gosmo.RecoveryModel) []driver.Value {
	r := databaseRow(id, name, 0)
	r[3] = string(model)
	return r
}

// Each greyed type is a certain server refusal (Msg 3024/4212 master, 4208
// SIMPLE, 3035 no base, 4214 no chain); what is not known stays enabled.
func TestBackupTypeBlocks(t *testing.T) {
	full := &gosmo.Database{Name: "AppDB", RecoveryModel: gosmo.RecoveryModelFull}
	simple := &gosmo.Database{Name: "AppDB", RecoveryModel: gosmo.RecoveryModelSimple}
	st := func(chain, base, known bool) *gosmo.DatabaseRecoveryStatus {
		return &gosmo.DatabaseRecoveryStatus{LogBackupChainStarted: chain, HasDifferentialBase: base, DifferentialBaseKnown: known}
	}
	for _, tc := range []struct {
		name            string
		f               backupDBFacts
		wantDiff, wantL string
	}{
		{"no facts", backupDBFacts{}, "", ""},
		{"master", backupDBFacts{db: &gosmo.Database{Name: "master", RecoveryModel: gosmo.RecoveryModelSimple}, status: st(false, true, true)},
			typeHintMaster, typeHintMaster},
		{"FULL, backed up", backupDBFacts{db: full, status: st(true, true, true)}, "", ""},
		{"FULL, never backed up", backupDBFacts{db: full, status: st(false, false, true)}, typeHintNoBase, typeHintNoLog},
		{"FULL, full taken while SIMPLE", backupDBFacts{db: full, status: st(false, true, true)}, "", typeHintNoLog},
		{"SIMPLE, backed up", backupDBFacts{db: simple, status: st(false, true, true)}, "", typeHintSimple},
		{"SIMPLE, never backed up", backupDBFacts{db: simple, status: st(false, false, true)}, typeHintNoBase, typeHintSimple},
		{"SIMPLE, no status read", backupDBFacts{db: simple}, "", typeHintSimple},
		{"FULL, no status read", backupDBFacts{db: full}, "", ""},
		// A login that cannot see sys.master_files: an unseen base is not "no base".
		{"FULL, primary file not visible", backupDBFacts{db: full, status: st(true, false, false)}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diff, log := backupTypeBlocks(tc.f)
			if diff != tc.wantDiff || log != tc.wantL {
				t.Errorf("backupTypeBlocks = %q, %q; want %q, %q", diff, log, tc.wantDiff, tc.wantL)
			}
		})
	}
}

// typeRulesDialog opens Back Up on AppDB (FULL, fully backed up) with
// neighbours in each other state, the list and statuses landed.
func typeRulesDialog(t *testing.T, statuses gRule) *BackupDialog {
	t.Helper()
	_, d := backupDestDialog(t,
		gRule{"compatibility_level, collation_name", gRows(
			recoveryRow(1, "master", gosmo.RecoveryModelSimple),
			recoveryRow(7, "AppDB", gosmo.RecoveryModelFull),
			recoveryRow(8, "SimpleDB", gosmo.RecoveryModelSimple),
			recoveryRow(9, "NewDB", gosmo.RecoveryModelFull),
		)},
		statuses,
	)
	return d
}

func allStatuses() gRule {
	return recoveryStatusResp(
		recoveryStatusRow("master", "", "566000000018400001", true),
		recoveryStatusRow("AppDB", "62000000040000001", "62000000036000001", true),
		recoveryStatusRow("SimpleDB", "", "62000000036000001", true),
		recoveryStatusRow("NewDB", "", "", true),
	)
}

// pickDatabase moves the Database dropdown the way its key handler does:
// the widget changes, then syncAutoDest.
func pickDatabase(t *testing.T, d *BackupDialog, name string) {
	t.Helper()
	for i := range 10 {
		d.ddDatabase.SetSelected(i)
		if d.ddDatabase.Value() == name {
			d.syncAutoDest()
			return
		}
	}
	t.Fatalf("%s is not in the Database dropdown", name)
}

func typeStates(d *BackupDialog) [3]bool {
	return [3]bool{d.rbType.OptionEnabled(0), d.rbType.OptionEnabled(1), d.rbType.OptionEnabled(2)}
}

// The greying follows the database dropdown, a greyed selection falls back to
// Full, and the status line names the reason either way.
func TestBackupTypeFollowsTheDatabase(t *testing.T) {
	d := typeRulesDialog(t, allStatuses())
	if got := typeStates(d); got != [3]bool{true, true, true} {
		t.Fatalf("AppDB (FULL, backed up): types %v, want all enabled", got)
	}
	if d.status != "Ready" {
		t.Errorf("AppDB status %q, want Ready", d.status)
	}

	d.rbType.SetSelected(2) // Transaction Log
	d.syncAutoDest()
	pickDatabase(t, d, "SimpleDB")
	if got := typeStates(d); got != [3]bool{true, true, false} {
		t.Errorf("SimpleDB: types %v, want Transaction Log greyed", got)
	}
	if d.rbType.Selected() != 0 {
		t.Errorf("Transaction Log stayed selected on a SIMPLE database (%d); want the fall back to Full", d.rbType.Selected())
	}
	if d.status != typeHintSimple || d.statusErr {
		t.Errorf("status = %q (err %v), want %q", d.status, d.statusErr, typeHintSimple)
	}
	if opts := d.currentOptions(); opts.Action != gosmo.BackupActionDatabase {
		t.Errorf("SimpleDB backs up as %v, want a full backup", opts.Action)
	}

	pickDatabase(t, d, "master")
	if got := typeStates(d); got != [3]bool{true, false, false} {
		t.Errorf("master: types %v, want only Full", got)
	}
	if d.status != typeHintMaster {
		t.Errorf("master status %q, want %q", d.status, typeHintMaster)
	}

	pickDatabase(t, d, "NewDB")
	if got := typeStates(d); got != [3]bool{true, false, false} {
		t.Errorf("NewDB (FULL, never backed up): types %v, want only Full", got)
	}
	if want := typeHintNoBase + " " + typeHintNoLog; d.status != want {
		t.Errorf("NewDB status %q, want %q", d.status, want)
	}

	pickDatabase(t, d, "AppDB")
	if got := typeStates(d); got != [3]bool{true, true, true} {
		t.Errorf("back on AppDB: types %v, want all enabled again", got)
	}
	if d.status != "Ready" {
		t.Errorf("back on AppDB: status %q, want the hint replaced by Ready", d.status)
	}
}

// A refused status read costs only the chain rules: the list still lands and
// the recovery-model rule still applies.
func TestBackupTypeStatusReadRefusedFailsOpen(t *testing.T) {
	d := typeRulesDialog(t, gRule{"sys.database_recovery_status", gosmotest.Fail(errors.New("denied"))})
	if len(d.dbFacts) != 4 {
		t.Fatalf("the database list did not land: %v", d.dbFacts)
	}
	pickDatabase(t, d, "NewDB")
	if got := typeStates(d); got != [3]bool{true, true, true} {
		t.Errorf("NewDB with no status read: types %v, want all enabled", got)
	}
	pickDatabase(t, d, "SimpleDB")
	if got := typeStates(d); got != [3]bool{true, true, false} {
		t.Errorf("SimpleDB with no status read: types %v, want Transaction Log greyed", got)
	}
}

// An error on the status line outranks the standing hint: a database change
// must not paper over a failed Validate.
func TestBackupTypeHintLeavesAnErrorAlone(t *testing.T) {
	d := typeRulesDialog(t, allStatuses())
	d.setStatusMsg("Destination path is required.", true)
	pickDatabase(t, d, "SimpleDB")
	if d.status != "Destination path is required." {
		t.Errorf("status = %q, want the error kept", d.status)
	}
}
