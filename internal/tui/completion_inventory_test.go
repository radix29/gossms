package tui

import (
	"context"
	"testing"
	"time"
)

// beginLoad/endLoad guard loadCompletionInventory/loadSysCompletionInventory
// against a fast double-refresh (e.g. mashing Ctrl+R) or a refresh that
// lands before the initial load returns — same contract as explorerNode's
// beginLoad/endLoad in object_explorer_test.go. Without this guard, an
// older load whose result arrives after a newer one would silently
// overwrite it.
func TestCompletionInventoryBeginEndLoad(t *testing.T) {
	inv := &completionInventory{}

	ctx1, seq1 := inv.beginLoad(context.Background(), time.Minute)
	if seq1 != 1 {
		t.Fatalf("first beginLoad seq = %d, want 1", seq1)
	}
	if ctx1.Err() != nil {
		t.Fatalf("ctx1 already done before being superseded: %v", ctx1.Err())
	}

	ctx2, seq2 := inv.beginLoad(context.Background(), time.Minute)
	if seq2 != 2 {
		t.Fatalf("second beginLoad seq = %d, want 2", seq2)
	}
	if ctx1.Err() != context.Canceled {
		t.Errorf("second beginLoad did not cancel the superseded fetch's context (err=%v)", ctx1.Err())
	}
	if ctx2.Err() != nil {
		t.Errorf("ctx2 should still be live, got %v", ctx2.Err())
	}

	// The older load (seq1) finishing last must report itself superseded.
	if inv.endLoad(seq1) {
		t.Errorf("endLoad(stale seq) = true, want false (superseded)")
	}
	if inv.cancelLoad == nil {
		t.Errorf("endLoad(stale seq) must not clear cancelLoad for the still-pending current fetch")
	}

	// The current load (seq2) must still be accepted.
	if !inv.endLoad(seq2) {
		t.Errorf("endLoad(current seq) = false, want true")
	}
	if inv.cancelLoad != nil {
		t.Errorf("endLoad(current seq) should clear cancelLoad")
	}
}
