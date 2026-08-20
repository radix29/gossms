package tui

import (
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// RestoreDialog modes: the option form, the backup-inspection view Analyze
// Backup switches to, the file-relocation view Files switches to, and the
// in-place progress view once the restore runs.
const (
	restoreModeForm = iota
	restoreModeInspect
	restoreModeFiles
	restoreModeProgress
)

// File-relocation modes, in the order the Files view's radio lists them.
const (
	// relocAuto relocates every file to the server's default data/log
	// folders, but only when the target name differs from the backup's —
	// restoring a copy next to the original otherwise has the two databases
	// fighting over the same physical files. This is the dialog's default
	// and the only behaviour it had before the Files view existed.
	relocAuto = iota
	// relocOriginal restores every file to the path recorded in the backup.
	relocOriginal
	// relocFolder relocates every file into the folders named in the Files
	// view, regardless of whether the database is being renamed.
	relocFolder
)

// relocPlan is the Files view's relocation choice, snapshotted on the UI
// goroutine so buildRestoreOptions can read it from a background one.
type relocPlan struct {
	mode    int
	dataDir string
	logDir  string
}

// needsFileList reports whether the plan actually moves anything, and so
// whether the RESTORE FILELISTONLY the MOVE clauses are built from is worth
// running at all.
func (p relocPlan) needsFileList(source, target string) bool {
	switch p.mode {
	case relocFolder:
		return true
	case relocOriginal:
		return false
	default:
		return !strings.EqualFold(source, target)
	}
}

const (
	restoreDialogW = 72
	restoreDialogH = 26
)

// maxHistorySets caps how many backup-history entries the Backup Set
// dropdown lists (most recent first) — its open list doesn't scroll.
const maxHistorySets = 10

// Button rows. The form's labels are kept short deliberately: five buttons
// at "Analyze Backup" width no longer fit inside restoreDialogW. The inspect
// row has room to spell "File Locations" out, but says "Files" like the form
// does because both buttons open the same view — two names for one
// destination reads as two destinations.
var (
	restoreFormButtons    = []string{"Analyze", "Files", "Script", "Start Restore", "Cancel"}
	restoreInspectButtons = []string{"Files", "Restore", "Back"}
	restoreFilesButtons   = []string{"Restore", "Back"}
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

	// Files view (restoreModeFiles): where the restored database's files
	// land, and the folders relocFolder puts them in.
	rbReloc    *widgets.RadioBox
	fDataDir   *widgets.InputField
	fLogDir    *widgets.InputField
	btnDefLoc  *widgets.Button
	filesFocus int

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

	// dragField is the input field that claimed the current Button1
	// gesture, nil between gestures. Motion goes to it wherever the pointer
	// is, so dragging a selection out of the field's rect keeps extending it
	// instead of freezing at the boundary — the hit test below it is what
	// used to stop the drag dead. Same idiom as FindReplaceDialog.
	dragField *widgets.InputField

	// Inspection data (restoreModeInspect), from Analyze Backup.
	headers    []*gosmo.BackupHeader
	files      []*gosmo.BackupFile
	inspectDev string

	// headerIdx is which of headers the inspect view shows and the restore
	// targets — carried into RestoreOptions.FileNumber. A device written
	// with NOINIT holds several backup sets (a full at position 1, then
	// differentials or logs), and RESTORE without WITH FILE = n always takes
	// the first, so leaving this at 0 for such a device restores the full
	// backup no matter which set the user was looking at.
	headerIdx int

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
	d.headerIdx = 0
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

	d.rbReloc = widgets.NewRadioBox("File Locations:", []string{
		"Relocate to the default folders when renaming the database",
		"Keep the locations recorded in the backup",
		"Relocate all files to the folders below",
	})
	d.btnDefLoc = widgets.NewButton("Default Location", d.fillDefaultLocation)
	d.fDataDir = widgets.NewInputField("Data folder:", d.dirFieldWidth(), false)
	d.fLogDir = widgets.NewInputField("Log folder: ", d.dirFieldWidth(), false)
	d.filesFocus = 0
	// Pre-filled so the Relocate option is usable the moment it's picked;
	// Default Location puts these same values back after an edit.
	d.fillDefaultLocation()

	d.prevSource = 0
	d.prevHistDB = d.ddHistDB.Value()
	d.lastAutoTarget = dbName
	d.fTarget.SetValue(dbName)

	d.rebuildFocusable()
	d.ModalDialog.Show()
	// A latch must not survive into the next showing: a dialog dismissed
	// mid-drag would otherwise reopen still routing every click to that field.
	d.dragField = nil
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
	d.focusIdx = setFocusIn(d.focusable, i, d.focusIdx)
}

// focusTo moves focus to w, if it's in the focusable list.
func (d *RestoreDialog) focusTo(w focusable) {
	if i := indexOfFocusable(d.focusable, w); i >= 0 {
		d.setFocus(i)
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

// browseFile opens the shared file dialog to pick the backup file. Like
// Backup's Browse, it browses the server's filesystem — RESTORE reads the
// device on the SQL Server host, not on this machine.
func (d *RestoreDialog) browseFile() {
	fs, ok := newServerFS(d.sc)
	if !ok {
		d.setStatusMsg("Not connected — cannot browse the server's filesystem.", true)
		return
	}
	start := strings.TrimSpace(d.fFile.Value())
	if start == "" {
		start = joinServerPath(d.sc.Server.Info().DefaultBackupPath, "")
	}
	d.app.fileDialog.ShowOpenOn(fs, "Select Backup File", start, func(path string) {
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
		d.showFileLocations()
	case 2:
		d.script()
	case 3:
		d.startRestore()
	case 4:
		d.Hide()
	}
}

func (d *RestoreDialog) doInspectButton() {
	switch d.btnFocus {
	case 0:
		d.showFileLocations()
	case 1:
		d.startRestore()
	case 2:
		d.backToForm()
	}
}

func (d *RestoreDialog) doFilesButton() {
	switch d.btnFocus {
	case 0:
		d.startRestore()
	case 1:
		d.backToForm()
	}
}

// backToForm returns from the inspect or files view to the option form.
func (d *RestoreDialog) backToForm() {
	d.mode = restoreModeForm
	d.btnFocus = 0
	d.SetTitle("Restore Database")
}

func (d *RestoreDialog) doProgressButton() {
	runProgressButton(d.task, d.btnFocus, d.Hide)
}

// FocusedClipboardTarget implements core.ClipboardHost: the backup-file or
// target-database field while the option form is showing and has it focused.
// The inspect, files and progress views all drive their own focus rather than
// focusable, so each answers nil.
func (d *RestoreDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if d.mode != restoreModeForm {
		return nil
	}
	return focusedClipboardTarget(d.focusable, d.focusIdx)
}
