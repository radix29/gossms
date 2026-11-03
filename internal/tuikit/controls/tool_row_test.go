package controls

import (
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

func newTestToolRow(labels ...string) *ToolRow {
	tr := &ToolRow{}
	for _, l := range labels {
		tr.Cells = append(tr.Cells, ToolCell{Label: l})
	}
	return tr
}

// TestToolRowPlacesEveryCellThatFits: cells get one space of padding either
// side and one gap between, after a one-column margin; no More when nothing is
// hidden, and the returned end is past the last cell plus a gap.
func TestToolRowPlacesEveryCellThatFits(t *testing.T) {
	tr := newTestToolRow("Refresh", "Pause")
	end := tr.Layout(core.Rect{X: 10, Y: 3, W: 40, H: 1}, "")
	if want := (core.Rect{X: 11, Y: 3, W: 9, H: 1}); tr.Cells[0].Rect != want {
		t.Errorf("cell 0 = %+v, want %+v", tr.Cells[0].Rect, want)
	}
	if want := (core.Rect{X: 21, Y: 3, W: 7, H: 1}); tr.Cells[1].Rect != want {
		t.Errorf("cell 1 = %+v, want %+v", tr.Cells[1].Rect, want)
	}
	if end != 29 || len(tr.Hidden) != 0 || !tr.More.Rect.IsZero() {
		t.Errorf("end %d, hidden %v, More %+v; want 29, none, zero", end, tr.Hidden, tr.More.Rect)
	}
}

// TestToolRowCollapsesTheTailIntoMore: what does not fit is the row's tail,
// and More is placed inside the row after the cells that stayed.
func TestToolRowCollapsesTheTailIntoMore(t *testing.T) {
	tr := newTestToolRow("Refresh", "A much longer label", "B", "C")
	r := core.Rect{X: 0, Y: 0, W: 30, H: 1}
	tr.Layout(r, "")
	if !slices.Equal(tr.Hidden, []int{1, 2, 3}) {
		t.Fatalf("hidden = %v, want the tail [1 2 3] — never a later, shorter cell squeezed in", tr.Hidden)
	}
	if tr.More.Label != ToolRowMoreLabel || tr.More.Rect.X != tr.Cells[0].Rect.Right()+ToolRowGap {
		t.Errorf("More = %+v, want it right after Refresh", tr.More)
	}
	if tr.More.Rect.Right() > r.Right() {
		t.Errorf("More runs past the row: %+v", tr.More.Rect)
	}
	for _, i := range tr.Hidden {
		if !tr.Cells[i].Rect.IsZero() {
			t.Errorf("hidden cell %d still has a rect", i)
		}
	}
}

// TestToolRowMoreTakesThePrefixsPlace on a row too narrow for both, and
// PrefixShown says so.
func TestToolRowMoreTakesThePrefixsPlace(t *testing.T) {
	tr := newTestToolRow("1 s", "5 s")
	r := core.Rect{X: 0, Y: 0, W: 12, H: 1}
	tr.Layout(r, "Refresh rate:")
	if tr.More.Rect.X != 1 || tr.PrefixShown(r) {
		t.Errorf("More at %d, prefix shown %v; want More at 1 in the prefix's place", tr.More.Rect.X, tr.PrefixShown(r))
	}
	tr.Layout(core.Rect{W: 60, H: 1}, "Refresh rate:")
	if !tr.PrefixShown(core.Rect{W: 60, H: 1}) {
		t.Error("a row with room for everything hid the prefix")
	}
}

// TestToolRowNoOverflowDropsWithoutMore: the drop-only row hides the same
// tail but puts no More cell up.
func TestToolRowNoOverflowDropsWithoutMore(t *testing.T) {
	tr := newTestToolRow("A: statement 1/2", "B: statement 1/2")
	tr.NoOverflow = true
	tr.Layout(core.Rect{W: 25, H: 1}, "")
	if !slices.Equal(tr.Hidden, []int{1}) || !tr.More.Rect.IsZero() {
		t.Errorf("hidden %v, More %+v; want [1] and no More", tr.Hidden, tr.More.Rect)
	}
}

// TestToolRowWithNoRowHidesEverything: a host with no room for the row passes
// a zero rect; nothing is placed and there is nothing to collapse into.
func TestToolRowWithNoRowHidesEverything(t *testing.T) {
	tr := newTestToolRow("Refresh")
	tr.Layout(core.Rect{W: 40, H: 1}, "")
	tr.Layout(core.Rect{}, "")
	if !tr.Cells[0].Rect.IsZero() || !tr.More.Rect.IsZero() || len(tr.Hidden) != 0 {
		t.Errorf("cell %+v, More %+v, hidden %v; want all empty", tr.Cells[0].Rect, tr.More.Rect, tr.Hidden)
	}
	if tr.CellAt(1, 0) != -1 {
		t.Error("a cell that is not on screen was hit")
	}
}

// TestToolRowOverflowItemsCarryTheCellsGate: with nil predicates the menu
// reads each cell's Disabled and Reason; the note is set only for a cell
// withheld now, and a selected cell keeps its bullet.
func TestToolRowOverflowItemsCarryTheCellsGate(t *testing.T) {
	ran := -1
	tr := &ToolRow{Cells: []ToolCell{
		{Label: "Refresh"},
		{Label: "5 s", Selected: true},
		{Label: "Install", Disabled: true, Reason: "Requires CONTROL SERVER"},
	}, Hidden: []int{1, 2}}
	items := tr.OverflowItems(nil, nil, func(i int) { ran = i })
	if len(items) != 2 || items[0].Label != "• 5 s" || items[1].Label != "Install" {
		t.Fatalf("items = %+v", items)
	}
	if !items[0].enabled() || items[0].Note != "" {
		t.Errorf("an enabled cell's item: enabled %v, note %q", items[0].enabled(), items[0].Note)
	}
	if items[1].enabled() || items[1].Note != "Requires CONTROL SERVER" {
		t.Errorf("a disabled cell's item: enabled %v, note %q", items[1].enabled(), items[1].Note)
	}
	items[0].Action()
	if ran != 1 {
		t.Errorf("run got %d, want the cell's index 1", ran)
	}
}

// TestToolRowRunGatesAndExplains: Run acts only on an allowed cell, reports a
// withheld one as "Label: reason" (the label without its "..."), stays silent without a reason, reads the
// cell's own fields when given nil, and ignores an index outside the row.
func TestToolRowRunGatesAndExplains(t *testing.T) {
	ran := -1
	tr := &ToolRow{Cells: []ToolCell{
		{Label: "Refresh", Action: func() { ran = 0 }},
		{Label: "Recycle...", Disabled: true, Reason: "Requires sysadmin", Action: func() { ran = 1 }},
		{Label: "Pause", Disabled: true, Action: func() { ran = 2 }},
		{Label: "Inert"},
	}}
	var said []string
	status := func(s string) { said = append(said, s) }

	tr.Run(0, nil, nil, status)
	if ran != 0 || len(said) != 0 {
		t.Fatalf("allowed cell: ran %d, said %q", ran, said)
	}
	ran = -1
	tr.Run(1, nil, nil, status)
	if ran != -1 || !slices.Equal(said, []string{"Recycle: Requires sysadmin"}) {
		t.Fatalf("withheld cell: ran %d, said %q", ran, said)
	}
	said = nil
	tr.Run(2, nil, nil, status)
	if ran != -1 || len(said) != 0 {
		t.Fatalf("withheld cell without a reason: ran %d, said %q", ran, said)
	}
	for _, i := range []int{-1, 3, 4} {
		tr.Run(i, nil, nil, status)
	}
	if ran != -1 || len(said) != 0 {
		t.Fatalf("out of range / no action: ran %d, said %q", ran, said)
	}

	// The host's predicates override the fields, both ways.
	tr.Run(0, func(int) bool { return true }, func(int) string { return "busy" }, status)
	tr.Run(1, func(int) bool { return false }, nil, status)
	if ran != 1 || !slices.Equal(said, []string{"Refresh: busy"}) {
		t.Fatalf("host predicates: ran %d, said %q", ran, said)
	}
}

// TestToolRowRunLabelDropsTheMenuAndDialogMarks: "Label: reason" reads the
// label without the marks that say a dialog or menu follows.
func TestToolRowRunLabelDropsTheMenuAndDialogMarks(t *testing.T) {
	for in, want := range map[string]string{
		"Recycle...":        "Recycle",
		"Export…":           "Export",
		"Log: SQL Server ▾": "Log: SQL Server",
		"Refresh":           "Refresh",
		"Install in master": "Install in master",
	} {
		if got := runLabel(in); got != want {
			t.Errorf("runLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
