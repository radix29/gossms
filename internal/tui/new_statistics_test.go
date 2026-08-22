package tui

import (
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// New Statistics builds one CREATE STATISTICS request. The part worth pinning
// here is the column order — the leading column is the only one that gets a
// histogram, so a picker that lost the order would build a different
// statistics object from the one the user described.

func nstatDialog(t *testing.T, pf *nstatPrefetch) *NewStatisticsDialog {
	t.Helper()
	d := &NewStatisticsDialog{dbName: "AppDB", schema: "dbo", table: "Orders"}
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(pf)
	return d
}

func nstatTestPrefetch() *nstatPrefetch {
	return &nstatPrefetch{
		columns: []*gosmo.Column{
			{Name: "OrderID", DataType: gosmo.DataType("int")},
			{Name: "Customer", DataType: gosmo.DataType("nvarchar")},
			{Name: "Placed", DataType: gosmo.DataType("datetime2")},
		},
		existingNames: map[string]bool{"st_orders_existing": true},
	}
}

func TestNewStatisticsRequest(t *testing.T) {
	d := nstatDialog(t, nstatTestPrefetch())
	f := d.forms[0]

	editText(t, f, "Name", "ST_Orders_Customer")
	// Added in an order that is neither the table's nor alphabetical.
	chooseSelect(t, f, "Column to add", "Placed")
	clickButton(t, f, "Add")
	chooseSelect(t, f, "Column to add", "Customer")
	clickButton(t, f, "Add")

	editSelect(t, f, "Sampling", "Full scan")
	editText(t, f, "Filter predicate", "[Customer] IS NOT NULL")
	editCheck(t, f, "Do not recompute automatically", true)
	editCheck(t, f, "Incremental (per partition)", true)

	req := d.request()
	if req.Name != "ST_Orders_Customer" {
		t.Errorf("name = %q, want the page's", req.Name)
	}
	if !slices.Equal(req.Columns, []string{"Placed", "Customer"}) {
		t.Errorf("columns = %v, want them in the order they were added", req.Columns)
	}
	if !req.FullScan || req.SamplePercent != 0 {
		t.Errorf("sampling = %+v, want a full scan and no percentage", req)
	}
	if req.FilterDefinition != "[Customer] IS NOT NULL" || !req.NoRecompute || !req.Incremental {
		t.Errorf("request = %+v, want the page's filter and options", req)
	}
}

// The sample percentage only applies when Sampling says so — a percentage
// left in the box under "Server default" must not reach the statement, since
// gosmo refuses a request that carries both.
func TestNewStatisticsSamplePercentOnlyWhenChosen(t *testing.T) {
	d := nstatDialog(t, nstatTestPrefetch())
	f := d.forms[0]
	chooseSelect(t, f, "Column to add", "Customer")
	clickButton(t, f, "Add")
	editText(t, f, "Sample percent", "25")

	if req := d.request(); req.SamplePercent != 0 || req.FullScan {
		t.Errorf("request = %+v, want the server default while Sampling says so", req)
	}
	editSelect(t, f, "Sampling", "Sample percent")
	if req := d.request(); req.SamplePercent != 25 || req.FullScan {
		t.Errorf("request = %+v, want the 25%% sample the page asked for", req)
	}
}

func TestNewStatisticsMoveDownReordersTheColumns(t *testing.T) {
	d := nstatDialog(t, nstatTestPrefetch())
	f := d.forms[0]
	for _, name := range []string{"OrderID", "Customer", "Placed"} {
		chooseSelect(t, f, "Column to add", name)
		clickButton(t, f, "Add")
	}

	// Not the first row: a page that ignored the selection would still pass.
	selectGridRow(t, plainGrid(t, f), 1, "Customer")
	clickButton(t, f, "Move Down")

	if got := d.request().Columns; !slices.Equal(got, []string{"OrderID", "Placed", "Customer"}) {
		t.Errorf("columns = %v, want Customer moved past Placed", got)
	}
	// The selection follows the column it moved, so Remove now takes that one.
	clickButton(t, f, "Remove")
	if got := d.request().Columns; !slices.Equal(got, []string{"OrderID", "Placed"}) {
		t.Errorf("columns = %v, want the moved column removed", got)
	}
}

func TestNewStatisticsPreflight(t *testing.T) {
	pf := nstatTestPrefetch()

	t.Run("no name", func(t *testing.T) {
		d := nstatDialog(t, pf)
		editText(t, d.forms[0], "Name", "")
		assertPreflight(t, d.preflight(), "name is required")
	})
	t.Run("duplicate name", func(t *testing.T) {
		d := nstatDialog(t, pf)
		editText(t, d.forms[0], "Name", "ST_Orders_EXISTING")
		assertPreflight(t, d.preflight(), "already exists")
	})
	t.Run("no columns", func(t *testing.T) {
		d := nstatDialog(t, pf)
		editText(t, d.forms[0], "Name", "ST_New")
		assertPreflight(t, d.preflight(), "at least one column")
	})
}

// A column can only appear once in a statistics object.
func TestNewStatisticsRefusesADuplicateColumn(t *testing.T) {
	d := nstatDialog(t, nstatTestPrefetch())
	f := d.forms[0]
	chooseSelect(t, f, "Column to add", "Customer")
	clickButton(t, f, "Add")
	clickButton(t, f, "Add")

	if got := d.request().Columns; len(got) != 1 {
		t.Errorf("columns = %v, want the duplicate refused", got)
	}
	if hint := formHint(t, f); hint == "" {
		t.Error("nothing was added and the page says nothing about why")
	}
}
