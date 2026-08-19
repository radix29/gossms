package planview

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
)

// twoStatementPlan carries a missing-index suggestion on its first
// statement and none on its second — the case the banner's per-statement
// layout has to get right.
const twoStatementPlan = `<ShowPlanXML Version="1.599" Build="17.0.1125.2">
<BatchSequence><Batch><Statements>
<StmtSimple StatementText="SELECT a" StatementSubTreeCost="1">
<QueryPlan>
<MissingIndexes>
  <MissingIndexGroup Impact="42.5">
    <MissingIndex Database="[AppDB]" Schema="[dbo]" Table="[Orders]">
      <ColumnGroup Usage="EQUALITY"><Column Name="[CustomerID]" ColumnId="2"/></ColumnGroup>
    </MissingIndex>
  </MissingIndexGroup>
</MissingIndexes>
<RelOp NodeId="0" PhysicalOp="Table Scan" LogicalOp="Table Scan"/>
</QueryPlan></StmtSimple>
<StmtSimple StatementText="SELECT b" StatementSubTreeCost="1">
<QueryPlan><RelOp NodeId="0" PhysicalOp="Table Scan" LogicalOp="Table Scan"/></QueryPlan>
</StmtSimple>
</Statements></Batch></BatchSequence></ShowPlanXML>`

func bannerView(t *testing.T) *PlanView {
	t.Helper()
	plan, err := showplan.Parse([]byte(twoStatementPlan))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := New()
	v.SetPlan(plan)
	v.SetBounds(0, 0, 100, 24)
	return v
}

func TestBannerShowsTheSuggestionAndCostsARow(t *testing.T) {
	v := bannerView(t)
	if v.bannerRect.H != 1 {
		t.Fatal("no banner row reserved for a statement with a missing index")
	}
	if v.bannerRect.Y != v.stmtRect.Y+1 || v.contentRect.Y != v.bannerRect.Y+1 {
		t.Errorf("rows out of order: stmt %d banner %d content %d",
			v.stmtRect.Y, v.bannerRect.Y, v.contentRect.Y)
	}
	text := v.bannerText()
	if !strings.Contains(text, "Impact 42.5%") || !strings.Contains(text, "ON [dbo].[Orders] ([CustomerID])") {
		t.Errorf("bannerText() = %q", text)
	}
}

// The banner belongs to one statement, so stepping to a statement without a
// suggestion has to give the row back to the content below it.
func TestBannerRowIsReclaimedOnAStatementWithoutOne(t *testing.T) {
	v := bannerView(t)
	contentH := v.contentRect.H
	v.stepStatement(1)
	if v.bannerRect.H != 0 {
		t.Error("banner still reserved on a statement with no missing index")
	}
	if v.contentRect.H != contentH+1 {
		t.Errorf("content height = %d, want %d — the banner's row was not given back",
			v.contentRect.H, contentH+1)
	}
	if v.bannerText() != "" {
		t.Errorf("bannerText() = %q on a statement with no suggestion", v.bannerText())
	}
}

func TestBannerKeyAndClickOpenTheDetails(t *testing.T) {
	for _, tt := range []struct {
		name string
		act  func(v *PlanView) bool
	}{
		{"m", func(v *PlanView) bool { return v.HandleKey(keyRune('m')) }},
		{"click", func(v *PlanView) bool {
			return v.HandleMouse(tcell.NewEventMouse(5, v.bannerRect.Y, tcell.Button1, tcell.ModNone))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := bannerView(t)
			var got string
			v.OnMissingIndex = func(script string) { got = script }
			if !tt.act(v) {
				t.Fatal("event not handled")
			}
			if !strings.Contains(got, "CREATE NONCLUSTERED INDEX") || !strings.Contains(got, "USE [AppDB]") {
				t.Errorf("script handed to the host = %q", got)
			}
		})
	}
}

// A statement with no suggestion must not swallow 'm' — the host routes
// unhandled keys elsewhere, and a widget that always claims a key is a
// keyboard trap.
func TestBannerKeyIsRefusedWithNothingToShow(t *testing.T) {
	v := bannerView(t)
	v.OnMissingIndex = func(string) {}
	v.stepStatement(1)
	if v.HandleKey(keyRune('m')) {
		t.Error("'m' claimed on a statement with no missing index")
	}
}
