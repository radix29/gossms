package tui

import (
	"testing"
)

// Both loaders below latch a placeholder before their goroutine starts — the
// Object Explorer's "Loading..." child, the details panel's "Loading..."
// status — and clear it only in the callback the goroutine posts. A panic
// unwinds straight past that callback, so App.safego alone reports the panic
// and leaves the placeholder up for good. These pin the App.safegoRepair step
// that replaces it with something the user can act on.

// panicLoader replaces the loader for typ with one that panics, restoring the
// original when the test ends. childLoaders is package state, so this must not
// run in parallel with anything that expands the same node type.
func panicLoader(t *testing.T, typ NodeType) {
	t.Helper()
	orig, had := childLoaders[typ]
	childLoaders[typ] = func(loaderCtx, *explorerNode) ([]*explorerNode, error) {
		panic("loader boom")
	}
	t.Cleanup(func() {
		if had {
			childLoaders[typ] = orig
			return
		}
		delete(childLoaders, typ)
	})
}

func TestExplorerExpandShowsAnErrorWhenItsLoaderPanics(t *testing.T) {
	a := newTestApp()
	addTestConn(a, "server-one")
	node := a.explorer.Selected()
	if node == nil {
		t.Fatal("setup: no root node selected")
	}
	panicLoader(t, node.data.Type)

	a.loadChildren(node)
	drainUntil(t, a, func() bool { return node.data.Loaded },
		"the panicking expand to release the node")

	// Loaded is the latch: while it is false the tree draws a "Loading..."
	// child under the node, forever.
	if len(node.children) != 1 {
		t.Fatalf("node has %d children after a panicking load, want 1 error node", len(node.children))
	}
	if got := node.children[0].data.Type; got != NodeError {
		t.Errorf("child node type = %v, want NodeError", got)
	}
	if got := node.children[0].label; got != errChildFetchPanicked.Error() {
		t.Errorf("child label = %q, want %q", got, errChildFetchPanicked.Error())
	}
}

// The repair must not outrun a newer expand of the same node. endLoad is what
// enforces that on the success path, and the repair has to honour it too — or
// a panicking fetch replaces the children a later, working one just installed.
func TestExplorerExpandPanicDoesNotClobberANewerLoad(t *testing.T) {
	a := newTestApp()
	addTestConn(a, "server-one")
	node := a.explorer.Selected()
	if node == nil {
		t.Fatal("setup: no root node selected")
	}

	// seq 1 is the fetch that panicked; seq 2 has since superseded it and is
	// still out. Taken straight from beginLoad rather than assigned by hand,
	// so the test tracks whatever numbering it uses.
	_, stale := node.beginLoad(t.Context(), childFetchTimeout)
	a.explorer.SetChildren(node, []*explorerNode{{label: "from the newer load"}})
	node.beginLoad(t.Context(), childFetchTimeout)

	a.childFetchPanicked(node, stale)

	if len(node.children) != 1 || node.children[0].label != "from the newer load" {
		t.Fatalf("a stale fetch's panic repair overwrote the newer load's children: %+v", node.children)
	}
}

// The details panel's counterpart. Driving a panic through fetch itself would
// mean injecting into fetchNodeDetails, which is not a var — so this exercises
// the repair directly against the two guards it has to keep.
func TestDetailBrowserPanicRepairClearsTheLoadingLatch(t *testing.T) {
	dbr := NewDetailBrowser("details")
	node := &explorerNode{label: "n"}

	dbr.seq = 7
	dbr.pending[node] = 7
	dbr.grid.SetStatus("Loading...")

	dbr.panicRepair(node, 7)()

	if _, still := dbr.pending[node]; still {
		t.Error("pending entry survived the panic — the node would never refetch")
	}
	if _, cached := dbr.cache[node]; cached {
		t.Error("a panic was cached as this node's details; the next selection " +
			"would be served the failure instead of retrying")
	}
	if got := dbr.grid.Status(); got != "Error" {
		t.Errorf("grid status = %q after the repair, want %q — the "+
			"Loading... placeholder is still up", got, "Error")
	}
	if got := dbr.grid.Row(0)[0]; got != errDetailFetchPanicked.Error() {
		t.Errorf("grid shows %q, want %q", got, errDetailFetchPanicked.Error())
	}
}

func TestDetailBrowserPanicRepairIgnoresASupersededFetch(t *testing.T) {
	dbr := NewDetailBrowser("details")
	node := &explorerNode{label: "n"}

	// A newer fetch (seq 8) for the same node is in flight, and the user has
	// since selected something else, so neither guard may fire.
	dbr.seq = 9
	dbr.pending[node] = 8

	dbr.panicRepair(node, 7)()

	if got := dbr.pending[node]; got != 8 {
		t.Errorf("pending[node] = %d after a stale panic, want 8 — the newer "+
			"fetch's entry was dropped and its result will not be cached", got)
	}
	if got := dbr.grid.Status(); got == "Error" {
		t.Error("a stale fetch's panic painted an error over the node the user " +
			"has since selected")
	}
}
