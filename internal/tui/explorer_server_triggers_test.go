package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
)

// The Object Explorer wiring for the server-scope Triggers family — the same
// per-family checklist the Credentials and Backup Devices tests cover.

func TestServerTriggersFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeServerTriggers]; !ok {
		t.Fatal("NodeServerTriggers has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeServerTriggers) {
		t.Error("NodeServerTriggers is not a container node — it would draw an object icon and refuse to expand")
	}
	if hasChildren(NodeServerTrigger) {
		t.Error("NodeServerTrigger claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

func TestServerTriggerLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeServerTrigger, style.s)
		if got == 0 {
			t.Errorf("%s: NodeServerTrigger has no glyph", style.name)
		}
		if got == '•' {
			t.Errorf("%s: NodeServerTrigger fell through to the default bullet", style.name)
		}
	}
}

func TestServerTriggerTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeServerTrigger); got != "Server Trigger" {
		t.Errorf("nodeTypeName(NodeServerTrigger) = %q, want %q", got, "Server Trigger")
	}
}

func TestServerTriggerScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeServerTrigger, "", "ddl_audit", ""))
	if len(items) == 0 {
		t.Fatal("a server trigger offers no Script item")
	}
	if items[0].Label != "Script Server Trigger as" {
		t.Errorf("Script item is labelled %q", items[0].Label)
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op, ok := objectOps[NodeServerTrigger]
	if !ok {
		t.Fatal("NodeServerTrigger has no objectOps entry — Delete is not offered")
	}
	if op.drop == nil {
		t.Error("the server trigger objectOp has no drop")
	}
	// sp_rename has no class for a server-scope trigger, and the name is baked
	// into the definition, so a rename would fail on click.
	if op.rename != nil {
		t.Error("the server trigger objectOp offers a rename SQL Server has no statement for")
	}
}

func TestServerTriggerDropStatement(t *testing.T) {
	sc, inst := newFakeConn(t)
	err := objectOps[NodeServerTrigger].drop(t.Context(), sc,
		nodeData{Type: NodeServerTrigger, Name: "ddl_audit"})
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	want := "DROP TRIGGER [ddl_audit] ON ALL SERVER"
	if stmts := inst.Statements(); len(stmts) != 1 || stmts[0] != want {
		t.Errorf("got %v, want [%s]", stmts, want)
	}
}

// serverTriggerRows scripts the three-row answer both the tree loader and the
// Details pane read. The middle trigger is the disabled one, so a loader that
// ignores is_disabled fails on a row that is not first.
func serverTriggerRows() fakeResponse {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return fakeResponse{
		match: "FROM   sys.server_triggers",
		cols:  6,
		rows: [][]driver.Value{
			{"aaa_first", false, when, when, "CREATE_DATABASE", "CREATE TRIGGER [aaa_first] ON ALL SERVER ..."},
			{"ddl_audit", true, when, when, "CREATE_DATABASE,ALTER_DATABASE", "CREATE TRIGGER [ddl_audit] ON ALL SERVER ..."},
			{"logon_guard", false, when, when, "LOGON", nil},
		},
	}
}

// The tree's label is where a disabled trigger's state is visible at all — the
// icon is the same either way, so a loader that drops the flag hides that the
// policy is not being enforced.
func TestServerTriggersFolderLabelsTheDisabledOne(t *testing.T) {
	sc, _ := newFakeConn(t, serverTriggerRows())
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := loadServerTriggersChildren(l, &explorerNode{data: nodeData{Type: NodeServerTriggers, conn: sc}})
	if err != nil {
		t.Fatalf("loadServerTriggersChildren: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("got %d children, want 3", len(children))
	}
	if children[1].label != "ddl_audit (Disabled)" {
		t.Errorf("the disabled trigger is labelled %q", children[1].label)
	}
	if children[1].data.IsEnabled {
		t.Error("the disabled trigger's node claims IsEnabled — its menu would offer Disable")
	}
	if children[0].label != "aaa_first" || !children[0].data.IsEnabled {
		t.Errorf("an enabled trigger is labelled %q / IsEnabled=%v", children[0].label, children[0].data.IsEnabled)
	}
	if children[2].data.Name != "logon_guard" || children[2].data.Type != NodeServerTrigger {
		t.Errorf("child 2 is %+v", children[2].data)
	}
}

// The leaf's menu is what carries Enable/Disable, and the label has to follow
// the node's state or the item runs the write the user did not ask for.
func TestServerTriggerMenuNamesTheOppositeState(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for _, tc := range []struct {
		enabled bool
		want    string
	}{{true, "Disable"}, {false, "Enable"}} {
		node := &explorerNode{data: nodeData{Type: NodeServerTrigger, Name: "ddl_audit", IsEnabled: tc.enabled, conn: sc}}
		labels := labelsOf(a.nodeMenuItems(node))
		if !slices.Contains(labels, tc.want) {
			t.Errorf("IsEnabled=%v: menu is %v, want it to offer %q", tc.enabled, labels, tc.want)
		}
		if !slices.Contains(labels, "Properties...") {
			t.Errorf("IsEnabled=%v: menu offers no Properties item", tc.enabled)
		}
	}
}

// The Details pane reads gosmo independently of the tree, so it is its own
// chance to list the wrong thing.
func TestServerTriggersFolderDetailListsEveryTrigger(t *testing.T) {
	sc, _ := newFakeConn(t, serverTriggerRows())

	var objs []nodeData
	cols, rows, err := serverTriggersFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeServerTriggers}}, &objs)
	if err != nil {
		t.Fatalf("serverTriggersFolderDetail: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if len(objs) != len(rows) {
		t.Fatalf("got %d row objects for %d rows — the pane's Delete is withheld", len(objs), len(rows))
	}
	if objs[1].Name != "ddl_audit" || objs[1].Type != NodeServerTrigger {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	status := slices.Index(cols, "Status")
	events := slices.Index(cols, "Events")
	if status < 0 || events < 0 {
		t.Fatalf("columns are %v", cols)
	}
	if rows[1][status] != "Disabled" || rows[0][status] != "Enabled" {
		t.Errorf("status column is %q / %q", rows[0][status], rows[1][status])
	}
	// A trigger's events are what it actually does; a row without them says
	// only that something fires.
	if !strings.Contains(rows[1][events], "ALTER_DATABASE") {
		t.Errorf("row 1 does not carry its events: %v", rows[1])
	}
}
