package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The Object Explorer wiring for the database-scope DDL trigger family — the
// same per-family checklist the server-scope Triggers test covers, plus the
// one check that family does not need: that this folder and the DML Triggers
// folder read different rows.

func TestDatabaseTriggersFolderHasALoader(t *testing.T) {
	if _, ok := childLoaders[NodeDatabaseTriggers]; !ok {
		t.Fatal("NodeDatabaseTriggers has no childLoaders entry — the folder would expand to nothing")
	}
	if !isContainerNode(NodeDatabaseTriggers) {
		t.Error("NodeDatabaseTriggers is not a container node — it would draw an object icon and refuse to expand")
	}
	if !isContainerNode(NodeProgrammability) {
		t.Error("NodeProgrammability is not a container node — the folder holding it would not expand")
	}
	if hasChildren(NodeDatabaseTrigger) {
		t.Error("NodeDatabaseTrigger claims children — the leaf would draw an expand arrow that leads nowhere")
	}
}

func TestDatabaseTriggerLeafHasAnIconInEveryStyle(t *testing.T) {
	for _, style := range []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	} {
		got := objectIcon(NodeDatabaseTrigger, style.s)
		if got == 0 {
			t.Errorf("%s: NodeDatabaseTrigger has no glyph", style.name)
		}
		if got == '•' {
			t.Errorf("%s: NodeDatabaseTrigger fell through to the default bullet", style.name)
		}
	}
}

func TestDatabaseTriggerTypeIsNamed(t *testing.T) {
	if got := nodeTypeName(NodeDatabaseTrigger); got != "Database Trigger" {
		t.Errorf("nodeTypeName(NodeDatabaseTrigger) = %q, want %q", got, "Database Trigger")
	}
}

func TestDatabaseTriggerScriptsAndDrops(t *testing.T) {
	a := &App{}
	items := a.scriptMenuItems(opNode(NodeDatabaseTrigger, "", "ddl_audit", "appdb"))
	if len(items) == 0 {
		t.Fatal("a database trigger offers no Script item")
	}
	if items[0].Label != "Script Database Trigger as" {
		t.Errorf("Script item is labelled %q", items[0].Label)
	}
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	if got := labelsOf(items[0].Sub); !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}

	op, ok := objectOps[NodeDatabaseTrigger]
	if !ok {
		t.Fatal("NodeDatabaseTrigger has no objectOps entry — Delete is not offered")
	}
	if op.drop == nil {
		t.Error("the database trigger objectOp has no drop")
	}
	// sp_rename has no class for a DDL trigger, and the name is baked into the
	// definition, so a rename would fail on click.
	if op.rename != nil {
		t.Error("the database trigger objectOp offers a rename SQL Server has no statement for")
	}
}

// The drop must be the ON DATABASE form. Database.DropTrigger's
// schema-qualified statement is the plausible reuse here and it addresses a
// different object entirely.
func TestDatabaseTriggerDropStatement(t *testing.T) {
	sc, inst := newFakeConn(t)
	err := objectOps[NodeDatabaseTrigger].drop(t.Context(), sc,
		nodeData{Type: NodeDatabaseTrigger, Name: "ddl_audit", DBName: "appdb"})
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	// StatementsIn, not Statements: the USE that puts the drop in the right
	// database is stripped as plumbing, and with it the only record of where
	// the statement landed.
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("got %d statements in appdb, want 1: %v", len(stmts), stmts)
	}
	if want := "DROP TRIGGER [ddl_audit] ON DATABASE"; stmts[0] != want {
		t.Errorf("got %q, want %q", stmts[0], want)
	}
	assertNoStatementsIn(t, inst, "master")
}

// dbTriggerRows scripts the three-row answer both the tree loader and the
// Details pane read. The middle trigger is the disabled one, so a loader that
// ignores is_disabled fails on a row that is not first; the last has a NULL
// definition, which is what a CLR trigger reads back as.
func dbTriggerRows() fakeResponse {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return fakeResponse{
		match: "tr.parent_class = 0",
		cols:  6,
		rows: [][]driver.Value{
			{"aaa_first", false, when, when, "CREATE_TABLE", "CREATE TRIGGER [aaa_first] ON DATABASE ..."},
			{"ddl_audit", true, when, when, "CREATE_TABLE,ALTER_TABLE", "CREATE TRIGGER [ddl_audit] ON DATABASE ..."},
			{"clr_guard", false, when, when, "DROP_TABLE", nil},
		},
	}
}

// dbTriggerDatabaseRow answers the sys.databases read DatabaseByName makes
// before any database-scoped listing.
func dbTriggerDatabaseRow() fakeResponse {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return fakeResponse{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
		"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, when,
	}}}
}

func dbTriggerConn(t *testing.T, extra ...fakeResponse) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	return newFakeConn(t, append([]fakeResponse{dbTriggerDatabaseRow(), dbTriggerRows()}, extra...)...)
}

// The tree's label is where a disabled trigger's state is visible at all — the
// icon is the same either way, so a loader that drops the flag hides that the
// policy is not being enforced.
func TestDatabaseTriggersFolderLabelsTheDisabledOne(t *testing.T) {
	sc, _ := dbTriggerConn(t)
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := loadDatabaseTriggersChildren(l,
		&explorerNode{data: nodeData{Type: NodeDatabaseTriggers, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseTriggersChildren: %v", err)
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
	// The DBName has to travel down: every write and every read below the
	// folder names the database, and the node is all that carries it.
	if children[2].data.Name != "clr_guard" || children[2].data.Type != NodeDatabaseTrigger ||
		children[2].data.DBName != "appdb" {
		t.Errorf("child 2 is %+v", children[2].data)
	}
	// A DDL trigger has no schema, and a node that invented one would script
	// and drop the wrong object.
	if children[0].data.Schema != "" {
		t.Errorf("a database trigger node carries schema %q", children[0].data.Schema)
	}
}

// The leaf's menu is what carries Enable/Disable, and the label has to follow
// the node's state or the item runs the write the user did not ask for.
func TestDatabaseTriggerMenuNamesTheOppositeState(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for _, tc := range []struct {
		enabled bool
		want    string
	}{{true, "Disable"}, {false, "Enable"}} {
		node := &explorerNode{data: nodeData{Type: NodeDatabaseTrigger, Name: "ddl_audit",
			DBName: "appdb", IsEnabled: tc.enabled, conn: sc}}
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
func TestDatabaseTriggersFolderDetailListsEveryTrigger(t *testing.T) {
	sc, _ := dbTriggerConn(t)

	var objs []nodeData
	cols, rows, err := databaseTriggersFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeDatabaseTriggers, DBName: "appdb"}}, &objs)
	if err != nil {
		t.Fatalf("databaseTriggersFolderDetail: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if len(objs) != len(rows) {
		t.Fatalf("got %d row objects for %d rows — the pane's Delete is withheld", len(objs), len(rows))
	}
	if objs[1].Name != "ddl_audit" || objs[1].Type != NodeDatabaseTrigger || objs[1].DBName != "appdb" {
		t.Errorf("row object 1 is %+v", objs[1])
	}
	status := slices.Index(cols, "Status")
	events := slices.Index(cols, "Events")
	if status < 0 || events < 0 {
		t.Fatalf("columns are %v", cols)
	}
	if rows[0][status] != "Enabled" || rows[1][status] != "Disabled" {
		t.Errorf("status column is %q / %q", rows[0][status], rows[1][status])
	}
	if !strings.Contains(rows[1][events], "ALTER_TABLE") {
		t.Errorf("events column is %q", rows[1][events])
	}
}

// The two folders must not read the same rows. Both queries are over
// sys.triggers and differ only in parent_class, so a loader wired to the wrong
// one lists plausible triggers — the DML ones — under Database Triggers.
func TestTheTwoTriggerFamiliesReadDifferentRows(t *testing.T) {
	dml := fakeResponse{
		match: "tr.parent_class = 1",
		cols:  6,
		rows: [][]driver.Value{
			{"tr_Patient", "Patient", "dbo", false, "INSERT", "CREATE TRIGGER dbo.tr_Patient ..."},
		},
	}
	sc, _ := dbTriggerConn(t, dml)
	l := loaderCtx{ctx: context.Background(), sc: sc}

	ddl, err := loadDatabaseTriggersChildren(l,
		&explorerNode{data: nodeData{Type: NodeDatabaseTriggers, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseTriggersChildren: %v", err)
	}
	for _, n := range ddl {
		if strings.HasPrefix(n.label, "tr_Patient") {
			t.Fatalf("the Database Triggers folder listed a DML trigger: %v", labelsOfNodes(ddl))
		}
	}

	dmlChildren, err := loadTriggersChildren(l,
		&explorerNode{data: nodeData{Type: NodeTriggers, Schema: "dbo", Name: "Patient", DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadTriggersChildren: %v", err)
	}
	if len(dmlChildren) != 1 || dmlChildren[0].label != "tr_Patient" {
		t.Errorf("a table's Triggers folder = %v, want just tr_Patient", labelsOfNodes(dmlChildren))
	}
	if dmlChildren[0].data.Type != NodeTrigger {
		t.Errorf("a DML trigger node is typed %v", dmlChildren[0].data.Type)
	}
}

func labelsOfNodes(nodes []*explorerNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.label)
	}
	return out
}
