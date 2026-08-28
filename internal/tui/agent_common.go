package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_common.go holds small helpers shared across the SQL Server Agent
// tree/detail/menu files: string formatters for gosmo's Agent enums, and
// the generic async enable/disable/delete plumbing every Agent entity type
// (Job, Schedule, Alert, Operator) shares.

// formatJobState renders a gosmo.JobState for display.
func formatJobState(s gosmo.JobState) string {
	switch s {
	case gosmo.JobStateIdle:
		return "Idle"
	case gosmo.JobStateSuspended:
		return "Suspended"
	case gosmo.JobStateExecuting, gosmo.JobStateRunning:
		return "Running"
	case gosmo.JobStateWaitingForWorker:
		return "Waiting for worker thread"
	case gosmo.JobStateBetweenRetries:
		return "Between retries"
	case gosmo.JobStateCancelling:
		return "Cancelling"
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

// refreshExplorerNode reloads node's children (if it's currently expanded)
// and invalidates its cached detail view — the shared body behind every
// Refresh context-menu action, and behind Agent deletes refreshing the
// parent folder. A nil node is a no-op.
func refreshExplorerNode(a *App, n *explorerNode) {
	if n == nil {
		return
	}
	n.data.Loaded = false
	n.children = nil
	if n.expanded {
		a.loadChildren(n)
	}
	a.detailBrowser.Invalidate(a, n)
}

// setAgentEnabled runs run (a gosmo Enable/Disable call) on a background
// goroutine, then updates node's cached IsEnabled flag and redraws the tree
// and detail view on success — the shared body behind every Agent entity's
// Enable/Disable toggle.
func (a *App) setAgentEnabled(sc *db.ServerConn, node *explorerNode, enable bool, run func(ctx context.Context) error) {
	if !a.requireConn(sc) {
		return
	}
	a.safego("enabling/disabling an Agent object", func() {
		ctx, cancel := serverWriteContext(sc)
		defer cancel()
		err := run(ctx)
		a.postAndWake(func() {
			if err != nil {
				word := "disable"
				if enable {
					word = "enable"
				}
				a.setStatus(fmt.Sprintf("Failed to %s %q: %v", word, node.label, err))
				return
			}
			node.data.IsEnabled = enable
			a.explorer.rebuild()
			a.detailBrowser.Invalidate(a, node)
		})
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
		a.safego("deleting an Agent object", func() {
			ctx, cancel := serverWriteContext(sc)
			defer cancel()
			err := run(ctx)
			a.postAndWake(func() {
				if err != nil {
					a.setStatus(fmt.Sprintf("Delete failed: %v", withPermissionAdvice(err)))
					return
				}
				a.setStatus(fmt.Sprintf("%q deleted", node.label))
				refreshExplorerNode(a, node.parent)
			})
		})
	})
}
