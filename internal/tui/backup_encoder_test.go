package tui

import (
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// backupForm builds just the widgets currentOptions reads, the way show()
// constructs them — no App, no screen, no server. currentOptions and
// selectedAction touch nothing else, which is what makes the whole encoder
// reachable without a connection.
func backupForm(dbName string) *BackupDialog {
	d := &BackupDialog{}
	d.ddDatabase = widgets.NewDropDown("Database: ", []string{dbName}, 40)
	d.rbType = widgets.NewRadioBox("Backup Type:", []string{"Full", "Differential", "Transaction Log"})
	d.fDest = widgets.NewInputField("", 40, false)
	d.cbCompress = widgets.NewCheckBox("Compression")
	d.cbVerify = widgets.NewCheckBox("Verify backup after completion")
	d.cbChecksum = widgets.NewCheckBox("Use backup checksum")
	d.cbCopyOnly = widgets.NewCheckBox("Copy-only backup")
	return d
}

// The Backup Type radio's index is the only record of which gosmo action it
// means, and nothing else states the mapping — so a reordered radio would
// keep working in every round-trip while the user picks "Differential" and
// gets a full backup. Naming the pairs is what catches it.
func TestBackupTypeRadioMatchesItsAction(t *testing.T) {
	want := []struct {
		label  string
		action gosmo.BackupAction
	}{
		{"Full", gosmo.BackupActionDatabase},
		{"Differential", gosmo.BackupActionDifferential},
		{"Transaction Log", gosmo.BackupActionLog},
	}
	// Files backups have a label but no radio entry — this dialog cannot
	// take one, and gosmo's action still has to be named for the Restore
	// dialog's Inspect view, which reads the type off a backup header.
	if got := backupTypeLabel(gosmo.BackupActionFiles); got != "Files" {
		t.Errorf("backupTypeLabel(Files) = %q, want \"Files\"", got)
	}

	d := backupForm("AppDB")
	for i, w := range want {
		d.rbType.SetSelected(i)
		if got := d.selectedAction(); got != w.action {
			t.Errorf("radio index %d (%q) selects action %v, want %v", i, w.label, got, w.action)
		}
		// The label the dialog shows for that action has to agree, or the
		// progress view names a different backup than the one running.
		if got := backupTypeLabel(w.action); got != w.label {
			t.Errorf("backupTypeLabel(%v) = %q, want %q", w.action, got, w.label)
		}
	}
}

// currentOptions is the whole form-to-request encoder, and gosmo's statement
// builder is pure, so the two can be checked together: the assertion is on
// the T-SQL that would actually run, not on an intermediate struct that
// might still build the wrong statement.
func TestBackupOptionsBuildTheExpectedStatement(t *testing.T) {
	t.Run("a full backup with every option on", func(t *testing.T) {
		d := backupForm("AppDB")
		d.fDest.SetValue(`C:\backups\AppDB_full.bak`)
		d.cbCompress.SetChecked(true)
		d.cbChecksum.SetChecked(true)
		d.cbCopyOnly.SetChecked(true)

		stmt, err := gosmo.BuildBackupStatement(d.currentOptions())
		if err != nil {
			t.Fatalf("BuildBackupStatement: %v", err)
		}
		for _, want := range []string{"BACKUP DATABASE", "[AppDB]", `C:\backups\AppDB_full.bak`,
			"COMPRESSION", "CHECKSUM", "COPY_ONLY", "INIT"} {
			if !strings.Contains(stmt, want) {
				t.Errorf("statement is missing %q:\n%s", want, stmt)
			}
		}
	})

	t.Run("a transaction log backup", func(t *testing.T) {
		d := backupForm("AppDB")
		d.rbType.SetSelected(2)
		d.fDest.SetValue(`C:\backups\AppDB.trn`)

		stmt, err := gosmo.BuildBackupStatement(d.currentOptions())
		if err != nil {
			t.Fatalf("BuildBackupStatement: %v", err)
		}
		if !strings.Contains(stmt, "BACKUP LOG") {
			t.Errorf("a Transaction Log selection produced:\n%s", stmt)
		}
	})

	// Every checkbox off must drop its clause rather than emit a negated
	// one: NO_COMPRESSION is a different instruction from "say nothing and
	// let the server default apply", and the dialog offers no third state.
	t.Run("options off emit no clause", func(t *testing.T) {
		d := backupForm("AppDB")
		d.fDest.SetValue(`C:\backups\AppDB.bak`)
		d.cbCompress.SetChecked(false)
		d.cbChecksum.SetChecked(false)
		d.cbCopyOnly.SetChecked(false)

		opts := d.currentOptions()
		if opts.Compression != nil {
			t.Error("Compression is set with the box unchecked — a nil pointer is what means \"use the server default\"")
		}
		stmt, err := gosmo.BuildBackupStatement(opts)
		if err != nil {
			t.Fatalf("BuildBackupStatement: %v", err)
		}
		for _, unwanted := range []string{"COPY_ONLY", "CHECKSUM", "COMPRESSION"} {
			if strings.Contains(stmt, unwanted) {
				t.Errorf("statement carries %q with the box unchecked:\n%s", unwanted, stmt)
			}
		}
	})

	// Init is hardcoded true — the dialog has no "append to media set"
	// option, so every backup it takes overwrites. Pinned because dropping
	// it silently turns the dialog into an appending one, and a media set
	// that grows without bound is not something the user would notice
	// quickly.
	t.Run("init is always set", func(t *testing.T) {
		d := backupForm("AppDB")
		d.fDest.SetValue(`C:\b.bak`)
		if !d.currentOptions().Init {
			t.Error("Init is false — the dialog has no append option, so it must overwrite")
		}
	})
}

// An empty destination must produce no device at all rather than one empty
// string: startBackup gates on len(Devices) == 0 to refuse the backup, and a
// single blank entry passes that check while producing a statement that
// names an unnamed device.
func TestBackupBlankDestinationYieldsNoDevice(t *testing.T) {
	for _, dest := range []string{"", "   ", "\t"} {
		d := backupForm("AppDB")
		d.fDest.SetValue(dest)
		if got := d.currentOptions().Devices; len(got) != 0 {
			t.Errorf("destination %q produced devices %q, want none so startBackup refuses", dest, got)
		}
	}
	d := backupForm("AppDB")
	d.fDest.SetValue(`  C:\backups\AppDB.bak  `)
	if got := d.currentOptions().Devices; len(got) != 1 || got[0] != `C:\backups\AppDB.bak` {
		t.Errorf("a padded path produced %q, want it trimmed", got)
	}
}

// The progress view's buttons change once the task finishes: a running
// backup can be hidden or cancelled, a finished one only closed. Offering
// "Cancel Backup" on a finished task cancels a context nothing is listening
// to and leaves the user thinking the backup was stopped.
func TestBackupProgressButtonsFollowTheTask(t *testing.T) {
	d := &BackupDialog{}
	if got := d.progressButtons(); len(got) != 2 {
		t.Errorf("with no task at all: %q, want the running pair", got)
	}
	d.task = &Task{}
	if got, want := strings.Join(d.progressButtons(), ","), "Hide,Cancel Backup"; got != want {
		t.Errorf("running task buttons = %q, want %q", got, want)
	}
	d.task.Done = true
	if got, want := strings.Join(d.progressButtons(), ","), "Close"; got != want {
		t.Errorf("finished task buttons = %q, want %q", got, want)
	}
}

// taskTimes drives the elapsed/remaining readout. The remaining estimate is
// only meaningful mid-run: at 0% there is nothing to extrapolate from
// (the arithmetic divides by the progress), and at 100% or once Done there
// is nothing left to wait for.
func TestTaskTimes(t *testing.T) {
	start := time.Now().Add(-10 * time.Second)

	t.Run("no estimate at zero progress", func(t *testing.T) {
		_, _, have := taskTimes(&Task{Started: start, Progress: 0})
		if have {
			t.Error("offered a remaining estimate at 0% — there is nothing to extrapolate from")
		}
	})

	t.Run("no estimate for an indeterminate task", func(t *testing.T) {
		_, _, have := taskTimes(&Task{Started: start, Progress: -1})
		if have {
			t.Error("offered a remaining estimate for an indeterminate task")
		}
	})

	t.Run("half done estimates about the elapsed time again", func(t *testing.T) {
		elapsed, remaining, have := taskTimes(&Task{Started: start, Progress: 50})
		if !have {
			t.Fatal("no estimate at 50%")
		}
		if elapsed < 10*time.Second {
			t.Errorf("elapsed = %v, want at least 10s", elapsed)
		}
		// 50% done in E means about E to go; allow a wide band, the clock
		// is real.
		if remaining < 9*time.Second || remaining > 12*time.Second {
			t.Errorf("remaining = %v at 50%% after ~10s, want roughly 10s", remaining)
		}
	})

	t.Run("a finished task measures to its finish, not to now", func(t *testing.T) {
		fin := start.Add(3 * time.Second)
		elapsed, _, have := taskTimes(&Task{Started: start, Finished: fin, Done: true, Progress: 100})
		if have {
			t.Error("offered a remaining estimate for a finished task")
		}
		if elapsed != 3*time.Second {
			t.Errorf("elapsed = %v, want exactly 3s — a done task's elapsed must stop at Finished", elapsed)
		}
	})
}

// backupHistoryQuery interpolates a database name into T-SQL that is then
// opened in a query window and run. It is the only place in this file that
// builds SQL from a value the user did not type, and msdb will happily run
// whatever comes out — so the quoting is the whole test. sqlStringLiteral is
// the guard: N'...' with embedded quotes doubled.
func TestBackupHistoryQueryQuotesTheDatabaseName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AppDB", "N'AppDB'"},
		{"O'Brien", "N'O''Brien'"},
		{"a''b", "N'a''''b'"},
		{"", "N''"},
		// The shape an injection attempt takes here: closing the literal and
		// appending a statement. Doubling the quote keeps it one literal.
		{"x'; DROP TABLE t--", "N'x''; DROP TABLE t--'"},
	} {
		if got := sqlStringLiteral(tc.in); got != tc.want {
			t.Errorf("sqlStringLiteral(%q) = %q, want %q", tc.in, got, tc.want)
		}
		q := backupHistoryQuery(tc.in)
		if !strings.Contains(q, "WHERE  bs.database_name = "+tc.want) {
			t.Errorf("backupHistoryQuery(%q) did not carry the quoted literal %s:\n%s", tc.in, tc.want, q)
		}
		// An odd number of quotes anywhere in the finished statement means a
		// literal was left open, which is what an escape failure looks like.
		if strings.Count(q, "'")%2 != 0 {
			t.Errorf("backupHistoryQuery(%q) left an unbalanced quote:\n%s", tc.in, q)
		}
	}
}

// sqlServerProductName turns a backup header's version major into the name
// the Inspect view shows. The mapping is not arithmetic — SQL Server skipped
// major 11's usual naming and there is no 2010 — so it is a table, and a
// table is worth stating.
func TestSQLServerProductName(t *testing.T) {
	for major, want := range map[int]string{
		17: "SQL Server 2025",
		16: "SQL Server 2022",
		15: "SQL Server 2019",
		14: "SQL Server 2017",
		13: "SQL Server 2016",
		12: "SQL Server 2014",
		11: "SQL Server 2012",
		10: "SQL Server 2008",
		9:  "SQL Server 2005",
	} {
		if got := sqlServerProductName(major); got != want {
			t.Errorf("sqlServerProductName(%d) = %q, want %q", major, got, want)
		}
	}
	// An unknown major must report the number rather than guess a name: a
	// backup written by a newer release than this build knows about is
	// exactly when the reader needs the raw fact.
	got := sqlServerProductName(99)
	if !strings.Contains(got, "99") {
		t.Errorf("sqlServerProductName(99) = %q, want the raw version number in it", got)
	}
}

func TestYesNo(t *testing.T) {
	if yesNo(true) != "Yes" || yesNo(false) != "No" {
		t.Errorf("yesNo = %q/%q, want Yes/No", yesNo(true), yesNo(false))
	}
}
