package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newTestToggleGrid mirrors pageServerProcessors' shape: a label column
// (not toggleable) followed by two toggle columns.
func newTestToggleGrid() *ToggleGridRow {
	g := NewToggleGrid([]string{"CPU", "Affinity", "I/O Affinity"}, []int{1, 2}, 10)
	g.Layout(0, 0, 60)
	g.SetRows(
		[][]string{{"Processor 0"}, {"Processor 1"}},
		[][]bool{{false, false}, {true, false}},
	)
	return g
}

func TestToggleGridSetRowsRendersCheckboxCells(t *testing.T) {
	g := newTestToggleGrid()
	if got := g.Grid.Row(0); got[0] != "Processor 0" || got[1] != "[ ]" || got[2] != "[ ]" {
		t.Fatalf("row 0 = %v, want [Processor 0 [ ] [ ]]", got)
	}
	if got := g.Grid.Row(1); got[1] != "[x]" || got[2] != "[ ]" {
		t.Fatalf("row 1 toggle cells = %v, want [[x] [ ]]", got)
	}
}

func TestToggleGridSpaceTogglesFocusedCell(t *testing.T) {
	g := newTestToggleGrid()
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // cell cursor -> col 1 ("Affinity")
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))

	if !g.Values()[0][0] {
		t.Fatal("Space on row 0, col 1 (toggleCols[0]) should have set Values()[0][0] = true")
	}
	if got := g.Grid.Row(0)[1]; got != "[x]" {
		t.Fatalf("rendered cell after toggle = %q, want [x]", got)
	}
}

func TestToggleGridEnterTogglesFocusedCell(t *testing.T) {
	g := newTestToggleGrid()
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) // col 0 is not a toggle column
	if g.Values()[0][0] || g.Values()[0][1] {
		t.Fatal("activating a non-toggle column should not change Values()")
	}

	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // -> col 2 ("I/O Affinity")
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if !g.Values()[0][1] {
		t.Fatal("Enter on row 0, col 2 (toggleCols[1]) should have set Values()[0][1] = true")
	}
}

func TestToggleGridOnToggleFiresWithToggleColsRelativeIndex(t *testing.T) {
	g := newTestToggleGrid()
	var gotRow, gotCol int
	var gotOn bool
	fired := false
	g.OnToggle = func(row, col int, on bool) { fired, gotRow, gotCol, gotOn = true, row, col, on }

	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // -> col 2 ("I/O Affinity")
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))

	if !fired {
		t.Fatal("OnToggle was not called")
	}
	if gotRow != 0 || gotCol != 1 || !gotOn {
		t.Fatalf("OnToggle(row=%d, col=%d, on=%v), want (0, 1, true)", gotRow, gotCol, gotOn)
	}
}

func TestToggleGridDirtyAndRevert(t *testing.T) {
	g := newTestToggleGrid()
	if g.Dirty() {
		t.Fatal("Dirty() = true immediately after SetRows, want false")
	}

	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	if !g.Dirty() {
		t.Fatal("Dirty() = false after toggling a cell, want true")
	}

	g.Revert()
	if g.Dirty() {
		t.Fatal("Dirty() = true after Revert, want false")
	}
	if g.Values()[0][0] {
		t.Fatal("Revert() should have restored Values()[0][0] to its baseline (false)")
	}
	if got := g.Grid.Row(0)[1]; got != "[ ]" {
		t.Fatalf("rendered cell after Revert = %q, want [ ] (re-rendered from baseline)", got)
	}
}

func TestToggleGridSetRowsResetsBaseline(t *testing.T) {
	g := newTestToggleGrid()
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	if !g.Dirty() {
		t.Fatal("setup: expected Dirty() = true after a toggle")
	}

	g.SetRows([][]string{{"Processor 0"}}, [][]bool{{true, false}})
	if g.Dirty() {
		t.Fatal("SetRows should reset the dirty baseline to the newly supplied values")
	}
}

func TestToggleGridSingleToggleColumnAtIndexZero(t *testing.T) {
	// Mirrors pageLoginServerRoles' shape: toggle column first, label second.
	g := NewToggleGrid([]string{"Member", "Role"}, []int{0}, 10)
	g.Layout(0, 0, 60)
	g.SetRows([][]string{{"db_owner"}, {"sysadmin"}}, [][]bool{{false}, {true}})

	if got := g.Grid.Row(0); got[0] != "[ ]" || got[1] != "db_owner" {
		t.Fatalf("row 0 = %v, want [[ ] db_owner]", got)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	if !g.Values()[0][0] {
		t.Fatal("Space on the default cell cursor (row 0, col 0) should toggle Values()[0][0]")
	}
}
