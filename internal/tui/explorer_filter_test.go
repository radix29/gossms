package tui

import (
	"testing"
	"time"
)

func nameProp() filterProp   { return filterProp{id: fpName, name: "Name", kind: filterText} }
func schemaProp() filterProp { return filterProp{id: fpSchema, name: "Schema", kind: filterText} }
func dateProp() filterProp {
	return filterProp{id: fpCreationDate, name: "Creation Date", kind: filterDate}
}
func memProp() filterProp {
	return filterProp{id: fpMemoryOptimized, name: "Is Memory Optimized", kind: filterBool}
}

func TestMatchCriterionText(t *testing.T) {
	d := nodeData{Schema: "Sales", Name: "Customer"}
	tests := []struct {
		prop  filterProp
		op    filterOp
		value string
		want  bool
	}{
		{nameProp(), opContains, "ust", true},
		{nameProp(), opContains, "CUST", true}, // case-insensitive, like the server's default collation
		{nameProp(), opContains, "order", false},
		{nameProp(), opNotContains, "order", true},
		{nameProp(), opEquals, "customer", true},
		{nameProp(), opEquals, "cust", false},
		{nameProp(), opNotEquals, "cust", true},
		{schemaProp(), opEquals, "sales", true},
		{schemaProp(), opEquals, "dbo", false},
		// The label is "Sales.Customer", but Name matching must see the bare
		// name only — otherwise every object matches its own schema prefix.
		{nameProp(), opContains, "Sales.", false},
	}
	for _, tt := range tests {
		c := filterCriterion{prop: tt.prop, op: tt.op, value: tt.value}
		if got := matchCriterion(c, d); got != tt.want {
			t.Errorf("%s %v %q = %v, want %v", tt.prop.name, tt.op, tt.value, got, tt.want)
		}
	}
}

func TestMatchCriterionDate(t *testing.T) {
	d := nodeData{CreateDate: time.Date(2026, 3, 4, 13, 45, 0, 0, time.UTC)}
	tests := []struct {
		op    filterOp
		value string
		want  bool
	}{
		// Equals means the whole calendar day, not midnight exactly.
		{opEquals, "2026-03-04", true},
		{opOn, "2026-03-04", true},
		{opEquals, "2026-03-05", false},
		{opBefore, "2026-03-05", true},
		{opBefore, "2026-03-04", false},
		{opAfter, "2026-03-03", true},
		{opAfter, "2026-03-04", false},
		{opEquals, "not a date", false},
	}
	for _, tt := range tests {
		c := filterCriterion{prop: dateProp(), op: tt.op, value: tt.value}
		if got := matchCriterion(c, d); got != tt.want {
			t.Errorf("Creation Date %v %q = %v, want %v", tt.op, tt.value, got, tt.want)
		}
	}
	// A node whose loader never fetched a creation date can't satisfy a date
	// criterion — it must be filtered out, not silently kept.
	c := filterCriterion{prop: dateProp(), op: opEquals, value: "2026-03-04"}
	if matchCriterion(c, nodeData{}) {
		t.Error("zero CreateDate matched a date criterion")
	}
}

func TestMatchCriterionBool(t *testing.T) {
	on := nodeData{IsMemoryOptimized: true}
	off := nodeData{}
	tests := []struct {
		d     nodeData
		op    filterOp
		value string
		want  bool
	}{
		{on, opEquals, "True", true},
		{on, opEquals, "yes", true},
		{on, opEquals, "0", false},
		{off, opEquals, "false", true},
		{on, opNotEquals, "true", false},
		{off, opNotEquals, "true", true},
		{on, opEquals, "maybe", false},
	}
	for _, tt := range tests {
		c := filterCriterion{prop: memProp(), op: tt.op, value: tt.value}
		if got := matchCriterion(c, tt.d); got != tt.want {
			t.Errorf("Is Memory Optimized %v %q = %v, want %v", tt.op, tt.value, got, tt.want)
		}
	}
}

// Multiple criteria narrow (AND), the way SSMS's own grid of rows does.
func TestNodeFilterMatchesAllCriteria(t *testing.T) {
	f := &nodeFilter{criteria: []filterCriterion{
		{prop: nameProp(), op: opContains, value: "cust"},
		{prop: schemaProp(), op: opEquals, value: "Sales"},
	}}
	if !f.matches(nodeData{Schema: "Sales", Name: "Customer"}) {
		t.Error("node satisfying both criteria was rejected")
	}
	if f.matches(nodeData{Schema: "dbo", Name: "Customer"}) {
		t.Error("node failing the Schema criterion was kept")
	}
	var none *nodeFilter
	if !none.matches(nodeData{Name: "anything"}) {
		t.Error("a nil filter must keep everything")
	}
}

// filterChildren applies to the objects in a folder only: sub-folders and
// the error placeholder stay, or a filtered Views folder would lose its
// "System Views" child and a failed expand would look like an empty one.
func TestFilterChildrenKeepsFoldersAndErrors(t *testing.T) {
	node := &explorerNode{
		label: "Views",
		data: nodeData{
			Type:   NodeViews,
			Filter: &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "cust"}}},
		},
	}
	children := []*explorerNode{
		{label: "System Views", data: nodeData{Type: NodeSystemViews}},
		{label: "Sales.Customer", data: nodeData{Type: NodeView, Schema: "Sales", Name: "Customer"}},
		{label: "dbo.Orders", data: nodeData{Type: NodeView, Schema: "dbo", Name: "Orders"}},
		{label: "boom", data: nodeData{Type: NodeError}},
	}
	got := filterChildren(node, children)
	want := []string{"System Views", "Sales.Customer", "boom"}
	if len(got) != len(want) {
		t.Fatalf("got %d children, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].label != w {
			t.Errorf("child[%d] = %q, want %q", i, got[i].label, w)
		}
	}

	node.data.Filter = nil
	if len(filterChildren(node, children)) != len(children) {
		t.Error("an unfiltered folder must keep every child")
	}
}

// Every property filterProps offers must be one the folder's own loader can
// actually populate — a Creation Date criterion on a folder whose nodes
// carry no date rejects every row, which reads as "the filter is broken".
func TestFilterPropsAreBackedByNodeData(t *testing.T) {
	for _, t2 := range []NodeType{
		NodeTables, NodeViews, NodeSystemViews, NodeStoredProcedures, NodeSystemProcedures,
		NodeFunctions, NodeSystemFunctions, NodeSequences, NodeSynonyms, NodeTriggers,
		NodeDatabases, NodeSystemDatabases, NodeLogins, NodeUsers, NodeDatabaseRoles,
		NodeSchemas, NodeServerRoles,
	} {
		props := filterProps(t2)
		if len(props) == 0 {
			t.Errorf("filterProps(%v) is empty — the Filter menu items would never appear", t2)
			continue
		}
		if props[0].id != fpName {
			t.Errorf("filterProps(%v)[0] = %v, want Name first", t2, props[0].name)
		}
		for _, p := range props {
			if len(filterOps(p.kind)) == 0 {
				t.Errorf("filterProps(%v): %q offers no operators", t2, p.name)
			}
		}
	}
	if got := filterProps(NodeTable); got != nil {
		t.Errorf("filterProps(NodeTable) = %v, want nil (a table is not a folder)", got)
	}
}

// The "(filtered)" suffix is drawn from nodeData.Filter rather than written
// into the node's label, so clearing a filter restores the original label.
func TestFlattenMarksFilteredFolder(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	tables := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, Loaded: true, conn: sc}}

	labelOf := func() string { return a.explorer.flatten(nil, tables, 0)[0].Label }
	if got := labelOf(); got != "Tables" {
		t.Fatalf("unfiltered label = %q, want \"Tables\"", got)
	}
	tables.data.Filter = &nodeFilter{
		criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: "cust"}},
	}
	if got := labelOf(); got != "Tables (filtered)" {
		t.Errorf("filtered label = %q, want \"Tables (filtered)\"", got)
	}
	tables.data.Filter = nil
	if got := labelOf(); got != "Tables" {
		t.Errorf("label after clearing = %q, want \"Tables\"", got)
	}
}
