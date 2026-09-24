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

// drawStatus renders the "Status:" line, growing *upward* from the row above
// the separator into whatever rows the form's last checkbox leaves free. A
// failed BACKUP reports SQL Server's own message here ("Cannot open backup
// device ... Operating system error 5(Access is denied.)"), which is several
// times one line and is the only thing the user still needs — the same reason
// the progress view wraps its own message.
func (d *BackupDialog) drawStatus(s tcell.Screen) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	}
	inner := d.InnerRect()
	w := inner.W - 2
	lines := wrapMessage("Status: "+d.status, w, d.ButtonRowY()-1-(d.cbCopyOnly.RectY()+1))
	y := d.ButtonRowY() - 1 - len(lines)
	for i, ln := range lines {
		core.DrawTextClipped(s, inner.X+1, y+i, w, st, ln)
	}
}

// drawProgress renders the progress view from the running/finished task.
func (d *BackupDialog) drawProgress(s tcell.Screen) {
	drawTaskProgress(s, d.InnerRect(), d.ButtonRowY()-1, taskProgressView{
		header: []string{
			"Database : " + d.taskDB,
			"Type     : " + d.taskType,
			"Target   : " + d.taskDest,
		},
		progressLabel: true,
		verb:          "Backup",
		task:          d.task,
	})
	if d.task == nil {
		return
	}
	d.DrawSeparator(s)
	labels := d.progressButtons()
	d.DrawButtons(s, labels, min(d.btnFocus, len(labels)-1))
}
