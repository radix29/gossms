package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// showTestFilterDialog opens the dialog on a Tables folder (the widest set
// of properties: Name, Schema, Creation Date, Is Memory Optimized).
func showTestFilterDialog(t *testing.T) (*App, *FilterDialog, *explorerNode) {
	t.Helper()
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	a.filterDialog = NewFilterDialog(a)
	node := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, DBName: "HealthClinic", conn: sc}}
	a.filterDialog.show(node, filterProps(node.data.Type), "server-one")
	return a, a.filterDialog, node
}

func TestFilterDialogBuildsCriteriaFromFilledRowsOnly(t *testing.T) {
	_, d, _ := showTestFilterDialog(t)
	d.rows[0].value.SetValue("cust") // Name contains cust
	d.rows[1].op.SetSelected(2)      // Schema equals
	d.rows[1].value.SetValue("Sales")

	f, badRow, err := d.buildFilter()
	if err != nil {
		t.Fatalf("buildFilter: %v (row %d)", err, badRow)
	}
	if len(f.criteria) != 2 {
		t.Fatalf("got %d criteria, want 2 — empty rows must not become criteria", len(f.criteria))
	}
	if f.criteria[0].op != opContains || f.criteria[0].value != "cust" {
		t.Errorf("criteria[0] = %+v, want Name contains cust", f.criteria[0])
	}
	if f.criteria[1].prop.id != fpSchema || f.criteria[1].op != opEquals {
		t.Errorf("criteria[1] = %+v, want Schema equals", f.criteria[1])
	}
}

// An unparseable date or boolean must be refused before it reaches the
// folder: matching treats one as "nothing matches", so a typo would come
// back as an empty folder rather than an error.
func TestFilterDialogRejectsUnparseableValues(t *testing.T) {
	_, d, _ := showTestFilterDialog(t)
	d.rows[2].value.SetValue("March 4th")
	if _, row, err := d.buildFilter(); err == nil {
		t.Error("a bad Creation Date was accepted")
	} else if row != 2 {
		t.Errorf("bad row = %d, want 2", row)
	}

	d.rows[2].value.SetValue("")
	d.rows[3].value.SetValue("sometimes")
	if _, row, err := d.buildFilter(); err == nil {
		t.Error("a bad Is Memory Optimized value was accepted")
	} else if row != 3 {
		t.Errorf("bad row = %d, want 3", row)
	}
}

// OK with every row empty means "no filter", which applyNodeFilter takes as
// a removal — that's how a filter is cleared with Clear Filter + OK.
func TestFilterDialogEmptyRowsProduceNoFilter(t *testing.T) {
	_, d, node := showTestFilterDialog(t)
	node.data.Filter = &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "x"}}}
	d.clearRows()
	f, _, err := d.buildFilter()
	if err != nil {
		t.Fatalf("buildFilter: %v", err)
	}
	if f.active() {
		t.Fatalf("empty rows produced %v, want no filter", f)
	}
	d.applyAndClose()
	if node.data.Filter != nil {
		t.Error("OK with empty rows must remove the folder's filter")
	}
	if d.Visible() {
		t.Error("OK must close the dialog")
	}
}

// Reopening the dialog must offer back the filter actually in force,
// operator included — otherwise OK silently rewrites it to the defaults.
func TestFilterDialogSeedsFromExistingFilter(t *testing.T) {
	_, d, node := showTestFilterDialog(t)
	node.data.Filter = &nodeFilter{criteria: []filterCriterion{
		{prop: schemaProp(), op: opNotEquals, value: "dbo"},
	}}
	d.show(node, filterProps(node.data.Type), "server-one")
	if got := d.rows[1].value.Value(); got != "dbo" {
		t.Errorf("Schema value = %q, want dbo", got)
	}
	if got := d.rows[1].ops[d.rows[1].op.Selected()]; got != opNotEquals {
		t.Errorf("Schema operator = %v, want %v", got, opNotEquals)
	}
}

// Tab must reach every widget and every button and come back round — a
// dialog whose cycle strands focus can only be escaped with the mouse.
func TestFilterDialogTabCyclesEveryStop(t *testing.T) {
	_, d, _ := showTestFilterDialog(t)
	want := len(d.rows)*2 + len(d.buttons())
	if got := d.focusCount(); got != want {
		t.Fatalf("focusCount() = %d, want %d", got, want)
	}
	seen := make(map[int]bool)
	for range want {
		seen[d.focusIdx] = true
		d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	if len(seen) != want {
		t.Errorf("Tab visited %d of %d stops", len(seen), want)
	}
	if d.focusIdx != 0 {
		t.Errorf("focus after a full cycle = %d, want 0", d.focusIdx)
	}
}

// Enter opens the focused operator dropdown rather than pressing OK, and
// Escape closes the list rather than the whole dialog — an open list gets
// first refusal of both.
func TestFilterDialogOpenDropDownTakesKeysFirst(t *testing.T) {
	_, d, _ := showTestFilterDialog(t)
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if d.openDropDown() == nil {
		t.Fatal("Enter on the operator dropdown did not open its list")
	}
	if !d.Visible() {
		t.Fatal("Enter pressed OK instead of opening the list")
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if d.openDropDown() != nil {
		t.Error("Escape did not close the open list")
	}
	if !d.Visible() {
		t.Error("Escape closed the dialog instead of the open list")
	}
}
