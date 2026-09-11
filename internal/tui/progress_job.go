package tui

import (
	"context"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// progressRevealDelay is how long the progress dialog holds back before it
// draws — see dialogs.ProgressDialog.RevealDelay. Long enough that a DROP
// answered at once never flashes a box, short enough that a slow one shows
// the dialog before the user wonders whether the Yes registered.
const progressRevealDelay = 250 * time.Millisecond

// The reasons an uninterruptible job's dialog gives for its greyed Cancel.
const (
	uninterruptibleRestore  = "A revert cannot be safely interrupted — stopping it halfway leaves the database restoring."
	uninterruptibleFailover = "A failover cannot be safely interrupted — stopping it halfway can leave the group resolving, with no primary."
)

// progressJob is one long operation run behind the progress dialog: the write
// a confirmation was asked for, from the moment it was answered until the
// server comes back. See App.runWithProgress.
type progressJob struct {
	title   string // the dialog's title, normally the confirmation's
	message string // what the dialog says is happening
	what    string // names the goroutine in a panic report

	// sc is the connection the work runs on; its context is the parent of
	// the one handed to the work.
	sc *db.ServerConn
	// timeout bounds the work; zero is serverWriteTimeout.
	timeout time.Duration

	// uninterruptible, when set, greys Cancel and is the reason the dialog
	// gives for it. For a statement that stopping halfway leaves worse off
	// than waiting — a revert to a snapshot, a failover.
	uninterruptible string

	// repair, when set, runs on the UI goroutine if the work panics, after
	// the dialog has been released — for a caller that latched a busy flag of
	// its own before starting the job (see safegoRepair).
	repair func()
}

// progressReport replaces the dialog's message from the work goroutine — the
// object a batch has reached. Safe to call from any goroutine.
type progressReport func(message string)

// runWithProgress runs work on a background goroutine behind the progress
// dialog, then hands its error to done on the UI goroutine once the dialog
// has closed.
//
// The dialog is modal and stays up until work returns: Cancel cancels work's
// context and leaves the dialog saying so, because only the returned error
// says how far the operation got. done's cancelled is true when the user
// pressed Cancel and work failed — a run that finished before the cancel
// reached the server reports its success, since that is what happened.
//
// The context, the spinner's clock and the dialog are all released by this
// function whatever work does, a panic included.
func (a *App) runWithProgress(job progressJob, work func(ctx context.Context, report progressReport) error, done func(err error, cancelled bool)) {
	if a.progressBusy {
		// Unreachable from the keyboard — the dialog takes every key while a
		// job runs — so this is a job started from a callback, and running it
		// unseen behind another job's dialog is the one thing not to do.
		a.setStatus("Another operation is still running — try again when it finishes")
		return
	}
	timeout := job.timeout
	if timeout == 0 {
		timeout = serverWriteTimeout
	}
	ctx, stop := context.WithTimeout(job.sc.Context(), timeout)

	a.progressBusy = true
	a.progressSeq++
	seq := a.progressSeq
	dlg := a.progressDialog
	if job.uninterruptible != "" {
		dlg.ShowUninterruptible(job.title, job.message, job.uninterruptible)
	} else {
		dlg.ShowProgress(job.title, job.message, stop)
	}
	report := func(message string) {
		a.postAndWake(func() {
			if a.progressSeq == seq && a.progressBusy {
				dlg.SetMessage(message)
			}
		})
	}
	release := func() {
		a.progressBusy = false
		dlg.Hide()
	}

	finished := make(chan struct{})
	a.animateUntil("animating the progress dialog", dlg.Spinner.Period, finished)
	a.safegoRepair(job.what, func() {
		release()
		if job.repair != nil {
			job.repair()
		}
	}, func() {
		defer close(finished)
		defer stop()
		err := work(ctx, report)
		a.postAndWake(func() {
			cancelled := err != nil && dlg.Cancelling()
			release()
			done(err, cancelled)
		})
	})
}
