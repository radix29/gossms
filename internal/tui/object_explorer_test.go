package tui

import (
	"context"
	"testing"
	"time"
)

// beginLoad/endLoad guard App.loadChildren's background fetch against a
// fast double-expand or a Refresh that lands before the first fetch
// returns: a newer beginLoad must cancel the previous fetch's context and
// make its eventual endLoad report itself superseded.
func TestExplorerNodeBeginEndLoad(t *testing.T) {
	n := &explorerNode{}

	ctx1, seq1 := n.beginLoad(time.Minute)
	if seq1 != 1 {
		t.Fatalf("first beginLoad seq = %d, want 1", seq1)
	}
	if ctx1.Err() != nil {
		t.Fatalf("ctx1 already done before being superseded: %v", ctx1.Err())
	}

	ctx2, seq2 := n.beginLoad(time.Minute)
	if seq2 != 2 {
		t.Fatalf("second beginLoad seq = %d, want 2", seq2)
	}
	if ctx1.Err() != context.Canceled {
		t.Errorf("second beginLoad did not cancel the superseded fetch's context (err=%v)", ctx1.Err())
	}
	if ctx2.Err() != nil {
		t.Errorf("ctx2 should still be live, got %v", ctx2.Err())
	}

	if n.endLoad(seq1) {
		t.Errorf("endLoad(stale seq) = true, want false (superseded)")
	}
	if n.cancelLoad == nil {
		t.Errorf("endLoad(stale seq) must not clear cancelLoad for the still-pending current fetch")
	}

	if !n.endLoad(seq2) {
		t.Errorf("endLoad(current seq) = false, want true")
	}
	if n.cancelLoad != nil {
		t.Errorf("endLoad(current seq) should clear cancelLoad")
	}
}
