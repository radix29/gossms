package tui

import (
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/tuikit/core"
)

// backup_common.go holds the small helpers the Backup and Restore dialogs
// (backup_dialog.go, restore_dialog.go) share: server-side path handling,
// the progress view's wording (the view is dialogs.DrawTaskProgress), and
// display formatting for backup metadata.

// wrapMessage lays text out over at most maxLines lines of w columns,
// clipping the last one with "…" when even that isn't enough.
//
// SQL Server's own restore failures run to 200-plus characters ("The backup
// set holds a backup of a database other than the existing 'x' database.
// RESTORE DATABASE is terminating abnormally."), so a message drawn on one
// clipped line stops right before the part that says what went wrong. Every
// dialog that shows a server error has room for several lines; use them.
func wrapMessage(text string, w, maxLines int) []string {
	return core.WrapTextLimit(text, w, maxLines)
}

// taskProgressMessage is the Backup and Restore progress views' wording
// (dialogs.TaskProgressView.Message): the operation's name in it, and a
// cancel keeps taskRepairNote.
func taskProgressMessage(verb string) func(*Task) (string, bool) {
	return func(t *Task) (string, bool) {
		switch {
		case t.Done && t.Cancelled:
			return verb + " cancelled." + taskRepairNote(t), true
		case t.Done && t.Err != nil:
			return "Failed: " + t.Err.Error(), true
		case t.Done:
			return verb + " completed successfully.", false
		case t.Message == "":
			return "Starting " + strings.ToLower(verb) + "...", false
		}
		return t.Message, false
	}
}

// backupTypeLabel names a gosmo.BackupAction the way the Backup/Restore
// dialogs display it.
func backupTypeLabel(a gosmo.BackupAction) string {
	switch a {
	case gosmo.BackupActionDifferential:
		return "Differential"
	case gosmo.BackupActionLog:
		return "Transaction Log"
	case gosmo.BackupActionFiles:
		return "Files"
	default:
		return "Full"
	}
}

// backupSetLabel names a backup set's type the way the Restore dialog lists
// it — backupTypeLabel's names where the two overlap, short enough for the
// Backup Set list's 15-column field. A set type gosmo cannot name says so
// rather than passing for a full backup and being offered for restore as one.
func backupSetLabel(t gosmo.BackupSetType) string {
	switch t {
	case gosmo.BackupSetDatabase:
		return "Full"
	case gosmo.BackupSetLog:
		return "Transaction Log"
	case gosmo.BackupSetDifferential:
		return "Differential"
	case gosmo.BackupSetFile:
		return "Files"
	case gosmo.BackupSetDifferentialFile:
		return "Diff. Files"
	case gosmo.BackupSetPartial:
		return "Partial"
	case gosmo.BackupSetDifferentialPartial:
		return "Diff. Partial"
	default:
		return "Unknown"
	}
}

// sqlServerProductName maps a backup header's SoftwareVersionMajor to the
// marketing name of the SQL Server release that wrote it.
func sqlServerProductName(major int) string {
	switch major {
	case 17:
		return "SQL Server 2025"
	case 16:
		return "SQL Server 2022"
	case 15:
		return "SQL Server 2019"
	case 14:
		return "SQL Server 2017"
	case 13:
		return "SQL Server 2016"
	case 12:
		return "SQL Server 2014"
	case 11:
		return "SQL Server 2012"
	case 10:
		return "SQL Server 2008"
	case 9:
		return "SQL Server 2005"
	default:
		return fmt.Sprintf("SQL Server (version %d)", major)
	}
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// backupHistoryQuery returns the msdb query behind "View Backup History"
// (Object Explorer, database node) — the same backupset/backupmediafamily
// join gosmo.Server.BackupHistory runs, but as literal T-SQL opened
// in a query window, so the result set is live and re-runnable.
// backupmediafamily has one row per media family *and* per mirror, so
// bmf.mirror = 0 keeps a MIRROR TO backup from listing once per copy;
// the tiebreakers keep a striped set's devices together, in order.
func backupHistoryQuery(dbName string) string {
	return fmt.Sprintf(`SELECT bs.database_name          AS [Database],
       bs.backup_start_date      AS [Start Date],
       bs.backup_finish_date     AS [Finish Date],
       CASE bs.type
           WHEN 'D' THEN 'Full'
           WHEN 'I' THEN 'Differential'
           WHEN 'L' THEN 'Transaction Log'
           WHEN 'F' THEN 'File/Filegroup'
           WHEN 'G' THEN 'Differential File'
           WHEN 'P' THEN 'Partial'
           WHEN 'Q' THEN 'Differential Partial'
           ELSE bs.type
       END                       AS [Type],
       bs.backup_size / 1048576.0 AS [Size (MB)],
       bmf.physical_device_name  AS [Device],
       bs.user_name              AS [User],
       bs.server_name            AS [Server]
FROM   msdb.dbo.backupset bs
JOIN   msdb.dbo.backupmediafamily bmf ON bmf.media_set_id = bs.media_set_id AND bmf.mirror = 0
WHERE  bs.database_name = %s
ORDER  BY bs.backup_finish_date DESC, bs.backup_set_id DESC, bmf.family_sequence_number;
`, gosmo.QuoteLiteral(dbName))
}
