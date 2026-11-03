package tui

import (
	"testing"
)

// A double-click in Object Explorer Details navigates into the row's object,
// as SSMS's does: the tree selects the child the row stands for. The pane's
// labels are its own (the Tables folder writes "dbo.Alpha" where a loader may
// not), so the row object's key is what finds the child, the label only where
// a view has no row objects.

// drillTree is a connected server with one Tables folder holding objs as its
// children, shown in a Details pane listing the same objects under labels the
// tree doesn't use. loaded false leaves the folder's children unloaded, as
// before its first expand.
func drillTree(t *testing.T, objs []nodeData, loaded bool) (*App, *DetailBrowser, *explorerNode) {
	t.Helper()
	a := newTestApp()
	sc := addTestConn(a, "srv")
	root := a.explorer.roots[0]
	folder := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: sc}}
	a.explorer.SetChildren(root, []*explorerNode{folder})
	root.expanded = true
	if loaded {
		a.explorer.SetChildren(folder, drillChildren(objs))
	}
	pane := a.newDetailBrowser()
	pane.SetBounds(0, 0, 80, 20)
	pane.currentNode = folder
	rows := make([][]string, len(objs))
	for i, o := range objs {
		rows[i] = []string{"pane label " + o.Name}
	}
	pane.showRows([]string{"Name"}, rows, objs)
	return a, pane, folder
}

func drillChildren(objs []nodeData) []*explorerNode {
	var children []*explorerNode
	for _, o := range objs {
		children = append(children, &explorerNode{label: o.Schema + "." + o.Name, data: o})
	}
	return children
}

func drillObjs() []nodeData {
	return []nodeData{
		{Type: NodeTable, Schema: "dbo", Name: "Alpha"},
		{Type: NodeTable, Schema: "sales", Name: "Beta"},
		{Type: NodeTable, Schema: "sales", Name: "Gamma"},
	}
}

// doubleClickRow is the grid's double-click on row, after its first press
// selected the row.
func doubleClickRow(pane *DetailBrowser, row int) {
	pane.grid.SetSelectedCell(row, 0)
	pane.grid.OnRowDoubleClick(row)
}

func TestDetailsDoubleClickSelectsTheRowsChild(t *testing.T) {
	a, pane, folder := drillTree(t, drillObjs(), true)
	doubleClickRow(pane, 1)
	got := a.explorer.Selected()
	if got == nil || got.data.Name != "Beta" {
		t.Fatalf("selected %v, want the Beta table", got)
	}
	if !folder.expanded {
		t.Error("the folder stayed collapsed over the selected child")
	}
	if pane.grid.OverlayActive() {
		t.Error("a row that navigated also opened Show Value")
	}
}

func TestDetailsDoubleClickWaitsForTheFoldersLoad(t *testing.T) {
	a, pane, folder := drillTree(t, drillObjs(), false)
	// Expanded with nothing loaded: its load is in flight, so the drill must
	// wait for it, not start another (which a test App can't run).
	folder.expanded = true
	doubleClickRow(pane, 2)
	if a.explorer.drill == nil {
		t.Fatal("no drill pending on an unloaded folder")
	}
	a.explorer.SetChildren(folder, drillChildren(drillObjs()))
	if got := a.explorer.Selected(); got == nil || got.data.Name != "Gamma" {
		t.Fatalf("selected %v once the load landed, want the Gamma table", got)
	}
	if a.explorer.drill != nil {
		t.Error("the drill outlived its load")
	}
}

func TestASelectionBeforeTheLoadCancelsTheDrill(t *testing.T) {
	a, pane, folder := drillTree(t, drillObjs(), false)
	folder.expanded = true
	doubleClickRow(pane, 2)
	root := a.explorer.roots[0]
	a.explorer.view.SelectID(root.id) // the user moved on
	a.explorer.SetChildren(folder, drillChildren(drillObjs()))
	if got := a.explorer.Selected(); got != root {
		t.Errorf("selected %v, want the server the user picked", got)
	}
}

func TestDetailsDoubleClickOpensTheChildsSchemaGroup(t *testing.T) {
	a, pane, folder := drillTree(t, drillObjs(), false)
	a.groupBySchema = true
	a.explorer.SetChildren(folder, drillChildren(drillObjs()))
	doubleClickRow(pane, 1)
	got := a.explorer.Selected()
	if got == nil || got.data.Name != "Beta" {
		t.Fatalf("selected %v, want the Beta table", got)
	}
	if g := got.parent; g.data.Type != NodeSchemaGroup || !g.expanded {
		t.Errorf("the table's parent is %v (expanded %v), want its expanded schema group", g.label, g.expanded)
	}
}

func TestADetailsRowStandingForNoChildShowsItsValue(t *testing.T) {
	a, pane, _ := drillTree(t, drillObjs(), true)
	pane.showRows([]string{"Property", "Value"}, [][]string{{"Name", "Tables"}}, nil)
	before := a.explorer.Selected()
	doubleClickRow(pane, 0)
	if a.explorer.Selected() != before {
		t.Error("a Property/Value row moved the tree selection")
	}
	if !pane.grid.OverlayActive() {
		t.Error("a Property/Value row's double-click did not show its value")
	}
}

// The server's and a database's views are Property/Value records whose leading
// rows are the node's folders: those navigate (matching a label the tree
// decorated with a state), the properties under them don't.
func TestARecordsFolderRowNavigatesAndItsPropertiesDont(t *testing.T) {
	a := newTestApp()
	addTestConn(a, "srv")
	root := a.explorer.roots[0]
	agent := &explorerNode{label: agentRootLabel + " (Stopped)", data: nodeData{Type: NodeAgentJobs}}
	security := &explorerNode{label: "Security", data: nodeData{Type: NodeSecurity}}
	a.explorer.SetChildren(root, []*explorerNode{security, agent})
	pane := a.newDetailBrowser()
	pane.SetBounds(0, 0, 80, 20)
	pane.currentNode = root
	pane.showRows([]string{"Property", "Value"}, [][]string{
		{"Security", detailFolderValue},
		{agentRootLabel, detailFolderValue},
		{"Security", "a property that happens to share a folder's name"},
	}, nil)

	doubleClickRow(pane, 1)
	if got := a.explorer.Selected(); got != agent {
		t.Fatalf("selected %v, want the (Stopped) Agent node", got)
	}
	a.explorer.view.SelectID(root.id)
	doubleClickRow(pane, 2)
	if got := a.explorer.Selected(); got != root {
		t.Errorf("a property row navigated to %v", got)
	}
}

func TestChildFolderRowsListsFoldersNotNotes(t *testing.T) {
	server := childFolderRows(t.Context(), nil, &explorerNode{data: nodeData{Type: NodeServer}})
	if len(server) == 0 || server[0][0] != "Databases" || server[0][1] != detailFolderValue {
		t.Errorf("server folders = %v, want Databases first, as folder rows", server)
	}
	offline := &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "d", IsOffline: true}}
	if rows := childFolderRows(t.Context(), nil, offline); len(rows) != 0 {
		t.Errorf("an offline database listed %v, want no folders (its note is not one)", rows)
	}
}
