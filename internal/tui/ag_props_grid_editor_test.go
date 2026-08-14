package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// wireGridEditor redraws the grid from inside OnSelectRow, and DataGrid.SetData
// resets the selection to 0,0. Before the helper existed, the Backup
// Preferences and Read-Only Routing pages did that redraw without restoring
// the cell, so the cursor snapped back to the first replica on every arrow key
// and no row past the second could be reached. These pin the restore and the
// commit/load ordering it has to keep.

// gridEditorHarness is three replica rows with a one-field editor below them,
// wired the way both AG pages wire theirs.
type gridEditorHarness struct {
	grid   *controls.DataGrid
	values []string // the "edits" behind the grid
	field  string   // the detail editor's single field
	sel    int
	reload func()
}

func newGridEditorHarness() *gridEditorHarness {
	h := &gridEditorHarness{
		grid:   controls.NewDataGrid(),
		values: []string{"a", "b", "c"},
		sel:    -1,
	}
	h.grid.SetCellCursor(true)
	headers := []string{"Replica", "Value"}
	rows := func() [][]string {
		out := make([][]string, len(h.values))
		for i, v := range h.values {
			out[i] = []string{string(rune('A' + i)), v}
		}
		return out
	}
	h.grid.SetData(headers, rows())
	commit := func() {
		if h.sel >= 0 && h.sel < len(h.values) {
			h.values[h.sel] = h.field
		}
	}
	sync := func() {
		h.sel = h.grid.SelectedRow()
		if h.sel >= 0 && h.sel < len(h.values) {
			h.field = h.values[h.sel]
		}
	}
	h.reload = wireGridEditor(h.grid, headers, rows, commit, sync)
	return h
}

func (h *gridEditorHarness) down(t *testing.T) {
	t.Helper()
	if !h.grid.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) {
		t.Fatal("DataGrid refused Down")
	}
}

func TestGridEditorKeepsSelectionAcrossItsOwnRedraw(t *testing.T) {
	h := newGridEditorHarness()
	if h.sel != 0 {
		t.Fatalf("initial sync selected row %d, want 0", h.sel)
	}
	for want := 1; want <= 2; want++ {
		h.down(t)
		if got := h.grid.SelectedRow(); got != want {
			t.Fatalf("after %d Down keypresses the grid selects row %d, want %d — the redraw reset it", want, got, want)
		}
		if h.sel != want {
			t.Fatalf("editor is showing row %d, want %d", h.sel, want)
		}
	}
}

func TestGridEditorCommitsBeforeLoadingTheNewRow(t *testing.T) {
	h := newGridEditorHarness()
	h.field = "edited" // what the user typed while row 0 was selected
	h.down(t)

	if h.values[0] != "edited" {
		t.Fatalf("row 0 kept %q, want the edit committed on the way out", h.values[0])
	}
	if h.field != "b" {
		t.Fatalf("editor shows %q after moving to row 1, want %q", h.field, "b")
	}
	if got := h.grid.Row(0)[1]; got != "edited" {
		t.Fatalf("grid cell (0,1) reads %q, want the committed edit", got)
	}
}

func TestGridEditorReloadRestoresSelection(t *testing.T) {
	h := newGridEditorHarness()
	h.down(t)
	h.values[1] = "reverted" // what a RevertFn does behind the grid
	h.reload()

	if got := h.grid.SelectedRow(); got != 1 {
		t.Fatalf("reload() left the grid on row %d, want 1", got)
	}
	if h.field != "reverted" {
		t.Fatalf("reload() left the editor showing %q, want the reverted value", h.field)
	}
}
