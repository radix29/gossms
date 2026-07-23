package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// RestoreDialog modes: the option form, the backup-inspection view Analyze
// Backup switches to, and the in-place progress view once the restore runs.
const (
	restoreModeForm = iota
	restoreModeInspect
	restoreModeProgress
)

const (
	restoreDialogW = 72
	restoreDialogH = 26
)

// maxHistorySets caps how many backup-history entries the Backup Set
// dropdown lists (most recent first) — its open list doesn't scroll.
const maxHistorySets = 10

var (
	restoreFormButtons    = []string{"Analyze Backup", "Script", "Start Restore", "Cancel"}
	restoreInspectButtons = []string{"Restore", "Back"}
)

// RestoreDialog is the Restore Database dialog (Object Explorer, database
// node and Databases folder, "Restore Database..."). The source is either
// a backup file path or an entry picked from msdb backup history; the
// restore itself runs as a background Task (see tasks.go), so Hide can
// dismiss the progress view while the restore keeps running.
//
// When the target database name differs from the one recorded in the
// backup, every file in the set is relocated (MOVE) to the server's
// default data/log directories under a "<target>_<logical name>" file
// name, so restoring a copy next to the original works without the two
// databases fighting over the same physical files.
type RestoreDialog struct {
	dialogs.ModalDialog
	app *App
	sc  *db.ServerConn

	mode int

	rbSource   *widgets.RadioBox
	fFile      *widgets.InputField
	btnBrowse  *widgets.Button
	ddHistDB   *widgets.DropDown
	ddHistSet  *widgets.DropDown
	fTarget    *widgets.InputField
	rbRecovery *widgets.RadioBox
	cbReplace  *widgets.CheckBox
	cbVerify   *widgets.CheckBox
	cbClose    *widgets.CheckBox

	focusIdx  int
	focusable []focusable
	btnFocus  int

	status    string
	statusErr bool

	// Label rows computed by layoutForm, read by Draw.
	sourceLabelY int
	targetLabelY int

	// Source-change detection (see syncSourceState): prevSource tracks the
	// Restore From radio, prevHistDB the history-database dropdown.
	prevSource int
	prevHistDB string

	// lastAutoTarget mirrors BackupDialog.lastAutoDest: the target name the
	// dialog filled in itself, so it only overwrites an unedited field.
	lastAutoTarget string

	// histLoaded records that the history-database list fetch has been
	// kicked off for this show(); history holds the selected database's
	// backup sets, in the Backup Set dropdown's order.
	histLoaded bool
	history    []*gosmo.BackupInfo

	// loadSeq discards stale async fetches (database list, history,
	// analysis) after the dialog re-shows or the inputs change.
	loadSeq int

	// Inspection data (restoreModeInspect), from Analyze Backup.
	headers    []*gosmo.BackupHeader
	files      []*gosmo.BackupFile
	inspectDev string

	// task is the running (or finished) restore the progress view renders.
	task       *Task
	taskTarget string
	taskSource string
}

// NewRestoreDialog creates the dialog. Widgets are built per show().
func NewRestoreDialog(app *App) *RestoreDialog {
	d := &RestoreDialog{app: app}
	d.InitModal(app.screen, "Restore Database", restoreDialogW, restoreDialogH)
	return d
}

// show resets the dialog to a fresh form for sc/dbName and displays it.
func (d *RestoreDialog) show(sc *db.ServerConn, dbName string) {
	d.sc = sc
	d.mode = restoreModeForm
	d.btnFocus = 0
	d.task = nil
	d.headers, d.files = nil, nil
	d.history = nil
	d.histLoaded = false
	d.loadSeq++
	d.SetTitle("Restore Database")
	d.setStatusMsg("Ready", false)

	d.rbSource = widgets.NewRadioBox("Restore From:", []string{"Backup File", "Backup History"})
	d.btnBrowse = widgets.NewButton("Browse", d.browseFile)
	d.fFile = widgets.NewInputField("", d.fileFieldWidth(), false)
	var histItems []string
	if dbName != "" {
		histItems = []string{dbName}
	}
	d.ddHistDB = widgets.NewDropDown("Database:   ", histItems, 40)
	d.ddHistSet = widgets.NewDropDown("Backup Set: ", nil, 48)
	d.fTarget = widgets.NewInputField("", d.fileFieldWidth()+3+d.btnBrowse.Width(), false)
	d.rbRecovery = widgets.NewRadioBox("Recovery Options:", []string{"WITH RECOVERY", "WITH NORECOVERY"})
	d.cbReplace = widgets.NewCheckBox("Replace existing database (WITH REPLACE)")
	d.cbVerify = widgets.NewCheckBox("Verify backup before restore")
	d.cbVerify.SetChecked(true)
	d.cbClose = widgets.NewCheckBox("Close existing connections")
	d.cbClose.SetChecked(true)

	d.prevSource = 0
	d.prevHistDB = d.ddHistDB.Value()
	d.lastAutoTarget = dbName
	d.fTarget.SetValue(dbName)

	d.rebuildFocusable()
	d.ModalDialog.Show()
	d.setFocus(0)
}

// fileFieldWidth computes the backup-file input's content width so the
// input box plus the Browse button fill the dialog's inner width.
func (d *RestoreDialog) fileFieldWidth() int {
	return restoreDialogW - 2 /*border*/ - 2 /*margins*/ - 2 /*brackets*/ - 1 /*gap*/ - d.btnBrowse.Width()
}

// rebuildFocusable assembles the Tab cycle for the current source mode:
// the file input + Browse button, or the two history dropdowns.
func (d *RestoreDialog) rebuildFocusable() {
	if d.rbSource.Selected() == 0 {
		d.focusable = []focusable{
			d.rbSource, d.fFile, d.btnBrowse, d.fTarget,
			d.rbRecovery, d.cbReplace, d.cbVerify, d.cbClose,
		}
	} else {
		d.focusable = []focusable{
			d.rbSource, d.ddHistDB, d.ddHistSet, d.fTarget,
			d.rbRecovery, d.cbReplace, d.cbVerify, d.cbClose,
		}
	}
	if d.focusIdx >= len(d.focusable) {
		d.focusIdx = 0
	}
}

func (d *RestoreDialog) setFocus(i int) {
	for _, f := range d.focusable {
		f.Focus(false)
	}
	if i >= 0 && i < len(d.focusable) {
		d.focusIdx = i
		d.focusable[i].Focus(true)
	}
}

func (d *RestoreDialog) focusTo(w focusable) {
	for i, f := range d.focusable {
		if f == w {
			d.setFocus(i)
			return
		}
	}
}

func (d *RestoreDialog) setStatusMsg(msg string, isErr bool) {
	d.status, d.statusErr = msg, isErr
}

// syncSourceState reacts to input events that changed the Restore From
// radio or the history-database selection: it swaps the Tab cycle, kicks
// off the lazy history fetches, and keeps an unedited target-database
// field following the picked source database.
func (d *RestoreDialog) syncSourceState() {
	if d.mode != restoreModeForm {
		return
	}
	if src := d.rbSource.Selected(); src != d.prevSource {
		d.prevSource = src
		d.rebuildFocusable()
		d.setFocus(d.focusIdx)
		if src == 1 && !d.histLoaded {
			d.histLoaded = true
			d.loadHistoryDatabases()
		}
	}
	if d.rbSource.Selected() == 1 {
		if dbName := d.ddHistDB.Value(); dbName != d.prevHistDB {
			d.prevHistDB = dbName
			d.loadHistory(dbName)
			d.autoFillTarget(dbName)
		}
	}
}

// autoFillTarget sets the target-database field to name unless the user
// has already typed their own.
func (d *RestoreDialog) autoFillTarget(name string) {
	if strings.TrimSpace(d.fTarget.Value()) == "" || d.fTarget.Value() == d.lastAutoTarget {
		d.lastAutoTarget = name
		d.fTarget.SetValue(name)
	}
}

// browseFile opens the shared file dialog to pick the backup file.
func (d *RestoreDialog) browseFile() {
	start := strings.TrimSpace(d.fFile.Value())
	if start == "" && d.sc != nil && d.sc.Server != nil {
		start = joinServerPath(d.sc.Server.Info().DefaultBackupPath, "")
	}
	d.app.fileDialog.ShowOpen("Select Backup File", start, func(path string) {
		d.fFile.SetValue(path)
	})
}

func (d *RestoreDialog) progressButtons() []string {
	if d.task != nil && d.task.Done {
		return []string{"Close"}
	}
	return []string{"Hide", "Cancel"}
}

func (d *RestoreDialog) doFormButton() {
	switch d.btnFocus {
	case 0:
		d.analyze()
	case 1:
		d.script()
	case 2:
		d.startRestore()
	case 3:
		d.Hide()
	}
}

func (d *RestoreDialog) doInspectButton() {
	switch d.btnFocus {
	case 0:
		d.startRestore()
	case 1:
		d.mode = restoreModeForm
		d.btnFocus = 0
		d.SetTitle("Restore Database")
	}
}

func (d *RestoreDialog) doProgressButton() {
	if d.task == nil || d.task.Done {
		d.Hide()
		return
	}
	switch d.btnFocus {
	case 0:
		d.Hide()
	case 1:
		d.task.Cancel()
	}
}

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

	d.drawStatus(s)
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

func (d *RestoreDialog) drawStatus(s tcell.Screen) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if d.statusErr {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
	}
	inner := d.InnerRect()
	core.DrawTextClipped(s, inner.X+1, d.ButtonRowY()-2, inner.W-2, st, "Status: "+d.status)
}

// drawInspect renders the Backup Information view: the first backup set's
// header fields plus the files it contains.
func (d *RestoreDialog) drawInspect(s tcell.Screen) {
	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	inner := d.InnerRect()
	lx := inner.X + 1
	w := inner.W - 2
	h := d.headers[0]

	row := inner.Y + 1
	core.DrawTextClipped(s, lx, row, w, labelStyle, "File: "+serverPathBase(d.inspectDev))
	row += 2

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
			fmt.Sprintf("(%d backup sets on this device — showing the first)", len(d.headers)))
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
			fmt.Sprintf("%-28s %-12s %10s", f.LogicalName, group, formatBytes(f.Size)))
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
	core.DrawTextClipped(s, lx, inner.Y+6, w, msgStyle, msg)

	elapsed, remaining, haveRemaining := taskTimes(t)
	core.DrawText(s, lx, inner.Y+8, labelStyle, "Elapsed  : "+formatHMS(elapsed))
	rem := "--:--:--"
	if haveRemaining {
		rem = formatHMS(remaining)
	}
	core.DrawText(s, lx, inner.Y+9, labelStyle, "Remaining: "+rem)

	d.DrawSeparator(s)
	labels := d.progressButtons()
	d.DrawButtons(s, labels, core.Min(d.btnFocus, len(labels)-1))
}

// HandleKey routes keyboard events.
func (d *RestoreDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}

	switch d.mode {
	case restoreModeProgress:
		switch ev.Key() {
		case tcell.KeyEscape:
			d.Hide()
		case tcell.KeyEnter:
			d.btnFocus = core.Min(d.btnFocus, len(d.progressButtons())-1)
			d.doProgressButton()
		case tcell.KeyTab, tcell.KeyF1:
			d.btnFocus = (d.btnFocus + 1) % len(d.progressButtons())
		case tcell.KeyBacktab:
			n := len(d.progressButtons())
			d.btnFocus = (d.btnFocus - 1 + n) % n
		}
		return true
	case restoreModeInspect:
		switch ev.Key() {
		case tcell.KeyEscape:
			d.mode = restoreModeForm
			d.btnFocus = 0
			d.SetTitle("Restore Database")
		case tcell.KeyEnter:
			d.doInspectButton()
		case tcell.KeyTab, tcell.KeyF1:
			d.btnFocus = (d.btnFocus + 1) % len(restoreInspectButtons)
		case tcell.KeyBacktab:
			n := len(restoreInspectButtons)
			d.btnFocus = (d.btnFocus - 1 + n) % n
		}
		return true
	}

	openDD := d.openDropDown()
	switch ev.Key() {
	case tcell.KeyTab:
		d.setFocus((d.focusIdx + 1) % len(d.focusable))
		return true
	case tcell.KeyBacktab:
		d.setFocus((d.focusIdx - 1 + len(d.focusable)) % len(d.focusable))
		return true
	case tcell.KeyEscape:
		if openDD != nil {
			openDD.HandleKey(ev)
			return true
		}
		d.Hide()
		return true
	case tcell.KeyEnter:
		if openDD != nil {
			openDD.HandleKey(ev)
			d.syncSourceState()
			return true
		}
		if b, ok := d.focusable[d.focusIdx].(*widgets.Button); ok {
			return b.HandleKey(ev)
		}
		d.doFormButton()
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % len(restoreFormButtons)
		return true
	}

	if h, ok := d.focusable[d.focusIdx].(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok {
		consumed := h.HandleKey(ev)
		d.syncSourceState()
		return consumed
	}
	return true
}

// openDropDown returns whichever history dropdown is currently open, if any.
func (d *RestoreDialog) openDropDown() *widgets.DropDown {
	if d.ddHistDB.IsOpen() {
		return d.ddHistDB
	}
	if d.ddHistSet.IsOpen() {
		return d.ddHistSet
	}
	return nil
}

// HandleMouse routes mouse events.
func (d *RestoreDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach every mouseDragging-latched widget even when it
	// lands outside the dialog (consumed below) or in a mode with an early
	// return above — otherwise its next press is swallowed as a
	// continuation of the stale drag. Each returns false on ButtonNone, so
	// this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.rbSource.HandleMouse(ev)
		d.ddHistDB.HandleMouse(ev)
		d.ddHistSet.HandleMouse(ev)
		d.rbRecovery.HandleMouse(ev)
		d.btnBrowse.HandleMouse(ev)
		d.cbReplace.HandleMouse(ev)
		d.cbVerify.HandleMouse(ev)
		d.cbClose.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}

	switch d.mode {
	case restoreModeProgress:
		if i := d.ButtonClicked(ev, d.progressButtons()); i >= 0 {
			d.btnFocus = i
			d.doProgressButton()
		}
		return true
	case restoreModeInspect:
		if i := d.ButtonClicked(ev, restoreInspectButtons); i >= 0 {
			d.btnFocus = i
			d.doInspectButton()
		}
		return true
	}

	if ev.Buttons() == tcell.ButtonNone {
		if f, ok := d.focusable[d.focusIdx].(*widgets.InputField); ok {
			f.HandleMouse(ev)
		}
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	if i := d.ButtonClicked(ev, restoreFormButtons); i >= 0 {
		d.btnFocus = i
		d.doFormButton()
		return true
	}

	histMode := d.rbSource.Selected() == 1

	// An open dropdown's list is an overlay drawn last, so it gets first
	// refusal of every click.
	if histMode {
		if dd := d.openDropDown(); dd != nil && dd.HandleMouse(ev) {
			d.focusTo(dd)
			d.syncSourceState()
			return true
		}
	}
	if d.rbSource.HandleMouse(ev) {
		d.focusTo(d.rbSource)
		d.syncSourceState()
		return true
	}
	if histMode {
		for _, dd := range []*widgets.DropDown{d.ddHistDB, d.ddHistSet} {
			if dd.HandleMouse(ev) {
				d.focusTo(dd)
				d.syncSourceState()
				return true
			}
		}
	} else {
		if d.btnBrowse.HandleMouse(ev) {
			d.focusTo(d.btnBrowse)
			return true
		}
	}
	if d.rbRecovery.HandleMouse(ev) {
		d.focusTo(d.rbRecovery)
		return true
	}

	for _, cb := range []*widgets.CheckBox{d.cbReplace, d.cbVerify, d.cbClose} {
		if cb.HandleMouse(ev) {
			d.focusTo(cb)
			return true
		}
	}
	mx, my := ev.Position()
	fields := []*widgets.InputField{d.fTarget}
	if !histMode {
		fields = append(fields, d.fFile)
	}
	for _, f := range fields {
		if f.HitTest(mx, my) {
			d.focusTo(f)
			f.HandleMouse(ev)
			return true
		}
	}
	return true
}
