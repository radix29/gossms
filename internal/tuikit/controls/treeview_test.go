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
