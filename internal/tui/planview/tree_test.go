package planview

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/tuikittest"
)

func namedKey(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, "", tcell.ModNone) }

func newTreeTabView(t *testing.T) *PlanView {
	t.Helper()
	v := New()
	v.SetBounds(0, 0, 120, 40)
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabTree)
	return v
}

// TestTreeScrollbarDragScrolls confirms dragging the tree pane's scrollbar
// scrolls it instead of being read as a click that selects whatever row
// sits under it.
func TestTreeScrollbarDragScrolls(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 120, 8) // short enough that all 12 rows can't fit
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabTree)

	r := v.treePaneRect
	if len(v.treeSt.rows) <= r.H {
		t.Fatalf("test needs more tree rows than treePaneRect.H (%d) to exercise scrolling, got %d rows", r.H, len(v.treeSt.rows))
	}
	selBefore := v.selectedID

	sbX := r.Right() - 1
	if !v.HandleMouse(tcell.NewEventMouse(sbX, r.Y+r.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("handling a click on the tree pane's scrollbar column should return true")
	}
	if z, held := v.drag.Owner(); !held || z != pzTreeBar {
		t.Fatalf("drag owner = %v (held %v) after pressing on the scrollbar, want pzTreeBar", z, held)
	}
	if v.treeSt.scroll == 0 {
		t.Error("treeSt.scroll should have jumped forward when clicking near the bottom of the track")
	}
	if v.selectedID != selBefore {
		t.Errorf("selectedID changed, clicking the scrollbar must not select a node")
	}

	// Held, the bar keeps the drag across the rows and off the pane: back to
	// the top over the row column, then below the view to the end.
	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y, tcell.Button1, tcell.ModNone))
	if v.treeSt.scroll != 0 {
		t.Errorf("dragging the thumb to the top over the rows left scroll = %d, want 0", v.treeSt.scroll)
	}
	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Bottom()+5, tcell.Button1, tcell.ModNone))
	if want := len(v.treeSt.rows) - r.H; v.treeSt.scroll != want {
		t.Errorf("dragging the thumb below the pane left scroll = %d, want %d", v.treeSt.scroll, want)
	}
	if v.selectedID != selBefore {
		t.Errorf("selectedID changed, dragging the thumb over the rows must not select a node")
	}

	v.HandleMouse(tcell.NewEventMouse(sbX, r.Y, tcell.ButtonNone, tcell.ModNone))
	if _, held := v.drag.Owner(); held {
		t.Error("the gesture should end on release")
	}
}

// A collapse that lets every row fit scrolls back to the top: clamping only
// to len(rows) left the first row hidden with no scrollbar drawn.
func TestTreeCollapseClampsScroll(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 120, 8)
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabTree)
	if len(v.treeSt.rows) <= v.treePaneRect.H {
		t.Fatalf("test needs more tree rows (%d) than treePaneRect.H (%d)", len(v.treeSt.rows), v.treePaneRect.H)
	}
	v.selectNode(v.currentStatement().Root.ID)
	v.treeSt.scroll = 1 // selectNode may scroll the root into view
	if !v.collapseSelected() {
		t.Fatal("collapsing the root should collapse something")
	}
	if len(v.treeSt.rows) > v.treePaneRect.H {
		t.Fatalf("after collapsing the root %d rows remain, want them all to fit in %d", len(v.treeSt.rows), v.treePaneRect.H)
	}
	if v.treeSt.scroll != 0 {
		t.Errorf("scroll = %d after a collapse that fits every row, want 0", v.treeSt.scroll)
	}
}

// A press on a row, held and dragged onto the scrollbar column, is still the
// row's: it must not grab the thumb and jump the scroll.
func TestTreeRowDragOntoScrollbarDoesNotScroll(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 120, 8)
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabTree)
	r := v.treePaneRect
	if len(v.treeSt.rows) <= r.H {
		t.Fatalf("test needs more tree rows (%d) than treePaneRect.H (%d)", len(v.treeSt.rows), r.H)
	}

	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y+1, tcell.Button1, tcell.ModNone))
	sel := v.selectedID
	v.HandleMouse(tcell.NewEventMouse(r.Right()-1, r.Y+r.H-1, tcell.Button1, tcell.ModNone))
	if v.treeSt.scroll != 0 {
		t.Errorf("a row press dragged onto the scrollbar scrolled to %d, want 0", v.treeSt.scroll)
	}
	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y+3, tcell.Button1, tcell.ModNone))
	if v.selectedID != sel {
		t.Errorf("a row press dragged over another row selected %d, want %d kept", v.selectedID, sel)
	}
}

// A drag that began in the operator summary grid selects no tree row when it
// enters the tree pane.
func TestSummaryDragIntoTreeSelectsNoRow(t *testing.T) {
	v := newSummaryTabView(t)
	b, r := v.bottomRect, v.treePaneRect
	sel := v.selectedID

	v.HandleMouse(tcell.NewEventMouse(b.X+2, b.Y+summaryDataRowY, tcell.Button1, tcell.ModNone))
	if z, held := v.drag.Owner(); !held || z != pzSummary {
		t.Fatalf("drag owner = %v (held %v) after pressing in the summary grid, want pzSummary", z, held)
	}
	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y+3, tcell.Button1, tcell.ModNone))
	if v.selectedID != sel {
		t.Errorf("a summary drag entering the tree selected %d, want %d kept", v.selectedID, sel)
	}
	if !v.bottomFocused {
		t.Error("a summary drag entering the tree moved focus to the tree")
	}
	v.HandleMouse(tcell.NewEventMouse(r.X+2, r.Y+3, tcell.ButtonNone, tcell.ModNone))
}

func TestTree_DefaultSelectionIsRoot(t *testing.T) {
	v := newTreeTabView(t)
	root := v.currentStatement().Root
	if v.selectedID != root.ID {
		t.Errorf("selectedID = %d, want root ID %d", v.selectedID, root.ID)
	}
	if len(v.treeSt.rows) != 12 {
		t.Fatalf("len(treeSt.rows) = %d, want 12 (fully expanded)", len(v.treeSt.rows))
	}
}

// TestTreeRowText_CostBadgeTakesPriorityOverWarning checks the 🔴 (expensive
// operator, cost >= expensiveCostThreshold) badge wins over ⚠ (warning) when
// both apply, the same priority the border/text color switches already use.
func TestTreeRowText_CostBadgeTakesPriorityOverWarning(t *testing.T) {
	v := newTreeTabView(t)
	st := v.currentStatement()
	n := &showplan.Node{
		ID:             9001,
		PhysicalOp:     "Expensive Scan",
		EstSubtreeCost: st.SubTreeCost, // 100% of the statement, well over the threshold
		Warnings:       []string{"Missing Index"},
	}
	text := v.treeRowText(treeRow{node: n})
	if !strings.Contains(text, "🔴") {
		t.Errorf("treeRowText = %q, want a 🔴 badge for a node over expensiveCostThreshold", text)
	}
	if strings.Contains(text, "⚠") {
		t.Errorf("treeRowText = %q, should not also show ⚠ when 🔴 applies", text)
	}
}

// TestTreeRowText_WarningBadgeWhenNotExpensive checks ⚠ still shows for a
// warned node that isn't over the cost threshold.
func TestTreeRowText_WarningBadgeWhenNotExpensive(t *testing.T) {
	v := newTreeTabView(t)
	v.currentStatement().SubTreeCost = 100
	n := &showplan.Node{ID: 9002, PhysicalOp: "Cheap Scan", Warnings: []string{"Missing Index"}}
	v.treeSt.rows = []treeRow{{node: n}}
	v.selectedID = -1
	for _, cost := range []float64{0, 20, 40, 79.4} {
		n.EstSubtreeCost = cost
		text := v.treeRowText(treeRow{node: n})
		if !strings.Contains(text, "⚠") {
			t.Errorf("treeRowText = %q, want a ⚠ badge for a warned, inexpensive node", text)
		}
		if strings.ContainsAny(text, "🔴🟠🟡") {
			t.Errorf("treeRowText = %q, should not show cost badges instead of a warning", text)
		}
		s := tuikittest.NewScreen(120, 40)
		v.drawTreePane(s)
		if _, style, _ := s.Get(v.treePaneRect.X, v.treePaneRect.Y); style.GetForeground() != theme.Active().Warning {
			t.Errorf("cost %.2f: row color = %v, want Warning", cost, style.GetForeground())
		}
	}
}

func TestTreeRowText_CostThresholds(t *testing.T) {
	v := newTreeTabView(t)
	v.currentStatement().SubTreeCost = 100
	v.selectedID = -1
	s := tuikittest.NewScreen(120, 40)
	for _, tt := range []struct {
		cost float64
		want string
	}{{19.4, ""}, {19.6, "🟡"}, {39.4, "🟡"}, {39.6, "🟠"}, {79.4, "🟠"}, {79.6, "🔴"}, {80, "🔴"}} {
		n := &showplan.Node{ID: 9001, PhysicalOp: "Scan", EstSubtreeCost: tt.cost}
		text := v.treeRowText(treeRow{node: n})
		for _, badge := range []string{"🔴", "🟠", "🟡"} {
			if strings.Contains(text, badge) != (badge == tt.want) {
				t.Errorf("cost %.2f: treeRowText = %q, want badge %q", tt.cost, text, tt.want)
			}
		}
		v.treeSt.rows = []treeRow{{node: n}}
		v.drawTreePane(s)
		want := theme.Active().Text
		if tt.want == "🔴" {
			want = theme.Active().Error
		}
		if _, style, _ := s.Get(v.treePaneRect.X, v.treePaneRect.Y); style.GetForeground() != want {
			t.Errorf("cost %.2f: row color = %v, want %v", tt.cost, style.GetForeground(), want)
		}
	}
}

// TestTreeRowText_ParallelIcon checks ⇄ shows for a parallel operator,
// independent of the cost/warning badge.
func TestTreeRowText_ParallelIcon(t *testing.T) {
	v := newTreeTabView(t)
	n := &showplan.Node{ID: 9003, PhysicalOp: "Gather Streams", Parallel: true}
	text := v.treeRowText(treeRow{node: n})
	if !strings.Contains(text, "⇄") {
		t.Errorf("treeRowText = %q, want a ⇄ parallelism icon", text)
	}
}

func TestTree_ArrowNavigationMovesSelection(t *testing.T) {
	v := newTreeTabView(t)
	start := v.selectedID
	if !v.HandleKey(namedKey(tcell.KeyDown)) {
		t.Fatal("HandleKey(Down) returned false")
	}
	if v.selectedID == start {
		t.Error("selectedID did not change after Down")
	}
	if !v.HandleKey(namedKey(tcell.KeyUp)) {
		t.Fatal("HandleKey(Up) returned false")
	}
	if v.selectedID != start {
		t.Errorf("selectedID = %d after Up, want back to %d", v.selectedID, start)
	}
}

func TestTree_CollapseHidesChildren(t *testing.T) {
	v := newTreeTabView(t)
	full := len(v.treeSt.rows)
	v.HandleKey(namedKey(tcell.KeyDown)) // select node 1 (Nested Loops, has children)
	v.HandleKey(namedKey(tcell.KeyLeft))
	if len(v.treeSt.rows) >= full {
		t.Errorf("row count = %d after collapsing a node with children, want fewer than %d", len(v.treeSt.rows), full)
	}
	v.HandleKey(namedKey(tcell.KeyRight))
	if len(v.treeSt.rows) != full {
		t.Errorf("row count = %d after re-expanding, want back to %d", len(v.treeSt.rows), full)
	}
}

func TestTree_BottomModeCycles(t *testing.T) {
	v := newTreeTabView(t)
	if v.bottomMode != bottomHidden {
		t.Fatalf("bottomMode = %v, want bottomHidden initially", v.bottomMode)
	}
	v.HandleKey(keyRune('o'))
	if v.bottomMode != bottomProperties {
		t.Errorf("bottomMode = %v, want bottomProperties after one 'o'", v.bottomMode)
	}
	v.HandleKey(keyRune('o'))
	if v.bottomMode != bottomSummary {
		t.Errorf("bottomMode = %v, want bottomSummary after two 'o'", v.bottomMode)
	}
	v.HandleKey(keyRune('o'))
	if v.bottomMode != bottomHidden {
		t.Errorf("bottomMode = %v, want bottomHidden after three 'o'", v.bottomMode)
	}
}

func TestSummary_SortAndJumpToTree(t *testing.T) {
	v := newTreeTabView(t)
	v.HandleKey(keyRune('o'))
	v.HandleKey(keyRune('o')) // bottomSummary

	v.HandleKey(keyRune('r')) // sort by rows
	if v.summarySt.sort != sortByRows {
		t.Fatalf("sort = %v, want sortByRows", v.summarySt.sort)
	}
	if len(v.summarySt.rows) != 12 {
		t.Fatalf("len(summarySt.rows) = %d, want 12", len(v.summarySt.rows))
	}
	for i := 1; i < len(v.summarySt.rows); i++ {
		if v.summarySt.rows[i-1].Rows() < v.summarySt.rows[i].Rows() {
			t.Fatalf("summary rows not sorted descending by rows at index %d", i)
		}
	}

	v.summarySt.grid.SetSelectedRow(0)
	if !v.HandleKey(namedKey(tcell.KeyTab)) {
		t.Fatal("HandleKey(Tab) returned false while the summary table is shown")
	}
	if !v.bottomFocused {
		t.Fatal("bottomFocused = false after Tab, want true")
	}
	v.HandleKey(namedKey(tcell.KeyEnter))
	if v.selectedID != v.summarySt.rows[0].ID {
		t.Errorf("selectedID = %d after Enter, want %d (top summary row)", v.selectedID, v.summarySt.rows[0].ID)
	}
	if v.bottomFocused {
		t.Error("bottomFocused should return to false after Enter jumps to the tree")
	}
}

func TestTree_HasSelectionAndSelectedText(t *testing.T) {
	v := newTreeTabView(t)
	if !v.HasSelection() {
		t.Fatal("HasSelection() = false on the Tree tab with a selected root")
	}
	text := v.SelectedText()
	if !strings.Contains(text, "Physical Operator") || !strings.Contains(text, "Top") {
		t.Errorf("SelectedText() = %q, want it to mention the selected operator's type", text)
	}
}

// TestSplitter_ClickAfterDragSelectsTree pins down that handleTreeTabMouse
// forwards the release (ButtonNone) event to treeSplit.HandleMouse: without
// it layout.Splitter's "dragging" flag stays stuck true after a divider
// drag, and the next plain click anywhere in the tab is misread as a drag
// continuation, moving the divider instead of selecting the clicked row.
func TestSplitter_ClickAfterDragSelectsTree(t *testing.T) {
	v := newTreeTabView(t)
	r0 := v.treeSplit.Ratio()

	pos, barY := v.treeSplit.SplitPos(), v.treePaneRect.Y
	v.HandleMouse(tcell.NewEventMouse(pos, barY, tcell.Button1, tcell.ModNone))       // press on the bar
	v.HandleMouse(tcell.NewEventMouse(pos+10, barY, tcell.Button1, tcell.ModNone))    // drag
	v.HandleMouse(tcell.NewEventMouse(pos+10, barY, tcell.ButtonNone, tcell.ModNone)) // release
	r1 := v.treeSplit.Ratio()
	if r1 == r0 {
		t.Fatal("ratio unchanged after a drag — test setup didn't actually drag the splitter")
	}

	// A plain click well inside the tree pane, away from the bar, at the row
	// that should select treeSt.rows[3].
	row := 3
	clickY := v.treePaneRect.Y + row
	v.HandleMouse(tcell.NewEventMouse(2, clickY, tcell.Button1, tcell.ModNone))

	if got := v.treeSplit.Ratio(); got != r1 {
		t.Errorf("ratio changed to %v after a plain tree click, want unchanged %v — splitter is still eating clicks", got, r1)
	}
	want := v.treeSt.rows[row].node.ID
	if v.selectedID != want {
		t.Errorf("selectedID = %d after clicking tree row %d, want %d — click was swallowed by the splitter", v.selectedID, row, want)
	}
}

// A press on a tree row belongs to that press alone. All-motion tracking
// resends Button1 on every motion while the button is down, and the row click
// toggles: a press held over the selected row expanded and collapsed it once
// per resent event, and a press on any other row selected it and then toggled
// it on the very next one — so a click that so much as twitched collapsed
// whatever it had just selected.
func TestTreeRowClickIsOnePerPhysicalPress(t *testing.T) {
	v := newTreeTabView(t)
	r := v.treePaneRect

	// Row 1 is a child of the root, which starts selected and expanded.
	target := v.treeSt.rows[1].node
	y := r.Y + 1
	collapsedBefore := v.treeSt.collapsed[target.ID]

	v.HandleMouse(tcell.NewEventMouse(r.X+2, y, tcell.Button1, tcell.ModNone))
	if v.selectedID != target.ID {
		t.Fatalf("selectedID = %d after clicking row 1, want %d", v.selectedID, target.ID)
	}
	for range 3 {
		v.HandleMouse(tcell.NewEventMouse(r.X+2, y, tcell.Button1, tcell.ModNone))
	}
	if v.treeSt.collapsed[target.ID] != collapsedBefore {
		t.Errorf("holding the press over row 1 toggled its expand state; collapsed = %v, want %v",
			v.treeSt.collapsed[target.ID], collapsedBefore)
	}

	// After the release the next press is a fresh one, and on the row that
	// is now selected it toggles — exactly once.
	v.HandleMouse(tcell.NewEventMouse(r.X+2, y, tcell.ButtonNone, tcell.ModNone))
	v.HandleMouse(tcell.NewEventMouse(r.X+2, y, tcell.Button1, tcell.ModNone))
	if v.treeSt.collapsed[target.ID] == collapsedBefore {
		t.Errorf("a fresh press on the selected row did not toggle it; collapsed = %v",
			v.treeSt.collapsed[target.ID])
	}
	for range 3 {
		v.HandleMouse(tcell.NewEventMouse(r.X+2, y, tcell.Button1, tcell.ModNone))
	}
	if v.treeSt.collapsed[target.ID] == collapsedBefore {
		t.Errorf("holding that press toggled row 1 back; collapsed = %v, want it left toggled",
			v.treeSt.collapsed[target.ID])
	}
}

// TestTree_LeftJumpsToParent pins Explorer-style Left in the operator tree: a
// leaf moves to its parent, an expanded operator collapses in place, and a
// collapsed one moves to its parent in turn.
func TestTree_LeftJumpsToParent(t *testing.T) {
	v := newTreeTabView(t)
	leaf := -1
	for i, r := range v.treeSt.rows {
		if r.depth >= 2 && len(r.node.Children) == 0 {
			leaf = i
			break
		}
	}
	if leaf < 0 {
		t.Fatal("test plan needs a leaf operator at depth >= 2")
	}
	parentOf := func(i int) int {
		for j := i - 1; j >= 0; j-- {
			if v.treeSt.rows[j].depth < v.treeSt.rows[i].depth {
				return v.treeSt.rows[j].node.ID
			}
		}
		return -1
	}
	parent := parentOf(leaf)
	v.selectNode(v.treeSt.rows[leaf].node.ID)

	v.HandleKey(namedKey(tcell.KeyLeft))
	if v.selectedID != parent {
		t.Fatalf("Left on a leaf: selectedID = %d, want parent %d", v.selectedID, parent)
	}
	rows := len(v.treeSt.rows)
	v.HandleKey(namedKey(tcell.KeyLeft))
	if v.selectedID != parent || !v.treeSt.collapsed[parent] || len(v.treeSt.rows) >= rows {
		t.Fatalf("Left on expanded parent: selectedID = %d, collapsed = %v; want it collapsed in place",
			v.selectedID, v.treeSt.collapsed[parent])
	}
	grand := parentOf(v.selectedRowIndex())
	v.HandleKey(namedKey(tcell.KeyLeft))
	if v.selectedID != grand {
		t.Errorf("Left on collapsed parent: selectedID = %d, want grandparent %d", v.selectedID, grand)
	}
}
