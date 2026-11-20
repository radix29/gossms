package tui

import (
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/tuikit/loop"
)

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
	d.app.fileDialog().ShowSaveOn(fs, "Backup Destination", start, func(path string) {
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
				app.confirmDialog().ShowConfirmDefaultNo("Overwrite Backup File",
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

	task, ctx := d.app.tasks.Start(d.sc.Server.Context(), "Backup "+opts.Database)
	d.task = task
	d.taskDB = opts.Database
	d.taskType = backupTypeLabel(opts.Action)
	d.taskDest = dest.String()
	d.mode = backupModeProgress
	d.btnFocus = 0
	d.SetTitle("Backup Database - Progress")

	app, srv := d.app, d.sc.Server
	// safegoRepair: the Task is latched running above, and only tasks.Done
	// finishes it. A panic past that leaves a task the history cap never
	// evicts, so the status bar counts it as in flight for the rest of the
	// session and the dialog sits in the progress view with a dead Cancel.
	app.safegoRepair("the backup", func() { app.tasks.Finish(task, loop.ErrTaskPanicked) }, func() {
		opts.Progress = func(pct int, msg string) { app.tasks.Progress(task, pct, msg) }
		err := srv.Backup(ctx, opts)
		if err == nil && verify {
			app.tasks.Progress(task, -1, "Verifying backup...")
			// Init is always set, so the file holds this one backup: set 1.
			err = srv.VerifyBackup(ctx, gosmo.VerifyOptions{Checksum: opts.Checksum}, dest)
		}
		app.tasks.Done(task, err)
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
