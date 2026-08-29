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
