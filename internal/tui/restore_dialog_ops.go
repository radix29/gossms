package tui

import (
	"context"
	"fmt"
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
			cur := d.ddHistDB.Value()
			names := make([]string, len(dbs))
			for i, dbo := range dbs {
				names[i] = dbo.Name()
			}
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
		app.wakeEventLoop()
	}()
}

// loadHistory fetches dbName's msdb backup history into the Backup Set
// dropdown, most recent first, capped at maxHistorySets.
func (d *RestoreDialog) loadHistory(dbName string) {
	d.history = nil
	d.ddHistSet = widgets.NewDropDown("Backup Set: ", nil, 48)
	d.rebuildFocusable()
	if dbName == "" {
		return
	}
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	d.setStatusMsg("Loading backup history for "+dbName+"...", false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		hist, err := sc.Server.BackupHistoryContext(ctx, dbName)
		app.postEvent(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Backup history: %v", err), true)
				return
			}
			if len(hist) > maxHistorySets {
				hist = hist[:maxHistorySets]
			}
			d.history = hist
			labels := make([]string, len(hist))
			for i, b := range hist {
				labels[i] = b.BackupFinish.Format("2006-01-02 15:04") + "  " +
					core.PadRight(backupTypeLabel(b.BackupType), 15) + " " + serverPathBase(b.DeviceName)
			}
			d.ddHistSet = widgets.NewDropDown("Backup Set: ", labels, 48)
			d.rebuildFocusable()
			d.setFocus(d.focusIdx)
			if len(hist) == 0 {
				d.setStatusMsg("No backup history for "+dbName, true)
			} else {
				d.setStatusMsg("Ready", false)
			}
		})
		app.wakeEventLoop()
	}()
}

// deviceForRestore returns the backup device the current form selects: the
// typed file path, or the picked history entry's device.
func (d *RestoreDialog) deviceForRestore() string {
	if d.rbSource.Selected() == 0 {
		return strings.TrimSpace(d.fFile.Value())
	}
	if i := d.ddHistSet.Selected(); i >= 0 && i < len(d.history) {
		return d.history[i].DeviceName
	}
	return ""
}

// analyze reads the backup's header and file list in the background and
// switches to the inspection view (mockup's "Backup Information").
func (d *RestoreDialog) analyze() {
	dev := d.deviceForRestore()
	if dev == "" {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	d.setStatusMsg("Analyzing backup...", false)
	d.loadSeq++
	seq := d.loadSeq
	app, srv := d.app, d.sc.Server
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		headers, err := srv.BackupHeadersContext(ctx, dev)
		var files []*gosmo.BackupFile
		if err == nil {
			files, err = srv.BackupFileListContext(ctx, dev)
		}
		app.postEvent(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(err.Error(), true)
				return
			}
			if len(headers) == 0 {
				d.setStatusMsg("No backup sets found on "+dev, true)
				return
			}
			d.headers, d.files, d.inspectDev = headers, files, dev
			d.autoFillTarget(headers[0].DatabaseName)
			d.mode = restoreModeInspect
			d.btnFocus = 0
			d.SetTitle("Backup Information")
			d.setStatusMsg("Ready", false)
		})
		app.wakeEventLoop()
	}()
}

// startRestore validates the form, then checks whether the target database
// already exists — if so, the restore would overwrite it, so beginRestore
// only runs after confirmOverwrite's typed confirmation. A brand new
// target needs no such gate.
func (d *RestoreDialog) startRestore() {
	dev := d.deviceForRestore()
	target := strings.TrimSpace(d.fTarget.Value())
	if dev == "" {
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		dbs, err := sc.Server.DatabasesContext(ctx)
		app.postEvent(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(fmt.Sprintf("Check target database: %v", err), true)
				return
			}
			d.setStatusMsg("Ready", false)
			exists := false
			for _, dbo := range dbs {
				if strings.EqualFold(dbo.Name(), target) {
					exists = true
					break
				}
			}
			if exists {
				d.confirmOverwrite(target, func() { d.beginRestore(dev, target) })
				return
			}
			d.beginRestore(dev, target)
		})
		app.wakeEventLoop()
	}()
}

// confirmOverwrite gates a restore that would overwrite an existing
// database behind retyping its first 4 characters — SSMS itself just asks
// a plain Yes/No here, but this is a largely irreversible, destructive
// action, so it gets the same extra friction as any other "type to
// confirm" prompt in this app.
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
// background Task — the part of startRestore that actually does the work,
// run either immediately (new target) or once confirmOverwrite's typed
// confirmation succeeds (existing target).
func (d *RestoreDialog) beginRestore(dev, target string) {
	recovery := d.rbRecovery.Selected() == 0
	replace := d.cbReplace.Checked()
	verify := d.cbVerify.Checked()
	closeConns := d.cbClose.Checked()

	task, ctx := d.app.startTask("Restore " + target)
	d.task = task
	d.taskTarget = target
	d.taskSource = serverPathBase(dev)
	d.mode = restoreModeProgress
	d.btnFocus = 0
	d.SetTitle("Restore Database - Progress")

	app, sc := d.app, d.sc
	go func() {
		err := d.runRestore(ctx, task, dev, target, recovery, replace, verify, closeConns)
		if err == nil {
			app.postEvent(func() { app.explorer.RefreshDatabasesFolder(sc) })
		}
		app.postTaskDone(task, err)
	}()
}

// runRestore is the background body of startRestore: verify (optional),
// read metadata, relocate files for a renamed target, close existing
// connections (optional), then the RESTORE itself with progress.
func (d *RestoreDialog) runRestore(ctx context.Context, task *Task, dev, target string, recovery, replace, verify, closeConns bool) error {
	app, srv := d.app, d.sc.Server

	if verify {
		app.postProgress(task, -1, "Verifying backup...")
		if err := srv.VerifyBackupContext(ctx, dev); err != nil {
			return err
		}
	}

	app.postProgress(task, -1, "Reading backup metadata...")
	ropts, err := d.buildRestoreOptions(ctx, dev, target, recovery, replace)
	if err != nil {
		return err
	}

	dbs, err := srv.DatabasesContext(ctx)
	if err != nil {
		return err
	}
	exists := false
	for _, dbo := range dbs {
		if strings.EqualFold(dbo.Name(), target) {
			exists = true
			break
		}
	}

	if closeConns && exists {
		app.postProgress(task, -1, "Closing existing connections...")
		if err := srv.Database(target).SetUserAccessContext(ctx, "SINGLE_USER"); err != nil {
			return err
		}
	}

	app.postProgress(task, -1, "Restoring...")
	ropts.Progress = func(pct int, msg string) { app.postProgress(task, pct, msg) }
	if err := srv.RestoreContext(ctx, ropts); err != nil {
		if closeConns && exists {
			// Best effort: don't leave the still-existing database stuck in
			// SINGLE_USER after a failed restore. Fresh context — ctx may
			// already be cancelled.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
			defer cancel()
			_ = srv.Database(target).SetUserAccessContext(cleanupCtx, "MULTI_USER")
		}
		return err
	}
	return nil
}

// buildRestoreOptions resolves dev/target into a gosmo.RestoreOptions,
// including the file relocation MOVE clauses a renamed target needs — the
// read-only metadata lookup shared by runRestore (which goes on to execute
// the result) and script() (which only renders it as T-SQL for review).
func (d *RestoreDialog) buildRestoreOptions(ctx context.Context, dev, target string, recovery, replace bool) (gosmo.RestoreOptions, error) {
	srv := d.sc.Server
	headers, err := srv.BackupHeadersContext(ctx, dev)
	if err != nil {
		return gosmo.RestoreOptions{}, err
	}
	if len(headers) == 0 {
		return gosmo.RestoreOptions{}, fmt.Errorf("no backup sets found on %s", dev)
	}
	source := headers[0].DatabaseName

	// Restoring under a different name: MOVE every file out of the paths
	// recorded in the backup (still owned by the source database) into the
	// server's default directories, named after the target.
	var relocate []gosmo.RelocateFile
	if !strings.EqualFold(source, target) {
		files, err := srv.BackupFileListContext(ctx, dev)
		if err != nil {
			return gosmo.RestoreOptions{}, err
		}
		info := srv.Info()
		for _, f := range files {
			dir := info.DefaultDataPath
			ext := serverPathExt(f.PhysicalName)
			if f.Type == "L" {
				dir = info.DefaultLogPath
				if ext == "" {
					ext = ".ldf"
				}
			} else if ext == "" {
				ext = ".ndf"
			}
			relocate = append(relocate, gosmo.RelocateFile{
				LogicalName:  f.LogicalName,
				PhysicalName: joinServerPath(dir, target+"_"+f.LogicalName+ext),
			})
		}
	}

	return gosmo.RestoreOptions{
		Database:      target,
		Devices:       []string{dev},
		RelocateFiles: relocate,
		Recovery:      recovery,
		NoRecovery:    !recovery,
		Replace:       replace,
	}, nil
}

// script builds the RESTORE statement's T-SQL — including the same file
// relocation this dialog would perform for a renamed target — and opens it
// in a new query window for review. Only the read-only metadata lookup
// buildRestoreOptions needs (backup headers/file list) touches the server;
// nothing is executed or changed.
func (d *RestoreDialog) script() {
	dev := d.deviceForRestore()
	target := strings.TrimSpace(d.fTarget.Value())
	if dev == "" {
		d.setStatusMsg("Select a backup file or history entry first.", true)
		return
	}
	if target == "" {
		d.setStatusMsg("Target database name is required.", true)
		return
	}
	recovery := d.rbRecovery.Selected() == 0
	replace := d.cbReplace.Checked()

	d.setStatusMsg("Building script...", false)
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		ropts, err := d.buildRestoreOptions(ctx, dev, target, recovery, replace)
		var stmt string
		if err == nil {
			stmt, err = gosmo.BuildRestoreStatement(ropts)
		}
		app.postEvent(func() {
			if seq != d.loadSeq || !d.Visible() {
				return
			}
			if err != nil {
				d.setStatusMsg(err.Error(), true)
				return
			}
			d.setStatusMsg("Ready", false)
			app.openQueryWithText(sc, "", stmt)
		})
		app.wakeEventLoop()
	}()
}
