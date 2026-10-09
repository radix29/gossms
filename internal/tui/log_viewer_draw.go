package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// log_viewer_draw.go renders the Log File Viewer panel: the toolbar row, the
// entry grid, the splitter, and the details pane.

// Draw renders the panel (Panel interface).
func (lv *LogViewer) Draw(s tcell.Screen) {
	// Both selectors are labelled with what they point at, so labels change
	// without a resize, and a rect laid out for the old label would let the next
	// button overpaint its tail. Relaying out here is a few width measurements.
	lv.refreshToolLabels()
	lv.layoutTools()
	lv.drawToolbar(s)
	lv.grid.Draw(s)
	lv.splitter.Draw(s)
	lv.drawDetails(s)
	// Last, over everything else: the grid's cell context menu and "Show Value"
	// popup are drawn outside the grid's own rect, and HandleKey/HandleMouse give
	// OverlayActive() first refusal, so skipping this leaves an invisible menu
	// eating every key until Escape.
	lv.grid.DrawOverlay(s)
}

// drawToolbar paints the toolbar row (selectors and buttons in Activity
// Monitor's tooltip scheme), then the filter field.
func (lv *LogViewer) drawToolbar(s tcell.Screen) {
	if lv.toolRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, lv.toolRect, ' ', barStyle)

	for i, t := range lv.tools.Cells {
		if t.Rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if lv.toolDisabled(i) {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, t.Rect, ' ', style)
		core.DrawText(s, t.Rect.X+1, t.Rect.Y, style, t.Label)
	}
	// The stand-in for whatever did not fit. Dimmed only while the whole row is;
	// its items are gated one by one once the menu is open.
	if !lv.tools.More.Rect.IsZero() {
		style := theme.StyleTooltip()
		if !lv.toolsEnabled() {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, lv.tools.More.Rect, ' ', style)
		core.DrawText(s, lv.tools.More.Rect.X+1, lv.tools.More.Rect.Y, style, lv.tools.More.Label)
	}
	if lv.filterVisible() {
		lv.filter.Draw(s)
	}
}

// drawDetails paints the selected entry in full below the splitter: date, log
// file and source one per line, then the message wrapped over the rest. The
// message is drawn as the log wrote it; the grid row is the flattened form.
func (lv *LogViewer) drawDetails(s tcell.Screen) {
	r := lv.detailRect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	style := theme.StyleDefault()
	dimStyle := style.Foreground(pal.TextDim)
	core.FillRect(s, r, ' ', style)

	row, ok := lv.selectedLogRow()
	if !ok {
		core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, dimStyle, "No entry selected")
		return
	}

	lines := lv.detailLines(row, r.W-2)
	for i := lv.detailScroll; i < len(lines); i++ {
		y := r.Y + i - lv.detailScroll
		if y >= r.Y+r.H {
			break
		}
		core.DrawTextClipped(s, r.X+1, y, r.W-2, style, lines[i])
	}
	// A message longer than the pane is normal (stack dumps), so say so rather
	// than end mid-sentence.
	if hidden := len(lines) - lv.detailScroll - r.H; hidden > 0 {
		core.DrawTextRight(s, r.X, r.Y+r.H-1, r.W-1, dimStyle,
			core.Truncate("▾ more (Alt+↓)", r.W-1))
	}
}

// detailLines renders one entry into the details pane's lines, wrapped to w.
// Separate from drawDetails so scroll bounds use the same text that is drawn.
//
// Cached per (entry, width), see detailCache. Callers must treat the slice as
// read-only: the next call returns the same one.
func (lv *LogViewer) detailLines(row logRow, w int) []string {
	// Three columns, not one: the body is indented by two, so a narrower pane
	// leaves WrapText a width <= 0, which returns the paragraph unwrapped, and
	// DrawTextClipped then cuts it at the edge with no ellipsis.
	if w < 3 {
		return nil
	}
	e := row.entry
	if e == lv.detailCacheEntry && w == lv.detailCacheWidth {
		return lv.detailCache
	}
	lines := []string{
		"Date    " + formatSQLDate(e.Date),
		// The row's own file, not the selection's: with several merged this is the
		// only place a row names its file at full width.
		"Log     " + row.ref.Type.String() + " (" + lv.fileLabel(row.ref) + ")",
		"Source  " + e.Source(),
		"Message",
	}
	for _, para := range splitLogLines(e.Text) {
		for _, line := range core.WrapText(para, w-2) {
			lines = append(lines, "  "+line)
		}
	}
	lv.detailCacheEntry, lv.detailCacheWidth, lv.detailCache = e, w, lines
	return lines
}
