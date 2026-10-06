package tui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// plan_compare_open.go starts a comparison the ways SSMS's Compare Showplan
// does: the plan on screen against a saved .sqlplan (Compare Showplan...), or
// against another plan already open in a panel (Compare with ▸). Both sit on
// the Query menu and on a right-click in a plan's graph or tree. The
// comparison itself is PlanComparePanel's; Query Store's Compare Plans is its
// third way in.

// openPlan is a plan a panel shows, with the name a comparison calls it by.
// panel is kept so Compare with ▸ can leave the plan's own panel off its list.
type openPlan struct {
	name  string
	plan  *showplan.Plan
	panel layout.Panel
}

// panelPlan returns the plan panel p shows: a detached or opened plan, or a
// query panel's Execution Plan tab.
func panelPlan(p layout.Panel) (openPlan, bool) {
	switch p := p.(type) {
	case *PlanPanel:
		if plan := p.planView.Plan(); plan != nil {
			return openPlan{name: p.Title(), plan: plan, panel: p}, true
		}
	case *QueryPanel:
		if p.planView != nil && p.planView.Plan() != nil {
			return openPlan{name: p.Title() + " (plan)", plan: p.planView.Plan(), panel: p}, true
		}
	}
	return openPlan{}, false
}

// openPlans lists every plan open in a panel, in tab order.
func (a *App) openPlans() []openPlan {
	var plans []openPlan
	for i := range a.panels.Count() {
		if op, ok := panelPlan(a.panels.PanelAt(i)); ok {
			plans = append(plans, op)
		}
	}
	return plans
}

// compareTitle names a comparison by its two sides — the A and B the grids'
// column headers refer to.
func compareTitle(a, b string) string {
	return "Compare Showplan — A: " + a + " · B: " + b
}

// openShowplanComparison opens from (A) against to (B), each side named by
// its source so a plan opened back out of the comparison is too.
func (a *App) openShowplanComparison(from openPlan, toName string, to *showplan.Plan) {
	cp := NewPlanComparePanel(a, compareTitle(from.name, toName), from.plan, to)
	cp.sideNames = [2]string{from.name, toName}
	a.panels.SetActive(a.panels.AddPanel(cp))
	a.focusPanels()
}

// compareShowplan runs Query > Compare Showplan...: the active panel's plan is
// A, and the .sqlplan the user picks is B.
func (a *App) compareShowplan() {
	from, ok := panelPlan(a.panels.ActivePanel())
	if !ok {
		a.setStatus("No execution plan to compare")
		return
	}
	a.compareShowplanFrom(from)
}

// compareShowplanFrom asks for the .sqlplan to compare from against. The plan
// is captured now, not looked up when the file is chosen: a query re-run
// behind the dialog would otherwise swap side A for a plan the user never
// asked to compare.
func (a *App) compareShowplanFrom(from openPlan) {
	start := ""
	if pp, ok := from.panel.(*PlanPanel); ok && pp.filePath != "" {
		start = filepath.Dir(pp.filePath)
	}
	a.fileDialog.ShowOpen("Compare Showplan", start, func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			a.setStatus(fmt.Sprintf("Open failed: %v", err))
			return
		}
		plan, err := parsePlanFile(data)
		if err != nil {
			a.setStatus(fmt.Sprintf("Could not parse %s: %v", filepath.Base(path), err))
			return
		}
		a.openShowplanComparison(from, filepath.Base(path), plan)
	})
}

// compareWithItem is the Compare with ▸ cascade for from: one entry per other
// open plan. With none to offer it is withheld and says why — an empty
// submenu would open onto nothing.
func (a *App) compareWithItem(from openPlan, ok bool) controls.MenuItem {
	item := controls.MenuItem{Label: "Compare with", Enabled: func() bool { return false }}
	if !ok {
		item.Note = "no execution plan"
		return item
	}
	for _, op := range a.openPlans() {
		if op.panel == from.panel {
			continue
		}
		item.Sub = append(item.Sub, controls.MenuItem{Label: op.name, Action: func() {
			a.openShowplanComparison(from, op.name, op.plan)
		}})
	}
	if len(item.Sub) == 0 {
		item.Note = "no other open plan"
		return item
	}
	item.Enabled = nil
	return item
}

// activeCompareWithItem is the Query menu's Compare with ▸, built for the
// active panel. The menu bar rebuilds its menus as it opens
// (MenuBar.OnBeforeOpen), so the list is the panels open at that moment.
func (a *App) activeCompareWithItem() controls.MenuItem {
	from, ok := panelPlan(a.panels.ActivePanel())
	return a.compareWithItem(from, ok)
}

// showPlanContextMenu is the right-click menu of a plan's graph or tree
// (planview.OnContextMenu), for the plan panel p shows.
func (a *App) showPlanContextMenu(x, y int, p layout.Panel) {
	from, ok := panelPlan(p)
	if !ok {
		return
	}
	a.contextMenu.Show(x, y, []controls.MenuItem{
		{Label: "Save Execution Plan As...", Action: func() { a.saveExecutionPlanAs() }},
		{Divider: true},
		{Label: "Compare Showplan...", Action: func() { a.compareShowplanFrom(from) }},
		a.compareWithItem(from, true),
	})
}

// parsePlanFile parses a .sqlplan as read from disk, through decodeTextFile
// like every other file gossms reads: SSMS writes .sqlplan as UTF-16,
// identified by its BOM (see text_encoding.go).
func parsePlanFile(data []byte) (*showplan.Plan, error) {
	text, _, _, _ := decodeTextFile(data)
	return showplan.Parse([]byte(text))
}
