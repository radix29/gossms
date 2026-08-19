package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// scriptMenu is a two-level cascade in the shape Object Explorer's
// "Script … as ▸" uses: a submenu item, a leaf, and a disabled leaf.
func scriptMenu(ran *string) []MenuItem {
	return []MenuItem{
		{Label: "New Query"},
		{Label: "Script Table as", Sub: []MenuItem{
			{Label: "CREATE To", Sub: []MenuItem{
				{Label: "New Query Window", Action: func() { *ran = "create/query" }},
				{Label: "Clipboard", Action: func() { *ran = "create/clip" }},
			}},
			{Label: "DROP To", Action: func() { *ran = "drop" }},
			{Label: "ALTER To", Action: func() { *ran = "alter" }, Enabled: func() bool { return false }},
		}},
		{Label: "Refresh", Action: func() { *ran = "refresh" }},
	}
}

func TestMenuRowSuffixMarksSubmenus(t *testing.T) {
	if got := menuRowSuffix(MenuItem{Label: "Script", Sub: []MenuItem{{Label: "CREATE"}}}); got != "▸" {
		t.Fatalf("menuRowSuffix(submenu) = %q, want the ▸ marker", got)
	}
	if got := menuRowSuffix(MenuItem{Label: "Save", Shortcut: "Ctrl+S"}); got != "Ctrl+S" {
		t.Fatalf("menuRowSuffix(plain item) = %q, want its shortcut", got)
	}
	// The marker has to be paid for in the box width, or the widest
	// submenu item's label runs into it.
	plain := menuContentWidth([]MenuItem{{Label: "Script Table as"}}, 0)
	sub := menuContentWidth([]MenuItem{{Label: "Script Table as", Sub: []MenuItem{{Label: "x"}}}}, 0)
	if sub != plain+1 {
		t.Fatalf("menuContentWidth with a ▸ marker = %d, want %d (one column wider than %d without)", sub, plain+1, plain)
	}
}

func TestContextMenuKeyboardOpensSubmenuAndRunsLeaf(t *testing.T) {
	var ran string
	cm := &ContextMenu{}
	cm.Show(2, 2, scriptMenu(&ran))

	press := func(k tcell.Key) {
		if !cm.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone)) {
			t.Fatalf("HandleKey(%v) = false, want true while the menu is open", k)
		}
	}
	press(tcell.KeyDown)  // "New Query"
	press(tcell.KeyDown)  // "Script Table as"
	press(tcell.KeyRight) // opens its submenu, on "CREATE To"
	if cm.cascade.depth() != 1 {
		t.Fatalf("cascade depth = %d after Right on a submenu item, want 1", cm.cascade.depth())
	}
	press(tcell.KeyDown) // "DROP To"
	press(tcell.KeyEnter)
	if ran != "drop" {
		t.Fatalf("action run = %q, want %q", ran, "drop")
	}
	if cm.Visible() || cm.cascade.depth() != 0 {
		t.Fatalf("menu visible=%v cascade depth=%d after activating a leaf; want closed and empty", cm.Visible(), cm.cascade.depth())
	}
}

func TestContextMenuLeftClosesOnlyTheDeepestLevel(t *testing.T) {
	var ran string
	cm := &ContextMenu{}
	cm.Show(2, 2, scriptMenu(&ran))

	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // level 1
	cm.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // level 2, on "CREATE To"
	if cm.cascade.depth() != 2 {
		t.Fatalf("cascade depth = %d, want 2", cm.cascade.depth())
	}
	cm.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	if cm.cascade.depth() != 1 {
		t.Fatalf("cascade depth = %d after Left, want 1 — Left closes one level, not the menu", cm.cascade.depth())
	}
	cm.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if cm.cascade.depth() != 0 || !cm.Visible() {
		t.Fatalf("depth=%d visible=%v after Escape inside a submenu; want the submenu closed and the menu still open", cm.cascade.depth(), cm.Visible())
	}
	cm.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if cm.Visible() {
		t.Fatalf("menu stayed open after a second Escape; want it dismissed")
	}
}

func TestContextMenuSubmenuEnterOnItemWithSubOpensRatherThanCloses(t *testing.T) {
	var ran string
	cm := &ContextMenu{}
	cm.Show(2, 2, scriptMenu(&ran))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if !cm.Visible() || cm.cascade.depth() != 1 {
		t.Fatalf("visible=%v depth=%d after Enter on a submenu item; want the menu open with its submenu shown", cm.Visible(), cm.cascade.depth())
	}
	if ran != "" {
		t.Fatalf("action %q ran on Enter over a submenu item; want none", ran)
	}
}

func TestContextMenuSubmenuKeyboardSkipsDisabledItem(t *testing.T) {
	var ran string
	cm := &ContextMenu{}
	cm.Show(2, 2, scriptMenu(&ran))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	cm.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	// "ALTER To" is disabled, so Down from "DROP To" must wrap past it back
	// to "CREATE To" rather than land on something that can't act.
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) // DROP To
	cm.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) // wraps
	if got := cm.cascade.hoverAt(1); got != 0 {
		t.Fatalf("submenu hover = %d, want 0 (\"CREATE To\") — the disabled row must be skipped", got)
	}
}

func TestContextMenuMouseHoverOpensSubmenuAndClickRunsLeaf(t *testing.T) {
	var ran string
	cm := &ContextMenu{}
	cm.Show(2, 2, scriptMenu(&ran))
	s := &fakeMenuScreen{w: 80, h: 24}
	cm.Draw(s)

	// Root box is at (2,2); row 1 ("Script Table as") is at y=4. Hovering it
	// opens the submenu without any click.
	cm.HandleMouse(tcell.NewEventMouse(5, 4, tcell.ButtonNone, tcell.ModNone))
	if cm.cascade.depth() != 1 {
		t.Fatalf("cascade depth = %d after hovering a submenu row, want 1", cm.cascade.depth())
	}
	cm.Draw(s)

	sub := cm.cascade.levelRect(cm.drawnRect(), 1)
	if sub.W == 0 {
		t.Fatalf("submenu rect not recorded by Draw: %+v", sub)
	}
	// Click "DROP To" — the submenu's second row.
	cm.HandleMouse(tcell.NewEventMouse(sub.X+2, sub.Y+2, tcell.Button1, tcell.ModNone))
	if ran != "drop" {
		t.Fatalf("action run = %q, want %q", ran, "drop")
	}
	if cm.Visible() {
		t.Fatalf("menu stayed open after clicking a submenu leaf; want it closed")
	}
}

func TestContextMenuHeldButtonOverLeafRunsActionOnce(t *testing.T) {
	var calls int
	cm := &ContextMenu{}
	cm.Show(2, 2, []MenuItem{{Label: "Refresh", Action: func() { calls++ }}})
	cm.Draw(&fakeMenuScreen{w: 80, h: 24})
	// tcell resends Button1 on every motion while the button is held; the
	// latch is what stops the second event re-firing the action.
	cm.HandleMouse(tcell.NewEventMouse(5, 3, tcell.Button1, tcell.ModNone))
	cm.HandleMouse(tcell.NewEventMouse(6, 3, tcell.Button1, tcell.ModNone))
	if calls != 1 {
		t.Fatalf("Action called %d times, want 1", calls)
	}
}

func TestContextMenuHoveringSiblingClosesTheOpenSubmenu(t *testing.T) {
	var ran string
	cm := &ContextMenu{}
	cm.Show(2, 2, scriptMenu(&ran))
	cm.Draw(&fakeMenuScreen{w: 80, h: 24})
	cm.HandleMouse(tcell.NewEventMouse(5, 4, tcell.ButtonNone, tcell.ModNone)) // "Script Table as"
	if cm.cascade.depth() != 1 {
		t.Fatalf("cascade depth = %d, want 1", cm.cascade.depth())
	}
	cm.HandleMouse(tcell.NewEventMouse(5, 5, tcell.ButtonNone, tcell.ModNone)) // "Refresh"
	if cm.cascade.depth() != 0 {
		t.Fatalf("cascade depth = %d after hovering a sibling row, want 0", cm.cascade.depth())
	}
}

func TestSubmenuRectFlipsLeftAtTheScreenEdge(t *testing.T) {
	items := []MenuItem{{Label: "New Query Window"}, {Label: "Clipboard"}}
	parent := core.Rect{X: 50, Y: 4, W: 24, H: 5}
	s := &fakeMenuScreen{w: 80, h: 24}

	r := submenuRect(s, parent, 6, items)
	w := menuContentWidth(items, 20)
	if r.X != parent.X-w {
		t.Fatalf("submenu X = %d, want %d — it must flip to the parent's left rather than run off a %d-column screen", r.X, parent.X-w, s.w)
	}
	if r.Y != 5 {
		t.Fatalf("submenu Y = %d, want 5 (first item aligned with the parent row at y=6)", r.Y)
	}

	// Room on the right: it opens there instead.
	if r := submenuRect(s, core.Rect{X: 2, Y: 4, W: 24, H: 5}, 6, items); r.X != 26 {
		t.Fatalf("submenu X = %d, want 26 (immediately right of the parent box)", r.X)
	}
	// Near the bottom it is pushed up to stay on screen.
	if r := submenuRect(s, core.Rect{X: 2, Y: 18, W: 24, H: 5}, 22, items); r.Y != s.h-(len(items)+2) {
		t.Fatalf("submenu Y = %d, want %d (pushed up to fit)", r.Y, s.h-(len(items)+2))
	}
}

func TestMenuBarSubmenuOpensWithRightAndRunsLeaf(t *testing.T) {
	var ran string
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 80)
	mb.SetMenus([]Menu{{Label: "Query", Items: []MenuItem{
		{Label: "Execute", Action: func() { ran = "execute" }},
		{Label: "Results To", Sub: []MenuItem{
			{Label: "Grid", Action: func() { ran = "grid" }},
			{Label: "Text", Action: func() { ran = "text" }},
		}},
	}}})
	mb.Open()

	mb.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))  // "Results To"
	mb.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone)) // opens it
	if mb.cascade.depth() != 1 {
		t.Fatalf("cascade depth = %d after Right on a submenu item, want 1", mb.cascade.depth())
	}
	if !mb.IsOpen() {
		t.Fatalf("Right on a submenu item moved to the next menu header; want it to open the submenu")
	}
	mb.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) // "Text"
	mb.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if ran != "text" {
		t.Fatalf("action run = %q, want %q", ran, "text")
	}
	if mb.IsOpen() || mb.cascade.depth() != 0 {
		t.Fatalf("open=%v depth=%d after activating a submenu leaf; want everything closed", mb.IsOpen(), mb.cascade.depth())
	}
}

func TestMenuBarRightStillSwitchesMenusOnAPlainItem(t *testing.T) {
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 80)
	mb.SetMenus([]Menu{
		{Label: "File", Items: []MenuItem{{Label: "Open", Action: func() {}}}},
		{Label: "Edit", Items: []MenuItem{{Label: "Copy", Action: func() {}}}},
	})
	mb.Open()
	mb.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	if mb.openMenu != 1 {
		t.Fatalf("openMenu = %d after Right on an item with no submenu, want 1 (next header)", mb.openMenu)
	}
}

func TestMenuBarSubmenuMouseClickRunsLeaf(t *testing.T) {
	var ran string
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 80)
	mb.SetMenus([]Menu{{Label: "Query", Items: []MenuItem{
		{Label: "Execute", Action: func() { ran = "execute" }},
		{Label: "Results To", Sub: []MenuItem{
			{Label: "Grid", Action: func() { ran = "grid" }},
			{Label: "Text", Action: func() { ran = "text" }},
		}},
	}}})
	mb.Open()
	s := &fakeMenuScreen{w: 80, h: 24}
	mb.DrawOverlay(s)

	// Dropdown rows start at y=2; "Results To" is the second one.
	mb.HandleMouse(tcell.NewEventMouse(4, 3, tcell.ButtonNone, tcell.ModNone))
	if mb.cascade.depth() != 1 {
		t.Fatalf("cascade depth = %d after hovering a submenu row, want 1", mb.cascade.depth())
	}
	mb.DrawOverlay(s)
	sub := mb.cascade.levelRect(mb.dropdownRect(), 1)
	mb.HandleMouse(tcell.NewEventMouse(sub.X+2, sub.Y+1, tcell.Button1, tcell.ModNone))
	if ran != "grid" {
		t.Fatalf("action run = %q, want %q", ran, "grid")
	}
	if mb.IsOpen() {
		t.Fatalf("dropdown stayed open after clicking a submenu leaf; want it closed")
	}
}

func TestMenuBarClickOnSubmenuParentKeepsMenuOpen(t *testing.T) {
	var ran string
	mb := NewMenuBar()
	mb.SetBounds(0, 0, 80)
	mb.SetMenus([]Menu{{Label: "Query", Items: []MenuItem{
		{Label: "Results To", Sub: []MenuItem{{Label: "Grid", Action: func() { ran = "grid" }}}},
	}}})
	mb.Open()
	mb.DrawOverlay(&fakeMenuScreen{w: 80, h: 24})
	mb.HandleMouse(tcell.NewEventMouse(4, 2, tcell.Button1, tcell.ModNone))
	if !mb.IsOpen() || mb.cascade.depth() != 1 {
		t.Fatalf("open=%v depth=%d after clicking a cascade row; want the dropdown open with its submenu shown", mb.IsOpen(), mb.cascade.depth())
	}
	if ran != "" {
		t.Fatalf("action %q ran from clicking a cascade row; want none", ran)
	}
}
