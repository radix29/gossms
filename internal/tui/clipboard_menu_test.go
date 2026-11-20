package tui

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tui/planview"
	"github.com/radix29/tuikit/dialogs"
)

// menuState is the open context menu's entries, label → enabled, and their
// order (dividers as "-").
func menuState(a *App) (order []string, enabled map[string]bool) {
	enabled = map[string]bool{}
	for _, it := range a.contextMenu.Items() {
		if it.Divider {
			order = append(order, "-")
			continue
		}
		order = append(order, it.Label)
		enabled[it.Label] = it.Enabled == nil || it.Enabled()
	}
	return order, enabled
}

// connectDialogOnStack is twoPaneConnectDialog as the frontmost dialog of its
// App, so handleMouse routes to it.
func connectDialogOnStack(t *testing.T) (*App, *ConnectDialog) {
	t.Helper()
	d := twoPaneConnectDialog(t)
	a := d.app
	installDialog(a, d)
	a.modals = dialogs.Stack{}
	a.modals.Register(d)
	a.modals.Sync()
	return a, d
}

func rightPress(a *App, x, y int) {
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button2, tcell.ModNone))
	a.lastButtons = tcell.Button2
}

// A right-click on any dialog text field is one route (App.openDialogClipboardMenu
// over core.ClipboardFocuser): it focuses the field it landed on, keeps that
// field's selection, and opens Cut/Copy/Paste/Select All there.
func TestRightClickOnADialogFieldFocusesItAndOpensTheClipboardMenu(t *testing.T) {
	a, d := connectDialogOnStack(t)
	d.fUser.SetValue("sa")
	d.fUser.SelectAll()
	if d.focusedWidget() == focusable(d.fUser) {
		t.Fatal("setup: the user field already has focus")
	}

	rightPress(a, d.fUser.InputX()+1, d.fUser.Bounds().Y)

	if !a.contextMenu.OverlayActive() {
		t.Fatal("right-click on the User Name field opened no menu")
	}
	if d.focusedWidget() != focusable(d.fUser) {
		t.Errorf("focus = %T, want the right-clicked field", d.focusedWidget())
	}
	if !d.fUser.HasSelection() {
		t.Error("the right-click dropped the field's selection")
	}
	order, enabled := menuState(a)
	if want := []string{"Cut", "Copy", "Paste", "-", "Select All"}; !slices.Equal(order, want) {
		t.Fatalf("menu = %v, want %v", order, want)
	}
	if !enabled["Cut"] || !enabled["Copy"] || !enabled["Paste"] {
		t.Errorf("enabled = %v, want Cut, Copy and Paste on with a selection", enabled)
	}
	if a.activeClipboardTarget() != clipboardTarget(d.fUser) {
		t.Errorf("the menu's actions would act on %T, not the right-clicked field", a.activeClipboardTarget())
	}

	// A password field gives nothing to the clipboard, so Copy and Cut stay grey.
	a.contextMenu.Hide()
	a.lastButtons = tcell.ButtonNone
	d.fPassword.SetValue("hunter2")
	d.fPassword.SelectAll()
	rightPress(a, d.fPassword.InputX()+1, d.fPassword.Bounds().Y)
	if _, enabled := menuState(a); enabled["Copy"] || enabled["Cut"] || !enabled["Paste"] {
		t.Errorf("password field menu enabled = %v, want Paste only", enabled)
	}
}

// The preview keeps its own menu (Copy Connection String): the generic route
// declines it, so the dialog's right-click handler still runs.
func TestConnectPreviewKeepsItsOwnRightClickMenu(t *testing.T) {
	a, d := connectDialogOnStack(t)
	d.fServer.SetValue("myhost")
	d.setTab(connectTabString)
	d.layoutFields()
	r := d.fConnStrPreview.Bounds()
	rightPress(a, r.X+2, r.Y)
	if _, enabled := menuState(a); !enabled["Copy Connection String"] {
		t.Errorf("preview menu = %v, want its own Copy Connection String", a.contextMenu.Items())
	}
}

// A greyed field is not a right-click target, as it is not a left-click or Tab
// one: with Entra Default, a right-click on Password opens no menu and
// leaves focus where it was, instead of parking it on a field that refuses
// every key.
func TestRightClickOnADisabledFieldOpensNoClipboardMenu(t *testing.T) {
	a, d := connectDialogOnStack(t)
	d.ddAuth.SetSelected(slices.Index(config.AllAuthMethods(), config.AuthEntraDefault))
	d.applyAuthFields()
	if d.fPassword.Enabled() {
		t.Fatal("setup: Password is enabled under Entra Default")
	}
	before := d.focusedWidget()

	rightPress(a, d.fPassword.InputX()+1, d.fPassword.Bounds().Y)

	if a.contextMenu.OverlayActive() {
		t.Error("a right-click on the greyed Password field opened the clipboard menu")
	}
	if d.focusedWidget() != before {
		t.Errorf("focus moved to %T, want it left on %T", d.focusedWidget(), before)
	}
}

// FilterDialog has its own hit test over its rows; it skips a disabled value
// field like dialogs.ClipboardFieldAt does.
func TestFilterDialogRightClickSkipsADisabledValueField(t *testing.T) {
	d := showTestFilterDialogOnScreen(t, 120, 30)
	v := d.rows[1].value
	v.SetEnabled(false)
	before := d.focusIdx
	r := v.Bounds()
	if d.FocusClipboardTargetAt(v.InputX()+1, r.Y) {
		t.Error("a right-click on a disabled value field was taken for the clipboard")
	}
	if d.focusIdx != before {
		t.Errorf("focusIdx = %d, want %d unchanged", d.focusIdx, before)
	}
	v.SetEnabled(true)
	if !d.FocusClipboardTargetAt(v.InputX()+1, r.Y) || d.focusIdx != 3 {
		t.Errorf("an enabled value field: focusIdx = %d, want 3", d.focusIdx)
	}
}

// An open dropdown list is drawn over the fields below it: a right-click on a
// list entry must not focus the field underneath and open the clipboard menu
// over the still-open list.
func TestRightClickOnAnOpenDropdownListOpensNoClipboardMenu(t *testing.T) {
	a, d := connectDialogOnStack(t)
	d.setFocus(dialogs.IndexOfFocusable(d.focusable, d.ddAuth))
	d.ddAuth.HandleKey(tcell.NewEventKey(tcell.KeyF4, "", tcell.ModNone))
	if !d.ddAuth.OverlayActive() {
		t.Fatal("setup: F4 did not open the Authentication list")
	}

	rightPress(a, d.fPassword.InputX()+1, d.fPassword.Bounds().Y)

	if a.contextMenu.OverlayActive() {
		t.Error("a right-click on the open list opened the clipboard menu")
	}
	if d.focusedWidget() != focusable(d.ddAuth) {
		t.Errorf("focus moved to %T, want it left on the Authentication dropdown", d.focusedWidget())
	}
}

// FilterDialog's own hit test refuses too while an operator list is open over
// the value fields of the rows below.
func TestFilterDialogRightClickIgnoredWhileAnOperatorListIsOpen(t *testing.T) {
	d := showTestFilterDialogOnScreen(t, 120, 30)
	d.focusIdx = 0
	d.syncFocus()
	d.rows[0].op.HandleKey(tcell.NewEventKey(tcell.KeyF4, "", tcell.ModNone))
	if !d.rows[0].op.OverlayActive() {
		t.Fatal("setup: F4 did not open the operator list")
	}
	v := d.rows[1].value
	if d.FocusClipboardTargetAt(v.InputX()+1, v.Bounds().Y) {
		t.Error("a right-click through the open operator list focused a value field")
	}
	if d.focusIdx != 0 {
		t.Errorf("focusIdx = %d, want 0 unchanged", d.focusIdx)
	}
}

// A right-click on a button is not the clipboard's: no menu, and the click is
// the dialog's to route.
func TestRightClickOffAFieldOpensNoClipboardMenu(t *testing.T) {
	a, d := connectDialogOnStack(t)
	if a.openDialogClipboardMenu(d.Rect().X, d.Rect().Y) {
		t.Error("a right-click on the dialog's border was taken for the clipboard")
	}
	if a.contextMenu.OverlayActive() {
		t.Error("a menu opened")
	}
}

// A read-only view offers Copy and Select All, never a Cut or Paste it would
// refuse.
func TestReadOnlyViewsGetACopyOnlyMenu(t *testing.T) {
	// Every read-only target the app hands showClipboardMenu must say so
	// through core.ClipboardTarget's Can* methods. The panels used to lack
	// them and were taken as editable, so their menu offered Cut and Paste.
	a := newClipboardTestApp()
	for _, target := range []clipboardTarget{&DeviceCodeDialog{}, planview.New(),
		NewPlanPanel(a, "Plan", loadTestPlan(t)).FocusedClipboardTarget(), a.newDetailBrowser().grid} {
		if target.CanCut() || target.CanPaste() {
			t.Errorf("%T: CanCut=%v CanPaste=%v, want both false", target, target.CanCut(), target.CanPaste())
		}
	}
	a.showClipboardMenu(a.newDetailBrowser().grid, 0, 0)
	if order, _ := menuState(a); slices.Contains(order, "Cut") || slices.Contains(order, "Paste") {
		t.Errorf("Object Explorer Details menu = %v, want no Cut or Paste", order)
	}
	qp := focusedQueryPanel(t, a)
	a.showClipboardMenu(qp.messages, 0, 0)
	if order, _ := menuState(a); !slices.Equal(order, []string{"Copy", "-", "Select All"}) {
		t.Errorf("Messages menu = %v, want Copy and Select All", order)
	}
	a.showClipboardMenu(qp.editor, 0, 0)
	if order, _ := menuState(a); !slices.Equal(order, []string{"Cut", "Copy", "Paste", "-", "Select All"}) {
		t.Errorf("SQL editor menu = %v, want the full menu", order)
	}
}

// The Log Viewer's filter box is a text field like any other: Ctrl+C/X/V and a
// terminal paste reach it while it has the keyboard. Before, the resolver never
// asked the panel and a paste into it was dropped.
func TestLogViewerFilterIsAClipboardTarget(t *testing.T) {
	a := newClipboardTestApp()
	lv := newTestLogViewer()
	lv.app = a
	a.panels.AddPanel(lv)
	a.focus = focusOnPanels
	a.syncActivePanelFocus()
	lv.SetBounds(0, 1, 140, 30)
	if !lv.filterVisible() {
		t.Fatal("setup: the filter box is not laid out")
	}
	lv.setFilterFocused(true)
	if got := a.activeClipboardTarget(); got != clipboardTarget(lv.filter) {
		t.Fatalf("target = %T, want the filter box", got)
	}
	a.beginBracketedPaste()
	for _, r := range "error" {
		a.paste.bracket.Key(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	a.endBracketedPaste()
	if lv.filter.Value() != "error" {
		t.Errorf("filter = %q after a terminal paste, want %q", lv.filter.Value(), "error")
	}
	lv.setFilterFocused(false)
	if got := a.activeClipboardTarget(); got != nil {
		t.Errorf("target = %T with the grid focused, want nil", got)
	}
}

// A panel answering core.ClipboardHost (Plan, Query Store, …) is asked, and
// its answer is the widget itself: Ctrl+C there used to reach nothing.
func TestAPanelThatIsAClipboardHostIsAsked(t *testing.T) {
	a := newClipboardTestApp()
	pp := NewPlanPanel(a, "Plan", loadTestPlan(t))
	a.panels.AddPanel(pp)
	a.focus = focusOnPanels
	if got := a.activeClipboardTarget(); got != clipboardTarget(pp.planView) {
		t.Errorf("target = %T, want the plan panel's PlanView", got)
	}
}

// A panel of several grids names the one with the keyboard, a different target
// per grid. That is what lets pasteInto's identity check drop a paste aimed at
// one grid that arrives after focus moved to the other, with no
// core.ClipboardTargetTokener; a panel answering with itself would need one.
// The target being the grid itself also gives Ctrl+C there the results grid's
// behaviour: with no viewer open it copies the cell under the cursor
// (copySelection's DataGrid branch), where the forwarding panel copied nothing.
func TestAGridPanelNamesItsFocusedGrid(t *testing.T) {
	a := newClipboardTestApp()
	plan := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	p := NewPlanComparePanel(a, "Compare", plan, plan)
	a.panels.AddPanel(p)
	a.focus = focusOnPanels

	p.setFocus(true)
	if got := a.activeClipboardTarget(); got != clipboardTarget(p.ops) {
		t.Errorf("operators focused: target = %T, want the operators grid", got)
	}
	p.setFocus(false)
	if got := a.activeClipboardTarget(); got != clipboardTarget(p.props) {
		t.Errorf("properties focused: target = %T, want the properties grid", got)
	}

	qs, _, _ := newQSPanel(t, "Top Resource Consuming Queries")
	qs.setFocus(qsFocusPlans)
	if got := qs.FocusedClipboardTarget(); got != clipboardTarget(qs.plansGrid) {
		t.Errorf("Query Store plans focused: target = %T, want the plans grid", got)
	}
	qs.setFocus(qsFocusReport)
	if got := qs.FocusedClipboardTarget(); got != clipboardTarget(qs.grid) {
		t.Errorf("Query Store report focused: target = %T, want the report grid", got)
	}

	rm := NewReplicationMonitorPanel(newTestApp(), nil)
	if got := rm.FocusedClipboardTarget(); got == nil || got != clipboardTarget(rm.focusedGrid()) {
		t.Errorf("Replication Monitor: target = %T, want its focused grid", got)
	}
}

// A nil grid is a nil target, not a non-nil interface holding a nil pointer,
// which activeClipboardTarget's callers would call through.
func TestGridClipboardTargetOfANilGridIsNil(t *testing.T) {
	if got := gridClipboardTarget(nil); got != nil {
		t.Errorf("gridClipboardTarget(nil) = %#v, want a nil interface", got)
	}
}

// Object Explorer Details is a target only while its grid's "Show Value"
// viewer is open: otherwise Ctrl+C there leaves the clipboard alone rather
// than copying the cell under the cursor, as it always has.
func TestDetailBrowserIsATargetOnlyWithItsViewerOpen(t *testing.T) {
	a := newClipboardTestApp()
	db := a.newDetailBrowser()
	db.SetBounds(0, 0, 100, 30)
	db.grid.SetData([]string{"Name"}, [][]string{{"alpha"}})
	if got := db.FocusedClipboardTarget(); got != nil {
		t.Errorf("no viewer open: target = %T, want nil", got)
	}
	db.grid.ShowValue()
	db.grid.SelectAll()
	if got := db.FocusedClipboardTarget(); got != clipboardTarget(db.grid) {
		t.Errorf("viewer open: target = %T, want the grid", got)
	}
}
