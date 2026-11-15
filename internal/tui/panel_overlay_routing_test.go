package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/layout"
)

// Every panel whose HandleMouse gives a child overlay first refusal reports it
// through layout.OverlayPanel, or App.handleMouse routes a press outside the
// panel by position and the overlay never sees it.
var (
	_ layout.OverlayPanel = (*ActivityMonitor)(nil)
	_ layout.OverlayPanel = (*AGDashboard)(nil)
	_ layout.OverlayPanel = (*DetailBrowser)(nil)
	_ layout.OverlayPanel = (*LivePlanPanel)(nil)
	_ layout.OverlayPanel = (*LogViewer)(nil)
	_ layout.OverlayPanel = (*PlanComparePanel)(nil)
	_ layout.OverlayPanel = (*PlanPanel)(nil)
	_ layout.OverlayPanel = (*QueryPanel)(nil)
	_ layout.OverlayPanel = (*QueryStorePanel)(nil)
	_ layout.OverlayPanel = (*ReplicationMonitorPanel)(nil)
	_ layout.OverlayPanel = (*XEventViewer)(nil)
)

// The Details pane's cell menu stayed open when a press landed in Object
// Explorer: App.handleMouse routed by position, so the grid never saw the
// press that core.MouseHandler says dismisses it. It must close; a right-click
// then falls through to the tree row (to open the next menu there), a left
// click stops at the dismissal (controls.ContextMenu.HandleMouse).
func TestAPressInTheExplorerClosesTheDetailsCellMenu(t *testing.T) {
	for _, button := range []tcell.ButtonMask{tcell.Button1, tcell.Button2} {
		a := newDragTestApp(t, 3)
		db := NewDetailBrowser("Details")
		a.panels.AddPanel(db)
		right := a.explorerSplit.SecondRect()
		a.panels.SetBounds(right.X, right.Y, right.W, right.H)
		db.grid.SetData([]string{"Name"}, [][]string{{"a"}, {"b"}, {"c"}})

		g := db.grid.Bounds()
		a.handleMouse(tcell.NewEventMouse(g.X+2, g.Y+2, tcell.Button2, 0))
		a.handleMouse(tcell.NewEventMouse(g.X+2, g.Y+2, tcell.ButtonNone, 0))
		if !db.OverlayActive() {
			t.Fatal("setup: the right-click opened no cell menu")
		}

		y := draggableChildRow(t, a)
		before := a.explorer.Selected()
		a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, button, 0))
		a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.ButtonNone, 0))
		if db.OverlayActive() {
			t.Errorf("button %v in Object Explorer left the Details cell menu open", button)
		}
		want := before
		if button == tcell.Button2 {
			want = a.explorer.NodeAt(dragTestLabelX, y)
		}
		if got := a.explorer.Selected(); got != want {
			t.Errorf("button %v: tree selection = %q, want %q (Button2 falls through, Button1 stops)",
				button, got.label, want.label)
		}
	}
}
