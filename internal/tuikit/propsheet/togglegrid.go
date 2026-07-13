package propsheet

import (
	"slices"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ToggleGridRow is a Form row wrapping a cell-cursor controls.DataGrid
// where some columns are [x]/[ ] boolean toggles (Space/Enter or click
// flips the focused cell) and the rest are plain read-only text — the
// mechanism every variable-length "grid with checkbox columns" page uses
// (processor affinity, change tracking tables, server role membership,
// login/database user mapping). It owns cell rendering, re-render on
// toggle, selected-row preservation across a toggle, and Dirty()/Revert()
// against the baseline captured by the most recent SetRows; the page only
// supplies the domain data and, in its apply closure, diffs Values()
// against what it loaded to decide which gosmo writer calls to make.
type ToggleGridRow struct {
	*GridRow

	columns    []string
	toggleCols []int

	text     [][]string // per row: one entry per non-toggle column, column order
	values   [][]bool   // per row: one entry per toggleCols entry, toggleCols order
	baseline [][]bool

	// OnToggle, if set, is called after a cell is toggled: row is the row
	// index, col is the index into toggleCols (not the raw grid column
	// index), on is the new state.
	OnToggle func(row, col int, on bool)
}

// NewToggleGrid creates a ToggleGridRow. columns are the grid's headers;
// toggleCols lists which column indices render as toggle cells — every
// other column is plain text supplied via SetRows. height is a fixed
// number of screen lines, sized the same way as NewGridRow.
func NewToggleGrid(columns []string, toggleCols []int, height int) *ToggleGridRow {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	t := &ToggleGridRow{
		GridRow:    NewGridRow(grid, height),
		columns:    columns,
		toggleCols: toggleCols,
	}
	grid.OnActivateCell = t.activateCell
	return t
}

// SetRows replaces the grid's rows and captures values as the
// dirty-tracking baseline. text[i] supplies row i's non-toggle columns, in
// column order (skipping toggleCols positions); values[i] supplies row i's
// toggleCols state, in toggleCols order.
func (t *ToggleGridRow) SetRows(text [][]string, values [][]bool) {
	t.text = text
	t.values = cloneBoolMatrix(values)
	t.baseline = cloneBoolMatrix(values)
	t.render()
}

// Values returns the current per-row, per-toggle-column state, indexed the
// same way as SetRows's values parameter.
func (t *ToggleGridRow) Values() [][]bool { return t.values }

// render rebuilds the grid's display rows from text and values, interleaving
// them back into columns order.
func (t *ToggleGridRow) render() {
	rows := make([][]string, len(t.text))
	for i := range rows {
		row := make([]string, len(t.columns))
		ti := 0
		for c := range t.columns {
			if j := slices.Index(t.toggleCols, c); j >= 0 {
				row[c] = toggleCell(t.values[i][j])
			} else {
				row[c] = t.text[i][ti]
				ti++
			}
		}
		rows[i] = row
	}
	t.Grid.SetData(t.columns, rows)
}

func (t *ToggleGridRow) activateCell(row, col int) {
	j := slices.Index(t.toggleCols, col)
	if j < 0 || row < 0 || row >= len(t.values) {
		return
	}
	t.values[row][j] = !t.values[row][j]
	t.render()
	t.Grid.SetSelectedRow(row)
	if t.OnToggle != nil {
		t.OnToggle(row, j, t.values[row][j])
	}
}

// Dirty, Revert, and Validate implement Editable, shadowing GridRow's own
// DirtyFn/RevertFn-based (unset here) implementations — ToggleGridRow
// tracks its own baseline instead of relying on the page to supply one.
func (t *ToggleGridRow) Dirty() bool {
	for i := range t.values {
		if !slices.Equal(t.values[i], t.baseline[i]) {
			return true
		}
	}
	return false
}

func (t *ToggleGridRow) Revert() {
	t.values = cloneBoolMatrix(t.baseline)
	t.render()
}

func (t *ToggleGridRow) Validate() error { return nil }

func cloneBoolMatrix(m [][]bool) [][]bool {
	out := make([][]bool, len(m))
	for i, row := range m {
		out[i] = slices.Clone(row)
	}
	return out
}

// toggleCell renders a toggle column's boolean value as SSMS-style
// checkbox text.
func toggleCell(v bool) string {
	if v {
		return "[x]"
	}
	return "[ ]"
}
