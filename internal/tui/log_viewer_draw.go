package tui

import (
	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// log_viewer_draw.go renders the Log File Viewer panel: the toolbar row, the
// entry grid, the splitter, and the details pane.

// Draw renders the panel (Panel interface).
func (lv *LogViewer) Draw(s tcell.Screen) {
	// Both selectors are labelled with what they point at, so the labels
	// change without a resize — and a rect laid out for the old label leaves
	// the next button overpainting the tail of this one. Relaying out here
	// keeps the two in step; it is a handful of width measurements.
	lv.refreshToolLabels()
	lv.layoutTools()
	lv.drawToolbar(s)
	lv.grid.Draw(s)
	lv.splitter.Draw(s)
	lv.drawDetails(s)
}

// drawToolbar paints the toolbar row: the two selectors and the buttons in
// the tooltip scheme Activity Monitor's buttons use, then the filter field.
func (lv *LogViewer) drawToolbar(s tcell.Screen) {
	if lv.toolRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, lv.toolRect, ' ', barStyle)

	for i, t := range lv.tools {
		if t.rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if lv.toolDisabled(i) {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, t.rect, ' ', style)
		core.DrawText(s, t.rect.X+1, t.rect.Y, style, t.label)
	}
	// The stand-in for whatever did not fit. Dimmed only while the whole row
	// is: what it holds is gated item by item once the menu is open.
	if !lv.more.rect.IsZero() {
		style := theme.StyleTooltip()
		if !lv.toolsEnabled() {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, lv.more.rect, ' ', style)
		core.DrawText(s, lv.more.rect.X+1, lv.more.rect.Y, style, lv.more.label)
	}
	if lv.filterVisible() {
		lv.filter.Draw(s)
	}
}

// drawDetails paints the selected entry in full below the splitter: its
// date, log file and source on one line each, then the message wrapped over
// the rest of the pane. The message is drawn as the log wrote it — the grid
// row above is the flattened one-line form.
func (lv *LogViewer) drawDetails(s tcell.Screen) {
	r := lv.detailRect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	style := theme.StyleDefault()
	dimStyle := style.Foreground(pal.TextDim)
	core.FillRect(s, r, ' ', style)

	e := lv.selectedEntry()
	if e == nil {
		core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, dimStyle, "No entry selected")
		return
	}

	lines := lv.detailLines(e, r.W-2)
	for i := lv.detailScroll; i < len(lines); i++ {
		y := r.Y + i - lv.detailScroll
		if y >= r.Y+r.H {
			break
		}
		core.DrawTextClipped(s, r.X+1, y, r.W-2, style, lines[i])
	}
	// A message longer than the pane is the normal case for a stack dump, so
	// the pane says so rather than silently ending mid-sentence.
	if hidden := len(lines) - lv.detailScroll - r.H; hidden > 0 {
		core.DrawTextRight(s, r.X, r.Y+r.H-1, r.W-1, dimStyle,
			core.Truncate("▾ more (Alt+↓)", r.W-1))
	}
}

// detailLines renders one entry into the details pane's lines, wrapped to w
// columns. Kept separate from drawDetails so the scroll bounds can be
// computed from the same text that gets drawn.
//
// The result is cached per (entry, width) — see detailCache. Callers must
// treat the slice as read-only: the next call hands back the same one.
func (lv *LogViewer) detailLines(e *gosmo.ErrorLogEntry, w int) []string {
	// Three columns, not one: the message body is indented by two, so a
	// narrower pane leaves WrapText a width of zero or less, and it answers
	// that by handing back the paragraph unwrapped — one long line that
	// DrawTextClipped then cuts at the edge with no ellipsis.
	if w < 3 {
		return nil
	}
	if e == lv.detailCacheEntry && w == lv.detailCacheWidth {
		return lv.detailCache
	}
	lines := []string{
		"Date    " + formatSQLDate(e.Date),
		"Log     " + lv.logType.String() + " (" + lv.currentFileLabel() + ")",
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
