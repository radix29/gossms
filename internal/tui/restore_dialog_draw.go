package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Draw renders whichever mode the dialog is in.
func (d *RestoreDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	switch d.mode {
	case restoreModeInspect:
		d.drawInspect(s)
		return
	case restoreModeFiles:
		d.drawFiles(s)
		return
	case restoreModeProgress:
		d.drawProgress(s)
		return
	}
	d.layoutForm()

	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	lx := inner.X + 1

	d.rbSource.Draw(s)
	if d.rbSource.Selected() == 0 {
		core.DrawText(s, lx, d.sourceLabelY, labelStyle, "Backup File:")
		d.fFile.Draw(s)
		d.btnBrowse.Draw(s)
	} else {
		d.ddHistDB.Draw(s)
		d.ddHistSet.Draw(s)
	}
	core.DrawText(s, lx, d.targetLabelY, labelStyle, "Target Database:")
	d.fTarget.Draw(s)
	d.rbRecovery.Draw(s)
	d.cbReplace.Draw(s)
	d.cbVerify.Draw(s)
	d.cbClose.Draw(s)

	// Two lines: the form's last row (cbClose) leaves exactly that much room
	// above the separator.
	d.drawStatus(s, 2)
	d.DrawSeparator(s)
	d.DrawButtons(s, restoreFormButtons, d.btnFocus)

	// Overlays drawn last, over the widgets positioned below them.
	if d.rbSource.Selected() == 1 {
		d.ddHistDB.DrawOverlay(s)
		d.ddHistSet.DrawOverlay(s)
	}
}

func (d *RestoreDialog) layoutForm() {
	inner := d.InnerRect()
	lx := inner.X + 1
	row := inner.Y + 1
	d.rbSource.SetBounds(lx, row)
	row += d.rbSource.Height() + 1

	d.sourceLabelY = row
	if d.rbSource.Selected() == 0 {
		row++
		d.fFile.SetBounds(lx, row)
		d.btnBrowse.SetBounds(lx+d.fFile.Width()+3, row)
		row += 2
	} else {
		d.ddHistDB.SetBounds(lx, row)
		row++
		d.ddHistSet.SetBounds(lx, row)
		row += 2
	}

	d.targetLabelY = row
	row++
	d.fTarget.SetBounds(lx, row)
	row += 2
	d.rbRecovery.SetBounds(lx, row)
	row += d.rbRecovery.Height() + 1
	d.cbReplace.SetBounds(lx, row)
	row++
	d.cbVerify.SetBounds(lx, row)
	row++
	d.cbClose.SetBounds(lx, row)
}

// drawStatus renders the status line, growing *upward* from the row above
// the separator so a wrapped server error uses the rows the mode leaves
// free above it (maxLines) instead of being cut off at one line.
func (d *RestoreDialog) drawStatus(s tcell.Screen, maxLines int) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	}
	inner := d.InnerRect()
	w := inner.W - 2
	lines := wrapMessage("Status: "+d.status, w, maxLines)
	y := d.ButtonRowY() - 1 - len(lines)
	for i, ln := range lines {
		core.DrawTextClipped(s, inner.X+1, y+i, w, st, ln)
	}
}

// drawInspect renders the Backup Information view: the selected backup
// set's header fields plus the files it contains.
func (d *RestoreDialog) drawInspect(s tcell.Screen) {
	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	inner := d.InnerRect()
	lx := inner.X + 1
	w := inner.W - 2
	h := d.selectedHeader()

	row := inner.Y + 1
	core.DrawTextClipped(s, lx, row, w, labelStyle, "File: "+serverPathBase(d.inspectDev))
	row += 2

	if h == nil {
		core.DrawTextClipped(s, lx, row, w, dimStyle, "No backup sets on this device.")
		return
	}

	size := formatBytes(h.BackupSize)
	if h.Compressed && h.CompressedSize > 0 {
		size += "  (compressed: " + formatBytes(h.CompressedSize) + ")"
	}
	lines := []string{
		"Database      : " + h.DatabaseName,
		"Backup Type   : " + backupTypeLabel(h.BackupType),
		"Backup Date   : " + h.BackupFinish.Format("2006-01-02 15:04:05"),
		"SQL Version   : " + sqlServerProductName(h.SoftwareVersionMajor),
		"Size          : " + size,
		"Compressed    : " + yesNo(h.Compressed),
		"Checksum      : " + yesNo(h.HasChecksums),
	}
	for _, ln := range lines {
		core.DrawTextClipped(s, lx, row, w, labelStyle, ln)
		row++
	}
	if len(d.headers) > 1 {
		core.DrawTextClipped(s, lx, row, w, dimStyle,
			fmt.Sprintf("Backup set %d of %d  (←/→ to change — the restore uses the one shown)",
				d.headerIdx+1, len(d.headers)))
	}
	row += 2

	core.DrawText(s, lx, row, labelStyle, "Files Included")
	row++
	sep := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, lx, row, w, sep)
	row++
	maxFiles := d.ButtonRowY() - 2 - row
	for i, f := range d.files {
		if i >= maxFiles-1 && len(d.files) > maxFiles {
			core.DrawTextClipped(s, lx, row, w, dimStyle,
				fmt.Sprintf("... and %d more", len(d.files)-i))
			break
		}
		group := f.FileGroupName
		if f.Type == "L" {
			group = "LOG"
		}
		core.DrawTextClipped(s, lx, row, w, labelStyle,
			core.PadRight(f.LogicalName, 28)+" "+core.PadRight(group, 12)+" "+core.PadLeft(formatBytes(f.Size), 10))
		row++
	}

	d.DrawSeparator(s)
	d.DrawButtons(s, restoreInspectButtons, d.btnFocus)
}

// drawProgress renders the progress view from the running/finished task.
func (d *RestoreDialog) drawProgress(s tcell.Screen) {
	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	lx := inner.X + 1
	w := inner.W - 2

	core.DrawTextClipped(s, lx, inner.Y+1, w, labelStyle, "Database : "+d.taskTarget)
	core.DrawTextClipped(s, lx, inner.Y+2, w, labelStyle, "Source   : "+d.taskSource)

	t := d.task
	if t == nil {
		return
	}
	pct := t.Progress
	if t.Done && t.Err == nil {
		pct = 100
	}
	drawProgressBar(s, lx, inner.Y+4, w, pct, labelStyle)

	elapsed, remaining, haveRemaining := taskTimes(t)
	core.DrawText(s, lx, inner.Y+6, labelStyle, "Elapsed  : "+formatHMS(elapsed))
	rem := "--:--:--"
	if haveRemaining {
		rem = formatHMS(remaining)
	}
	core.DrawText(s, lx, inner.Y+7, labelStyle, "Remaining: "+rem)

	msg := t.Message
	msgStyle := labelStyle
	switch {
	case t.Done && t.Err != nil:
		msg = "Failed: " + t.Err.Error()
		msgStyle = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	case t.Done:
		msg = "Restore completed successfully."
	case msg == "":
		msg = "Starting restore..."
	}
	// Last, and given every remaining row down to the separator: a failed
	// restore reports SQL Server's own message, which is far longer than one
	// line and is the only thing on this screen the user still needs.
	msgY := inner.Y + 9
	for i, ln := range wrapMessage(msg, w, d.ButtonRowY()-1-msgY) {
		core.DrawTextClipped(s, lx, msgY+i, w, msgStyle, ln)
	}

	d.DrawSeparator(s)
	labels := d.progressButtons()
	d.DrawButtons(s, labels, core.Min(d.btnFocus, len(labels)-1))
}
