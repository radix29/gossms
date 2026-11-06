package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// An indexed view's Indexes and Statistics folders are the table's folder
// types, so what they must get right is which object they read and what
// their leaves carry: the view, read by ViewByName (a TableByName finds no
// view), and OnView on every leaf, which the Properties pages' extended-
// property level and a Delete reached from the leaf depend on.

const idxView = "vOrders"

// idxViewResp answers Database.ViewByName for sales.vOrders, schema-bound.
func idxViewResp() fakeResponse {
	return fakeResponse{match: "FROM   sys.views o", db: idxDatabase, arg: idxView, cols: 6, rows: [][]driver.Value{
		{idxTableObjectID, idxSchema, idxView, time.Time{}, time.Time{}, true},
	}}
}

func TestViewIndexesFolderReadsTheView(t *testing.T) {
	sc, _ := newGConn(t,
		dbByNameResp(idxDatabase, 5),
		idxViewResp(),
		idxListResp("CIX_vOrders", 1, indexFixture{typeDesc: "CLUSTERED", unique: true, rowLocks: true, pageLocks: true, compression: "NONE"}),
		idxColumnsResp([]driver.Value{int64(1), "OrderID", false, false}),
	)
	l := loaderCtx{ctx: t.Context(), sc: sc}
	children, err := loadIndexesChildren(l, &explorerNode{data: nodeData{
		Type: NodeIndexes, DBName: idxDatabase, Schema: idxSchema, Name: idxView, OnView: true, conn: sc}})
	if err != nil {
		t.Fatalf("loadIndexesChildren: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("got %d index nodes, want 1", len(children))
	}
	got := children[0]
	if got.label != "CIX_vOrders (Clustered, Unique)" || got.data.TableName != idxView || !got.data.OnView {
		t.Errorf("index node = %q %+v, want CIX_vOrders on %s marked OnView", got.label, got.data, idxView)
	}
}

// Delete on a view's index resolves the name a table read cannot find as the
// view, and drops the index ON the view.
func TestDeleteOfAViewIndexDropsItOnTheView(t *testing.T) {
	sc, inst := newGConn(t,
		dbByNameResp(idxDatabase, 5),
		fakeResponse{match: "FROM   sys.tables t", db: idxDatabase, arg: idxView, cols: 12},
		idxViewResp(),
		idxListResp("IX_vOrders_n", 2, plainIndex()),
		idxColumnsResp([]driver.Value{int64(2), "n", false, false}),
	)
	n := nodeData{Type: NodeIndex, DBName: idxDatabase, Schema: idxSchema, TableName: idxView, Name: "IX_vOrders_n", OnView: true}
	if err := objectOps[NodeIndex].drop(t.Context(), sc, n); err != nil {
		t.Fatalf("drop: %v", err)
	}
	want := "DROP INDEX [IX_vOrders_n] ON [sales].[vOrders]"
	stmts := inst.StatementsIn(idxDatabase)
	if len(stmts) != 1 || !strings.Contains(stmts[0], want) {
		t.Errorf("statements = %q, want one %q", stmts, want)
	}
}

func TestViewIndexExtendedPropertiesAreAtTheViewLevel(t *testing.T) {
	for _, tc := range []struct {
		onView bool
		want   string
	}{{true, "VIEW"}, {false, "TABLE"}} {
		if got := extPropObjectLevel(tc.onView); got != tc.want {
			t.Errorf("extPropObjectLevel(%v) = %q, want %q", tc.onView, got, tc.want)
		}
	}
}
