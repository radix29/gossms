package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Draw renders whichever mode the dialog is in.
func (d *BackupDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	if d.mode == backupModeProgress {
		d.drawProgress(s)
		return
	}
	d.layoutForm()

	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	lx := inner.X + 1

	server := d.sc.Opts.Server
	core.DrawTextClipped(s, lx, d.serverRowY, inner.W-2, labelStyle, "Server:   "+server)
	d.ddDatabase.Draw(s)
	d.rbType.Draw(s)
	core.DrawText(s, lx, d.destLabelY, labelStyle, "Destination:")
	d.fDest.Draw(s)
	d.btnBrowse.Draw(s)
	d.cbCompress.Draw(s)
	d.cbVerify.Draw(s)
	d.cbChecksum.Draw(s)
	d.cbCopyOnly.Draw(s)

	d.drawStatus(s)
	d.DrawSeparator(s)
	d.DrawButtons(s, backupFormButtons, d.btnFocus)

	// Drawn last so the open database list isn't painted over by the
	// widgets positioned below it.
	d.ddDatabase.DrawOverlay(s)
}

func (d *BackupDialog) layoutForm() {
	inner := d.InnerRect()
	lx := inner.X + 1
	row := inner.Y + 1
	d.serverRowY = row
	row++
	d.ddDatabase.SetBounds(lx, row)
	row += 2
	d.rbType.SetBounds(lx, row)
	row += d.rbType.Height() + 1
	d.destLabelY = row
	row++
	d.fDest.SetBounds(lx, row)
	d.btnBrowse.SetBounds(lx+d.fDest.Width()+3, row)
	row += 2
	d.cbCompress.SetBounds(lx, row)
	row++
	d.cbVerify.SetBounds(lx, row)
	row++
	d.cbChecksum.SetBounds(lx, row)
	row++
	d.cbCopyOnly.SetBounds(lx, row)
}

// drawStatus renders the "Status:" line just above the button separator.
func (d *BackupDialog) drawStatus(s tcell.Screen) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	}
	inner := d.InnerRect()
	core.DrawTextClipped(s, inner.X+1, d.ButtonRowY()-2, inner.W-2, st, "Status: "+d.status)
}

// drawProgress renders the progress view from the running/finished task.
func (d *BackupDialog) drawProgress(s tcell.Screen) {
	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	lx := inner.X + 1
	w := inner.W - 2

	core.DrawTextClipped(s, lx, inner.Y+1, w, labelStyle, "Database : "+d.taskDB)
	core.DrawTextClipped(s, lx, inner.Y+2, w, labelStyle, "Type     : "+d.taskType)
	core.DrawTextClipped(s, lx, inner.Y+3, w, labelStyle, "Target   : "+d.taskDest)

	t := d.task
	if t == nil {
		return
	}
	core.DrawText(s, lx, inner.Y+5, labelStyle, "Progress:")
	pct := t.Progress
	if t.Done && t.Err == nil {
		pct = 100
	}
	drawProgressBar(s, lx, inner.Y+7, w, pct, labelStyle)

	elapsed, remaining, haveRemaining := taskTimes(t)
	core.DrawText(s, lx, inner.Y+9, labelStyle, "Elapsed  : "+formatHMS(elapsed))
	rem := "--:--:--"
	if haveRemaining {
		rem = formatHMS(remaining)
	}
	core.DrawText(s, lx, inner.Y+10, labelStyle, "Remaining: "+rem)

	msg := t.Message
	msgStyle := labelStyle
	switch {
	case t.Done && t.Err != nil:
		msg = "Failed: " + t.Err.Error()
		msgStyle = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	case t.Done:
		msg = "Backup completed successfully."
	case msg == "":
		msg = "Starting backup..."
	}
	core.DrawTextClipped(s, lx, inner.Y+12, w, msgStyle, msg)

	d.DrawSeparator(s)
	labels := d.progressButtons()
	d.DrawButtons(s, labels, core.Min(d.btnFocus, len(labels)-1))
}
