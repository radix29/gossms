package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
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
var backupFormButtons = []string{"Start Backup", "Script", "Validate", "Cancel"}

// The Compression dropdown's entries, SSMS's three. One checkbox could not
// say both "server default" (no clause) and "do not compress"
// (NO_COMPRESSION): unchecked meant the default, so on a server whose
// "backup compression default" is 1 unticking it still compressed.
const (
	compressServerDefault = iota
	compressOn
	compressOff
)

// compressDefaultLabel is the first entry's text; loadCompressionDefault
// appends the server's setting once read.
const compressDefaultLabel = "Use the default server setting"

func compressItems(serverDefault string) []string {
	first := compressDefaultLabel
	if serverDefault != "" {
		first += " (" + serverDefault + ")"
	}
	return []string{first, "Compress backup", "Do not compress backup"}
}

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
	ddCompress *widgets.DropDown
	cbVerify   *widgets.CheckBox
	cbChecksum *widgets.CheckBox
	cbCopyOnly *widgets.CheckBox

	focusIdx  int
	focusable []focusable
	btnFocus  int

	status    string
	statusErr bool

	// destLabelY/serverRowY are rows computed by layoutForm, read by Draw.
	destLabelY int
	serverRowY int

	// lastAutoDest is the destination the dialog last generated itself;
	// database/type changes only regenerate the field while its content
	// still equals this (i.e. the user hasn't edited it). prevDB/prevType
	// detect those changes.
	lastAutoDest string
	prevDB       string
	prevType     int

	// onButtons is set while the button row holds keyboard focus — the stop
	// after the last field in the Tab ring (buttonRowKey). Every field is
	// blurred meanwhile; focusIdx keeps the field focus left.
	onButtons bool

	// dbListRun is the Database dropdown's fetch, superseded by each showing
	// and abandoned by Hide along with defPaths.
	dbListRun latest

	// backupDir is the server's default backup directory as read when the
	// dialog opened (Server.DefaultPaths), nil until that read lands —
	// autoDest uses the connect-time snapshot until then. The snapshot alone
	// went stale: a default moved since connect put the backup in the old
	// directory.
	backupDir *string
	defPaths  latest

	// dbFacts is what the loaded database list says about each database, by
	// name, for applyTypeRules; nil until the list lands, and a database
	// missing from it (the opened one, if BACKUP refuses it) has every type
	// enabled. typeHint is the reason applyTypeRules last greyed a type, ""
	// for none — part of the resting status.
	dbFacts  map[string]backupDBFacts
	typeHint string

	// compressDef is loadCompressionDefault's read of the server's "backup
	// compression default", abandoned by Hide.
	compressDef latest

	// destCheck is Start Backup's probe for an existing destination file,
	// superseded by a second press and abandoned by Hide.
	destCheck latest

	// now stamps the generated destination name; nil is time.Now. A field so
	// a test can pin two showings to different seconds.
	now func() time.Time

	// drag is the text-selection gesture a click in one of the dialog's text
	// fields starts — see dialogs.FieldGesture for the ordering its three calls
	// depend on.
	drag dialogs.FieldGesture

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
	d.ddCompress = widgets.NewDropDown("Compression: ", compressItems(""), 38)
	d.cbVerify = widgets.NewCheckBox("Verify backup after completion")
	d.cbVerify.SetChecked(true)
	d.cbChecksum = widgets.NewCheckBox("Use backup checksum")
	d.cbChecksum.SetChecked(true)
	d.cbCopyOnly = widgets.NewCheckBox("Copy-only backup")

	d.prevDB = d.ddDatabase.Value()
	d.prevType = 0
	d.dbFacts, d.typeHint = nil, ""
	d.backupDir = nil
	d.lastAutoDest = d.autoDest()
	d.fDest.SetValue(d.lastAutoDest)
	d.applyDeviceRules()
	d.applyEditionRules()

	d.rebuildFocusable()
	d.ModalDialog.Show()
	// A latch must not survive into the next showing: a dialog dismissed
	// mid-drag would otherwise reopen still routing every click to that field.
	d.drag.Clear()
	d.setFocus(0)
	d.loadDatabases()
	d.loadDefaultPaths()
	d.loadCompressionDefault()
}

// Hide closes the dialog and abandons its loads. A running backup is a Task,
// not a load, and carries on.
func (d *BackupDialog) Hide() {
	d.dbListRun.Abandon()
	d.defPaths.Abandon()
	d.compressDef.Abandon()
	d.destCheck.Abandon()
	d.ModalDialog.Hide()
}

// destFieldWidth computes the destination input's content width so the
// input box plus the Browse button fill the dialog's inner width.
func (d *BackupDialog) destFieldWidth() int {
	return backupDialogW - 2 /*border*/ - 2 /*margins*/ - 2 /*brackets*/ - 1 /*gap*/ - d.btnBrowse.Width()
}

func (d *BackupDialog) rebuildFocusable() {
	d.focusable = []focusable{
		d.ddDatabase, d.rbType, d.fDest, d.btnBrowse,
		d.ddCompress, d.cbVerify, d.cbChecksum, d.cbCopyOnly,
	}
}

func (d *BackupDialog) setFocus(i int) {
	d.onButtons = false
	d.focusIdx = setFocusIn(d.focusable, i, d.focusIdx)
}

// stepFocus is Tab (+1) or Backtab (-1) in the fields: past either end of the
// ring is the button row.
func (d *BackupDialog) stepFocus(dir int) {
	if i := d.focusIdx + dir; i >= 0 && i < len(d.focusable) {
		d.setFocus(i)
		return
	}
	d.onButtons = true
	setFocusIn(d.focusable, -1, d.focusIdx)
}

// refocus re-applies focus after rebuildFocusable swapped a widget, leaving it
// on the button row if that is where it was: a database list landing while the
// user is on the buttons must not pull them back into the form.
func (d *BackupDialog) refocus() {
	if d.onButtons {
		setFocusIn(d.focusable, -1, d.focusIdx)
		return
	}
	d.setFocus(d.focusIdx)
}

// leaveButtons is Tab (+1) or Backtab (-1) off the button row, onto the first
// or last field. The highlight goes back to Start Backup, which is what Enter
// in a field fires.
func (d *BackupDialog) leaveButtons(dir int) {
	d.btnFocus = 0
	if dir > 0 {
		d.setFocus(0)
	} else {
		d.setFocus(len(d.focusable) - 1)
	}
}

// focusTo moves focus to w, if it's in the focusable list.
func (d *BackupDialog) focusTo(w focusable) {
	if i := indexOfFocusable(d.focusable, w); i >= 0 {
		d.setFocus(i)
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
// "<db>_FULL_<yyyymmdd_hhmmss>.bak" / "_DIFF_….bak" / "_LOG_….trn".
//
// The timestamp is what makes WITH INIT (currentOptions) safe. A fixed
// "<db>_log.trn" sent every log backup to one file; INIT replaced the previous
// one on disk while msdb still listed it, breaking the restore chain.
func (d *BackupDialog) autoDest() string {
	dbName := d.ddDatabase.Value()
	if dbName == "" {
		return ""
	}
	kind, ext := "FULL", ".bak"
	switch d.rbType.Selected() {
	case 1:
		kind = "DIFF"
	case 2:
		kind, ext = "LOG", ".trn"
	}
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	name := dbName + "_" + kind + "_" + now().Format("20060102_150405") + ext
	dir := ""
	if d.backupDir != nil {
		dir = *d.backupDir
	} else if d.sc != nil && d.sc.Server != nil {
		dir = d.sc.Server.Info().DefaultBackupPath
	}
	return gosmo.JoinServerPath(dir, name)
}

// loadDefaultPaths reads the server's current default backup directory and,
// if the destination is still the one the dialog generated, regenerates it
// there. A failed read leaves the snapshot's directory in place.
func (d *BackupDialog) loadDefaultPaths() {
	app, sc := d.app, d.sc
	if app == nil || sc == nil || sc.Server == nil {
		return
	}
	ctx, tok := d.defPaths.BeginTimeout(sc.Server.Context(), childFetchTimeout)
	app.safego("reading the default backup location", func() {
		p, err := sc.Server.DefaultPaths(ctx)
		app.postAndWake(func() {
			if !d.defPaths.Done(tok) || !d.Visible() || err != nil || d.mode != backupModeForm {
				return
			}
			d.backupDir = &p.Backup
			if d.fDest.Value() == d.lastAutoDest {
				d.lastAutoDest = d.autoDest()
				d.fDest.SetValue(d.lastAutoDest)
				d.applyDeviceRules()
				d.refreshRestingStatus()
			}
		})
	})
}

// syncAutoDest re-applies applyTypeRules for the selected database, then
// regenerates the destination field after a database/type change, unless the
// user has edited the path themselves, and then re-applies applyDeviceRules — which depends on the destination, so it has to run on
// every pass, not only the ones that regenerate it.
func (d *BackupDialog) syncAutoDest() {
	if d.mode != backupModeForm {
		return
	}
	d.applyTypeRules()
	sel, typ := d.ddDatabase.Value(), d.rbType.Selected()
	if sel != d.prevDB || typ != d.prevType {
		d.prevDB, d.prevType = sel, typ
		if strings.TrimSpace(d.fDest.Value()) == "" || d.fDest.Value() == d.lastAutoDest {
			d.lastAutoDest = d.autoDest()
			d.fDest.SetValue(d.lastAutoDest)
		}
	}
	d.applyDeviceRules()
}

// backupURLHint is the resting status line for a blob destination. BACKUP TO
// URL authenticates through a SQL Server credential whose *name* is the
// container URL; without one the statement fails at run time with an
// authentication error that says nothing about credentials, so the dialog says
// it up front. Kept to one line at backupDialogW.
const backupURLHint = "To URL: the container needs a credential named for it."

// restingStatus is the status line when nothing has gone wrong.
func (d *BackupDialog) restingStatus() string {
	if d.typeHint != "" {
		return d.typeHint
	}
	if gosmo.IsBackupURL(d.fDest.Value()) {
		return backupURLHint
	}
	return "Ready"
}

// refreshRestingStatus re-renders the resting status after the destination
// changed. A real message — an error, or Validate's rendered statement — is
// left alone: it is what the user asked for and outranks a standing hint.
func (d *BackupDialog) refreshRestingStatus() {
	if d.isRestingStatus() {
		d.setStatusMsg(d.restingStatus(), false)
	}
}

// isRestingStatus reports whether the status line holds one of the standing
// messages restingStatus produces, which a change of destination or database
// may replace.
func (d *BackupDialog) isRestingStatus() bool {
	if d.statusErr {
		return false
	}
	switch d.status {
	case "", "Ready", backupURLHint:
		return true
	}
	return d.typeHint != "" && d.status == d.typeHint
}

// backupDBFacts is what applyTypeRules knows about one database.
type backupDBFacts struct {
	db *gosmo.Database
	// status is the database's log chain and differential base, nil when
	// DatabaseRecoveryStatuses was refused or did not list it.
	status *gosmo.DatabaseRecoveryStatus
}

// The reasons applyTypeRules gives on the status line, each one line at
// backupDialogW.
const (
	typeHintMaster = "master takes full backups only."
	typeHintSimple = "Log backups need the FULL or BULK_LOGGED recovery model."
	typeHintNoBase = "Differentials need a full, non-copy-only backup first."
	typeHintNoLog  = "Log backups need a full backup taken under FULL recovery."
)

// backupTypeBlocks says why a Differential and a Transaction Log backup of db
// would be refused, "" for one the server takes. Each is a certain refusal
// after the user has filled in the form (checked live on 17, 2026-10-16):
//
//   - master: a differential is Msg 3024, a log backup Msg 4212.
//   - SIMPLE recovery (master, usually msdb): a log backup is Msg 4208.
//   - no differential base — no full backup yet, or only COPY_ONLY ones: a
//     differential is Msg 3035.
//   - no log chain — no full backup since the database last went to FULL or
//     BULK_LOGGED (a full taken while SIMPLE does not count): Msg 4214.
//
// What is not known stays enabled — no facts, no status row, a primary file
// the login cannot see — and the server has the last word (fail open).
func backupTypeBlocks(f backupDBFacts) (diff, log string) {
	if f.db == nil {
		return "", ""
	}
	if f.db.Name == "master" {
		return typeHintMaster, typeHintMaster
	}
	if f.db.RecoveryModel == gosmo.RecoveryModelSimple {
		log = typeHintSimple
	}
	if st := f.status; st != nil {
		if st.DifferentialBaseKnown && !st.HasDifferentialBase {
			diff = typeHintNoBase
		}
		if log == "" && !st.LogBackupChainStarted {
			log = typeHintNoLog
		}
	}
	return diff, log
}

// applyTypeRules greys the Backup Type entries the selected database cannot
// take (backupTypeBlocks). A greyed selection falls back to Full and the status
// line says why; otherwise the reason is the resting status, so a greyed entry
// is never unexplained. Re-applied on every syncAutoDest pass: it follows the
// database dropdown.
func (d *BackupDialog) applyTypeRules() {
	diff, log := backupTypeBlocks(d.dbFacts[d.ddDatabase.Value()])
	d.rbType.SetOptionEnabled(1, diff == "")
	d.rbType.SetOptionEnabled(2, log == "")
	hint := diff
	if log != "" && log != diff {
		hint = strings.TrimSpace(hint + " " + log)
	}
	resting := d.isRestingStatus()
	d.typeHint = hint
	switch {
	case !d.rbType.OptionEnabled(d.rbType.Selected()):
		d.rbType.SetSelected(0)
		d.setStatusMsg(hint, false)
	case resting:
		d.setStatusMsg(d.restingStatus(), false)
	}
}

// applyDeviceRules switches off what the destination device cannot carry.
//
// gosmo picks the device keyword off the path (gosmo.IsBackupURL), so a blob
// destination emits TO URL on any edition. This is the other half: an Azure
// engine refuses a DISK device outright —
//
//	Msg 41902 ... SQL Database Managed Instance supports database restore
//	from URI backup device only.
//
// — and, owning its own backup chain, accepts a backup to URL only as a COPY_ONLY
// full. So there the radio group is pinned to Full and Copy-only to checked, both
// greyed. Browse walks the *server's filesystem*, which a blob container is not,
// so it goes with any URL destination on any edition.
//
// Rules are re-applied on each syncAutoDest pass, not once at show(): the
// destination is a text field the user can retype into a URL.
func (d *BackupDialog) applyDeviceRules() {
	azure := serverIsAzure(d.sc)
	d.btnBrowse.SetEnabled(!azure && !gosmo.IsBackupURL(d.fDest.Value()))
	if azure {
		d.rbType.SetSelected(0)
		d.rbType.SetEnabled(false)
		d.cbCopyOnly.SetChecked(true)
		d.cbCopyOnly.SetEnabled(false)
	}
	d.refreshRestingStatus()
}

// applyEditionRules pins what the server's edition cannot do. Express and Web
// refuse WITH COMPRESSION (Msg 1844), so defaulting to it would fail every
// backup there with no hint which box to untick. The dropdown is pinned to the server default — which those editions leave
// uncompressed — and greyed, the way applyDeviceRules pins Azure's type. The
// edition does not change while the dialog is open, so show() runs it once.
func (d *BackupDialog) applyEditionRules() {
	var info *gosmo.ServerInfo
	if d.sc != nil && d.sc.Server != nil {
		info = d.sc.Server.Info()
	}
	if !info.SupportsBackupCompression() {
		d.ddCompress.SetSelected(compressServerDefault)
		d.ddCompress.SetEnabled(false)
	}
}

// loadCompressionDefault reads the server's "backup compression default" and
// names it on the dropdown's first entry, so "Use the default server setting"
// says what that is. A failed read leaves the entry bare; an edition without
// compression is not asked, its default being off by definition.
func (d *BackupDialog) loadCompressionDefault() {
	app, sc := d.app, d.sc
	if app == nil || sc == nil || sc.Server == nil || !d.ddCompress.Enabled() {
		return
	}
	ctx, tok := d.compressDef.BeginTimeout(sc.Server.Context(), childFetchTimeout)
	app.safego("reading the backup compression default", func() {
		c, err := sc.Server.ConfigurationByName(ctx, "backup compression default")
		app.postAndWake(func() {
			if !d.compressDef.Done(tok) || !d.Visible() || err != nil || d.mode != backupModeForm {
				return
			}
			label := "off"
			if c.ValueInUse != 0 {
				label = "on"
			}
			sel := d.ddCompress.Selected()
			d.ddCompress.SetItems(compressItems(label))
			d.ddCompress.SetSelected(sel)
		})
	})
}

// openDropDown is whichever dropdown has its list open, or nil. An open list
// is an overlay that gets first refusal of every event.
func (d *BackupDialog) openDropDown() *widgets.DropDown {
	for _, dd := range []*widgets.DropDown{d.ddDatabase, d.ddCompress} {
		if dd.IsOpen() {
			return dd
		}
	}
	return nil
}

// loadDatabases fetches the server's database list in the background and
// swaps it into the Database dropdown, keeping the current selection. What
// the list may contain is backupDatabases' rule, not this dialog's.
//
// The recovery statuses ride along for applyTypeRules. Their read failing
// costs only those rules — every type stays enabled — not the list.
func (d *BackupDialog) loadDatabases() {
	app, sc := d.app, d.sc
	ctx, seq := d.dbListRun.BeginTimeout(sc.Server.Context(), childFetchTimeout)
	app.safego("loading the backup database list", func() {
		dbs, err := backupDatabases(ctx, sc)
		var statuses []*gosmo.DatabaseRecoveryStatus
		if err == nil {
			statuses, _ = sc.Server.DatabaseRecoveryStatuses(ctx)
		}
		app.postAndWake(func() {
			if !d.dbListRun.Done(seq) || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Load databases: %v", err), true)
				return
			}
			facts := make(map[string]backupDBFacts, len(dbs))
			for _, x := range dbs {
				facts[x.Name] = backupDBFacts{db: x}
			}
			for _, st := range statuses {
				if f, ok := facts[st.DatabaseName]; ok {
					f.status = st
					facts[st.DatabaseName] = f
				}
			}
			d.dbFacts = facts
			d.setDatabaseItems(namesOf(dbs))
		})
	})
}

// setDatabaseItems replaces the Database dropdown (DropDown items are fixed at
// construction), preserving the current selection and focus.
//
// A selection missing from the incoming list is kept, at the front, not dropped.
// show() seeds the dropdown with the database the dialog opened on, and
// backupDatabases filters out ones BACKUP will refuse, so Back Up on an OFFLINE
// database arrives with a selection not in the list. Falling to index 0 would
// silently retarget the dialog at whichever database sorts first; failing loudly
// on the picked one is better.
func (d *BackupDialog) setDatabaseItems(names []string) {
	cur := d.ddDatabase.Value()
	if cur != "" && !slices.Contains(names, cur) {
		names = append([]string{cur}, names...)
	}
	dd := widgets.NewDropDown("Database: ", names, 40)
	for i, n := range names {
		if n == cur {
			dd.SetSelected(i)
			break
		}
	}
	d.ddDatabase = dd
	d.rebuildFocusable()
	d.refocus()
	d.prevDB = d.ddDatabase.Value()
	d.syncAutoDest()
}

// browseDest opens the shared file dialog to pick the destination path.
// It browses the *server's* filesystem (newServerFS): BACKUP resolves the
// device path on the SQL Server host, so a path picked from the client's own
// disks names a directory the server can't write to.
func (d *BackupDialog) browseDest() {
	fs, ok := newServerFS(d.sc)
	if !ok {
		d.setStatusMsg("Not connected — cannot browse the server's filesystem.", true)
		return
	}
	start := strings.TrimSpace(d.fDest.Value())
	if start == "" {
		start = d.autoDest()
	}
	d.app.fileDialog.ShowSaveOn(fs, "Backup Destination", start, func(path string) {
		d.fDest.SetValue(path)
		// The picked path is the user's own choice now, so syncAutoDest must
		// stop treating the field as still holding a generated default and
		// overwriting it on the next database/type change.
		d.lastAutoDest = ""
		d.applyDeviceRules()
	})
}

// validate builds the BACKUP statement from the current options without
// running it, reporting the result on the status line.
func (d *BackupDialog) validate() {
	opts := d.currentOptions()
	stmt, err := d.sc.Server.BuildBackupStatement(opts)
	if err != nil {
		d.setStatusMsg(err.Error(), true)
		return
	}
	d.setStatusMsg("Valid — "+stmt, false)
}

// script builds the BACKUP statement from the current options and opens it
// in a new query window for review — "Script Action to New Query Window"
// applied to Back Up Database. Validate only checks that the options build
// cleanly; this hands the T-SQL over to inspect, tweak, or run.
func (d *BackupDialog) script() {
	opts := d.currentOptions()
	stmt, err := d.sc.Server.BuildBackupStatement(opts)
	if err != nil {
		d.setStatusMsg(err.Error(), true)
		return
	}
	d.app.openQueryWithText(d.sc, "", stmt)
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
		opts.Devices = []gosmo.BackupTarget{gosmo.DiskTarget(dest)}
	}
	switch d.ddCompress.Selected() {
	case compressOn:
		opts.Compression = new(true) // Go 1.26: new(expr)
	case compressOff:
		opts.Compression = new(false)
	}
	return opts
}

// startBackup validates the form and, once the destination has been checked
// (checkDestThen), runs the backup.
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
	d.checkDestThen(opts.Devices[0], func() { d.runBackup(opts, verify) })
}

// checkDestThen asks before a backup overwrites an existing file. Every backup
// carries WITH INIT, so a destination that already holds backups loses them —
// a generated name never does, but a typed or browsed one can.
//
// xp_fileexist needs sysadmin where BACKUP needs only db_backupoperator, so a
// probe that fails (refused, timed out) is not a refusal: the backup runs
// unasked, as it did before there was a check. A URL destination is a blob,
// which xp_fileexist cannot see, and goes straight through.
func (d *BackupDialog) checkDestThen(dest gosmo.BackupTarget, run func()) {
	if dest.IsURL() {
		run()
		return
	}
	app, srv, path := d.app, d.sc.Server, dest.String()
	ctx, tok := d.destCheck.BeginTimeout(srv.Context(), childFetchTimeout)
	d.setStatusMsg("Checking the destination...", false)
	app.safego("checking the backup destination", func() {
		exists, isDir, err := srv.FileSystemExists(ctx, path)
		app.postAndWake(func() {
			if !d.destCheck.Done(tok) || !d.Visible() || d.mode != backupModeForm {
				return
			}
			switch {
			case err != nil || !exists:
				run()
			case isDir:
				d.setStatusMsg("The destination is a folder — add a file name.", true)
			default:
				d.setStatusMsg(d.restingStatus(), false)
				app.confirmDialog.ShowConfirmDefaultNo("Overwrite Backup File",
					path+" already exists on the server. Overwrite it? The backups it holds will be lost.",
					func(yes bool) {
						if yes && d.Visible() && d.mode == backupModeForm {
							run()
						}
					})
			}
		})
	})
}

// runBackup switches to the progress view and runs the backup as a background
// Task; with verify, a RESTORE VERIFYONLY runs as part of the same task.
func (d *BackupDialog) runBackup(opts gosmo.BackupOptions, verify bool) {
	dest := opts.Devices[0]

	task, ctx := d.app.startTask(d.sc.Server.Context(), "Backup "+opts.Database)
	d.task = task
	d.taskDB = opts.Database
	d.taskType = backupTypeLabel(opts.Action)
	d.taskDest = dest.String()
	d.mode = backupModeProgress
	d.btnFocus = 0
	d.SetTitle("Backup Database - Progress")

	app, srv := d.app, d.sc.Server
	// safegoRepair: the Task is latched running above, and only postTaskDone
	// finishes it. A panic past that leaves a task pruneFinishedTasks never
	// evicts, so the status bar counts it as in flight for the rest of the
	// session and the dialog sits in the progress view with a dead Cancel.
	app.safegoRepair("the backup", func() { app.markTaskDone(task, errTaskPanicked) }, func() {
		opts.Progress = func(pct int, msg string) { app.postProgress(task, pct, msg) }
		err := srv.Backup(ctx, opts)
		if err == nil && verify {
			app.postProgress(task, -1, "Verifying backup...")
			// Init is always set, so the file holds this one backup: set 1.
			err = srv.VerifyBackup(ctx, gosmo.VerifyOptions{Checksum: opts.Checksum}, dest)
		}
		app.postTaskDone(task, err)
	})
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
		d.script()
	case 2:
		d.validate()
	case 3:
		d.Hide()
	}
}

func (d *BackupDialog) doProgressButton() {
	runProgressButton(d.task, d.btnFocus, d.Hide)
}

// FocusedClipboardTarget implements core.ClipboardHost: the destination path
// field while the option form is showing and has it focused. The progress view
// is buttons and a log, and drives its own btnFocus rather than focusable, so
// it answers nil.
func (d *BackupDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if d.mode != backupModeForm || d.onButtons {
		return nil
	}
	return focusedClipboardTarget(d.focusable, d.focusIdx)
}
