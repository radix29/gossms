package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// backup_common.go holds the small helpers the Backup and Restore dialogs
// (backup_dialog.go, restore_dialog.go) share: server-side path handling,
// the progress view and its bar, and display formatting for backup metadata.

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

// joinServerPath joins a directory and file name using the separator the
// directory itself uses — the path lives on the server, whose OS may not be
// the one gossms runs on.
func joinServerPath(dir, name string) string {
	if dir == "" {
		return name
	}
	sep := "/"
	if strings.Contains(dir, `\`) {
		sep = `\`
	}
	return strings.TrimRight(dir, `/\`) + sep + name
}

// serverPathBase returns the file-name component of a server-side path,
// whichever separator convention it uses.
func serverPathBase(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// serverPathExt returns the extension (".bak") of a server-side path's file
// name, or "" if it has none.
func serverPathExt(path string) string {
	base := serverPathBase(path)
	if i := strings.LastIndex(base, "."); i > 0 {
		return base[i:]
	}
	return ""
}

// formatHMS renders a duration as "HH:MM:SS". The app's only duration
// renderer: the backup/restore progress screens, the query panel's elapsed
// time, and every SQL Agent run duration go through it, so a job that took
// an hour and two minutes reads the same everywhere.
//
// Seconds truncate rather than round, which is what a running clock needs —
// the query panel's live timer must never show a second that hasn't elapsed.
func formatHMS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, (sec/60)%60, sec%60)
}

// formatBytes renders a byte count with a binary unit, e.g. "5.8 GB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// drawProgressBar renders a solid/shaded bar with a trailing percentage.
// pct < 0 draws an empty bar with no percentage (indeterminate).
func drawProgressBar(s tcell.Screen, x, y, w, pct int, st tcell.Style) {
	barW := w - 5 // room for " 100%"
	if barW < 1 {
		return
	}
	filled := 0
	if pct > 0 {
		filled = min(barW, barW*pct/100)
	}
	for i := 0; i < barW; i++ {
		ch := '░'
		if i < filled {
			ch = '█'
		}
		s.SetContent(x+i, y, ch, nil, st)
	}
	if pct >= 0 {
		core.DrawText(s, x+barW+1, y, st, strconv.Itoa(pct)+"%")
	}
}

// taskProgressView is what the Backup and Restore progress views differ in:
// their header rows, whether a "Progress:" label sits above the bar, and the
// operation's name in the final message.
type taskProgressView struct {
	header        []string // "Database : x", …, one row each from inner.Y+1
	progressLabel bool     // Backup's "Progress:" row, a blank row above the bar
	verb          string   // "Backup" / "Restore"
	task          *Task
}

// drawTaskProgress draws a progress view into inner, stopping above bottom
// (the separator row): the header rows, then — once there is a task — the bar,
// elapsed and remaining times, and the task's message wrapped over every row
// left.
func drawTaskProgress(s tcell.Screen, inner core.Rect, bottom int, v taskProgressView) {
	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	lx := inner.X + 1
	w := inner.W - 2

	for i, h := range v.header {
		core.DrawTextClipped(s, lx, inner.Y+1+i, w, labelStyle, h)
	}
	t := v.task
	if t == nil {
		return
	}
	y := inner.Y + len(v.header) + 2
	if v.progressLabel {
		core.DrawText(s, lx, y, labelStyle, "Progress:")
		y += 2
	}
	pct := t.Progress
	if t.Done && t.Err == nil {
		pct = 100
	}
	drawProgressBar(s, lx, y, w, pct, labelStyle)

	elapsed, remaining, haveRemaining := taskTimes(t)
	core.DrawText(s, lx, y+2, labelStyle, "Elapsed  : "+formatHMS(elapsed))
	rem := "--:--:--"
	if haveRemaining {
		rem = formatHMS(remaining)
	}
	core.DrawText(s, lx, y+3, labelStyle, "Remaining: "+rem)

	msg := t.Message
	msgStyle := labelStyle
	switch {
	case t.Done && t.Err != nil:
		msg = "Failed: " + t.Err.Error()
		msgStyle = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	case t.Done:
		msg = v.verb + " completed successfully."
	case msg == "":
		msg = "Starting " + strings.ToLower(v.verb) + "..."
	}
	// Last, and given every remaining row down to the separator: a failed
	// backup or restore reports SQL Server's own message, which is far longer
	// than one line and is the only thing on this screen the user still needs.
	msgY := y + 5
	for i, ln := range wrapMessage(msg, w, bottom-msgY) {
		core.DrawTextClipped(s, lx, msgY+i, w, msgStyle, ln)
	}
}

// taskTimes computes a task's elapsed time and, while it's still running
// with a known percentage, a linear estimate of the time remaining.
func taskTimes(t *Task) (elapsed, remaining time.Duration, haveRemaining bool) {
	end := time.Now()
	if t.Done {
		end = t.Finished
	}
	elapsed = end.Sub(t.Started)
	if !t.Done && t.Progress > 0 && t.Progress < 100 {
		remaining = elapsed * time.Duration(100-t.Progress) / time.Duration(t.Progress)
		haveRemaining = true
	}
	return elapsed, remaining, haveRemaining
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

// sqlStringLiteral renders s as a T-SQL N'...' literal, doubling embedded
// single quotes — the one escaping rule that matters for a plain
// identifier-shaped value like a database name.
//
// It is gosmo.QuoteLiteral, which emits the N prefix itself; hand-rolling the
// doubling would duplicate it across the two repos.
func sqlStringLiteral(s string) string {
	return gosmo.QuoteLiteral(s)
}

// backupHistoryQuery returns the msdb query behind "View Backup History"
// (Object Explorer, database node) — the same backupset/backupmediafamily
// join gosmo.Server.BackupHistory runs, but as literal T-SQL opened
// in a query window, so the result set is live and re-runnable.
func backupHistoryQuery(dbName string) string {
	return fmt.Sprintf(`SELECT bs.database_name          AS [Database],
       bs.backup_start_date      AS [Start Date],
       bs.backup_finish_date     AS [Finish Date],
       CASE bs.type
           WHEN 'D' THEN 'Full'
           WHEN 'I' THEN 'Differential'
           WHEN 'L' THEN 'Transaction Log'
           WHEN 'F' THEN 'File/Filegroup'
           ELSE bs.type
       END                       AS [Type],
       bs.backup_size / 1048576.0 AS [Size (MB)],
       bmf.physical_device_name  AS [Device],
       bs.user_name              AS [User],
       bs.server_name            AS [Server]
FROM   msdb.dbo.backupset bs
JOIN   msdb.dbo.backupmediafamily bmf ON bmf.media_set_id = bs.media_set_id
WHERE  bs.database_name = %s
ORDER  BY bs.backup_finish_date DESC;
`, sqlStringLiteral(dbName))
}
