package tui

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tui/planview"
	"github.com/radix29/tuikit/core"
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
	a.connectDialog = d
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
	// Every read-only target the app hands showClipboardMenu must say so; one
	// missing a ClipboardAccess method silently stops satisfying it and gets
	// Cut and Paste.
	for _, target := range []clipboardTarget{&DeviceCodeDialog{}, planview.New()} {
		if _, ok := target.(core.ClipboardAccess); !ok {
			t.Errorf("%T is not a core.ClipboardAccess", target)
		}
	}
	a := newClipboardTestApp()
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
	a.paste.buf.WriteString("error")
	a.endBracketedPaste()
	if lv.filter.Value() != "error" {
		t.Errorf("filter = %q after a terminal paste, want %q", lv.filter.Value(), "error")
	}
	lv.setFilterFocused(false)
	if got := a.activeClipboardTarget(); got != nil {
		t.Errorf("target = %T with the grid focused, want nil", got)
	}
}

// A panel that is its own clipboard target (Plan, Query Store, …) is asked:
// Ctrl+C there used to reach nothing.
func TestAPanelThatIsAClipboardTargetIsAsked(t *testing.T) {
	a := newClipboardTestApp()
	pp := NewPlanPanel(a, "Plan", loadTestPlan(t))
	a.panels.AddPanel(pp)
	a.focus = focusOnPanels
	if got := a.activeClipboardTarget(); got != clipboardTarget(pp) {
		t.Errorf("target = %T, want the plan panel", got)
	}
}
