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

// showTestFilterDialogOnScreen opens the dialog on a Tables folder against a
// terminal of the given size, so the geometry can be checked at widths where
// ModalDialog clamps the dialog narrower than it asked for.
func showTestFilterDialogOnScreen(t *testing.T, w, h int) *FilterDialog {
	t.Helper()
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	a.screen = &fakeSizedScreen{w: w, h: h}
	a.filterDialog = NewFilterDialog(a)
	node := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, DBName: "HealthClinic", conn: sc}}
	a.filterDialog.show(node, filterProps(NodeTables), "server-one")
	a.filterDialog.layout() // Draw's job; the test isn't drawing
	return a.filterDialog
}

// Every row's widgets must stay inside the dialog's borders at any terminal
// width. recentre clamps the dialog to the screen, and the columns were laid
// out from fixed constants sized for the full 74 — so on a narrow window the
// value fields, the widest column, drew straight through the right border.
func TestFilterDialogRowsFitInsideBorders(t *testing.T) {
	for _, w := range []int{120, 80, 74, 66, 55, 44} {
		d := showTestFilterDialogOnScreen(t, w, 30)
		inner := d.InnerRect()
		last := inner.X + inner.W - 1
		if len(d.rows) == 0 {
			t.Fatalf("width %d: dialog has no rows", w)
		}
		for i := range d.rows {
			r := &d.rows[i]
			// The closing bracket is one past the input area (see
			// InputField.Draw), and the value field is the rightmost widget.
			if right := r.value.RectX() + r.value.Width() + 1; right > last {
				t.Errorf("terminal %d: row %d's value field ends at column %d, past the dialog's last inside column %d",
					w, i, right, last)
			}
			if r.op.RectX() < inner.X {
				t.Errorf("terminal %d: row %d's operator starts at column %d, left of the dialog at %d", w, i, r.op.RectX(), inner.X)
			}
		}
	}
}

// Wide enough for the full dialog means the full column widths — the reflow
// must not shrink anything it doesn't have to.
func TestFilterDialogKeepsFullColumnsWhenItFits(t *testing.T) {
	d := showTestFilterDialogOnScreen(t, 120, 30)
	if d.propColW != filterPropColW || d.opW != filterOpW || d.valueW != filterValueW {
		t.Errorf("columns on a wide terminal = %d/%d/%d, want the full %d/%d/%d",
			d.propColW, d.opW, d.valueW, filterPropColW, filterOpW, filterValueW)
	}
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

// A whitespace-only value must count as an empty row, not as a criterion.
// matchText trims the value it compares, so " " arrived as an empty needle and
// `Contains` matched every row: the folder was labelled "(filtered)" and
// nothing was filtered out.
func TestFilterDialogWhitespaceValueIsNotACriterion(t *testing.T) {
	_, d, node := showTestFilterDialog(t)
	d.rows[0].value.SetValue("   ") // Name contains <spaces>

	f, badRow, err := d.buildFilter()
	if err != nil {
		t.Fatalf("buildFilter: %v (row %d)", err, badRow)
	}
	if f.active() {
		t.Fatalf("a whitespace-only value produced %v, want no filter", f.criteria)
	}

	// The same value through the whole path: OK must leave the folder
	// unfiltered rather than installing a filter that keeps every child.
	d.applyAndClose()
	if node.data.Filter != nil {
		t.Errorf("OK with a whitespace-only value installed %v, want no filter", node.data.Filter.criteria)
	}
}

// A value with real text keeps it, but loses the padding — so the criterion
// the folder holds is the one the summary line and a reopened dialog show.
func TestFilterDialogTrimsCriterionValues(t *testing.T) {
	_, d, _ := showTestFilterDialog(t)
	d.rows[0].value.SetValue("  cust  ")
	f, badRow, err := d.buildFilter()
	if err != nil {
		t.Fatalf("buildFilter: %v (row %d)", err, badRow)
	}
	if len(f.criteria) != 1 || f.criteria[0].value != "cust" {
		t.Errorf("criteria = %+v, want one Name criterion with value %q", f.criteria, "cust")
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
