package planview

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newSummaryTabView returns a Tree-tab PlanView with the Operator Summary
// showing in its bottom section. The grid's cell cursor comes from New() —
// it's what makes the right-click / Ctrl+Space cell menu reachable, and so
// what makes PlanView's overlay contract load-bearing: the popups float free
// of the grid's rect and nothing else in the frame draws them.
func newSummaryTabView(t *testing.T) *PlanView {
	t.Helper()
	v := newTreeTabView(t)
	v.bottomMode = bottomSummary
	v.layoutTree()
	v.rebuildSummaryRows()
	if v.bottomRect.H <= 0 || v.bottomRect.W <= 0 {
		t.Fatalf("summary section has no area to click in: %+v", v.bottomRect)
	}
	return v
}

// summaryDataRowY is the first row of the grid that holds data rather than
// the header/separator the DataGrid draws at the top of its rect.
const summaryDataRowY = 2

// rightClickSummary opens the summary grid's context menu the way a user
// would — a right-click on one of its rows, routed through PlanView.
func rightClickSummary(t *testing.T, v *PlanView) {
	t.Helper()
	r := v.bottomRect
	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y+summaryDataRowY, tcell.Button2, tcell.ModNone))
	if !v.OverlayActive() {
		t.Fatal("right-click on an Operator Summary row did not open the grid's context menu")
	}
}

// PlanView must report an open summary popup to its host, which is what
// gets it drawn at all: nothing called DrawOverlay on this grid, so a menu
// opened here would have been invisible and then silently swallowed every
// later key and mouse event.
func TestSummaryContextMenuIsReportedAsAnActiveOverlay(t *testing.T) {
	v := newSummaryTabView(t)
	if v.OverlayActive() {
		t.Fatal("OverlayActive should be false before anything is opened")
	}
	rightClickSummary(t, v)
}

// An open overlay must get first refusal of every key. Without it, a digit
// typed at the menu switches PlanView's tab instead — and since the menu was
// never drawn, the tab would appear to change for no reason while the
// invisible menu stayed open.
func TestSummaryOverlayTakesKeysBeforeTabSwitching(t *testing.T) {
	v := newSummaryTabView(t)
	rightClickSummary(t, v)

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, "1", tcell.ModNone))
	if v.activeTab != TabTree {
		t.Errorf("a key typed at the open overlay switched to tab %v, want it consumed by the overlay", v.activeTab)
	}
}

// Escape must reach the grid and close the menu, rather than being read as
// the Tree tab's own key handling.
func TestSummaryOverlayClosesOnEscape(t *testing.T) {
	v := newSummaryTabView(t)
	rightClickSummary(t, v)

	v.HandleKey(namedKey(tcell.KeyEscape))
	if v.OverlayActive() {
		t.Error("Escape did not close the summary grid's context menu")
	}
}

// The overlay only exists in the Tree tab's summary mode, so switching away
// must stop reporting it — a host that kept giving PlanView first refusal
// would otherwise starve its own widgets.
func TestSummaryOverlayNotReportedOutsideSummaryMode(t *testing.T) {
	v := newSummaryTabView(t)
	rightClickSummary(t, v)

	v.setActiveTab(TabPlan)
	if v.OverlayActive() {
		t.Error("OverlayActive stayed true after leaving the Tree tab")
	}
	v.setActiveTab(TabTree)
	v.bottomMode = bottomHidden
	if v.OverlayActive() {
		t.Error("OverlayActive stayed true after hiding the bottom section")
	}
}

// The cell cursor is what makes "Show Value" reachable at all, and the
// Status column carries an operator's full warning text — the exact thing
// that gets clipped to the column width. Right-clicking a row must select
// that cell and open the menu.
func TestSummaryRightClickSelectsCellAndOpensMenu(t *testing.T) {
	v := newSummaryTabView(t)
	rightClickSummary(t, v)

	if !v.bottomFocused {
		t.Error("right-clicking the summary did not move focus to it, so the menu is driven by keys the tree still owns")
	}
}

// DataGrid offers its "Copy" item only when its own hook is set, and
// PlanView mirrors the host's OnCopyRequest across on every input event. A
// host that wires one must get a working Copy; one that doesn't must not be
// offered a menu entry that silently does nothing.
func TestSummaryCopyHookMirrorsHost(t *testing.T) {
	v := newSummaryTabView(t)
	if v.summarySt.grid.OnCopyRequest != nil {
		t.Fatal("grid started with a copy hook the host never set")
	}

	var copied string
	v.OnCopyRequest = func(s string) { copied = s }
	rightClickSummary(t, v)
	if v.summarySt.grid.OnCopyRequest == nil {
		t.Fatal("host's OnCopyRequest was not mirrored onto the grid, so Copy is missing from the menu")
	}

	// Drive the hook the way the menu item does, and check it reaches the host.
	v.summarySt.grid.OnCopyRequest("Hash Match")
	if copied != "Hash Match" {
		t.Errorf("copy hook delivered %q to the host, want %q", copied, "Hash Match")
	}

	v.OnCopyRequest = nil
	v.HandleKey(namedKey(tcell.KeyEscape))
	v.handleSummaryMouse(tcell.NewEventMouse(v.bottomRect.X+2, v.bottomRect.Y+summaryDataRowY, tcell.ButtonNone, tcell.ModNone))
	if v.summarySt.grid.OnCopyRequest != nil {
		t.Error("grid kept a copy hook after the host cleared its own, so Copy would appear and do nothing")
	}
}

// While the "Show Value" popup is open it owns the clipboard, so Ctrl+C
// copies what's selected inside it rather than the Tree tab's operator
// details. With it closed, the details are what Ctrl+C gets, as before.
func TestSummaryViewerOwnsClipboardWhileOpen(t *testing.T) {
	v := newSummaryTabView(t)
	detailsText := v.SelectedText()
	if detailsText == "" {
		t.Fatal("expected the Tree tab to offer operator details as its selection")
	}

	openShowValue(t, v)

	// SelectAll + HasSelection only report true through the viewer's own
	// read-only editor, so together they prove the viewer really is what's
	// open — OverlayActive alone would also be true for the context menu.
	v.SelectAll()
	if !v.summarySt.grid.HasSelection() {
		t.Fatal("the cell viewer holds no selection after SelectAll, so it never opened")
	}
	if !v.HasSelection() {
		t.Fatal("PlanView reported no selection while the cell viewer holds one")
	}
	if got := v.SelectedText(); got == detailsText {
		t.Error("Ctrl+C returned the operator details instead of the cell viewer's own text")
	}
}

// openShowValue picks "Show Value" out of the open cell menu with the
// keyboard, the way a user does. ContextMenu.Show starts with nothing
// hovered, so the first Down lands on the first item; "Copy" precedes
// "Show Value" whenever the host wired a copy hook.
func openShowValue(t *testing.T, v *PlanView) {
	t.Helper()
	rightClickSummary(t, v)
	v.HandleKey(namedKey(tcell.KeyDown))
	if v.OnCopyRequest != nil {
		v.HandleKey(namedKey(tcell.KeyDown)) // step past "Copy"
	}
	v.HandleKey(namedKey(tcell.KeyEnter))
	if !v.summarySt.grid.OverlayActive() {
		t.Fatal("Show Value did not open the cell viewer")
	}
}

// Enter while the cell menu is open must activate the highlighted item, not
// the summary's own jump-to-tree-node shortcut — which used to swallow it,
// leaving the menu open and the tree selection moved instead.
func TestSummaryMenuEnterActivatesItemNotNodeJump(t *testing.T) {
	v := newSummaryTabView(t)
	nodeBefore := v.selectedID

	openShowValue(t, v)
	v.SelectAll()
	if !v.summarySt.grid.HasSelection() {
		t.Error("Enter did not activate Show Value — the cell viewer never opened")
	}
	if v.selectedID != nodeBefore {
		t.Errorf("Enter jumped the tree selection (%d → %d) instead of driving the menu",
			nodeBefore, v.selectedID)
	}
}
