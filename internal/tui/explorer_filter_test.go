package tui

import (
	"reflect"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
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
	got := filterChildren(node.data.Filter, children)
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
	if len(filterChildren(node.data.Filter, children)) != len(children) {
		t.Error("an unfiltered folder must keep every child")
	}
}

// filterableFolders is every folder type that offers a filter. Listed here so
// a new one has to be added deliberately, alongside the properties its loader
// populates.
var filterableFolders = []NodeType{
	NodeTables, NodeViews, NodeSystemViews, NodeStoredProcedures, NodeSystemProcedures,
	NodeFunctions, NodeSystemFunctions, NodeSequences, NodeSynonyms, NodeTriggers,
	NodeDatabases, NodeSystemDatabases, NodeLogins, NodeUsers, NodeDatabaseRoles,
	NodeSchemas, NodeServerRoles,
}

// propBacking is the nodeData field each filterable property is supposed to
// read, as a setter that touches nothing else, plus a criterion value that
// must match what the setter wrote.
var propBacking = map[filterPropID]struct {
	set   func(*nodeData)
	value string
}{
	fpName:            {func(d *nodeData) { d.Name = "widget" }, "widget"},
	fpSchema:          {func(d *nodeData) { d.Schema = "widget" }, "widget"},
	fpCreationDate:    {func(d *nodeData) { d.CreateDate = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) }, "2026-03-04"},
	fpMemoryOptimized: {func(d *nodeData) { d.IsMemoryOptimized = true }, "True"},
}

// Every property a folder offers must actually be wired through to the
// nodeData field it names. nodeData is a 20-field union and the wiring is
// spread over filterTextValue/filterBoolValue/matchDate, so a property added
// to filterProps without its matching case falls through to Name and silently
// filters on the wrong field — or, for a folder whose loader leaves the field
// zero, rejects every row and reads as "the filter is broken".
//
// The teeth are the positive assertion: with only its own field populated, a
// property's default criterion must match. A property reading someone else's
// field sees the zero value there and fails it.
func TestFilterPropsAreBackedByNodeData(t *testing.T) {
	for _, ft := range filterableFolders {
		props := filterProps(ft)
		if len(props) == 0 {
			t.Errorf("filterProps(%v) is empty — the Filter menu items would never appear", ft)
			continue
		}
		if props[0].id != fpName {
			t.Errorf("filterProps(%v)[0] = %v, want Name first", ft, props[0].name)
		}
		for _, p := range props {
			ops := filterOps(p.kind)
			if len(ops) == 0 {
				t.Errorf("filterProps(%v): %q offers no operators", ft, p.name)
				continue
			}
			b, ok := propBacking[p.id]
			if !ok {
				t.Errorf("filterProps(%v) offers %q, but no nodeData field is declared for it — wire it into filterTextValue/filterBoolValue and add it to propBacking", ft, p.name)
				continue
			}
			var d nodeData
			b.set(&d)
			c := filterCriterion{prop: p, op: ops[0], value: b.value}
			if !matchCriterion(c, d) {
				t.Errorf("filterProps(%v): %q %v %q rejected a node with only that field set — the property reads the wrong nodeData field", ft, p.name, ops[0], b.value)
			}
			if matchCriterion(c, nodeData{}) {
				t.Errorf("filterProps(%v): %q %v %q matched an empty node", ft, p.name, ops[0], b.value)
			}
		}
	}
	if got := filterProps(NodeTable); got != nil {
		t.Errorf("filterProps(NodeTable) = %v, want nil (a table is not a folder)", got)
	}
}

// Every folder that offers a filter must be a container the tree treats as a
// folder — filterChildren exempts container children from filtering, so a
// filterable type that isn't one would have its own sub-folders filtered away.
func TestFilterableFoldersAreContainers(t *testing.T) {
	for _, ft := range filterableFolders {
		if !isContainerNode(ft) {
			t.Errorf("%v offers a filter but isn't a container node", ft)
		}
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

// -- push-down ----------------------------------------------------------------

// The translation is only safe if it means the same thing the client-side pass
// means. These pin the mapping criterion by criterion; the equivalence itself
// (server answer == client answer) is asserted live in gosmo's
// live_objectfilter_test.go.
func TestNodeFilterPushdown(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(filterDateLayout, s)
		if err != nil {
			t.Fatalf("bad test date %q: %v", s, err)
		}
		return d
	}
	pName := nameProp()
	pSchema := schemaProp()
	pCreated := dateProp()
	pMem := filterProp{id: fpMemoryOptimized, name: "Is Memory Optimized", kind: filterBool}
	yes, no := true, false

	for _, c := range []struct {
		name   string
		filter *nodeFilter
		want   gosmo.ObjectFilter
		wantOK bool
	}{
		{
			name:   "no filter",
			filter: nil,
			wantOK: true,
		},
		{
			name:   "name contains",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pName, op: opContains, value: "cust"}}},
			want:   gosmo.ObjectFilter{Name: []gosmo.TextCriterion{{Op: gosmo.TextContains, Value: "cust"}}},
			wantOK: true,
		},
		{
			// matchText trims the typed value; the pattern sent to the server
			// has to be trimmed the same way or the server narrows further
			// than the client would and drops rows.
			name:   "the value is trimmed like matchText trims it",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pName, op: opContains, value: "  cust  "}}},
			want:   gosmo.ObjectFilter{Name: []gosmo.TextCriterion{{Op: gosmo.TextContains, Value: "cust"}}},
			wantOK: true,
		},
		{
			name:   "schema criteria land on Schema, not Name",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pSchema, op: opEquals, value: "sales"}}},
			want:   gosmo.ObjectFilter{Schema: []gosmo.TextCriterion{{Op: gosmo.TextEquals, Value: "sales"}}},
			wantOK: true,
		},
		{
			name:   "not contains",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pName, op: opNotContains, value: "tmp"}}},
			want:   gosmo.ObjectFilter{Name: []gosmo.TextCriterion{{Op: gosmo.TextNotContains, Value: "tmp"}}},
			wantOK: true,
		},
		{
			name:   "not equals",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pName, op: opNotEquals, value: "Orders"}}},
			want:   gosmo.ObjectFilter{Name: []gosmo.TextCriterion{{Op: gosmo.TextNotEquals, Value: "Orders"}}},
			wantOK: true,
		},
		{
			// The dialog offers Equals and On for a date and means one thing
			// by both, exactly as matchDate treats them.
			name:   "date equals is date on",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pCreated, op: opEquals, value: "2026-08-20"}}},
			want:   gosmo.ObjectFilter{Created: []gosmo.DateCriterion{{Op: gosmo.DateOn, Day: day("2026-08-20")}}},
			wantOK: true,
		},
		{
			name:   "date before",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pCreated, op: opBefore, value: "2026-08-20"}}},
			want:   gosmo.ObjectFilter{Created: []gosmo.DateCriterion{{Op: gosmo.DateBefore, Day: day("2026-08-20")}}},
			wantOK: true,
		},
		{
			name:   "date after",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pCreated, op: opAfter, value: "2026-08-20"}}},
			want:   gosmo.ObjectFilter{Created: []gosmo.DateCriterion{{Op: gosmo.DateAfter, Day: day("2026-08-20")}}},
			wantOK: true,
		},
		{
			name:   "memory optimized equals true",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pMem, op: opEquals, value: "True"}}},
			want:   gosmo.ObjectFilter{MemoryOptimized: &yes},
			wantOK: true,
		},
		{
			// "does not equal True" is "equals false" — there is no third
			// state, and the server has no NOT form for a bit column here.
			name:   "memory optimized not equals inverts",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pMem, op: opNotEquals, value: "true"}}},
			want:   gosmo.ObjectFilter{MemoryOptimized: &no},
			wantOK: true,
		},
		{
			name: "several criteria",
			filter: &nodeFilter{criteria: []filterCriterion{
				{prop: pName, op: opContains, value: "cust"},
				{prop: pSchema, op: opEquals, value: "dbo"},
			}},
			want: gosmo.ObjectFilter{
				Name:   []gosmo.TextCriterion{{Op: gosmo.TextContains, Value: "cust"}},
				Schema: []gosmo.TextCriterion{{Op: gosmo.TextEquals, Value: "dbo"}},
			},
			wantOK: true,
		},
		{
			// The client rejects an unparseable date and matches nothing; the
			// server would be handed a zero day, so the whole filter is
			// refused and the folder is read unfiltered instead.
			name:   "an unparseable date is not pushable",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pCreated, op: opOn, value: "yesterday"}}},
			wantOK: false,
		},
		{
			name:   "an unparseable bool is not pushable",
			filter: &nodeFilter{criteria: []filterCriterion{{prop: pMem, op: opEquals, value: "maybe"}}},
			wantOK: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.filter.pushdown()
			if ok != c.wantOK {
				t.Fatalf("pushdown ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(got.Name, c.want.Name) || !reflect.DeepEqual(got.Schema, c.want.Schema) {
				t.Errorf("text criteria = %+v / %+v, want %+v / %+v", got.Name, got.Schema, c.want.Name, c.want.Schema)
			}
			if !reflect.DeepEqual(got.Created, c.want.Created) {
				t.Errorf("date criteria = %+v, want %+v", got.Created, c.want.Created)
			}
			switch {
			case c.want.MemoryOptimized == nil && got.MemoryOptimized != nil:
				t.Errorf("MemoryOptimized = %v, want unset", *got.MemoryOptimized)
			case c.want.MemoryOptimized != nil && got.MemoryOptimized == nil:
				t.Errorf("MemoryOptimized unset, want %v", *c.want.MemoryOptimized)
			case c.want.MemoryOptimized != nil && *got.MemoryOptimized != *c.want.MemoryOptimized:
				t.Errorf("MemoryOptimized = %v, want %v", *got.MemoryOptimized, *c.want.MemoryOptimized)
			}
		})
	}
}

// serverFilter is what the loaders call: a filter that cannot be pushed down
// becomes an empty one, so the folder is read whole and narrowed by
// filterChildren rather than being narrowed wrongly at the server.
func TestServerFilterFallsBackToTheWholeFolder(t *testing.T) {
	bad := &nodeFilter{criteria: []filterCriterion{{
		prop:  filterProp{id: fpCreationDate, kind: filterDate},
		op:    opOn,
		value: "not-a-date",
	}}}
	if got := serverFilter(bad); !got.Empty() {
		t.Errorf("serverFilter = %+v, want an empty filter for an unpushable criterion", got)
	}
	if got := serverFilter(nil); !got.Empty() {
		t.Errorf("serverFilter(nil) = %+v, want empty", got)
	}
}
