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
		}
	}
	return fakeResponse{match: "FROM   sys.columns c", db: idxDatabase, cols: 37, rows: [][]driver.Value{
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
