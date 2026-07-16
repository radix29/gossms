package showplan

import (
	"math"
	"os"
	"strings"
	"testing"
)

func mustParseFile(t *testing.T, path string) *Plan {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	p, err := Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return p
}

func findNode(root *Node, id int) *Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := findNode(c, id); n != nil {
			return n
		}
	}
	return nil
}

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (±%v)", name, got, want, tol)
	}
}

func TestParseActualPlan_UTF16Fixture(t *testing.T) {
	plan := mustParseFile(t, "testdata/actual_plan.sqlplan")

	if !strings.HasPrefix(plan.XML, "<?xml") {
		t.Fatalf("plan.XML does not look decoded: %q", plan.XML[:min(40, len(plan.XML))])
	}
	if plan.Version != "1.599" {
		t.Errorf("Version = %q, want 1.599", plan.Version)
	}
	if len(plan.Statements) != 1 {
		t.Fatalf("len(Statements) = %d, want 1", len(plan.Statements))
	}
	st := plan.Statements[0]

	if !strings.Contains(st.Text, "vw_PatientHistory") {
		t.Errorf("StatementText missing expected content: %q", st.Text)
	}
	if st.Type != "SELECT" {
		t.Errorf("Type = %q, want SELECT", st.Type)
	}
	if st.QueryHash != "0x7D985C0758DBF8DB" {
		t.Errorf("QueryHash = %q", st.QueryHash)
	}
	approx(t, "SubTreeCost", st.SubTreeCost, 0.0188341, 1e-7)
	if st.DOP != 1 {
		t.Errorf("DOP = %d, want 1", st.DOP)
	}

	nodes := st.Nodes()
	if len(nodes) != 12 {
		t.Fatalf("len(Nodes()) = %d, want 12", len(nodes))
	}
	if !plan.HasActual() {
		t.Error("HasActual() = false, want true (fixture carries RunTimeInformation)")
	}

	if st.Root == nil || st.Root.PhysicalOp != "Top" || st.Root.ID != 0 {
		t.Fatalf("Root = %+v, want PhysicalOp=Top ID=0", st.Root)
	}
	if len(st.Root.Children) != 1 || st.Root.Children[0].ID != 1 {
		t.Fatalf("Root.Children = %+v, want single child ID=1", st.Root.Children)
	}

	n1 := st.Root.Children[0]
	if n1.PhysicalOp != "Nested Loops" || n1.LogicalOp != "Left Outer Join" {
		t.Errorf("node1 = %+v", n1)
	}
	wantChildIDs := []int{2, 11}
	if len(n1.Children) != 2 || n1.Children[0].ID != wantChildIDs[0] || n1.Children[1].ID != wantChildIDs[1] {
		t.Fatalf("node1.Children IDs = %v, want %v", ids(n1.Children), wantChildIDs)
	}

	n7 := findNode(st.Root, 7)
	if n7 == nil {
		t.Fatal("node 7 not found")
	}
	if n7.PhysicalOp != "Clustered Index Scan" {
		t.Errorf("node7.PhysicalOp = %q", n7.PhysicalOp)
	}
	if n7.Object.Table != "Appointments" || n7.Object.Alias != "a" || n7.Object.Index != "PK_Appointments" {
		t.Errorf("node7.Object = %+v", n7.Object)
	}
	if n7.Object.IndexKind != "Clustered" {
		t.Errorf("node7.Object.IndexKind = %q", n7.Object.IndexKind)
	}
	if n7.Runtime == nil {
		t.Fatal("node7.Runtime = nil, want populated")
	}
	if n7.Runtime.Rows != 5 || n7.Runtime.LogicalReads != 2 {
		t.Errorf("node7.Runtime = %+v, want Rows=5 LogicalReads=2", n7.Runtime)
	}
	if len(n7.Children) != 0 {
		t.Errorf("node7 should be a leaf, got %d children", len(n7.Children))
	}

	n8 := findNode(st.Root, 8)
	if n8 == nil {
		t.Fatal("node 8 not found")
	}
	if !strings.Contains(n8.SeekPredicate, "DoctorID") {
		t.Errorf("node8.SeekPredicate = %q, want it to mention DoctorID", n8.SeekPredicate)
	}

	// Cost%: leaf node7's own cost is its whole subtree cost (no children),
	// as a fraction of the statement total.
	wantCost := n7.EstSubtreeCost / st.SubTreeCost
	approx(t, "node7.Cost", n7.Cost(st.SubTreeCost), wantCost, 1e-9)
	if n7.Cost(st.SubTreeCost) <= 0 || n7.Cost(st.SubTreeCost) >= 1 {
		t.Errorf("node7.Cost = %v, want in (0,1)", n7.Cost(st.SubTreeCost))
	}

	// Every node's own attributes should have made it into Props.
	found := false
	for _, kv := range st.Root.Props {
		if kv.Key == "PhysicalOp" && kv.Value == "Top" {
			found = true
		}
	}
	if !found {
		t.Error(`Root.Props missing {"PhysicalOp","Top"}`)
	}
}

func ids(nodes []*Node) []int {
	out := make([]int, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func TestParseEstimatedPlan(t *testing.T) {
	plan := mustParseFile(t, "testdata/estimated_plan.xml")

	if plan.HasActual() {
		t.Error("HasActual() = true, want false (no RunTimeInformation in this fixture)")
	}
	st := plan.Statements[0]
	if st.Root == nil {
		t.Fatal("Root = nil")
	}
	if st.Root.Runtime != nil {
		t.Error("Root.Runtime != nil on an estimated-only plan")
	}

	scan := findNode(st.Root, 1)
	if scan == nil {
		t.Fatal("node 1 (Clustered Index Scan) not found")
	}
	if scan.PhysicalOp != "Clustered Index Scan" {
		t.Errorf("node1.PhysicalOp = %q", scan.PhysicalOp)
	}
	if len(scan.Warnings) != 1 || scan.Warnings[0] != "ColumnsWithNoStatistics" {
		t.Errorf("node1.Warnings = %v, want [ColumnsWithNoStatistics]", scan.Warnings)
	}
	if scan.Object.Table != "Doctors" || scan.Object.Index != "PK_Doctors" {
		t.Errorf("node1.Object = %+v", scan.Object)
	}
}

// TestParse_BooleanAttrsAcceptXSDOneZeroForm checks Warnings' boolean
// attributes and Parallel accept the "1"/"0" XSD boolean lexical form,
// not just "true"/"false" — a real SQL Server 17.0.4055.5 build was
// observed emitting NoJoinPredicate="1" and Parallel="0" for the same
// plan that other builds/attributes represent as "true"/"false", and the
// "1" form was previously silently dropped (a real CROSS JOIN's warning
// went missing from the UI with no error).
func TestParse_BooleanAttrsAcceptXSDOneZeroForm(t *testing.T) {
	const xmlDoc = `<ShowPlanXML Version="1.599" Build="17.0.4055.5">
<BatchSequence><Batch><Statements>
<StmtSimple StatementText="x">
<QueryPlan><RelOp NodeId="0" PhysicalOp="Nested Loops" LogicalOp="Inner Join" Parallel="1">
<Warnings NoJoinPredicate="1"></Warnings>
</RelOp></QueryPlan>
</StmtSimple>
</Statements></Batch></BatchSequence></ShowPlanXML>`

	plan, err := Parse([]byte(xmlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root := plan.Statements[0].Root
	if root == nil {
		t.Fatal("Root = nil")
	}
	if !root.Parallel {
		t.Error("root.Parallel = false, want true for Parallel=\"1\"")
	}
	if len(root.Warnings) != 1 || root.Warnings[0] != "NoJoinPredicate" {
		t.Errorf("root.Warnings = %v, want [NoJoinPredicate]", root.Warnings)
	}
}

func TestParse_InvalidDocument(t *testing.T) {
	if _, err := Parse([]byte("<NotAPlan/>")); err == nil {
		t.Error("Parse(garbage) returned nil error, want an error")
	}
	if _, err := Parse([]byte("not even xml")); err == nil {
		t.Error("Parse(non-xml) returned nil error, want an error")
	}
}

func TestIndent(t *testing.T) {
	single := `<a x="1"><b>text</b><c/></a>`
	got := Indent(single)
	want := "<a x=\"1\">\n  <b>\n    text\n  </b>\n  <c/>\n</a>"
	if got != want {
		t.Errorf("Indent(single-line) =\n%s\nwant\n%s", got, want)
	}

	already := "<a>\n  <b/>\n</a>"
	if got := Indent(already); got != already {
		t.Errorf("Indent(already multi-line) changed the input:\n%s", got)
	}
}

func TestIndent_RealPlanRoundTrips(t *testing.T) {
	plan := mustParseFile(t, "testdata/estimated_plan.xml")
	out := Indent(plan.XML)
	// The fixture is single-line apart from one ordinary trailing newline
	// at EOF — a regression here (the "already multi-line" heuristic
	// matching on that trailing newline alone and returning the input
	// unindented) would leave out looking deceptively "multi-line" too,
	// since strings.Contains(out, "\n") is trivially true either way; the
	// real signal is a newline appearing before the input's own trailing
	// one, i.e. one actually inserted between two tags.
	if !strings.Contains(strings.TrimRight(out, "\r\n"), "\n") {
		t.Fatal("Indent did not insert any newlines between tags — looks like a no-op")
	}
	if strings.Count(out, "<RelOp") != strings.Count(plan.XML, "<RelOp") {
		t.Error("Indent changed the number of <RelOp> occurrences")
	}
	if strings.Count(out, "\n") < 10 {
		t.Errorf("Indent produced only %d newlines for a %d-tag document, want roughly one per tag",
			strings.Count(out, "\n"), strings.Count(plan.XML, "<"))
	}
}

func TestIndent_TrailingNewlineAloneIsNotMultiLine(t *testing.T) {
	// A single-line document with just a plain EOF newline (the common
	// case for any file written by a normal editor/tool) must still be
	// indented — it must not be mistaken for an already-formatted
	// multi-line document. Regression test for the bug caught by manual
	// inspection of plandemo's XML tab: the naive "contains \">\\n\""
	// check matched this trailing newline and returned the input as-is.
	single := "<a x=\"1\"><b>text</b></a>\n"
	got := Indent(single)
	want := "<a x=\"1\">\n  <b>\n    text\n  </b>\n</a>"
	if got != want {
		t.Errorf("Indent(single-line with trailing EOF newline) =\n%q\nwant\n%q", got, want)
	}
}
