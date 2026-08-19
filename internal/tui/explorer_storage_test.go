package tui

import (
	"context"
	"slices"
	"testing"
)

// The Storage and Always Encrypted Keys folders are static, so their
// children are exactly the subfolders the loaders name — the same wiring
// TestStaticLoadersPropagateDBName pins for a database's own folders.
func TestStorageAndKeyFoldersPropagateDBName(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	for _, tt := range []struct {
		nt     NodeType
		labels []string
	}{
		{NodeStorage, []string{"Partition Functions", "Partition Schemes"}},
		{NodeAlwaysEncryptedKeys, []string{"Column Master Keys", "Column Encryption Keys"}},
	} {
		node := &explorerNode{data: nodeData{Type: tt.nt, DBName: "AppDB", conn: sc}}
		children, err := childLoaders[tt.nt](l, node)
		if err != nil {
			t.Fatalf("loader for %v: %v", tt.nt, err)
		}
		var got []string
		for _, c := range children {
			got = append(got, c.label)
			if c.data.DBName != "AppDB" {
				t.Errorf("%q.DBName = %q, want AppDB", c.label, c.data.DBName)
			}
		}
		if !slices.Equal(got, tt.labels) {
			t.Errorf("children = %v, want %v", got, tt.labels)
		}
	}
}

// Every new folder is expandable and every new leaf is not — hasChildren
// and childLoaders have to agree, or a leaf shows a permanent expander or a
// folder can't be opened.
func TestNewNodeTypesAreFoldersOrLeavesConsistently(t *testing.T) {
	folders := []NodeType{NodeStorage, NodePartitionFunctions, NodePartitionSchemes,
		NodeSecurityPolicies, NodeAlwaysEncryptedKeys, NodeColumnMasterKeys, NodeColumnEncryptionKeys}
	for _, nt := range folders {
		if !hasChildren(nt) {
			t.Errorf("folder %d reports no children", nt)
		}
		if !isContainerNode(nt) {
			t.Errorf("folder %d is not a container — it draws an object icon", nt)
		}
		if _, ok := childLoaders[nt]; !ok {
			t.Errorf("folder %d has no loader — expanding it yields nothing", nt)
		}
	}
	leaves := []NodeType{NodePartitionFunction, NodePartitionScheme, NodeSecurityPolicy,
		NodeColumnMasterKey, NodeColumnEncryptionKey}
	for _, nt := range leaves {
		if hasChildren(nt) {
			t.Errorf("leaf %d claims children", nt)
		}
		if isContainerNode(nt) {
			t.Errorf("leaf %d draws as a folder", nt)
		}
	}
}

// Each new family is scriptable and deletable, and every one of them names
// itself rather than falling back to the generic "Object".
func TestNewFamiliesAreScriptableAndDeletable(t *testing.T) {
	for _, nt := range []NodeType{NodePartitionFunction, NodePartitionScheme,
		NodeSecurityPolicy, NodeColumnMasterKey, NodeColumnEncryptionKey} {
		if _, ok := scriptables[nt]; !ok {
			t.Errorf("node type %d has no Script cascade", nt)
		}
		op := objectOpFor(nt)
		if op == nil || op.drop == nil {
			t.Errorf("node type %d cannot be deleted", nt)
		}
		if nodeTypeName(nt) == "Object" {
			t.Errorf("node type %d has no display name", nt)
		}
	}
}

// A security policy's menu offers the toggle its current state calls for,
// and its label carries that state too.
func TestSecurityPolicyMenuOffersTheOppositeToggle(t *testing.T) {
	a := newTestApp()
	for _, tt := range []struct {
		enabled bool
		want    string
	}{{true, "Disable"}, {false, "Enable"}} {
		node := opNode(NodeSecurityPolicy, "sec", "pol", "")
		node.data.IsEnabled = tt.enabled
		labels := labelsOf(a.nodeMenuItems(node))
		if !slices.Contains(labels, tt.want) {
			t.Errorf("enabled=%v menu = %v, want %q", tt.enabled, labels, tt.want)
		}
	}
}
