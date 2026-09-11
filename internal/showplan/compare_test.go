package showplan

import (
	"strings"
	"testing"
)

// node builds one fixture operator. Object is always set, since pairing keys on
// it.
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

// lines renders a comparison compactly so tests assert the whole shape,
// including line ordering.
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

// Identical trees pair wholly and nothing is changed.
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

// The index isn't part of pairing: a seek that changed index is the comparison
// users want, and keying on it would split into two one-sided rows.
func TestCompareReportsTheIndexAndTheEstimateThatMoved(t *testing.T) {
	// Below the root, since roots always pair.
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

// A plan re-costed against refreshed statistics nudges every number; calling
// them all changed says nothing.
func TestCompareLeavesASmallReEstimateAlone(t *testing.T) {
	a := stmt(node(0, "Index Seek", "orders", "IX_date", 1000, 1.0))
	b := stmt(node(0, "Index Seek", "orders", "IX_date", 1005, 1.005))
	if got := CompareStatements(a, b); got[0].Kind != ChangeSame {
		t.Errorf("a 0.5%% re-estimate reads as %v with changes %v", got[0].Kind, got[0].Changes)
	}

	// A real move still counts, both directions.
	c := stmt(node(0, "Index Seek", "orders", "IX_date", 1200, 1.0))
	if got := CompareStatements(a, c); got[0].Kind != ChangeDifferent {
		t.Errorf("a 20%% move reads as %v", got[0].Kind)
	}
	if got := CompareStatements(c, a); got[0].Kind != ChangeDifferent {
		t.Errorf("the same move the other way reads as %v", got[0].Kind)
	}
	// A number appearing from zero is a change, not a division by zero.
	z := stmt(node(0, "Index Seek", "orders", "IX_date", 0, 1.0))
	if got := CompareStatements(z, a); got[0].Kind != ChangeDifferent {
		t.Errorf("0 → 1000 reads as %v", got[0].Kind)
	}
}

// A one-sided subtree shows where it sits, not after all matches.
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

// A seek that became a scan must not pair; that would hide the change behind
// property diffs.
func TestCompareDoesNotPairAcrossADifferentPhysicalOperator(t *testing.T) {
	a := stmt(node(0, "Index Seek", "orders", "IX_date", 10, 0.2))
	b := stmt(node(0, "Table Scan", "orders", "", 900000, 42))

	// Roots always pair, so check one level down.
	ra := stmt(node(9, "Nested Loops", "", "", 10, 1, a.Root))
	rb := stmt(node(9, "Nested Loops", "", "", 10, 1, b.Root))
	wantLines(t, CompareStatements(ra, rb),
		"Nested Loops  [Same]",
		" Index Seek orders [Only in A]",
		" Table Scan orders [Only in B]",
	)
}

// A real plan compared with itself: no differences, one line per operator.
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

// Every property is listed, moved or not.
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

// A statement with no plan (SET, USE) has no root and must not panic.
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

// Runtime numbers compare exactly while estimates keep their tolerance: 100,000
// → 100,500 reads is 500 pages of new work, not "Same".
func TestCompareCountsEveryActualRowAndRead(t *testing.T) {
	withRuntime := func(n *Node, rows, reads int64) *Statement {
		n.Runtime = &Runtime{Rows: rows, LogicalReads: reads, Executions: 1}
		return stmt(n)
	}
	a := withRuntime(node(0, "Index Seek", "orders", "IX_date", 1000, 1.0), 100000, 100000)
	b := withRuntime(node(0, "Index Seek", "orders", "IX_date", 1000, 1.0), 100500, 100000)
	got := CompareStatements(a, b)
	if got[0].Kind != ChangeDifferent {
		t.Errorf("a 0.5%% move in actual rows reads as %v with changes %v", got[0].Kind, got[0].Changes)
	}

	c := withRuntime(node(0, "Index Seek", "orders", "IX_date", 1000, 1.0), 100000, 100500)
	if got := CompareStatements(a, c); got[0].Kind != ChangeDifferent {
		t.Errorf("a 0.5%% move in logical reads reads as %v with changes %v", got[0].Kind, got[0].Changes)
	}

	// The node's estimates keep their tolerance.
	d := withRuntime(node(0, "Index Seek", "orders", "IX_date", 1005, 1.005), 100000, 100000)
	if got := CompareStatements(a, d); got[0].Kind != ChangeSame {
		t.Errorf("a 0.5%% re-estimate reads as %v with changes %v", got[0].Kind, got[0].Changes)
	}
}
