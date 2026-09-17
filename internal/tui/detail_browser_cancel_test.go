package tui

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
	"time"

	dbconn "github.com/radix29/gossms/internal/db"
)

// The Detail Browser's fetches run on the shared connection pool, and seq only
// discards a superseded fetch's *result*: the fetch itself ran on, so arrowing
// through N nodes left the one the user stopped on queued behind N fetches.
// These pin that moving on cancels the fetch it replaces, and that nothing a
// cancelled fetch still produces is cached.
//
// Each gated read ignores its context (fakeResponse.block), so the superseded
// fetch genuinely finishes afterwards with a good answer — the worst case for
// the cache, and the one a guard on the error alone would let through.

const loginsListMatch = "FROM sys.server_principals\n\tWHERE type IN ('S','U','G','E','X','C','K')"

// settleDetails drains the UI queue for a while, for a test waiting on a post
// that is supposed to change nothing — there is no condition to wait for.
func settleDetails(a *App) {
	for deadline := time.Now().Add(200 * time.Millisecond); time.Now().Before(deadline); {
		a.drainPending()
		time.Sleep(time.Millisecond)
	}
}

// readContexts is every context a read matching want ran under, in order.
func readContexts(inst *fakeInstance, want string) []context.Context {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	var out []context.Context
	for i, q := range inst.reads {
		if strings.Contains(q, want) {
			out = append(out, inst.readCtxs[i])
		}
	}
	return out
}

func TestMovingOnCancelsTheSupersededDetailFetch(t *testing.T) {
	logins := loginListResponse()
	gate := make(chan struct{})
	logins.block = gate
	released := false
	release := func() {
		if !released {
			released = true
			close(gate)
		}
	}
	defer release()

	a := newTestApp()
	sc, inst := newFakeConn(t, logins)
	a.detailBrowser = a.newDetailBrowser()
	db := a.detailBrowser

	nodeA := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: sc}}
	nodeB := &explorerNode{label: "my_col", data: nodeData{Type: NodeColumn, Name: "my_col", conn: sc}}

	db.ShowNodeDetails(a, nodeA)
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(loginsListMatch); return ok },
		"the logins read to reach the server")
	ctxA, _ := inst.ReadContext(loginsListMatch)
	if err := ctxA.Err(); err != nil {
		t.Fatalf("the logins read was cancelled while it was still the current one: %v", err)
	}

	db.ShowNodeDetails(a, nodeB)
	if ctxA.Err() == nil {
		t.Error("the superseded logins read is still running against the server")
	}
	drainUntil(t, a, func() bool {
		r := db.grid.Row(0)
		return len(r) == 2 && r[1] == "my_col"
	}, "the second node's details to show")

	// The superseded fetch now finishes, with a perfectly good answer.
	release()
	settleDetails(a)
	if _, ok := db.cache[nodeA]; ok {
		t.Error("the cancelled fetch's result was cached")
	}
	if r := db.grid.Row(0); len(r) != 2 || r[1] != "my_col" {
		t.Errorf("grid row 0 = %v, want the second node's details still showing", r)
	}

	// Back on A it is fetched afresh, and this time it lands and is cached.
	db.ShowNodeDetails(a, nodeA)
	if got := db.grid.Status(); got != "Loading..." {
		t.Errorf("status = %q, want a fresh fetch for the node whose fetch was cancelled", got)
	}
	drainUntil(t, a, func() bool { _, ok := db.cache[nodeA]; return ok }, "the refetch to be cached")
	if db.run.cancel != nil {
		t.Error("a fetch that landed kept its context registered")
	}
}

// A backfilling folder is the expensive case: every row in flight is its own
// read. All of them are cancelled, and the part-filled rows are not cached —
// cached, a reselect would be a hit that never refetches.
func TestMovingOnCancelsAFoldersBackfill(t *testing.T) {
	const n = 20
	rows := make([][]driver.Value, n)
	for i := range rows {
		rows[i] = []driver.Value{fmt.Sprintf("db%02d", i), int64(5 + i), "ONLINE", "FULL", int64(160),
			"SQL_Latin1_General_CP1_CI_AS", false, time.Time{}, int64(0)}
	}
	gate := make(chan struct{})
	const spaceMatch = "AS avail_log_mb\nFROM sys.database_files"

	a := newTestApp()
	sc, inst := newFakeConn(t,
		fakeResponse{match: "FROM sys.databases", cols: 9, rows: rows},
		fakeResponse{match: spaceMatch, cols: 5, rows: [][]driver.Value{{1.0, 1.0, 0.0, 0.0, 0.0}}, block: gate})
	a.detailBrowser = a.newDetailBrowser()
	db := a.detailBrowser

	folder := &explorerNode{label: "Databases", data: nodeData{Type: NodeDatabases, conn: sc}}
	other := &explorerNode{label: "my_col", data: nodeData{Type: NodeColumn, Name: "my_col", conn: sc}}

	db.ShowNodeDetails(a, folder)
	drainUntil(t, a, func() bool { return len(readContexts(inst, spaceMatch)) == maxRowFetchConcurrency },
		"the backfill's first wave to reach the server")

	db.ShowNodeDetails(a, other)
	for i, ctx := range readContexts(inst, spaceMatch) {
		if ctx.Err() == nil {
			t.Errorf("backfill read %d is still running after the selection moved on", i)
		}
	}
	if _, ok := db.run.pending[folder]; ok {
		t.Error("the cancelled fetch kept its pending entry, so its final stage can still cache")
	}

	// The first wave now answers, and the loader runs on to its cacheOnlyObjects.
	close(gate)
	settleDetails(a)
	if _, ok := db.cache[folder]; ok {
		t.Error("the cancelled fetch's part-filled rows were cached")
	}
}

// Invalidate on the node on screen refetches it, and the fetch it replaces is
// cancelled rather than left to finish beside the new one.
func TestInvalidateCancelsTheFetchItReplaces(t *testing.T) {
	logins := loginListResponse()
	gate := make(chan struct{})
	logins.block = gate
	defer close(gate)

	a := newTestApp()
	sc, inst := newFakeConn(t, logins)
	a.detailBrowser = a.newDetailBrowser()
	db := a.detailBrowser
	node := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: sc}}

	db.ShowNodeDetails(a, node)
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(loginsListMatch); return ok },
		"the logins read to reach the server")
	first, _ := inst.ReadContext(loginsListMatch)

	db.Invalidate(a, node)
	if first.Err() == nil {
		t.Error("the fetch Invalidate replaced is still running")
	}
	if db.run.node != node || db.run.cancel == nil {
		t.Error("the refetch is not the fetch in flight")
	}
}

// Disconnecting cancels a fetch in flight on that connection.
func TestPurgeConnCancelsTheFetchInFlight(t *testing.T) {
	logins := loginListResponse()
	gate := make(chan struct{})
	logins.block = gate
	defer close(gate)

	a := newTestApp()
	sc, inst := newFakeConn(t, logins)
	a.detailBrowser = a.newDetailBrowser()
	db := a.detailBrowser
	node := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: sc}}

	db.ShowNodeDetails(a, node)
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(loginsListMatch); return ok },
		"the logins read to reach the server")
	ctx, _ := inst.ReadContext(loginsListMatch)

	db.PurgeConn(sc)
	if ctx.Err() == nil {
		t.Error("the fetch outlived its connection's purge")
	}
	if db.run.cancel != nil {
		t.Error("PurgeConn left the cancelled fetch as the one in flight")
	}
}

// A backfill whose fetch was cancelled before a row started skips the row
// outright: each would otherwise take a pool connection only to fail.
func TestBackfillRowsSkipsRowsOnceCancelled(t *testing.T) {
	a := newTestApp()
	a.detailBrowser = NewDetailBrowser("Details")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := 0
	a.detailBrowser.backfillRows(a, ctx, 1, 5, "test backfill",
		func(context.Context, int) func() { started++; return func() {} },
		func(int) {})
	if started != 0 {
		t.Errorf("%d rows started after the fetch was cancelled, want 0", started)
	}
}

// A Reload that retires the node whose fetch is in flight cancels it: the node
// has left the tree, and nothing it fetches can be shown or cached.
func TestForgetCancelsARetiredNodesFetch(t *testing.T) {
	logins := loginListResponse()
	gate := make(chan struct{})
	logins.block = gate
	defer close(gate)

	a := newTestApp()
	sc, inst := newFakeConn(t, logins)
	a.detailBrowser = a.newDetailBrowser()
	db := a.detailBrowser
	node := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: sc}}

	db.ShowNodeDetails(a, node)
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(loginsListMatch); return ok },
		"the logins read to reach the server")
	ctx, _ := inst.ReadContext(loginsListMatch)

	db.Forget([]*explorerNode{{label: "unrelated"}})
	if ctx.Err() != nil {
		t.Fatal("forgetting another node cancelled this one's fetch")
	}
	db.Forget([]*explorerNode{node})
	if ctx.Err() == nil {
		t.Error("the retired node's fetch is still running")
	}
}

// PurgeConn must supersede the run it stops, not merely cancel it. The run
// belongs to a connection being torn down, so its result has nowhere to go —
// but Cancel (detailRuns.stop) leaves seq untouched, so the run stays current
// and a result already on its way would paint the disconnected server's rows
// over whatever is displayed.
//
// The divergent case below — run.node on the purged connection while
// currentNode is on another — is not reachable today, because ShowNodeDetails
// assigns currentNode in the same call that begins the run. That is exactly
// why it is pinned here: the old code was correct only by way of that
// invariant, via the currentNode branch, and nothing said so.
//
// Mutation check: put supersede back to stop and this fails.
func TestPurgeConnSupersedesTheRunItStops(t *testing.T) {
	a := newTestApp()
	a.detailBrowser = a.newDetailBrowser()
	db := a.detailBrowser

	scGone := &dbconn.ServerConn{}
	scOther := &dbconn.ServerConn{}
	gone := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: scGone}}
	other := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: scOther}}

	// The divergence: the run is for scGone's node, the pane is showing
	// scOther's, so PurgeConn(scGone) takes the first branch and not the
	// currentNode one.
	_, token := db.run.begin(context.Background(), gone)
	db.currentNode = other

	db.PurgeConn(scGone)

	if db.run.Done(token) {
		t.Error("the purged run is still current, so its result would paint the disconnected server's rows over the pane")
	}
	if db.currentNode != other {
		t.Error("PurgeConn cleared the node belonging to the connection that is still up")
	}
}
