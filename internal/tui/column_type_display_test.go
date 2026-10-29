package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
)

// The Object Explorer's column label and Table Properties' Columns grid show
// the same column's type, and both have to go through gosmo's renderer. The
// tree once had its own copy, which dropped a zero scale — datetime2(0) read
// as datetime2, which is scale 7 — and printed an alias type bare, so the tree
// and the dialog disagreed about the same column.

// typedColumnResp answers Table.Columns with a datetime2(0) column and an
// alias-typed one, in columnSelect's scan order.
func typedColumnResp() fakeResponse {
	row := func(name string, ord int64, typ string, maxLen, prec, scale int64, typeSchema string, userDefined bool) []driver.Value {
		return []driver.Value{
			name, ord,
			typ, maxLen, prec, scale,
			true, false, false,
			"", "", "",
			false, "",
			int64(0), int64(0),
			false,
			typeSchema, userDefined,
			false, false,
			false, false,
			"",
			int64(0), false, false,
			int64(0), false,
			"", "", "",
			"", "", false, int64(0), "",
			"", "", "", "", false,
		}
	}
	return fakeResponse{match: "FROM   sys.columns c", db: idxDatabase, cols: 42, rows: [][]driver.Value{
		row("PlacedAt", 1, "datetime2", 6, 19, 0, "sys", false),
		row("ContactPhone", 2, "Phone", 50, 0, 0, "sales", true),
	}}
}

func TestColumnTypeReadsTheSameInTreeAndTableProperties(t *testing.T) {
	want := []string{"datetime2(0)", "[sales].[Phone]"}

	sc, inst := newFakeConn(t, dbByNameResp(idxDatabase, 5), idxTableResp(), typedColumnResp())
	form, _ := loadPage(t, pageTableColumns(sc, idxDatabase, idxSchema, idxTable), inst)
	grid := plainGrid(t, form)
	for i, w := range want {
		row := grid.Row(i)
		if row == nil {
			t.Fatalf("Table Properties grid has no row %d — the fake is under-scripted", i)
		}
		if row[1] != w {
			t.Errorf("Table Properties row %d type = %q, want %q", i, row[1], w)
		}
	}

	sc, _ = newFakeConn(t, dbByNameResp(idxDatabase, 5), idxTableResp(), typedColumnResp())
	l := loaderCtx{ctx: t.Context(), sc: sc}
	children, err := loadColumnsChildren(l, &explorerNode{data: nodeData{
		Type: NodeColumns, DBName: idxDatabase, Schema: idxSchema, Name: idxTable, conn: sc}})
	if err != nil {
		t.Fatalf("loadColumnsChildren: %v", err)
	}
	if len(children) != len(want) {
		t.Fatalf("got %d column nodes, want %d", len(children), len(want))
	}
	for i, w := range want {
		if !strings.Contains(children[i].label, "("+w+", ") {
			t.Errorf("OE label %q does not carry the type %q Table Properties shows", children[i].label, w)
		}
	}
}

// A view's Columns folder reads through Database.ObjectColumns (by name — a
// View has no Columns method) and labels its columns as a table's are. They
// are NodeViewColumn, which has no Delete or Rename: both are ALTER TABLE,
// which a view refuses.
func TestViewColumnsLoadAndOfferNoTableColumnOps(t *testing.T) {
	cols := typedColumnResp()
	cols.match = "OBJECT_ID(@p1)"
	cols.arg = "[" + idxSchema + "].[vOrders]"
	sc, _ := newFakeConn(t, dbByNameResp(idxDatabase, 5), cols)
	l := loaderCtx{ctx: t.Context(), sc: sc}
	children, err := loadViewColumnsChildren(l, &explorerNode{data: nodeData{
		Type: NodeViewColumns, DBName: idxDatabase, Schema: idxSchema, Name: "vOrders", conn: sc}})
	if err != nil {
		t.Fatalf("loadViewColumnsChildren: %v", err)
	}
	want := []string{"PlacedAt (datetime2(0), null)", "ContactPhone ([sales].[Phone], null)"}
	if len(children) != len(want) {
		t.Fatalf("got %d column nodes, want %d", len(children), len(want))
	}
	for i, c := range children {
		if c.label != want[i] {
			t.Errorf("column %d label = %q, want %q", i, c.label, want[i])
		}
		if c.data.Type != NodeViewColumn || c.data.TableName != "vOrders" || c.data.Schema != idxSchema {
			t.Errorf("column %d data = %+v, want a NodeViewColumn of %s.vOrders", i, c.data, idxSchema)
		}
	}
	if op := objectOpFor(NodeViewColumn); op != nil {
		t.Errorf("objectOpFor(NodeViewColumn) = %+v, want nil — a view column cannot be dropped or renamed", op)
	}
}
