package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// newLatchTestApp builds an App with the widgets handleMouse touches on the
// menu/toolbar row, plus one alert dialog to open from a button action.
func newLatchTestApp() *App {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 30}
	a.menuBar = controls.NewMenuBar()
	a.toolbar = controls.NewToolbar()
	a.contextMenu = &controls.ContextMenu{}
	a.alertDialog = dialogs.NewAlertDialog(a.screen)
	a.allDialogs = []Dialog{a.alertDialog}
	a.explorerSplit = layout.NewVerticalSplitter()
	a.explorerSplit.SetBounds(0, 1, 100, 28)
	return a
}

// press/release drive handleMouse the way Run's loop does — syncDialogStack
// runs between events, so a dialog opened on the press is already on the
// stack when the release arrives. That is exactly what used to make the
// release miss every latch owner.
func (a *App) testClickAt(x, y int) {
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	a.syncDialogStack()
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	a.syncDialogStack()
}

// A toolbar button whose action opens a dialog must not leave Toolbar's
// mouseDragging latched. It used to: the release landed while the dialog was
// on the stack, handleMouse returned early there, and Toolbar never saw it —
// so the next fresh click read as a resend of the old gesture and the
// button's action never fired.
func TestToolbarClickWorksAfterDialogOpeningClick(t *testing.T) {
	a := newLatchTestApp()

	fired := 0
	a.toolbar.SetButtons([]controls.ToolbarButton{
		{Icon: "X", Tooltip: "Opens a dialog", Action: func() {
			fired++
			a.alertDialog.ShowAlert("Test", "hi")
		}},
	})
	a.menuBar.SetBounds(0, 0, 100)
	a.toolbar.SetBounds(0, 0, 100)

	// One button — icon width 1 plus a pad column either side — right-aligned
	// in a 100-column row, so it occupies columns 97..99.
	const buttonX = 98

	a.testClickAt(buttonX, 0)
	if fired != 1 {
		t.Fatalf("first click fired the action %d times, want 1", fired)
	}
	a.alertDialog.Hide()
	a.syncDialogStack()

	a.testClickAt(buttonX, 0)
	if fired != 2 {
		t.Errorf("click after a dialog-opening one fired the action %d times, want 2 "+
			"(Toolbar.mouseDragging left latched by the swallowed release)", fired)
	}
}

// Same latch, on the menu bar: a menu item whose action opens a dialog must
// not stop the next click on a menu header from opening its dropdown.
func TestMenuBarOpensAfterDialogOpeningItem(t *testing.T) {
	a := newLatchTestApp()
	a.menuBar.SetMenus([]controls.Menu{{
		Label: "File",
		Items: []controls.MenuItem{{Label: "Thing", Action: func() {
			a.alertDialog.ShowAlert("Test", "hi")
		}}},
	}})
	a.menuBar.SetBounds(0, 0, 100)
	a.toolbar.SetBounds(0, 0, 100)

	a.testClickAt(2, 0) // open the File menu
	if !a.menuBar.IsOpen() {
		t.Fatal("File menu did not open on the first click")
	}
	a.testClickAt(2, 2) // activate "Thing", which opens the alert
	if !a.alertDialog.Visible() {
		t.Fatal("the menu item's action did not open the dialog")
	}
	a.alertDialog.Hide()
	a.syncDialogStack()

	a.testClickAt(2, 0)
	if !a.menuBar.IsOpen() {
		t.Error("menu header click after a dialog-opening menu item did not open the menu " +
			"(MenuBar.mouseDragging left latched by the swallowed release)")
	}
}
