package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
)

// comparePlanXML is a two-operator plan whose seek names index. Written out
// rather than built as a struct: the panel takes parsed plans, and a fixture
// that skipped the parser would not notice a change in what the parser fills.
func comparePlanXML(index string, estRows, cost float64) string {
	return `<?xml version="1.0"?>
<ShowPlanXML xmlns="http://schemas.microsoft.com/sqlserver/2004/07/showplan" Version="1.539" Build="16.0.1000.6">
 <BatchSequence><Batch><Statements>
  <StmtSimple StatementText="SELECT 1" StatementType="SELECT" StatementSubTreeCost="` + ftoa(cost) + `">
   <QueryPlan DegreeOfParallelism="1">
    <RelOp NodeId="0" PhysicalOp="Nested Loops" LogicalOp="Inner Join" EstimateRows="` + ftoa(estRows) + `" EstimatedTotalSubtreeCost="` + ftoa(cost) + `">
     <NestedLoops>
      <RelOp NodeId="1" PhysicalOp="Index Seek" LogicalOp="Index Seek" EstimateRows="` + ftoa(estRows) + `" EstimatedTotalSubtreeCost="` + ftoa(cost/2) + `">
       <IndexScan>
        <Object Database="[appdb]" Schema="[dbo]" Table="[orders]" Index="[` + index + `]"/>
       </IndexScan>
      </RelOp>
     </NestedLoops>
    </RelOp>
   </QueryPlan>
  </StmtSimple>
 </Statements></Batch></BatchSequence>
</ShowPlanXML>`
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func mustParsePlan(t *testing.T, xml string) *showplan.Plan {
	t.Helper()
	p, err := showplan.Parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return p
}

// TestComparePanelRendersBothPlansSideBySide. The pane's whole job is that a
// difference can be read off one row, so the row is checked whole.
func TestComparePanelRendersBothPlansSideBySide(t *testing.T) {
	a := newTestApp()
	left := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	right := mustParsePlan(t, comparePlanXML("IX_customer", 4000, 8))
	p := NewPlanComparePanel(a, "Compare plans 1 and 2", left, right)
	p.SetBounds(0, 0, 160, 40)

	seek := gridRowStartingWith(t, p.ops, "  Index Seek")
	if got := seek[1]; got != "Changed" {
		t.Errorf("the seek reads as %q, want Changed", got)
	}
	if !strings.Contains(seek[len(seek)-1], "IX_date → IX_customer") {
		t.Errorf("the differences cell is %q, want the index change named", seek[len(seek)-1])
	}
	// Both sides' numbers are shown, and they are not the same number.
	if seek[4] == seek[5] {
		t.Errorf("both Est rows columns read %q", seek[4])
	}
	// The property grid carries the statement-level pair too.
	cost := gridRowStartingWith(t, p.props, "Estimated subtree cost")
	if cost[1] == cost[2] || cost[3] != "◆" {
		t.Errorf("the cost property row = %v, want two different values marked as changed", cost)
	}
	if !strings.Contains(p.props.Status(), "changed") {
		t.Errorf("the summary = %q, want it to count the changes", p.props.Status())
	}
}

// TestComparePanelSaysWhenNothingDiffers, which is a result and not an empty
// pane: a user who forced the wrong plan back needs to see that the two are the
// same plan.
func TestComparePanelSaysWhenNothingDiffers(t *testing.T) {
	a := newTestApp()
	plan := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	p := NewPlanComparePanel(a, "Compare", plan, mustParsePlan(t, comparePlanXML("IX_date", 10, 1)))
	p.SetBounds(0, 0, 160, 40)
	if !strings.Contains(p.props.Status(), "no differences") {
		t.Errorf("summary = %q, want it to say there are none", p.props.Status())
	}
	for i := 0; p.ops.Row(i) != nil; i++ {
		if got := p.ops.Row(i)[1]; got != "Same" {
			t.Errorf("row %d reads as %q against an identical plan", i, got)
		}
	}
}

// TestComparePanelTabLeavesOnTheSecondPress — App only moves focus out of a
// panel that declines the key.
func TestComparePanelTabLeavesOnTheSecondPress(t *testing.T) {
	a := newTestApp()
	plan := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	p := NewPlanComparePanel(a, "Compare", plan, plan)
	p.SetBounds(0, 0, 160, 40)
	p.SetActive(true)

	tab := func() bool { return p.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)) }
	if !tab() {
		t.Fatal("the first Tab was declined, want it to move to the operator grid")
	}
	if !p.focusOps {
		t.Fatal("focus did not move to the operator grid")
	}
	if tab() {
		t.Error("the second Tab was consumed — the panel is a keyboard trap")
	}
	if p.focusOps {
		t.Error("leaving did not reset focus to the property grid")
	}
}

// gridRowStartingWith finds a grid row by its first cell, never by index: both
// grids here are built from a comparison whose row order is the thing under
// test, and an index-addressed assertion agrees with a mis-paired tree.
func gridRowStartingWith(t *testing.T, g interface{ Row(int) []string }, prefix string) []string {
	t.Helper()
	for i := 0; ; i++ {
		row := g.Row(i)
		if row == nil {
			t.Fatalf("no row starting with %q", prefix)
		}
		if strings.HasPrefix(row[0], prefix) {
			return row
		}
	}
}

// TestComparePanelSkipsAStatementWithNoPlan. A batch whose first statement is a
// SET carries no operator tree, and comparing that one would show two empty
// plans for a query whose plans are right there behind it.
func TestComparePanelSkipsAStatementWithNoPlan(t *testing.T) {
	withSet := func(index string) string {
		const set = `  <StmtSimple StatementText="SET NOCOUNT ON" StatementType="SET ON/OFF"/>` + "\n"
		return strings.Replace(comparePlanXML(index, 10, 1), `  <StmtSimple StatementText="SELECT 1"`,
			set+`  <StmtSimple StatementText="SELECT 1"`, 1)
	}
	a := newTestApp()
	left := mustParsePlan(t, withSet("IX_date"))
	if len(left.Statements) != 2 {
		t.Fatalf("the fixture parsed to %d statements, want the SET and the SELECT", len(left.Statements))
	}
	p := NewPlanComparePanel(a, "Compare", left, mustParsePlan(t, withSet("IX_customer")))
	p.SetBounds(0, 0, 160, 40)

	seek := gridRowStartingWith(t, p.ops, "  Index Seek")
	if !strings.Contains(seek[len(seek)-1], "IX_date → IX_customer") {
		t.Errorf("the comparison shows %q, want the index change from the statement that has a plan", seek[len(seek)-1])
	}
}

// TestAGestureStaysWithThePaneThatClaimedIt. Rule 1 of the mouseDragging idiom
// (ARCHITECTURE.md § The mouseDragging idiom): a gesture belongs to whatever
// claimed its first press, until the release.
//
// The panel tracked its owner as a bool — the operators grid, or not — so the
// splitter and the properties grid shared one case and a held event during a
// properties drag was offered to the splitter before the grid that owned it.
// Nothing came of that in the shipped build: the splitter's own mouseDragging
// latch is set by the very press that started the drag elsewhere, so it
// declines the event as not fresh, and a grid clamps a drag that leaves its
// rows. Ownership is the invariant and those are the second line of defence,
// which is why the owner is now a three-way zone routed by routeDrag.
//
// This pins the ownership itself, which is the part that is actually the
// panel's own: each region claims its press, keeps the claim while the button
// is held even as the pointer crosses another region, and gives it up on the
// release — and the splitter resizes only for a gesture it claimed. All three
// zones are exercised, because a router that answers the same for every one of
// them passes any single case.
func TestAGestureStaysWithThePaneThatClaimedIt(t *testing.T) {
	a := newTestApp()
	plan := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	p := NewPlanComparePanel(a, "Compare", plan, mustParsePlan(t, comparePlanXML("IX_customer", 4000, 8)))
	p.SetBounds(0, 0, 80, 24)

	bar := p.split.SplitPos()
	props, ops := p.split.FirstRect(), p.split.SecondRect()
	press := func(x, y int, b tcell.ButtonMask) {
		p.HandleMouse(tcell.NewEventMouse(x, y, b, tcell.ModNone))
	}

	cases := []struct {
		name string
		y    int
		want pcDragZone
	}{
		{"the properties grid", props.Y + 2, pcZoneProps},
		{"the operators grid", ops.Y + 2, pcZoneOps},
		{"the splitter", bar, pcZoneSplit},
	}
	for _, tc := range cases {
		t.Run(tc.name+" keeps the gesture it claimed", func(t *testing.T) {
			press(5, tc.y, tcell.Button1)
			if p.dragZone != tc.want {
				t.Fatalf("dragZone = %d after a press in %s, want %d", p.dragZone, tc.name, tc.want)
			}
			// The pointer crosses into another region with the button down.
			ratio := p.split.Ratio()
			press(5, bar+2, tcell.Button1)
			if p.dragZone != tc.want {
				t.Errorf("dragZone = %d mid-drag, want %s to still own the gesture", p.dragZone, tc.name)
			}
			if moved := p.split.Ratio() != ratio; moved != (tc.want == pcZoneSplit) {
				t.Errorf("splitter moved = %v during a gesture owned by %s, want %v",
					moved, tc.name, tc.want == pcZoneSplit)
			}
			press(5, bar+2, tcell.ButtonNone)
			if p.dragZone != pcZoneNone {
				t.Errorf("dragZone = %d after the release, want pcZoneNone", p.dragZone)
			}
			p.split.SetRatio(0.4)
			p.layoutChildren()
		})
	}
}

// twoStatementPlanXML is a batch of a SET, the seek query comparePlanXML builds
// and a sort over a scan. Each SELECT numbers its operators from 0, as SQL
// Server does, so the second can only be told from the first by statement.
func twoStatementPlanXML(index string) string {
	const set = `  <StmtSimple StatementText="SET NOCOUNT ON" StatementType="SET ON/OFF"/>` + "\n"
	const sorted = `  <StmtSimple StatementText="SELECT line FROM lines ORDER BY line" StatementType="SELECT" StatementSubTreeCost="3">
   <QueryPlan DegreeOfParallelism="1">
    <RelOp NodeId="0" PhysicalOp="Sort" LogicalOp="Sort" EstimateRows="9" EstimatedTotalSubtreeCost="3">
     <Sort>
      <RelOp NodeId="1" PhysicalOp="Clustered Index Scan" LogicalOp="Clustered Index Scan" EstimateRows="9" EstimatedTotalSubtreeCost="1">
       <IndexScan><Object Database="[appdb]" Schema="[dbo]" Table="[lines]" Index="[PK_lines]"/></IndexScan>
      </RelOp>
     </Sort>
    </RelOp>
   </QueryPlan>
  </StmtSimple>
`
	x := comparePlanXML(index, 10, 1)
	x = strings.Replace(x, `  <StmtSimple StatementText="SELECT 1"`, set+`  <StmtSimple StatementText="SELECT 1"`, 1)
	return strings.Replace(x, ` </Statements>`, sorted+` </Statements>`, 1)
}

// TestComparePanelPairsTheStatementsTheUserPicks. Each side starts on its first
// statement with a plan, and either side moves on its own: two batches that
// differ by a statement must still be comparable query to query, which a fixed
// N-with-N pairing cannot do.
func TestComparePanelPairsTheStatementsTheUserPicks(t *testing.T) {
	a := newTestApp()
	left := mustParsePlan(t, twoStatementPlanXML("IX_date"))
	right := mustParsePlan(t, twoStatementPlanXML("IX_customer"))
	if len(left.Statements) != 3 {
		t.Fatalf("the fixture parsed to %d statements, want SET, seek, sort", len(left.Statements))
	}
	p := NewPlanComparePanel(a, "Compare", left, right)
	p.SetBounds(0, 0, 160, 40)

	// The default pair: both seeks, the SET passed over.
	if p.stmtA != 1 || p.stmtB != 1 {
		t.Fatalf("starts on statements %d and %d, want 1 and 1 (the first with a plan)", p.stmtA, p.stmtB)
	}
	seek := gridRowStartingWith(t, p.ops, "  Index Seek")
	if !strings.Contains(seek[len(seek)-1], "IX_date → IX_customer") {
		t.Errorf("the default pair shows %q, want the seeks compared", seek[len(seek)-1])
	}

	// '}' moves B alone, to the sort: the roots pair (CompareStatements always
	// pairs roots), but nothing below them does.
	key := func(r rune) bool { return p.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone)) }
	if !key('}') {
		t.Fatal("'}' was declined, want it to step plan B's statement")
	}
	if p.stmtA != 1 || p.stmtB != 2 {
		t.Fatalf("after '}' the pair is %d/%d, want 1/2", p.stmtA, p.stmtB)
	}
	if row := gridRowStartingWith(t, p.ops, "  Index Seek"); row[1] != "Only in A" {
		t.Errorf("A's seek reads %q against B's sort, want Only in A", row[1])
	}
	if row := gridRowStartingWith(t, p.ops, "  Clustered Index Scan"); row[1] != "Only in B" {
		t.Errorf("B's scan reads %q, want Only in B", row[1])
	}

	// ']' moves A to its sort too: the same statement both sides, so Same.
	if !key(']') {
		t.Fatal("']' was declined, want it to step plan A's statement")
	}
	for i := 0; p.ops.Row(i) != nil; i++ {
		if got := p.ops.Row(i)[1]; got != "Same" {
			t.Errorf("row %d (%s) reads %q comparing the sort with itself", i, p.ops.Row(i)[0], got)
		}
	}

	// Stepping wraps past the SET: it has no plan to compare.
	if key(']'); p.stmtA != 1 {
		t.Errorf("']' from the last statement went to %d, want 1 — the SET has no plan", p.stmtA)
	}
	if !strings.Contains(p.pickers.Cells[pcPickA].Label, "statement 2/3") {
		t.Errorf("picker A reads %q, want it to name the statement compared", p.pickers.Cells[pcPickA].Label)
	}
}

// TestComparePanelPickerMenuChoosesAStatement — the mouse route: a click on a
// picker pops that side's statements, the SET withheld, and choosing one
// re-compares.
func TestComparePanelPickerMenuChoosesAStatement(t *testing.T) {
	a := newTestApp()
	p := NewPlanComparePanel(a, "Compare", mustParsePlan(t, twoStatementPlanXML("IX_date")),
		mustParsePlan(t, twoStatementPlanXML("IX_customer")))
	p.SetBounds(0, 0, 160, 40)

	if p.toolRect.H != 1 {
		t.Fatal("no picker row for two multi-statement plans")
	}
	r := p.pickers.Cells[pcPickB].Rect
	if r.IsZero() {
		t.Fatal("picker B did not fit a 160-column pane")
	}
	p.HandleMouse(tcell.NewEventMouse(r.X+1, r.Y, tcell.Button1, tcell.ModNone))
	p.HandleMouse(tcell.NewEventMouse(r.X+1, r.Y, tcell.ButtonNone, tcell.ModNone))
	if !a.contextMenu.Visible() {
		t.Fatal("a click on picker B opened no menu")
	}
	items := a.contextMenu.Items()
	if len(items) != 3 {
		t.Fatalf("the menu lists %d statements, want 3", len(items))
	}
	if items[0].Enabled() {
		t.Error("the SET is offered, but it has no plan to compare")
	}
	if !strings.HasPrefix(items[1].Label, "• ") {
		t.Errorf("the statement in force is not marked: %q", items[1].Label)
	}
	items[2].Action()
	if p.stmtB != 2 {
		t.Errorf("choosing statement 3 left B on %d", p.stmtB)
	}
	if row := gridRowStartingWith(t, p.ops, "  Clustered Index Scan"); row[1] != "Only in B" {
		t.Errorf("after choosing B's sort its scan reads %q, want Only in B", row[1])
	}
}

// TestComparePanelHasNoPickerRowForSingleStatementPlans. Query Store's
// comparison is one statement a side; a toolbar of two selectors with nothing
// to select would take a row from the grids for nothing.
func TestComparePanelHasNoPickerRowForSingleStatementPlans(t *testing.T) {
	a := newTestApp()
	plan := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	p := NewPlanComparePanel(a, "Compare", plan, mustParsePlan(t, comparePlanXML("IX_customer", 4000, 8)))
	p.SetBounds(0, 0, 160, 40)
	if p.toolRect.H != 0 {
		t.Error("a picker row for two one-statement plans")
	}
	if got := p.split.FirstRect().Y; got != 1 {
		t.Errorf("the grids start at row %d, want 1 — straight under the title", got)
	}
	if p.HandleKey(tcell.NewEventKey(tcell.KeyRune, "]", tcell.ModNone)) {
		t.Error("']' was consumed with no other statement to step to")
	}
}

// TestComparePanelKeepsBothPickersOnANarrowRow. A toolbar cell that does not fit
// is not drawn or clickable at all, so a long statement on A once took B's
// picker off a 70-column row. Both stay, at every width a pane gets.
func TestComparePanelKeepsBothPickersOnANarrowRow(t *testing.T) {
	a := newTestApp()
	p := NewPlanComparePanel(a, "Compare", mustParsePlan(t, twoStatementPlanXML("IX_date")),
		mustParsePlan(t, twoStatementPlanXML("IX_customer")))
	p.stepStatement(pcPickA, 1)
	p.stepStatement(pcPickB, 1) // both on the long ORDER BY statement
	for _, w := range []int{160, 70, 50, 40} {
		p.SetBounds(0, 0, w, 30)
		for i, name := range []string{"A", "B"} {
			r := p.pickers.Cells[i].Rect
			if r.IsZero() {
				t.Errorf("width %d: picker %s was dropped from the row", w, name)
			} else if r.Right() > w {
				t.Errorf("width %d: picker %s runs to column %d", w, name, r.Right())
			}
		}
	}
}
