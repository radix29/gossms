package tui

import (
	"database/sql/driver"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The Script cascade is the only way to script an object, so a node type
// missing from the scriptables table can't be scripted at all.
func TestScriptMenuItemsPerNodeType(t *testing.T) {
	tests := []struct {
		name  string
		node  *explorerNode
		item  string // "" = no Script item at all
		verbs []string
	}{
		{"table", opNode(NodeTable, "dbo", "Orders", ""), "Script Table as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To", "SELECT To", "INSERT To", "UPDATE To", "DELETE To"}},
		{"view", opNode(NodeView, "dbo", "vOrders", ""), "Script View as",
			[]string{"CREATE To", "ALTER To", "DROP To", "DROP And CREATE To", "SELECT To", "INSERT To", "UPDATE To", "DELETE To"}},
		{"procedure", opNode(NodeStoredProcedure, "dbo", "pDoWork", ""), "Script Stored Procedure as",
			[]string{"CREATE To", "ALTER To", "DROP To", "DROP And CREATE To", "EXECUTE To"}},
		{"function", opNode(NodeFunction, "dbo", "fnAge", ""), "Script Function as",
			[]string{"CREATE To", "ALTER To", "DROP To", "DROP And CREATE To", "SELECT To"}},
		// An index adds the three maintenance statements below its DDL ones.
		{"index", opNode(NodeIndex, "dbo", "IX_Orders", "Orders"), "Script Index as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To",
				"REBUILD To", "REORGANIZE To", "UPDATE STATISTICS To"}},
		// A statistic scripts through gosmo, filter and options included; the
		// Properties page's Script as CREATE is the same generator.
		{"statistic", opNode(NodeStatistic, "dbo", "st_Orders", "Orders"), "Script Statistics as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To", "UPDATE STATISTICS To"}},
		// A key is an index too, but ALTER INDEX on a constraint-backing one
		// is Index Properties' business, not the constraint's menu.
		{"key", opNode(NodeKey, "dbo", "PK_Orders", "Orders"), "Script Key as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To"}},
		{"login", opNode(NodeLogin, "", "app_login", ""), "Script Login as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To"}},
		// A database has no DROP form — see scriptables.
		{"database", opNode(NodeDatabase, "", "AppDB", ""), "Script Database as", []string{"CREATE To", "DROP To", "DROP And CREATE To"}},
		// Folders and columns are not objects that script.
		{"tables folder", opNode(NodeTables, "", "", ""), "", nil},
		{"column", opNode(NodeColumn, "dbo", "OrderID", ""), "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{}
			items := a.scriptMenuItems(tt.node)
			if tt.item == "" {
				if len(items) != 0 {
					t.Fatalf("scriptMenuItems = %v, want none", labelsOf(items))
				}
				return
			}
			if len(items) != 1 || items[0].Label != tt.item {
				t.Fatalf("scriptMenuItems = %v, want one %q", labelsOf(items), tt.item)
			}
			if got := labelsOf(items[0].Sub); !slices.Equal(got, tt.verbs) {
				t.Errorf("verbs = %v, want %v", got, tt.verbs)
			}
			// Every verb is a cascade of its own, never a leaf that fires
			// without the user saying where the script should go.
			for _, v := range items[0].Sub {
				if v.Action != nil {
					t.Errorf("verb %q fires an action directly", v.Label)
				}
				if got := labelsOf(v.Sub); !slices.Equal(got, []string{"New Query Editor Window", "File...", "Clipboard"}) {
					t.Errorf("verb %q destinations = %v", v.Label, got)
				}
			}
		})
	}
}

// The Script cascade is spliced in above Rename/Delete, matching SSMS's
// order, and every destination under it is reachable.
func TestScriptCascadeSitsAboveRenameInTheNodeMenu(t *testing.T) {
	a := &App{}
	node := opNode(NodeTable, "dbo", "Orders", "")
	labels := labelsOf(a.contextMenuItemsForNode(node))
	script := slices.Index(labels, "Script Table as")
	rename := slices.Index(labels, "Rename...")
	refresh := slices.Index(labels, refreshMenuLabel)
	if script < 0 || rename < 0 || refresh < 0 {
		t.Fatalf("menu %v is missing one of Script/Rename/Refresh", labels)
	}
	if !(script < rename && rename < refresh) {
		t.Errorf("order = Script %d, Rename %d, Refresh %d in %v", script, rename, refresh, labels)
	}
}

func TestScriptFileNameQualifiesBySchema(t *testing.T) {
	for _, tt := range []struct {
		n    nodeData
		want string
	}{
		{nodeData{Schema: "dbo", Name: "Orders"}, "dbo.Orders.sql"},
		{nodeData{Name: "app_login"}, "app_login.sql"},
		{nodeData{Schema: "dbo", Name: "a/b"}, "dbo.a_b.sql"},
		{nodeData{Schema: "d:o", Name: "q*?"}, "d_o.q__.sql"},
		// A Windows device stem is reserved whatever follows the dot. Each part
		// is made safe on its own, so a device name after the schema is
		// prefixed too: unneeded there, but harmless.
		{nodeData{Name: "CON"}, "_CON.sql"},
		{nodeData{Schema: "aux", Name: "t"}, "_aux.t.sql"},
		{nodeData{Schema: "dbo", Name: "NUL"}, "dbo._NUL.sql"},
	} {
		if got := scriptFileName(tt.n); got != tt.want {
			t.Errorf("scriptFileName(%+v) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// A system module can't be scripted at all — its definition is in the
// resource database — so it offers no Script item. A system database still
// does: its script is assembled from metadata, not from sys.sql_modules.
func TestScriptMenuItemsOnSystemObjects(t *testing.T) {
	view := opNode(NodeView, "sys", "objects", "")
	view.data.IsSystem = true
	if items := (&App{}).scriptMenuItems(view); len(items) != 0 {
		t.Errorf("system view offers %v", labelsOf(items))
	}
	master := opNode(NodeDatabase, "", "master", "")
	master.data.IsSystem = true
	if items := (&App{}).scriptMenuItems(master); len(items) != 1 {
		t.Errorf("system database offers %v, want the Script cascade", labelsOf(items))
	}
}

// TestScriptToFileAndClipboardOpensInTheObjectsDatabase pins review plan X14:
// File... and Clipboard wrote the bare object script, so a DROP copied from
// one database and run in a window on another dropped that database's
// namesake. Every destination is handed the same text, and it opens with USE.
func TestScriptToFileAndClipboardOpensInTheObjectsDatabase(t *testing.T) {
	const want = "USE [" + brokerDB + "];\nGO\n"
	n := nodeData{Type: NodeMessageType, DBName: brokerDB, Name: "//claims/Submit"}
	newApp := func(t *testing.T) (*App, controls.MenuItem) {
		sc, _ := newFakeConn(t, brokerDBResp(), brokerRowByArg(messageTypeResp(), n.Name, 1))
		a := newClipboardTestApp()
		dests := a.scriptDestinations(sc, n, scriptables[NodeMessageType].verbs[1]) // DROP To
		return a, controls.MenuItem{Sub: dests}
	}
	dest := func(m controls.MenuItem, label string) func() {
		for _, d := range m.Sub {
			if d.Label == label {
				return d.Action
			}
		}
		t.Fatalf("no %q destination", label)
		return nil
	}

	t.Run("Clipboard", func(t *testing.T) {
		got := make(chan string, 1)
		old := clipboardWriter
		clipboardWriter = func(text string) bool { got <- text; return true }
		defer func() { clipboardWriter = old }()
		a, m := newApp(t)
		dest(m, "Clipboard")()
		var text string
		drainUntil(t, a, func() bool {
			select {
			case text = <-got:
				return true
			default:
				return false
			}
		}, "the clipboard write")
		if !strings.HasPrefix(text, want) || !strings.Contains(text, "DROP MESSAGE TYPE") {
			t.Errorf("clipboard text does not open with %q:\n%s", want, text)
		}
	})

	t.Run("File", func(t *testing.T) {
		a, m := newApp(t)
		dest(m, "File...")()
		drainUntil(t, a, a.fileDialog.Visible, "the save prompt")
		path := filepath.Join(t.TempDir(), "drop.sql")
		a.fileDialog.OnChoose(path)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if text := string(b); !strings.HasPrefix(text, want) || !strings.Contains(text, "DROP MESSAGE TYPE") {
			t.Errorf("file text does not open with %q:\n%s", want, text)
		}
	})
}

func TestFabricDWExternalFunctionScriptMenuUsesStoredDefinition(t *testing.T) {
	const options = "SET ANSI_NULLS ON;\nGO\nSET QUOTED_IDENTIFIER ON;\nGO\n"
	const drop = "DROP FUNCTION IF EXISTS [analytics].[Predict];\nGO\n"
	const body = " FUNCTION [analytics].[Predict] AS EXTERNAL FUNCTION [Functions].[PredictPython];"
	for _, tc := range []struct{ name, definition, create, alter string }{
		{"create", "CREATE" + body, "CREATE" + body, "ALTER" + body},
		{"stored alter", "ALTER" + body, "CREATE" + body, "ALTER" + body},
		{"commented create", "CREATE/* proxy */" + body, "CREATE/* proxy */" + body, "ALTER/* proxy */" + body},
		{"commented create or alter", "CREATE OR/* proxy */ALTER" + body, "CREATE OR/* proxy */ALTER" + body, "CREATE OR/* proxy */ALTER" + body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConn(t, treeFamilyDatabaseRow(),
				fakeResponse{match: "WHERE  o.type = 'XF'", cols: 6, rows: [][]driver.Value{
					{int64(42), "analytics", "Predict", treeFamilyCreated, treeFamilyCreated, "XF"},
				}},
				fakeResponse{match: "o.type IN ('FN','TF','IF','FS','FT','XF')", cols: 6, rows: [][]driver.Value{
					{tc.definition, true, true, false, int64(42), "XF"},
				}},
				fakeResponse{match: "FROM   sys.extended_properties ep", cols: 11},
				fakeResponse{match: "FROM   sys.parameters p", cols: 15, rows: [][]driver.Value{
					{"@input", int64(1), "int", int64(4), int64(10), int64(0), false, false,
						"sys", false, "", "", false, int64(0), ""},
				}},
			)
			sc.IsFabricDW = true
			functions := loadFamily(t, sc, NodeExternalFunctions)
			if len(functions) != 1 {
				t.Fatalf("external functions = %v, want one function", labelsOfNodes(functions))
			}
			node := functions[0]
			menu := (&App{}).scriptMenuItems(node)
			if len(menu) != 1 || menu[0].Label != "Script Function as" {
				t.Fatalf("external function script menu = %v", labelsOf(menu))
			}
			want := map[string]string{
				"CREATE To":          options + tc.create + "\nGO\n",
				"ALTER To":           options + tc.alter + "\nGO\n",
				"DROP To":            drop,
				"DROP And CREATE To": drop + "\n" + options + tc.create + "\nGO\n",
				"SELECT To":          "SELECT [analytics].[Predict](<input, int,>) AS N'Predict';\nGO\n",
			}
			verbs := scriptables[node.data.Type].verbs
			if len(verbs) != len(want) || len(menu[0].Sub) != len(want) {
				t.Fatalf("external function offers %d generators and %d menu verbs, want %d", len(verbs), len(menu[0].Sub), len(want))
			}
			for _, verb := range verbs {
				text, err := verb.gen(t.Context(), sc, node.data)
				expected, ok := want[verb.label]
				if !ok || err != nil || text != "USE [appdb];\nGO\n"+expected {
					t.Errorf("%s: script = %q, error = %v; want database context and %q", verb.label, text, err, expected)
				}
			}
			if len(inst.Reads("p.vector_base_type_desc = N'float16'")) != 0 ||
				len(inst.Reads("sys.assembly_modules")) != 0 || len(inst.Statements()) != 0 {
				t.Error("scripting an external function queried vector/CLR metadata or executed a write")
			}
			inst.mu.Lock()
			defer inst.mu.Unlock()
			if len(inst.unmatched) != 0 {
				t.Errorf("script menu made unexpected reads:\n%s", strings.Join(inst.unmatched, "\n"))
			}
		})
	}
}

// A server-scoped object belongs to no database, so its script takes no USE:
// one would pin it to whatever database the menu happened to be opened from.
func TestScriptOfAServerScopedObjectTakesNoUse(t *testing.T) {
	sc, _ := newFakeConn(t, serverRoleResponses()...)
	n := nodeData{Type: NodeServerRole, Name: principalSrvRole}
	for _, v := range scriptables[NodeServerRole].verbs {
		text, err := v.gen(t.Context(), sc, n)
		if err != nil {
			t.Fatalf("%s: %v", v.label, err)
		}
		if strings.Contains(text, "USE ") {
			t.Errorf("%s carries a USE:\n%s", v.label, text)
		}
	}
}
