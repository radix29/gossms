package tui

import (
	"context"
	"time"
)

// latest is the "start a load, cancel the one it replaces, drop stale results"
// lifecycle, owned once instead of hand-rolled per site. An explorer node's
// children, a completion inventory's catalog, a Query Store report, its plan
// pane, its chart and the Log Viewer's read are all latest-only: the newest
// request is the only one whose result anyone wants, and every earlier one
// should stop taking a pool connection from it the moment it is superseded.
//
// Both halves matter, and every copy of this that shipped with only the first
// half was a bug (docs/review-plan-2026-09-11.md R3, R5, R13):
//
//   - the token discards a superseded result, so a slow fetch cannot overwrite
//     the fresher one that replaced it;
//   - the cancel stops the superseded fetch's queries, so they release their
//     connection now rather than at their timeout. Without it, holding Down
//     through a ranking or arrowing through a folder starts one read per row
//     and the row the user stops on queues behind all of them.
//
// Every method runs on the UI goroutine; the zero value is ready to use, and a
// latest with nothing in flight is safe to Cancel, Abandon or ask Idle.
type latest struct {
	// seq numbers every run this latest has ever started. It is never reset:
	// a per-showing counter that restarts at 0 lets the previous showing's
	// first result pass the next showing's first guard (R5).
	seq int
	// cancel stops the run in flight, and is nil when there is none.
	cancel context.CancelFunc
}

// Begin supersedes whatever run is in flight and starts a new one under a
// context derived from parent — the owning connection's Context(), so
// disconnecting cancels the run rather than leaving it to idle out. It returns
// that context and the run's token, which the caller hands to Done on
// completion.
func (l *latest) Begin(parent context.Context) (context.Context, int) {
	return l.begin(context.WithCancel(parent))
}

// BeginTimeout is Begin with a deadline of its own, for a run that must not
// outlive its own timeout even while the connection stays up.
func (l *latest) BeginTimeout(parent context.Context, timeout time.Duration) (context.Context, int) {
	return l.begin(context.WithTimeout(parent, timeout))
}

func (l *latest) begin(ctx context.Context, cancel context.CancelFunc) (context.Context, int) {
	l.Cancel()
	l.seq++
	l.cancel = cancel
	return ctx, l.seq
}

// Current reports whether token is still the run everyone is waiting on. False
// means a newer Begin superseded it and token's result must be dropped.
func (l *latest) Current(token int) bool { return l.seq == token }

// Done reports what Current does, and on true releases the finished run's
// context. The cancel is called, not merely dropped: the result is already in
// hand, but the context stays registered on its parent — with its timer armed,
// for BeginTimeout — until something cancels it, for every run ever started.
func (l *latest) Done(token int) bool {
	if !l.Current(token) {
		return false
	}
	l.Cancel()
	return true
}

// Cancel stops the run in flight, if any, without superseding it: its token
// stays current, so a result already on its way still lands. That is what a
// panel's Close wants — the panel is going away and nothing will read the
// result anyway — and what Begin does before starting the run that replaces it.
func (l *latest) Cancel() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

// Abandon stops the run in flight and supersedes it, so its eventual Done
// reports it stale. For a run whose result now has nowhere to go: a tree node
// leaving the tree, or a selection that cleared without starting a new read.
func (l *latest) Abandon() {
	l.Cancel()
	l.seq++
}

// Idle reports whether no run is in flight.
func (l *latest) Idle() bool { return l.cancel == nil }
