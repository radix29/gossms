package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// wireCellToggle and newCellToggleGrid replaced the OnActivateCell block nine
// Properties pages hand-rolled. These pin the two things that block was for:
// the guard that stops an activation outside the editable column or past the
// last row from writing to an edit, and the redraw that leaves the grid where
// the user is standing.

// newToggleEdits is the page's own state behind the grid: one bool per row.
func newToggleEdits(n int) []bool { return make([]bool, n) }

// TestCellToggleGuardsColumnAndRow: only column 0 toggles, and a row outside
// the slice is ignored. Without the guard a click on the blank space past the
// last row wrote to the final edit.
func TestCellToggleGuardsColumnAndRow(t *testing.T) {
	on := newToggleEdits(3)
	headers := []string{"Linked", "Name"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(on))
		for i, v := range on {
			rows[i] = []string{mapCell(v), string(rune('a' + i))}
		}
		return rows
	}
	grid := newCellToggleGrid(headers, 0, func() int { return len(on) },
		func(row int) { on[row] = !on[row] }, rowsFor)

	grid.OnActivateCell(1, 1) // not the editable column
	grid.OnActivateCell(-1, 0)
	grid.OnActivateCell(3, 0) // past the last row
	for i, v := range on {
		if v {
			t.Errorf("edit %d was toggled by a guarded activation", i)
		}
	}

	grid.OnActivateCell(1, 0)
	if !on[1] || on[0] || on[2] {
		t.Errorf("after toggling row 1, on = %v, want only row 1 set", on)
	}
	if got := grid.Row(1)[0]; got != mapCell(true) {
		t.Errorf("row 1 cell = %q, want %q — the grid did not re-render", got, mapCell(true))
	}
}

// TestCellToggleKeepsCursorAndScroll: toggling a cell half way down a long grid
// must not move the grid. SetData would reset the cursor to 0,0 and the scroll
// to the top, which is the bug redrawGrid exists for — and which, from inside a
// grid callback, also makes every row but the first unreachable.
func TestCellToggleKeepsCursorAndScroll(t *testing.T) {
	on := newToggleEdits(40)
	headers := []string{"Linked", "Name"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(on))
		for i, v := range on {
			rows[i] = []string{mapCell(v), string(rune('a' + i%26))}
		}
		return rows
	}
	grid := newCellToggleGrid(headers, 0, func() int { return len(on) },
		func(row int) { on[row] = !on[row] }, rowsFor)
	grid.SetBounds(0, 0, 40, 10)

	for i := 0; i < 20; i++ {
		grid.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	wantRow, wantCol := grid.SelectedCell()
	wantScroll := grid.ScrollRow()
	if wantRow == 0 || wantScroll == 0 {
		t.Fatalf("setup: cursor at row %d, scroll %d — the grid never moved, so this proves nothing", wantRow, wantScroll)
	}

	if !grid.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) {
		t.Fatal("the grid refused Enter on a cell-cursor grid")
	}
	if !on[wantRow] {
		t.Fatalf("Enter did not toggle row %d", wantRow)
	}
	if row, col := grid.SelectedCell(); row != wantRow || col != wantCol {
		t.Errorf("cursor moved to %d,%d after a toggle, want %d,%d", row, col, wantRow, wantCol)
	}
	if got := grid.ScrollRow(); got != wantScroll {
		t.Errorf("scroll moved to %d after a toggle, want %d", got, wantScroll)
	}
}
