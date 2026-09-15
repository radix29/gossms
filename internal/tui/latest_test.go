package tui

import (
	"context"
	"testing"
	"time"
)

// latest is what guards every latest-only fetch in the package — an explorer
// node's children, a completion inventory, the Query Store's report, plan and
// series reads, the Log Viewer's read. Both halves of its contract are pinned
// here, because a copy with only the first half is the bug this type exists to
// stop: a newer Begin must supersede the previous run's token *and* cancel its
// context, so the superseded read releases its pool connection now rather than
// at its timeout.
func TestLatestSupersedesAndCancels(t *testing.T) {
	var l latest

	if !l.Idle() {
		t.Fatal("a zero latest reports a run in flight")
	}

	ctx1, tok1 := l.Begin(context.Background())
	if tok1 != 1 {
		t.Fatalf("first Begin token = %d, want 1", tok1)
	}
	if l.Idle() {
		t.Error("Idle() = true with a run in flight")
	}
	if ctx1.Err() != nil {
		t.Fatalf("ctx1 already done before being superseded: %v", ctx1.Err())
	}

	ctx2, tok2 := l.Begin(context.Background())
	if tok2 != 2 {
		t.Fatalf("second Begin token = %d, want 2", tok2)
	}
	if ctx1.Err() != context.Canceled {
		t.Errorf("second Begin did not cancel the superseded run's context (err=%v)", ctx1.Err())
	}
	if ctx2.Err() != nil {
		t.Errorf("ctx2 should still be live, got %v", ctx2.Err())
	}

	// The superseded run (tok1) finishing last must report itself stale, and
	// must not release the context of the run that replaced it.
	if l.Current(tok1) {
		t.Errorf("Current(stale token) = true, want false")
	}
	if l.Done(tok1) {
		t.Errorf("Done(stale token) = true, want false (superseded)")
	}
	if l.Idle() {
		t.Errorf("Done(stale token) cleared the still-pending current run")
	}
	if ctx2.Err() != nil {
		t.Errorf("Done(stale token) cancelled the current run's context (err=%v)", ctx2.Err())
	}

	if !l.Current(tok2) {
		t.Errorf("Current(current token) = false, want true")
	}
	if !l.Done(tok2) {
		t.Errorf("Done(current token) = false, want true")
	}
	if !l.Idle() {
		t.Errorf("Done(current token) should leave no run in flight")
	}
	// Released by calling the cancel, not by dropping it: a discarded CancelFunc
	// leaves the context registered on its parent — with the timer armed, for
	// BeginTimeout — once per run ever started.
	if ctx2.Err() != context.Canceled {
		t.Errorf("Done(current token) dropped the cancel without calling it (ctx err=%v)", ctx2.Err())
	}

	// Done is idempotent: releasing a run does not supersede its token, so a
	// second call still reports it current and does nothing. (Only one of the
	// completion callback and the safegoRepair step runs per run, but neither
	// may panic on an already-released latest.)
	if !l.Done(tok2) {
		t.Errorf("Done after the run was released = false; the token is still the current one")
	}
}

// BeginTimeout carries a deadline of its own, so a run outlives neither its
// parent nor its own timeout.
func TestLatestBeginTimeoutBoundsTheRun(t *testing.T) {
	var l latest

	parent, cancelParent := context.WithCancel(context.Background())
	ctx, _ := l.BeginTimeout(parent, time.Hour)
	if _, ok := ctx.Deadline(); !ok {
		t.Error("BeginTimeout returned a context with no deadline")
	}
	cancelParent()
	if ctx.Err() != context.Canceled {
		t.Errorf("cancelling the parent left the run live (err=%v)", ctx.Err())
	}

	// A bounded wait, not <-short.Done(): a BeginTimeout that dropped its
	// deadline would hang the whole package here rather than fail.
	short, _ := l.BeginTimeout(context.Background(), time.Nanosecond)
	select {
	case <-short.Done():
	case <-time.After(5 * time.Second):
	}
	if short.Err() != context.DeadlineExceeded {
		t.Errorf("BeginTimeout run ended with %v, want DeadlineExceeded", short.Err())
	}
}

// Cancel and Abandon both stop the run; only Abandon supersedes its token.
// Cancel is what a panel's Close wants — the panel is going away, and a result
// already on its way lands as a no-op. Abandon is for a run whose result would
// otherwise refill something that was just emptied.
func TestLatestCancelKeepsTheTokenAndAbandonDoesNot(t *testing.T) {
	var l latest

	ctx, tok := l.Begin(context.Background())
	l.Cancel()
	if ctx.Err() != context.Canceled {
		t.Errorf("Cancel() did not cancel the run's context (err=%v)", ctx.Err())
	}
	if !l.Idle() {
		t.Error("Cancel() left the stale cancel in place; the next call would cancel a finished run")
	}
	if !l.Current(tok) {
		t.Error("Cancel() superseded the token; a result already posted would be dropped")
	}

	ctx2, tok2 := l.Begin(context.Background())
	l.Abandon()
	if ctx2.Err() != context.Canceled {
		t.Errorf("Abandon() did not cancel the run's context (err=%v)", ctx2.Err())
	}
	if l.Current(tok2) {
		t.Error("Abandon() left the token current; its result would still be accepted")
	}

	// Both are safe with nothing in flight.
	var empty latest
	empty.Cancel()
	empty.Abandon()
	if !empty.Idle() {
		t.Error("Cancel/Abandon on an idle latest left a run in flight")
	}
}
