package planview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func loadTestPlan(t *testing.T) *showplan.Plan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "showplan", "testdata", "actual_plan.sqlplan"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	p, err := showplan.Parse(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return p
}

func keyRune(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone)
}

func TestNew_EmptyState(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	if v.Plan() != nil {
		t.Error("Plan() should be nil before any load")
	}
	if v.HasSelection() {
		t.Error("HasSelection() should be false with no plan loaded")
	}
}

func TestSetPlanXML_InvalidReportsError(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	if err := v.SetPlanXML("not xml at all"); err == nil {
		t.Fatal("SetPlanXML(garbage) returned nil error")
	}
	if v.Plan() != nil {
		t.Error("Plan() should stay nil after a failed SetPlanXML")
	}
}

func TestSetPlan_InstallsAndDefaultsToPlanTab(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	if v.Plan() == nil {
		t.Fatal("Plan() is nil after SetPlan")
	}
	if v.activeTab != TabPlan {
		t.Errorf("activeTab = %v, want TabPlan", v.activeTab)
	}
	// The fixture is a single-statement plan, so no statement bar.
	if v.stmtRect.H != 0 {
		t.Errorf("stmtRect.H = %d, want 0 for a single-statement plan", v.stmtRect.H)
	}
}

func TestHandleKey_TabSwitching(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))

	if !v.HandleKey(keyRune('3')) {
		t.Fatal("HandleKey('3') returned false")
	}
	if v.activeTab != TabXML {
		t.Errorf("activeTab = %v, want TabXML after '3'", v.activeTab)
	}
	if !v.HandleKey(keyRune('2')) {
		t.Fatal("HandleKey('2') returned false")
	}
	if v.activeTab != TabTree {
		t.Errorf("activeTab = %v, want TabTree after '2'", v.activeTab)
	}
	if !v.HandleKey(keyRune('1')) {
		t.Fatal("HandleKey('1') returned false")
	}
	if v.activeTab != TabPlan {
		t.Errorf("activeTab = %v, want TabPlan after '1'", v.activeTab)
	}
}

func TestHandleKey_UnhandledReturnsFalse(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	// On the Plan tab (placeholder, no XML editor routing), an arbitrary
	// key must be refused so a host can route it elsewhere — see the
	// keyboard-conventions rule (tuikit widgets must return false for keys
	// they don't act on).
	if v.HandleKey(keyRune('z')) {
		t.Error("HandleKey('z') on the Plan tab returned true, want false")
	}
}

func TestXMLTab_SelectionAndCopy(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabXML)
	v.SetActive(true)

	v.xml.SelectAll()
	if !v.HasSelection() {
		t.Fatal("HasSelection() = false after SelectAll on the XML tab")
	}
	text := v.SelectedText()
	if text == "" {
		t.Error("SelectedText() is empty after SelectAll")
	}
	if v.Cut() != text {
		t.Error("Cut() should return the same text as SelectedText() (read-only view)")
	}

	// The XML tab's own text selection must not leak into the Plan tab's
	// notion of "selection" — the Plan tab has a legitimate selection of
	// its own (the highlighted operator node), reported as that node's
	// details rather than the XML text.
	v.setActiveTab(TabPlan)
	if !v.HasSelection() {
		t.Error("HasSelection() = false on the Plan tab with a selected operator, want true")
	}
	if planText := v.SelectedText(); planText == text || planText == "" {
		t.Errorf("SelectedText() on the Plan tab = %q, want the selected operator's details, not the XML tab's leftover text", planText)
	}
}

func TestStatementSelector_MultiStatement(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	plan := loadTestPlan(t)
	// Synthesize a second statement to exercise the multi-statement path
	// without needing a second real fixture.
	plan.Statements = append(plan.Statements, plan.Statements[0])
	v.SetPlan(plan)

	if v.stmtRect.H != 1 {
		t.Fatalf("stmtRect.H = %d, want 1 for a multi-statement plan", v.stmtRect.H)
	}
	if v.stmtIdx != 0 {
		t.Fatalf("stmtIdx = %d, want 0 right after load", v.stmtIdx)
	}
	if !v.HandleKey(keyRune(']')) {
		t.Fatal("HandleKey(']') returned false")
	}
	if v.stmtIdx != 1 {
		t.Errorf("stmtIdx = %d, want 1 after ']'", v.stmtIdx)
	}
	if !v.HandleKey(keyRune(']')) {
		t.Fatal("HandleKey(']') returned false")
	}
	if v.stmtIdx != 0 {
		t.Errorf("stmtIdx = %d, want 0 after wrapping past the last statement", v.stmtIdx)
	}
}

func TestExpandButton_ClickNoopWithoutCallback(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	// expandBtnRect is zero until Draw runs (untested here — tcell v3 has
	// no SimulationScreen, so Draw itself isn't exercised by any test in
	// this codebase; see controls/editor_test.go for the same state-only
	// convention). Clicking where the button would be must still be a
	// harmless tab-bar click, not a panic, with OnExpand nil.
	if v.HandleMouse(newClick(1, 0)) == false {
		t.Fatal("HandleMouse on the tab bar row returned false, want true (tab click)")
	}
}

// TestExpandButton_ClickFiresCallback checks a click landing inside
// expandBtnRect calls OnExpand exactly once — expandBtnRect is normally
// only populated by drawTabBar (see the no-SimulationScreen note above),
// so it's set directly here to simulate the post-Draw state.
func TestExpandButton_ClickFiresCallback(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	v.expandBtnRect = core.Rect{X: 60, Y: 0, W: 10, H: 1}
	calls := 0
	v.OnExpand = func() { calls++ }

	if !v.HandleMouse(newClick(62, 0)) {
		t.Fatal("HandleMouse inside expandBtnRect returned false, want true")
	}
	if calls != 1 {
		t.Errorf("OnExpand called %d times, want 1", calls)
	}
}

func newClick(x, y int) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
}

// twoStatementPlanXML is a batch of two statements whose NodeIds overlap, as
// SQL Server numbers them: per statement, from 0. Node 2 exists only in the
// second statement, under node 1.
const twoStatementPlanXML = `<?xml version="1.0"?>
<ShowPlanXML xmlns="http://schemas.microsoft.com/sqlserver/2004/07/showplan" Version="1.539" Build="16.0.1000.6">
 <BatchSequence><Batch><Statements>
  <StmtSimple StatementText="SELECT 1" StatementType="SELECT" StatementSubTreeCost="1">
   <QueryPlan DegreeOfParallelism="1">
    <RelOp NodeId="0" PhysicalOp="Nested Loops" LogicalOp="Inner Join" EstimateRows="1" EstimatedTotalSubtreeCost="1">
     <NestedLoops>
      <RelOp NodeId="1" PhysicalOp="Index Seek" LogicalOp="Index Seek" EstimateRows="1" EstimatedTotalSubtreeCost="0.5">
       <IndexScan><Object Database="[appdb]" Schema="[dbo]" Table="[orders]" Index="[IX_date]"/></IndexScan>
      </RelOp>
     </NestedLoops>
    </RelOp>
   </QueryPlan>
  </StmtSimple>
  <StmtSimple StatementText="SELECT 2" StatementType="SELECT" StatementSubTreeCost="3">
   <QueryPlan DegreeOfParallelism="1">
    <RelOp NodeId="0" PhysicalOp="Sort" LogicalOp="Sort" EstimateRows="9" EstimatedTotalSubtreeCost="3">
     <Sort>
      <RelOp NodeId="1" PhysicalOp="Hash Match" LogicalOp="Aggregate" EstimateRows="9" EstimatedTotalSubtreeCost="2">
       <Hash>
        <RelOp NodeId="2" PhysicalOp="Clustered Index Scan" LogicalOp="Clustered Index Scan" EstimateRows="90" EstimatedTotalSubtreeCost="1">
         <IndexScan><Object Database="[appdb]" Schema="[dbo]" Table="[lines]" Index="[PK_lines]"/></IndexScan>
        </RelOp>
       </Hash>
      </RelOp>
     </Sort>
    </RelOp>
   </QueryPlan>
  </StmtSimple>
 </Statements></Batch></BatchSequence>
</ShowPlanXML>`

// TestSelectNodeSwitchesStatementAndRevealsTheOperator. SelectNode is how a host
// opens a plan at an operator, so it has to land on the right statement (NodeIds
// repeat across a batch) and show the operator in the Tree tab even when an
// ancestor is collapsed — otherwise the details pane names it and the tree
// highlights nothing.
func TestSelectNodeSwitchesStatementAndRevealsTheOperator(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 100, 30)
	if err := v.SetPlanXML(twoStatementPlanXML); err != nil {
		t.Fatal(err)
	}
	if len(v.Plan().Statements) != 2 {
		t.Fatalf("fixture parsed to %d statements, want 2", len(v.Plan().Statements))
	}
	v.setActiveTab(TabTree)
	v.treeSt.collapsed[1] = true // statement 2's Hash Match, the scan's parent

	if !v.SelectNode(1, 2) {
		t.Fatal("SelectNode(1, 2) = false, want the second statement's scan selected")
	}
	if v.stmtIdx != 1 {
		t.Errorf("stmtIdx = %d, want 1", v.stmtIdx)
	}
	if n := v.selectedNode(); n == nil || n.PhysicalOp != "Clustered Index Scan" {
		t.Errorf("selected %v, want the Clustered Index Scan", n)
	}
	if v.treeSt.collapsed[1] {
		t.Error("the scan's collapsed parent was left collapsed, so the Tree tab cannot show it")
	}
	found := false
	for _, r := range v.treeSt.rows {
		found = found || r.node.ID == 2
	}
	if !found {
		t.Error("the Tree tab's rows do not contain the selected operator")
	}
}

// TestSelectNodeRefusesAnOperatorTheStatementLacks — and changes nothing, so a
// stale (statement, node) pair cannot move the view to an unrelated operator.
func TestSelectNodeRefusesAnOperatorTheStatementLacks(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 100, 30)
	if err := v.SetPlanXML(twoStatementPlanXML); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ stmt, id int }{{0, 2}, {2, 0}, {-1, 0}} {
		if v.SelectNode(c.stmt, c.id) {
			t.Errorf("SelectNode(%d, %d) = true, want false", c.stmt, c.id)
		}
		if v.stmtIdx != 0 || v.selectedID != 0 {
			t.Errorf("SelectNode(%d, %d) moved the view to statement %d node %d",
				c.stmt, c.id, v.stmtIdx, v.selectedID)
		}
	}
	if New().SelectNode(0, 0) {
		t.Error("SelectNode on an empty view = true")
	}
}

// A right-click on the operators opens the host's plan menu once per press —
// a held button resends Button2 on every motion — and nowhere else: the XML
// tab's right-click is the editor's.
func TestRightClickOnOperatorsOpensTheHostMenuOncePerPress(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 100, 30)
	v.SetPlan(loadTestPlan(t))
	var at []core.Rect
	v.OnContextMenu = func(x, y int) { at = append(at, core.Rect{X: x, Y: y}) }

	c := v.graphCanvasRect
	press := tcell.NewEventMouse(c.X+2, c.Y+1, tcell.Button2, tcell.ModNone)
	v.HandleMouse(press)
	v.HandleMouse(tcell.NewEventMouse(c.X+3, c.Y+1, tcell.Button2, tcell.ModNone)) // held, moved
	v.HandleMouse(tcell.NewEventMouse(c.X+3, c.Y+1, tcell.ButtonNone, tcell.ModNone))
	if len(at) != 1 || at[0].X != c.X+2 || at[0].Y != c.Y+1 {
		t.Fatalf("plan tab: OnContextMenu calls = %v, want one at the press", at)
	}

	v.setActiveTab(TabTree)
	v.HandleMouse(tcell.NewEventMouse(v.treePaneRect.X+1, v.treePaneRect.Y, tcell.Button2, tcell.ModNone))
	v.HandleMouse(tcell.NewEventMouse(v.treePaneRect.X+1, v.treePaneRect.Y, tcell.ButtonNone, tcell.ModNone))
	if len(at) != 2 {
		t.Fatalf("tree tab: OnContextMenu calls = %d, want 2", len(at))
	}

	v.setActiveTab(TabXML)
	v.HandleMouse(tcell.NewEventMouse(v.contentRect.X+1, v.contentRect.Y+1, tcell.Button2, tcell.ModNone))
	if len(at) != 2 {
		t.Error("a right-click in the XML tab opened the plan menu")
	}
}
