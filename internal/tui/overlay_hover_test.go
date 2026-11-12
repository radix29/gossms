package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/tuikittest"
)

// TestOverlayBlocksHoverUnderneath: pointer motion arrives as ButtonNone, which
// routeRelease hands to every layer so latches reset. Under a dialog or context
// menu that hover must not reach the toolbar behind it — its button lit up and
// its tooltip showed while the Properties dialog was open.
func TestOverlayBlocksHoverUnderneath(t *testing.T) {
	const tip = "TOOLTIP"
	for _, tc := range []struct {
		name    string
		overlay func(a *App)
		want    bool
	}{
		{"no overlay", func(*App) {}, true},
		{"dialog", func(a *App) { a.alertDialog.ShowAlert("Title", "Message") }, false},
		{"context menu", func(a *App) {
			a.contextMenu.Show(40, 10, []controls.MenuItem{{Label: "Item"}})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newDragTestApp(t, 0)
			a.toolbar.SetButtons([]controls.ToolbarButton{{Icon: "X", Tooltip: tip}})
			a.toolbar.SetBounds(0, 0, dragTestScreenW)
			tc.overlay(a)
			a.modals.Sync()

			// The button is the toolbar's last cell; a hover from a previous motion
			// must be cleared too, not only a new one refused.
			a.toolbar.HandleMouse(tcell.NewEventMouse(dragTestScreenW-2, 0, tcell.ButtonNone, 0))
			a.handleMouse(tcell.NewEventMouse(dragTestScreenW-2, 0, tcell.ButtonNone, 0))

			s := tuikittest.NewScreen(dragTestScreenW, dragTestScreenH)
			s.Fill('~', tcell.StyleDefault) // so a drawn space shows too
			blank := s.Snapshot()
			a.toolbar.DrawOverlay(s)
			if got := s.Snapshot() != blank; got != tc.want {
				t.Errorf("tooltip drawn = %v, want %v", got, tc.want)
			}
		})
	}
}
