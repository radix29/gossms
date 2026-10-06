package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// addPlanPanel opens xml as a detached plan panel titled title, as Expand or
// File > Open would.
func addPlanPanel(t *testing.T, a *App, title, xml string) *PlanPanel {
	t.Helper()
	pp := NewPlanPanel(a, title, mustParsePlan(t, xml))
	a.panels.SetActive(a.panels.AddPanel(pp))
	return pp
}

// Compare with ▸ offers every other open plan — a query panel's plan tab
// included, the plan's own panel and a panel without a plan not — and choosing
// one compares the active plan as A against it as B.
func TestCompareWithListsTheOtherOpenPlans(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.planView = qp.newPlanView()
	qp.planView.SetPlan(mustParsePlan(t, comparePlanXML("IX_query", 5, 2)))
	a.panels.AddPanel(qp)
	a.panels.AddPanel(NewQueryPanel(a, "Query 2")) // no plan
	other := addPlanPanel(t, a, "saved.sqlplan", comparePlanXML("IX_customer", 4000, 8))
	from := addPlanPanel(t, a, "Execution Plan — Query 3", comparePlanXML("IX_date", 10, 1))

	item := a.activeCompareWithItem()
	var labels []string
	for _, s := range item.Sub {
		labels = append(labels, s.Label)
	}
	if strings.Join(labels, "|") != "Query 1 (plan)|saved.sqlplan" {
		t.Fatalf("Compare with lists %q, want the query panel's plan and the other plan panel", labels)
	}
	item.Sub[1].Action()

	cp, ok := a.panels.ActivePanel().(*PlanComparePanel)
	if !ok {
		t.Fatalf("active panel = %T, want *PlanComparePanel", a.panels.ActivePanel())
	}
	if cp.planA != from.planView.Plan() || cp.planB != other.planView.Plan() {
		t.Error("the comparison is not the active plan (A) against the chosen one (B)")
	}
	if cp.Title() != "Compare Showplan — A: Execution Plan — Query 3 · B: saved.sqlplan" {
		t.Errorf("Title() = %q", cp.Title())
	}
}

// With no other plan open the cascade is withheld and says why, and with no
// plan on screen at all both entry points are.
func TestCompareWithIsWithheldWithNothingToCompare(t *testing.T) {
	a := newTestApp()
	a.panels.SetActive(a.panels.AddPanel(NewQueryPanel(a, "Query 1")))
	if item := a.activeCompareWithItem(); item.Enabled() || item.Note != "no execution plan" {
		t.Errorf("with no plan: enabled=%v note=%q", item.Enabled(), item.Note)
	}
	addPlanPanel(t, a, "only.sqlplan", comparePlanXML("IX_date", 10, 1))
	item := a.activeCompareWithItem()
	if item.Enabled() || item.Note != "no other open plan" || len(item.Sub) != 0 {
		t.Errorf("with one plan: enabled=%v note=%q sub=%d", item.Enabled(), item.Note, len(item.Sub))
	}
}

// Compare Showplan... reads the chosen .sqlplan — UTF-16 with a BOM, as SSMS
// writes it — as plan B, against the plan on screen as A.
func TestCompareShowplanComparesAgainstTheChosenFile(t *testing.T) {
	a := newTestApp()
	a.fileDialog = dialogs.NewFileDialog(nil)
	from := addPlanPanel(t, a, "Execution Plan — Query 1", comparePlanXML("IX_date", 10, 1))
	path := filepath.Join(t.TempDir(), "actual.sqlplan")
	if err := os.WriteFile(path, readFixturePlan(t), 0o644); err != nil {
		t.Fatal(err)
	}

	a.compareShowplan()
	a.fileDialog.OnChoose(path)

	cp, ok := a.panels.ActivePanel().(*PlanComparePanel)
	if !ok {
		t.Fatalf("active panel = %T, want *PlanComparePanel", a.panels.ActivePanel())
	}
	if cp.planA != from.planView.Plan() || cp.planB == nil || len(cp.planB.Statements) == 0 {
		t.Error("the comparison is not the plan on screen against the file's plan")
	}
	if !strings.HasSuffix(cp.Title(), "B: actual.sqlplan") {
		t.Errorf("Title() = %q", cp.Title())
	}
	// A side opened back out is named by its source, not by the whole
	// comparison title — which a Compare with of that panel would nest again.
	cp.openOperator(pcPickB, cp.planB.Statements[cp.stmtB].Root)
	if got := a.panels.ActivePanel().Title(); got != "Plan B: actual.sqlplan" {
		t.Errorf("the opened side's title = %q", got)
	}
}

// A file that isn't a plan opens no comparison and says so.
func TestCompareShowplanRefusesAFileThatIsNotAPlan(t *testing.T) {
	a := newTestApp()
	a.fileDialog = dialogs.NewFileDialog(nil)
	addPlanPanel(t, a, "p", comparePlanXML("IX_date", 10, 1))
	path := filepath.Join(t.TempDir(), "notes.sqlplan")
	if err := os.WriteFile(path, []byte("not a plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.compareShowplan()
	a.fileDialog.OnChoose(path)
	if _, ok := a.panels.ActivePanel().(*PlanComparePanel); ok {
		t.Error("a comparison opened against a file that is not a plan")
	}
	if !strings.Contains(a.statusText, "Could not parse notes.sqlplan") {
		t.Errorf("status = %q", a.statusText)
	}
}

// Enter on an operator row opens the plan of the column the cursor is on, at
// that operator and in the statement being compared — the second of the batch
// here (the first is a SET), so a NodeId opened in the wrong statement shows.
func TestCompareEnterOpensThePlanAtTheOperator(t *testing.T) {
	for _, tc := range []struct {
		col   int
		title string
	}{
		{0, "Plan A — Compare"}, // Operator
		{5, "Plan B — Compare"}, // Est rows B
	} {
		a := newTestApp()
		left := mustParsePlan(t, twoStatementPlanXML("IX_date"))
		right := mustParsePlan(t, twoStatementPlanXML("IX_customer"))
		p := NewPlanComparePanel(a, "Compare", left, right)
		a.panels.SetActive(a.panels.AddPanel(p))
		p.SetBounds(0, 0, 160, 40)
		p.setFocus(true)
		row := -1
		for i, d := range p.diffs {
			if d.Node().PhysicalOp == "Index Seek" {
				row = i
			}
		}
		p.ops.SetSelectedCell(row, tc.col)

		if !p.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) {
			t.Fatalf("col %d: Enter was not handled", tc.col)
		}
		pp, ok := a.panels.ActivePanel().(*PlanPanel)
		if !ok {
			t.Fatalf("col %d: active panel = %T, want *PlanPanel", tc.col, a.panels.ActivePanel())
		}
		if pp.Title() != tc.title {
			t.Errorf("col %d: Title() = %q, want %q", tc.col, pp.Title(), tc.title)
		}
		want := left
		if tc.col == 5 {
			want = right
		}
		if pp.planView.Plan() != want {
			t.Errorf("col %d: opened the other side's plan", tc.col)
		}
		if stmt, id := pp.planView.SelectedOperator(); stmt != 1 || id != 1 {
			t.Errorf("col %d: selected statement %d operator %d, want the seek (1, 1)", tc.col, stmt, id)
		}
	}
}

// A row only one plan has opens that plan whichever column the cursor is on:
// there is nothing on the other side to open.
func TestCompareEnterFallsBackToTheSideThatHasTheOperator(t *testing.T) {
	a := newTestApp()
	left := mustParsePlan(t, comparePlanXML("IX_date", 10, 1))
	right := mustParsePlan(t, strings.Replace(comparePlanXML("IX_date", 10, 1),
		`PhysicalOp="Index Seek" LogicalOp="Index Seek"`, `PhysicalOp="Table Scan" LogicalOp="Table Scan"`, 1))
	p := NewPlanComparePanel(a, "Compare", left, right)
	p.SetBounds(0, 0, 160, 40)
	row := -1
	for i, d := range p.diffs {
		if d.Left != nil && d.Right == nil {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("the fixture has no A-only operator")
	}
	p.ops.SetSelectedCell(row, 5) // a B column
	if side, n := p.operatorSide(); side != pcPickA || n != p.diffs[row].Left {
		t.Errorf("operatorSide() = %d, want A's operator", side)
	}
}
