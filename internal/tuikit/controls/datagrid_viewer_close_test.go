package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// openDrawnViewer opens the content viewer and draws one frame, so the
// Close button has a laid-out rect.
func openDrawnViewer(t *testing.T) *DataGrid {
	t.Helper()
	g := newCellCursorGrid()
	g.SetBounds(0, 0, 40, 10)
	g.selRow, g.selCol = 1, 0
	g.openViewer()
	g.DrawOverlay(tuikittest.NewScreen(100, 30))
	if !g.viewer.open {
		t.Fatal("setup: viewer did not open")
	}
	if g.viewer.closeRect.W <= 0 {
		t.Fatalf("setup: Close button was not laid out: %+v", g.viewer.closeRect)
	}
	return g
}

// A click anywhere outside the popup must leave it alone. It used to
// dismiss, which threw away a long value the moment a click strayed while
// reading it or part-way through selecting it.
func TestDataGridViewerSurvivesClickOutside(t *testing.T) {
	g := openDrawnViewer(t)

	for _, p := range []struct{ x, y int }{
		{0, 0},   // top-left corner of the screen
		{5, 5},   // over the grid underneath
		{99, 29}, // bottom-right corner
	} {
		g.HandleMouse(tcell.NewEventMouse(p.x, p.y, tcell.Button1, tcell.ModNone))
		g.HandleMouse(tcell.NewEventMouse(p.x, p.y, tcell.ButtonNone, tcell.ModNone))
		if !g.viewer.open {
			t.Fatalf("a click at (%d,%d) outside the popup closed it", p.x, p.y)
		}
	}
}

// A click outside must also not reach the grid underneath — the popup owns
// every mouse event while it's up (see OverlayActive), so a stray click
// can't quietly move the cell cursor out from under the value on show.
func TestDataGridViewerSwallowsClicksOutside(t *testing.T) {
	g := openDrawnViewer(t)
	row, col := g.SelectedCell()

	g.HandleMouse(tcell.NewEventMouse(5, 3, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(5, 3, tcell.ButtonNone, tcell.ModNone))

	if r, c := g.SelectedCell(); r != row || c != col {
		t.Errorf("a click behind the popup moved the cell cursor from (%d,%d) to (%d,%d)", row, col, r, c)
	}
}

// The Close button dismisses it, so there is a way out that doesn't need
// the keyboard.
func TestDataGridViewerCloseButtonDismisses(t *testing.T) {
	g := openDrawnViewer(t)
	r := g.viewer.closeRect

	g.HandleMouse(tcell.NewEventMouse(r.X+1, r.Y, tcell.Button1, tcell.ModNone))
	if g.viewer.open {
		t.Fatal("clicking [ Close ] did not dismiss the viewer")
	}
	g.HandleMouse(tcell.NewEventMouse(r.X+1, r.Y, tcell.ButtonNone, tcell.ModNone))
	if g.viewer.dismiss.Held() {
		t.Error("the dismiss latch survived the release")
	}
}

// The button closes on the press, so the rest of that gesture arrives with
// the popup already gone — tcell resends Button1 on every motion while it
// stays held. Those resends must not land on the grid underneath as a
// fresh cell click.
func TestDataGridViewerCloseSwallowsRestOfGesture(t *testing.T) {
	g := openDrawnViewer(t)
	row, col := g.SelectedCell()
	r := g.viewer.closeRect

	g.HandleMouse(tcell.NewEventMouse(r.X+1, r.Y, tcell.Button1, tcell.ModNone))
	// Still held, now dragged over the grid's own rows.
	g.HandleMouse(tcell.NewEventMouse(5, 3, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(6, 4, tcell.Button1, tcell.ModNone))
	if r, c := g.SelectedCell(); r != row || c != col {
		t.Errorf("the tail of the closing gesture moved the cell cursor from (%d,%d) to (%d,%d)", row, col, r, c)
	}

	// After the release the grid takes clicks normally again.
	g.HandleMouse(tcell.NewEventMouse(6, 4, tcell.ButtonNone, tcell.ModNone))
	if g.viewer.dismiss.Held() {
		t.Fatal("the dismiss latch survived the release")
	}
}

// A latch must not survive into the next showing of the same widget: the
// button closes on the press, so the matching release is never routed back
// to a popup that no longer exists.
func TestDataGridViewerReopenClearsDismissLatch(t *testing.T) {
	g := openDrawnViewer(t)
	r := g.viewer.closeRect
	g.HandleMouse(tcell.NewEventMouse(r.X+1, r.Y, tcell.Button1, tcell.ModNone))

	// Reopened without the release ever arriving.
	g.openViewer()
	if g.viewer.dismiss.Held() {
		t.Fatal("reopening the viewer kept the previous dismissal latched")
	}
	g.DrawOverlay(tuikittest.NewScreen(100, 30))
	g.HandleMouse(tcell.NewEventMouse(g.viewer.closeRect.X+1, g.viewer.closeRect.Y, tcell.Button1, tcell.ModNone))
	if g.viewer.open {
		t.Error("the reopened viewer refused the first click on [ Close ]")
	}
}

// Replacing the grid's data closes the viewer: it was showing a cell of
// the data being replaced, and with click-outside no longer dismissing it,
// leaving it up would strand a popup over stale text that still claims
// every key and mouse event.
func TestDataGridViewerClosesWhenDataReplaced(t *testing.T) {
	g := openDrawnViewer(t)
	g.SetData([]string{"A"}, [][]string{{"one"}})
	if g.viewer.open {
		t.Error("SetData left the content viewer open over the replaced data")
	}
	if g.OverlayActive() {
		t.Error("OverlayActive stayed true after the data was replaced")
	}
}

// The viewer is read-only, so Cut removes nothing and returns "": a host
// treats "" as "nothing cut" and keeps the clipboard. Cut once returned the
// selection, and the app reported "Cut to clipboard" for text still on screen.
func TestDataGridViewerCutReturnsNothing(t *testing.T) {
	g := openDrawnViewer(t)
	g.SelectAll()
	if !g.HasSelection() || g.SelectedText() == "" {
		t.Fatal("setup: Select All in the viewer selected nothing")
	}
	before := g.viewer.Text()
	if got := g.Cut(); got != "" {
		t.Errorf("Cut() = %q from the read-only viewer, want \"\"", got)
	}
	if g.viewer.Text() != before {
		t.Error("Cut changed the read-only viewer's text")
	}
}
