package tui

import (
	"context"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// completionLoad is one lazy IntelliSense cache load: a database's catalog,
// the sys-schema catalog, the server's database directory, its linked-server
// list, a linked server's databases, a remote database's catalog. Every one
// of them has the same lifecycle, which startCompletionLoad owns so the next
// cache level gets it whole rather than a copy that drifts:
//
//   - latest-only under its own timeout — a newer load for the entry
//     supersedes and cancels this one, and a hung server can't leave the
//     popup on "Loading suggestions..." forever;
//   - a panic evicts the entry and closes every popup on the server (see
//     completionLoadPanicked);
//   - an error after the starting connection closed evicts rather than
//     poisons the entry (see startCompletionLoad);
//   - every outcome that reaches the entry refreshes the server's popups, so
//     one left on the placeholder fills in live (B12).
type completionLoad[T any] struct {
	what    string // the panic label: "loading the autocomplete catalog"
	timeout time.Duration
	load    *latest
	// owned reports whether the entry is still the one its cache holds. A
	// purge on disconnect drops a server's entries, and a reconnect installs
	// different live ones under the same keys: the entry's own load token
	// can't catch that, since nothing bumped the discarded entry's seq.
	owned func() bool
	// evict makes the next lookup start a fresh load: drops the entry from
	// its cache, or for an entry that must stay listed (a linked server
	// whose databases failed to load), clears its loading latch.
	evict func()
	fetch func(ctx context.Context) (T, error)
	// apply records the result on the entry: clears its loading latch,
	// stores the data or the error, and reports a failure on the status bar.
	// err is never a closed connection's teardown error.
	apply func(v T, err error)
}

// startCompletionLoad runs c.fetch on a background goroutine bounded by
// c.timeout under sc's context, and delivers the result on the UI goroutine.
//
// An error with sc no longer open evicts the entry instead of reaching apply.
// The caches are shared by every ServerConn resolving to the same
// server+login (+database); sc merely started the fetch, so its closing
// mid-fetch says nothing about whether another connection still wants the
// result. err's shape depends on a race — context cancellation or "database
// is closed", by whether sc.Close() ran before or after a connection was
// acquired — so sc.IsOpen() is checked instead of matching either. The popup
// refresh that follows is the next lookup, for a popup another panel holds
// open on the entry.
func startCompletionLoad[T any](a *App, sc *db.ServerConn, c completionLoad[T]) {
	serverKey := sysCompletionInventoryKey(sc.Opts)
	ctx, seq := c.load.BeginTimeout(sc.Context(), c.timeout)
	a.safegoRepair(c.what, func() {
		a.completionLoadPanicked(c.load, seq, serverKey, c.owned, c.evict)
	}, func() {
		v, err := c.fetch(ctx)
		a.postAndWake(func() {
			if !c.load.Done(seq) || !c.owned() {
				return // superseded, or the entry has left its cache
			}
			if err != nil && !sc.IsOpen() {
				c.evict()
			} else {
				c.apply(v, err)
			}
			// Every panel on the server, not only those connected to this
			// database: another may be waiting on it through a cross-database
			// or four-part name.
			a.refreshSysCompletionPopups(serverKey)
		})
	})
}

// completionLoadPanicked is every completion load's safegoRepair step. The
// loading latch is otherwise cleared only in the callback the fetch posts on
// completion, which a panic unwinds straight past, leaving every later lookup
// seeing a load in flight that doesn't exist.
//
// The entry is evicted rather than merely unlatched, so the next lookup
// retries from scratch instead of reading a catalog half-built by the fetch
// that died. seq keeps a superseded panic off a live newer load.
//
// It also closes every open popup on the server, which may be showing the
// placeholder this load would have replaced. Closed rather than refreshed: a
// refresh re-asks the provider, which finds the entry gone and starts a fresh
// load — one that panics the same way, while the popup stays open, loops.
func (a *App) completionLoadPanicked(l *latest, seq int, serverKey string, owned func() bool, evict func()) {
	if !l.Done(seq) || !owned() {
		return
	}
	evict()
	a.closeSysCompletionPopups(serverKey)
}
