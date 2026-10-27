package tui

import (
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// TestAgentJobsIconIsStopwatchNotFolder checks NodeAgentJobs ("SQL Server
// Agent") gets its own distinct glyph rather than the generic folder icon
// every other container node type falls back to.
func TestAgentJobsIconIsStopwatchNotFolder(t *testing.T) {
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		d := nodeData{Type: NodeAgentJobs}
		if got := nodeIcon(d, style, false); got != '⏱' {
			t.Errorf("nodeIcon(NodeAgentJobs, %v, false) = %q, want stopwatch ⏱", style, got)
		}
		if got := nodeIcon(d, style, true); got != '⏱' {
			t.Errorf("nodeIcon(NodeAgentJobs, %v, true) = %q, want stopwatch ⏱ regardless of expanded state", style, got)
		}
	}
	if got := nodeIcon(nodeData{Type: NodeAgentJobs}, config.IconStyleNone, false); got != 0 {
		t.Errorf("nodeIcon(NodeAgentJobs, IconStyleNone, false) = %q, want 0 (no icon)", got)
	}
}

// TestGroupedFolderTypes pins the families Group by Schema regroups: the main
// object folders only, with the System * folders, Sequences and Types left
// flat (decided scope). Every NodeType is checked, so a type added to the set
// by accident fails here too.
func TestGroupedFolderTypes(t *testing.T) {
	want := map[NodeType]bool{
		NodeTables:           true,
		NodeViews:            true,
		NodeSynonyms:         true,
		NodeStoredProcedures: true,
		NodeFunctions:        true,
	}
	for nt := range nodeTypeCount {
		if got := groupedFolderTypes[nt]; got != want[nt] {
			t.Errorf("groupedFolderTypes[%s] = %v, want %v", nodeTypeName(nt), got, want[nt])
		}
		if groupedFolderTypes[nt] && !isContainerNode(nt) {
			t.Errorf("%s is grouped but is not a folder", nodeTypeName(nt))
		}
	}
}

// TestSchemaGroupNode checks a NodeSchemaGroup is a folder drawn with the
// schema glyph, open or closed, and offers New Query and Refresh only.
func TestSchemaGroupNode(t *testing.T) {
	if !isContainerNode(NodeSchemaGroup) {
		t.Error("NodeSchemaGroup is not a folder")
	}
	if _, ok := childLoaders[NodeSchemaGroup]; ok {
		t.Error("NodeSchemaGroup has a childLoaders entry; its children come from regrouping")
	}
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		want := objectIcon(NodeSchema, style)
		for _, expanded := range []bool{false, true} {
			if got := nodeIcon(nodeData{Type: NodeSchemaGroup}, style, expanded); got != want {
				t.Errorf("nodeIcon(NodeSchemaGroup, %v, %v) = %q, want the schema glyph %q", style, expanded, got, want)
			}
		}
	}

	sc, _ := newFakeConn(t)
	a := newTestApp()
	folder := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: sc, DBName: "db1"}}
	group := &explorerNode{label: "dbo", parent: folder, data: nodeData{
		Type: NodeSchemaGroup, DBName: "db1", Schema: "dbo", GroupOf: NodeTables, Loaded: true,
	}}
	folder.children = []*explorerNode{group}
	folder.data.Loaded = true
	var labels []string
	var refresh func()
	for _, it := range a.nodeMenuItems(group) {
		if !it.Divider {
			labels = append(labels, it.Label)
		}
		if it.Label == refreshMenuLabel {
			refresh = it.Action
		}
	}
	if want := []string{"New Query", refreshMenuLabel}; !slices.Equal(labels, want) {
		t.Fatalf("NodeSchemaGroup menu = %v, want %v", labels, want)
	}
	// Refresh reloads the folder: the group has no loader, so reloading the
	// group itself would leave it empty.
	refresh()
	if folder.data.Loaded || !group.retired {
		t.Errorf("Refresh on a schema group: folder Loaded = %v, group retired = %v; want the folder reloaded",
			folder.data.Loaded, group.retired)
	}
}
