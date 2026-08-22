package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Index Properties' two write pages are the ones whose apply reissues the
// index rather than altering it. The Options page's three-way branch decides
// between an ALTER INDEX SET, the same SET without IGNORE_DUP_KEY (which SQL
// Server rejects outright on a constraint-backing index), and a full REBUILD —
// and a rebuild of a large index is an outage, not a setting. The Included
// Columns page has no ALTER at all: every apply is a CREATE INDEX ... WITH
// (DROP_EXISTING = ON) rebuilt from the index's own key columns, so a page
// that hands gosmo the wrong INCLUDE list replaces the index with a different
// one under the same name.
//
// Every fixture here is deliberately not the first row of its list, and the
// index is not the table's only index: a page that ignored what it was pointed
// at would pass on a one-row fake.

const (
	idxTableObjectID = int64(42)
	idxDatabase      = "appdb"
	idxSchema        = "sales"
	idxTable         = "Orders"
)

// idxTableResp answers Database.TableByNameContext for sales.Orders.
func idxTableResp() fakeResponse {
	return fakeResponse{match: "FROM   sys.tables t", db: idxDatabase, cols: 7, rows: [][]driver.Value{
		{idxTableObjectID, idxSchema, idxTable, time.Time{}, time.Time{}, false, false},
	}}
}

// idxListResp answers Table.indexListContext with one index. The scan order is
// sys.indexes' own; see gosmo's table.go.
func idxListResp(name string, indexID int64, opts indexFixture) fakeResponse {
	return fakeResponse{match: "FROM   sys.indexes i", db: idxDatabase, cols: 18, rows: [][]driver.Value{{
		name, indexID, opts.typeDesc,
		opts.unique, opts.primaryKey, opts.uniqueConstraint, false,
		opts.fillFactor, opts.filter,
		opts.padded, opts.ignoreDupKey, opts.rowLocks, opts.pageLocks,
		opts.compression,
		"PRIMARY", int64(0), true, "",
	}}}
}

// indexFixture is the index state a test starts from, so each one says which
// property it is about rather than restating eighteen columns.
type indexFixture struct {
	typeDesc         string
	unique           bool
	primaryKey       bool
	uniqueConstraint bool
	fillFactor       int64
	filter           string
	padded           bool
	ignoreDupKey     bool
	rowLocks         bool
	pageLocks        bool
	compression      string
}

func plainIndex() indexFixture {
	return indexFixture{typeDesc: "NONCLUSTERED", fillFactor: 80, rowLocks: true, pageLocks: true, compression: "NONE"}
}

// idxColumnsResp answers Table.indexColumnsContext: index_id, column name,
// descending, included.
//
// Matched on its ORDER BY rather than on its FROM: the table's own column
// query joins sys.index_columns too (aliased ic2, to find the primary key),
// so "sys.index_columns ic" is a substring of both and this four-column answer
// was served to a scan wanting seventeen.
func idxColumnsResp(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "ic.key_ordinal", db: idxDatabase, cols: 4, rows: rows}
}

// loadIndexOptions loads the Options page for index IX_Orders_CustomerID over
// the given index state.
func loadIndexOptions(t *testing.T, opts indexFixture) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t,
		dbByNameResp(idxDatabase, 5),
		idxTableResp(),
		idxListResp("IX_Orders_CustomerID", 3, opts),
		idxColumnsResp(
			[]driver.Value{int64(3), "CustomerID", false, false},
			[]driver.Value{int64(3), "OrderDate", true, false},
		),
	)
	form, apply := loadPage(t, pageIndexOptions(sc, idxDatabase, idxSchema, idxTable, "IX_Orders_CustomerID"), inst)
	return inst, apply, form
}

// TestIndexOptionsLockChangeDoesNotRebuild. ALLOW_ROW_LOCKS and
// ALLOW_PAGE_LOCKS are plain SET options; issuing a REBUILD for one of them
// would take the index offline to change a flag that needed no rebuild at all.
func TestIndexOptionsLockChangeDoesNotRebuild(t *testing.T) {
	inst, apply, form := loadIndexOptions(t, plainIndex())

	editCheck(t, form, "Allow page locks", false)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "ALTER INDEX [IX_Orders_CustomerID] ON [sales].[Orders] SET")
	stmt := inst.StatementsIn(idxDatabase)[0]
	if strings.Contains(stmt, "REBUILD") {
		t.Errorf("a lock-option change rebuilt the index:\n%s", stmt)
	}
	if !strings.Contains(stmt, "ALLOW_PAGE_LOCKS = OFF") {
		t.Errorf("wrote:\n%s\nwant ALLOW_PAGE_LOCKS = OFF", stmt)
	}
	if !strings.Contains(stmt, "ALLOW_ROW_LOCKS = ON") {
		t.Errorf("wrote:\n%s\nwant the untouched ALLOW_ROW_LOCKS = ON carried through", stmt)
	}
}

// TestIndexOptionsFillFactorRebuilds. Fill factor only takes effect on a
// rebuild, so a page that wrote it as a SET option would report success and
// change nothing.
func TestIndexOptionsFillFactorRebuilds(t *testing.T) {
	inst, apply, form := loadIndexOptions(t, plainIndex())

	editText(t, form, "Fill factor", "70")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "REBUILD WITH")
	stmt := inst.StatementsIn(idxDatabase)[0]
	if !strings.Contains(stmt, "FILLFACTOR = 70") {
		t.Errorf("wrote:\n%s\nwant FILLFACTOR = 70", stmt)
	}
	// Compression was not touched, so the rebuild must not name one: an
	// unasked-for DATA_COMPRESSION rewrites every page of the index.
	if strings.Contains(stmt, "DATA_COMPRESSION") {
		t.Errorf("rebuild names a compression nobody chose:\n%s", stmt)
	}
}

// TestIndexOptionsCompressionRebuildsWithTheValueChosen pins the dropdown to
// the keyword: ROW and PAGE are both valid, and a page that sent the wrong one
// rewrites the whole index into a storage format the user did not pick.
func TestIndexOptionsCompressionRebuildsWithTheValueChosen(t *testing.T) {
	inst, apply, form := loadIndexOptions(t, plainIndex())

	editSelect(t, form, "Data compression", "PAGE")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "DATA_COMPRESSION = PAGE")
}

// TestIndexOptionsOnAConstraintBackedIndexNeverSendsIgnoreDupKey. SQL Server
// rejects IGNORE_DUP_KEY on an index backing a PRIMARY KEY or UNIQUE
// constraint even when the value is unchanged, so the whole apply fails and
// the user's lock-option edit is lost with it. The page's answer is to drop
// the row and take the SetLockOptions path; this is what proves it took it.
func TestIndexOptionsOnAConstraintBackedIndexNeverSendsIgnoreDupKey(t *testing.T) {
	idx := plainIndex()
	idx.typeDesc = "CLUSTERED"
	idx.primaryKey = true
	idx.unique = true
	inst, apply, form := loadIndexOptions(t, idx)

	editCheck(t, form, "Allow row locks", false)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "ALLOW_ROW_LOCKS = OFF")
	if stmt := inst.StatementsIn(idxDatabase)[0]; strings.Contains(stmt, "IGNORE_DUP_KEY") {
		t.Errorf("wrote IGNORE_DUP_KEY on a constraint-backing index:\n%s", stmt)
	}
}

// TestIndexOptionsWritesNothingWhenUntouched. Every row on this page is
// populated from the server, and a page that mistook "loaded" for "edited"
// would rebuild an index on an Apply the user made for a different page.
func TestIndexOptionsWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _ := loadIndexOptions(t, plainIndex())

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, idxDatabase)
}

// -- Included Columns --------------------------------------------------------

// idxColumnResp answers Table.ColumnsContext. Only the name and the type
// matter to this page; the rest of columnSelect's seventeen values are what
// the scan needs to get that far.
func idxColumnResp(names ...string) fakeResponse {
	rows := make([][]driver.Value, len(names))
	for i, n := range names {
		rows[i] = []driver.Value{
			n, int64(i + 1),
			"int", int64(4), int64(10), int64(0),
			false, false, false,
			"", "", "",
			false, "",
			int64(0), int64(0),
			false,
		}
	}
	return fakeResponse{match: "FROM   sys.columns c", db: idxDatabase, cols: 17, rows: rows}
}

// loadIncludedColumns loads the Included Columns page for an index keyed on
// CustomerID with OrderTotal already included, over a five-column table.
func loadIncludedColumns(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t,
		dbByNameResp(idxDatabase, 5),
		idxTableResp(),
		idxListResp("IX_Orders_CustomerID", 3, plainIndex()),
		idxColumnsResp(
			[]driver.Value{int64(3), "CustomerID", false, false},
			[]driver.Value{int64(3), "OrderTotal", false, true},
		),
		idxColumnResp("OrderID", "CustomerID", "OrderDate", "OrderTotal", "ShipCity"),
	)
	form, apply := loadPage(t, pageIndexIncludedColumns(sc, idxDatabase, idxSchema, idxTable, "IX_Orders_CustomerID"), inst)
	// The grid is built from the eligible columns, and an under-scripted fake
	// yields an empty one — on which every assertion below passes for the
	// wrong reason.
	if got := len(toggleGrid(t, form).Text()); got != 4 {
		t.Fatalf("the grid has %d rows, want the 4 non-key columns — the fake is under-scripted, not the page wrong", got)
	}
	return inst, apply, form
}

// TestIncludedColumnsKeyColumnsAreNotOffered. A key column ticked into the
// INCLUDE list makes SQL Server reject the CREATE outright ("Cannot use
// duplicate column names"), and the index is already gone by then only if the
// page got as far as DROP_EXISTING — so the guard belongs before the write.
func TestIncludedColumnsKeyColumnsAreNotOffered(t *testing.T) {
	_, _, form := loadIncludedColumns(t)

	for _, row := range toggleGrid(t, form).Text() {
		if row[0] == "CustomerID" {
			t.Fatal("the index's key column is offered as an includable column")
		}
	}
}

// TestIncludedColumnsAddsTheColumnTheRowIsNamedFor ticks the fourth row.
// The whole index is reissued from this list, so a page that read the grid
// back off by one recreates the index over a column the user never chose —
// under the same name, so nothing afterwards says it happened.
func TestIncludedColumnsAddsTheColumnTheRowIsNamedFor(t *testing.T) {
	inst, apply, form := loadIncludedColumns(t)

	toggleByName(t, toggleGrid(t, form), "ShipCity", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "INCLUDE ([OrderTotal], [ShipCity])")
	stmt := inst.StatementsIn(idxDatabase)[0]
	for _, want := range []string{
		"CREATE NONCLUSTERED INDEX [IX_Orders_CustomerID] ON [sales].[Orders]",
		"([CustomerID] ASC)",
		"WITH (DROP_EXISTING = ON)",
	} {
		if !strings.Contains(stmt, want) {
			t.Errorf("wrote:\n%s\nwant it to contain: %s", stmt, want)
		}
	}
}

// TestIncludedColumnsRemoveDropsOnlyThatColumn is the destructive direction:
// the reissued index carries exactly the columns still ticked, so a page that
// sent the wrong list silently narrows the index and every query that relied
// on it starts doing key lookups.
func TestIncludedColumnsRemoveDropsOnlyThatColumn(t *testing.T) {
	inst, apply, form := loadIncludedColumns(t)

	grid := toggleGrid(t, form)
	toggleByName(t, grid, "ShipCity", 0)   // in
	toggleByName(t, grid, "OrderTotal", 0) // out

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "INCLUDE ([ShipCity])")
	if stmt := inst.StatementsIn(idxDatabase)[0]; strings.Contains(stmt, "OrderTotal") {
		t.Errorf("the unticked column is still in the reissued index:\n%s", stmt)
	}
}

// TestIncludedColumnsWritesNothingWhenUntouched. Every apply on this page is a
// DROP_EXISTING rebuild of the whole index, so an apply on an untouched page
// is an unasked-for outage rather than a no-op.
func TestIncludedColumnsWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _ := loadIncludedColumns(t)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, idxDatabase)
}
