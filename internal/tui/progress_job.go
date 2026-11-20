package tui

import (
	"context"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/dialogs"
)

// progressRevealDelay is how long the progress dialog holds back before it
// draws (dialogs.ProgressDialog.RevealDelay). Long enough that a DROP answered
// at once never flashes a box, short enough that a slow one shows the dialog
// before the user wonders whether the Yes registered.
const progressRevealDelay = 250 * time.Millisecond

// The reasons an uninterruptible job's dialog gives for its greyed Cancel.
const (
	uninterruptibleRestore  = "A revert cannot be safely interrupted — stopping it halfway leaves the database restoring."
	uninterruptibleFailover = "A failover cannot be safely interrupted — stopping it halfway can leave the group resolving, with no primary."
)

// progressJob is one long operation run behind the progress dialog: the write a
// confirmation was asked for, from the moment it was answered until the server
// comes back. See App.runWithProgress.
type progressJob struct {
	title   string // the dialog's title, normally the confirmation's
	message string // what the dialog says is happening
	what    string // names the goroutine in a panic report

	// sc is the connection the work runs on; its context is the parent of the one
	// handed to the work.
	sc *db.ServerConn
	// timeout bounds the work; zero is serverWriteTimeout.
	timeout time.Duration

	// uninterruptible, when set, greys Cancel and is the reason the dialog gives. For
	// a statement that stopping halfway leaves worse off than waiting (a revert to a
	// snapshot, a failover).
	uninterruptible string

	// repair, when set, runs on the UI goroutine if the work panics, after the dialog
	// has been released, or if the job is refused: for a caller that latched a busy
	// flag of its own before starting the job (dialogs.ProgressJob.Repair).
	repair func()
}

// progressReport replaces the dialog's message from the work goroutine (the
// object a batch has reached). Safe from any goroutine.
type progressReport = dialogs.ProgressReport

// runWithProgress runs work on a background goroutine behind the progress dialog,
// then hands its error to done on the UI goroutine once the dialog has closed.
// The lifecycle — Cancel leaving the dialog up until work returns, done's
// cancelled true only when Cancel was pressed *and* work failed, the release on
// a panic, a late report dropped, the spinner's clock — is
// dialogs.ProgressDialog.Run. What stays here is the context's parent and
// timeout, and the refusal's status text.
func (a *App) runWithProgress(job progressJob, work func(ctx context.Context, report progressReport) error, done func(err error, cancelled bool)) {
	timeout := job.timeout
	if timeout == 0 {
		timeout = serverWriteTimeout
	}
	ctx, stop := context.WithTimeout(job.sc.Server.Context(), timeout)
	if !a.progressDialog().Run(ctx, stop, a.runner(), &a.wake, dialogs.ProgressJob{
		Title: job.title, Message: job.message, What: job.what,
		Uninterruptible: job.uninterruptible, Repair: job.repair,
	}, work, done) {
		// Unreachable from the keyboard (the dialog takes every key while a job runs),
		// so this is a job started from a callback, and running it unseen behind another
		// job's dialog is the one thing not to do. Run has already run job.repair.
		a.setStatus("Another operation is still running — try again when it finishes")
	}
}
