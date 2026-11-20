package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/widgets"
)

// The Files view (restoreModeFiles) is where the restore's file handling is
// chosen: whether the restored database's files stay at the paths recorded in the
// backup, go to the server's default data/log folders, or go to folders the user
// names, with each file's recorded location and its restore path listed side by
// side.

// dirFieldWidth is the content width of the data/log folder inputs, sized so the
// label, the box and the dialog's right margin fill the inner width.
func (d *RestoreDialog) dirFieldWidth() int {
	return restoreDialogW - 2 /*border*/ - 2 /*margins*/ - 13 /*label + gap*/ - 2 /*brackets*/
}

// defaultDirs returns the server's default data and log directories: the ones
// read when the dialog opened, or the connect-time snapshot until that read lands
// (and if it failed). Empty when the dialog has no live connection (unit tests).
func (d *RestoreDialog) defaultDirs() (dataDir, logDir string) {
	if d.defDirs != nil {
		return d.defDirs.Data, d.defDirs.Log
	}
	if d.sc == nil || d.sc.Server == nil {
		return "", ""
	}
	if info := d.sc.Server.Info(); info != nil {
		return info.DefaultDataPath, info.DefaultLogPath
	}
	return "", ""
}

// loadDefaultPaths reads the server's current default directories and moves each
// folder field still showing the snapshot's value onto the fresh one. A field the
// user has edited is left alone. A failed read leaves the snapshot: a restore is
// not worth refusing over it.
func (d *RestoreDialog) loadDefaultPaths() {
	app, sc := d.app, d.sc
	if app == nil || sc == nil || sc.Server == nil {
		return
	}
	ctx, tok := d.defPaths.BeginTimeout(sc.Server.Context(), childFetchTimeout)
	app.safego("reading the default file locations", func() {
		p, err := sc.Server.DefaultPaths(ctx)
		app.postAndWake(func() {
			if !d.defPaths.Done(tok) || !d.Visible() || err != nil {
				return
			}
			oldData, oldLog := d.defaultDirs()
			d.defDirs = &p
			if strings.TrimSpace(d.fDataDir.Value()) == oldData {
				d.fDataDir.SetValue(p.Data)
			}
			if strings.TrimSpace(d.fLogDir.Value()) == oldLog {
				d.fLogDir.SetValue(p.Log)
			}
		})
	})
}

// fillDefaultLocation puts the server's own default data/log directories into the
// folder fields: the Default Location button, and the initial contents of both
// fields.
func (d *RestoreDialog) fillDefaultLocation() {
	dataDir, logDir := d.defaultDirs()
	d.fDataDir.SetValue(dataDir)
	d.fLogDir.SetValue(logDir)
}

// relocation snapshots the Files view's choice for buildRestoreOptions, which runs
// on a background goroutine and must not read widgets. The default directories go
// in with it (defaultDirs' answer), so the restore puts files where the Files view
// previewed them.
//
// The collation decides whether restoring Sales as sales is a rename: on a
// case-sensitive instance it is, and without MOVE clauses the restore collides
// with the source database's files.
func (d *RestoreDialog) relocation() gosmo.RestoreRelocation {
	dataDir, logDir := d.defaultDirs()
	return gosmo.RestoreRelocation{
		Mode:    relocationModes[d.rbReloc.Selected()],
		DataDir: strings.TrimSpace(d.fDataDir.Value()),
		LogDir:  strings.TrimSpace(d.fLogDir.Value()),

		DefaultDataDir: dataDir,
		DefaultLogDir:  logDir,
		Collation:      serverCollation(d.sc),
	}
}

// relocationModes is the Files view's radio, by index, as gosmo's modes.
var relocationModes = map[int]gosmo.RelocationMode{
	relocAuto:     gosmo.RelocateIfRenamed,
	relocOriginal: gosmo.RelocateNone,
	relocFolder:   gosmo.RelocateToFolders,
}

// showFileLocations opens the Files view. The view lists the backup set's files,
// so the file list has to be in hand first: a device already analyzed for this
// form has it, anything else re-reads the header and file list as Analyze does.
func (d *RestoreDialog) showFileLocations() {
	src := d.sourceForRestore()
	if len(src.devices) == 0 {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	if slices.Equal(src.devices, d.inspectDevs) && len(d.headers) > 0 {
		d.enterFilesMode()
		return
	}
	d.loadBackupInfo(restoreModeFiles)
}

// enterFilesMode switches to the Files view with its own focus ring.
func (d *RestoreDialog) enterFilesMode() {
	d.mode = restoreModeFiles
	d.btnFocus = 0
	d.SetTitle("Restore File Locations")
	d.syncRelocState()
	d.setFilesFocus(0)
}

// filesFocusable is the view's Tab order. The folder fields and Default
// Location are disabled unless relocFolder is selected, and dialogs.RingStep
// passes over them then: a disabled field that can still be focused is a hole
// the cursor vanishes into.
func (d *RestoreDialog) filesFocusable() []focusable {
	return []focusable{d.rbReloc, d.fDataDir, d.fLogDir, d.btnDefLoc}
}

// setFilesFocus focuses filesFocusable()[i], or the view's button row at
// len(filesFocusable()).
func (d *RestoreDialog) setFilesFocus(i int) {
	d.filesFocus = dialogs.SetFocusIn(d.filesFocusable(), i, i)
}

// onFilesButtons reports whether the Files view's button row has focus.
func (d *RestoreDialog) onFilesButtons() bool {
	return d.filesFocus == len(d.filesFocusable())
}

// moveFilesFocus steps the view's ring by dir. Leaving the button row resets
// it to Restore, the button Enter presses from a field.
func (d *RestoreDialog) moveFilesFocus(dir int) {
	d.setFilesFocus(dialogs.RingStep(d.filesFocusable(), d.filesFocus, dir))
	if !d.onFilesButtons() {
		d.btnFocus = 0
	}
}

// syncRelocState follows a change of relocation mode: the folder fields, and
// the button that fills them, are only usable when the restore is actually
// going to use them.
func (d *RestoreDialog) syncRelocState() {
	on := d.rbReloc.Selected() == relocFolder
	d.fDataDir.SetEnabled(on)
	d.fLogDir.SetEnabled(on)
	d.btnDefLoc.SetEnabled(on)
	// Focus on a control just greyed would sit on one that refuses every key;
	// the radio that greyed it is where it goes back to.
	if fs := d.filesFocusable(); d.filesFocus < len(fs) && !dialogs.FocusableEnabled(fs[d.filesFocus]) {
		d.setFilesFocus(0)
	}
}

// plannedPaths maps each logical file name to the physical path the current
// choice would restore it to. Built by gosmo.RestoreRelocation.Moves, which also
// builds the MOVE clauses, so the preview cannot drift from the restore.
func (d *RestoreDialog) plannedPaths() map[string]string {
	source := ""
	if h := d.selectedHeader(); h != nil {
		source = h.DatabaseName
	}
	moves := d.relocation().Moves(d.files, source, strings.TrimSpace(d.fTarget.Value()))
	paths := make(map[string]string, len(moves))
	for _, m := range moves {
		paths[m.LogicalName] = m.PhysicalName
	}
	return paths
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

// filesListY is the first row of the file list, below the button laid out by
// layoutFiles plus a blank row.
func (d *RestoreDialog) filesListY() int {
	return d.fLogDir.Bounds().Y + 3
}

// drawFiles renders the relocation options over the backup set's file list: each
// file's location in the backup, and the location it would be restored to under
// the current choice.
func (d *RestoreDialog) drawFiles(s tcell.Screen) {
	d.layoutFiles()

	labelStyle := theme.StyleDialog()
	dimStyle := theme.StyleDialogDim()
	inner := d.InnerRect()
	lx := inner.X + 1
	w := inner.W - 2

	d.rbReloc.Draw(s)
	d.fDataDir.Draw(s)
	d.fLogDir.Draw(s)
	d.btnDefLoc.Draw(s)

	row := d.filesListY()
	row = dialogs.DrawSectionHeading(s, lx, row, w, "Files in the backup")

	if len(d.files) == 0 {
		core.DrawTextClipped(s, lx, row, w, dimStyle, "No file list for this backup set.")
	}
	planned := d.plannedPaths()
	lastRow := d.ButtonRowY() - 3 // drawStatus owns ButtonRowY()-2
	for i, f := range d.files {
		// Each file takes two rows, so it only goes in when both fit; the row left over
		// carries the "... and N more" line.
		if row+1 > lastRow {
			core.DrawTextClipped(s, lx, row, w, dimStyle,
				fmt.Sprintf("... and %d more", len(d.files)-i))
			break
		}
		kind := "DATA"
		if f.Type == gosmo.BackupFileLog {
			kind = "LOG"
		}
		// Paths clip from the front: two files under the same SQL Server default
		// folder share ~70 columns of prefix, so a tail clip (core.Truncate)
		// would render them identical.
		head := core.PadRight(f.LogicalName, 16) + " " + core.PadRight("("+kind+")", 7)
		core.DrawText(s, lx, row, labelStyle,
			head+core.TruncateLeft(f.PhysicalName, w-core.DisplayWidth(head)))
		row++
		to, moved := planned[f.LogicalName]
		if !moved {
			to = f.PhysicalName
		}
		toStyle := dimStyle
		if moved {
			toStyle = labelStyle
		}
		core.DrawText(s, lx+6, row, toStyle, "→ "+core.TruncateLeft(to, w-8))
		row++
	}

	// One line only: the file list above runs down to lastRow.
	d.drawStatus(s, 1)
	d.DrawSeparator(s)
	d.DrawButtons(s, restoreFilesButtons, d.btnFocus)
}

// handleFilesKey routes keys in the Files view: the form's ring, with Escape
// going back to the form rather than closing the dialog.
func (d *RestoreDialog) handleFilesKey(ev *tcell.EventKey) bool {
	if d.onFilesButtons() {
		return dialogs.ButtonRowKey(ev, &d.btnFocus, len(restoreFilesButtons), nil, d.doFilesButton, d.backToForm, d.moveFilesFocus)
	}
	dialogs.FieldRingKey(ev, d.filesFocusable()[d.filesFocus], d.doFilesButton, d.backToForm, d.moveFilesFocus)
	d.syncRelocState()
	return true
}

// handleFilesMouse routes mouse events in the Files view. Called with the release
// already delivered to every latch-bearing widget by HandleMouse.
func (d *RestoreDialog) handleFilesMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 {
		return true
	}
	// The gesture belongs to whichever field claimed its press; see HandleMouse's
	// d.drag.
	if d.drag.Replay(ev) {
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
			d.drag.Claim(f, ev)
			return true
		}
	}
	return true
}

func (d *RestoreDialog) focusFilesTo(w focusable) {
	if i := dialogs.IndexOfFocusable(d.filesFocusable(), w); i >= 0 {
		d.setFilesFocus(i)
	}
}
