package tui

import (
	"context"
	"database/sql/driver"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// newConnectedNode builds a standalone explorerNode of an unhandled type
// (falls to fetchNodeDetails' fallback, which never touches sc.Server)
// wired to a fake, "open" connection — safe to exercise ShowNodeDetails'
// cache/dispatch logic without a real gosmo.Server or network access.
func newConnectedNode(label string) (*explorerNode, *dbconn.ServerConn) {
	sc := &dbconn.ServerConn{}
	return &explorerNode{label: label, data: nodeData{Type: NodeColumn, Name: label, conn: sc}}, sc
}

func TestShowNodeDetailsUsesCache(t *testing.T) {
	a := newTestApp()
	node, _ := newConnectedNode("cached-node")

	db := NewDetailBrowser("test")
	db.cache[node] = &detailResult{
		cols: []string{"Property", "Value"},
		rows: [][]string{{"Name", "cached-node"}, {"Type", "Column"}},
	}

	db.ShowNodeDetails(a, node)

	if got := db.grid.Row(0); got[1] != "cached-node" {
		t.Fatalf("grid row 0 = %v, want cached data to be shown synchronously", got)
	}
	// A cache hit must not disturb the "Loading..." status a real fetch
	// would set — it never runs fetchNodeDetails at all.
	if db.grid.Status() == "Loading..." {
		t.Error("status = Loading..., want the cached result applied instead of a fresh fetch")
	}
}

// RefreshCurrent must refetch the node the panel is displaying, not
// whatever the explorer has selected — pin it by pointing the panel at one
// node while a second, unrelated node stays cached and untouched.
func TestRefreshCurrentRefetchesThePanelsOwnNode(t *testing.T) {
	a := newTestApp()
	shown, _ := newConnectedNode("shown-node")
	other, _ := newConnectedNode("other-node")

	db := NewDetailBrowser("test")
	db.cache[shown] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "stale"}}}
	db.cache[other] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "other"}}}
	db.ShowNodeDetails(a, shown)

	db.RefreshCurrent(a)

	if _, ok := db.cache[shown]; ok {
		t.Error("cache still has an entry for the displayed node after RefreshCurrent")
	}
	if _, ok := db.cache[other]; !ok {
		t.Error("RefreshCurrent dropped the cache entry for a node it isn't showing")
	}
	if db.grid.Status() != "Loading..." {
		t.Errorf("status = %q, want Loading... after RefreshCurrent", db.grid.Status())
	}
}

// RefreshCurrent with nothing displayed must be a no-op, not a fetch for a
// nil node — the panel is in that state right after PurgeConn empties it.
func TestRefreshCurrentWithNoNodeIsANoOp(t *testing.T) {
	a := newTestApp()
	db := NewDetailBrowser("test")

	db.RefreshCurrent(a)

	if db.grid.Status() == "Loading..." {
		t.Error("RefreshCurrent started a fetch with no node displayed")
	}
}

func TestInvalidateRefetchesCurrentlyDisplayedNode(t *testing.T) {
	a := newTestApp()
	node, _ := newConnectedNode("current-node")

	db := NewDetailBrowser("test")
	db.cache[node] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "stale"}}}
	db.ShowNodeDetails(a, node) // cache hit, sets db.currentNode = node

	db.Invalidate(a, node)

	if _, ok := db.cache[node]; ok {
		t.Error("cache still has an entry for node after Invalidate")
	}
	if db.grid.Status() != "Loading..." {
		t.Errorf("status = %q, want Loading... (Invalidate should refetch the currently-displayed node)", db.grid.Status())
	}
}

func TestInvalidateOfNonCurrentNodeOnlyDropsCache(t *testing.T) {
	a := newTestApp()
	nodeA, _ := newConnectedNode("node-a")
	nodeB, _ := newConnectedNode("node-b")

	db := NewDetailBrowser("test")
	db.cache[nodeA] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "a"}}}
	db.cache[nodeB] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "b"}}}
	db.ShowNodeDetails(a, nodeB) // nodeB is now current, shown from cache

	db.Invalidate(a, nodeA) // not the displayed node

	if _, ok := db.cache[nodeA]; ok {
		t.Error("cache still has an entry for nodeA after Invalidate")
	}
	// nodeB's cache and on-screen data must be untouched — Invalidate only
	// forces a refetch for the node currently on screen.
	if _, ok := db.cache[nodeB]; !ok {
		t.Error("Invalidate(nodeA) incorrectly dropped nodeB's cache entry")
	}
	if got := db.grid.Row(0); got[1] != "b" {
		t.Errorf("grid row 0 = %v, want nodeB's cached data still shown", got)
	}
}

func TestInvalidateNilReceiverIsSafe(t *testing.T) {
	var db *DetailBrowser
	a := newTestApp()
	node, _ := newConnectedNode("n")
	db.Invalidate(a, node) // must not panic
}

// TestFetchNodeDetailsFallsBackToChildList checks the "if not explicitly
// defined, just list the child objects" fallback: a folder node type with
// no purpose-built case in fetchNodeDetails (Server Objects, here) shows its
// children's labels instead of the leaf-style Property/Value grid that made
// no sense for a folder.
func TestFetchNodeDetailsFallsBackToChildList(t *testing.T) {
	sc := &dbconn.ServerConn{}
	node := &explorerNode{label: "Server Objects", data: nodeData{Type: NodeServerObjects, conn: sc}}

	var objs []nodeData
	cols, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("fetchNodeDetails: %v", err)
	}
	if len(cols) != 1 || cols[0] != "Name" {
		t.Fatalf("cols = %v, want [Name]", cols)
	}
	var gotLinkedServers bool
	for _, r := range rows {
		if len(r) == 1 && r[0] == "Linked Servers" {
			gotLinkedServers = true
		}
	}
	if !gotLinkedServers {
		t.Errorf("rows = %v, want a \"Linked Servers\" row (Server Objects' only child now)", rows)
	}
}

// TestFetchNodeDetailsLeafKeepsPropertyValue checks a genuine leaf type
// (no children) still gets the original Property/Value grid, not the
// child-list fallback — mirrors newConnectedNode's own "never touches
// sc.Server" comment above, since a leaf must not call into childLoaders
// at all.
func TestFetchNodeDetailsLeafKeepsPropertyValue(t *testing.T) {
	sc := &dbconn.ServerConn{}
	node := &explorerNode{label: "my_col", data: nodeData{Type: NodeColumn, conn: sc}}

	var objs []nodeData
	cols, _, err := fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("fetchNodeDetails: %v", err)
	}
	if len(cols) != 2 || cols[0] != "Property" || cols[1] != "Value" {
		t.Fatalf("cols = %v, want [Property Value]", cols)
	}
}

func TestShowNodeDetailsNotConnected(t *testing.T) {
	a := newTestApp()
	sc := &dbconn.ServerConn{}
	sc.Close() // marks it closed, so isConnected reports false
	node := &explorerNode{label: "n", data: nodeData{Type: NodeColumn, conn: sc}}

	db := NewDetailBrowser("test")
	db.ShowNodeDetails(a, node)

	if got := db.grid.Row(0); got[1] != "Not connected" {
		t.Errorf("grid row 0 = %v, want a Not connected status row", got)
	}
}

// TestRefreshButtonFiresOnceOnPress checks the title bar's refresh button
// runs OnRefresh on a fresh Button1 press and not again on the held-button
// motion events tcell resends before the release — the mouseDragging latch.
func TestRefreshButtonFiresOnceOnPress(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 80, 20)
	fired := 0
	db.OnRefresh = func() { fired++ }

	x, y := db.refreshRect.X, db.refreshRect.Y
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if fired != 1 {
		t.Fatalf("OnRefresh fired %d times on press+hold, want 1", fired)
	}

	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, 0))
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if fired != 2 {
		t.Fatalf("OnRefresh fired %d times after release + fresh press, want 2", fired)
	}
}

// TestRefreshButtonMissDelegatesToGrid checks a press below the title bar
// still reaches the data grid.
func TestRefreshButtonMissDelegatesToGrid(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 80, 20)
	db.OnRefresh = func() { t.Error("OnRefresh fired for a press outside the button") }
	db.grid.SetData([]string{"Property", "Value"}, [][]string{{"Name", "a"}, {"Type", "b"}})

	db.HandleMouse(tcell.NewEventMouse(2, db.refreshRect.Y+4, tcell.Button1, 0))
	if db.grid.SelectedRow() != 1 {
		t.Errorf("grid selected row = %d, want 1 (press should reach the grid)", db.grid.SelectedRow())
	}
}

// TestDetailBrowserShowValueReadsTheStatementByQueryID. The Detail Browser's
// grid holds only [][]string, shared with every other node type, so the
// flattened cell is all it has — and opening that cell in a query panel ships
// a batch whose FROM clause is inside a line comment. The row's query id is
// the handle back to the real statement.
func TestDetailBrowserShowValueReadsTheStatementByQueryID(t *testing.T) {
	const stored = "SELECT 1 -- pick one\nFROM dbo.t"

	a := newTestApp()
	sc, inst := newFakeConn(t, fakeResponse{
		match: "WHERE  q.query_id = @p1", cols: 2,
		rows: [][]driver.Value{{stored, "dbo.q"}},
	})
	db := a.newDetailBrowser()

	node := &explorerNode{
		label: "Top Resource Consuming Queries",
		data: nodeData{Type: NodeQueryStoreReport, Name: "Top Resource Consuming Queries",
			DBName: "appdb", conn: sc},
	}
	flat := queryStoreOneLine(stored)
	db.cache[node] = &detailResult{
		cols: []string{qsQueryIDColumn, "Object", qsQueryColumn},
		rows: [][]string{{"7", "dbo.p", "SELECT 9"}, {"12", "dbo.q", flat}},
	}
	db.ShowNodeDetails(a, node)

	// The *second* row: a hook that ignored the cursor and read row 0 would
	// pass with the selection left at the top.
	queryCol := db.grid.ColumnIndex(qsQueryColumn)
	db.grid.SetSelectedCell(1, queryCol)

	before := a.panels.Count()
	if !db.showQueryStoreValue(a, queryCol, qsQueryColumn, flat) {
		t.Fatal("the hook declined the Query column of a Query Store report")
	}
	drainUntil(t, a, func() bool { return a.panels.Count() > before }, "the statement panel to open")

	qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel)
	if !ok {
		t.Fatalf("the new panel is %T, want a query panel", a.panels.PanelAt(a.panels.Count()-1))
	}
	if got := qp.editor.Text(); got != stored {
		t.Errorf("the panel holds %q, want the stored statement %q", got, stored)
	}
	// It asked about query 12, not query 7 — a hook that read row 0 gets the
	// same answer out of this fake, so the bound id is the only witness.
	args, ok := inst.ReadArgs("WHERE  q.query_id = @p1")
	if !ok || len(args) != 1 {
		t.Fatalf("the text read bound %v, want one parameter", args)
	}
	if got, _ := args[0].Value.(int64); got != 12 {
		t.Errorf("the text read asked about query %v, want 12 — the selected row", args[0].Value)
	}
}

// TestDetailBrowserShowValueLeavesOtherGridsAlone: every other node type's
// grid has no query id to read by, and its cells are the only text there is.
func TestDetailBrowserShowValueLeavesOtherGridsAlone(t *testing.T) {
	a := newTestApp()
	db := a.newDetailBrowser()
	node, _ := newConnectedNode("a-column")
	db.cache[node] = &detailResult{
		cols: []string{qsQueryIDColumn, qsQueryColumn},
		rows: [][]string{{"12", "SELECT 1"}},
	}
	db.ShowNodeDetails(a, node)
	db.grid.SetSelectedCell(0, 1)

	before := a.panels.Count()
	// Claimed by showSQLCellValue's own path — which opens the cell, because
	// on a grid that is not a Query Store report the cell is the whole value.
	db.showQueryStoreValue(a, 1, qsQueryColumn, "SELECT 1")
	if a.panels.Count() != before+1 {
		t.Fatalf("panel count %d, want %d", a.panels.Count(), before+1)
	}
	if qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel); ok {
		if got := qp.editor.Text(); got != "SELECT 1" {
			t.Errorf("the panel holds %q, want the cell", got)
		}
	}
}

func TestUsableDiskVolumesDropsNonsenseAndDuplicates(t *testing.T) {
	// The first two rows are what an Azure Managed Instance actually reports
	// (t-qmi-01, 2026-09-09): one mount point, twice, each claiming more free
	// space than the volume holds.
	vols := []gosmo.DiskVolumeInfo{
		{MountPoint: `C:\`, TotalMB: 192, AvailableMB: 65344, SamplePath: `C:\ManagedDisks\a.mdf`},
		{MountPoint: `C:\`, TotalMB: 192, AvailableMB: 98112, SamplePath: `C:\SFApplications\tempdb.mdf`},
		{MountPoint: `D:\`, TotalMB: 0, AvailableMB: 0},
		{MountPoint: `E:\`, TotalMB: 4096, AvailableMB: 1024},
		{MountPoint: `E:\`, TotalMB: 4096, AvailableMB: 2048},
		{MountPoint: "", VolumeName: "", TotalMB: 100, AvailableMB: 10, SamplePath: "/var/opt/mssql/a.mdf"},
		{MountPoint: "", VolumeName: "", TotalMB: 100, AvailableMB: 10, SamplePath: "/var/opt/mssql/b.mdf"},
	}
	got := usableDiskVolumes(vols)

	want := []string{`E:\`, "", ""}
	if len(got) != len(want) {
		t.Fatalf("usableDiskVolumes returned %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].MountPoint != w {
			t.Errorf("row %d mount point = %q, want %q", i, got[i].MountPoint, w)
		}
	}
	// The unnamed volume can't be deduped — the sample path is per file, not
	// per volume — so both of its rows survive deliberately.
	if got[1].SamplePath == got[2].SamplePath {
		t.Errorf("unnamed volumes collapsed into one row: %+v", got)
	}
	if got[0].AvailableMB != 1024 {
		t.Errorf("kept the wrong duplicate for E:\\: available = %v, want 1024", got[0].AvailableMB)
	}
}

// B1: the "Not connected" branch is the one path onto the screen that goes
// through neither applyResult nor postPartial nor showEmpty, so it used to
// leave the previous node's row→object mapping, chart strip and pinned
// tooltip in place under a one-row status grid. The visible half is a stale
// composition bar; the sharp half is detailMenuItems, which reads rowObjs and
// currentNode together and so offered Delete on the *previous* node's first
// object, resolved against the new node's connection.
func TestShowNodeDetailsNotConnectedDropsThePreviousNode(t *testing.T) {
	a := newTestApp()
	live := &dbconn.ServerConn{}
	shown := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: live}}

	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = shown
	db.applyResult(&detailResult{
		cols:   []string{"Name", "Type"},
		rows:   [][]string{{"dbo.orders", "Table"}},
		objs:   []nodeData{{Type: NodeTable, Name: "orders", Schema: "dbo", conn: live}},
		charts: stripCharts(),
	})
	// Pin a readout on the strip, as a click on a bar does.
	strip := db.chartsRect()
	if strip.IsZero() {
		t.Fatal("no chart strip to pin a tooltip on")
	}
	db.tooltip = db.pinChartTooltip(strip.X+1, strip.Bottom()-1)
	if db.tooltip == nil {
		t.Fatal("pinChartTooltip returned nil; the test cannot show the pin surviving")
	}
	if len(db.rowObjs) == 0 || len(db.charts) == 0 {
		t.Fatalf("setup left rowObjs=%d charts=%d, want both non-empty", len(db.rowObjs), len(db.charts))
	}

	dead := &dbconn.ServerConn{}
	dead.Close() // marks it closed, so isConnected reports false
	next := &explorerNode{label: "Views", data: nodeData{Type: NodeViews, conn: dead}}
	db.ShowNodeDetails(a, next)

	if got := db.grid.Row(0); got[1] != "Not connected" {
		t.Fatalf("grid row 0 = %v, want a Not connected status row", got)
	}
	if db.rowObjs != nil {
		t.Errorf("rowObjs = %v after a disconnected node, want nil — it still describes %q's rows",
			db.rowObjs, shown.label)
	}
	if db.charts != nil {
		t.Errorf("charts = %d panels after a disconnected node, want none — the previous node's "+
			"composition bars stay drawn under the status grid", len(db.charts))
	}
	if db.tooltip != nil {
		t.Errorf("tooltip = %+v after a disconnected node, want nil", db.tooltip)
	}
	if items := a.detailMenuItems(db); items != nil {
		t.Errorf("detailMenuItems = %d items on a Not connected pane, want none — that is Delete "+
			"offered on the previous node's object", len(items))
	}
}

// T1: the loading branch is B1's twin. A node that isn't cached used to get
// only a "Loading..." status, so until its fetch landed — up to
// childFetchTimeout — the previous node's rows, objects, charts and tooltip
// stayed live, and detailMenuItems paired those objects with the *new* node's
// connection: Delete dropped server A's objects on server B.
func TestShowNodeDetailsLoadingDropsThePreviousNode(t *testing.T) {
	a := newTestApp()
	serverA := &dbconn.ServerConn{}
	shown := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: serverA}}

	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = shown
	db.applyResult(&detailResult{
		cols:   []string{"Name", "Type"},
		rows:   [][]string{{"dbo.orders", "Table"}},
		objs:   []nodeData{{Type: NodeTable, Name: "orders", Schema: "dbo"}},
		charts: stripCharts(),
	})
	strip := db.chartsRect()
	if strip.IsZero() {
		t.Fatal("no chart strip to pin a tooltip on")
	}
	db.tooltip = db.pinChartTooltip(strip.X+1, strip.Bottom()-1)
	if db.tooltip == nil {
		t.Fatal("pinChartTooltip returned nil; the test cannot show the pin surviving")
	}
	if items := a.detailMenuItems(db); len(items) == 0 {
		t.Fatal("setup offers no Delete on the shown node; the test cannot show it surviving")
	}

	// A node on another, live server, with no cache entry: the loading branch.
	next, _ := newConnectedNode("orders")
	db.ShowNodeDetails(a, next)

	if got := db.grid.Status(); got != "Loading..." {
		t.Fatalf("status = %q, want Loading... — the test is not exercising the loading branch", got)
	}
	if got := db.grid.Row(0); got != nil {
		t.Errorf("grid row 0 = %v while loading another node, want none — it is %q's", got, shown.label)
	}
	if db.rowObjs != nil {
		t.Errorf("rowObjs = %v while loading another node, want nil", db.rowObjs)
	}
	if db.charts != nil {
		t.Errorf("charts = %d panels while loading another node, want none", len(db.charts))
	}
	if db.tooltip != nil {
		t.Errorf("tooltip = %+v while loading another node, want nil", db.tooltip)
	}
	if items := a.detailMenuItems(db); items != nil {
		t.Errorf("detailMenuItems = %d items while loading, want none — that is Delete of %q's "+
			"object on the new node's server", len(items), shown.label)
	}
}

// A Refresh reloads the node on screen through the same loading branch. Its
// rows are still that node's, so they stay up rather than flashing empty, but
// their objects go: nothing says which of them the reload will find gone.
func TestRefreshKeepsRowsButDropsTheirObjects(t *testing.T) {
	a := newTestApp()
	node, _ := newConnectedNode("orders")

	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = node
	db.applyResult(&detailResult{
		cols: []string{"Name", "Type"},
		rows: [][]string{{"dbo.orders", "Table"}},
		objs: []nodeData{{Type: NodeTable, Name: "orders", Schema: "dbo"}},
	})
	db.RefreshCurrent(a)

	if got := db.grid.Status(); got != "Loading..." {
		t.Fatalf("status = %q, want Loading...", got)
	}
	if got := db.grid.Row(0); got == nil {
		t.Error("grid row 0 gone during a Refresh, want the node's row kept")
	}
	if items := a.detailMenuItems(db); items != nil {
		t.Errorf("detailMenuItems = %d items during a Refresh, want none", len(items))
	}
}

// The defence behind both resets: row objects installed for one node are
// never offered for Delete while another is current, whatever path left them.
func TestDetailMenuRefusesObjectsOfAnotherNode(t *testing.T) {
	a := newTestApp()
	serverA := &dbconn.ServerConn{}
	shown := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: serverA}}

	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = shown
	db.applyResult(&detailResult{
		cols: []string{"Name", "Type"},
		rows: [][]string{{"dbo.orders", "Table"}},
		objs: []nodeData{{Type: NodeTable, Name: "orders", Schema: "dbo"}},
	})
	if items := a.detailMenuItems(db); len(items) == 0 {
		t.Fatal("setup offers no Delete on the shown node")
	}

	// A path that swaps currentNode without resetting — the shape of B1 and T1.
	db.currentNode, _ = newConnectedNode("orders")
	if items := a.detailMenuItems(db); items != nil {
		t.Errorf("detailMenuItems = %d items with another node current, want none", len(items))
	}
}

// TestDetailLoadersCoverage pins the detailLoaders table against the rest of
// the dispatch: no entry for a type fetch handles itself (it would never run),
// and none for a placeholder type (NodeLoading/NodeError have no details).
// fetch's own cases are read out of its switch rather than listed, so a type
// moved between the two is checked without anyone remembering this test.
func TestDetailLoadersCoverage(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "detail_browser_runs.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing detail_browser_runs.go: %v", err)
	}
	var progressive []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "fetch" || fn.Recv == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if id, ok := e.(*ast.Ident); ok {
						progressive = append(progressive, id.Name)
					}
				}
			}
			return true
		})
	}
	if len(progressive) == 0 {
		t.Fatal("found no case in DetailBrowser.fetch's switch — has it moved?")
	}
	names := nodeTypeNames(t)
	for nt := range detailLoaders {
		if nt < 0 || nt >= nodeTypeCount {
			t.Errorf("detailLoaders has an entry for NodeType %d, outside the const block", nt)
			continue
		}
		if slices.Contains(progressive, names[nt]) {
			t.Errorf("%s is in detailLoaders, but DetailBrowser.fetch handles it itself — the entry never runs", names[nt])
		}
		if nodeTypeWiring[nt].class == classInternal {
			t.Errorf("%s is a placeholder type with a detailLoaders entry", names[nt])
		}
	}
}
