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

// evictInventory must only drop the entry the finishing load actually
// belongs to. The sys-schema loader used to delete unconditionally, so this
// sequence stranded a live load: disconnect (purgeCompletionInventories
// drops the entry) → reconnect to the same server (a new entry goes in under
// the same key, with its own fetch in flight) → the old connection's fetch
// finally fails and evicts the *new* entry. Its load then completed into an
// object no longer in the map, leaving sys completion unavailable until some
// later lookup started a third load.
func TestEvictInventoryOnlyDropsItsOwnEntry(t *testing.T) {
	stale := &completionInventory{}
	live := &completionInventory{}
	const key = "srv,1433,,sa"

	m := map[string]*completionInventory{key: live}

	evictInventory(m, key, stale)
	if m[key] != live {
		t.Fatal("a stale load's eviction removed the newer entry installed after a reconnect")
	}

	evictInventory(m, key, live)
	if _, ok := m[key]; ok {
		t.Error("the current entry's own eviction did not remove it, so the next lookup won't retry")
	}
}

// Eviction of a key that isn't there at all (both entries already purged)
// must be a no-op rather than panicking or inserting anything.
func TestEvictInventoryMissingKeyIsNoOp(t *testing.T) {
	m := map[string]*completionInventory{}
	evictInventory(m, "gone", &completionInventory{})
	if len(m) != 0 {
		t.Errorf("evicting a missing key left the map with %d entries, want 0", len(m))
	}
}
