package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestTreeView() *TreeView {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10)
	tv.SetNodes([]TreeNode{{ID: 1, Label: "root"}})
	return tv
}

// newTestTreeViewExpandable returns a tree with one collapsed, expandable
// node. SetNodes clamps sel to a valid index, so this single node starts out
// selected — matching the common real-world case a click-drag begins from
// (an already-selected row).
func newTestTreeViewExpandable() *TreeView {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10)
	tv.SetNodes([]TreeNode{{ID: 1, Label: "root", HasKids: true}})
	return tv
}

// TestSetNodesClampsScrollWhenListShrinks pins down that SetNodes calls
// ensureVisible itself: a collapse or refresh shrinking the flat node list
// below the current scroll offset would otherwise leave scroll pointing
// past the end of nodes, and Draw's render loop (idx := tv.scroll + row; if
// idx >= len(tv.nodes) { break }) exits on its first iteration, rendering
// the tree blank until the next arrow-key press recomputes scroll.
func TestSetNodesClampsScrollWhenListShrinks(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // inner.H = 8

	nodes := make([]TreeNode, 30)
	for i := range nodes {
		nodes[i] = TreeNode{ID: TreeNodeID(i + 1)}
	}
	tv.SetNodes(nodes)

	// Jump to the last node, scrolling deep into the 30-node list (an
	// 8-row view can't show it without scrolling).
	tv.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone))
	if tv.scroll == 0 {
		t.Fatalf("scroll did not advance after jumping to the last of %d nodes in an 8-row view", len(nodes))
	}
	deepScroll := tv.scroll

	// A collapse elsewhere in the tree rebuilds the flat node list via
	// SetNodes, shrinking it well below the old scroll offset.
	tv.SetNodes(nodes[:3])
	if tv.scroll >= len(tv.nodes) {
		t.Fatalf("scroll = %d after SetNodes shrank the list from 30 to %d nodes (scroll was %d beforehand) — Draw's render loop would show nothing", tv.scroll, len(tv.nodes), deepScroll)
	}
}

// TestSelectIDSelectsAndFiresOnSelect confirms SelectID both moves the
// visual selection to the requested node and invokes OnSelect — unlike
// SetNodes, whose sel-clamping alone doesn't mean "select this node" and
// fires nothing. This is what a caller that adds a node programmatically
// (e.g. ObjectExplorer.AddRoot for a newly connected server) needs so the
// rest of the app reacts the same way a manual click/arrow-key selection
// would.
func TestSelectIDSelectsAndFiresOnSelect(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10)
	tv.SetNodes([]TreeNode{{ID: 1, Label: "a"}, {ID: 2, Label: "b"}, {ID: 3, Label: "c"}})

	var fired TreeNodeID
	calls := 0
	tv.OnSelect = func(id TreeNodeID) { fired = id; calls++ }

	tv.SelectID(3)

	if tv.sel != 2 {
		t.Errorf("sel after SelectID(3) = %d, want 2 (the index of ID 3)", tv.sel)
	}
	if calls != 1 || fired != 3 {
		t.Errorf("OnSelect calls = %d, fired = %d; want exactly one call with ID 3", calls, fired)
	}
}

// TestSelectIDUnknownIDIsNoOp confirms an ID absent from the current node
// list leaves selection and OnSelect untouched, rather than e.g. clamping
// to some arbitrary index.
func TestSelectIDUnknownIDIsNoOp(t *testing.T) {
	tv := newTestTreeView()
	tv.OnSelect = func(TreeNodeID) { t.Error("OnSelect fired for an ID not present in the tree") }

	tv.SelectID(999)

	if tv.sel != 0 {
		t.Errorf("sel after SelectID(unknown) = %d, want unchanged 0", tv.sel)
	}
}

// TestTreeViewClickOnLabelOfSelectedRowDoesNotToggle pins down that only the
// "[+]"/"[-]" expander glyph toggles expand state, not any click landing on
// an already-selected row: dragging an object into the query editor always
// starts from the label, and would otherwise flip its expand state on the
// way out.
func TestTreeViewClickOnLabelOfSelectedRowDoesNotToggle(t *testing.T) {
	tv := newTestTreeViewExpandable()
	if tv.nodes[0].Expanded {
		t.Fatalf("test setup: node starts expanded, want collapsed")
	}

	handled := tv.HandleMouse(tcell.NewEventMouse(10, 1, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true")
	}
	if tv.nodes[0].Expanded {
		t.Fatalf("clicking the label of an already-selected row toggled expand; want it to only select/arm a drag")
	}
}

func TestTreeViewClickOnExpanderTogglesRow(t *testing.T) {
	tv := newTestTreeViewExpandable()

	// Column 1 falls inside the "[+] " glyph for a depth-0 row (inner.X=1).
	handled := tv.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true")
	}
	if !tv.nodes[0].Expanded {
		t.Fatalf("clicking the expander glyph did not toggle expand")
	}
}

// TestTreeViewClickOnExpanderTogglesRowWhileScrolled pins down that the
// expander hit-test translates through tv.scrollX: comparing the click's
// screen column directly against the expander's virtual column
// (node.Depth*2..+4, see Draw's row-line layout) only matches at
// scrollX==0, leaving a still-visible glyph unresponsive once the tree is
// scrolled horizontally.
func TestTreeViewClickOnExpanderTogglesRowWhileScrolled(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // inner.X = 1, inner.W = 38
	tv.SetNodes([]TreeNode{{ID: 1, Label: strings.Repeat("x", 100), Depth: 5, HasKids: true}})

	// The depth-5 expander's virtual columns are 10..14; at scrollX=8 that
	// lands on-screen at column 1+(10-8)..1+(14-8) = 3..7, still well within
	// the panel and clearly not the unscrolled position.
	tv.scrollX = 8

	handled := tv.HandleMouse(tcell.NewEventMouse(4, 1, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true")
	}
	if !tv.nodes[0].Expanded {
		t.Fatalf("clicking the visibly-scrolled expander glyph did not toggle expand")
	}
}

// TestTreeViewHeldButtonOverExpanderDoesNotReToggle covers the
// open-then-immediately-close flicker: tcell's all-motion mouse tracking
// resends Buttons()==Button1 on every cursor motion while the button stays
// down, so without the mouseDragging latch a click on the expander that
// twitches before release re-toggles the node right back closed.
func TestTreeViewHeldButtonOverExpanderDoesNotReToggle(t *testing.T) {
	tv := newTestTreeViewExpandable()

	tv.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModNone))
	if !tv.nodes[0].Expanded {
		t.Fatalf("press on expander did not expand the node")
	}

	handled := tv.HandleMouse(tcell.NewEventMouse(2, 1, tcell.Button1, tcell.ModNone))
	if !handled {
		t.Fatalf("HandleMouse() = false, want true while still over the row")
	}
	if !tv.nodes[0].Expanded {
		t.Fatalf("node collapsed on a held-button move over the same expander; want it to stay expanded")
	}

	// Release, then a genuine new press, does toggle it again.
	tv.HandleMouse(tcell.NewEventMouse(2, 1, tcell.ButtonNone, tcell.ModNone))
	tv.HandleMouse(tcell.NewEventMouse(2, 1, tcell.Button1, tcell.ModNone))
	if tv.nodes[0].Expanded {
		t.Fatalf("a fresh press after release did not collapse the node")
	}
}

// TestTreeViewRightClickUsesButton2 pins tcell v3's mouse button mapping:
// Button2 is Secondary (right-click), Button3 is Middle. Using Button3, as
// tcell v1/v2 did, silently breaks the Object Explorer's context menu.
func TestTreeViewRightClickUsesButton2(t *testing.T) {
	tv := newTestTreeView()
	var gotID TreeNodeID
	fired := false
	tv.OnRightClick = func(id TreeNodeID, x, y int) { fired = true; gotID = id }

	if tv.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button3, tcell.ModNone)) {
		t.Error("Button3 (Middle) should not be handled as a tree click")
	}
	if fired {
		t.Error("OnRightClick fired on Button3 (Middle), want only on Button2 (Secondary)")
	}

	if !tv.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button2, tcell.ModNone)) {
		t.Error("Button2 (Secondary/right) should be handled")
	}
	if !fired {
		t.Fatal("OnRightClick did not fire on Button2 (Secondary/right-click)")
	}
	if gotID != 1 {
		t.Errorf("OnRightClick node ID = %d, want 1", gotID)
	}
}

func TestTreeViewShiftF10OpensContextMenu(t *testing.T) {
	tv := newTestTreeView()
	fired := false
	tv.OnRightClick = func(id TreeNodeID, x, y int) { fired = true }

	if tv.HandleKey(tcell.NewEventKey(tcell.KeyF10, "", tcell.ModNone)) {
		t.Error("plain F10 has no tree-level meaning and should not be consumed")
	}
	if fired {
		t.Error("OnRightClick fired on plain F10, want only on Shift+F10")
	}

	if !tv.HandleKey(tcell.NewEventKey(tcell.KeyF10, "", tcell.ModShift)) {
		t.Error("Shift+F10 should be handled")
	}
	if !fired {
		t.Fatal("OnRightClick did not fire on Shift+F10")
	}
}

func TestTreeViewMenuKeyOpensContextMenu(t *testing.T) {
	tv := newTestTreeView()
	fired := false
	tv.OnRightClick = func(id TreeNodeID, x, y int) { fired = true }

	if !tv.HandleKey(tcell.NewEventKey(tcell.KeyMenu, "", tcell.ModNone)) {
		t.Error("KeyMenu should be handled")
	}
	if !fired {
		t.Fatal("OnRightClick did not fire on KeyMenu")
	}
}

// TestTreeViewCtrlSpaceOpensContextMenu confirms Ctrl+Space is a third
// keyboard equivalent for the context menu, alongside Shift+F10 and
// KeyMenu. tcell.KeyRune + " " + ModCtrl is the decoded shape terminals
// actually report, not a legacy KeyNUL constant — Editor and DataGrid match
// on the same key.
func TestTreeViewCtrlSpaceOpensContextMenu(t *testing.T) {
	tv := newTestTreeView()
	fired := false
	tv.OnRightClick = func(id TreeNodeID, x, y int) { fired = true }

	if !tv.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl)) {
		t.Error("Ctrl+Space should be handled")
	}
	if !fired {
		t.Fatal("OnRightClick did not fire on Ctrl+Space")
	}
}

// TestSetNodesComputesContentWForHorizontalScroll checks contentW picks up
// the widest row's full rendered width (indent + expander + icon + label),
// not just the label — the horizontal scrollbar in Draw only appears once
// contentW exceeds inner.W, so an under-count would hide it for rows that
// are wide only because of deep nesting or a wide icon.
func TestSetNodesComputesContentWForHorizontalScroll(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // inner.W = 38
	tv.SetNodes([]TreeNode{
		{ID: 1, Label: "short"},
		{ID: 2, Label: "a_much_longer_column_label_here", Depth: 2, HasKids: true},
	})
	want := 2*2 + 4 + len("a_much_longer_column_label_here")
	if tv.contentW != want {
		t.Errorf("contentW = %d, want %d", tv.contentW, want)
	}
}

// TestWheelLeftRightScrollsHorizontally checks WheelLeft/WheelRight move
// scrollX directly, and that scrollRight clamps at contentW-inner.W so the
// scrollbar thumb can never run past the end of the track.
func TestWheelLeftRightScrollsHorizontally(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // inner.W = 38
	tv.SetNodes([]TreeNode{{ID: 1, Label: strings.Repeat("x", 100)}})

	if !tv.HandleMouse(tcell.NewEventMouse(5, 1, tcell.WheelRight, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelRight) returned false")
	}
	if tv.scrollX != 4 {
		t.Errorf("scrollX = %d after one WheelRight, want 4", tv.scrollX)
	}

	if !tv.HandleMouse(tcell.NewEventMouse(5, 1, tcell.WheelLeft, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelLeft) returned false")
	}
	if tv.scrollX != 0 {
		t.Errorf("scrollX = %d after WheelRight then WheelLeft, want back to 0", tv.scrollX)
	}

	maxScroll := tv.contentW - tv.rect.Inner(1).W
	for i := 0; i < 100; i++ {
		tv.HandleMouse(tcell.NewEventMouse(5, 1, tcell.WheelRight, tcell.ModNone))
	}
	if tv.scrollX != maxScroll {
		t.Errorf("scrollX = %d after scrolling far past the end, want clamped to %d", tv.scrollX, maxScroll)
	}
}

// TestShiftWheelUpDownScrollsHorizontally checks the Shift+WheelUp/WheelDown
// fallback some terminals use instead of WheelLeft/WheelRight — matches
// DataGrid's/Editor's/PlanView's identical convention.
func TestShiftWheelUpDownScrollsHorizontally(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10)
	tv.SetNodes([]TreeNode{{ID: 1, Label: strings.Repeat("x", 100)}})

	tv.HandleMouse(tcell.NewEventMouse(5, 1, tcell.WheelDown, tcell.ModShift))
	if tv.scrollX != 4 {
		t.Errorf("scrollX = %d after Shift+WheelDown, want 4", tv.scrollX)
	}
	tv.HandleMouse(tcell.NewEventMouse(5, 1, tcell.WheelUp, tcell.ModShift))
	if tv.scrollX != 0 {
		t.Errorf("scrollX = %d after Shift+WheelUp, want back to 0", tv.scrollX)
	}
}

// TestHorizontalScrollbarDragMovesScrollX checks a Button1 press on the
// horizontal scrollbar's track — drawn over the bottom border row, outside
// the row range HandleMouse's row-based hit-testing otherwise assumes —
// updates scrollX, mirroring the vertical scrollbar's existing drag support.
func TestHorizontalScrollbarDragMovesScrollX(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // rect.Bottom()-1 = 9, inner.X..inner.X+inner.W = 1..39
	tv.SetNodes([]TreeNode{{ID: 1, Label: strings.Repeat("x", 100)}})

	if !tv.HandleMouse(tcell.NewEventMouse(20, 9, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse(Button1) on the horizontal scrollbar track returned false")
	}
	if tv.scrollX == 0 {
		t.Error("scrollX did not move after dragging the horizontal scrollbar thumb")
	}
}

// NodeIDAt is what a host uses to find out what a mouse press actually
// landed on, so it must report "no node" for every part of the widget that
// isn't a node row — in particular the vertical scrollbar column, which the
// internal row hit-test never bounded mx against (that bound lived only in
// HandleScrollbarDrag, running first). App's Object Explorer drag-and-drop
// arms from this: without the column check, a press on the scrollbar armed a
// node drag that then swallowed every motion event, so the thumb never
// followed the mouse.
func TestNodeIDAtRejectsNonNodePositions(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10)    // inner = {X:1, Y:1, W:38, H:8}
	nodes := make([]TreeNode, 30) // more than inner.H, so a scrollbar shows
	for i := range nodes {
		nodes[i] = TreeNode{ID: i + 1, Label: "n"}
	}
	tv.SetNodes(nodes)

	if id, ok := tv.NodeIDAt(5, 1); !ok || id != 1 {
		t.Errorf("NodeIDAt on the first node row = (%d, %v), want (1, true)", id, ok)
	}

	// rect.Right()-1 is where Draw paints the vertical scrollbar.
	if id, ok := tv.NodeIDAt(39, 3); ok {
		t.Errorf("NodeIDAt on the scrollbar column = (%d, true), want no node", id)
	}
	// The left border column is not content either.
	if id, ok := tv.NodeIDAt(0, 3); ok {
		t.Errorf("NodeIDAt on the left border = (%d, true), want no node", id)
	}
	// Top and bottom border rows.
	if id, ok := tv.NodeIDAt(5, 0); ok {
		t.Errorf("NodeIDAt on the top border row = (%d, true), want no node", id)
	}
	if id, ok := tv.NodeIDAt(5, 9); ok {
		t.Errorf("NodeIDAt on the bottom border row = (%d, true), want no node", id)
	}
	// Outside the widget entirely.
	if id, ok := tv.NodeIDAt(100, 100); ok {
		t.Errorf("NodeIDAt outside the rect = (%d, true), want no node", id)
	}
}

// Blank space below the last node is inside the content area but past the
// end of the node list, so it must report no node — arming a drag from it
// used to carry whatever happened to be selected.
func TestNodeIDAtRejectsBlankSpaceBelowLastNode(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // inner.H = 8 rows for nodes
	tv.SetNodes([]TreeNode{{ID: 1, Label: "a"}, {ID: 2, Label: "b"}})

	if _, ok := tv.NodeIDAt(5, 2); !ok {
		t.Error("NodeIDAt on the second node row reported no node")
	}
	if id, ok := tv.NodeIDAt(5, 6); ok {
		t.Errorf("NodeIDAt on blank space below the last node = (%d, true), want no node", id)
	}
}
