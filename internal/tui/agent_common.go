package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_common.go holds small helpers shared across the SQL Server Agent
// tree/detail/menu files: string formatters for gosmo's Agent enums, and
// the generic async enable/disable/delete plumbing every Agent entity type
// (Job, Schedule, Alert, Operator) shares.

// formatJobState renders a gosmo.JobState for display.
//
// The states come from SQL Server Agent itself (gosmo.JobState); when that
// read is unavailable — Agent stopped, or a login with neither sysadmin nor
// SQLAgentReaderRole — gosmo falls back to a derivation that can only say
// Executing or Idle, so those two are what a restricted login sees.
func formatJobState(s gosmo.JobState) string {
	switch s {
	case gosmo.JobStateIdle:
		return "Idle"
	case gosmo.JobStateSuspended:
		return "Suspended"
	case gosmo.JobStateExecuting:
		return "Running"
	case gosmo.JobStateWaitingForWorker:
		return "Waiting for worker thread"
	case gosmo.JobStateBetweenRetries:
		return "Between retries"
	case gosmo.JobStateWaitingForStepToFinish:
		return "Waiting for step to finish"
	case gosmo.JobStatePerformingCompletionActions:
		return "Performing completion actions"
	default:
		return "Unknown"
	}
}

// formatJobOutcome renders a gosmo.JobOutcome for display.
func formatJobOutcome(o gosmo.JobOutcome) string {
	switch o {
	case gosmo.JobOutcomeFailed:
		return "Failed"
	case gosmo.JobOutcomeSucceeded:
		return "Succeeded"
	case gosmo.JobOutcomeRetried:
		return "Retrying"
	case gosmo.JobOutcomeCancelled:
		return "Cancelled"
	default:
		return "Unknown"
	}
}

// formatNotifyLevel renders a gosmo.NotifyLevel for display.
func formatNotifyLevel(n gosmo.NotifyLevel) string {
	switch n {
	case gosmo.NotifyNever:
		return "Never"
	case gosmo.NotifyOnSuccess:
		return "When the job succeeds"
	case gosmo.NotifyOnFailure:
		return "When the job fails"
	case gosmo.NotifyOnComplete:
		return "When the job completes"
	default:
		return "Unknown"
	}
}

// setAgentEnabled runs run (a gosmo Enable/Disable call) behind the progress
// dialog, then updates node's cached IsEnabled flag and redraws the tree and
// detail view on success — the shared body behind every Agent entity's
// Enable/Disable toggle. noun is the entity's lower-case name ("job",
// "schedule", …) for the dialog and the status line.
//
// Neither direction is confirmed, as in SSMS; the progress dialog's reveal
// delay keeps a fast toggle invisible, and one waiting on a lock gets a
// spinner and Cancel instead of leaving the tree live under it.
func (a *App) setAgentEnabled(sc *db.ServerConn, node *explorerNode, noun string, enable bool, run func(ctx context.Context) error) {
	if !a.requireConn(sc) {
		return
	}
	word, doing, title := "disable", "Disabling", "Disable "
	if enable {
		word, doing, title = "enable", "Enabling", "Enable "
	}
	Noun := strings.ToUpper(noun[:1]) + noun[1:]
	a.runWithProgress(progressJob{
		title:   title + Noun,
		message: fmt.Sprintf("%s %s %q...", doing, noun, node.label),
		what:    "enabling/disabling an Agent " + noun,
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		return run(ctx)
	}, func(err error, cancelled bool) {
		switch {
		case cancelled:
			// The state is re-read rather than assumed: the cancel may have
			// reached the server after the change had committed.
			a.setStatus(fmt.Sprintf("%s %q cancelled", doing, node.label))
			if parent := node.parent; parent != nil {
				a.explorer.Reload(parent)
			}
		case err != nil:
			a.setStatus(fmt.Sprintf("Failed to %s %q: %v", word, node.label, err))
		default:
			node.data.IsEnabled = enable
			a.explorer.rebuild()
			a.detailBrowser.Invalidate(a, node)
			a.setStatus(fmt.Sprintf("%s %q is now %sd", Noun, node.label, word))
		}
	})
}

// deleteAgentEntity confirms with the user, then runs run (a gosmo Drop/
// Delete call) on a background goroutine — the shared body behind every
// Agent entity's Delete action. On success the parent folder is refreshed
// so the tree drops the node.
func (a *App) deleteAgentEntity(sc *db.ServerConn, node *explorerNode, title, message string, run func(ctx context.Context) error) {
	if !a.requireConn(sc) {
		return
	}
	a.confirmDialog.ShowConfirm(title, message, func(confirmed bool) {
		if !confirmed {
			return
		}
		a.runWithProgress(progressJob{
			title:   title,
			message: fmt.Sprintf("Deleting %q...", node.label),
			what:    "deleting an Agent object",
			sc:      sc,
		}, func(ctx context.Context, _ progressReport) error {
			return run(ctx)
		}, func(err error, cancelled bool) {
			switch {
			case cancelled:
				// Refreshed anyway: the cancel may have reached the server
				// after the delete had already committed.
				a.setStatus(fmt.Sprintf("Delete of %q cancelled", node.label))
				a.explorer.Reload(node.parent)
			case err != nil:
				a.setStatus(fmt.Sprintf("Delete failed: %v", withPermissionAdvice(err)))
			default:
				a.setStatus(fmt.Sprintf("%q deleted", node.label))
				a.explorer.Reload(node.parent)
			}
		})
	})
}
