package tui

import (
	"testing"
)

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
