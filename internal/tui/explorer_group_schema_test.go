package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
)

// groupTree is a connection's tree holding one loaded folder of type ft in
// database db1, its children set by SetChildren, as a load would.
func groupTree(t *testing.T, a *App, ft NodeType, children ...*explorerNode) *explorerNode {
	t.Helper()
	sc, _ := newGConn(t)
	root := a.explorer.AddRoot("server-one", sc)
	folder := &explorerNode{label: nodeTypeName(ft), explorerItem: explorerItem{Expanded: true}, data: nodeData{Type: ft, DBName: "db1", conn: sc}}
	a.explorer.SetChildren(root, []*explorerNode{folder})
	root.Expanded = true
	a.explorer.SetChildren(folder, children)
	return folder
}

func obj(nt NodeType, schema, name string) *explorerNode {
	return &explorerNode{label: schema + "." + name, data: nodeData{Type: nt, DBName: "db1", Schema: schema, Name: name}}
}

func subFolder(nt NodeType) *explorerNode {
	return &explorerNode{label: nodeTypeName(nt), data: nodeData{Type: nt, DBName: "db1"}}
}

// shape renders a folder's children as labels, a group's as "schema[a b]".
func shape(n *explorerNode) []string {
	var out []string
	for _, c := range n.Children {
		if c.data.Type != NodeSchemaGroup {
			out = append(out, c.label)
			continue
		}
		s := c.label + "["
		for i, gc := range c.Children {
			if i > 0 {
				s += " "
			}
			s += gc.label
		}
		out = append(out, s+"]")
	}
	return out
}

// TestApplyGroupingShapes is the transform over the folder shapes a load can
// produce: sub-folders and error rows stay first, objects bucket by schema in
// loaded order, groups sort case-insensitively with the server's order breaking
// ties, and OFF leaves the loaded list untouched.
func TestApplyGroupingShapes(t *testing.T) {
	tables := func() []*explorerNode {
		return []*explorerNode{
			subFolder(NodeSystemTables), subFolder(NodeFileTables),
			obj(NodeTable, "sales", "Orders"), obj(NodeTable, "dbo", "A"),
			obj(NodeTable, "Sales", "Big"), obj(NodeTable, "dbo", "B"), obj(NodeTable, "app", "Z"),
		}
	}
	errRow := func() *explorerNode { return &explorerNode{label: "Error: boom", data: nodeData{Type: NodeError}} }
	tests := []struct {
		name     string
		ft       NodeType
		children func() []*explorerNode
		on, off  []string
	}{
		{"mixed schemas, sub-folders first, case-only differences", NodeTables, tables,
			[]string{"System Tables", "FileTables", "app[app.Z]", "dbo[dbo.A dbo.B]", "sales[sales.Orders]", "Sales[Sales.Big]"},
			[]string{"System Tables", "FileTables", "sales.Orders", "dbo.A", "Sales.Big", "dbo.B", "app.Z"}},
		{"error row", NodeViews, func() []*explorerNode { return []*explorerNode{errRow()} },
			[]string{"Error: boom"}, []string{"Error: boom"}},
		{"empty folder", NodeSynonyms, func() []*explorerNode { return nil }, nil, nil},
		{"only a sub-folder", NodeStoredProcedures,
			func() []*explorerNode { return []*explorerNode{subFolder(NodeSystemProcedures)} },
			[]string{"System Procedures"}, []string{"System Procedures"}},
		{"single schema", NodeFunctions, func() []*explorerNode {
			return []*explorerNode{subFolder(NodeSystemFunctions), obj(NodeFunction, "dbo", "f2"), obj(NodeFunction, "dbo", "f1")}
		}, []string{"System Functions", "dbo[dbo.f2 dbo.f1]"}, []string{"System Functions", "dbo.f2", "dbo.f1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, on := range []bool{false, true} {
				a := newTestApp()
				a.groupBySchema = on
				folder := groupTree(t, a, tt.ft, tt.children()...)
				want := tt.off
				if on {
					want = tt.on
				}
				if got := shape(folder); !slices.Equal(got, want) {
					t.Errorf("groupBySchema=%v: children = %q, want %q", on, got, want)
				}
				for _, g := range folder.Children {
					if g.data.Type != NodeSchemaGroup {
						continue
					}
					if g.Parent != folder || !g.Loaded || g.data.GroupOf != tt.ft || g.data.DBName != "db1" ||
						g.data.conn != folder.data.conn || a.explorer.nodeByID(g.ID()) != g {
						t.Errorf("group %s: parent/Loaded/GroupOf/DBName/conn/byID not set up: %+v", g.label, g.data)
					}
					for _, c := range g.Children {
						if c.Parent != g {
							t.Errorf("%s's parent is not its group", c.label)
						}
					}
				}
			}
		})
	}
}

// TestGroupingToggleIsLossless flips a loaded Tables folder ON, OFF and ON
// again: the same object nodes come back in the loaded order with their ids,
// expanded flags and loaded subtrees, an open group reopens, and a retired
// group leaves byID with the next rebuild.
func TestGroupingToggleIsLossless(t *testing.T) {
	a := newTestApp()
	oe := a.explorer
	loaded := []*explorerNode{subFolder(NodeSystemTables),
		obj(NodeTable, "sales", "Orders"), obj(NodeTable, "dbo", "A"), obj(NodeTable, "dbo", "B")}
	folder := groupTree(t, a, NodeTables, loaded...)
	orders := loaded[1]
	orders.Expanded = true
	cols := &explorerNode{label: "Columns", data: nodeData{Type: NodeColumns, DBName: "db1", Schema: "sales", Name: "Orders"}}
	oe.SetChildren(orders, []*explorerNode{cols})
	ids := map[*explorerNode]int{}
	for _, c := range loaded {
		ids[c] = c.ID()
	}

	a.groupBySchema = true
	oe.regroupAll()
	sales := folder.Children[2]
	if sales.label != "sales" {
		t.Fatalf("children = %q, want the sales group last", shape(folder))
	}
	sales.Expanded = true
	salesID := sales.ID()
	oe.regroupAll() // ON again: the live group is reused, not rebuilt
	if folder.Children[2] != sales || sales.Retired() {
		t.Fatal("re-applying ON replaced a live group")
	}

	a.groupBySchema = false
	oe.regroupAll()
	if !slices.Equal(folder.Children, loaded) {
		t.Fatalf("OFF: children = %q, want the loaded list %q", shape(folder), shape(&explorerNode{explorerItem: explorerItem{Children: loaded}}))
	}
	if !sales.Retired() || oe.nodeByID(salesID) != nil {
		t.Error("OFF: the sales group is still live or still in byID after the rebuild")
	}

	a.groupBySchema = true
	oe.regroupAll()
	if got, want := shape(folder), []string{"System Tables", "dbo[dbo.A dbo.B]", "sales[sales.Orders]"}; !slices.Equal(got, want) {
		t.Fatalf("ON again: children = %q, want %q", got, want)
	}
	if g := folder.Children[2]; !g.Expanded || g == sales {
		t.Errorf("ON again: sales group expanded = %v, reused retired node = %v; want a new, expanded group", g.Expanded, g == sales)
	}
	for _, c := range loaded {
		if c.ID() != ids[c] || oe.nodeByID(c.ID()) != c || c.Retired() {
			t.Errorf("%s: id %d → %d, byID kept = %v, retired = %v", c.label, ids[c], c.ID(), oe.nodeByID(c.ID()) == c, c.Retired())
		}
	}
	if !orders.Expanded || !orders.Loaded || len(orders.Children) != 1 || orders.Children[0] != cols {
		t.Error("the expanded table lost its loaded Columns subtree across the toggle")
	}
	if !slices.Equal(folder.flat, loaded) {
		t.Error("flat no longer holds the loaded list")
	}
}

// TestGroupingSurvivesReload: a Refresh of a grouped folder rebuilds its groups
// from the new load, reopening the ones that were open, so the object a rename
// just reloaded stays in view.
func TestGroupingSurvivesReload(t *testing.T) {
	a := newTestApp()
	a.groupBySchema = true
	folder := groupTree(t, a, NodeViews, obj(NodeView, "dbo", "v1"), obj(NodeView, "app", "v2"))
	folder.Expanded = false            // so Reload starts no background load
	folder.Children[1].Expanded = true // dbo
	old := folder.Children[1]

	a.explorer.Reload(folder)
	if len(folder.flat) != 0 || len(folder.Children) != 0 || !old.Retired() {
		t.Fatal("Reload left the folder's flat list, children or groups in place")
	}
	a.explorer.SetChildren(folder, []*explorerNode{obj(NodeView, "dbo", "v1_renamed"), obj(NodeView, "app", "v2")})
	if got, want := shape(folder), []string{"app[app.v2]", "dbo[dbo.v1_renamed]"}; !slices.Equal(got, want) {
		t.Fatalf("children = %q, want %q", got, want)
	}
	if !folder.Children[1].Expanded || folder.Children[0].Expanded {
		t.Error("the reloaded groups did not keep which one was open")
	}
}

// TestGroupingLeavesOtherFamiliesFlat: with the flag ON, a folder outside
// groupedFolderTypes keeps its loaded children as they are.
func TestGroupingLeavesOtherFamiliesFlat(t *testing.T) {
	for _, ft := range []NodeType{NodeSystemDataTypes, NodeRules, NodeDefaults, NodeBrokerQueues, NodeTypes} {
		a := newTestApp()
		a.groupBySchema = true
		children := []*explorerNode{obj(NodeView, "sys", "b"), obj(NodeView, "dbo", "a")}
		folder := groupTree(t, a, ft, children...)
		a.explorer.regroupAll()
		if !slices.Equal(folder.Children, children) || folder.flat != nil {
			t.Errorf("%s: children = %q, flat = %v; want the loaded list and no flat", nodeTypeName(ft), shape(folder), folder.flat)
		}
	}
}

// TestGroupingFilteredFolder: groups are built from the filtered children, so
// a schema with no match gets none, and "(filtered)" stays on the folder.
func TestGroupingFilteredFolder(t *testing.T) {
	a := newTestApp()
	a.groupBySchema = true
	f := &nodeFilter{criteria: []filterCriterion{{prop: filterProp{id: fpName, kind: filterText}, op: opContains, value: "ord"}}}
	children := filterChildren(f, []*explorerNode{subFolder(NodeSystemTables),
		obj(NodeTable, "dbo", "Orders"), obj(NodeTable, "app", "Users"), obj(NodeTable, "sales", "OrderLines")})
	folder := groupTree(t, a, NodeTables, children...)
	folder.data.Filter = f
	if got, want := shape(folder), []string{"System Tables", "dbo[dbo.Orders]", "sales[sales.OrderLines]"}; !slices.Equal(got, want) {
		t.Errorf("children = %q, want %q", got, want)
	}
	labels := map[string]bool{}
	var walk func(*explorerNode, int)
	walk = func(n *explorerNode, depth int) {
		labels[a.explorer.row(n, depth).Label] = true
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(folder.Parent, 0)
	if !labels["Tables (filtered)"] || labels["dbo (filtered)"] || labels["sales (filtered)"] {
		t.Errorf("tree labels %v: want the suffix on the folder only", labels)
	}
}

// TestListingParent: an object under a schema group is listed by the folder
// above the group; every other node's listing parent is its parent.
func TestListingParent(t *testing.T) {
	a := newTestApp()
	a.groupBySchema = true
	folder := groupTree(t, a, NodeTables, subFolder(NodeSystemTables), obj(NodeTable, "dbo", "A"))
	sys, dbo := folder.Children[0], folder.Children[1]
	tbl := dbo.Children[0]
	root := folder.Parent
	for _, tc := range []struct {
		name    string
		n, want *explorerNode
	}{
		{"object under a group", tbl, folder},
		{"the group", dbo, folder},
		{"sub-folder", sys, folder},
		{"folder", folder, root},
		{"root", root, nil},
	} {
		if got := tc.n.listingParent(); got != tc.want {
			t.Errorf("%s: listingParent = %v, want %v", tc.name, got, tc.want)
		}
	}
	a.groupBySchema = false
	a.explorer.regroupAll()
	if got := tbl.listingParent(); got != folder {
		t.Errorf("OFF: listingParent = %v, want the folder", got)
	}
}

// TestWriteUnderGroupReloadsFolder: the reload after a rename or delete of an
// object under a group (ReloadFolders on its listingParent) reloads the folder,
// and a Refresh of the group itself, from its menu or F5 (Reload), is the
// folder's: the group has no loader, so reloading it would empty it for good.
func TestWriteUnderGroupReloadsFolder(t *testing.T) {
	refreshes := map[string]func(a *App, g, tbl *explorerNode){
		"ReloadFolders on the object's listingParent": func(a *App, _, tbl *explorerNode) {
			a.explorer.ReloadFolders(resolveConn(tbl), sameNodeAs(tbl.listingParent()))
		},
		"Reload of the group (F5)": func(a *App, g, _ *explorerNode) { a.explorer.Reload(g) },
		"the group's menu Refresh": func(a *App, g, _ *explorerNode) {
			for _, it := range a.nodeMenuItems(g) {
				if it.Label == refreshMenuLabel {
					it.Action()
					return
				}
			}
			t.Fatal("the schema group's menu has no Refresh")
		},
	}
	for name, refresh := range refreshes {
		t.Run(name, func(t *testing.T) {
			a := newTestApp()
			a.groupBySchema = true
			folder := groupTree(t, a, NodeTables, obj(NodeTable, "dbo", "A"), obj(NodeTable, "app", "B"))
			folder.Expanded = false // so Reload starts no background load
			g := folder.Children[1] // dbo
			refresh(a, g, g.Children[0])
			if folder.Loaded || len(folder.Children) != 0 || folder.flat != nil || !g.Retired() {
				t.Fatalf("the folder was not reloaded: Loaded=%v children=%q flat=%d group retired=%v",
					folder.Loaded, shape(folder), len(folder.flat), g.Retired())
			}
		})
	}
}

// TestMoveToSchemaRebuckets: the folder reload after Move to Schema lists the
// object under its new schema; the group it left, now empty, is gone, and the
// new schema's group is built for it.
func TestMoveToSchemaRebuckets(t *testing.T) {
	a := newTestApp()
	a.groupBySchema = true
	folder := groupTree(t, a, NodeViews, obj(NodeView, "dbo", "v1"), obj(NodeView, "app", "v2"))
	folder.Expanded = false
	moved := folder.Children[1].Children[0] // dbo.v1
	a.explorer.ReloadFolders(resolveConn(moved), sameNodeAs(moved.listingParent()))
	a.explorer.SetChildren(folder, []*explorerNode{obj(NodeView, "app", "v1"), obj(NodeView, "app", "v2")})
	if got, want := shape(folder), []string{"app[app.v1 app.v2]"}; !slices.Equal(got, want) {
		t.Errorf("children = %q, want %q", got, want)
	}
	for _, g := range folder.groups {
		if g.data.Schema == "dbo" && !g.Retired() {
			t.Error("the emptied dbo group is still live")
		}
	}
}

// TestMoveToSchemaFollowsSelection: Move to Schema lands the object in another
// schema's group, collapsed here; the folder reload opens that group and the
// selection follows the object into it instead of falling to the old group.
func TestMoveToSchemaFollowsSelection(t *testing.T) {
	a := newTestApp()
	a.groupBySchema = true
	folder := groupTree(t, a, NodeViews, obj(NodeView, "dbo", "v1"), obj(NodeView, "dbo", "v3"), obj(NodeView, "app", "v2"))
	dbo := folder.Children[1]
	dbo.Expanded = true
	a.explorer.rebuild()
	moved := dbo.Children[0] // dbo.v1
	a.explorer.view.SelectID(moved.ID())

	to := moved.data.key()
	to.Schema = "app"
	a.explorer.Follow(moved, to)
	folder.Loaded = false
	a.explorer.tree.DropChildren(folder)
	a.explorer.SetChildren(folder, []*explorerNode{obj(NodeView, "app", "v1"), obj(NodeView, "dbo", "v3"), obj(NodeView, "app", "v2")})

	got := a.explorer.Selected()
	if got == nil || got.data.Schema != "app" || got.data.Name != "v1" {
		t.Fatalf("Selected() = %v, want app.v1", got)
	}
	if g := got.Parent; g.data.Type != NodeSchemaGroup || g.data.Schema != "app" || g.Retired() || !g.Expanded {
		t.Errorf("app.v1's parent = %q (type %v, retired %v, expanded %v), want the open app group",
			g.label, g.data.Type, g.Retired(), g.Expanded)
	}
}

// TestGroupingToggleKeepsSelection: toggling ON opens the group holding the
// selection, whether it is an object, a node under one or a "Loading..." row,
// and leaves the rest closed; the selection stays on the same node (same id,
// no reselect). Toggling OFF keeps an object selected, and moves a selection on
// a group, which is retired, to its folder.
func TestGroupingToggleKeepsSelection(t *testing.T) {
	selected := func(a *App) *explorerNode {
		if tn := a.explorer.view.SelectedNode(); tn != nil {
			if n, ok := tn.Tag.(*explorerNode); ok {
				return n
			}
			return tn.Tag.(controls.TreePlaceholder[*explorerNode]).Owner
		}
		return nil
	}
	setup := func(t *testing.T) (a *App, folder, orders, cols, users *explorerNode) {
		a = newTestApp()
		orders, users = obj(NodeTable, "sales", "Orders"), obj(NodeTable, "app", "Users")
		folder = groupTree(t, a, NodeTables, subFolder(NodeSystemTables), orders, obj(NodeTable, "dbo", "A"), users)
		orders.Expanded = true
		cols = &explorerNode{label: "Columns", data: nodeData{Type: NodeColumns, DBName: "db1", Schema: "sales", Name: "Orders"}}
		a.explorer.SetChildren(orders, []*explorerNode{cols})
		users.Expanded = true // unloaded: shows a "Loading..." row
		a.explorer.rebuild()
		return
	}
	group := func(folder *explorerNode, schema string) *explorerNode {
		for _, c := range folder.Children {
			if c.data.Type == NodeSchemaGroup && c.data.Schema == schema {
				return c
			}
		}
		t.Fatalf("no %s group in %q", schema, shape(folder))
		return nil
	}

	for _, tc := range []struct {
		name   string
		pick   func(orders, cols, users *explorerNode) (controls.TreeNodeID, *explorerNode)
		opened string
	}{
		{"object", func(o, _, _ *explorerNode) (controls.TreeNodeID, *explorerNode) { return o.ID(), o }, "sales"},
		{"node under an object", func(_, c, _ *explorerNode) (controls.TreeNodeID, *explorerNode) { return c.ID(), c }, "sales"},
		{"Loading... row", func(_, _, u *explorerNode) (controls.TreeNodeID, *explorerNode) { return u.PlaceholderID(), u }, "app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, folder, orders, cols, users := setup(t)
			id, want := tc.pick(orders, cols, users)
			a.explorer.view.SelectID(id)
			a.groupBySchema = true
			a.explorer.regroupAll()
			for _, g := range folder.Children {
				if g.data.Type == NodeSchemaGroup && g.Expanded != (g.data.Schema == tc.opened) {
					t.Errorf("group %s expanded = %v; want only %s open", g.label, g.Expanded, tc.opened)
				}
			}
			if tn := a.explorer.view.SelectedNode(); tn == nil || tn.ID != id || selected(a) != want {
				t.Errorf("selection moved off %s", want.label)
			}
		})
	}

	t.Run("OFF keeps an object", func(t *testing.T) {
		a, _, orders, _, _ := setup(t)
		a.explorer.view.SelectID(orders.ID())
		a.groupBySchema = true
		a.explorer.regroupAll()
		a.groupBySchema = false
		a.explorer.regroupAll()
		if selected(a) != orders {
			t.Errorf("selection = %v, want sales.Orders", selected(a))
		}
	})
	t.Run("OFF moves a group's selection to its folder", func(t *testing.T) {
		a, folder, _, _, _ := setup(t)
		a.groupBySchema = true
		a.explorer.regroupAll()
		a.explorer.view.SelectID(group(folder, "dbo").ID())
		a.groupBySchema = false
		a.explorer.regroupAll()
		if selected(a) != folder {
			t.Errorf("selection = %v, want the Tables folder", selected(a))
		}
	})
}

// Alt+G (and Alt+Shift+G) toggles Group by Schema whatever has focus, flipping
// the View menu item's check and regrouping the loaded tree. Plain 'g' is text, and
// Ctrl+Alt+G (AltGr on many layouts) must not toggle.
func TestAltGTogglesGroupBySchema(t *testing.T) {
	a := newKeyTestApp(t)
	a.newQueryPanel() // the focus plain 'g' falls through to
	folder := groupTree(t, a, NodeTables, obj(NodeTable, "sales", "Orders"), obj(NodeTable, "dbo", "A"))
	// Built once: the item's Checked reads the state at draw time, so the
	// menu follows the toggle without a rebuild.
	menus := a.buildMenus()
	viewChecked := func() bool {
		for _, m := range menus {
			if m.Label != "View" {
				continue
			}
			for _, it := range m.Items {
				if it.Shortcut == "Alt+G" && it.Label == "Group by Schema" && it.Checked != nil {
					return it.Checked()
				}
			}
		}
		t.Fatal("no Group by Schema toggle (Alt+G) in the View menu")
		return false
	}
	check := func(step string, on bool, want []string) {
		t.Helper()
		if a.groupBySchema != on || viewChecked() != on || !slices.Equal(shape(folder), want) {
			t.Fatalf("%s: groupBySchema=%v checked=%v tree=%q; want %v %q",
				step, a.groupBySchema, viewChecked(), shape(folder), on, want)
		}
	}
	flat := []string{"sales.Orders", "dbo.A"}
	grouped := []string{"dbo[dbo.A]", "sales[sales.Orders]"}

	check("startup", false, flat)
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, "g", tcell.ModAlt))
	check("Alt+G", true, grouped)
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, "g", tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, "g", tcell.ModAlt|tcell.ModCtrl),
	} {
		a.handleKey(ev)
		check(ev.Name(), true, grouped)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, "G", tcell.ModAlt|tcell.ModShift))
	check("Alt+Shift+G", false, flat)
}

// TestSchemaGroupDrag: a schema group is the one folder that drags, and it
// drops its bracketed schema name, quoted like any other identifier. A press on
// its row arms the drag of the group itself, not of an object inside it.
func TestSchemaGroupDrag(t *testing.T) {
	if !isDraggableNode(NodeSchemaGroup) {
		t.Fatal("NodeSchemaGroup is not draggable")
	}
	if isDraggableNode(NodeTables) {
		t.Fatal("the folder a group sits in became draggable")
	}
	for schema, want := range map[string]string{"dbo": "[dbo]", "we]ird": "[we]]ird]"} {
		g := &explorerNode{label: schema, data: nodeData{Type: NodeSchemaGroup, Schema: schema, GroupOf: NodeTables}}
		if got := explorerDragText(g); got != want {
			t.Errorf("explorerDragText(group %q) = %q, want %q", schema, got, want)
		}
	}

	a := newDragTestApp(t, 0)
	a.groupBySchema = true
	root := a.explorer.Selected()
	folder := &explorerNode{label: "Tables", explorerItem: explorerItem{Expanded: true}, data: nodeData{Type: NodeTables, DBName: "db1", conn: root.data.conn}}
	a.explorer.SetChildren(root, []*explorerNode{folder})
	a.explorer.SetChildren(folder, []*explorerNode{obj(NodeTable, "sales", "t1")})
	y := -1
	for row := range dragTestScreenH {
		if n := a.explorer.NodeAt(dragTestLabelX, row); n != nil && n.data.Type == NodeSchemaGroup {
			y = row
			break
		}
	}
	if y < 0 {
		t.Fatal("no schema group row in the explorer pane")
	}
	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.Button1, tcell.ModNone))
	if draggedNode(a) == nil || draggedNode(a).data.Type != NodeSchemaGroup {
		t.Fatalf("press on a schema group row armed %+v, want the group", draggedNode(a))
	}
	if got := explorerDragText(draggedNode(a)); got != "[sales]" {
		t.Errorf("armed group drops %q, want [sales]", got)
	}
	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.ButtonNone, tcell.ModNone))
}

func TestExplorerCloseAndViewMenuRestore(t *testing.T) {
	a := newDragTestApp(t, 1)
	selected := a.explorer.Selected()
	qp := NewQueryPanel(a, "Query 1")
	a.panels.AddPanel(qp)
	a.explorerSplit.SetRatio(0.4)
	a.layoutAll()
	a.focusExplorer()
	before := a.explorer.view.Bounds()
	a.explorer.Draw(a.screen)
	var header strings.Builder
	for x := range before.W {
		str, _, _ := a.screen.Get(x, before.Y)
		header.WriteString(str)
	}
	x := strings.Index(header.String(), "[x]")
	group := strings.Index(header.String(), "[⋮]")
	if group < 0 || x <= group || !strings.HasSuffix(header.String(), "[x]┐") {
		t.Fatalf("missing close button after grouping button in right corner: %q", header.String())
	}
	x = core.DisplayWidth(header.String()[:x])
	a.handleMouse(tcell.NewEventMouse(x+1, before.Y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x+1, before.Y+3, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x+1, before.Y+3, tcell.ButtonNone, tcell.ModNone))
	if !a.explorerHidden || a.focus != focusOnPanels || a.explorer.view.Focused() {
		t.Fatal("close did not hide the explorer and focus the panel")
	}
	if got := a.panels.Bounds(); got.X != 0 || got.W != dragTestScreenW {
		t.Fatalf("hidden explorer still occupies panel space: %+v", got)
	}
	if a.explorer.Selected() != selected || len(a.connections) != 1 {
		t.Fatal("hiding changed the tree or connections")
	}
	item := findMenuBarItem(t, a.buildMenus(), "View", "Object Explorer")
	item.Action()
	if a.explorerHidden || a.focus != focusOnExplorer || !a.explorer.view.Focused() {
		t.Fatal("View > Object Explorer did not show and focus the explorer")
	}
	if got := a.explorer.view.Bounds(); got != before || a.explorer.Selected() != selected {
		t.Fatalf("reopening lost geometry or selection: %+v", got)
	}
	a.focusPanels()
	item.Action()
	if a.explorerHidden || a.focus != focusOnExplorer || a.explorer.view.Bounds() != before {
		t.Fatal("View > Object Explorer changed the visible explorer instead of focusing it")
	}
}

func TestHiddenExplorerDoesNotTakeSplitterMouseEvents(t *testing.T) {
	a := newDragTestApp(t, 1)
	a.panels.AddPanel(NewQueryPanel(a, "Query 1"))
	a.layoutAll()
	bar := a.explorerSplit.SplitPos()
	ratio := a.explorerSplit.Ratio()
	a.hideExplorer()
	a.handleMouse(tcell.NewEventMouse(bar, 4, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(bar+10, 4, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(bar+10, 4, tcell.ButtonNone, tcell.ModNone))
	if !a.explorerHidden || a.focus != focusOnPanels || a.explorerSplit.Ratio() != ratio {
		t.Fatal("the hidden explorer or its splitter handled panel mouse events")
	}
}

// TestGroupBySchemaTitleButton: the "[⋮]"/"[Σ]" on the Object Explorer's top border
// is the mouse surface of Alt+G. A click toggles once per press, never arms an
// explorer drag (the press is on the border, not a row) and leaves a visible
// selection where it was; the button shows the state.
func TestGroupBySchemaTitleButton(t *testing.T) {
	a := newDragTestApp(t, 0)
	root := a.explorer.Selected()
	folder := &explorerNode{label: "Tables", explorerItem: explorerItem{Expanded: true}, data: nodeData{Type: NodeTables, DBName: "db1", conn: root.data.conn}}
	a.explorer.SetChildren(root, []*explorerNode{folder})
	t1 := obj(NodeTable, "sales", "t1")
	a.explorer.SetChildren(folder, []*explorerNode{t1})
	a.explorer.view.SelectID(t1.ID())

	// The grouping button sits before the close button in the top border.
	bx, by := dragTestSplitX-1-3-4+1, 1
	press := func(step string, wantOn bool) {
		t.Helper()
		a.handleMouse(tcell.NewEventMouse(bx, by, tcell.Button1, tcell.ModNone))
		a.handleMouse(tcell.NewEventMouse(bx, by, tcell.Button1, tcell.ModNone)) // a resend of the hold
		if draggedNode(a) != nil {
			t.Fatalf("%s: a press on the button armed a drag of %q", step, draggedNode(a).label)
		}
		a.handleMouse(tcell.NewEventMouse(bx, by, tcell.ButtonNone, tcell.ModNone))
		if a.groupBySchema != wantOn {
			t.Fatalf("%s: groupBySchema = %v, want %v", step, a.groupBySchema, wantOn)
		}
		if a.explorer.Selected() != t1 {
			t.Fatalf("%s: selection = %v, want the table", step, a.explorer.Selected())
		}
	}
	press("first click", true)
	if want := []string{"sales[sales.t1]"}; !slices.Equal(shape(folder), want) {
		t.Errorf("tree after click = %q, want %q", shape(folder), want)
	}
	press("second click", false)
	if want := []string{"sales.t1"}; !slices.Equal(shape(folder), want) {
		t.Errorf("tree after second click = %q, want %q", shape(folder), want)
	}
}

// TestGroupingNestedFolder: a grouped folder inside a grouped folder (System
// Tables under Tables) regroups on its own. The outer folder keeps it as a
// sub-folder, first; the toggle reaches it through the outer folder's
// children, and OFF restores both flat lists.
func TestGroupingNestedFolder(t *testing.T) {
	a := newTestApp()
	sys := subFolder(NodeSystemTables)
	sys.Expanded = true
	folder := groupTree(t, a, NodeTables, sys, obj(NodeTable, "dbo", "A"))
	a.explorer.SetChildren(sys, []*explorerNode{
		obj(NodeTable, "sys", "trace_xe_action_map"), obj(NodeTable, "INFORMATION_SCHEMA", "x"),
	})

	a.groupBySchema = true
	a.explorer.regroupAll()
	if got, want := shape(folder), []string{"System Tables", "dbo[dbo.A]"}; !slices.Equal(got, want) {
		t.Errorf("Tables ON = %q, want %q", got, want)
	}
	if got, want := shape(sys), []string{"INFORMATION_SCHEMA[INFORMATION_SCHEMA.x]", "sys[sys.trace_xe_action_map]"}; !slices.Equal(got, want) {
		t.Errorf("System Tables ON = %q, want %q", got, want)
	}

	a.groupBySchema = false
	a.explorer.regroupAll()
	if got, want := shape(sys), []string{"sys.trace_xe_action_map", "INFORMATION_SCHEMA.x"}; !slices.Equal(got, want) {
		t.Errorf("System Tables OFF = %q, want %q", got, want)
	}
}
