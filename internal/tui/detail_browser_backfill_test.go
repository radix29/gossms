package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestBackfillRowsFillsEveryRow is the ordinary path: every row's fetch runs
// and its posted closure is queued by the time backfillRows returns, so a
// cacheOnly posted afterwards drains last and sees a fully filled slice.
func TestBackfillRowsFillsEveryRow(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := a.explorer.Selected()
	a.detailBrowser = NewDetailBrowser("Details")
	db := a.detailBrowser
	db.seq = 1
	db.pending[node] = 1

	cols := []string{"Name", "Size"}
	rows := [][]string{{"a", "…"}, {"b", "…"}, {"c", "…"}}

	db.backfillRows(a, sc, 1, len(rows), "test backfill",
		func(_ context.Context, i int) func() {
			return func() { rows[i][1] = strings.Repeat("x", i+1) }
		},
		func(i int) { rows[i][1] = "N/A" })

	db.cacheOnly(a, node, 1, cols, rows, nil)
	a.drainPending()

	cached, ok := db.cache[node]
	if !ok {
		t.Fatal("cacheOnly stored nothing")
	}
	for i, want := range []string{"x", "xx", "xxx"} {
		if got := cached.rows[i][1]; got != want {
			t.Errorf("cached row %d = %q, want %q", i, got, want)
		}
	}
}

// TestBackfillRowsMarksAPanickingRowFailed is the bug this helper exists to
// hold shut. A panic in one row's fetch unwinds only that goroutine, and
// wg.Done still fires — so wg.Wait returns and the caller caches rows. Without
// the recovery queueing markFailed *before* wg.Done, that row is cached still
// showing its "…" placeholder, permanently: reselecting the node is a cache
// hit that never refetches. The other rows must be unaffected.
func TestBackfillRowsMarksAPanickingRowFailed(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := a.explorer.Selected()
	a.detailBrowser = NewDetailBrowser("Details")
	db := a.detailBrowser
	db.seq = 1
	db.pending[node] = 1

	cols := []string{"Name", "Size"}
	rows := [][]string{{"a", "…"}, {"b", "…"}, {"c", "…"}}

	db.backfillRows(a, sc, 1, len(rows), "test backfill",
		func(_ context.Context, i int) func() {
			if i == 1 {
				panic(errors.New("driver blew up on row 1"))
			}
			return func() { rows[i][1] = "ok" }
		},
		func(i int) { rows[i][1] = "N/A" })

	db.cacheOnly(a, node, 1, cols, rows, nil)
	a.drainPending()

	cached, ok := db.cache[node]
	if !ok {
		t.Fatal("cacheOnly stored nothing")
	}
	for i, want := range []string{"ok", "N/A", "ok"} {
		if got := cached.rows[i][1]; got != want {
			t.Errorf("cached row %d = %q, want %q", i, got, want)
		}
	}
	if strings.Contains(cached.rows[1][1], "…") {
		t.Error("the panicking row was cached still showing its placeholder")
	}
}

// TestBackfillRowsReportsThePanic pins that recovering to repair the row
// doesn't also swallow the report — the status bar still says something went
// wrong.
func TestBackfillRowsReportsThePanic(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	a.detailBrowser = NewDetailBrowser("Details")
	db := a.detailBrowser
	db.seq = 1

	rows := [][]string{{"a", "…"}}
	db.backfillRows(a, sc, 1, 1, "loading the thing",
		func(_ context.Context, _ int) func() { panic("boom") },
		func(i int) { rows[i][1] = "N/A" })
	a.drainPending()

	if got := a.statusText; !strings.Contains(got, "loading the thing") {
		t.Errorf("status = %q, want it to name the failed operation", got)
	}
}

// The progressive Databases/Tables loaders backfill each row's slow columns
// from its own goroutine, posting the write onto the UI goroutine and
// caching the finished rows once every one has landed (see
// loadDatabasesFolderDetails / loadTablesFolderDetails). A stale seq must
// suppress only the redraw, never the row write: skipping both caches rows
// still showing their "…" placeholder, permanently, since reselecting the
// node is then a cache hit that never refetches.
//
// The fan-out itself is covered by the backfillRows tests above; what's
// exercised here is the seq/cache contract it's built on: a backfilled row
// must reach the cache, and a cached result must be served back verbatim.

// TestBackfilledRowsReachCacheAfterSelectionMoved: rows mutated in place
// after the selection moved on (seq advanced) must still be what cacheOnly
// stores, not the placeholder version.
func TestBackfilledRowsReachCacheAfterSelectionMoved(t *testing.T) {
	// The DetailBrowser is attached only after the connections are in
	// place: AddRoot selects the node it adds, and ShowNodeDetails would
	// then dispatch a real fetch against this fake connection's nil
	// gosmo.Server. It's nil-safe precisely so tests can do this.
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := a.explorer.Selected()
	if node == nil {
		t.Fatal("no node selected after addTestConn")
	}
	a.detailBrowser = NewDetailBrowser("Details")
	db := a.detailBrowser

	cols := []string{"Name", "Total (MB)"}
	rows := [][]string{{"db1", "…"}, {"db2", "…"}}

	// A fetch is dispatched for node at seq 1 and shows its placeholder rows.
	db.seq = 1
	db.pending[node] = 1

	// The user selects something else before the backfill lands.
	db.seq = 2

	// The backfill writes land anyway — this is the fix.
	rows[0][1] = "100 MB"
	rows[1][1] = "200 MB"

	// The loader finishes and caches. cacheOnly posts via postEvent, which
	// needs no screen; drain it by hand the way Run()'s loop would.
	db.cacheOnly(a, node, 1, cols, rows, nil)
	a.drainPending()

	cached, ok := db.cache[node]
	if !ok {
		t.Fatal("cacheOnly stored nothing for node")
	}
	for i, want := range []string{"100 MB", "200 MB"} {
		if got := cached.rows[i][1]; got != want {
			t.Errorf("cached row %d = %q, want %q (placeholder must not be what's cached)", i, got, want)
		}
	}
	_ = sc
}

// TestStaleFetchDoesNotClobberNewerCache pins the other half of that
// contract: cacheOnly refuses to write when a newer fetch for the same node
// has been dispatched since, so unconditionally writing rows[i] can't let
// an older fetch's data win.
func TestStaleFetchDoesNotClobberNewerCache(t *testing.T) {
	a := newTestApp()
	addTestConn(a, "server-one")
	node := a.explorer.Selected()
	a.detailBrowser = NewDetailBrowser("Details")
	db := a.detailBrowser

	cols := []string{"Name"}
	db.pending[node] = 2 // a newer fetch is already in flight

	db.cacheOnly(a, node, 1, cols, [][]string{{"stale"}}, nil)
	a.drainPending()

	if _, ok := db.cache[node]; ok {
		t.Error("cacheOnly wrote the cache for a superseded fetch")
	}
}

// TestPurgeConnDropsEntriesForDisconnectedServer covers the leak: both maps
// are keyed by *explorerNode and would otherwise keep every disconnected
// server's nodes, and their full result rows, alive for the session.
func TestPurgeConnDropsEntriesForDisconnectedServer(t *testing.T) {
	a := newTestApp()
	sc1 := addTestConn(a, "server-one")
	node1 := a.explorer.Selected()
	sc2 := addTestConn(a, "server-two")
	node2 := a.explorer.Selected()
	a.detailBrowser = NewDetailBrowser("Details")
	db := a.detailBrowser

	db.cache[node1] = &detailResult{cols: []string{"a"}}
	db.pending[node1] = 1
	db.cache[node2] = &detailResult{cols: []string{"b"}}
	db.pending[node2] = 1
	db.currentNode = node1

	db.PurgeConn(sc1)

	if _, ok := db.cache[node1]; ok {
		t.Error("cache still holds the disconnected server's node")
	}
	if _, ok := db.pending[node1]; ok {
		t.Error("pending still holds the disconnected server's node")
	}
	if db.currentNode != nil {
		t.Error("currentNode still points at the disconnected server's node")
	}
	if _, ok := db.cache[node2]; !ok {
		t.Error("PurgeConn dropped the other connection's cache entry")
	}
	if _, ok := db.pending[node2]; !ok {
		t.Error("PurgeConn dropped the other connection's pending entry")
	}
	_ = sc2
}
