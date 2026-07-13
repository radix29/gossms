package propsheet

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// GridRow embeds a *controls.DataGrid as a single Form row — the mechanism
// every variable-length collection page uses (files, filegroups, role
// membership, permission grants, …).
//
// A grid's pending-edit state (which cells the user has toggled, pending
// add/remove) almost always lives in the app page's own edit-state map
// keyed by something richer than a row/col pair (principal name,
// permission name, …), not inside the grid. DirtyFn/RevertFn let the page
// plug that state into Form's Dirty()/Revert() without GridRow needing to
// know its shape.
type GridRow struct {
	Grid *controls.DataGrid

	DirtyFn  func() bool
	RevertFn func()

	fixedHeight int
}

// NewGridRow wraps grid as a Form row occupying a fixed number of screen
// lines (header + separator + data rows + status bar — size it the same
// way you'd size any standalone DataGrid).
func NewGridRow(grid *controls.DataGrid, height int) *GridRow {
	return &GridRow{Grid: grid, fixedHeight: height}
}

func (r *GridRow) Height(w int) int { return r.fixedHeight }
func (r *GridRow) Layout(x, y, w int) {
	r.Grid.SetBounds(x, y, w, r.fixedHeight)
}
func (r *GridRow) Focusable() bool { return true }
func (r *GridRow) Draw(s tcell.Screen, focused bool) {
	r.Grid.Focus(focused)
	r.Grid.Draw(s)
}
func (r *GridRow) HandleKey(ev *tcell.EventKey) bool     { return r.Grid.HandleKey(ev) }
func (r *GridRow) HandleMouse(ev *tcell.EventMouse) bool { return r.Grid.HandleMouse(ev) }

// DrawOverlay implements propsheet.OverlayDrawer: the grid's built-in
// full-cell-content popup (see controls.DataGrid.DrawOverlay) must draw
// after every row and the sheet's own button row, so Form defers it here
// instead of GridRow.Draw drawing it inline.
func (r *GridRow) DrawOverlay(s tcell.Screen) { r.Grid.DrawOverlay(s) }

// CopyText returns the selected cell (cell-cursor mode) or the whole
// selected row, tab-joined (plain row-selection mode).
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

// HasSelection, SelectedText, Cut, Paste, and SelectAll implement
// ClipboardRow by forwarding to the grid, which is itself only a real
// clipboard target while its built-in "Show Value" content viewer is open
// (see controls.DataGrid.HasSelection) — so PropertySheet's Ctrl+C/X/V and
// Select All reach that popup's read-only text instead of CopyText's plain
// cell/row value whenever it's showing, with no other change needed here.
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
func (r *GridRow) Validate() error { return nil }
