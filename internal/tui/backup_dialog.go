package tui

import (
	"context"
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

// BackupDialog modes: the option form first, then an in-place progress
// view once the backup is running.
const (
	backupModeForm = iota
	backupModeProgress
)

const (
	backupDialogW = 70
	backupDialogH = 23
)

// backupFormButtons is the form mode's button row.
var backupFormButtons = []string{"Start Backup", "Validate", "Cancel"}

// BackupDialog is the Back Up Database dialog (Object Explorer, database
// node, "Back Up Database..."). The backup itself runs as a background
// Task (see tasks.go), so the progress view's Hide button can dismiss the
// dialog while the backup keeps running — the status bar and Background
// Tasks dialog still track it.
type BackupDialog struct {
	dialogs.ModalDialog
	app *App
	sc  *db.ServerConn

	mode int

	ddDatabase *widgets.DropDown
	rbType     *widgets.RadioBox
	fDest      *widgets.InputField
	btnBrowse  *widgets.Button
	cbCompress *widgets.CheckBox
	cbVerify   *widgets.CheckBox
	cbChecksum *widgets.CheckBox
	cbCopyOnly *widgets.CheckBox

	focusIdx  int
	focusable []focusable
	btnFocus  int

	status    string
	statusErr bool

	// destLabelY/typeLabelY are rows computed by layoutForm, read by Draw.
	destLabelY int
	serverRowY int

	// lastAutoDest is the destination the dialog last generated itself;
	// database/type changes only regenerate the field while its content
	// still equals this (i.e. the user hasn't edited it). prevDB/prevType
	// detect those changes.
	lastAutoDest string
	prevDB       string
	prevType     int

	// loadSeq discards a stale async database-list fetch after the dialog
	// has been re-shown.
	loadSeq int

	// task is the running (or finished) backup the progress view renders,
	// with its display strings frozen at start time.
	task     *Task
	taskDB   string
	taskType string
	taskDest string
}

// NewBackupDialog creates the dialog. Widgets are built per show().
func NewBackupDialog(app *App) *BackupDialog {
	d := &BackupDialog{app: app}
	d.InitModal(app.screen, "Back Up Database", backupDialogW, backupDialogH)
	return d
}

// show resets the dialog to a fresh form for sc/dbName and displays it.
// The full database list arrives asynchronously; until then the dropdown
// holds just the database the dialog was opened on.
func (d *BackupDialog) show(sc *db.ServerConn, dbName string) {
	d.sc = sc
	d.mode = backupModeForm
	d.btnFocus = 0
	d.task = nil
	d.SetTitle("Back Up Database")
	d.setStatusMsg("Ready", false)

	var items []string
	if dbName != "" {
		items = []string{dbName}
	}
	d.ddDatabase = widgets.NewDropDown("Database: ", items, 40)
	d.rbType = widgets.NewRadioBox("Backup Type:", []string{"Full", "Differential", "Transaction Log"})
	d.btnBrowse = widgets.NewButton("Browse", d.browseDest)
	d.fDest = widgets.NewInputField("", d.destFieldWidth(), false)
	d.cbCompress = widgets.NewCheckBox("Compression")
	d.cbCompress.SetChecked(true)
	d.cbVerify = widgets.NewCheckBox("Verify backup after completion")
	d.cbVerify.SetChecked(true)
	d.cbChecksum = widgets.NewCheckBox("Use backup checksum")
	d.cbChecksum.SetChecked(true)
	d.cbCopyOnly = widgets.NewCheckBox("Copy-only backup")

	d.prevDB = d.ddDatabase.Value()
	d.prevType = 0
	d.lastAutoDest = d.autoDest()
	d.fDest.SetValue(d.lastAutoDest)

	d.rebuildFocusable()
	d.ModalDialog.Show()
	d.setFocus(0)
	d.loadDatabases()
}

// destFieldWidth computes the destination input's content width so the
// input box plus the Browse button fill the dialog's inner width.
func (d *BackupDialog) destFieldWidth() int {
	return backupDialogW - 2 /*border*/ - 2 /*margins*/ - 2 /*brackets*/ - 1 /*gap*/ - d.btnBrowse.Width()
}

func (d *BackupDialog) rebuildFocusable() {
	d.focusable = []focusable{
		d.ddDatabase, d.rbType, d.fDest, d.btnBrowse,
		d.cbCompress, d.cbVerify, d.cbChecksum, d.cbCopyOnly,
	}
}

func (d *BackupDialog) setFocus(i int) {
	for _, f := range d.focusable {
		f.Focus(false)
	}
	if i >= 0 && i < len(d.focusable) {
		d.focusIdx = i
		d.focusable[i].Focus(true)
	}
}

// focusTo moves focus to w, if it's in the focusable list.
func (d *BackupDialog) focusTo(w focusable) {
	for i, f := range d.focusable {
		if f == w {
			d.setFocus(i)
			return
		}
	}
}

func (d *BackupDialog) setStatusMsg(msg string, isErr bool) {
	d.status, d.statusErr = msg, isErr
}

// selectedAction maps the Backup Type radio to a gosmo.BackupAction.
func (d *BackupDialog) selectedAction() gosmo.BackupAction {
	switch d.rbType.Selected() {
	case 1:
		return gosmo.BackupActionDifferential
	case 2:
		return gosmo.BackupActionLog
	default:
		return gosmo.BackupActionDatabase
	}
}

// autoDest generates the default destination path for the current
// database/type selection: the server's default backup directory plus
// "<db>_full.bak" / "<db>_diff.bak" / "<db>_log.trn".
func (d *BackupDialog) autoDest() string {
	dbName := d.ddDatabase.Value()
	if dbName == "" {
		return ""
	}
	suffix := "_full.bak"
	switch d.rbType.Selected() {
	case 1:
		suffix = "_diff.bak"
	case 2:
		suffix = "_log.trn"
	}
	dir := ""
	if d.sc != nil && d.sc.Server != nil {
		dir = d.sc.Server.Info().DefaultBackupPath
	}
	return joinServerPath(dir, dbName+suffix)
}

// syncAutoDest regenerates the destination field after a database/type
// change, unless the user has edited the path themselves.
func (d *BackupDialog) syncAutoDest() {
	if d.mode != backupModeForm {
		return
	}
	sel, typ := d.ddDatabase.Value(), d.rbType.Selected()
	if sel == d.prevDB && typ == d.prevType {
		return
	}
	d.prevDB, d.prevType = sel, typ
	if strings.TrimSpace(d.fDest.Value()) == "" || d.fDest.Value() == d.lastAutoDest {
		d.lastAutoDest = d.autoDest()
		d.fDest.SetValue(d.lastAutoDest)
	}
}

// loadDatabases fetches the server's database list in the background and
// swaps it into the Database dropdown, keeping the current selection.
// tempdb is excluded — it can't be backed up.
func (d *BackupDialog) loadDatabases() {
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		dbs, err := sc.Server.DatabasesContext(ctx)
		app.postEvent(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Load databases: %v", err), true)
				return
			}
			names := make([]string, 0, len(dbs))
			for _, dbo := range dbs {
				if !strings.EqualFold(dbo.Name(), "tempdb") {
					names = append(names, dbo.Name())
				}
			}
			d.setDatabaseItems(names)
		})
		app.wakeEventLoop()
	}()
}

// setDatabaseItems replaces the Database dropdown (DropDown items are
// fixed at construction), preserving the current selection and focus.
func (d *BackupDialog) setDatabaseItems(names []string) {
	cur := d.ddDatabase.Value()
	dd := widgets.NewDropDown("Database: ", names, 40)
	for i, n := range names {
		if n == cur {
			dd.SetSelected(i)
			break
		}
	}
	d.ddDatabase = dd
	d.rebuildFocusable()
	d.setFocus(d.focusIdx)
	d.prevDB = d.ddDatabase.Value()
	d.syncAutoDest()
}

// browseDest opens the shared file dialog to pick the destination path.
// The path is used on the server, but browsing the local filesystem is
// still the best available picker (and the two are the same machine for a
// locally-run instance).
func (d *BackupDialog) browseDest() {
	start := strings.TrimSpace(d.fDest.Value())
	if start == "" {
		start = d.autoDest()
	}
	d.app.fileDialog.ShowSave("Backup Destination", start, func(path string) {
		d.fDest.SetValue(path)
	})
}

// validate builds the BACKUP statement from the current options without
// running it, reporting the result on the status line.
func (d *BackupDialog) validate() {
	opts := d.currentOptions()
	stmt, err := gosmo.BuildBackupStatement(opts)
	if err != nil {
		d.setStatusMsg(err.Error(), true)
		return
	}
	d.setStatusMsg("Valid — "+stmt, false)
}

// currentOptions assembles gosmo.BackupOptions from the form fields.
func (d *BackupDialog) currentOptions() gosmo.BackupOptions {
	opts := gosmo.BackupOptions{
		Database: d.ddDatabase.Value(),
		Action:   d.selectedAction(),
		Checksum: d.cbChecksum.Checked(),
		CopyOnly: d.cbCopyOnly.Checked(),
		Init:     true,
	}
	if dest := strings.TrimSpace(d.fDest.Value()); dest != "" {
		opts.Devices = []string{dest}
	}
	if d.cbCompress.Checked() {
		opts.Compression = new(true) // Go 1.26: new(expr)
	}
	return opts
}

// startBackup validates the form, switches to the progress view, and runs
// the backup as a background Task; with "Verify backup after completion"
// checked, a RESTORE VERIFYONLY runs as part of the same task.
func (d *BackupDialog) startBackup() {
	opts := d.currentOptions()
	if opts.Database == "" {
		d.setStatusMsg("Select a database to back up.", true)
		return
	}
	if len(opts.Devices) == 0 {
		d.setStatusMsg("Destination path is required.", true)
		return
	}
	verify := d.cbVerify.Checked()
	dest := opts.Devices[0]

	task, ctx := d.app.startTask("Backup " + opts.Database)
	d.task = task
	d.taskDB = opts.Database
	d.taskType = backupTypeLabel(opts.Action)
	d.taskDest = dest
	d.mode = backupModeProgress
	d.btnFocus = 0
	d.SetTitle("Backup Database - Progress")

	app, srv := d.app, d.sc.Server
	go func() {
		opts.Progress = func(pct int, msg string) { app.postProgress(task, pct, msg) }
		err := srv.BackupContext(ctx, opts)
		if err == nil && verify {
			app.postProgress(task, -1, "Verifying backup...")
			err = srv.VerifyBackupContext(ctx, dest)
		}
		app.postTaskDone(task, err)
	}()
}

// progressButtons is the progress view's button row: Hide keeps the backup
// running without the dialog; once the task finishes only Close remains.
func (d *BackupDialog) progressButtons() []string {
	if d.task != nil && d.task.Done {
		return []string{"Close"}
	}
	return []string{"Hide", "Cancel Backup"}
}

func (d *BackupDialog) doFormButton() {
	switch d.btnFocus {
	case 0:
		d.startBackup()
	case 1:
		d.validate()
	case 2:
		d.Hide()
	}
}

func (d *BackupDialog) doProgressButton() {
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

// HandleKey routes keyboard events.
func (d *BackupDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}

	if d.mode == backupModeProgress {
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
	}

	switch ev.Key() {
	case tcell.KeyTab:
		d.setFocus((d.focusIdx + 1) % len(d.focusable))
		return true
	case tcell.KeyBacktab:
		d.setFocus((d.focusIdx - 1 + len(d.focusable)) % len(d.focusable))
		return true
	case tcell.KeyEscape:
		if d.ddDatabase.IsOpen() {
			d.ddDatabase.HandleKey(ev)
			return true
		}
		d.Hide()
		return true
	case tcell.KeyEnter:
		if d.ddDatabase.IsOpen() {
			d.ddDatabase.HandleKey(ev)
			d.syncAutoDest()
			return true
		}
		if b, ok := d.focusable[d.focusIdx].(*widgets.Button); ok {
			return b.HandleKey(ev)
		}
		d.doFormButton()
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % len(backupFormButtons)
		return true
	}

	if h, ok := d.focusable[d.focusIdx].(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok {
		consumed := h.HandleKey(ev)
		d.syncAutoDest()
		return consumed
	}
	return true
}

// HandleMouse routes mouse events.
func (d *BackupDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}

	if d.mode == backupModeProgress {
		if i := d.ButtonClicked(ev, d.progressButtons()); i >= 0 {
			d.btnFocus = i
			d.doProgressButton()
		}
		return true
	}

	// Always forward a release to a focused input so a text-selection drag
	// terminates cleanly (see ConnectDialog.HandleMouse).
	if ev.Buttons() == tcell.ButtonNone {
		if f, ok := d.focusable[d.focusIdx].(*widgets.InputField); ok {
			f.HandleMouse(ev)
		}
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	if i := d.ButtonClicked(ev, backupFormButtons); i >= 0 {
		d.btnFocus = i
		d.doFormButton()
		return true
	}

	// The database dropdown's open list is an overlay drawn last, so it
	// gets first refusal of every click.
	if d.ddDatabase.HandleMouse(ev) {
		d.focusTo(d.ddDatabase)
		d.syncAutoDest()
		return true
	}
	if d.rbType.HandleMouse(ev) {
		d.focusTo(d.rbType)
		d.syncAutoDest()
		return true
	}
	if d.btnBrowse.HandleMouse(ev) {
		d.focusTo(d.btnBrowse)
		return true
	}

	mx, my := ev.Position()
	for _, cb := range []*widgets.CheckBox{d.cbCompress, d.cbVerify, d.cbChecksum, d.cbCopyOnly} {
		if my == cb.RectY() && mx >= cb.RectX() && mx < cb.RectX()+3 {
			cb.SetChecked(!cb.Checked())
			d.focusTo(cb)
			return true
		}
	}
	if d.fDest.HitTest(mx, my) {
		d.focusTo(d.fDest)
		d.fDest.HandleMouse(ev)
		return true
	}
	return true
}
