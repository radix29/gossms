package tui

import (
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// The Back Up / Restore dialogs against an Azure engine edition. Every rule
// checked here is Managed Instance's, driven live at t-qmi-01 and written up
// in docs/plan-azure-managed-instance.md § 3: MI answers any TO DISK with
//
//	Msg 41902 ... SQL Database Managed Instance supports database restore
//	from URI backup device only.
//
// and, owning its own backup chain, takes a backup to URL only as a COPY_ONLY
// full.

const blobDest = "https://acct.blob.core.windows.net/backups/AppDB_full.bak"

// azureConn is a bare Managed Instance connection — no capability probe, since
// nothing these dialogs gate is a permission question.
func azureConn(t *testing.T) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConnOnAzureMI(t)
	return sc
}

func onPremConn(t *testing.T) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t)
	return sc
}

// The whole point of the item: what the dialog encodes has to come out as a
// URL device, because a DISK one is refused outright.
func TestBackupToABlobEmitsToURL(t *testing.T) {
	d := backupForm("AppDB")
	d.fDest.SetValue(blobDest)

	stmt, err := gosmo.BuildBackupStatement(d.currentOptions())
	if err != nil {
		t.Fatalf("BuildBackupStatement: %v", err)
	}
	if !strings.Contains(stmt, "TO URL = N'"+blobDest+"'") {
		t.Errorf("statement does not name a URL device:\n%s", stmt)
	}
	if strings.Contains(stmt, "DISK") {
		t.Errorf("statement still names a DISK device:\n%s", stmt)
	}
}

func TestBackupOnAzurePinsFullAndCopyOnly(t *testing.T) {
	d := backupForm("AppDB")
	d.sc = azureConn(t)
	d.fDest.SetValue(blobDest)
	d.rbType.SetSelected(1) // the user got as far as Differential
	d.applyDeviceRules()

	if d.rbType.Enabled() {
		t.Error("the Backup Type radio is still live on a Managed Instance, which takes full backups only")
	}
	if got := d.rbType.Selected(); got != 0 {
		t.Errorf("Backup Type is %d, want 0 (Full) — the pinned option, not just a disabled wrong one", got)
	}
	if d.cbCopyOnly.Enabled() {
		t.Error("Copy-only is still live on a Managed Instance, which requires it")
	}
	if !d.cbCopyOnly.Checked() {
		t.Error("Copy-only is unchecked on a Managed Instance, which accepts nothing else")
	}
	if d.btnBrowse.Enabled() {
		t.Error("Browse is live against a blob container, which is not a filesystem")
	}

	stmt, err := gosmo.BuildBackupStatement(d.currentOptions())
	if err != nil {
		t.Fatalf("BuildBackupStatement: %v", err)
	}
	for _, want := range []string{"BACKUP DATABASE [AppDB]", "TO URL = N'" + blobDest + "'", "COPY_ONLY"} {
		if !strings.Contains(stmt, want) {
			t.Errorf("statement is missing %q:\n%s", want, stmt)
		}
	}
	for _, unwanted := range []string{"DIFFERENTIAL", "BACKUP LOG"} {
		if strings.Contains(stmt, unwanted) {
			t.Errorf("statement carries %q on an instance that refuses it:\n%s", unwanted, stmt)
		}
	}
}

// The other direction, and the one an over-broad gate breaks: an on-premises
// instance must lose nothing.
func TestBackupOnPremKeepsEveryOption(t *testing.T) {
	d := backupForm("AppDB")
	d.sc = onPremConn(t)
	d.fDest.SetValue(`C:\backups\AppDB.bak`)
	d.applyDeviceRules()

	if !d.rbType.Enabled() || !d.cbCopyOnly.Enabled() || !d.btnBrowse.Enabled() {
		t.Errorf("an on-premises backup lost an option: type=%v copyOnly=%v browse=%v",
			d.rbType.Enabled(), d.cbCopyOnly.Enabled(), d.btnBrowse.Enabled())
	}
	if d.status == backupURLHint {
		t.Error("the URL credential hint is shown for a DISK destination")
	}
}

// Browse is the one rule that follows the destination rather than the edition:
// an on-premises server can back up to a blob too, and its filesystem is not
// where that blob lives.
func TestBackupBrowseFollowsTheDestination(t *testing.T) {
	d := backupForm("AppDB")
	d.sc = onPremConn(t)

	d.fDest.SetValue(`C:\backups\AppDB.bak`)
	d.applyDeviceRules()
	if !d.btnBrowse.Enabled() {
		t.Fatal("Browse is off for a filesystem destination")
	}
	if d.status == backupURLHint {
		t.Error("the credential hint is shown for a filesystem destination")
	}

	d.fDest.SetValue(blobDest)
	d.applyDeviceRules()
	if d.btnBrowse.Enabled() {
		t.Error("Browse stayed live after the destination was retyped as a URL")
	}
	if d.status != backupURLHint {
		t.Errorf("status = %q, want the credential hint once the destination is a URL", d.status)
	}

	d.fDest.SetValue(`C:\backups\AppDB.bak`)
	d.applyDeviceRules()
	if d.status != "Ready" {
		t.Errorf("status = %q, want it back to Ready once the destination is a path again", d.status)
	}
}

// A real message outranks the standing hint: it is what the user asked for,
// and Validate's rendered statement or a failed backup's error is the only
// thing on screen that still matters.
func TestBackupRestingHintDoesNotOverwriteARealMessage(t *testing.T) {
	d := backupForm("AppDB")
	d.sc = onPremConn(t)
	d.fDest.SetValue(blobDest)
	d.setStatusMsg("Cannot open backup device.", true)
	d.applyDeviceRules()
	if d.status != "Cannot open backup device." {
		t.Errorf("status = %q, want the error left alone", d.status)
	}
}

func TestRestoreFromABlobEmitsFromURL(t *testing.T) {
	stmt, err := gosmo.BuildRestoreStatement(gosmo.RestoreOptions{
		Database: "AppDB",
		Devices:  []string{blobDest},
	})
	if err != nil {
		t.Fatalf("BuildRestoreStatement: %v", err)
	}
	if !strings.Contains(stmt, "FROM URL = N'"+blobDest+"'") {
		t.Errorf("statement does not name a URL device:\n%s", stmt)
	}
}

func TestRestoreBrowseFollowsTheSource(t *testing.T) {
	d := restoreForm()
	d.sc = onPremConn(t)

	d.fFile.SetValue(`C:\backups\AppDB.bak`)
	d.applyDeviceRules()
	if !d.btnBrowse.Enabled() {
		t.Fatal("Browse is off for a filesystem source on an on-premises instance")
	}

	d.fFile.SetValue(blobDest)
	d.applyDeviceRules()
	if d.btnBrowse.Enabled() {
		t.Error("Browse stayed live for a blob source")
	}
	if d.status != restoreURLHint {
		t.Errorf("status = %q, want the credential hint for a blob source", d.status)
	}
}

func TestRestoreBrowseIsOffOnAzure(t *testing.T) {
	d := restoreForm()
	d.sc = azureConn(t)
	d.fFile.SetValue(`C:\backups\AppDB.bak`) // a path the instance would refuse anyway
	d.applyDeviceRules()
	if d.btnBrowse.Enabled() {
		t.Error("Browse is live on a Managed Instance, which has no filesystem to browse for backups")
	}
}
