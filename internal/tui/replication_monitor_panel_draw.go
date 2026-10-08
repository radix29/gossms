package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// replication_monitor_panel_draw.go is Replication Monitor's toolbar and
// drawing: one row of selectors over the four grids.

// The toolbar cells, in order.
const (
	rmToolRefresh = iota
	rmToolAuto
	rmToolHistory
	rmToolErrors
)

// buildTools makes the toolbar cells. Labels carry the selection, so they
// are refreshed on every draw (refreshToolLabels).
func (p *ReplicationMonitorPanel) buildTools() {
	p.tools.Cells = []controls.ToolCell{
		rmToolRefresh: {Action: p.Refresh},
		rmToolAuto:    {Action: p.showRateMenu},
		rmToolHistory: {Action: p.showWindowMenu},
		rmToolErrors:  {Action: p.toggleErrorsOnly},
	}
	p.refreshToolLabels()
}

func (p *ReplicationMonitorPanel) refreshToolLabels() {
	p.tools.Cells[rmToolRefresh].Label = "Refresh (F5)"
	p.tools.Cells[rmToolAuto].Label = "Auto refresh: " + rmRates[p.rateIdx].label + " ▾"
	p.tools.Cells[rmToolHistory].Label = "History: " + rmWindows[p.windowIdx].label + " ▾"
	if p.errorsOnly {
		p.tools.Cells[rmToolErrors].Label = "Failed sessions only: on"
	} else {
		p.tools.Cells[rmToolErrors].Label = "Failed sessions only: off"
	}
	p.tools.Cells[rmToolErrors].Selected = p.errorsOnly
}

// toolDisabled is the one gate the row has: Refresh while a read is out. The
// selectors stay live — a choice made mid-read takes effect on the next.
func (p *ReplicationMonitorPanel) toolDisabled(i int) bool { return i == rmToolRefresh && p.busy }

func (p *ReplicationMonitorPanel) toolReason(int) string { return "" }

// runTool invokes cell i, or does nothing while it is withheld.
func (p *ReplicationMonitorPanel) runTool(i int) {
	if p.toolDisabled(i) {
		return
	}
	p.tools.Cells[i].Action()
}

func (p *ReplicationMonitorPanel) layoutTools() {
	p.toolEnd = p.tools.Layout(p.toolRect, "")
}

func (p *ReplicationMonitorPanel) popMenuAt(r core.Rect, items []controls.MenuItem) {
	if r.IsZero() {
		r = core.Rect{X: p.rect.X, Y: p.rect.Y}
	}
	p.app.contextMenu.Show(r.X, r.Y+1, items)
}

func (p *ReplicationMonitorPanel) showRateMenu() {
	p.popMenuAt(p.tools.Cells[rmToolAuto].Rect, qsMenuItems(rmRates, rmRates[p.rateIdx],
		func(r rmRate) string { return r.label },
		func(r rmRate) {
			for i := range rmRates {
				if rmRates[i] == r {
					p.rateIdx = i
				}
			}
			p.startTicker()
			if !p.busy && p.snap.note == "" && len(p.snap.pubs) > 0 {
				p.pubsGrid.SetStatus(p.summary())
			}
		}))
}

func (p *ReplicationMonitorPanel) showWindowMenu() {
	p.popMenuAt(p.tools.Cells[rmToolHistory].Rect, qsMenuItems(rmWindows, rmWindows[p.windowIdx],
		func(w rmWindow) string { return w.label },
		func(w rmWindow) {
			for i := range rmWindows {
				if rmWindows[i] == w {
					p.windowIdx = i
				}
			}
			p.loadSessions(false)
		}))
}

func (p *ReplicationMonitorPanel) toggleErrorsOnly() {
	p.errorsOnly = !p.errorsOnly
	p.loadSessions(false)
}

// Draw renders the panel (Panel interface).
func (p *ReplicationMonitorPanel) Draw(s tcell.Screen) {
	p.refreshToolLabels()
	p.layoutTools()
	p.drawToolRow(s)
	for _, sp := range p.splits {
		sp.Draw(s)
	}
	grids := p.grids()
	for _, g := range grids {
		g.Draw(s)
	}
	// Last, over every grid: a cell's menu or value popup draws outside its
	// grid, and undrawn it still eats every key until Escape.
	for _, g := range grids {
		g.DrawOverlay(s)
	}
}

// drawToolRow paints the toolbar in Query Store's scheme, dimming Refresh
// while a read is out — the same predicate runTool refuses on.
func (p *ReplicationMonitorPanel) drawToolRow(s tcell.Screen) {
	r := p.toolRect
	if r.H != 1 {
		return
	}
	pal := theme.Active()
	core.FillRect(s, r, ' ', theme.StyleMenuBar())
	cells := append(p.tools.Cells[:len(p.tools.Cells):len(p.tools.Cells)], p.tools.More)
	for i, t := range cells {
		if t.Rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if i < len(p.tools.Cells) && p.toolDisabled(i) {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, t.Rect, ' ', style)
		core.DrawText(s, t.Rect.X+1, t.Rect.Y, style, t.Label)
	}
}
