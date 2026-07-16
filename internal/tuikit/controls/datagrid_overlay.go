package controls

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// selectedCellsText returns the current selection's content as tab-
// separated columns and newline-separated rows — what the right-click
// menu's "Copy" item hands to OnCopyRequest.
func (g *DataGrid) selectedCellsText() string {
	r0, c0, r1, c1 := g.selectionBounds()
	var b strings.Builder
	for r := r0; r <= r1; r++ {
		if r > r0 {
			b.WriteByte('\n')
		}
		cells := g.rows.Row(r)
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

// allRowsText returns every row in the grid, tab-separated / newline-
// separated, optionally prefixed with a header row of column names — what
// the row-number gutter's blank header-cell menu's "Copy All"/"Copy All
// with Headers" hand to OnCopyRequest.
func (g *DataGrid) allRowsText(withHeaders bool) string {
	var b strings.Builder
	if withHeaders {
		b.WriteString(strings.Join(g.columns, "\t"))
		if g.rows.Len() > 0 {
			b.WriteByte('\n')
		}
	}
	for r := 0; r < g.rows.Len(); r++ {
		if r > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Join(g.rows.Row(r), "\t"))
	}
	return b.String()
}

// requestCopy hands text to OnCopyRequest, if set — see that field's doc
// comment for why DataGrid can't write to the OS clipboard itself.
func (g *DataGrid) requestCopy(text string) {
	if g.OnCopyRequest != nil {
		g.OnCopyRequest(text)
	}
}

// cellContextMenuItems builds the right-click (or Ctrl+Space) menu for a
// selected cell/block: "Copy" only when OnCopyRequest is wired (so a grid
// that hasn't opted in shows exactly what it always has), plus "Show
// Value" for a single cell — a block selection has no one cell's full
// content to show, so that item is omitted while blockSelecting is true.
func (g *DataGrid) cellContextMenuItems() []MenuItem {
	var items []MenuItem
	if g.OnCopyRequest != nil {
		items = append(items, MenuItem{Label: "Copy", Action: func() { g.requestCopy(g.selectedCellsText()) }})
	}
	if !g.blockSelecting {
		items = append(items, MenuItem{Label: showValueMenuItem, Action: g.openViewer})
	}
	return items
}

// showValueMenuItem is the sole context-menu entry offered on a
// right-clicked cell — see HandleMouse's Button2 case.
const showValueMenuItem = "Show Value"

// openViewer shows the full-content popup for the currently selected
// cell's text in a read-only Editor, so it can be navigated, selected, and
// copied like any other text.
func (g *DataGrid) openViewer() {
	cells := g.rows.Row(g.selRow)
	if g.selCol < 0 || g.selCol >= len(cells) {
		return
	}
	g.viewHeader = ""
	if g.selCol < len(g.columns) {
		g.viewHeader = g.columns[g.selCol]
	}
	if g.viewEditor == nil {
		g.viewEditor = NewEditor(nil)
		g.viewEditor.SetGutterVisible(false)
		g.viewEditor.SetWrapMode(true)
		g.viewEditor.SetReadOnly(true)
	}
	g.viewEditor.SetText(cells[g.selCol])
	g.viewEditor.SetActive(true)
	g.viewOpen = true
}

// DrawOverlay renders the right-click context menu and the full-content
// popup, if either is open. Must be called after every other widget in the
// same frame has drawn, so nothing paints over them — see Draw.
func (g *DataGrid) DrawOverlay(s tcell.Screen) {
	if g.viewOpen {
		sw, sh := s.Size()
		w := core.Min(cellViewerW, core.Max(20, sw-4))
		h := cellViewerLines + 4 // border top/bottom + cellViewerLines text rows + 1 hint row
		x := core.Max(0, (sw-w)/2)
		y := core.Max(0, (sh-h)/2)
		rect := core.Rect{X: x, Y: y, W: w, H: h}

		p := theme.Active()
		core.DimArea(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, p.DialogOverlay, viewerDimNum, viewerDimDen)
		core.FillRect(s, rect, ' ', theme.StyleDialog())
		borderSt := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogBorder)
		titleSt := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTitle).Bold(true)
		core.DrawBoxTitle(s, rect, g.viewHeader, borderSt, titleSt)

		g.viewEditor.SetBounds(x+2, y+1, w-4, cellViewerLines)
		g.viewEditor.Draw(s)

		hintSt := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawTextClipped(s, x+2, y+h-2, w-4, hintSt, "Esc to close — Shift+arrows to select, Ctrl+C to copy")
	}
	g.ctxMenu.Draw(s)
}

// ---------------------------------------------------------------------------
// Clipboard target — active only while the content viewer is open
// ---------------------------------------------------------------------------

// HasSelection, SelectedText, Cut, Paste, and SelectAll make *DataGrid
// itself a clipboard target (see internal/tui/clipboard.go's
// clipboardTarget and propsheet.ClipboardRow), forwarding to the built-in
// viewer's read-only Editor while it's open. HasSelection is always false
// otherwise, so a host that falls back to its own row/cell copy behavior
// (e.g. propsheet.GridRow.CopyText) when there's "no selection" keeps doing
// exactly that whenever the viewer isn't showing.
func (g *DataGrid) HasSelection() bool {
	return g.viewOpen && g.viewEditor.HasSelection()
}

func (g *DataGrid) SelectedText() string {
	if !g.viewOpen {
		return ""
	}
	return g.viewEditor.SelectedText()
}

// Cut degrades to Copy: the viewer is read-only, so there's nothing to
// remove.
func (g *DataGrid) Cut() string { return g.SelectedText() }

// Paste is a no-op: the viewer is read-only.
func (g *DataGrid) Paste(text string) {}

func (g *DataGrid) SelectAll() {
	if g.viewOpen {
		g.viewEditor.SelectAll()
	}
}

// OverlayActive reports whether the right-click context menu or the
// full-content popup is currently showing. A host that lays the grid out
// alongside another focusable widget (e.g. QueryPanel's SQL editor) must
// check this and give the grid exclusive first refusal of every key and
// mouse event while it's true — both overlays are centred/positioned
// independently of the grid's own rect (see DrawOverlay), so ordinary
// position- or focus-based routing would otherwise hand their input to
// whatever widget happens to occupy those screen coordinates underneath.
func (g *DataGrid) OverlayActive() bool {
	return g.viewOpen || g.ctxMenu.Visible()
}
