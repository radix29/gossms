package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// The Object Explorer wiring for the Endpoints family — the same per-family
// checklist the earlier families cover, plus the system-endpoint guard, which
// nothing before this family needed.

func TestEndpointsFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeEndpoints]; !ok {
		t.Fatal("NodeEndpoints has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeEndpoints) {
		t.Error("NodeEndpoints is not a container node — it would draw an object icon and refuse to expand")
	}
	if hasChildren(NodeEndpoint) {
		t.Error("NodeEndpoint claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

func TestEndpointLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeEndpoint, style.s)
		if got == 0 {
			t.Errorf("%s: NodeEndpoint has no glyph", style.name)
		}
		if got == '•' {
			t.Errorf("%s: NodeEndpoint fell through to the default bullet", style.name)
		}
	}
}

func TestEndpointTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeEndpoint); got != "Endpoint" {
		t.Errorf("nodeTypeName(NodeEndpoint) = %q, want %q", got, "Endpoint")
	}
}

func TestEndpointScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeEndpoint, "", "AGEP", ""))
	if len(items) == 0 {
		t.Fatal("an endpoint offers no Script item")
	}
	if items[0].Label != "Script Endpoint as" {
		t.Errorf("Script item is labelled %q", items[0].Label)
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op, ok := objectOps[NodeEndpoint]
	if !ok {
		t.Fatal("NodeEndpoint has no objectOps entry — Delete is not offered")
	}
	if op.drop == nil {
		t.Error("the endpoint objectOp has no drop")
	}
	// ALTER ENDPOINT has no WITH NAME and sp_rename has no class for one, so a
	// rename would fail on click.
	if op.rename != nil {
		t.Error("the endpoint objectOp offers a rename SQL Server has no statement for")
	}
}

// endpointRows scripts the four-row answer the loader and the Details pane
// read. The user endpoint is neither first nor last, and one built-in is
// STOPPED, so a loader that reads row 0 or ignores is-system fails.
func endpointRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.endpoints",
		cols:  8,
		rows: [][]driver.Value{
			{int64(1), "Dedicated Admin Connection", "sa", "TCP", "TSQL", "STARTED", true, int64(0)},
			{int64(4), "TSQL Default TCP", "sa", "TCP", "TSQL", "STOPPED", false, int64(0)},
			{int64(65536), "AGEP", "sa", "TCP", "DATABASE_MIRRORING", "STARTED", false, int64(5022)},
			{int64(65537), "BrokerEP", "sa", "TCP", "SERVICE_BROKER", "DISABLED", false, int64(4022)},
		},
	}
}

// endpointByName scopes the by-name read with arg:, and it must be placed
// before the list response: the fake matches by substring in order, and
// EndpointByNameContext's query also contains "FROM   sys.endpoints", so
// without it every endpoint resolves to whichever row is listed first — which
// makes a drop test pass whatever name it is given.
func endpointByName(name string, row []driver.Value) fakeResponse {
	return fakeResponse{match: "FROM   sys.endpoints", arg: name, cols: 8, rows: [][]driver.Value{row}}
}

var (
	dacRow  = []driver.Value{int64(1), "Dedicated Admin Connection", "sa", "TCP", "TSQL", "STARTED", true, int64(0)}
	agepRow = []driver.Value{int64(65536), "AGEP", "sa", "TCP", "DATABASE_MIRRORING", "STARTED", false, int64(5022)}
)

// The whole reason IsSystem reaches nodeData: deleteObject and renameObject
// both withhold themselves on it, and a built-in endpoint offered for deletion
// fails on a statement SQL Server refuses.
func TestEndpointsFolderMarksTheBuiltInOnes(t *testing.T) {
	sc, _ := newFakeConn(t, endpointRows())
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := loadEndpointsChildren(l, &explorerNode{data: nodeData{Type: NodeEndpoints, conn: sc}})
	if err != nil {
		t.Fatalf("loadEndpointsChildren: %v", err)
	}
	if len(children) != 4 {
		t.Fatalf("got %d children, want 4", len(children))
	}
	for i, want := range []bool{true, true, false, false} {
		if children[i].data.IsSystem != want {
			t.Errorf("child %d (%s) IsSystem = %v, want %v", i, children[i].data.Name, children[i].data.IsSystem, want)
		}
	}
	// A stopped or disabled endpoint accepts nothing, and the icon says the
	// same thing either way.
	if children[1].label != "TSQL Default TCP (Stopped)" {
		t.Errorf("the stopped endpoint is labelled %q", children[1].label)
	}
	if children[3].label != "BrokerEP (Disabled)" {
		t.Errorf("the disabled endpoint is labelled %q", children[3].label)
	}
	if children[2].label != "AGEP" || !children[2].data.IsEnabled {
		t.Errorf("the started endpoint is labelled %q / IsEnabled=%v", children[2].label, children[2].data.IsEnabled)
	}
}

// The Details pane reads gosmo independently of the tree, so its row objects
// are their own chance to lose the flag that withholds Delete.
func TestEndpointsFolderDetailCarriesIsSystem(t *testing.T) {
	sc, _ := newFakeConn(t, endpointRows())

	var objs []nodeData
	cols, rows, err := endpointsFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeEndpoints}}, &objs)
	if err != nil {
		t.Fatalf("endpointsFolderDetail: %v", err)
	}
	if len(rows) != 4 || len(objs) != 4 {
		t.Fatalf("got %d rows and %d row objects, want 4 of each", len(rows), len(objs))
	}
	if !objs[0].IsSystem || objs[2].IsSystem {
		t.Errorf("row objects lost IsSystem: %+v / %+v", objs[0], objs[2])
	}
	if objs[2].Name != "AGEP" || objs[2].Type != NodeEndpoint {
		t.Errorf("row object 2 is %+v", objs[2])
	}
	port := slices.Index(cols, "Port")
	kind := slices.Index(cols, "Type")
	if port < 0 || kind < 0 {
		t.Fatalf("columns are %v", cols)
	}
	// A built-in TCP endpoint reports port 0, which is not a port anything
	// connects to — showing "0" invites the reader to try it.
	if rows[0][port] != "" {
		t.Errorf("the built-in TCP endpoint reports port %q", rows[0][port])
	}
	if rows[2][port] != "5022" || rows[2][kind] != "DATABASE_MIRRORING" {
		t.Errorf("the mirroring endpoint's row is %v", rows[2])
	}
}

// The three state items must be offered on a system endpoint rather than
// hidden: hiding them says the login may not, and the reason is the endpoint.
// The refusal is a message the user can read.
func TestASystemEndpointRefusesTheStateChangeWithAMessage(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := &explorerNode{data: nodeData{Type: NodeEndpoint, Name: "TSQL Default TCP", IsSystem: true, conn: sc}}

	labels := labelsOf(a.nodeMenuItems(node))
	for _, want := range []string{"Start", "Stop", "Disable", "Properties..."} {
		if !slices.Contains(labels, want) {
			t.Errorf("the menu is %v, want it to offer %q", labels, want)
		}
	}

	a.setEndpointState(sc, node, gosmo.EndpointStopped)
	if !strings.Contains(a.statusText, "built-in") {
		t.Errorf("status after refusing a built-in endpoint is %q", a.statusText)
	}
}

// gosmo refuses the drop too, and that second refusal is the one that matters:
// the Details pane and the tree both route Delete through this closure.
func TestDroppingASystemEndpointIsRefused(t *testing.T) {
	sc, inst := newFakeConn(t,
		endpointByName("Dedicated Admin Connection", dacRow), endpointRows())
	err := objectOps[NodeEndpoint].drop(t.Context(), sc,
		nodeData{Type: NodeEndpoint, Name: "Dedicated Admin Connection"})
	if !errors.Is(err, gosmo.ErrSystemEndpoint) {
		t.Fatalf("drop returned %v, want ErrSystemEndpoint", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a refused drop still ran %v", stmts)
	}
}

func TestEndpointDropStatement(t *testing.T) {
	sc, inst := newFakeConn(t, endpointByName("AGEP", agepRow), endpointRows())
	if err := objectOps[NodeEndpoint].drop(t.Context(), sc, nodeData{Type: NodeEndpoint, Name: "AGEP"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	want := "DROP ENDPOINT [AGEP]"
	if stmts := inst.Statements(); len(stmts) != 1 || stmts[0] != want {
		t.Errorf("got %v, want [%s]", stmts, want)
	}
}
