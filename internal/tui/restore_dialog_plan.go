package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// planEntryLabel is the Backup Set dropdown's first entry when the history holds
// a full backup: restore the database from the plan gosmo.PlanRestore builds
// rather than from one set.
const planEntryLabel = "Restore plan: newest full, differential and logs"

// planPicked reports whether the form restores a plan: the Backup History source
// with the plan entry picked.
func (d *RestoreDialog) planPicked() bool {
	return d.rbSource.Selected() == 1 && d.planOffered && d.ddHistSet.Selected() == 0
}

// historyPick is the history entry the Backup Set dropdown has picked, nil for
// none or for the plan entry.
func (d *RestoreDialog) historyPick() *gosmo.BackupInfo {
	i := d.ddHistSet.Selected()
	if d.planOffered {
		i--
	}
	if i < 0 || i >= len(d.history) {
		return nil
	}
	return d.history[i]
}

// previewPlan plans the restore from the history loaded when the database was
// picked, with the form's point in time and tail-log choice. It is what the
// status line describes and what Analyze and Files show (the plan's full backup,
// the one set that places files). The restore itself plans again from a fresh
// read (planSteps): a log backup taken since the dialog loaded would otherwise be
// missing from the chain, and a tail log restored after it would fail.
func (d *RestoreDialog) previewPlan() (*gosmo.RestorePlan, error) {
	stopAt, _ := parseStopAt(d.fStopAt.Value())
	return gosmo.PlanRestore(d.allHistory, gosmo.RestorePlanOptions{StopAt: stopAt, TailLog: d.cbTail.Checked()})
}

// planSummary is the status line for a picked plan: its sets, or why there is
// none.
func (d *RestoreDialog) planSummary() string {
	plan, err := d.previewPlan()
	if err != nil {
		return "Restore plan: " + planError(err)
	}
	var parts []string
	logs := 0
	for _, b := range plan.Sets {
		switch b.SetType {
		case gosmo.BackupSetLog:
			logs++
		case gosmo.BackupSetDatabase:
			parts = append(parts, "full "+b.BackupFinish.Format("2006-01-02 15:04"))
		default:
			parts = append(parts, strings.ToLower(backupSetTypeName(b.SetType))+" "+b.BackupFinish.Format("2006-01-02 15:04"))
		}
	}
	if logs > 0 {
		parts = append(parts, fmt.Sprintf("%d log%s", logs, plural(logs)))
	}
	if plan.TailLog {
		parts = append(parts, "the tail log")
	}
	return "Restore plan: " + strings.Join(parts, ", ") + "."
}

// planError is a PlanRestore refusal without gosmo's prefix, for the status line.
func planError(err error) string {
	return strings.TrimPrefix(err.Error(), "gosmo: plan restore: ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// tailLogDevice is where the tail-log backup of dbName goes: SSMS's
// <db>_LogBackup_<timestamp>.bak in the server's default backup folder, the folder
// BACKUP writes to. A new file per restore, so it holds one set.
func tailLogDevice(backupDir, dbName string, now time.Time) string {
	return gosmo.JoinServerPath(backupDir, dbName+"_LogBackup_"+now.Format("2006-01-02_15-04-05")+".bak")
}

// checkPlanFields refuses, before any read, a plan the form cannot run: none to
// preview, or a tail-log backup of a database other than the target. The tail is
// backed up WITH NORECOVERY, leaving its database RESTORING; only a restore over
// that same database brings it back.
func (d *RestoreDialog) checkPlanFields(target string) bool {
	if _, err := d.previewPlan(); err != nil {
		d.setStatusMsg("Restore plan: "+planError(err), true)
		return false
	}
	if d.cbTail.Checked() && !gosmo.SameName(serverCollation(d.sc), d.ddHistDB.Value(), target) {
		d.setStatusMsg("A tail-log backup leaves "+d.ddHistDB.Value()+" RESTORING: restore it over itself, or untick the tail log.", true)
		d.focusTo(d.cbTail)
		return false
	}
	return true
}

// planRequest is a restoreRequest's plan: whose history it is read from, and
// where the tail of the log is backed up to ("" for no tail-log backup).
type planRequest struct {
	database   string
	tailDevice string
}

// restoreStep is one RESTORE of a plan: the set, and whether it is the tail log,
// which is not in history when the plan is made and is restored without reading
// its header first (a script names a file that does not exist yet).
type restoreStep struct {
	src  restoreSource
	tail bool
}

// planSteps reads req's database history afresh and plans it: the sets in
// restore order, then the tail log when the plan restores it.
func planSteps(ctx context.Context, srv *gosmo.Server, req restoreRequest) ([]restoreStep, error) {
	hist, err := srv.BackupHistory(ctx, req.plan.database)
	if err != nil {
		return nil, err
	}
	hist, _ = restorableHistory(hist)
	plan, err := gosmo.PlanRestore(hist, gosmo.RestorePlanOptions{StopAt: req.stopAt, TailLog: req.plan.tailDevice != ""})
	if err != nil {
		return nil, errors.New("restore plan: " + planError(err))
	}
	steps := make([]restoreStep, 0, len(plan.Sets)+1)
	for _, b := range plan.Sets {
		steps = append(steps, restoreStep{src: restoreSource{devices: historyDevices(b), setNumber: b.SetNumber()}})
	}
	if plan.TailLog {
		steps = append(steps, restoreStep{src: restoreSource{devices: []string{req.plan.tailDevice}}, tail: true})
	}
	return steps, nil
}

// stepRequest is req for step i of n: every step but the last WITH NORECOVERY,
// the form's recovery choice on the last; Replace and Close existing connections
// on the first, the one that meets the existing database; the point in time on
// every log (a log that ends before it is applied whole).
func stepRequest(req restoreRequest, step restoreStep, i, n int) restoreRequest {
	r := req
	r.src, r.fileNumber, r.plan = step.src, step.src.setNumber, nil
	if i < n-1 {
		r.recovery, r.standbyFile = gosmo.RestoreWithNoRecovery, ""
	}
	if i > 0 {
		r.replace, r.closeConns = false, false
	}
	return r
}

// stepOptions resolves step i of n into its RESTORE: through the header and file
// list a single-set restore reads, or, for the tail log, as a RESTORE LOG of the
// new file's only set.
func stepOptions(ctx context.Context, srv *gosmo.Server, req restoreRequest, step restoreStep, i, n int) (gosmo.RestoreOptions, *gosmo.BackupHeader, error) {
	r := stepRequest(req, step, i, n)
	if step.tail {
		return gosmo.RestoreOptions{
			Database:    r.target,
			Action:      gosmo.BackupActionLog,
			Devices:     r.src.targets(),
			Recovery:    r.recovery,
			StandByFile: r.standbyFile,
			StopAt:      r.stopAt,
		}, nil, nil
	}
	// restoreSet refuses a point in time on a full or differential, rightly for
	// a single set; in a plan the point is the logs', so it is read without one.
	stopAt := r.stopAt
	r.stopAt = nil
	h, err := restoreSet(ctx, srv, r)
	if err != nil {
		return gosmo.RestoreOptions{}, nil, err
	}
	if h.SetType == gosmo.BackupSetLog {
		r.stopAt = stopAt
	}
	opts, err := restoreOptionsFor(ctx, srv, r, h)
	return opts, h, err
}

// tailBackupOptions is the tail-log backup req asks for: BACKUP LOG WITH
// NORECOVERY, which leaves the database RESTORING so nothing commits after it.
func tailBackupOptions(req restoreRequest) gosmo.BackupOptions {
	return gosmo.BackupOptions{
		Database:   req.plan.database,
		Action:     gosmo.BackupActionLog,
		Devices:    []gosmo.BackupTarget{gosmo.DiskTarget(req.plan.tailDevice)},
		NoRecovery: true,
	}
}

// runRestorePlan is runRestore for a plan: plan from a fresh history read, back
// up the tail if asked, then restore each set in order. The plan is made before
// the tail-log backup on purpose: a plan that cannot run is refused while the
// database is still online, not after the tail log has left it RESTORING.
func runRestorePlan(ctx context.Context, app *App, srv *gosmo.Server, task *Task, req restoreRequest) error {
	app.postProgress(task, -1, "Reading backup history...")
	steps, err := planSteps(ctx, srv, req)
	if err != nil {
		return err
	}
	if req.plan.tailDevice != "" {
		app.postProgress(task, -1, "Backing up the tail of the log...")
		opts := tailBackupOptions(req)
		opts.Progress = func(pct int, msg string) { app.postProgress(task, pct, msg) }
		if err := srv.Backup(ctx, opts); err != nil {
			return err
		}
	}
	for i, step := range steps {
		opts, h, err := stepOptions(ctx, srv, req, step, i, len(steps))
		if err != nil {
			return err
		}
		kind := "tail log"
		if h != nil {
			kind = strings.ToLower(backupSetTypeName(h.SetType))
		}
		if req.verify {
			app.postProgress(task, -1, fmt.Sprintf("Verifying set %d of %d (%s)...", i+1, len(steps), kind))
			vopts := gosmo.VerifyOptions{FileNumber: step.src.setNumber}
			if h != nil {
				vopts.Checksum = h.HasChecksums
			}
			if err := srv.VerifyBackup(ctx, vopts, step.src.targets()...); err != nil {
				return err
			}
		}
		app.postProgress(task, -1, fmt.Sprintf("Restoring set %d of %d (%s)...", i+1, len(steps), kind))
		opts.Progress = func(pct int, msg string) { app.postProgress(task, pct, msg) }
		if err := srv.Restore(ctx, opts); err != nil {
			return err
		}
	}
	return nil
}

// planScript is the T-SQL a plan runs, in order and separated by GO: the
// tail-log backup, then each RESTORE.
func planScript(ctx context.Context, srv *gosmo.Server, req restoreRequest) (string, error) {
	steps, err := planSteps(ctx, srv, req)
	if err != nil {
		return "", err
	}
	var stmts []string
	if req.plan.tailDevice != "" {
		stmt, err := srv.BuildBackupStatement(tailBackupOptions(req))
		if err != nil {
			return "", err
		}
		stmts = append(stmts, stmt)
	}
	for i, step := range steps {
		opts, _, err := stepOptions(ctx, srv, req, step, i, len(steps))
		if err != nil {
			return "", err
		}
		stmt, err := srv.BuildRestoreStatement(opts)
		if err != nil {
			return "", err
		}
		stmts = append(stmts, stmt)
	}
	return strings.Join(stmts, "\nGO\n\n") + "\nGO\n", nil
}
