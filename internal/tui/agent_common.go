package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_common.go holds helpers shared by the SQL Server Agent files:
// formatters for gosmo's Agent enums, and async enable/disable/delete plumbing
// for Jobs, Schedules, Alerts and Operators.

// formatJobState renders a gosmo.JobState. When Agent's own state is
// unavailable (Agent stopped, or neither sysadmin nor SQLAgentReaderRole),
// gosmo can only derive Executing or Idle.
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

// setAgentEnabled runs run (a gosmo Enable/Disable) behind the progress dialog,
// then updates node's IsEnabled and redraws on success — shared by every Agent
// entity. noun is the lowercase entity name for dialog and status.
//
// Unconfirmed in both directions, as in SSMS. The progress dialog's reveal
// delay hides fast toggles; slow ones get a spinner and Cancel.
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
			// Re-read rather than assumed: the cancel may have arrived after
			// the change committed.
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

// deleteAgentEntity confirms, then runs run (a gosmo Drop/Delete) in the
// background and refreshes the parent folder on success. Shared by every Agent
// entity.
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
				// Refresh anyway: the cancel may have arrived after the delete
				// committed.
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
