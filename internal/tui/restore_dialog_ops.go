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
		defer app.recoverPanic("loading the restore database list")
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		dbs, err := sc.Server.DatabasesContext(ctx)
		app.postAndWake(func() {
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
		defer app.recoverPanic("loading backup history")
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		hist, err := sc.Server.BackupHistoryContext(ctx, dbName)
		app.postAndWake(func() {
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

// selectedHeader returns the backup set the inspect view is showing and the
// restore will target. Never nil once analyze has populated headers — every
// caller runs in restoreModeInspect, which analyze only enters after
// rejecting an empty header list.
func (d *RestoreDialog) selectedHeader() *gosmo.BackupHeader {
	if d.headerIdx < 0 || d.headerIdx >= len(d.headers) {
		return d.headers[0]
	}
	return d.headers[d.headerIdx]
}

// selectHeader moves the inspect view to backup set i, clamped — ←/→ at
// either end stays put rather than wrapping, so holding an arrow can't
// silently cycle past the set the user meant to stop on.
func (d *RestoreDialog) selectHeader(i int) {
	d.headerIdx = core.Clamp(i, 0, len(d.headers)-1)
}

// restoreFileNumber is the WITH FILE = n the restore needs to target the
// selected backup set. Backup sets are numbered from 1 and BackupHeader
// carries its own Position, which is the authoritative number — the slice
// index only matches it when the device's sets are contiguous from 1.
// Zero (no clause) whenever the device holds a single set, which is the
// common case and what SQL Server defaults to anyway.
func (d *RestoreDialog) restoreFileNumber() int {
	if len(d.headers) <= 1 {
		return 0
	}
	h := d.selectedHeader()
	if h.Position > 0 {
		return h.Position
	}
	return d.headerIdx + 1
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
		defer app.recoverPanic("analyzing the backup device")
		ctx, cancel := context.WithTimeout(d.sc.Context(), childFetchTimeout)
		defer cancel()
		headers, err := srv.BackupHeadersContext(ctx, dev)
		var files []*gosmo.BackupFile
		if err == nil {
			files, err = srv.BackupFileListContext(ctx, dev)
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
				d.setStatusMsg("No backup sets found on "+dev, true)
				return
			}
			d.headers, d.files, d.inspectDev = headers, files, dev
			d.headerIdx = 0
			d.autoFillTarget(headers[0].DatabaseName)
			d.mode = restoreModeInspect
			d.btnFocus = 0
			d.SetTitle("Backup Information")
			d.setStatusMsg("Ready", false)
		})
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
		defer app.recoverPanic("preparing the restore")
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		dbs, err := sc.Server.DatabasesContext(ctx)
		app.postAndWake(func() {
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
	}()
}

// confirmOverwrite gates a restore that would overwrite an existing
// database behind retyping its first 4 characters — a largely
// irreversible, destructive action, so it gets the same friction as every
// other "type to confirm" prompt in this app.
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
	// Snapshotted here, on the UI goroutine — runRestore reads it from a
	// background one, where d.headerIdx must not be touched. Zero unless the
	// user analyzed a multi-set device and picked a set.
	fileNumber := d.restoreFileNumber()

	task, ctx := d.app.startTask(d.sc.Context(), "Restore "+target)
	d.task = task
	d.taskTarget = target
	d.taskSource = serverPathBase(dev)
	d.mode = restoreModeProgress
	d.btnFocus = 0
	d.SetTitle("Restore Database - Progress")

	app, sc := d.app, d.sc
	go func() {
		defer app.recoverPanic("the restore")
		err := d.runRestore(ctx, task, dev, target, recovery, replace, verify, closeConns, fileNumber)
		if err == nil {
			app.postAndWake(func() { app.explorer.RefreshDatabasesFolder(sc) })
		}
		app.postTaskDone(task, err)
	}()
}

// runRestore is the background body of startRestore: verify (optional),
// read metadata, relocate files for a renamed target, close existing
// connections (optional), then the RESTORE itself with progress.
func (d *RestoreDialog) runRestore(ctx context.Context, task *Task, dev, target string, recovery, replace, verify, closeConns bool, fileNumber int) error {
	app, srv := d.app, d.sc.Server

	if verify {
		app.postProgress(task, -1, "Verifying backup...")
		if err := srv.VerifyBackupContext(ctx, dev); err != nil {
			return err
		}
	}

	app.postProgress(task, -1, "Reading backup metadata...")
	ropts, err := d.buildRestoreOptions(ctx, dev, target, recovery, replace, fileNumber)
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
			// SINGLE_USER after a failed restore. Fresh timeout off the
			// connection's own context, not ctx (the task's), which may
			// already be cancelled — e.g. by the task's own Cancel button,
			// which doesn't cancel the connection itself.
			cleanupCtx, cancel := context.WithTimeout(d.sc.Context(), childFetchTimeout)
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
//
// fileNumber is the backup set to restore (RESTORE's WITH FILE = n), 0 for
// a device holding only one. It is passed in rather than read off the
// dialog because this runs on a background goroutine: d.headerIdx is UI
// state, so the caller snapshots it via restoreFileNumber before starting.
func (d *RestoreDialog) buildRestoreOptions(ctx context.Context, dev, target string, recovery, replace bool, fileNumber int) (gosmo.RestoreOptions, error) {
	srv := d.sc.Server
	headers, err := srv.BackupHeadersContext(ctx, dev)
	if err != nil {
		return gosmo.RestoreOptions{}, err
	}
	if len(headers) == 0 {
		return gosmo.RestoreOptions{}, fmt.Errorf("no backup sets found on %s", dev)
	}
	// The source name comes from the set actually being restored: a device
	// can hold sets from more than one database, and it is what decides
	// whether the MOVE clauses below are needed at all.
	source := headers[0].DatabaseName
	for _, h := range headers {
		if fileNumber > 0 && h.Position == fileNumber {
			source = h.DatabaseName
			break
		}
	}

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
		FileNumber:    fileNumber,
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
	fileNumber := d.restoreFileNumber() // snapshot on the UI goroutine — see beginRestore

	d.setStatusMsg("Building script...", false)
	d.loadSeq++
	seq := d.loadSeq
	app, sc := d.app, d.sc
	go func() {
		defer app.recoverPanic("scripting the restore")
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		ropts, err := d.buildRestoreOptions(ctx, dev, target, recovery, replace, fileNumber)
		var stmt string
		if err == nil {
			stmt, err = gosmo.BuildRestoreStatement(ropts)
		}
		app.postAndWake(func() {
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
	}()
}
