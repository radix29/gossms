package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// The Files view (restoreModeFiles) is where the restore's file handling is
// chosen: whether the restored database's files stay at the paths recorded
// in the backup, go to the server's default data/log folders, or go to
// folders the user names — with each file's recorded location and the path
// it would actually be restored to listed side by side.

// dirFieldWidth is the content width of the data/log folder inputs, sized so
// the label, the box and the dialog's right margin fill the inner width.
func (d *RestoreDialog) dirFieldWidth() int {
	return restoreDialogW - 2 /*border*/ - 2 /*margins*/ - 13 /*label + gap*/ - 2 /*brackets*/
}

// defaultDirs returns the server's default data and log directories, empty
// when the dialog has no live connection (unit tests).
func (d *RestoreDialog) defaultDirs() (dataDir, logDir string) {
	if d.sc == nil || d.sc.Server == nil {
		return "", ""
	}
	if info := d.sc.Server.Info(); info != nil {
		return info.DefaultDataPath, info.DefaultLogPath
	}
	return "", ""
}

// fillDefaultLocation puts the server's own default data/log directories
// into the folder fields — the Default Location button, and the initial
// contents of both fields.
func (d *RestoreDialog) fillDefaultLocation() {
	dataDir, logDir := d.defaultDirs()
	d.fDataDir.SetValue(dataDir)
	d.fLogDir.SetValue(logDir)
}

// relocation snapshots the Files view's choice for buildRestoreOptions,
// which runs on a background goroutine and must not read widgets.
func (d *RestoreDialog) relocation() relocPlan {
	return relocPlan{
		mode:    d.rbReloc.Selected(),
		dataDir: strings.TrimSpace(d.fDataDir.Value()),
		logDir:  strings.TrimSpace(d.fLogDir.Value()),
	}
}

// showFileLocations opens the Files view. The view lists the backup set's
// files, so the set's file list has to be in hand first: a device already
// analyzed for this form has it, anything else re-reads the header and file
// list exactly as Analyze does.
func (d *RestoreDialog) showFileLocations() {
	dev := d.deviceForRestore()
	if dev == "" {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	if dev == d.inspectDev && len(d.headers) > 0 {
		d.enterFilesMode()
		return
	}
	d.loadBackupInfo(restoreModeFiles)
}

// enterFilesMode switches to the Files view with its own focus cycle.
func (d *RestoreDialog) enterFilesMode() {
	d.mode = restoreModeFiles
	d.btnFocus = 0
	d.SetTitle("Restore File Locations")
	d.syncRelocState()
	d.setFilesFocus(0)
}

// filesFocusCycle is the view's Tab order. The folder fields only take part
// while relocFolder is selected — they are disabled otherwise, and a
// disabled field that can still be focused is a hole the cursor vanishes
// into.
func (d *RestoreDialog) filesFocusCycle() []focusable {
	if d.rbReloc.Selected() != relocFolder {
		return []focusable{d.rbReloc}
	}
	return []focusable{d.rbReloc, d.fDataDir, d.fLogDir, d.btnDefLoc}
}

func (d *RestoreDialog) setFilesFocus(i int) {
	for _, f := range []focusable{d.rbReloc, d.fDataDir, d.fLogDir, d.btnDefLoc} {
		f.Focus(false)
	}
	cycle := d.filesFocusCycle()
	n := len(cycle)
	d.filesFocus = ((i % n) + n) % n
	cycle[d.filesFocus].Focus(true)
}

// syncRelocState follows a change of relocation mode: the folder fields are
// only editable when the restore is actually going to use them.
func (d *RestoreDialog) syncRelocState() {
	on := d.rbReloc.Selected() == relocFolder
	d.fDataDir.SetEnabled(on)
	d.fLogDir.SetEnabled(on)
	d.setFilesFocus(d.filesFocus)
}

// plannedPaths maps each logical file name to the physical path the current
// choice would restore it to. Built from relocateFiles, the same function
// that builds the MOVE clauses, so the preview cannot drift from what the
// restore actually does.
func (d *RestoreDialog) plannedPaths() map[string]string {
	source := ""
	if h := d.selectedHeader(); h != nil {
		source = h.DatabaseName
	}
	dataDir, logDir := d.defaultDirs()
	moves := relocateFiles(d.files, d.relocation(), dataDir, logDir,
		source, strings.TrimSpace(d.fTarget.Value()))
	paths := make(map[string]string, len(moves))
	for _, m := range moves {
		paths[m.LogicalName] = m.PhysicalName
	}
	return paths
}

// clipPathLeft fits path into w display columns by dropping columns off its
// *front*, marking the cut with "…". core.Truncate cuts the tail instead,
// which on a server path throws away the only part that differs: two files
// under "C:\Program Files\Microsoft SQL Server\MSSQL17.MSSQLSERVER\MSSQL\"
// clip to the identical string, file name and all.
func clipPathLeft(path string, w int) string {
	if w <= 0 {
		return ""
	}
	if core.DisplayWidth(path) <= w {
		return path
	}
	runes := []rune(path)
	width := core.DisplayWidth("…")
	cut := len(runes)
	for i, r := range slices.Backward(runes) {
		rw := core.DisplayWidth(string(r))
		if width+rw > w {
			break
		}
		width += rw
		cut = i
	}
	return "…" + string(runes[cut:])
}

func (d *RestoreDialog) layoutFiles() {
	inner := d.InnerRect()
	lx := inner.X + 1
	row := inner.Y + 1
	d.rbReloc.SetBounds(lx, row)
	row += d.rbReloc.Height() + 1
	d.fDataDir.SetBounds(lx, row)
	row++
	d.fLogDir.SetBounds(lx, row)
	row++
	d.btnDefLoc.SetBounds(d.fLogDir.InputX(), row)
}

// filesListY is the first row of the file list, below the button laid out
// by layoutFiles plus a blank row.
func (d *RestoreDialog) filesListY() int {
	return d.fLogDir.RectY() + 3
}

// drawFiles renders the relocation options over the backup set's file list:
// each file's location in the backup, and the location it would be restored
// to under the current choice.
func (d *RestoreDialog) drawFiles(s tcell.Screen) {
	d.layoutFiles()

	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	inner := d.InnerRect()
	lx := inner.X + 1
	w := inner.W - 2

	d.rbReloc.Draw(s)
	d.fDataDir.Draw(s)
	d.fLogDir.Draw(s)
	d.btnDefLoc.Draw(s)

	row := d.filesListY()
	core.DrawText(s, lx, row, labelStyle, "Files in the backup")
	row++
	sep := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, lx, row, w, sep)
	row++

	if len(d.files) == 0 {
		core.DrawTextClipped(s, lx, row, w, dimStyle, "No file list for this backup set.")
	}
	planned := d.plannedPaths()
	lastRow := d.ButtonRowY() - 3 // drawStatus owns ButtonRowY()-2
	for i, f := range d.files {
		// Each file takes two rows, so it only goes in when both of them
		// fit; the row left over carries the "... and N more" line.
		if row+1 > lastRow {
			core.DrawTextClipped(s, lx, row, w, dimStyle,
				fmt.Sprintf("... and %d more", len(d.files)-i))
			break
		}
		kind := "DATA"
		if f.Type == "L" {
			kind = "LOG"
		}
		head := core.PadRight(f.LogicalName, 16) + " " + core.PadRight("("+kind+")", 7)
		core.DrawText(s, lx, row, labelStyle,
			head+clipPathLeft(f.PhysicalName, w-core.DisplayWidth(head)))
		row++
		to, moved := planned[f.LogicalName]
		if !moved {
			to = f.PhysicalName
		}
		toStyle := dimStyle
		if moved {
			toStyle = labelStyle
		}
		core.DrawText(s, lx+6, row, toStyle, "→ "+clipPathLeft(to, w-8))
		row++
	}

	// One line only: the file list above runs down to lastRow.
	d.drawStatus(s, 1)
	d.DrawSeparator(s)
	d.DrawButtons(s, restoreFilesButtons, d.btnFocus)
}

// handleFilesKey routes keys in the Files view.
func (d *RestoreDialog) handleFilesKey(ev *tcell.EventKey) bool {
	cycle := d.filesFocusCycle()
	switch ev.Key() {
	case tcell.KeyEscape:
		d.backToForm()
		return true
	case tcell.KeyTab:
		d.setFilesFocus(d.filesFocus + 1)
		return true
	case tcell.KeyBacktab:
		d.setFilesFocus(d.filesFocus - 1)
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % len(restoreFilesButtons)
		return true
	case tcell.KeyEnter:
		if b, ok := cycle[d.filesFocus].(*widgets.Button); ok {
			return b.HandleKey(ev)
		}
		d.doFilesButton()
		return true
	}
	if h, ok := cycle[d.filesFocus].(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok {
		consumed := h.HandleKey(ev)
		d.syncRelocState()
		return consumed
	}
	return true
}

// handleFilesMouse routes mouse events in the Files view. Called with the
// release already delivered to every latch-bearing widget by HandleMouse.
func (d *RestoreDialog) handleFilesMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 {
		return true
	}
	// The gesture belongs to whichever field claimed its press — see
	// HandleMouse's dragField.
	if d.dragField != nil {
		d.dragField.HandleMouse(ev)
		return true
	}
	if i := d.ButtonClicked(ev, restoreFilesButtons); i >= 0 {
		d.btnFocus = i
		d.doFilesButton()
		return true
	}
	if d.rbReloc.HandleMouse(ev) {
		d.focusFilesTo(d.rbReloc)
		d.syncRelocState()
		return true
	}
	if d.btnDefLoc.HandleMouse(ev) {
		d.focusFilesTo(d.btnDefLoc)
		return true
	}
	mx, my := ev.Position()
	for _, f := range []*widgets.InputField{d.fDataDir, d.fLogDir} {
		if f.Enabled() && f.HitTest(mx, my) {
			d.focusFilesTo(f)
			f.HandleMouse(ev)
			d.dragField = f
			return true
		}
	}
	return true
}

func (d *RestoreDialog) focusFilesTo(w focusable) {
	for i, f := range d.filesFocusCycle() {
		if f == w {
			d.setFilesFocus(i)
			return
		}
	}
}
