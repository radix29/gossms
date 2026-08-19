package tui

import (
	"sync"
	"testing"
)

// The tree and Detail Browser loaders run on background goroutines while the
// UI goroutine keeps writing the folder node's data — applyNodeFilter sets
// data.Filter, and toggleSecurityPolicy and the object ops write it too. The
// loaders therefore take an explorerNode.snapshot, never the live node. These
// tests pin that: the first two assert the snapshot's data cannot be reached
// by a later write, and the third is the concurrent shape the race detector
// has to stay quiet on.

// TestSnapshotIsUnaffectedByLaterDataWrite is the whole point of the
// snapshot: a fetch already under way must keep seeing the folder as it was
// when it started, not a filter installed halfway through.
func TestSnapshotIsUnaffectedByLaterDataWrite(t *testing.T) {
	orig := &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "cust"}}}
	node := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, DBName: "sales", Filter: orig}}

	snap := node.snapshot()

	// Everything applyNodeFilter and the object ops write on the UI goroutine.
	node.data.Filter = &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "order"}}}
	node.data.DBName = "other"
	node.label = "Tables (filtered)"

	if snap.data.Filter != orig {
		t.Errorf("snapshot filter changed under the loader: got %v, want the filter installed at snapshot time", snap.data.Filter)
	}
	if snap.data.DBName != "sales" {
		t.Errorf("snapshot DBName = %q, want %q", snap.data.DBName, "sales")
	}
	if snap.label != "Tables" {
		t.Errorf("snapshot label = %q, want %q", snap.label, "Tables")
	}
}

// TestSnapshotFiltersByTheFilterItCaptured checks the snapshot is actually
// usable as the loader's view of the folder: filtering through it applies the
// criteria that were in force when the fetch began, not the ones that
// replaced them.
func TestSnapshotFiltersByTheFilterItCaptured(t *testing.T) {
	node := &explorerNode{data: nodeData{
		Type:   NodeTables,
		Filter: &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "cust"}}},
	}}
	snap := node.snapshot()

	// The UI goroutine narrows the filter to something matching nothing below.
	node.data.Filter = &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "zzz"}}}

	children := []*explorerNode{
		{label: "Customers", data: nodeData{Type: NodeTable, Name: "Customers"}},
		{label: "Orders", data: nodeData{Type: NodeTable, Name: "Orders"}},
	}
	got := filterChildren(snap.data.Filter, children)
	if len(got) != 1 || got[0].data.Name != "Customers" {
		t.Fatalf("filterChildren through the snapshot = %v, want just Customers", nodeNames(got))
	}
}

// TestSnapshotIsRaceFreeAgainstConcurrentFilterWrites reproduces the shipped
// arrangement: the snapshot is taken on the UI goroutine, the loader reads
// only the snapshot, and the UI goroutine keeps writing the live node
// meanwhile. Meaningful under -race, which is where the loaders taking the
// live node showed up as a write/read on node.data.
func TestSnapshotIsRaceFreeAgainstConcurrentFilterWrites(t *testing.T) {
	node := &explorerNode{data: nodeData{Type: NodeTables, Filter: nil}}
	children := []*explorerNode{{label: "Customers", data: nodeData{Type: NodeTable, Name: "Customers"}}}

	const rounds = 200
	var wg sync.WaitGroup
	loaded := make(chan int, rounds)

	for range rounds {
		// The UI goroutine's half: snapshot, hand it off, then write the node.
		snap := node.snapshot()
		wg.Add(1)
		go func() {
			defer wg.Done()
			loaded <- len(filterChildren(snap.data.Filter, children))
		}()
		node.data.Filter = &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "cust"}}}
	}
	wg.Wait()
	close(loaded)

	for n := range loaded {
		if n != 1 {
			t.Fatalf("loader saw %d children, want 1 — every snapshot here has a filter Customers passes", n)
		}
	}
}

func nodeNames(nodes []*explorerNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.data.Name
	}
	return out
}
