package controls

import (
	"strings"

	"github.com/gdamore/tcell/v3"
)

// SelectedCellsText returns the selection as tab-separated columns and
// newline-separated rows: what the menu's "Copy" hands to OnCopyRequest.
// Exported for a host wiring the grid's selection to its own Ctrl+C: the
// keyboard equivalent of the menu's "Copy", which the grid can't do itself
// (see OnCopyRequest).
func (g *DataGrid) SelectedCellsText() string {
	_, c0, _, c1 := g.SelectionBounds()
	rows := g.SelectedRows()
	// A Ctrl+click selection is whole rows: no column range, so every column.
	if g.marking {
		c0, c1 = 0, max(0, len(g.columns)-1)
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		// Row, not rows.Row: a marked row can outlive a source that shrank under it.
		cells := g.Row(r)
		for c := c0; c <= c1; c++ {
			if c > c0 {
				b.WriteByte('\t')
			}
			if c < len(cells) {
				b.WriteString(cells[c])
			}
		}
	}
	return b.String()
}

// allRowsText returns every row, tab- and newline-separated, optionally prefixed
// with a header row of column names: what the row-number gutter's header-cell
// menu's "Copy All"/"Copy All with Headers" hand to OnCopyRequest.
func (g *DataGrid) allRowsText(withHeaders bool) string {
	var b strings.Builder
	if withHeaders {
		b.WriteString(strings.Join(g.columns, "\t"))
		if g.rows.Len() > 0 {
			b.WriteByte('\n')
		}
	}
	for r := range g.rows.Len() {
		if r > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Join(g.rows.Row(r), "\t"))
	}
	return b.String()
}

// requestCopy hands text to OnCopyRequest, if set (see that field for why
// DataGrid can't write to the OS clipboard).
func (g *DataGrid) requestCopy(text string) {
	if g.OnCopyRequest != nil {
		g.OnCopyRequest(text)
	}
}

// cellContextMenuItems builds the right-click (or Ctrl+Space) menu for a
// selected cell/block: "Copy" only when OnCopyRequest is wired, "Show Value" only
// for a single cell (a block or multi-row Ctrl+click selection has no one cell's
// content to show), then the host's OnMenuItems entries unless the grid is
// browse-only (see SetBrowseOnly).
func (g *DataGrid) cellContextMenuItems() []MenuItem {
	var items []MenuItem
	if g.OnCopyRequest != nil {
		items = append(items, MenuItem{Label: Strings.Copy, Action: func() {
			// The menu can outlive its rows (a refresh that empties the grid while it is
			// open); copying then would blank the clipboard.
			if g.SelectedRowCount() > 0 {
				g.requestCopy(g.SelectedCellsText())
			}
		}})
	}
	if !g.blockSelecting && g.SelectedRowCount() <= 1 {
		items = append(items, MenuItem{Label: Strings.ShowValue, Action: g.openViewer})
	}
	if g.OnMenuItems != nil && !g.browseOnly {
		if host := g.OnMenuItems(); len(host) > 0 {
			if len(items) > 0 {
				items = append(items, MenuItem{Divider: true})
			}
			items = append(items, host...)
		}
	}
	return items
}

// gridContextMenuItems builds the row-number header cell's menu: Copy All and
// Copy All with Headers when OnCopyRequest is wired, then the host's
// OnGridMenuItems entries unless the grid is browse-only.
func (g *DataGrid) gridContextMenuItems() []MenuItem {
	var items []MenuItem
	if g.OnCopyRequest != nil {
		items = append(items,
			MenuItem{Label: Strings.CopyAll, Action: func() { g.requestCopy(g.allRowsText(false)) }},
			MenuItem{Label: Strings.CopyAllWithHeaders, Action: func() { g.requestCopy(g.allRowsText(true)) }})
	}
	if g.OnGridMenuItems != nil && !g.browseOnly {
		if host := g.OnGridMenuItems(); len(host) > 0 {
			if len(items) > 0 {
				items = append(items, MenuItem{Divider: true})
			}
			items = append(items, host...)
		}
	}
	return items
}

// ShowValue does what the context menu's "Show Value" does for the selected
// cell, for a host binding it to another gesture (an OnRowDoubleClick, say). It
// ends the mouse press first: called from inside a press, the release then goes
// to the popup or the host's tab instead, and the press latches would survive
// to make the grid's next click a drag continuation that pairs with nothing.
func (g *DataGrid) ShowValue() {
	g.mouseDragging = false
	g.rowPressed = false
	g.openViewer()
}

// openViewer shows the selected cell's text in the grid's ValueViewer, titled
// with its column name. A host claiming the value via OnShowValue displays it
// instead and the popup doesn't open.
func (g *DataGrid) openViewer() {
	// Row, not rows.Row: nil past the end, as on an empty grid or after a refresh
	// shrank the source under an open menu.
	cells := g.Row(g.selRow)
	if g.selCol < 0 || g.selCol >= len(cells) {
		return
	}
	header := ""
	if g.selCol < len(g.columns) {
		header = g.columns[g.selCol]
	}
	// A NULL has no value for a host to open; the popup shows its "NULL".
	if g.OnShowValue != nil && !g.IsNull(g.selRow, g.selCol) &&
		g.OnShowValue(g.selCol, header, cells[g.selCol]) {
		return
	}
	g.viewer.Show(header, cells[g.selCol])
}

// DrawOverlay renders the "Show Value" popup and the right-click context menu,
// if open. Call after every other widget in the frame has drawn; see Draw.
func (g *DataGrid) DrawOverlay(s tcell.Screen) {
	g.viewer.DrawOverlay(s)
	g.ctxMenu.DrawOverlay(s)
}

// ---------------------------------------------------------------------------
// Clipboard target — active only while the content viewer is open
// ---------------------------------------------------------------------------

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it make
// *DataGrid a core.ClipboardTarget (and a propsheet.ClipboardRow's grid),
// forwarding to the "Show Value" popup (ValueViewer) while it is open.
// HasSelection is false otherwise, so a host falling back to its own row/cell
// copy (e.g. propsheet.GridRow.CopyText) keeps doing so whenever the viewer
// isn't showing.
func (g *DataGrid) HasSelection() bool { return g.viewer.HasSelection() }

func (g *DataGrid) SelectedText() string { return g.viewer.SelectedText() }

// Cut returns "" and removes nothing: the grid and its viewer are read-only.
// "" tells the host nothing was cut, so it neither overwrites the clipboard nor
// reports a cut (Copy is the way to take the text).
func (g *DataGrid) Cut() string { return "" }

// Paste is a no-op: the viewer is read-only.
func (g *DataGrid) Paste(text string) {}

func (g *DataGrid) SelectAll() { g.viewer.SelectAll() }

// OverlayActive reports whether the context menu or the full-content popup is
// showing. A host laying the grid out beside another focusable widget (e.g. an
// Editor above a results grid) must give the grid exclusive first refusal of every
// key and mouse event while true: both overlays are positioned independently of
// the grid's rect (see DrawOverlay), so position- or focus-based routing would
// hand their input to whatever widget sits under those coordinates.
func (g *DataGrid) OverlayActive() bool {
	return g.viewer.OverlayActive() || g.ctxMenu.OverlayActive()
}
