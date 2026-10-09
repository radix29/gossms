package planview

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Draw renders the tab bar, statement selector (when shown), and the
// active tab's content.
func (v *PlanView) Draw(s tcell.Screen) {
	v.drawTabBar(s)
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

// tabSegments computes each tab's on-screen extent; Draw and hit-test share it
// so hits match what's drawn.
func (v *PlanView) tabSegments() [][]controls.TabSegment {
	widths := make([][]int, len(tabLabels))
	for i, label := range tabLabels {
		widths[i] = []int{controls.TabLabelWidth(label)}
	}
	return controls.TabStripSegments(v.tabRect.X+1, widths, v.tabRect.Right())
}

// drawTabBar renders the Plan/Tree/XML tabs and, when OnExpand is set, a
// right-aligned "[ Expand ]" button.
func (v *PlanView) drawTabBar(s tcell.Screen) {
	pal := theme.Active()
	bar := theme.StyleMenuBar()
	core.FillRect(s, v.tabRect, ' ', bar)
	col := v.tabRect.X + 1
	for i, seg := range v.tabSegments() {
		st := bar
		if Tab(i) == v.activeTab {
			st = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
		}
		core.DrawText(s, seg[0].X, v.tabRect.Y, st, " "+tabLabels[i]+" ")
		col = seg[0].X + seg[0].W + 1
	}
	v.expandBtnRect = core.Rect{}
	if search := v.searchIndicatorText(); search != "" {
		w := core.DisplayWidth(search)
		x := v.tabRect.Right() - w - 1
		if x > col {
			core.DrawText(s, x, v.tabRect.Y, bar, search)
		}
		return // the search indicator takes priority over Expand
	}
	if v.OnExpand != nil {
		label := "[ Expand ]"
		w := core.DisplayWidth(label)
		x := v.tabRect.Right() - w - 1
		if x > col {
			core.DrawText(s, x, v.tabRect.Y, bar, label)
			v.expandBtnRect = core.Rect{X: x, Y: v.tabRect.Y, W: w, H: 1}
		}
	}
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

// tabAt returns the tab index at screen column mx on the tab bar, or -1.
func (v *PlanView) tabAt(mx int) int {
	for i, seg := range v.tabSegments() {
		if mx >= seg[0].X && mx < seg[0].X+seg[0].W {
			return i
		}
	}
	return -1
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

	rest := fmt.Sprintf("  %.0f%%  %s", v.statementCostPct(v.stmtIdx), oneLine(stmt.Text))
	restX := next.X + next.W
	if w := v.stmtRect.Right() - restX; w > 0 {
		core.DrawTextClipped(s, restX, v.stmtRect.Y, w, st, rest)
	}
}

// oneLine collapses line breaks and repeated whitespace to a single display line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}
