package tui

import (
	"context"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
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

// A disconnect must supersede every load it purges, not merely stop it. The
// purge deletes the entry, so a fetch that completed just before the
// disconnect has nowhere to land — but Cancel leaves seq untouched, so its
// callback still passed Done, applied the catalog to an entry no longer in the
// map, and called setStatus, replacing "Disconnected" on the status bar with
// "Autocomplete ready for <db>". Abandon is the one that supersedes; see
// latest, and ARCHITECTURE.md § Latest-only loads.
//
// Mutation check: put either Abandon back to Cancel and the matching subtest
// fails.
func TestPurgeCompletionInventoriesAbandonsInFlightLoads(t *testing.T) {
	opts := config.Connection{Server: "srv", User: "sa"}
	sc := &db.ServerConn{Opts: opts}
	serverKey := sysCompletionInventoryKey(opts)

	perDB := &completionInventory{loading: true, serverKey: serverKey}
	sys := &completionInventory{loading: true, serverKey: serverKey}

	// Each entry has a fetch in flight, exactly as loadCompletionInventory
	// leaves it: a token the callback will hand back to Done.
	_, perDBToken := perDB.load.Begin(context.Background())
	_, sysToken := sys.load.Begin(context.Background())

	a := &App{
		completionInventories: map[string]*completionInventory{
			completionInventoryKey(opts, "AdventureWorks"): perDB,
		},
		sysCompletionInventories: map[string]*completionInventory{serverKey: sys},
	}

	a.purgeCompletionInventories(sc)

	if len(a.completionInventories) != 0 || len(a.sysCompletionInventories) != 0 {
		t.Fatalf("purge left %d per-database and %d sys entries, want 0 and 0",
			len(a.completionInventories), len(a.sysCompletionInventories))
	}

	if perDB.load.Done(perDBToken) {
		t.Error("a purged per-database load is still current, so its result would apply a catalog to a dropped entry and overwrite the Disconnected status")
	}
	if sys.load.Done(sysToken) {
		t.Error("a purged sys-schema load is still current, so its result would apply a catalog to a dropped entry and overwrite the Disconnected status")
	}
}
