package tui

import (
	"slices"
	"testing"
)

// TestSchemaGroupFolder: a group's Details run as its folder's — the folder's
// type, label and filter, the group's database and connection, and no schema,
// so the folder's loader lists what it lists for the folder itself.
func TestSchemaGroupFolder(t *testing.T) {
	a := newTestApp()
	a.groupBySchema = true
	folder := groupTree(t, a, NodeViews, obj(NodeView, "dbo", "v1"), obj(NodeView, "app", "v2"))
	folder.data.Filter = &nodeFilter{criteria: []filterCriterion{{prop: filterProp{id: fpName, kind: filterText}, op: opContains, value: "v"}}}
	a.explorer.applyGrouping(folder)
	g := folder.children[1]
	if g.data.Type != NodeSchemaGroup || g.data.Schema != "dbo" {
		t.Fatalf("children[1] = %v %q, want the dbo group", g.data.Type, g.data.Schema)
	}
	got := schemaGroupFolder(g)
	if got.data.Type != NodeViews || got.data.Schema != "" || got.data.DBName != "db1" ||
		got.data.conn != folder.data.conn || got.data.Filter != folder.data.Filter || got.label != folder.label {
		t.Errorf("schemaGroupFolder = %q %+v, want the Views folder's type, label, filter and connection, no schema", got.label, got.data)
	}
	if got == g || g.data.Type != NodeSchemaGroup {
		t.Error("schemaGroupFolder must return a snapshot, leaving the group as it was")
	}
}

// TestRowsInSchema: a group keeps its schema's rows with their objects, by
// the objects (not the Name cell), and drops rows it cannot attribute.
func TestRowsInSchema(t *testing.T) {
	rows := [][]string{{"dbo.a"}, {"app.b"}, {"dbo.c"}, {"Dbo.d"}}
	objs := []nodeData{{Schema: "dbo", Name: "a"}, {Schema: "app", Name: "b"}, {Schema: "dbo", Name: "c"}, {Schema: "Dbo", Name: "d"}}
	gotRows, gotObjs := rowsInSchema(rows, objs, "dbo")
	if want := [][]string{{"dbo.a"}, {"dbo.c"}}; !slices.EqualFunc(gotRows, want, slices.Equal) {
		t.Errorf("rows = %q, want %q (case-sensitive, like the groups)", gotRows, want)
	}
	if len(gotObjs) != 2 || gotObjs[0].Name != "a" || gotObjs[1].Name != "c" {
		t.Errorf("objs = %+v, want a and c alongside their rows", gotObjs)
	}
	if r, o := rowsInSchema(rows, objs[:3], "dbo"); r != nil || o != nil {
		t.Errorf("rows without a parallel object list: got %q %+v, want none", r, o)
	}
}

// TestSchemaCountRows: a grouped folder's rows are its schemas in the tree's
// order (case-insensitive, first seen breaking ties), each counted, with no
// row for a schema-less entry such as a System Functions sub-folder.
func TestSchemaCountRows(t *testing.T) {
	objs := []nodeData{
		{Type: NodeSystemFunctions},
		{Type: NodeFunction, Schema: "sales"}, {Type: NodeFunction, Schema: "dbo"},
		{Type: NodeFunction, Schema: "Sales"}, {Type: NodeFunction, Schema: "dbo"},
		{Type: NodeFunction, Schema: "app"},
	}
	want := [][]string{{"app", "1"}, {"dbo", "2"}, {"sales", "1"}, {"Sales", "1"}}
	if got := schemaCountRows(objs); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows = %q, want %q", got, want)
	}
	if got := schemaCountRows(nil); len(got) != 0 {
		t.Errorf("no objects: rows = %q, want none", got)
	}
}

// TestGroupToggleInvalidatesDetails: a cached Details entry for a grouped
// folder or a group was built in the other mode, so the toggle drops it; every
// other node's entry is kept.
func TestGroupToggleInvalidatesDetails(t *testing.T) {
	a := newKeyTestApp(t)
	folder := groupTree(t, a, NodeTables, obj(NodeTable, "dbo", "A"))
	other := subFolder(NodeSequences)
	a.detailBrowser = NewDetailBrowser("Details")
	dbr := a.detailBrowser

	a.toggleGroupBySchema()
	group := folder.children[0]
	if group.data.Type != NodeSchemaGroup {
		t.Fatalf("children[0] = %v, want a schema group", group.data.Type)
	}
	for _, n := range []*explorerNode{folder, group, other, folder.parent} {
		dbr.cache[n] = &detailResult{cols: []string{"Name"}}
	}
	a.toggleGroupBySchema()
	for n, want := range map[*explorerNode]bool{folder: false, group: false, other: true, folder.parent: true} {
		if _, ok := dbr.cache[n]; ok != want {
			t.Errorf("%s: cached = %v after the toggle, want %v", n.label, ok, want)
		}
	}
}
