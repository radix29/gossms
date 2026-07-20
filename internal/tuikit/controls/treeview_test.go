package controls

import (
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

// TestTreeViewClickOnLabelOfSelectedRowDoesNotToggle pins the fix for a real
// bug: dragging an already-selected object (e.g. a table) from Object
// Explorer into the query editor toggled its expand state as a side effect,
// because the old code toggled on any click landing on the already-selected
// row, regardless of column. Only the "[+]"/"[-]" expander glyph should
// toggle — dragging always starts from the label, never the glyph.
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

// TestTreeViewHeldButtonOverExpanderDoesNotReToggle pins the fix for the
// open-then-immediately-close flicker: tcell's all-motion mouse tracking
// resends Buttons()==Button1 on every cursor motion while the button stays
// down, so a click on the expander that so much as twitches before release
// used to re-toggle the node right back closed.
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
// Button2 is Secondary (right-click), Button3 is Middle. A prior regression
// used Button3 for right-click, matching tcell v1/v2's convention instead,
// which silently broke the Object Explorer's context menu.
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
// KeyMenu. tcell.KeyRune + " " + ModCtrl is the real decoded shape (verified
// live: xfce4-terminal reports Ctrl+Space this way), not a legacy KeyNUL
// constant — see the same reasoning applied to Editor and DataGrid.
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
