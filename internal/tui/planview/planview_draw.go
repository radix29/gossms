package planview

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/textutil"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/theme"
)

// Draw renders the tab bar, statement selector (when shown), and the
// active tab's content.
func (v *PlanView) Draw(s tcell.Screen) {
	v.tabBar().Draw(s)
	v.drawStatementBar(s)
	v.drawMissingIndexBanner(s)
	v.drawLiveRow(s)
	switch {
	case v.err != nil:
		v.drawMessage(s, fmt.Sprintf("Error parsing execution plan: %v", v.err))
	case v.plan == nil && v.liveOn && v.liveNote != "":
		v.drawWrappedMessage(s, v.liveNote)
	case v.plan == nil && v.liveOn:
		v.drawMessage(s, "Waiting for the running statement's plan…")
	case v.plan == nil:
		v.drawMessage(s, "No execution plan loaded")
	case v.activeTab == TabXML:
		v.xml.Draw(s)
	case v.activeTab == TabTree:
		v.drawTreeTab(s)
	default: // TabPlan
		v.drawGraphTab(s)
	}
}

// DrawOverlay renders the summary grid's right-click menu and "Show Value"
// popup, if open. Call it after every other widget in the frame has drawn: both
// float free of the grid's rect, so later drawing paints over them. Hosts
// (QueryPanel, PlanPanel) call it at the end of their Draw, as for DataGrid/
// Editor overlays.
func (v *PlanView) DrawOverlay(s tcell.Screen) {
	v.drawSummaryOverlay(s)
}

// OverlayActive reports whether one of those popups is showing. It carries
// controls.DataGrid.OverlayActive's contract outward: while true, a host must
// give PlanView exclusive first refusal of every key and mouse event, or input
// meant for a popup goes to whatever sits under it. PlanView applies the same
// rule in HandleKey/HandleMouse.
func (v *PlanView) OverlayActive() bool {
	return v.summaryOverlayActive()
}

// drawMessage fills the content area with one line of placeholder or error text.
func (v *PlanView) drawMessage(s tcell.Screen, msg string) {
	st := theme.StylePanel()
	core.FillRect(s, v.contentRect, ' ', st)
	if v.contentRect.H > 0 && v.contentRect.W > 2 {
		core.DrawTextClipped(s, v.contentRect.X+1, v.contentRect.Y, v.contentRect.W-2, st, msg)
	}
}

// drawWrappedMessage is drawMessage for a note longer than a line, wrapped
// to the content width (SetLiveNote's explanations).
func (v *PlanView) drawWrappedMessage(s tcell.Screen, msg string) {
	st := theme.StylePanel()
	core.FillRect(s, v.contentRect, ' ', st)
	if v.contentRect.W <= 2 {
		return
	}
	for i, line := range core.WrapTextLimit(msg, v.contentRect.W-2, v.contentRect.H) {
		core.DrawTextClipped(s, v.contentRect.X+1, v.contentRect.Y+i, v.contentRect.W-2, st, line)
	}
}

// expandLabel is the tab bar's right-aligned Expand button, offered when
// OnExpand is set and no search is showing.
const expandLabel = "[ Expand ]"

// tabBar returns the Plan/Tree/XML tab bar, built fresh each call so draw and
// hit-test share one layout. Its right cell is the search indicator while one
// shows (it takes priority), else the Expand button when OnExpand is set.
func (v *PlanView) tabBar() *controls.TabBar {
	b := &controls.TabBar{
		Labels:   tabLabels[:],
		Active:   int(v.activeTab),
		Inset:    1,
		Right:    v.searchIndicatorText(),
		OnSelect: func(i int) { v.setActiveTab(Tab(i)) },
	}
	if b.Right == "" && v.OnExpand != nil {
		b.Right = expandLabel
	}
	b.SetBounds(v.tabRect.X, v.tabRect.Y, v.tabRect.W)
	return b
}

// expandRect returns where the Expand button is drawn, or the zero Rect when
// it isn't (no OnExpand, a search indicator in its place, or no room).
func (v *PlanView) expandRect() core.Rect {
	if b := v.tabBar(); b.Right == expandLabel {
		return b.RightRect()
	}
	return core.Rect{}
}

// searchIndicatorText returns the tab bar's search display: "/query_" while
// typing, "/query (i/n)" once confirmed with matches, else "".
func (v *PlanView) searchIndicatorText() string {
	switch {
	case v.searchSt.active:
		return "/" + v.searchSt.query + "_"
	case len(v.searchSt.matches) > 0:
		return fmt.Sprintf("/%s (%d/%d)", v.searchSt.query, v.searchSt.idx+1, len(v.searchSt.matches))
	}
	return ""
}

// arrowRects returns the ◀ and ▶ hit-test rectangles for the statement
// selector, matching drawStatementBar's column layout exactly.
func (v *PlanView) arrowRects() (prev, next core.Rect) {
	x0 := v.stmtRect.X + 1
	mid := fmt.Sprintf(" Statement %d/%d ", v.stmtIdx+1, len(v.plan.Statements))
	x1 := x0 + core.DisplayWidth("◀")
	x2 := x1 + core.DisplayWidth(mid)
	return core.Rect{X: x0, Y: v.stmtRect.Y, W: x1 - x0, H: 1},
		core.Rect{X: x2, Y: v.stmtRect.Y, W: core.DisplayWidth("▶"), H: 1}
}

// drawStatementBar renders the "◀ Statement i/n ▶  cost%  statement text"
// row, shown only for a multi-statement plan (see layout).
func (v *PlanView) drawStatementBar(s tcell.Screen) {
	if v.stmtRect.H != 1 {
		return
	}
	pal := theme.Active()
	st := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Text)
	core.FillRect(s, v.stmtRect, ' ', st)

	stmt := v.plan.Statements[v.stmtIdx]
	prev, next := v.arrowRects()
	core.DrawText(s, prev.X, prev.Y, st, "◀")
	core.DrawText(s, prev.X+prev.W, prev.Y, st,
		fmt.Sprintf(" Statement %d/%d ", v.stmtIdx+1, len(v.plan.Statements)))
	core.DrawText(s, next.X, next.Y, st, "▶")

	rest := fmt.Sprintf("  %.0f%%  %s", v.statementCostPct(v.stmtIdx), textutil.Collapse(stmt.Text))
	restX := next.X + next.W
	if w := v.stmtRect.Right() - restX; w > 0 {
		core.DrawTextClipped(s, restX, v.stmtRect.Y, w, st, rest)
	}
}
