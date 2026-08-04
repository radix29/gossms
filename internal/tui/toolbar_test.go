package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// TestMetaToggleIconStates checks the "Show Output Column Metadata" toggle's
// ON/OFF text is distinct and equal-width — equal width so toggling it never
// shifts the toolbar's other buttons (same rule as actualPlanToggleIcon).
func TestMetaToggleIconStates(t *testing.T) {
	off, on := metaToggleIcon(false), metaToggleIcon(true)
	if off != "Meta[-OFF]" || on != "Meta[ON--]" {
		t.Errorf("metaToggleIcon = %q (off) / %q (on)", off, on)
	}
	if len(off) != len(on) {
		t.Errorf("metaToggleIcon widths differ: off=%q (%d), on=%q (%d)", off, len(off), on, len(on))
	}
}

// TestBuildToolbarHasMetaAfterActualPlan pins the toolbar's order and each
// button's label: Meta sits immediately after Act.Plan, itself after
// Est.Plan, and both plan buttons keep their compact, space-free text.
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
	est, act, meta := idx("Est.Plan"), idx(actualPlanToggleIcon(false)), idx(metaToggleIcon(false))
	if est < 0 || act < 0 || meta < 0 {
		t.Fatalf("toolbar missing a button: Est.Plan=%d Act.Plan=%d Meta=%d", est, act, meta)
	}
	if act != est+1 || meta != act+1 {
		t.Errorf("toolbar order = Est.Plan@%d, Act.Plan@%d, Meta@%d; want Meta directly after Act.Plan, itself after Est.Plan", est, act, meta)
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
// it sits in the same group as Actual Execution Plan (no divider between
// them), carries its state in its label, and flips that label when toggled
// — the menu is rebuilt on toggle, so a stale label would be the symptom.
func TestQueryMenuOutputColumnMetaItem(t *testing.T) {
	if outputColumnMetaMenuLabel(false) == outputColumnMetaMenuLabel(true) {
		t.Fatalf("outputColumnMetaMenuLabel is the same in both states: %q", outputColumnMetaMenuLabel(false))
	}

	a := newTestApp()
	items := queryMenuItems(t, a.buildMenus())
	act, meta := -1, -1
	for i, it := range items {
		switch it.Label {
		case actualExecutionPlanMenuLabel(a.actualPlanEnabled):
			act = i
		case outputColumnMetaMenuLabel(a.metaEnabled):
			meta = i
		}
	}
	if act < 0 || meta < 0 {
		t.Fatalf("Query menu missing an item: Actual Execution Plan=%d, Output Column Metadata=%d", act, meta)
	}
	if meta != act+1 {
		t.Errorf("Output Column Metadata at %d, Actual Execution Plan at %d; want adjacent, same group", meta, act)
	}
	if items[meta].Divider {
		t.Error("the Output Column Metadata item is a divider")
	}

	if items[meta].Action == nil {
		t.Fatal("the Output Column Metadata item has no action — it would silently do nothing")
	}
	// toggleOutputColumnMeta needs a live screen (layoutAll), so drive the
	// state directly and rebuild: what this pins is that the label is derived
	// from metaEnabled at build time, not captured once at startup.
	a.metaEnabled = true
	if findMenuItem(queryMenuItems(t, a.buildMenus()), outputColumnMetaMenuLabel(true)) == nil {
		t.Errorf("with metaEnabled set, the Query menu has no %q item — the label did not follow the state", outputColumnMetaMenuLabel(true))
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
