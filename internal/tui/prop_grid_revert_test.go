package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// A propsheet page's RevertFn redraws its grid after putting the edits back.
// Two things go wrong if it reaches for DataGrid.SetData directly, and both
// shipped on the two user-mapping pages (login_props.go's pageLoginUserMapping
// and new_login_pages.go's buildNewLoginUserMappingPage):
//
//   - SetData resets the cell cursor to 0,0, so the grid highlights the first
//     row while the detail widgets below it still describe the row the user was
//     on. redrawGrid is the fix.
//   - The resync that follows runs commitCurrent first, and the detail widgets
//     still hold the *pre-revert* values — so the selected row is written
//     straight back to what Revert just undid. wireGridEditor's reload, which
//     redraws and reloads without committing, is the fix.
//
// A RevertFn that changes the row set (membership_page, extended_properties,
// the Agent job step/schedule/alert pages) resets the cursor on purpose and is
// not what these pin.

// mappingRevertHarness reproduces the user-mapping idiom: a grid of rows and a
// single-field detail editor below it, wired through the real wireGridEditor
// and reverted through the real reload, exactly as both pages now do.
type mappingRevertHarness struct {
	grid *controls.DataGrid

	schema     []string // the per-row value Revert restores
	origSchema []string

	field    string // the detail editor's schema box
	selected int

	revert func()
}

func newMappingRevertHarness() *mappingRevertHarness {
	h := &mappingRevertHarness{
		schema:     []string{"dbo", "dbo", "dbo"},
		origSchema: []string{"dbo", "dbo", "dbo"},
		selected:   -1,
	}
	h.grid = controls.NewDataGrid()
	h.grid.SetCellCursor(true)

	headers := []string{"Database", "Schema"}
	rowsFor := func() [][]string {
		out := make([][]string, len(h.schema))
		for i, s := range h.schema {
			out[i] = []string{string(rune('A' + i)), s}
		}
		return out
	}
	h.grid.SetData(headers, rowsFor())

	commitCurrent := func() {
		if h.selected >= 0 && h.selected < len(h.schema) {
			h.schema[h.selected] = h.field
		}
	}
	syncFromSelection := func() {
		h.selected = h.grid.SelectedRow()
		if h.selected < 0 || h.selected >= len(h.schema) {
			h.field = ""
			return
		}
		h.field = h.schema[h.selected]
	}
	reload := wireGridEditor(h.grid, headers, rowsFor, commitCurrent, syncFromSelection)

	h.revert = func() {
		copy(h.schema, h.origSchema)
		reload()
	}
	return h
}

func (h *mappingRevertHarness) down(t *testing.T) {
	t.Helper()
	if !h.grid.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) {
		t.Fatal("DataGrid refused Down")
	}
}

func TestMappingRevertKeepsTheCursorWhereTheUserLeftIt(t *testing.T) {
	h := newMappingRevertHarness()
	h.down(t)
	h.down(t)
	if h.selected != 2 {
		t.Fatalf("setup: editor is on row %d, want 2", h.selected)
	}

	h.field = "sales" // what the user typed into the schema box
	h.revert()

	if got := h.grid.SelectedRow(); got != 2 {
		t.Fatalf("Revert left the grid on row %d, want 2 — SetData reset the cursor", got)
	}
	if h.selected != 2 {
		t.Fatalf("Revert left the editor describing row %d, want 2", h.selected)
	}
}

func TestMappingRevertDiscardsTheSelectedRowsUncommittedEdit(t *testing.T) {
	h := newMappingRevertHarness()
	h.down(t) // row 1 is selected, and is the one being edited

	h.field = "sales" // typed but never committed — no row change since
	h.revert()

	if h.schema[1] != "dbo" {
		t.Fatalf("row 1 reads %q after Revert, want %q — commitCurrent wrote the "+
			"pre-revert editor value back over it", h.schema[1], "dbo")
	}
	if h.field != "dbo" {
		t.Fatalf("the schema box shows %q after Revert, want %q", h.field, "dbo")
	}
	if got := h.grid.Row(1)[1]; got != "dbo" {
		t.Fatalf("grid cell (1,1) reads %q after Revert, want %q", got, "dbo")
	}
}

func TestMappingRevertStillCommitsAnEditWhenTheUserMovesRows(t *testing.T) {
	// The counterpart to the test above: reload must only suppress the commit
	// Revert itself triggers, never the one an ordinary row change depends on
	// — that commit is what carries a typed edit into the row being left.
	h := newMappingRevertHarness()
	h.field = "sales"
	h.down(t)

	if h.schema[0] != "sales" {
		t.Fatalf("row 0 reads %q after moving off it, want the edit committed", h.schema[0])
	}
	if h.field != "dbo" {
		t.Fatalf("the schema box shows %q on row 1, want %q", h.field, "dbo")
	}
}
