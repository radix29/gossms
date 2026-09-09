package tui

import (
	"slices"
	"strings"
	"testing"
)

// tree_families_ops_test.go covers the Phase 3 tree families' operations: what
// the eleven new leaves offer under Script, Delete, Rename and Move, and the
// one command beyond Properties any of them has (a plan guide's
// Enable/Disable).
//
// Every one of these families arrived at once, so the risk is a family wired
// by copying its neighbour: a type that renames as an OBJECT (sp_rename does
// nothing and reports success), an assembly offered a transfer it has no
// schema for, or an ALTER verb on an object with no ALTER statement.

// No family here has an ALTER form — CREATE TYPE, CREATE RULE and CREATE
// DEFAULT have none, ALTER ASSEMBLY takes a new binary rather than a new
// definition, and a plan guide is changed by dropping and recreating it. An
// ALTER verb in this list would generate the CREATE under a name that says
// otherwise.
func TestNewLeafScriptVerbs(t *testing.T) {
	ddl := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	for _, tc := range []struct {
		name string
		node *explorerNode
		item string // "" = no Script item at all
	}{
		{"alias type", opNode(NodeUserDefinedDataType, "dbo", "Phone", ""), "Script User-Defined Data Type as"},
		{"table type", opNode(NodeUserDefinedTableType, "dbo", "IDList", ""), "Script User-Defined Table Type as"},
		{"CLR type", opNode(NodeUserDefinedType, "dbo", "Point", ""), "Script User-Defined Type as"},
		{"XML schema collection", opNode(NodeXmlSchemaCollection, "dbo", "OrderSchema", ""), "Script XML Schema Collection as"},
		{"rule", opNode(NodeRule, "dbo", "PhoneRule", ""), "Script Rule as"},
		{"default", opNode(NodeDefault, "dbo", "Zero", ""), "Script Default as"},
		{"assembly", opNode(NodeAssembly, "", "Geometry", ""), "Script Assembly as"},
		{"plan guide", opNode(NodePlanGuide, "", "pg_orders", ""), "Script Plan Guide as"},
		{"external data source", opNode(NodeExternalDataSource, "", "RemoteDB", ""), "Script External Data Source as"},
		{"external file format", opNode(NodeExternalFileFormat, "", "CSV", ""), "Script External File Format as"},
		{"external library", opNode(NodeExternalLibrary, "", "ggplot2", ""), "Script External Library as"},
		// A built-in type is not an object a script creates, and SSMS offers
		// nothing for one either.
		{"system data type", opNode(NodeSystemDataType, "sys", "int", ""), ""},
		// Folders script nothing, the way every other folder does not.
		{"types folder", opNode(NodeTypes, "", "", ""), ""},
		{"external resources folder", opNode(NodeExternalResources, "", "", ""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{}
			items := a.scriptMenuItems(tc.node)
			if tc.item == "" {
				if len(items) != 0 {
					t.Fatalf("scriptMenuItems = %v, want none", labelsOf(items))
				}
				return
			}
			if len(items) != 1 || items[0].Label != tc.item {
				t.Fatalf("scriptMenuItems = %v, want one %q", labelsOf(items), tc.item)
			}
			if got := labelsOf(items[0].Sub); !slices.Equal(got, ddl) {
				t.Errorf("verbs = %v, want %v", got, ddl)
			}
		})
	}
}

// What each family can be told to do, and — as much the point — what it
// cannot: a rename or a transfer offered where the statement does not exist
// is an item that fails at the server after the user has typed a new name.
func TestNewLeafObjectOps(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		node                 *explorerNode
		rename, delete, move bool
	}{
		// sp_rename's USERDATATYPE class reaches alias types and nothing else
		// in sys.types.
		{"alias type", opNode(NodeUserDefinedDataType, "dbo", "Phone", ""), true, true, true},
		{"table type", opNode(NodeUserDefinedTableType, "dbo", "IDList", ""), false, true, true},
		{"CLR type", opNode(NodeUserDefinedType, "dbo", "Point", ""), false, true, true},
		{"XML schema collection", opNode(NodeXmlSchemaCollection, "dbo", "OrderSchema", ""), false, true, true},
		// Rules and defaults are ordinary sys.objects rows, so both of
		// sp_rename's and ALTER SCHEMA TRANSFER's default classes serve.
		{"rule", opNode(NodeRule, "dbo", "PhoneRule", ""), true, true, true},
		{"default", opNode(NodeDefault, "dbo", "Zero", ""), true, true, true},
		// Database-scoped: no schema to move between, and no sp_rename class.
		{"assembly", opNode(NodeAssembly, "", "Geometry", ""), false, true, false},
		{"plan guide", opNode(NodePlanGuide, "", "pg_orders", ""), false, true, false},
		{"external data source", opNode(NodeExternalDataSource, "", "RemoteDB", ""), false, true, false},
		{"external file format", opNode(NodeExternalFileFormat, "", "CSV", ""), false, true, false},
		{"external library", opNode(NodeExternalLibrary, "", "ggplot2", ""), false, true, false},
		// A built-in type is not the user's to drop.
		{"system data type", opNode(NodeSystemDataType, "sys", "int", ""), false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := objectOpFor(tc.node.data.Type)
			if op == nil {
				if tc.rename || tc.delete || tc.move {
					t.Fatal("no objectOps entry at all — Delete and Rename are both unreachable")
				}
				return
			}
			if got := op.rename != nil; got != tc.rename {
				t.Errorf("rename = %v, want %v", got, tc.rename)
			}
			if got := op.drop != nil || op.dropWithOption != nil; got != tc.delete {
				t.Errorf("delete = %v, want %v", got, tc.delete)
			}
			if got := op.transfer != nil; got != tc.move {
				t.Errorf("transfer = %v, want %v", got, tc.move)
			}
		})
	}
}

// The statements behind Delete, read off the confirmation's Script button —
// the surface that shows what the Yes would have run without running it.
func TestNewLeafDeleteStatements(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  NodeType
		obj  [2]string // schema, name
		want string
	}{
		// DROP TYPE covers all three type families; nothing in the statement
		// distinguishes them.
		{"alias type", NodeUserDefinedDataType, [2]string{"sales", "Phone"}, "DROP TYPE [sales].[Phone]"},
		{"table type", NodeUserDefinedTableType, [2]string{"sales", "IDList"}, "DROP TYPE [sales].[IDList]"},
		{"CLR type", NodeUserDefinedType, [2]string{"sales", "Point"}, "DROP TYPE [sales].[Point]"},
		{"XML schema collection", NodeXmlSchemaCollection, [2]string{"sales", "OrderSchema"},
			"DROP XML SCHEMA COLLECTION [sales].[OrderSchema]"},
		{"rule", NodeRule, [2]string{"sales", "PhoneRule"}, "DROP RULE [sales].[PhoneRule]"},
		{"default", NodeDefault, [2]string{"sales", "Zero"}, "DROP DEFAULT [sales].[Zero]"},
		{"assembly", NodeAssembly, [2]string{"", "Geometry"}, "DROP ASSEMBLY [Geometry]"},
		{"external data source", NodeExternalDataSource, [2]string{"", "RemoteDB"},
			"DROP EXTERNAL DATA SOURCE [RemoteDB]"},
		{"external file format", NodeExternalFileFormat, [2]string{"", "CSV"},
			"DROP EXTERNAL FILE FORMAT [CSV]"},
		{"external library", NodeExternalLibrary, [2]string{"", "ggplot2"},
			"DROP EXTERNAL LIBRARY [ggplot2]"},
		// A plan guide is dropped by sp_control_plan_guide, not by a DROP
		// statement.
		{"plan guide", NodePlanGuide, [2]string{"", "pg_orders"}, "sp_control_plan_guide"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := opTestConn(t)
			node := opTestNode(sc, tc.typ, tc.obj[0], tc.obj[1], "")
			before := a.panels.Count()

			a.deleteObject(node)
			answerScript(t, a, false)
			waitAndDrain(t, a)

			if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
				t.Errorf("Script executed %q — it must run nothing at all", stmts)
			}
			if got := scriptedText(t, a, before); !strings.Contains(got, tc.want) {
				t.Errorf("the query window holds %q, want %q", got, tc.want)
			}
		})
	}
}

// A type is not in sys.objects, so ALTER SCHEMA ... TRANSFER needs its class
// prefix. Without it the server refuses the move naming an object that does
// not exist — and the plain form is what a copy of the table's entry gives.
func TestMovingATypeUsesTheTypeClass(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  NodeType
		obj  string
		want string
	}{
		{"alias type", NodeUserDefinedDataType, "Phone", "ALTER SCHEMA [archive] TRANSFER TYPE::[sales].[Phone]"},
		{"XML schema collection", NodeXmlSchemaCollection, "OrderSchema",
			"ALTER SCHEMA [archive] TRANSFER XML SCHEMA COLLECTION::[sales].[OrderSchema]"},
		// A rule is an ordinary object and takes the default class.
		{"rule", NodeRule, "PhoneRule", "ALTER SCHEMA [archive] TRANSFER [sales].[PhoneRule]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := opTestConn(t)
			node := opTestNode(sc, tc.typ, "sales", tc.obj, "")
			op := objectOpFor(tc.typ)
			if op == nil || op.transfer == nil {
				t.Fatalf("%v offers no transfer", tc.typ)
			}
			if err := op.transfer(sc.Context(), sc, node.data, "archive"); err != nil {
				t.Fatalf("transfer: %v", err)
			}
			stmts := strings.Join(inst.StatementsIn("appdb"), "\n")
			if !strings.Contains(stmts, tc.want) {
				t.Errorf("statements = %q, want %q", stmts, tc.want)
			}
		})
	}
}

// An alias type renames through sp_rename's USERDATATYPE class. The OBJECT
// class is the trap: sp_rename reports success and renames nothing, because
// no sys.objects row has that name.
func TestRenamingAnAliasTypeUsesTheUserDataTypeClass(t *testing.T) {
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeUserDefinedDataType, "sales", "Phone", "")
	op := objectOpFor(NodeUserDefinedDataType)
	if op == nil || op.rename == nil {
		t.Fatal("an alias type offers no rename")
	}
	if err := op.rename(sc.Context(), sc, node.data, "PhoneNo"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	stmts := strings.Join(inst.StatementsIn("appdb"), "\n")
	if !strings.Contains(stmts, "USERDATATYPE") {
		t.Errorf("statements = %q, want sp_rename's USERDATATYPE class", stmts)
	}
}

// The schema-scoped families drop into a query editor as schema.name; the
// database-scoped ones have no schema to qualify with, and quoting the empty
// one would produce an invalid two-part name.
func TestExplorerDragTextForTheNewLeaves(t *testing.T) {
	for _, tc := range []struct {
		node *explorerNode
		want string
	}{
		{opNode(NodeUserDefinedDataType, "sales", "Phone", ""), "[sales].[Phone]"},
		{opNode(NodeUserDefinedTableType, "sales", "IDList", ""), "[sales].[IDList]"},
		{opNode(NodeUserDefinedType, "sales", "Point", ""), "[sales].[Point]"},
		{opNode(NodeXmlSchemaCollection, "sales", "OrderSchema", ""), "[sales].[OrderSchema]"},
		{opNode(NodeRule, "sales", "PhoneRule", ""), "[sales].[PhoneRule]"},
		{opNode(NodeDefault, "sales", "Zero", ""), "[sales].[Zero]"},
		{opNode(NodeAssembly, "", "Geometry", ""), "[Geometry]"},
		{opNode(NodePlanGuide, "", "pg_orders", ""), "[pg_orders]"},
		{opNode(NodeExternalDataSource, "", "RemoteDB", ""), "[RemoteDB]"},
		{opNode(NodeExternalFileFormat, "", "CSV", ""), "[CSV]"},
		{opNode(NodeExternalLibrary, "", "ggplot2", ""), "[ggplot2]"},
		// A built-in type is dropped as the type name it is written as.
		{opNode(NodeSystemDataType, "sys", "int", ""), "[int]"},
	} {
		if got := explorerDragText(tc.node); got != tc.want {
			t.Errorf("explorerDragText(%v %q) = %q, want %q",
				tc.node.data.Type, tc.node.data.Name, got, tc.want)
		}
	}
}

// A disabled plan guide shapes no plan and is invisible except for the label
// suffix, so the toggle is the item that makes the folder worth having — and
// it has to name the direction the guide is actually in.
func TestPlanGuideMenuOffersTheToggleInBothDirections(t *testing.T) {
	a := newTestApp()
	sc, _ := opTestConn(t)
	node := opTestNode(sc, NodePlanGuide, "", "pg_orders", "")

	node.data.IsEnabled = true
	labels := labelsOf(a.nodeMenuItems(node))
	if !slices.Contains(labels, "Disable") || slices.Contains(labels, "Enable") {
		t.Errorf("an enabled guide's menu = %v, want Disable", labels)
	}
	if !slices.Contains(labels, "Properties...") {
		t.Errorf("the toggle displaced Properties: %v", labels)
	}

	node.data.IsEnabled = false
	labels = labelsOf(a.nodeMenuItems(node))
	if !slices.Contains(labels, "Enable") || slices.Contains(labels, "Disable") {
		t.Errorf("a disabled guide's menu = %v, want Enable", labels)
	}
}

// Disabling is the direction that changes which plans the server picks, so it
// is confirmed; enabling is not. The statement is sp_control_plan_guide's,
// with the direction entirely in the parameters — which is why the arguments
// are what this reads, not the statement text.
func TestTogglingAPlanGuideConfirmsOnlyTheDisable(t *testing.T) {
	t.Run("disable asks first", func(t *testing.T) {
		a := newTestApp()
		sc, inst := opTestConn(t)
		node := opTestNode(sc, NodePlanGuide, "", "pg_orders", "")
		node.data.IsEnabled = true

		a.togglePlanGuide(sc, node)
		if !a.confirmDialog.Visible() {
			t.Fatal("disabling a plan guide ran without asking")
		}
		answerConfirm(t, a, false)
		waitAndDrain(t, a)

		assertPlanGuideOperation(t, inst, "DISABLE")
		if node.data.IsEnabled {
			t.Error("the node still reads as enabled")
		}
	})

	t.Run("enable does not", func(t *testing.T) {
		a := newTestApp()
		sc, inst := opTestConn(t)
		node := opTestNode(sc, NodePlanGuide, "", "pg_orders", "")
		node.data.IsEnabled = false

		a.togglePlanGuide(sc, node)
		if a.confirmDialog.Visible() {
			t.Fatal("enabling a plan guide asked for confirmation")
		}
		waitAndDrain(t, a)

		assertPlanGuideOperation(t, inst, "ENABLE")
		if !node.data.IsEnabled {
			t.Error("the node still reads as disabled")
		}
	})
}

// assertPlanGuideOperation pins which way sp_control_plan_guide was called.
// Its statement text is identical for ENABLE and DISABLE — the direction is
// entirely in the parameters, so Statements() cannot tell the two apart.
func assertPlanGuideOperation(t *testing.T, inst *fakeInstance, want string) {
	t.Helper()
	args, ok := inst.ExecArgs("sp_control_plan_guide")
	if !ok {
		t.Fatal("the toggle did not run sp_control_plan_guide")
	}
	if len(args) < 2 || args[0] != want || args[1] != "pg_orders" {
		t.Errorf("sp_control_plan_guide ran with %v, want %s on pg_orders", args, want)
	}
}
