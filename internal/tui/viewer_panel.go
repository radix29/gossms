package tui

import (
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/layout"
	"github.com/radix29/tuikit/theme"
)

// viewer_panel.go is the shell the Log File Viewer and the Extended Events
// viewer share: a toolbar row over a grid and a details pane either side of a
// horizontal splitter. Only the shell is shared. The panels' content diverges
// (LogViewer's filter field, XEventViewer's group rows and overlay catch-up),
// so HandleKey/HandleMouse, the toolbar cells and the details text stay with
// each panel; see log_viewer.go and xevent_viewer.go.

// viewerPanel is embedded by LogViewer and XEventViewer.
type viewerPanel struct {
	rect   core.Rect
	active bool

	grid     *controls.DataGrid
	splitter *layout.Splitter

	toolRect   core.Rect
	gridRect   core.Rect
	detailRect core.Rect
	tools      controls.ToolRow

	// detail is the details pane's scroll state, so a long value can be read
	// past the pane's height. Each panel builds (and caches) its lines and
	// passes their count to detail.Draw and scrollDetailLines alike.
	detail core.LinePane
}

// newViewerPanel makes the shell: a cell-cursor grid wired to the app's
// copy-with-status and cell-width setting, under a splitter titled title
// giving the grid ratio of the height.
func newViewerPanel(app *App, title string, ratio float64) viewerPanel {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	grid.SetStatusStyle(theme.StyleGridStatus)
	grid.OnCopyRequest = app.copyWithStatus
	grid.SetMaxCellWidth(app.cfg.MaxCellLength + 2)
	p := viewerPanel{
		grid:     grid,
		splitter: newHorizontalSplitter(title),
		// A message longer than the pane is normal (stack dumps, deadlock XML), so
		// say so rather than end mid-sentence.
		detail: core.LinePane{More: "▾ more (Alt+↓)"},
	}
	p.splitter.SetRatio(ratio)
	return p
}

// setShellBounds positions the shell: the toolbar row, then the grid and the
// details pane either side of the splitter. The embedding panel's SetBounds
// lays out its toolbar cells in toolRect afterwards.
func (p *viewerPanel) setShellBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	if h >= 1 {
		p.toolRect = core.Rect{X: x, Y: y, W: w, H: 1}
	} else {
		p.toolRect = core.Rect{}
	}
	p.splitter.SetBounds(x, y+1, w, h-1)
	p.layoutChildren()
}

// layoutChildren gives the grid and the details pane their halves of the area
// below the toolbar, on every resize and after every splitter drag.
func (p *viewerPanel) layoutChildren() {
	p.gridRect = p.splitter.FirstRect()
	p.detailRect = p.splitter.SecondRect()
	p.grid.SetBounds(p.gridRect.X, p.gridRect.Y, p.gridRect.W, p.gridRect.H)
}

// scrollDetailLines moves the details pane by delta lines, clamped to n, the
// line count of the text the pane draws (its detailLines), so the last line is
// always reachable and never overshot.
func (p *viewerPanel) scrollDetailLines(n, delta int) {
	if p.detailRect.H <= 0 {
		return
	}
	p.detail.ScrollBy(delta, n, p.detailRect.H)
}
