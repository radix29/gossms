package tui

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// selectionTree is a connected server with its Databases folder and a
// Security > Logins folder holding three logins, everything expanded — the
// shape the selection tests below work on. None of the nodes carry a DBName,
// so selecting one never starts a capability probe against the nil server.
type selectionTree struct {
	a                           *App
	sc                          *db.ServerConn
	root, dbs, security, logins *explorerNode
	alice, bob, carol           *explorerNode
	selects                     []*explorerNode // every node OnSelect reported, in order
}

func newSelectionTree(t *testing.T) *selectionTree {
	t.Helper()
	a := newTestApp()
	// A real height, so the tree has screen lines to keep a row on.
	a.explorer.SetBounds(0, 0, 40, 30)
	st := &selectionTree{a: a, sc: addTestConn(a, "testsrv")}
	st.root = a.explorer.Selected()
	st.dbs = &explorerNode{label: "Databases", data: nodeData{Type: NodeDatabases, conn: st.sc}}
	st.security = &explorerNode{label: "Security", data: nodeData{Type: NodeSecurity, conn: st.sc}}
	st.logins = &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: st.sc}}
	st.root.expanded = true
	a.explorer.SetChildren(st.root, []*explorerNode{st.dbs, st.security})
	st.security.expanded = true
	a.explorer.SetChildren(st.security, []*explorerNode{st.logins})
	st.logins.expanded = true
	st.alice, st.bob, st.carol = testLogin(st.sc, "alice"), testLogin(st.sc, "bob"), testLogin(st.sc, "carol")
	a.explorer.SetChildren(st.logins, []*explorerNode{st.alice, st.bob, st.carol})

	orig := a.explorer.view.OnSelect
	a.explorer.view.OnSelect = func(id controls.TreeNodeID) {
		st.selects = append(st.selects, a.explorer.byID[id])
		orig(id)
	}
	return st
}

func testLogin(sc *db.ServerConn, name string) *explorerNode {
	return &explorerNode{label: name, data: nodeData{Type: NodeLogin, Name: name, conn: sc}}
}

// selectNode selects n as a click would, then forgets that OnSelect so the
// assertions see only what the rebuild under test fired.
func (st *selectionTree) selectNode(t *testing.T, n *explorerNode) {
	t.Helper()
	st.a.explorer.view.SelectID(n.id)
	if got := st.a.explorer.Selected(); got != n {
		t.Fatalf("setup: selected %v, want %q", got, n.label)
	}
	st.selects = nil
}

// reload is what every Refresh does to a folder (ObjectExplorer.Reload): drop
// its children, then install whatever the background fetch returned.
func (st *selectionTree) reload(n *explorerNode, children ...*explorerNode) {
	n.data.Loaded = false
	st.a.explorer.dropChildren(n)
	st.a.explorer.SetChildren(n, children)
}

// screenLine is the selected row's position within the tree's viewport.
func (st *selectionTree) screenLine() int {
	_, y, _ := st.a.explorer.SelectionAnchor()
	return y
}

// Children arriving for a folder above the selection must not move the
// selection onto one of them. They did: TreeView kept the selection by index,
// so after Databases' three databases landed Selected() returned db2 while
// the highlight and the Details pane were still about Security — and Delete,
// Rename and Properties went to db2.
func TestExplorerSelectionSurvivesLoadAboveIt(t *testing.T) {
	st := newSelectionTree(t)
	st.dbs.expanded = true // expanded, still showing "Loading..."
	st.a.explorer.rebuild()
	st.selectNode(t, st.security)
	line := st.screenLine()

	st.a.explorer.SetChildren(st.dbs, []*explorerNode{
		{label: "db1", data: nodeData{Type: NodeDatabase, Name: "db1"}},
		{label: "db2", data: nodeData{Type: NodeDatabase, Name: "db2"}},
		{label: "db3", data: nodeData{Type: NodeDatabase, Name: "db3"}},
	})

	if got := st.a.explorer.Selected(); got != st.security {
		t.Fatalf("Selected() after a load above it = %v, want Security", got)
	}
	if got := st.screenLine(); got != line {
		t.Errorf("selected row moved from screen line %d to %d", line, got)
	}
	if len(st.selects) != 0 {
		t.Errorf("OnSelect fired %d times though the selection did not change", len(st.selects))
	}
}

// A load below the selection changes nothing about it.
func TestExplorerSelectionUnchangedByLoadBelowIt(t *testing.T) {
	st := newSelectionTree(t)
	st.selectNode(t, st.dbs)
	st.dbs.expanded = true
	st.a.explorer.rebuild()

	st.a.explorer.SetChildren(st.dbs, []*explorerNode{
		{label: "db1", data: nodeData{Type: NodeDatabase, Name: "db1"}},
	})

	if got := st.a.explorer.Selected(); got != st.dbs {
		t.Fatalf("Selected() after a load below it = %v, want Databases", got)
	}
	if len(st.selects) != 0 {
		t.Errorf("OnSelect fired %d times though the selection did not change", len(st.selects))
	}
}

// A Refresh re-creates every child, so the selected node is replaced by a new
// one for the same object — which the selection must follow, even when a new
// sibling sorted in above it shifts its row, and report through OnSelect: the
// Details pane is keyed by node pointer and still holds the old one.
func TestExplorerSelectionFollowsRecreatedNode(t *testing.T) {
	st := newSelectionTree(t)
	st.selectNode(t, st.bob)

	st.reload(st.logins, testLogin(st.sc, "aaron"), testLogin(st.sc, "alice"),
		testLogin(st.sc, "bob"), testLogin(st.sc, "carol"))

	got := st.a.explorer.Selected()
	if got == nil || got == st.bob || got.data.Name != "bob" {
		t.Fatalf("Selected() after a refresh = %v, want the re-created bob", got)
	}
	if len(st.selects) != 1 || st.selects[0] != got {
		t.Errorf("OnSelect reported %v, want exactly the re-created bob", st.selects)
	}
}

// Deleting the selected object moves the selection to its parent folder,
// not onto whichever sibling slid into its row.
func TestExplorerSelectionMovesToParentWhenNodeRemoved(t *testing.T) {
	st := newSelectionTree(t)
	st.selectNode(t, st.bob)
	st.a.statusText = FormatNodePath(st.bob)

	st.reload(st.logins, testLogin(st.sc, "alice"), testLogin(st.sc, "carol"))

	if got := st.a.explorer.Selected(); got != st.logins {
		t.Fatalf("Selected() after the selected login was dropped = %v, want Logins", got)
	}
	if len(st.selects) != 1 || st.selects[0] != st.logins {
		t.Errorf("OnSelect reported %v, want exactly Logins", st.selects)
	}
	// The status bar described bob, so it follows the selection.
	if want := FormatNodePath(st.logins); st.a.statusText != want {
		t.Errorf("status = %q, want %q", st.a.statusText, want)
	}
}

// The reload that moves the selection lands just after the write's own status
// message. Replacing that with the new selection's path would wipe "deleted"
// before anyone could read it, so only a status still describing the old
// selection is replaced.
func TestExplorerReselectionKeepsWriteStatus(t *testing.T) {
	st := newSelectionTree(t)
	st.selectNode(t, st.bob)
	st.a.statusText = `"bob" deleted`

	st.reload(st.logins, testLogin(st.sc, "alice"), testLogin(st.sc, "carol"))

	if got := st.a.explorer.Selected(); got != st.logins {
		t.Fatalf("Selected() = %v, want Logins", got)
	}
	if st.a.statusText != `"bob" deleted` {
		t.Errorf("status = %q, want the delete's own message kept", st.a.statusText)
	}
}

// When the parent was re-created too (a Refresh one level higher), the new
// parent comes back collapsed, so the selection lands on it: the nearest
// surviving ancestor, matched by identity.
func TestExplorerSelectionFallsBackToRecreatedAncestor(t *testing.T) {
	st := newSelectionTree(t)
	st.selectNode(t, st.bob)

	newLogins := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: st.sc}}
	st.reload(st.security, newLogins)

	if got := st.a.explorer.Selected(); got != newLogins {
		t.Fatalf("Selected() after Security was refreshed = %v, want the new Logins folder", got)
	}
}

// A selection parked on a "Loading..." row stays on it while rows above change
// — the row keeps one ID across rebuilds for that — and goes to the folder it
// belongs to when the load replaces it, not onto whichever child lands in its
// row.
func TestExplorerSelectionOnLoadingRowGoesToOwner(t *testing.T) {
	st := newSelectionTree(t)
	st.security.data.Loaded = false // Security mid-Refresh, showing "Loading..."
	st.security.children = nil
	st.a.explorer.rebuild()
	st.selectNode(t, st.security)
	st.a.explorer.view.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	onLoadingRow := func() bool {
		tn := st.a.explorer.view.SelectedNode()
		row, ok := tn.Tag.(loadingRow)
		return ok && row.owner == st.security
	}
	if !onLoadingRow() {
		t.Fatalf("setup: Down from a loading folder should land on its Loading row")
	}

	st.dbs.expanded = true // Databases' children arrive above it
	st.a.explorer.SetChildren(st.dbs, []*explorerNode{
		{label: "db1", data: nodeData{Type: NodeDatabase, Name: "db1"}},
		{label: "db2", data: nodeData{Type: NodeDatabase, Name: "db2"}},
	})
	if !onLoadingRow() {
		t.Fatalf("a load above moved the selection off Security's Loading row to %+v", st.a.explorer.view.SelectedNode())
	}

	st.a.explorer.SetChildren(st.security, []*explorerNode{st.logins})
	if got := st.a.explorer.Selected(); got != st.security {
		t.Fatalf("Selected() after the load replaced the Loading row = %v, want Security", got)
	}
}

// Nodes with no Name — folders, log entries — are matched by label, or a
// Refresh would move a selection on the second of several same-typed siblings
// onto the first.
func TestExplorerSelectionMatchesNamelessNodeByLabel(t *testing.T) {
	st := newSelectionTree(t)
	logNode := func(label string) *explorerNode {
		return &explorerNode{label: label, data: nodeData{Type: NodeSQLServerLog, conn: st.sc}}
	}
	current, archive1 := logNode("Current"), logNode("Archive #1")
	st.reload(st.logins, current, archive1) // any expanded folder will do
	st.selectNode(t, archive1)

	st.reload(st.logins, logNode("Current"), logNode("Archive #1"))

	if got := st.a.explorer.Selected(); got == nil || got == archive1 || got.label != "Archive #1" {
		t.Fatalf("Selected() after a refresh = %v, want the re-created Archive #1", got)
	}
}

// beginLoad/endLoad guard App.loadChildren's background fetch against a
// fast double-expand or a Refresh that lands before the first fetch
// returns: a newer beginLoad must cancel the previous fetch's context and
// make its eventual endLoad report itself superseded.
func TestExplorerNodeBeginEndLoad(t *testing.T) {
	n := &explorerNode{}

	ctx1, seq1 := n.beginLoad(context.Background(), time.Minute)
	if seq1 != 1 {
		t.Fatalf("first beginLoad seq = %d, want 1", seq1)
	}
	if ctx1.Err() != nil {
		t.Fatalf("ctx1 already done before being superseded: %v", ctx1.Err())
	}

	ctx2, seq2 := n.beginLoad(context.Background(), time.Minute)
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
	if ctx2.Err() != nil {
		t.Errorf("endLoad(stale seq) cancelled the current fetch's context (err=%v)", ctx2.Err())
	}

	if !n.endLoad(seq2) {
		t.Errorf("endLoad(current seq) = false, want true")
	}
	if n.cancelLoad != nil {
		t.Errorf("endLoad(current seq) should clear cancelLoad")
	}
	// Cleared by calling it, not by dropping it: a discarded CancelFunc
	// leaves the timeout context registered on its parent with the timer
	// armed for the full childFetchTimeout, once per node ever expanded.
	if ctx2.Err() != context.Canceled {
		t.Errorf("endLoad(current seq) dropped cancelLoad without calling it (ctx err=%v)", ctx2.Err())
	}
}

// Disconnecting removes the selected server's whole root, so nothing related
// to it survives: the selection is reported on whichever root is left, and
// removing the last one leaves an empty tree with nothing selected.
func TestExplorerSelectionAfterRootRemoved(t *testing.T) {
	st := newSelectionTree(t)
	other := addTestConn(st.a, "othersrv")
	st.selectNode(t, st.bob)

	st.a.explorer.RemoveRootByConn(st.sc)
	got := st.a.explorer.Selected()
	if got == nil || got.data.conn != other {
		t.Fatalf("Selected() after the selected server's root was removed = %v, want othersrv's root", got)
	}
	if len(st.selects) != 1 || st.selects[0] != got {
		t.Errorf("OnSelect reported %v, want exactly othersrv's root", st.selects)
	}

	st.a.explorer.RemoveRootByConn(other)
	if got := st.a.explorer.Selected(); got != nil {
		t.Errorf("Selected() with no roots left = %v, want nil", got)
	}
}
