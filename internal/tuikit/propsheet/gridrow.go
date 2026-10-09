package propsheet

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// GridRow embeds a *controls.DataGrid as a single Form row: the mechanism every
// variable-length collection page uses (files, filegroups, role membership,
// permission grants, ...).
//
// A grid's pending-edit state almost always lives in the page's own edit-state
// map keyed by something richer than a row/col pair (principal name, permission
// name, ...), not in the grid. DirtyFn/RevertFn plug that state into Form's
// Dirty()/Revert(), and ValidateFn into Validate() for a check of the page's
// edits as a whole (two rows given one name, a list left empty) which no single
// cell can make. Form validates only rows that report dirty, so ValidateFn runs
// only while DirtyFn says so.
type GridRow struct {
	Grid *controls.DataGrid

	DirtyFn    func() bool
	RevertFn   func()
	ValidateFn func() error

	fixedHeight int
	drawHeight  int
}

// NewGridRow wraps grid as a Form row occupying a fixed number of screen lines
// (header + separator + data rows + status bar), sized like a standalone
// DataGrid.
func NewGridRow(grid *controls.DataGrid, height int) *GridRow {
	return &GridRow{Grid: grid, fixedHeight: height, drawHeight: height}
}

func (r *GridRow) Height(w int) int { return r.fixedHeight }
func (r *GridRow) Layout(x, y, w int) {
	r.Grid.SetBounds(x, y, w, r.drawHeight)
}

// MinDrawHeight and SetDrawHeight implement Shrinkable: header, separator, one
// data row and the status bar are the least a grid renders usefully in; below
// fixedHeight the grid scrolls its own rows.
func (r *GridRow) MinDrawHeight() int  { return 4 }
func (r *GridRow) SetDrawHeight(h int) { r.drawHeight = h }
func (r *GridRow) Focusable() bool     { return true }

// Browsable implements Browsable: on a read-only form the grid can still be
// focused and moved through, so a page whose detail rows follow the selection
// can be read past its first row.
func (r *GridRow) Browsable() bool { return true }

// SetDrawReadOnly implements ReadOnlyDrawer by putting the grid in browse-only
// mode (controls.DataGrid.SetBrowseOnly), the half of read-only that keeps a
// focusable grid from editing. The gate is the grid's, not a key filter here: it
// knows which of its keys and clicks activate a cell.
func (r *GridRow) SetDrawReadOnly(v bool) { r.Grid.SetBrowseOnly(v) }
func (r *GridRow) Draw(s tcell.Screen, focused bool) {
	r.Grid.Focus(focused)
	r.Grid.Draw(s)
}

// HandleKey forwards to the grid, but reports an arrow key the grid could not
// act on as unhandled, so Form's Up/Down focus movement and the sheet's
// Left-to-the-page-list still work from a focused grid.
//
// DataGrid answers true to every arrow key whether or not it moved: fine
// standalone, but a keyboard trap here, since Form falls back to navigation only
// on false (Down at the last row, Up at the first, Left at column 0 left the user
// stuck, an empty grid swallowed all of them).
//
// Movement is detected rather than predicted, which stays correct as the grid's
// key handling changes. The scroll column counts: a grid with no cell cursor
// scrolls horizontally without ever changing SelectedCell.
func (r *GridRow) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
	default:
		return r.Grid.HandleKey(ev)
	}
	row, col := r.Grid.SelectedCell()
	scroll := r.Grid.ScrollCol()
	if !r.Grid.HandleKey(ev) {
		return false
	}
	newRow, newCol := r.Grid.SelectedCell()
	return newRow != row || newCol != col || r.Grid.ScrollCol() != scroll
}
func (r *GridRow) HandleMouse(ev *tcell.EventMouse) bool { return r.Grid.HandleMouse(ev) }

// DrawOverlay implements propsheet.OverlayDrawer: the grid's full-cell-content
// popup (controls.DataGrid.DrawOverlay) must draw after every row and the sheet's
// button row, so Form defers it here instead of GridRow.Draw drawing it inline.
func (r *GridRow) DrawOverlay(s tcell.Screen) { r.Grid.DrawOverlay(s) }

// OverlayActive implements propsheet.OverlayActiver, so Form/PropertySheet give
// the grid's "Show Value" popup first refusal ahead of position-based click
// routing (controls.DataGrid.OverlayActive).
func (r *GridRow) OverlayActive() bool { return r.Grid.OverlayActive() }

// CopyText returns the selected cell (cell-cursor mode) or the whole selected
// row, tab-joined (row-selection mode).
func (r *GridRow) CopyText() string {
	row, col := r.Grid.SelectedCell()
	cells := r.Grid.Row(row)
	if cells == nil {
		return ""
	}
	if r.Grid.CellCursorEnabled() {
		if col >= 0 && col < len(cells) {
			return cells[col]
		}
		return ""
	}
	return strings.Join(cells, "\t")
}

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it
// implement ClipboardRow by forwarding to the grid, itself a real clipboard
// target only while its "Show Value" viewer is open (controls.DataGrid.
// HasSelection); so Ctrl+C/X/V and Select All reach that popup's read-only text
// instead of CopyText's plain value whenever it shows.
func (r *GridRow) HasSelection() bool   { return r.Grid.HasSelection() }
func (r *GridRow) SelectedText() string { return r.Grid.SelectedText() }
func (r *GridRow) Cut() string          { return r.Grid.Cut() }
func (r *GridRow) Paste(text string)    { r.Grid.Paste(text) }
func (r *GridRow) SelectAll()           { r.Grid.SelectAll() }

func (r *GridRow) Dirty() bool {
	return r.DirtyFn != nil && r.DirtyFn()
}
func (r *GridRow) Revert() {
	if r.RevertFn != nil {
		r.RevertFn()
	}
}
func (r *GridRow) Validate() error {
	if r.ValidateFn == nil {
		return nil
	}
	return r.ValidateFn()
}
