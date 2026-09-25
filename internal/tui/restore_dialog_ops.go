package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// loadHistoryDatabases fetches the server's database list for the history
// Database dropdown, then loads the selected database's backup history.
func (d *RestoreDialog) loadHistoryDatabases() {
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	app.safego("loading the restore database list", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		// Every database, offline ones included: this dropdown picks whose backup
		// *history* to read out of msdb, which outlives the database's current
		// state — see databaseNames.
		names, err := databaseNames(ctx, sc)
		app.postAndWake(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Load databases: %v", err), true)
				return
			}
			cur := d.ddHistDB.Value()
			dd := widgets.NewDropDown("Database:   ", names, 40)
			for i, n := range names {
				if n == cur {
					dd.SetSelected(i)
					break
				}
			}
			d.ddHistDB = dd
			d.rebuildFocusable()
			d.setFocus(d.focusIdx)
			d.prevHistDB = d.ddHistDB.Value()
			d.loadHistory(d.prevHistDB)
			d.autoFillTarget(d.prevHistDB)
		})
	})
}

// loadHistory fetches dbName's msdb backup history into the Backup Set
// dropdown, most recent first, capped at maxHistorySets.
func (d *RestoreDialog) loadHistory(dbName string) {
	d.history = nil
	d.ddHistSet = widgets.NewDropDown("Backup Set: ", nil, histSetWidth)
	d.rebuildFocusable()
	if dbName == "" {
		return
	}
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	d.setStatusMsg("Loading backup history for "+dbName+"...", false)
	app.safego("loading backup history", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		hist, err := sc.Server.BackupHistory(ctx, dbName)
		app.postAndWake(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Backup history: %v", err), true)
				return
			}
			hist, deviceless := restorableHistory(hist)
			if len(hist) > maxHistorySets {
				hist = hist[:maxHistorySets]
			}
			d.history = hist
			labels := make([]string, len(hist))
			for i, b := range hist {
				labels[i] = b.BackupFinish.Format("2006-01-02 15:04") + "  " +
					core.PadRight(backupTypeLabel(b.BackupType), 15) + " " + historyDeviceLabel(historyDevices(b))
			}
			d.ddHistSet = widgets.NewDropDown("Backup Set: ", labels, histSetWidth)
			d.rebuildFocusable()
			d.setFocus(d.focusIdx)
			if len(hist) == 0 && deviceless > 0 {
				// Form mode wraps the status to two lines; keep it inside them.
				d.setStatusMsg(fmt.Sprintf("No restorable backups for %s — its %d are automated (no device); "+
					"use Azure point-in-time restore.", dbName, deviceless), true)
			} else if len(hist) == 0 {
				d.setStatusMsg("No backup history for "+dbName, true)
			} else {
				d.setStatusMsg(d.restingStatus(), false)
			}
		})
	})
}

// restorableHistory drops the history entries no RESTORE can name, returning
// the rest in order and how many were dropped.
//
// A Managed Instance's automated backups are recorded in msdb with a NULL
// physical_device_name (gosmo reads it as ""), and on t-qmi-01 they were every
// row of every database's history. Listed, each one is a Backup Set entry that
// restores from nothing — and the cap in loadHistory would let them crowd out
// the user's own URL backups. Those backups are restored through the control
// plane (point-in-time restore), not a RESTORE statement. Filtered here rather
// than in gosmo: the Backup History viewer and Database Properties list the
// same rows, and there they are true history.
func restorableHistory(hist []*gosmo.BackupInfo) ([]*gosmo.BackupInfo, int) {
	kept := slices.DeleteFunc(slices.Clone(hist), func(b *gosmo.BackupInfo) bool {
		return strings.TrimSpace(b.DeviceName) == ""
	})
	return kept, len(hist) - len(kept)
}

// restoreSource is the backup the form names: every device it is read from,
// and where on them the set sits.
//
// devices is one path for a plain backup and every stripe of a striped one —
// a RESTORE, and each read the dialog makes before it, has to name them all,
// and one stripe alone is refused ("The media set has 2 media families but
// only 1 are provided").
//
// position is the history entry's backupset.position, 0 for a typed path. A
// file written with NOINIT holds one set per backup appended to it, and a
// RESTORE that leaves WITH FILE off reads set 1 — the oldest — so picking
// the newest history entry restored the oldest backup in the file.
type restoreSource struct {
	devices  []string
	position int
}

// first is the source's first device, "" for none — what the URL and Browse
// rules look at, since a striped set's stripes share one kind.
func (s restoreSource) first() string {
	if len(s.devices) == 0 {
		return ""
	}
	return s.devices[0]
}

// targets is the source as RESTORE's FROM list.
func (s restoreSource) targets() []gosmo.BackupTarget {
	t := make([]gosmo.BackupTarget, len(s.devices))
	for i, dev := range s.devices {
		t[i] = gosmo.DiskTarget(dev)
	}
	return t
}

// sourceLabel names devices for display: the first one's file name, and how
// many files a striped set spans.
func sourceLabel(devices []string) string {
	if len(devices) == 0 {
		return ""
	}
	label := serverPathBase(devices[0])
	if len(devices) > 1 {
		label += fmt.Sprintf(" (%d files)", len(devices))
	}
	return label
}

// historyDeviceLabel is sourceLabel for a Backup Set dropdown entry, where
// the file name comes last and is clipped first: the file count goes in
// front of it, so a striped set still says so when its name does not fit.
func historyDeviceLabel(devices []string) string {
	if len(devices) > 1 {
		return fmt.Sprintf("[%d files] %s", len(devices), serverPathBase(devices[0]))
	}
	return sourceLabel(devices)
}

// historyDevices is b's device list, falling back to DeviceName for an entry
// built with only that.
func historyDevices(b *gosmo.BackupInfo) []string {
	if len(b.Devices) > 0 {
		return b.Devices
	}
	if b.DeviceName != "" {
		return []string{b.DeviceName}
	}
	return nil
}

// sourceForRestore returns the backup the current form selects: the typed
// file path, or the picked history entry's devices and position.
func (d *RestoreDialog) sourceForRestore() restoreSource {
	if d.rbSource.Selected() == 0 {
		if dev := strings.TrimSpace(d.fFile.Value()); dev != "" {
			return restoreSource{devices: []string{dev}}
		}
		return restoreSource{}
	}
	if i := d.ddHistSet.Selected(); i >= 0 && i < len(d.history) {
		b := d.history[i]
		return restoreSource{devices: historyDevices(b), position: b.Position}
	}
	return restoreSource{}
}

// deviceForRestore returns the first device of the backup the form selects,
// "" for none.
func (d *RestoreDialog) deviceForRestore() string { return d.sourceForRestore().first() }

// selectedHeader returns the backup set the inspect view is showing and the
// restore will target, or nil when there are no headers. The nil return is not
// decoration: this runs on the UI goroutine, where recoverPanic cannot reach, so
// an index into an empty slice takes the process down with the terminal still on
// the alternate screen. A stale headerIdx falls back to the first set.
func (d *RestoreDialog) selectedHeader() *gosmo.BackupHeader {
	if len(d.headers) == 0 {
		return nil
	}
	if d.headerIdx < 0 || d.headerIdx >= len(d.headers) {
		return d.headers[0]
	}
	return d.headers[d.headerIdx]
}

// selectHeader moves the inspect view to backup set i, clamped: ←/→ at either
// end stays put rather than wrapping, so holding an arrow can't cycle past the
// set the user meant to stop on.
//
// Each set on a device has its own file list, so moving between them re-reads
// it. Without that the Files Included panel shows the first set's files under a
// header naming a different one.
func (d *RestoreDialog) selectHeader(i int) {
	was := d.headerIdx
	d.headerIdx = core.Clamp(i, 0, len(d.headers)-1)
	if d.headerIdx != was {
		d.loadFileList()
	}
}

// loadFileList re-reads the selected backup set's file list in the background.
// Held ←/→ fires one per set crossed, and loadSeq drops every answer but the
// newest.
func (d *RestoreDialog) loadFileList() {
	src, fileNumber := restoreSource{devices: d.inspectDevs}, d.restoreFileNumber()
	if len(src.devices) == 0 {
		return
	}
	d.loadSeq++
	seq := d.loadSeq
	// Captured here, never read as d.sc inside the goroutine: show() replaces
	// d.sc when the dialog is reopened, on another server as like as not.
	app, sc := d.app, d.sc
	app.safego("reading a backup set's file list", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		files, err := sc.Server.BackupFileList(ctx, fileNumber, src.targets()...)
		app.postAndWake(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(err.Error(), true)
				return
			}
			d.files = files
		})
	})
}

// backupSetNumber is the WITH FILE = n that targets headers[i].
//
// RESTORE numbers backup sets by each set's own Position, so that is what is
// sent; the slice index coincides with it only when the device's sets run
// contiguously from 1, and index+1 is the fallback for a header that reported no
// position.
//
// Zero means no clause, which SQL Server reads as the first set. A lone set gets
// zero, so the common single-backup device produces a statement without a
// redundant WITH FILE = 1. That is safe because a device holding one set always
// holds it at position 1: appending numbers sets 1..n, and WITH INIT and WITH
// FORMAT both reinitialise the media set to a single set at 1.
//
// Every path that names a set goes through here — the restore, the MOVE clauses
// built from that set's file list, and the Files Included panel. They must
// agree: a file list read from a different set names logical files the restored
// set does not contain, which fails the whole RESTORE.
func backupSetNumber(headers []*gosmo.BackupHeader, i int) int {
	if len(headers) <= 1 {
		return 0
	}
	if i < 0 || i >= len(headers) {
		i = 0
	}
	if p := headers[i].Position; p > 0 {
		return p
	}
	return i + 1
}

// restoreFileNumber is the WITH FILE = n for the set the inspect view is
// showing — see backupSetNumber for the rule.
func (d *RestoreDialog) restoreFileNumber() int {
	return backupSetNumber(d.headers, d.headerIdx)
}

// fileNumberFor is the WITH FILE = n a restore of src sends: the set the
// inspect view is showing when src is the backup it analyzed, or else the
// history entry's own position.
//
// The inspect view's set only counts for the devices it was read from.
// Analyze a multi-set file, move to set 3, then pick a history entry on
// another file, and the restore used to send WITH FILE = 3 against a file
// that may not have a third set, or has a different backup there.
//
// A position of 1 sends no clause, by backupSetNumber's rule: set 1 is what
// SQL Server reads without one.
func (d *RestoreDialog) fileNumberFor(src restoreSource) int {
	if len(d.headers) > 0 && len(src.devices) > 0 && slices.Equal(d.inspectDevs, src.devices) {
		return d.restoreFileNumber()
	}
	if src.position > 1 {
		return src.position
	}
	return 0
}

// headerAt is the index of the header at position, 0 when none is there or
// position is unknown — where the inspect view opens for a history entry.
func headerAt(headers []*gosmo.BackupHeader, position int) int {
	if position > 0 {
		for i, h := range headers {
			if h.Position == position {
				return i
			}
		}
	}
	return 0
}

// analyze reads the backup's header and file list in the background and
// switches to the inspection view (mockup's "Backup Information").
func (d *RestoreDialog) analyze() { d.loadBackupInfo(restoreModeInspect) }

// loadBackupInfo reads the backup's header and file list in the background,
// then switches to next — the inspection view, or the Files view, both of
// which render that same data.
func (d *RestoreDialog) loadBackupInfo(next int) {
	src := d.sourceForRestore()
	if len(src.devices) == 0 {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	d.setStatusMsg("Analyzing backup...", false)
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc // see loadFileList
	app.safego("analyzing the backup device", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		headers, err := sc.Server.BackupHeaders(ctx, src.targets()...)
		// A history entry opens the view on its own set, a typed path on the
		// first: the entry the user picked is the backup they meant.
		idx := headerAt(headers, src.position)
		var files []*gosmo.BackupFile
		if err == nil && len(headers) > 0 {
			// The file list must name the set the view opens on, by the same
			// rule selectHeader's reload uses — otherwise the panel disagrees
			// with itself the moment the user arrows off and back.
			files, err = sc.Server.BackupFileList(ctx, backupSetNumber(headers, idx), src.targets()...)
		}
		app.postAndWake(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(err.Error(), true)
				return
			}
			if len(headers) == 0 {
				d.setStatusMsg("No backup sets found on "+sourceLabel(src.devices), true)
				return
			}
			d.headers, d.files, d.inspectDevs = headers, files, src.devices
			d.headerIdx = idx
			d.autoFillTarget(headers[idx].DatabaseName)
			d.setStatusMsg(d.restingStatus(), false)
			if next == restoreModeFiles {
				d.enterFilesMode()
				return
			}
			d.mode = restoreModeInspect
			d.btnFocus = 0
			d.SetTitle("Backup Information")
		})
	})
}

// startRestore validates the form, then checks whether the target database
// already exists: if so the restore would overwrite it, so beginRestore only
// runs after confirmOverwrite's typed confirmation.
func (d *RestoreDialog) startRestore() {
	src := d.sourceForRestore()
	target := strings.TrimSpace(d.fTarget.Value())
	if len(src.devices) == 0 {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	if target == "" {
		d.setStatusMsg("Target database name is required.", true)
		return
	}

	d.setStatusMsg("Checking target database...", false)
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	app.safego("preparing the restore", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		dbs, err := sc.Server.Databases(ctx)
		app.postAndWake(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Check target database: %v", err), true)
				return
			}
			d.setStatusMsg(d.restingStatus(), false)
			existing := newNameSet(serverCollation(sc))
			for _, dbo := range dbs {
				existing.Add(dbo.Name)
			}
			if existing.Has(target) {
				d.confirmOverwrite(target, func() { d.beginRestore(src, target) })
				return
			}
			d.beginRestore(src, target)
		})
	})
}

// confirmOverwrite gates a restore that would overwrite an existing database
// behind retyping its first 4 characters. Shorter than the Always On prompts,
// which ask for the whole name: a target typed into this dialog is already in
// front of the user, so the prompt is a pause rather than a transcription.
func (d *RestoreDialog) confirmOverwrite(target string, proceed func()) {
	runes := []rune(target)
	prefix := target
	if len(runes) > 4 {
		prefix = string(runes[:4])
	}
	d.app.confirmTypedDialog.ShowTypedConfirm(
		"Confirm Overwrite",
		fmt.Sprintf("Database %q already exists. Restoring will overwrite it.", target),
		prefix,
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		},
	)
}

// beginRestore switches to the progress view and runs the restore as a
// background Task — startRestore's working half, run immediately for a new
// target or after confirmOverwrite for an existing one.
func (d *RestoreDialog) beginRestore(src restoreSource, target string) {
	// Snapshotted on the UI goroutine: runRestore reads them from a background
	// one, where the dialog's state must not be touched.
	req := d.restoreRequest(src, target)
	req.verify = d.cbVerify.Checked()

	app, sc := d.app, d.sc
	task, ctx := app.startTask(sc.Context(), "Restore "+target)
	d.task = task
	d.taskTarget = target
	d.taskSource = sourceLabel(src.devices)
	d.mode = restoreModeProgress
	d.btnFocus = 0
	d.SetTitle("Restore Database - Progress")

	// safegoRepair for the same reason as startBackup's.
	app.safegoRepair("the restore", func() { app.markTaskDone(task, errTaskPanicked) }, func() {
		err := runRestore(ctx, app, sc.Server, task, req)
		if err == nil {
			app.postAndWake(func() { app.explorer.RefreshDatabasesFolder(sc) })
		}
		app.postTaskDone(task, err)
	})
}

// restoreRequest is everything a restore reads off the form, snapshotted on
// the UI goroutine for runRestore and script(), which run on another.
type restoreRequest struct {
	src                                   restoreSource
	target                                string
	recovery, replace, verify, closeConns bool
	// fileNumber is the backup set to restore (WITH FILE = n), 0 for the
	// first — see fileNumberFor.
	fileNumber int
	plan       relocPlan
	// defData and defLog are the default directories relocAuto moves files
	// to, and an empty folder field falls back to — defaultDirs' answer, so
	// the restore puts files where the Files view previewed them.
	defData, defLog string
}

// restoreRequest snapshots the form for a restore or script of src into
// target. verify is left for the caller: only a real restore verifies.
func (d *RestoreDialog) restoreRequest(src restoreSource, target string) restoreRequest {
	dataDir, logDir := d.defaultDirs()
	return restoreRequest{
		src:        src,
		target:     target,
		recovery:   d.rbRecovery.Selected() == 0,
		replace:    d.cbReplace.Checked(),
		closeConns: d.cbClose.Checked(),
		fileNumber: d.fileNumberFor(src),
		plan:       d.relocation(),
		defData:    dataDir,
		defLog:     logDir,
	}
}

// runRestore is the background body of startRestore: verify (optional),
// read metadata, relocate files for a renamed target, then the RESTORE itself
// with progress — closing existing connections in the same batch when asked.
//
// It is a function of srv, not a method reading d.sc: the restore is a Task
// that outlives Hide, and reopening the dialog on another server replaces
// d.sc while this is still in VerifyBackup. Read through the dialog, the
// headers, file list and default directories came from the second server
// while the RESTORE ran on the first — MOVE clauses naming the wrong
// server's folders.
func runRestore(ctx context.Context, app *App, srv *gosmo.Server, task *Task, req restoreRequest) error {
	if req.verify {
		app.postProgress(task, -1, "Verifying backup...")
		if err := srv.VerifyBackup(ctx, req.src.targets()...); err != nil {
			return err
		}
	}

	app.postProgress(task, -1, "Reading backup metadata...")
	ropts, err := buildRestoreOptions(ctx, srv, req)
	if err != nil {
		return err
	}

	app.postProgress(task, -1, "Restoring...")
	ropts.Progress = func(pct int, msg string) { app.postProgress(task, pct, msg) }
	return srv.Restore(ctx, ropts)
}

// relocateFiles returns the MOVE clauses that put the backup set's files where
// plan asks, or nil to leave every file at the path recorded in the backup.
// defData/defLog are the server's default directories, used by relocAuto and as
// the fallback for an empty folder field.
//
// Renaming decides the file *names* in every mode that moves anything: restoring
// under a different database name mints "<target>_<logical><ext>", so the copy
// can't collide with the original database's files, while a same-name restore
// keeps the backup's names and changes only the directory.
func relocateFiles(files []*gosmo.BackupFile, plan relocPlan, defData, defLog, source, target string) []gosmo.RelocateFile {
	if !plan.needsFileList(source, target) {
		return nil
	}
	dataDir, logDir := defData, defLog
	if plan.mode == relocFolder {
		if plan.dataDir != "" {
			dataDir = plan.dataDir
		}
		if plan.logDir != "" {
			logDir = plan.logDir
		}
	}
	renamed := !sameName(plan.collation, source, target)

	var relocate []gosmo.RelocateFile
	for _, f := range files {
		dir, ext := dataDir, serverPathExt(f.PhysicalName)
		if f.Type == "L" {
			dir = logDir
			if ext == "" {
				ext = ".ldf"
			}
		} else if ext == "" {
			ext = ".ndf"
		}
		name := serverPathBase(f.PhysicalName)
		if renamed {
			name = target + "_" + f.LogicalName + ext
		}
		relocate = append(relocate, gosmo.RelocateFile{
			LogicalName:  f.LogicalName,
			PhysicalName: joinServerPath(dir, name),
		})
	}
	return relocate
}

// buildRestoreOptions resolves req into a gosmo.RestoreOptions, including the
// MOVE clauses its plan asks for — the read-only metadata lookup shared by
// runRestore, which executes the result, and script(), which only renders it
// as T-SQL. It reads srv only, never the dialog: see runRestore.
//
// closeConns is left to gosmo, which closes the connections in the RESTORE's
// own batch. As a separate SET SINGLE_USER first, the freed slot was anyone's
// until the RESTORE arrived — gossms's own background reads included — and a
// Managed Instance refused the statement outright; gosmo also puts the
// database back to MULTI_USER, including after a cancelled restore.
func buildRestoreOptions(ctx context.Context, srv *gosmo.Server, req restoreRequest) (gosmo.RestoreOptions, error) {
	target, fileNumber, plan := req.target, req.fileNumber, req.plan
	headers, err := srv.BackupHeaders(ctx, req.src.targets()...)
	if err != nil {
		return gosmo.RestoreOptions{}, err
	}
	if len(headers) == 0 {
		return gosmo.RestoreOptions{}, fmt.Errorf("no backup sets found on %s", sourceLabel(req.src.devices))
	}
	// The source name comes from the set actually being restored: a device can
	// hold sets from more than one database, and it decides whether the MOVE
	// clauses below are needed.
	source := headers[0].DatabaseName
	for _, h := range headers {
		if fileNumber > 0 && h.Position == fileNumber {
			source = h.DatabaseName
			break
		}
	}

	var relocate []gosmo.RelocateFile
	if plan.needsFileList(source, target) {
		// The file list must name the same set as FileNumber below. Asking for
		// the device without one describes set 1, whose logical file names
		// belong to a different database whenever backups were appended, and
		// MOVE clauses naming files the restored set lacks fail the RESTORE.
		files, err := srv.BackupFileList(ctx, fileNumber, req.src.targets()...)
		if err != nil {
			return gosmo.RestoreOptions{}, err
		}
		relocate = relocateFiles(files, plan, req.defData, req.defLog, source, target)
	}

	return gosmo.RestoreOptions{
		Database:      target,
		Devices:       req.src.targets(),
		FileNumber:    fileNumber,
		RelocateFiles: relocate,
		Recovery:      recoveryFor(req.recovery),
		Replace:       req.replace,

		CloseExistingConnections: req.closeConns,
	}, nil
}

// recoveryFor maps the dialog's two-way Recovery Options radio box onto
// gosmo's recovery state.
func recoveryFor(recovery bool) gosmo.RestoreRecovery {
	if recovery {
		return gosmo.RestoreWithRecovery
	}
	return gosmo.RestoreWithNoRecovery
}

// script builds the RESTORE statement's T-SQL, including the file relocation
// this dialog would perform for a renamed target, and opens it in a new query
// window. Only buildRestoreOptions' read-only metadata lookup touches the
// server.
func (d *RestoreDialog) script() {
	src := d.sourceForRestore()
	target := strings.TrimSpace(d.fTarget.Value())
	if len(src.devices) == 0 {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	if target == "" {
		d.setStatusMsg("Target database name is required.", true)
		return
	}
	req := d.restoreRequest(src, target) // snapshots on the UI goroutine — see beginRestore

	d.setStatusMsg("Building script...", false)
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	app.safego("scripting the restore", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		ropts, err := buildRestoreOptions(ctx, sc.Server, req)
		var stmt string
		if err == nil {
			// The instance's own form: on a Managed Instance, closing
			// connections is KILLs rather than the SINGLE_USER it refuses.
			stmt, err = sc.Server.BuildRestoreStatement(ropts)
		}
		app.postAndWake(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(err.Error(), true)
				return
			}
			d.setStatusMsg(d.restingStatus(), false)
			app.openQueryWithText(sc, "", stmt)
		})
	})
}
