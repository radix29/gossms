package planview

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// overlayScreen is a tcell.Screen fake with just enough surface to run
// DrawOverlay: the "Show Value" popup's geometry is derived from Size(),
// and the drawing helpers underneath it need Get/Put/SetContent/ShowCursor
// to exist. Nothing painted is retained — the tests below assert on
// PlanView and DataGrid state. Drawing matters here only because it's what
// gives the popup's read-only editor its bounds; until a frame has been
// drawn, no mouse event can be routed into it at all.
type overlayScreen struct {
	tcell.Screen
	w, h int
}

func (s *overlayScreen) Size() (int, int) { return s.w, s.h }
func (s *overlayScreen) Get(x, y int) (string, tcell.Style, int) {
	return " ", tcell.StyleDefault, 1
}
func (s *overlayScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	return str, 1
}
func (s *overlayScreen) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {}
func (s *overlayScreen) ShowCursor(x, y int)                                                    {}

// planViewScreenW/H match newTreeTabView's SetBounds, so DrawOverlay
// centres the popup for a view of exactly this size.
const planViewScreenW, planViewScreenH = 120, 40

// openShowValueDrawn opens the cell viewer and draws one frame, returning
// the popup's centre. That point is inside the popup's read-only editor
// and — the part these tests turn on — outside v.bottomRect: the popup is
// centred on the whole screen while the summary strip sits along the
// bottom, so it is where an ordinary drag inside the popup both starts and
// ends.
func openShowValueDrawn(t *testing.T, v *PlanView) (int, int) {
	t.Helper()
	openShowValue(t, v)
	v.DrawOverlay(&overlayScreen{w: planViewScreenW, h: planViewScreenH})
	cx, cy := planViewScreenW/2, planViewScreenH/2
	if v.bottomRect.Contains(cx, cy) {
		t.Fatalf("popup centre (%d,%d) is inside bottomRect %+v — these tests need it outside", cx, cy, v.bottomRect)
	}
	return cx, cy
}

// A gesture inside the "Show Value" popup ends with a release, and that
// release has to reach the grid: DataGrid hands it to the popup's editor,
// whose HandleMouse clears mouseDragging on a release regardless of
// position, precisely so a drag terminates cleanly wherever it ends.
// Routing the release by screen position instead never reaches the grid —
// the popup is centred on the whole screen, nowhere near the summary strip
// — so the latch stays armed and the *next* press is read as more of the
// same drag, extending the selection instead of re-anchoring it.
func TestSummaryViewerReleaseClearsDragLatch(t *testing.T) {
	v := newSummaryTabView(t)
	cx, cy := openShowValueDrawn(t, v)

	// Baseline: with no latch armed, a press re-anchors the selection onto
	// the click, so nothing stays selected. This is what the press after
	// the release must also do.
	v.SelectAll()
	v.HandleMouse(tcell.NewEventMouse(cx, cy, tcell.Button1, tcell.ModNone))
	if v.summarySt.grid.HasSelection() {
		t.Fatal("a press with no drag in progress failed to re-anchor the selection")
	}

	v.HandleMouse(tcell.NewEventMouse(cx, cy, tcell.ButtonNone, tcell.ModNone))

	v.SelectAll()
	v.HandleMouse(tcell.NewEventMouse(cx, cy, tcell.Button1, tcell.ModNone))
	if v.summarySt.grid.HasSelection() {
		t.Error("a fresh press in the cell viewer extended the previous selection — the release never reached the grid, leaving its drag latch armed")
	}
}

// The release must not disturb the popup on its way through, and
// PlanView's own tab-row/statement-bar latch has to come down with it —
// it's the same physical gesture.
func TestSummaryViewerReleaseKeepsPopupAndClearsOwnLatch(t *testing.T) {
	v := newSummaryTabView(t)
	cx, cy := openShowValueDrawn(t, v)

	v.HandleMouse(tcell.NewEventMouse(cx, cy, tcell.Button1, tcell.ModNone))
	v.HandleMouse(tcell.NewEventMouse(cx, cy, tcell.ButtonNone, tcell.ModNone))

	if !v.summarySt.grid.OverlayActive() {
		t.Error("releasing inside the cell viewer closed it")
	}
	if v.mouseDragging {
		t.Error("PlanView kept its own drag latch armed after the release")
	}
}
