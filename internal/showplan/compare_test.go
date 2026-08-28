package showplan

import (
	"strings"
	"testing"
)

// node builds one operator for a comparison fixture. Object is what pairing
// keys on beside the physical operator, so it is always set.
func node(id int, op, table, index string, estRows, cost float64, children ...*Node) *Node {
	return &Node{
		ID: id, PhysicalOp: op, LogicalOp: op,
		Object:         Object{Schema: "dbo", Table: table, Index: index},
		EstRows:        estRows,
		EstSubtreeCost: cost,
		Children:       children,
	}
}

func stmt(root *Node) *Statement {
	return &Statement{Type: "SELECT", Root: root, SubTreeCost: root.EstSubtreeCost}
}

// lines renders a comparison compactly, so a test can assert the whole shape
// rather than one row of it — the ordering between matched and one-sided lines
// is the part most easily got wrong.
func lines(diffs []NodeDiff) []string {
	out := make([]string, 0, len(diffs))
	for _, d := range diffs {
		n := d.Node()
		out = append(out, strings.Repeat(" ", d.Depth)+n.PhysicalOp+" "+n.Object.Table+" ["+d.Kind.String()+"]")
	}
	return out
}

func wantLines(t *testing.T, got []NodeDiff, want ...string) {
	t.Helper()
	have := lines(got)
	if len(have) != len(want) {
		t.Fatalf("comparison has %d lines:\n%s\nwant %d:\n%s",
			len(have), strings.Join(have, "\n"), len(want), strings.Join(want, "\n"))
	}
	for i := range want {
		if have[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, have[i], want[i])
		}
	}
}

// TestCompareMatchesTheSameOperatorAcrossPlans: identical trees pair up whole,
// and nothing reads as changed.
func TestCompareMatchesTheSameOperatorAcrossPlans(t *testing.T) {
	build := func() *Statement {
		return stmt(node(0, "Nested Loops", "", "", 10, 0.5,
			node(1, "Index Seek", "orders", "IX_date", 10, 0.2),
			node(2, "Key Lookup", "orders", "PK_orders", 10, 0.3)))
	}
	diffs := CompareStatements(build(), build())
	wantLines(t, diffs,
		"Nested Loops  [Same]",
		" Index Seek orders [Same]",
		" Key Lookup orders [Same]",
	)
	for _, d := range diffs {
		if len(d.Changes) != 0 {
			t.Errorf("%s reports changes %v against itself", d.Node().PhysicalOp, d.Changes)
		}
	}
}

// TestCompareReportsTheIndexAndTheEstimateThatMoved. The index is deliberately
// not part of what pairs two operators: a seek that changed index is the
// comparison this pane exists for, and keying on the index would split it into
// two one-sided rows with nothing to read against each other.
func TestCompareReportsTheIndexAndTheEstimateThatMoved(t *testing.T) {
	// Under a parent, not at the root: two roots pair unconditionally — every
	// plan has exactly one — so the root is the one place this cannot be seen.
	a := stmt(node(9, "Nested Loops", "", "", 10, 1,
		node(0, "Index Seek", "orders", "IX_date", 10, 0.2)))
	b := stmt(node(9, "Nested Loops", "", "", 10, 1,
		node(0, "Index Seek", "orders", "IX_customer", 4000, 3.7)))

	diffs := CompareStatements(a, b)
	if len(diffs) != 2 || diffs[1].Kind != ChangeDifferent {
		t.Fatalf("comparison = %v, want the seek paired and changed", lines(diffs))
	}
	joined := strings.Join(diffs[1].Changes, "; ")
	for _, want := range []string{"IX_date → IX_customer", "Est rows 10.0 → 4000.0", "Est subtree cost"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes = %q, want it to name %q", joined, want)
		}
	}
}

// TestCompareLeavesASmallReEstimateAlone. A plan re-costed against refreshed
// statistics moves every number in the tree by a hair; a comparison that
// called all of them changed would say nothing at all.
func TestCompareLeavesASmallReEstimateAlone(t *testing.T) {
	a := stmt(node(0, "Index Seek", "orders", "IX_date", 1000, 1.0))
	b := stmt(node(0, "Index Seek", "orders", "IX_date", 1005, 1.005))
	if got := CompareStatements(a, b); got[0].Kind != ChangeSame {
		t.Errorf("a 0.5%% re-estimate reads as %v with changes %v", got[0].Kind, got[0].Changes)
	}

	// And a real move still counts, in both directions.
	c := stmt(node(0, "Index Seek", "orders", "IX_date", 1200, 1.0))
	if got := CompareStatements(a, c); got[0].Kind != ChangeDifferent {
		t.Errorf("a 20%% move reads as %v", got[0].Kind)
	}
	if got := CompareStatements(c, a); got[0].Kind != ChangeDifferent {
		t.Errorf("the same move the other way reads as %v", got[0].Kind)
	}
	// A number that appears from nothing is a change, not a division by zero.
	z := stmt(node(0, "Index Seek", "orders", "IX_date", 0, 1.0))
	if got := CompareStatements(z, a); got[0].Kind != ChangeDifferent {
		t.Errorf("0 → 1000 reads as %v", got[0].Kind)
	}
}

// TestCompareShowsAOneSidedSubtreeWhereItSits, rather than after everything
// that matched: a sort added on one side belongs beside the operators it was
// added between.
func TestCompareShowsAOneSidedSubtreeWhereItSits(t *testing.T) {
	a := stmt(node(0, "Nested Loops", "", "", 10, 1,
		node(1, "Index Seek", "orders", "IX_date", 10, 0.2),
		node(2, "Clustered Index Scan", "customers", "PK_cust", 90, 0.8)))
	b := stmt(node(0, "Nested Loops", "", "", 10, 1,
		node(1, "Index Seek", "orders", "IX_date", 10, 0.2),
		node(3, "Sort", "", "", 90, 0.4,
			node(4, "Table Scan", "customers", "", 90, 0.9)),
		node(2, "Clustered Index Scan", "customers", "PK_cust", 90, 0.8)))

	wantLines(t, CompareStatements(a, b),
		"Nested Loops  [Same]",
		" Index Seek orders [Same]",
		" Sort  [Only in B]",
		"  Table Scan customers [Only in B]",
		" Clustered Index Scan customers [Same]",
	)
}

// TestCompareDoesNotPairAcrossADifferentPhysicalOperator. A seek that became a
// scan is what happened, and pairing the two would hide it behind a row of
// property changes.
func TestCompareDoesNotPairAcrossADifferentPhysicalOperator(t *testing.T) {
	a := stmt(node(0, "Index Seek", "orders", "IX_date", 10, 0.2))
	b := stmt(node(0, "Table Scan", "orders", "", 900000, 42))

	// The roots pair regardless — every plan has exactly one — so this is
	// checked one level down, where the choice is real.
	ra := stmt(node(9, "Nested Loops", "", "", 10, 1, a.Root))
	rb := stmt(node(9, "Nested Loops", "", "", 10, 1, b.Root))
	wantLines(t, CompareStatements(ra, rb),
		"Nested Loops  [Same]",
		" Index Seek orders [Only in A]",
		" Table Scan orders [Only in B]",
	)
}

// TestCompareRunsAgainstARealPlan, so the fixture the parser is pinned by also
// pins the comparison: a plan compared with itself has no differences and one
// line per operator.
func TestCompareRunsAgainstARealPlan(t *testing.T) {
	plan := mustParseFile(t, "testdata/actual_plan.sqlplan")
	st := plan.Statements[0]

	diffs := CompareStatements(st, st)
	if got, want := len(diffs), len(st.Nodes()); got != want {
		t.Errorf("comparison has %d lines for a %d-operator plan", got, want)
	}
	for _, d := range diffs {
		if d.Kind != ChangeSame {
			t.Errorf("operator %d reads as %v against itself: %v", d.Node().ID, d.Kind, d.Changes)
		}
		if d.Left == nil || d.Right == nil {
			t.Errorf("operator %d paired with nothing", d.Node().ID)
		}
	}

	props := CompareProperties(st, st)
	if len(props) == 0 {
		t.Fatal("no statement properties compared")
	}
	for _, p := range props {
		if p.Different {
			t.Errorf("property %q differs from itself: %q vs %q", p.Name, p.Left, p.Right)
		}
	}
}

// TestComparePropertiesNamesTheDifferenceAndKeepsTheRest: every property is
// listed whether it moved or not, so the pane reads as a fixed table rather
// than a list whose length depends on the two plans.
func TestComparePropertiesNamesTheDifferenceAndKeepsTheRest(t *testing.T) {
	a := stmt(node(0, "Index Seek", "orders", "IX_date", 10, 0.2))
	b := stmt(node(0, "Index Seek", "orders", "IX_date", 10, 4.5))
	b.DOP = 8

	props := CompareProperties(a, b)
	byName := map[string]PropDiff{}
	for _, p := range props {
		byName[p.Name] = p
	}
	if p := byName["Degree of parallelism"]; !p.Different || p.Left != "0" || p.Right != "8" {
		t.Errorf("DOP row = %+v, want 0 vs 8 marked different", p)
	}
	if p := byName["Statement type"]; p.Different {
		t.Errorf("Statement type reads as different: %+v", p)
	}
	if _, ok := byName["Estimated subtree cost"]; !ok {
		t.Error("the cost row is missing from the property comparison")
	}
	if len(CompareProperties(a, a)) != len(props) {
		t.Error("two identical plans compare a different number of properties")
	}
}

// TestCompareSurvivesAStatementWithNoPlan. A SET or USE statement carries no
// operator tree, and a comparison that dereferenced its root would take the
// panel down with it.
func TestCompareSurvivesAStatementWithNoPlan(t *testing.T) {
	empty := &Statement{Type: "SET ON/OFF"}
	full := stmt(node(0, "Index Seek", "orders", "IX_date", 10, 0.2))

	if got := CompareStatements(empty, empty); len(got) != 0 {
		t.Errorf("two planless statements compared to %v", lines(got))
	}
	wantLines(t, CompareStatements(empty, full), "Index Seek orders [Only in B]")
	wantLines(t, CompareStatements(full, empty), "Index Seek orders [Only in A]")
	if got := CompareStatements(nil, full); len(got) != 0 {
		t.Errorf("a nil statement compared to %v", lines(got))
	}
	if got := CompareProperties(nil, full); got != nil {
		t.Errorf("a nil statement produced properties %v", got)
	}
}
