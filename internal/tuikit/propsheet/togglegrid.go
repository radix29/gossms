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

	// drawReadOnly renders the toggle cells as ticks and crosses rather than
	// as checkboxes — see SetDrawReadOnly.
	drawReadOnly bool
}

// SetDrawReadOnly implements ReadOnlyDrawer: the toggle columns render their
// state the way a read-only CheckRow does, so the one thing left on a gated
// page that still looked like a control stops doing so. The cells are already
// inert — Form routes no press into a read-only row.
//
// render, not renderPreservingView: Form calls this from SetReadOnly, which a
// page runs before the sheet has ever laid the grid out, and preserving the
// view of a grid with no rect ends in SetSelectedCell's ensureVisible
// scrolling past every row — the affinity grid drew four blank lines under its
// header, live.
func (t *ToggleGridRow) SetDrawReadOnly(v bool) {
	if t.drawReadOnly == v {
		return
	}
	t.drawReadOnly = v
	t.render()
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

// renderRows builds the grid's display rows from text and values,
// interleaving them back into columns order.
func (t *ToggleGridRow) renderRows() [][]string {
	rows := make([][]string, len(t.text))
	for i := range rows {
		row := make([]string, len(t.columns))
		ti := 0
		for c := range t.columns {
			if j := slices.Index(t.toggleCols, c); j >= 0 {
				row[c] = t.toggleCell(t.values[i][j])
			} else {
				row[c] = t.text[i][ti]
				ti++
			}
		}
		rows[i] = row
	}
	return rows
}

// render replaces the grid's rows outright, taking SetData's reset of the cell
// cursor, the scroll and any dragged column width. For SetRows, whose rows are
// a different set; a change that leaves the row set alone uses
// renderPreservingView instead.
func (t *ToggleGridRow) render() {
	t.Grid.SetData(t.columns, t.renderRows())
}

// renderPreservingView re-renders the same rows without moving the grid under
// the user — see controls.DataGrid.SetDataPreservingView, and redrawGrid in
// the application layer, which is the same fix for the same reason on the
// Properties pages that hand-rolled it.
func (t *ToggleGridRow) renderPreservingView() {
	t.Grid.SetDataPreservingView(t.columns, t.renderRows())
}

// Text returns the non-toggle cell text, row-parallel with Values.
//
// The pairing is the point: a page reads Values()[i] against its own i'th
// object, so anything that needs to know *which* row a value belongs to has
// to be able to read the row's own text. Without it the two are only
// relatable by an index nobody outside the page can check.
func (t *ToggleGridRow) Text() [][]string { return t.text }

// Toggle flips one toggle cell the way clicking or pressing Space on it does,
// including the redraw and the OnToggle callback. row is a row index; col
// indexes toggleCols, not the raw grid column — the same convention OnToggle
// reports in.
func (t *ToggleGridRow) Toggle(row, col int) {
	if col < 0 || col >= len(t.toggleCols) {
		return
	}
	t.activateCell(row, t.toggleCols[col])
}

func (t *ToggleGridRow) activateCell(row, col int) {
	j := slices.Index(t.toggleCols, col)
	if j < 0 || row < 0 || row >= len(t.values) {
		return
	}
	t.values[row][j] = !t.values[row][j]
	t.renderPreservingView()
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
	// Preserving, not resetting: Ctrl+Z restores the values of the rows
	// already on screen, so the row the user is on is still the row they
	// meant — the row set has not changed, only what it says.
	t.renderPreservingView()
}

func (t *ToggleGridRow) Validate() error { return nil }

func cloneBoolMatrix(m [][]bool) [][]bool {
	out := make([][]bool, len(m))
	for i, row := range m {
		out[i] = slices.Clone(row)
	}
	return out
}

// toggleCell renders a toggle column's boolean value as SSMS-style checkbox
// text, or as a tick/cross when the row draws read-only.
func (t *ToggleGridRow) toggleCell(v bool) string {
	if t.drawReadOnly {
		if v {
			return "✓"
		}
		return "✗"
	}
	if v {
		return "[x]"
	}
	return "[ ]"
}
