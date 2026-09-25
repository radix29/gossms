package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
)

// Delete and Move to Schema driven end to end against the scripted instance:
// the confirmation is answered the way a user answers it, and what reaches the
// server is read back. What these pin is the pairing between the answer and
// the statement — a cascade box that changes nothing, or a transfer that moves
// the object the menu was not opened on, are both invisible from the UI.

func opTestConn(t *testing.T) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	return newFakeConn(t,
		fakeResponse{match: "FROM sys.databases", arg: "appdb", cols: 9, rows: [][]driver.Value{
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now(), int64(0)},
		}},
	)
}

// opTestNode builds a node of type typ wired to sc, with a parent to refresh.
func opTestNode(sc *db.ServerConn, typ NodeType, schema, name, table string) *explorerNode {
	parent := &explorerNode{label: "folder"}
	n := opNode(typ, schema, name, table)
	n.data.DBName = "appdb"
	n.data.conn = sc
	n.parent = parent
	parent.children = []*explorerNode{n}
	return n
}

// answerConfirm answers the open confirmation, optionally ticking its
// checkbox first — Tab twice from Yes reaches the box on a two-button prompt.
func answerConfirm(t *testing.T, a *App, tickOption bool) {
	t.Helper()
	if !a.confirmDialog.Visible() {
		t.Fatal("no confirmation is open")
	}
	if tickOption {
		// Backtab, not Tab: the checkbox sits last in the cycle, so one step
		// back reaches it whatever the button count is — Tab's distance to it
		// changed when Delete grew its Script button. The Tab afterwards lands
		// back on the first button, which is Yes in every showing.
		a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, "", tcell.ModNone))
		a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
		a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
}

func TestDeleteTableCascadeFollowsTheCheckbox(t *testing.T) {
	for _, c := range []struct {
		name       string
		tick       bool
		wantFKDrop bool
	}{
		{"left unticked", false, false},
		{"ticked", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := opTestConn(t)
			node := opTestNode(sc, NodeTable, "sales", "Orders", "")

			a.deleteObject(node)
			answerConfirm(t, a, c.tick)
			waitAndDrain(t, a)

			stmts := inst.StatementsIn("appdb")
			joined := strings.Join(stmts, "\n---\n")
			if !strings.Contains(joined, "DROP TABLE [sales].[Orders]") {
				t.Errorf("the table was not dropped:\n%s", joined)
			}
			// The cascade half is a separate statement that drops the foreign
			// keys pointing *at* this table — the reason the option exists.
			gotFKDrop := strings.Contains(joined, "sys.foreign_keys")
			if gotFKDrop != c.wantFKDrop {
				t.Errorf("referencing foreign keys dropped = %v, want %v:\n%s", gotFKDrop, c.wantFKDrop, joined)
			}
		})
	}
}

// The checkbox must not carry over: it widens what the drop touches, so every
// showing starts unticked even right after one that was ticked.
func TestDeleteTableOptionStartsUntickedEachTime(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)

	first := opTestNode(sc, NodeTable, "sales", "Orders", "")
	a.deleteObject(first)
	answerConfirm(t, a, true)
	waitAndDrain(t, a)

	second := opTestNode(sc, NodeTable, "sales", "Archive", "")
	a.deleteObject(second)
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	var archive []string
	for _, s := range inst.StatementsIn("appdb") {
		if strings.Contains(s, "Archive") || strings.Contains(s, "sys.foreign_keys") {
			archive = append(archive, s)
		}
	}
	joined := strings.Join(archive, "\n---\n")
	if strings.Count(joined, "sys.foreign_keys") != 1 {
		t.Errorf("the second delete inherited the first showing's cascade:\n%s", joined)
	}
}

func TestDeleteColumnDropsFromItsOwnTable(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	// The column's Schema is the table's; its Name is the column's own.
	node := opTestNode(sc, NodeColumn, "sales", "flagged", "Orders")

	a.deleteObject(node)
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("statements = %q, want exactly the ALTER TABLE", stmts)
	}
	if got := stmts[0]; got != "ALTER TABLE [sales].[Orders] DROP COLUMN [flagged]" {
		t.Errorf("statement = %q; the owning table comes from TableName, not Name", got)
	}
}

func TestMoveToSchemaTransfersTheObject(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeView, "sales", "vOrders", "")

	a.confirmMoveToSchema(sc, node, objectOpFor(NodeView), "archive")
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("statements = %q, want exactly the ALTER SCHEMA", stmts)
	}
	if got := stmts[0]; got != "ALTER SCHEMA [archive] TRANSFER [sales].[vOrders]" {
		t.Errorf("statement = %q", got)
	}
}

// Answering No must leave the server untouched — the confirmation is the only
// thing between the menu item and an irreversible write.
func TestDeleteAndMoveDoNothingWhenRefused(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)

	a.deleteObject(opTestNode(sc, NodeTable, "sales", "Orders", ""))
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))

	node := opTestNode(sc, NodeView, "sales", "vOrders", "")
	a.confirmMoveToSchema(sc, node, objectOpFor(NodeView), "archive")
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))

	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a refused delete/move executed %q", stmts)
	}
}

// The menu item is gated on the connection as well as on the type: with none,
// the action must report that rather than dereference it.
func TestMoveToSchemaWithoutAConnectionDoesNothing(t *testing.T) {
	a := newTestApp()
	node := opNode(NodeView, "sales", "vOrders", "")
	a.moveObjectToSchema(node)
	if a.confirmDialog.Visible() {
		t.Error("a disconnected Move to Schema opened its confirmation")
	}
}

// Belt and braces on the menu wiring: the item a user clicks has to be the one
// that runs, so the labels are matched against the actions they carry.
func TestObjectOpsMenuActionsAreWired(t *testing.T) {
	a := newTestApp()
	node := opNode(NodeTable, "dbo", "Orders", "")
	for _, it := range a.objectOpsMenuItems(node) {
		if it.Action == nil {
			t.Errorf("menu item %q has no action", it.Label)
		}
	}
}

// answerPrompt types a new name into the open rename prompt and accepts it,
// the way a user does — the prompt's own input, not the callback, so the
// trimming and the "unchanged name" check are exercised too.
func answerPrompt(t *testing.T, a *App, name string) {
	t.Helper()
	if !a.promptDialog.Visible() {
		t.Fatal("no prompt is open")
	}
	a.promptDialog.HandleKey(tcell.NewEventKey(tcell.KeyCtrlU, "", tcell.ModNone))
	for _, r := range name {
		a.promptDialog.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	a.promptDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
}

// Renaming a column addresses sp_rename's three-part table.column form, built
// from the table the node hangs off rather than from the column's own Schema
// and Name — the same distinction the column Delete gets wrong if TableName is
// dropped. The rename is also gated on a warning, since nothing that names the
// column is updated by it.
func TestRenameColumnRenamesInItsOwnTable(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeColumn, "sales", "flagged", "Orders")

	a.renameObject(node)
	answerPrompt(t, a, "is_flagged")
	if !a.confirmDialog.Visible() {
		t.Fatal("the column rename went ahead without asking about dependent objects")
	}
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("statements = %q, want exactly the sp_rename", stmts)
	}
	got := stmts[0]
	for _, want := range []string{"sp_rename", "COLUMN"} {
		if !strings.Contains(got, want) {
			t.Errorf("statement = %q, want it to contain %q", got, want)
		}
	}
	assertArgs(t, inst, "sp_rename", "[sales].[Orders].[flagged]", "is_flagged")
}

// Declining the warning must leave the column alone: the prompt has already
// been answered by then, so a rename that runs anyway is invisible until the
// next Refresh.
func TestDecliningTheColumnRenameWarningWritesNothing(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeColumn, "sales", "flagged", "Orders")

	a.renameObject(node)
	answerPrompt(t, a, "is_flagged")
	if !a.confirmDialog.Visible() {
		t.Fatal("no confirmation is open")
	}
	// Tab moves off Yes to No, Enter answers it.
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	// No wait: declining posts no callback, so there is nothing to drain and
	// waiting for one is what would fail here.
	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("statements = %q, want none after declining", stmts)
	}
}

// Every database-scoped drop addresses its object by name in the statement
// text and reads nothing off the *gosmo.Database, so every one of them goes
// through dbOf and none should touch sys.databases. Two entries did not until
// 2026-09-18 (review plan P2), spending a round trip each and failing with
// "database not found" where the sibling would have emitted the DROP. The
// assertion is a *read* count: assertNoStatementsIn sees writes only, and the
// bug here was an extra read.
func TestDropsThroughDbOfReadNoCatalog(t *testing.T) {
	for _, tc := range []struct {
		nt NodeType
		n  nodeData
		// resp is what the drop legitimately reads on its way — never
		// sys.databases. Only the audit specification has one: it reads its
		// own state first, because dropping an enabled specification fails.
		resp []fakeResponse
		want string
	}{
		{nt: NodeDatabaseScopedCredential,
			n:    nodeData{Type: NodeDatabaseScopedCredential, DBName: "appdb", Name: "app_cred"},
			want: "DROP DATABASE SCOPED CREDENTIAL [app_cred]"},
		{nt: NodeCertificate,
			n:    nodeData{Type: NodeCertificate, DBName: "appdb", Name: "app_cert"},
			want: "DROP CERTIFICATE [app_cert]"},
		{nt: NodeDatabaseAuditSpecification,
			n: nodeData{Type: NodeDatabaseAuditSpecification, DBName: "appdb", Name: "app_audit_spec"},
			resp: []fakeResponse{{
				match: "is_state_enabled FROM sys.database_audit_specifications",
				cols:  1, rows: [][]driver.Value{{false}},
			}},
			want: "DROP DATABASE AUDIT SPECIFICATION [app_audit_spec]"},
		{nt: NodeDatabaseTrigger,
			n:    nodeData{Type: NodeDatabaseTrigger, DBName: "appdb", Name: "ddl_audit"},
			want: "DROP TRIGGER [ddl_audit] ON DATABASE"},
	} {
		t.Run(nodeTypeName(tc.nt), func(t *testing.T) {
			// No scripted sys.databases row: a drop that looks one up gets no
			// answer and fails, which is the second half of what this pins.
			sc, inst := newFakeConn(t, tc.resp...)
			if err := objectOps[tc.nt].drop(t.Context(), sc, tc.n); err != nil {
				t.Fatalf("drop: %v", err)
			}
			if reads := inst.Reads("sys.databases"); len(reads) != 0 {
				t.Errorf("drop read sys.databases, want no catalog read:\n%s", strings.Join(reads, "\n"))
			}
			assertOneStatementIn(t, inst, "appdb", tc.want)
			assertNoStatementsIn(t, inst, "master")
		})
	}
}

// Every schema-scoped entry of objectOps calls a method on its own gosmo
// handle. The generic adapters (dropIn, renameIn, transferIn) take the handle
// constructor as a value, so wiring a node type to a sibling family's
// constructor compiles and sends the wrong statement — DROP VIEW for a
// procedure, an sp_rename without USERDATATYPE for an alias type, a TRANSFER
// without TYPE:: for a table type. These pin each entry to its statement.
func TestSchemaScopedObjectOpsSendTheirOwnStatement(t *testing.T) {
	type opCase struct {
		nt   NodeType
		op   string // "drop", "rename" or "transfer"
		want string // a substring of the one statement sent
		// objtype is sp_rename's @objtype, for a rename.
		objtype string
	}
	var cases []opCase
	for nt, kw := range map[NodeType]string{
		NodeView: "VIEW", NodeStoredProcedure: "PROCEDURE", NodeFunction: "FUNCTION",
		NodeTrigger: "TRIGGER", NodeSequence: "SEQUENCE", NodeSynonym: "SYNONYM",
		NodeRule: "RULE", NodeDefault: "DEFAULT", NodeBrokerQueue: "QUEUE",
		NodeUserDefinedDataType: "TYPE", NodeUserDefinedTableType: "TYPE", NodeUserDefinedType: "TYPE",
		NodeXMLSchemaCollection: "XML SCHEMA COLLECTION",
	} {
		cases = append(cases, opCase{nt: nt, op: "drop", want: "DROP " + kw + " [sales].[obj]"})
	}
	for _, nt := range []NodeType{NodeTable, NodeView, NodeStoredProcedure, NodeFunction, NodeTrigger,
		NodeSequence, NodeSynonym, NodeRule, NodeDefault} {
		cases = append(cases, opCase{nt: nt, op: "rename", want: "sp_rename", objtype: "OBJECT"})
	}
	cases = append(cases, opCase{nt: NodeUserDefinedDataType, op: "rename", want: "sp_rename", objtype: "USERDATATYPE"})
	for _, nt := range []NodeType{NodeTable, NodeView, NodeStoredProcedure, NodeFunction,
		NodeSequence, NodeSynonym, NodeRule, NodeDefault, NodeBrokerQueue} {
		cases = append(cases, opCase{nt: nt, op: "transfer", want: "ALTER SCHEMA [archive] TRANSFER [sales].[obj]"})
	}
	for _, nt := range []NodeType{NodeUserDefinedDataType, NodeUserDefinedTableType, NodeUserDefinedType} {
		cases = append(cases, opCase{nt: nt, op: "transfer", want: "ALTER SCHEMA [archive] TRANSFER TYPE::[sales].[obj]"})
	}
	cases = append(cases, opCase{nt: NodeXMLSchemaCollection, op: "transfer",
		want: "ALTER SCHEMA [archive] TRANSFER XML SCHEMA COLLECTION::[sales].[obj]"})

	for _, tc := range cases {
		t.Run(nodeTypeName(tc.nt)+"/"+tc.op, func(t *testing.T) {
			sc, inst := newFakeConn(t)
			op := objectOps[tc.nt]
			n := nodeData{Type: tc.nt, DBName: "appdb", Schema: "sales", Name: "obj"}
			var err error
			switch tc.op {
			case "drop":
				err = op.drop(t.Context(), sc, n)
			case "rename":
				err = op.rename(t.Context(), sc, n, "obj2")
			case "transfer":
				err = op.transfer(t.Context(), sc, n, "archive")
			}
			if err != nil {
				t.Fatalf("%s: %v", tc.op, err)
			}
			stmts := inst.StatementsIn("appdb")
			if len(stmts) != 1 || !strings.Contains(stmts[0], tc.want) {
				t.Fatalf("statements = %q, want one containing %q", stmts, tc.want)
			}
			if tc.objtype == "" {
				return
			}
			args, _ := inst.ExecArgs("sp_rename")
			if len(args) != 2 || args[0] != "[sales].[obj]" || args[1] != "obj2" {
				t.Errorf("sp_rename args = %v, want [[sales].[obj] obj2]", args)
			}
			if !strings.Contains(stmts[0], "@objtype = N'"+tc.objtype+"'") {
				t.Errorf("statement = %q, want @objtype %s", stmts[0], tc.objtype)
			}
		})
	}
}

// A foreign key or CHECK constraint is renamed in its table's schema. The
// node's TableName names the table; Schema/Name name the constraint.
func TestRenameConstraintAddressesTheConstraint(t *testing.T) {
	for _, nt := range []NodeType{NodeForeignKey, NodeCheck} {
		t.Run(nodeTypeName(nt), func(t *testing.T) {
			sc, inst := newFakeConn(t)
			n := nodeData{Type: nt, DBName: "appdb", Schema: "sales", Name: "FK_a", TableName: "Orders"}
			if err := objectOps[nt].rename(t.Context(), sc, n, "FK_b"); err != nil {
				t.Fatalf("rename: %v", err)
			}
			args, ok := inst.ExecArgs("sp_rename")
			if !ok || len(args) != 2 || args[0] != "[sales].[FK_a]" || args[1] != "FK_b" {
				t.Errorf("sp_rename args = %v, want [[sales].[FK_a] FK_b]", args)
			}
		})
	}
}
