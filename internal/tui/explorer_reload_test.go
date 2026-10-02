package tui

import (
	"context"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// reloadTree is newSelectionTree with a Detail Browser, and a child under each
// login so a Reload of Logins has grandchildren to release too.
func newReloadTree(t *testing.T) *selectionTree {
	t.Helper()
	st := newSelectionTree(t)
	st.a.detailBrowser = NewDetailBrowser("Object Explorer Details")
	for _, l := range []*explorerNode{st.alice, st.bob, st.carol} {
		st.a.explorer.SetChildren(l, []*explorerNode{testLogin(st.sc, l.label+"-child")})
	}
	return st
}

// freshLogins is what a Refresh of Logins brings back: new nodes for the same
// three logins, each with its own child — the shape the tree had before.
func (st *selectionTree) freshLogins() []*explorerNode {
	out := []*explorerNode{testLogin(st.sc, "alice"), testLogin(st.sc, "bob"), testLogin(st.sc, "carol")}
	for _, l := range out {
		l.children = []*explorerNode{testLogin(st.sc, l.label+"-child")}
	}
	return out
}

// installFresh lands a load for n the way loadChildren's callback does,
// registering grandchildren too so byID holds the same shape as before.
func (st *selectionTree) installFresh(n *explorerNode, children []*explorerNode) {
	grand := make(map[*explorerNode][]*explorerNode, len(children))
	for _, c := range children {
		grand[c], c.children = c.children, nil
	}
	st.a.explorer.SetChildren(n, children)
	for _, c := range children {
		st.a.explorer.SetChildren(c, grand[c])
	}
}

// A Refresh replaces every node under the folder. Each copy of Refresh set
// n.children = nil and nothing more, so every replaced node, and its whole
// subtree, stayed in byID and in the Detail Browser's cache for the life of the
// connection: memory grew with every Refresh of a large folder.
func TestReloadReleasesReplacedSubtree(t *testing.T) {
	st := newReloadTree(t)
	oe, dbr := st.a.explorer, st.a.detailBrowser
	st.logins.expanded = false // no background load: the test lands it by hand
	for _, n := range []*explorerNode{st.logins, st.alice, st.bob, st.carol, st.bob.children[0]} {
		dbr.cache[n] = &detailResult{cols: []string{"Name"}, rows: [][]string{{n.label}}}
	}
	dbr.run.pending[st.carol.children[0]] = 7 // a fetch still in flight
	baseByID, baseCache := len(oe.byID), len(dbr.cache)
	old := []*explorerNode{st.alice, st.bob, st.carol, st.alice.children[0], st.bob.children[0], st.carol.children[0]}

	oe.Reload(st.logins)
	st.installFresh(st.logins, st.freshLogins())

	if got := len(oe.byID); got != baseByID {
		t.Errorf("len(byID) after a Refresh = %d, want %d — the replaced nodes are still registered", got, baseByID)
	}
	for _, n := range old {
		if oe.byID[n.id] == n {
			t.Errorf("replaced node %q is still in byID", n.label)
		}
		if _, ok := dbr.cache[n]; ok {
			t.Errorf("replaced node %q is still in the Detail Browser's cache", n.label)
		}
		if _, ok := dbr.run.pending[n]; ok {
			t.Errorf("replaced node %q is still in the Detail Browser's pending map", n.label)
		}
	}
	// Logins itself was refreshed, not replaced: its entry goes (Invalidate),
	// and the node stays.
	if got, want := len(dbr.cache), baseCache-5; got != want {
		t.Errorf("len(cache) after a Refresh = %d, want %d", got, want)
	}
	if oe.byID[st.logins.id] != st.logins {
		t.Error("the refreshed folder itself left byID")
	}
}

// A replaced node's own fetch — a login expanded just before its folder was
// refreshed — must be cancelled, not left to run out childFetchTimeout, and
// its result must not install children into a node no longer in the tree.
func TestReloadCancelsReplacedNodesLoad(t *testing.T) {
	st := newReloadTree(t)
	oe := st.a.explorer
	st.logins.expanded = false
	ctx, seq := st.bob.load.BeginTimeout(context.Background(), time.Minute)
	grandCtx, _ := st.bob.children[0].load.BeginTimeout(context.Background(), time.Minute)

	oe.Reload(st.logins)

	if ctx.Err() != context.Canceled {
		t.Errorf("replaced node's load context: err = %v, want Canceled", ctx.Err())
	}
	if grandCtx.Err() != context.Canceled {
		t.Errorf("replaced grandchild's load context: err = %v, want Canceled", grandCtx.Err())
	}
	if st.bob.load.Done(seq) {
		t.Error("the replaced node's fetch reported itself current")
	}

	// A result that arrives anyway is refused rather than registered.
	before := len(oe.byID)
	oe.SetChildren(st.bob, []*explorerNode{testLogin(st.sc, "orphan")})
	if got := len(oe.byID); got != before {
		t.Errorf("SetChildren on a replaced node registered %d nodes", got-before)
	}
}

// Until the reload lands the replaced rows are still drawn, and the user can
// still select and act on them: they stay in byID until the rebuild that takes
// them off the screen.
func TestReloadKeepsStaleRowsUsableUntilRebuild(t *testing.T) {
	st := newReloadTree(t)
	st.selectNode(t, st.bob)
	st.logins.expanded = false // stale rows stay drawn: nothing rebuilds

	st.a.explorer.Reload(st.logins)

	if got := st.a.explorer.Selected(); got != st.bob {
		t.Fatalf("Selected() while the reload is in flight = %v, want the still-drawn bob", got)
	}
}

// The refresh after a create (New Login, New Database, Attach, Restore…) is a
// Reload like any other. RefreshLoginsFolder and RefreshDatabasesFolder set
// children = nil by hand, so every replaced node stayed in byID until
// disconnect.
func TestPostWriteRefreshReleasesTheOldSubtree(t *testing.T) {
	st := newReloadTree(t)
	oe := st.a.explorer
	st.logins.expanded = false // no background load: nothing to land
	old := []*explorerNode{st.alice, st.bob, st.carol, st.alice.children[0]}

	oe.ReloadFolders(st.sc, folderOf("", NodeLogins))
	oe.rebuild()

	if st.logins.data.Loaded || st.logins.children != nil {
		t.Error("the Logins folder was not reloaded")
	}
	for _, n := range old {
		if !n.retired || oe.byID[n.id] == n {
			t.Errorf("replaced node %q was not released (retired=%v)", n.label, n.retired)
		}
	}
}

// A write that finishes after a Refresh above its folder must still refresh
// that folder. The dialogs held the node they were opened from, and Reload of
// a retired node is a no-op, so the new object stayed missing from the tree.
func TestPostWriteRefreshFindsTheReplacementFolder(t *testing.T) {
	st := newReloadTree(t)
	oe := st.a.explorer
	opened := st.logins // what a New Login dialog was opened from
	st.security.expanded = false

	oe.Reload(st.security) // the user refreshes Security while the dialog is open
	fresh := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: st.sc}}
	oe.SetChildren(st.security, []*explorerNode{fresh})
	fresh.data.Loaded = true
	if !opened.retired {
		t.Fatal("fixture: the Refresh did not retire the folder the dialog holds")
	}

	oe.ReloadFolders(st.sc, sameNodeAs(opened))

	if fresh.data.Loaded {
		t.Error("the create's refresh missed the folder that replaced the one the dialog was opened from")
	}
}

// sameNodeAs keeps apart folders of one type and database that belong to
// different parents — one table's Indexes folder is not another's.
func TestSameNodeAsKeepsTableScopedFoldersApart(t *testing.T) {
	ix := func(table string) nodeData {
		return nodeData{Type: NodeIndexes, DBName: "appdb", Schema: "dbo", TableName: table}
	}
	match := sameNodeAs(&explorerNode{data: ix("Orders")})
	if !match(ix("Orders")) {
		t.Error("the same folder was not matched")
	}
	if match(ix("Customers")) {
		t.Error("another table's Indexes folder was matched")
	}
	if sameNodeAs(nil)(nodeData{}) {
		t.Error("a nil node matched")
	}
}

// The Detail Browser's pending map exists only to order the cache writes of
// fetches in flight; kept after the write, it gained an entry for every node
// ever selected and held each alive.
func TestDetailPendingEndsWhenResultIsCached(t *testing.T) {
	a := newTestApp()
	dbr := NewDetailBrowser("Object Explorer Details")
	a.detailBrowser = dbr
	node := &explorerNode{label: "n"}

	for name, final := range map[string]func(seq int){
		"postFinal": func(seq int) { dbr.postFinal(a, node, seq, []string{"Name"}, [][]string{{"x"}}, nil) },
		"postFinalCharts": func(seq int) {
			dbr.postFinalCharts(a, node, seq, []string{"Name"}, [][]string{{"x"}}, nil, nil)
		},
		"cacheOnlyObjects": func(seq int) { dbr.cacheOnlyObjects(a, node, seq, []string{"Name"}, [][]string{{"x"}}, nil, nil) },
	} {
		delete(dbr.cache, node)
		dbr.run.pending[node] = 3
		final(3)
		a.drainPending()
		if _, ok := dbr.cache[node]; !ok {
			t.Errorf("%s: result not cached", name)
		}
		if _, ok := dbr.run.pending[node]; ok {
			t.Errorf("%s: pending entry kept after the result was cached", name)
		}
	}
}

// -- R4: a Refresh of the server re-reads what the login may do -------------

// capsConn is a connected server whose capability answers a test can change
// between two probes, as a GRANT to the connected login would.
func capsConn(t *testing.T, a *App, granted, denied, dbGranted, dbDenied []string) (*db.ServerConn, *fakeInstance, *explorerNode) {
	t.Helper()
	sc, inst := newFakeConn(t, capabilityResponses(true, granted, denied, dbGranted, dbDenied)...)
	sc.ProbeCapabilities()
	a.connections = append(a.connections, sc)
	root := a.explorer.AddRoot("capsrv", sc)
	return sc, inst, root
}

func rescript(inst *fakeInstance, responses []fakeResponse) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.responses = append([]fakeResponse{serverInfoResponse(), sysInfoResponse()}, responses...)
}

// menuRefresh runs node's context-menu Refresh.
func menuRefresh(t *testing.T, a *App, node *explorerNode) {
	t.Helper()
	for _, it := range a.nodeMenuItems(node) {
		if it.Label == refreshMenuLabel {
			it.Action()
			return
		}
	}
	t.Fatalf("no %s item on %q's menu", refreshMenuLabel, node.label)
}

// Rights granted to a connected login take effect on its existing sessions, so
// a Refresh of the server has to re-read them. F5 dropped only the database
// answers, the context menu's Refresh dropped nothing, and neither re-read the
// server-scope set, which was probed once in Connect: a GRANT ALTER ANY LOGIN
// stayed invisible until the login reconnected.
func TestServerRefreshRereadsCapabilities(t *testing.T) {
	for name, refresh := range map[string]func(t *testing.T, a *App, root *explorerNode){
		"F5": func(t *testing.T, a *App, root *explorerNode) {
			a.explorer.view.SelectID(root.id)
			a.explorer.RefreshSelected()
		},
		"context menu": func(t *testing.T, a *App, root *explorerNode) { menuRefresh(t, a, root) },
	} {
		t.Run(name, func(t *testing.T) {
			a := newTestApp()
			sc, inst, root := capsConn(t, a, nil, []string{"ALTER ANY LOGIN"}, nil, []string{"ALTER"})
			if sc.Capabilities().Has("ALTER ANY LOGIN") {
				t.Fatal("setup: ALTER ANY LOGIN held before the grant")
			}
			if sc.DatabaseCapabilities(context.Background(), "db1").Has("ALTER") {
				t.Fatal("setup: database ALTER held before the grant")
			}

			rescript(inst, capabilityResponses(true, []string{"ALTER ANY LOGIN"}, nil, []string{"ALTER"}, nil))
			refresh(t, a, root)

			drainUntil(t, a, func() bool { return sc.Capabilities().Has("ALTER ANY LOGIN") },
				"the server-scope capabilities to be re-read")
			if !sc.DatabaseCapabilities(context.Background(), "db1").Has("ALTER") {
				t.Error("the database answer cached before the Refresh is still served")
			}
		})
	}
}

// A Refresh of anything below the server re-reads that node, not what the
// login may do: the capability probe is not a cost every folder Refresh pays.
func TestFolderRefreshLeavesCapabilitiesCached(t *testing.T) {
	a := newTestApp()
	sc, inst, root := capsConn(t, a, nil, nil, nil, []string{"ALTER"})
	sc.DatabaseCapabilities(context.Background(), "db1")
	folder := &explorerNode{label: "Databases", data: nodeData{Type: NodeDatabases, conn: sc}}
	a.explorer.SetChildren(root, []*explorerNode{folder})

	rescript(inst, capabilityResponses(true, nil, nil, []string{"ALTER"}, nil))
	menuRefresh(t, a, folder)

	if sc.CachedDatabaseCapabilities("db1").Has("ALTER") {
		t.Error("a folder Refresh dropped the database capability cache")
	}
}
