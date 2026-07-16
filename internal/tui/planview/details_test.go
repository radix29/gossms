package planview

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func TestDetailLines_AlignedAndCurated(t *testing.T) {
	v := newTreeTabView(t)
	lines := detailLines(v.selectedNode(), v.currentStatement())
	if len(lines) == 0 {
		t.Fatal("detailLines returned nothing for the selected root")
	}

	col := strings.Index(lines[0], " : ")
	if col < 0 {
		t.Fatalf("line %q has no \" : \" separator", lines[0])
	}
	for _, l := range lines {
		if i := strings.Index(l, " : "); i != col {
			t.Errorf("line %q separator at column %d, want %d (labels must line up)", l, i, col)
		}
	}

	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Physical Operator", "Estimated Rows", "Warnings"} {
		if !strings.Contains(joined, want) {
			t.Errorf("detailLines missing expected label %q; got:\n%s", want, joined)
		}
	}
}

// TestDetailKVs_IncludesCostPercent checks Cost % — folded in from the
// Plan tab's now-removed Operator Summary table — appears in the shared
// Properties field list for both tabs.
func TestDetailKVs_IncludesCostPercent(t *testing.T) {
	v := newTreeTabView(t)
	lines := detailLines(v.selectedNode(), v.currentStatement())
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Cost %") {
		t.Errorf("detailLines missing \"Cost %%\"; got:\n%s", joined)
	}
}

func TestDetailLines_NilNode(t *testing.T) {
	if lines := detailLines(nil, nil); lines != nil {
		t.Errorf("detailLines(nil, nil) = %v, want nil", lines)
	}
}

func TestDetailsPane_WheelScrolls(t *testing.T) {
	v := newTreeTabView(t)
	v.SetBounds(0, 0, 120, 8) // short enough that the details pane can't fit every line

	total := len(detailLines(v.selectedNode(), v.currentStatement()))
	if total <= v.detailsContentRect.H {
		t.Fatalf("detail lines (%d) fit entirely in the pane (%d rows) — test needs an overflow to be meaningful", total, v.detailsContentRect.H)
	}

	mx, my := v.detailsPaneRect.X+1, v.detailsPaneRect.Y+1
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelDown) over the details pane returned false")
	}
	if v.detailsScroll != 1 {
		t.Errorf("detailsScroll = %d after WheelDown, want 1", v.detailsScroll)
	}
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelUp, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelUp) over the details pane returned false")
	}
	if v.detailsScroll != 0 {
		t.Errorf("detailsScroll = %d after WheelUp, want back to 0", v.detailsScroll)
	}
}

// TestNodePropsForDisplay_PrependsWarnings checks the bottom Properties
// section's own row list — separate from n.Props, which mirrors the plan
// XML's attributes literally and never contains Warnings (decodeWarnings
// consumes the <Warnings> element into n.Warnings instead) — surfaces a
// node's warning text as its first row.
func TestNodePropsForDisplay_PrependsWarnings(t *testing.T) {
	v := newTreeTabView(t)
	n := v.currentStatement().Nodes()[5]
	n.Warnings = []string{"Test Warning"}

	kvs := nodePropsForDisplay(n)
	if len(kvs) != len(n.Props)+1 {
		t.Fatalf("nodePropsForDisplay returned %d rows, want len(Props)+1 = %d", len(kvs), len(n.Props)+1)
	}
	if kvs[0].Key != "Warnings" || kvs[0].Value != "Test Warning" {
		t.Errorf("nodePropsForDisplay[0] = %+v, want {Warnings Test Warning}", kvs[0])
	}
}

// TestNodePropsForDisplay_NoWarningsIsJustProps checks a warning-free node
// gets exactly n.Props back, unmodified — no synthetic row, no copy.
func TestNodePropsForDisplay_NoWarningsIsJustProps(t *testing.T) {
	v := newTreeTabView(t)
	n := v.currentStatement().Nodes()[5]
	n.Warnings = nil

	kvs := nodePropsForDisplay(n)
	if len(kvs) != len(n.Props) {
		t.Fatalf("nodePropsForDisplay returned %d rows, want len(Props) = %d", len(kvs), len(n.Props))
	}
	for _, kv := range kvs {
		if kv.Key == "Warnings" {
			t.Errorf("nodePropsForDisplay includes a Warnings row for a node with none: %+v", kvs)
		}
	}
}

func TestNodePropsForDisplay_NilNode(t *testing.T) {
	if kvs := nodePropsForDisplay(nil); kvs != nil {
		t.Errorf("nodePropsForDisplay(nil) = %v, want nil", kvs)
	}
}

// TestBottomProperties_WheelScrolls checks the bottom Properties section's
// scroll offset is actually reachable — propsSt.scroll existed as a field
// before this fix, but nothing ever wheel-scrolled it, so any node with
// more attributes than the fixed-height bottom section was permanently
// unreachable past the last visible row.
func TestBottomProperties_WheelScrolls(t *testing.T) {
	v := newTreeTabView(t)
	v.cycleBottomMode() // bottomHidden -> bottomProperties
	if v.bottomMode != bottomProperties {
		t.Fatalf("bottomMode = %v, want bottomProperties", v.bottomMode)
	}
	n := v.currentStatement().Nodes()[5]
	n.Warnings = []string{"Test Warning"}
	v.selectNode(n.ID)

	total := len(nodePropsForDisplay(v.selectedNode()))
	if total <= v.bottomRect.H {
		t.Fatalf("node has %d display rows, bottom section fits %d — test needs an overflow to be meaningful", total, v.bottomRect.H)
	}

	mx, my := v.bottomRect.X+1, v.bottomRect.Y+1
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelDown) over the bottom Properties section returned false")
	}
	if v.propsSt.scroll != 1 {
		t.Errorf("propsSt.scroll = %d after WheelDown, want 1", v.propsSt.scroll)
	}
	if !v.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelUp, tcell.ModNone)) {
		t.Fatal("HandleMouse(WheelUp) over the bottom Properties section returned false")
	}
	if v.propsSt.scroll != 0 {
		t.Errorf("propsSt.scroll = %d after WheelUp, want back to 0", v.propsSt.scroll)
	}
}

func TestDetailsHeaderText_ShowsScrollIndicatorOnlyWhenNeeded(t *testing.T) {
	if got := detailsHeaderText("Operator Details", 40, false, false); got != "Operator Details" {
		t.Errorf("detailsHeaderText(no overflow) = %q, want plain title", got)
	}
	if got := detailsHeaderText("Operator Details", 40, false, true); !strings.Contains(got, "▼") {
		t.Errorf("detailsHeaderText(canDown) = %q, want a ▼ indicator", got)
	}
	if got := detailsHeaderText("Operator Details", 40, true, false); !strings.Contains(got, "▲") {
		t.Errorf("detailsHeaderText(canUp) = %q, want a ▲ indicator", got)
	}
	if got := detailsHeaderText("Properties", 40, true, true); !strings.HasPrefix(got, "Properties") {
		t.Errorf("detailsHeaderText(custom title) = %q, want it to start with the given title", got)
	}
}
