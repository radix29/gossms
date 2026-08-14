package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The New AG dialog's Databases grid, wired and driven exactly as
// buildGeneralPage wires it but through a real propsheet.Form — the layer
// ag_props_grid_editor_test.go doesn't reach. It is the Form that turns a grid
// which fails to move into a grid you cannot stay in: GridRow reports "not
// handled" and Form moves focus on, so before the redraw preserved the cell,
// only the first database could ever be included in a new group.
func TestNewAGDatabaseGridMovesThroughTheForm(t *testing.T) {
	names := []string{"backup_test", "t4_grid_a", "t4_grid_b"}
	included := make([]bool, len(names))

	headers := []string{"Database name", "In the group"}
	rowsFor := func() [][]string {
		out := make([][]string, len(names))
		for i, n := range names {
			out[i] = []string{n, boolStr(included[i])}
		}
		return out
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	includeRow := propsheet.Check("Include the selected database", false)
	current := -1
	commit := func() {
		if current >= 0 && current < len(names) {
			included[current] = includeRow.Checked()
		}
	}
	sync := func() {
		current = grid.SelectedRow()
		if current >= 0 && current < len(names) {
			includeRow.SetChecked(included[current])
		}
	}
	reload := wireGridEditor(grid, headers, rowsFor, commit, sync)

	gridRow := propsheet.NewGridRow(grid, 7)
	gridRow.DirtyFn = func() bool {
		commit()
		reload()
		for _, v := range included {
			if v {
				return true
			}
		}
		return false
	}

	f := propsheet.NewForm(propsheet.Section("Availability databases"), gridRow, includeRow)
	f.SetBounds(0, 0, 100, 20)
	f.Focus(true)

	if got := f.Focused(); got != propsheet.Row(gridRow) {
		t.Fatalf("form focused %T, want the grid row", got)
	}
	for want := 1; want <= 2; want++ {
		if !f.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) {
			t.Fatalf("form refused Down #%d", want)
		}
		if got := grid.SelectedRow(); got != want {
			t.Fatalf("after Down #%d grid is on row %d, want %d (focused row now %T)", want, got, want, f.Focused())
		}
	}
}
