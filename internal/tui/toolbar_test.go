package tui

import (
	"testing"

	"github.com/radix29/tuikit/controls"
)

// TestBuildToolbarHasMetaAfterActualPlan pins the toolbar's order and each
// button's label: Est.Plan, Act.Plan, Live, Meta in a row (SSMS's order for
// the plan buttons), and the plan buttons keep their compact, space-free text.
func TestBuildToolbarHasMetaAfterActualPlan(t *testing.T) {
	a := new(App{})
	buttons := a.buildToolbar()
	idx := func(icon string) int {
		for i, b := range buttons {
			if b.Icon == icon {
				return i
			}
		}
		return -1
	}
	est, act, live, meta := idx("Est.Plan"), idx("Act.Plan"), idx("Live"), idx("Meta")
	if est < 0 || act < 0 || live < 0 || meta < 0 {
		t.Fatalf("toolbar missing a button: Est.Plan=%d Act.Plan=%d Live=%d Meta=%d", est, act, live, meta)
	}
	if act != est+1 || live != act+1 || meta != live+1 {
		t.Errorf("toolbar order = Est.Plan@%d, Act.Plan@%d, Live@%d, Meta@%d; want them consecutive in that order", est, act, live, meta)
	}
	if buttons[meta].Action == nil {
		t.Error("the Meta button has no action — it would silently do nothing when clicked")
	}
	// The Meta toggle applies to any result set, connected or not, so unlike
	// Est.Plan it must not be context-gated into permanent grey-out.
	if buttons[meta].Enabled != nil && !buttons[meta].Enabled() {
		t.Error("the Meta button should not be disabled with no query panel open")
	}
}

// TestQueryMenuOutputColumnMetaItem pins the Query menu's metadata toggle:
// it sits in the same group as Actual Execution Plan and Live Query
// Statistics (no divider between them) and is a Checked toggle.
func TestQueryMenuOutputColumnMetaItem(t *testing.T) {
	a := newTestApp()
	items := queryMenuItems(t, a.buildMenus())
	act, live, meta := -1, -1, -1
	for i, it := range items {
		switch it.Label {
		case "Actual Execution Plan":
			act = i
		case "Live Query Statistics":
			live = i
		case "Output Column Metadata":
			meta = i
		}
	}
	if act < 0 || live < 0 || meta < 0 {
		t.Fatalf("Query menu missing an item: Actual Execution Plan=%d, Live Query Statistics=%d, Output Column Metadata=%d", act, live, meta)
	}
	if live != act+1 || meta != live+1 {
		t.Errorf("Actual Execution Plan at %d, Live Query Statistics at %d, Output Column Metadata at %d; want consecutive, same group", act, live, meta)
	}
	if items[meta].Action == nil {
		t.Fatal("the Output Column Metadata item has no action — it would silently do nothing")
	}
}

// TestQueryTogglesFollowStateWithoutRebuild: the five menu toggles and three
// toolbar toggles read their state at draw time (MenuItem.Checked,
// ToolbarButton.On), so a menu and toolbar built once show every flip. They
// used to fold ON/OFF into the label and needed a rebuild after each toggle;
// a toggle that forgot it showed a stale state.
func TestQueryTogglesFollowStateWithoutRebuild(t *testing.T) {
	a := newTestApp()
	menus, buttons := a.buildMenus(), a.buildToolbar()
	type toggle struct {
		menu, label, icon string
		set               func(bool)
	}
	toggles := []toggle{
		{"Edit", "Word Wrap", "", func(on bool) { a.wordWrap = on }},
		{"View", "Group by Schema", "", func(on bool) { a.groupBySchema = on }},
		{"Query", "Actual Execution Plan", "Act.Plan", func(on bool) { a.actualPlanEnabled = on }},
		{"Query", "Live Query Statistics", "Live", func(on bool) { a.liveStatsEnabled = on }},
		{"Query", "Output Column Metadata", "Meta", func(on bool) { a.metaEnabled = on }},
	}
	for _, tg := range toggles {
		item := findMenuItem(findMenu(t, menus, tg.menu).Items, tg.label)
		if item == nil || item.Checked == nil {
			t.Fatalf("%s > %s is not a Checked toggle", tg.menu, tg.label)
		}
		var button *controls.ToolbarButton
		if tg.icon != "" {
			for i := range buttons {
				if buttons[i].Icon == tg.icon {
					button = &buttons[i]
				}
			}
			if button == nil || button.On == nil {
				t.Fatalf("toolbar %q is not an On toggle", tg.icon)
			}
		}
		for _, on := range []bool{true, false} {
			tg.set(on)
			if item.Checked() != on {
				t.Errorf("%s: Checked()=%v after setting %v", tg.label, !on, on)
			}
			if button != nil && button.On() != on {
				t.Errorf("%s: On()=%v after setting %v", tg.icon, !on, on)
			}
		}
	}
}

// queryMenuItems returns the Query menu's items from a buildMenus() result.
func queryMenuItems(t *testing.T, menus []controls.Menu) []controls.MenuItem {
	t.Helper()
	for _, m := range menus {
		if m.Label == "Query" {
			return m.Items
		}
	}
	t.Fatal("no Query menu in buildMenus()")
	return nil
}
