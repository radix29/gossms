package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tui/planview"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// PlanPanel is a detached, top-level window showing one execution plan in
// its own planview.PlanView — opened by the Execution Plan tab's
// "[ Expand ]" button (see QueryPanel.newPlanView's OnExpand). Every click
// creates a new PlanPanel, even from the same query panel; nothing is
// shared or reused. Unlike DetailBrowser it's an ordinary closable panel
// (no Closable() override), so it gets the tab bar's [x] like a Query
// window does.
type PlanPanel struct {
	rect     core.Rect
	title    string
	planView *planview.PlanView
	active   bool

	// filePath is the .sqlplan this panel was opened from, and where Save
	// writes back without prompting. Empty for a plan that came from a
	// query rather than a file.
	filePath string
}

// NewPlanPanel creates a detached plan panel already showing plan. plan is
// handed to a fresh planview.PlanView via SetPlan (not SetPlanXML) so the
// already-parsed tree is reused instead of re-parsed. app supplies the
// clipboard hook for the operator summary's "Copy" item, the same one
// QueryPanel's own PlanView gets — without it DataGrid leaves the item out
// of the menu entirely.
func NewPlanPanel(app *App, title string, plan *showplan.Plan) *PlanPanel {
	v := planview.New()
	v.OnCopyRequest = app.copyWithStatus
	// A detached plan has no connection of its own, so the missing-index
	// script opens against whatever the tree points at. The script names its
	// own database in a USE, so the panel it lands in only decides where it
	// would run, not what it says.
	v.OnMissingIndex = func(script string) {
		sc, database := app.selectedConnTarget()
		app.openQueryWithText(sc, database, script)
	}
	v.SetPlan(plan)
	return new(PlanPanel{title: title, planView: v})
}

// Title returns the panel's tab/window title (Panel interface).
func (pp *PlanPanel) Title() string { return pp.title }

// PlanXML returns the plan's source document, for File > Save.
func (pp *PlanPanel) PlanXML() string {
	if plan := pp.planView.Plan(); plan != nil {
		return plan.XML
	}
	return ""
}

// SetBounds positions the panel, reserving the first row for the title bar.
func (pp *PlanPanel) SetBounds(x, y, w, h int) {
	pp.rect = core.Rect{X: x, Y: y, W: w, H: h}
	pp.planView.SetBounds(x, y+1, w, h-1)
}

// SetActive marks this panel focused (affects title bar colour).
func (pp *PlanPanel) SetActive(v bool) {
	pp.active = v
	pp.planView.SetActive(v)
}

// Draw renders the title bar and the wrapped PlanView.
func (pp *PlanPanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	if pp.active {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
	}
	core.FillRect(s, core.Rect{X: pp.rect.X, Y: pp.rect.Y, W: pp.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, pp.rect.X+1, pp.rect.Y, pp.rect.W-2, titleStyle, pp.title)
	pp.planView.Draw(s)
	// Drawn last — see the "overlays drawn last" rule in tuikit/README.md.
	pp.planView.DrawOverlay(s)
}

// HandleKey delegates to the wrapped PlanView.
func (pp *PlanPanel) HandleKey(ev *tcell.EventKey) bool { return pp.planView.HandleKey(ev) }

// HandleMouse delegates to the wrapped PlanView.
func (pp *PlanPanel) HandleMouse(ev *tcell.EventMouse) bool { return pp.planView.HandleMouse(ev) }

// HasSelection, SelectedText, Cut, Paste, and SelectAll implement
// clipboardTarget (see internal/tui/clipboard.go) by forwarding to the
// wrapped PlanView — same pattern as DetailBrowser forwarding to its grid.
func (pp *PlanPanel) HasSelection() bool   { return pp.planView.HasSelection() }
func (pp *PlanPanel) SelectedText() string { return pp.planView.SelectedText() }
func (pp *PlanPanel) Cut() string          { return pp.planView.Cut() }
func (pp *PlanPanel) Paste(text string)    { pp.planView.Paste(text) }
func (pp *PlanPanel) SelectAll()           { pp.planView.SelectAll() }
