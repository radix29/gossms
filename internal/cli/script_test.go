package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runScript(t *testing.T, s *scriptServer, argv ...string) runResult {
	t.Helper()
	return runTree(t, context.Background(), newCommandTree(objectDeps(t, s.objectServer)),
		append(append([]string{"script"}, argv...), "-p", "dev", "-o", "json")...)
}

// scriptReads is the queries s answered that only scripting makes, past
// connecting and resolving the name: a refusal must come before any.
func scriptReads(s *scriptServer) []string {
	var out []string
	for _, q := range s.Queries() {
		for _, mark := range []string{"sys.sql_modules", "temporal_type", "OBJECT_ID(@p1)", "sys.extended_properties",
			"sys.parameters", "sys.sequences", "sys.synonyms", "sys.sql_expression_dependencies"} {
			if strings.Contains(q.Text, mark) {
				out = append(out, q.String())
				break
			}
		}
	}
	return out
}

// dataField is the envelope's data record field key.
func dataField(t *testing.T, env map[string]any, key string) any {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data record in %v", env)
	}
	return data[key]
}

// TestScriptObjectFamilies scripts each family through real gosmo: the
// script's own text shows the family, verb and function type the command
// chose reached the scripter.
func TestScriptObjectFamilies(t *testing.T) {
	for _, tc := range []struct {
		object, as string
		name, kind string
		want       []string
	}{
		{"Sales.Customer", "", "[Sales].[Customer]", "table", []string{
			"/* Table: [Sales].[Customer]  Database: app */",
			"CREATE TABLE [Sales].[Customer] (\n    [id] int IDENTITY(1,1) NOT NULL,",
			"CREATE NONCLUSTERED INDEX [IX_Customer_Name]",
			"ADD CONSTRAINT [FK_Customer_Region]",
			"ADD CONSTRAINT [CK_pct] CHECK ([pct]>=(0));",
			"EXEC sys.sp_addextendedproperty @name = N'MS_Description', @value = N'Customers'",
		}},
		{"Sales.Customer", "insert", "[Sales].[Customer]", "table", []string{
			"INSERT INTO [Sales].[Customer]\n           ([CustomerName])\nVALUES     (<CustomerName, nvarchar(50),>);",
		}},
		// A DROP names what would block it.
		{"Sales.Customer", "drop", "[Sales].[Customer]", "table", []string{
			"--   [Sales].[vCustomer] (VIEW)", "--   [Sales].[Order].[FK_Order_Customer] (FOREIGN KEY)",
			"    DROP TABLE [Sales].[Customer];",
		}},
		{"vCustomer", "alter", "[Sales].[vCustomer]", "view", []string{
			"ALTER VIEW Sales.vCustomer WITH SCHEMABINDING", "CREATE UNIQUE CLUSTERED INDEX [CIX_vCustomer]",
		}},
		{"Sales.usp_Get", "execute", "[Sales].[usp_Get]", "procedure", []string{
			"EXEC @return_value = [Sales].[usp_Get]\n     @id = <id, int,>,\n     @total = @total OUTPUT;",
		}},
		{"Sales.usp_Get", "", "[Sales].[usp_Get]", "procedure", []string{
			"SET QUOTED_IDENTIFIER ON;\nGO\nCREATE PROCEDURE Sales.usp_Get AS RETURN 1\nGO\n",
		}},
		{"Sales.p_clr", "drop", "[Sales].[p_clr]", "procedure", []string{"DROP PROCEDURE IF EXISTS [Sales].[p_clr];"}},
		// A scalar function's SELECT calls it, which only its function
		// type tells.
		{"fScalar", "select", "[dbo].[fScalar]", "function", []string{
			"SELECT [dbo].[fScalar](<id, int,>, <total, decimal(9,2),>) AS N'fScalar';",
		}},
		{"dbo.fInline", "drop-and-create", "[dbo].[fInline]", "function", []string{
			"DROP FUNCTION IF EXISTS [dbo].[fInline];", "CREATE FUNCTION dbo.fInline() RETURNS TABLE",
		}},
		{"trCustomer", "alter", "[Sales].[trCustomer]", "trigger", []string{"ALTER TRIGGER Sales.trCustomer ON Sales.Customer"}},
		// A trigger's CREATE restores its firing order and disabled state.
		{"trCustomer", "", "[Sales].[trCustomer]", "trigger", []string{
			"@order = N'First', @stmttype = N'INSERT';", "DISABLE TRIGGER [Sales].[trCustomer] ON [Sales].[Customer];",
		}},
		{"dbo.sn", "", "[dbo].[sn]", "synonym", []string{"CREATE SYNONYM [dbo].[sn] FOR [other].[dbo].[t];"}},
		{"seq", "", "[dbo].[seq]", "sequence", []string{"CREATE SEQUENCE [dbo].[seq]\n    AS [decimal](12,0)", "CACHE 50;"}},
		{"seq", "drop", "[dbo].[seq]", "sequence", []string{"DROP SEQUENCE IF EXISTS [dbo].[seq];"}},
	} {
		s := newScriptServer(t)
		argv := []string{"object", tc.object}
		if tc.as != "" {
			argv = append(argv, "--as", tc.as)
		}
		r := runScript(t, s, argv...)
		if r.exit != 0 {
			t.Errorf("%v: exit %d\n%s%s", argv, r.exit, r.stdout, r.stderr)
			continue
		}
		env := envelope(t, r.stdout)
		verb := tc.as
		if verb == "" {
			verb = "create"
		}
		script, _ := dataField(t, env, "script").(string)
		if dataField(t, env, "object") != tc.name || dataField(t, env, "type") != tc.kind || dataField(t, env, "verb") != verb ||
			!strings.HasPrefix(script, "USE [app];\nGO\n") {
			t.Errorf("%v: record %v", argv, env["data"])
		}
		for _, w := range tc.want {
			if !strings.Contains(script, w) {
				t.Errorf("%v: script lacks %q\n%s", argv, w, script)
			}
		}
	}
}

// TestScriptObjectText: the script is a Verbatim field, under its marker and
// uncut by --max-col-width.
func TestScriptObjectText(t *testing.T) {
	s := newScriptServer(t)
	r := runTree(t, context.Background(), newCommandTree(objectDeps(t, s.objectServer)),
		"script", "object", "dbo.sn", "-p", "dev", "--max-col-width", "5")
	want := "=== SCRIPT (5 lines) ===\nUSE [app];\nGO\nIF OBJECT_ID(N'[dbo].[sn]', N'SN') IS NULL\n" +
		"CREATE SYNONYM [dbo].[sn] FOR [other].[dbo].[t];\nGO\n"
	if r.exit != 0 || !strings.Contains(r.stdout, want) {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
}

func TestScriptObjectRefusals(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		exit int
		want string
	}{
		// A verb the kind lacks names the kind's verbs, before any script.
		{[]string{"trCustomer", "--as", "select"}, 2, "a trigger offers create, alter, drop, drop-and-create"},
		{[]string{"Sales.Customer", "--as", "alter"}, 2, "a table offers create, drop, drop-and-create, select, insert, update, delete"},
		{[]string{"dbo.sn", "--as", "execute"}, 2, "a synonym offers create, drop, drop-and-create"},
		// Not an --as value at all.
		{[]string{"Sales.Customer", "--as", "rebuild"}, 2, `"rebuild" is not a valid value`},
		// A table's child is scripted with its table.
		{[]string{"PK_Customer"}, 2, "gossms script object [Sales].[Customer]"},
		{[]string{"dbo.sp_shipped"}, 2, "is a system object"},
		{[]string{"Sales.Nope"}, 8, "no object"},
	} {
		s := newScriptServer(t)
		r := runScript(t, s, append([]string{"object"}, tc.argv...)...)
		if reads := scriptReads(s); r.exit != tc.exit || len(reads) != 0 || !strings.Contains(r.stdout+r.stderr, tc.want) {
			t.Errorf("%v: exit %d, script reads %v, want %d and %q\n%s%s", tc.argv, r.exit, reads, tc.exit, tc.want, r.stdout, r.stderr)
		}
	}
}

// TestScriptObjectErrors pins the exit codes chosen from what gosmo returns
// (found live on 17): a definition hidden from the login is 9, an encrypted
// module 11, each with its own hint.
func TestScriptObjectErrors(t *testing.T) {
	for _, tc := range []struct {
		argv  []string
		setup func(*scriptServer)
		exit  int
		code  string
		hint  string
	}{
		{[]string{"Sales.usp_Get"}, func(s *scriptServer) { s.hiddenDefinition = true }, 9, "PERMISSION_DENIED", "needs VIEW DEFINITION"},
		{[]string{"Sales.p_secret"}, nil, 11, "UNSUPPORTED", "WITH ENCRYPTION"},
		// A table's DROP refused the schema-bound dependents read: that
		// read's permission, not VIEW DEFINITION, is the hint.
		{[]string{"Sales.Customer", "--as", "drop"}, func(s *scriptServer) { s.boundErr = deniedDependents }, 9, "PERMISSION_DENIED",
			"reads what is schema-bound to it, which takes SELECT on sys.sql_expression_dependencies"},
	} {
		s := newScriptServer(t)
		if tc.setup != nil {
			tc.setup(s)
		}
		r := runScript(t, s, append([]string{"object"}, tc.argv...)...)
		e := firstError(t, envelope(t, r.stdout))
		if r.exit != tc.exit || e["code"] != tc.code || !strings.Contains(fmt.Sprint(e["hint"]), tc.hint) {
			t.Errorf("%v: exit %d, error %v", tc.argv, r.exit, e)
		}
	}
}

func TestScriptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "customer.sql")
	r := runScript(t, newScriptServer(t), "object", "Sales.Customer", "--script-file", path)
	env := envelope(t, r.stdout)
	got, err := os.ReadFile(path)
	if r.exit != 0 || err != nil || string(got) != dataField(t, env, "script") || dataField(t, env, "scriptFile") != path {
		t.Fatalf("exit %d, file %q (%v), record %v", r.exit, got, err, env["data"])
	}

	// An existing file is refused before connecting, unless --force.
	s := newScriptServer(t)
	r = runScript(t, s, "object", "Sales.Customer", "--script-file", path)
	if r.exit != 2 || len(s.Queries()) != 0 || !strings.Contains(r.stdout+r.stderr, "the file exists") {
		t.Errorf("existing: exit %d, queries %v\n%s", r.exit, s.Queries(), r.stdout)
	}
	if r = runScript(t, s, "object", "Sales.Customer", "--as", "drop", "--script-file", path, "--force"); r.exit != 0 {
		t.Errorf("--force: exit %d\n%s", r.exit, r.stdout)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "DROP TABLE [Sales].[Customer];") {
		t.Errorf("--force did not replace the file: %q", got)
	}

	// A failed script writes nothing.
	fresh := filepath.Join(t.TempDir(), "none.sql")
	s = newScriptServer(t)
	s.hiddenDefinition = true
	if r = runScript(t, s, "object", "Sales.usp_Get", "--script-file", fresh); r.exit != 9 {
		t.Errorf("failed script: exit %d", r.exit)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("failed script wrote %s", fresh)
	}

	// --force alone names the files script object can write.
	r = runScript(t, newScriptServer(t), "object", "Sales.Customer", "--force")
	if r.exit != 2 || !strings.Contains(r.stdout+r.stderr, "--force: only with --out-file or --script-file") {
		t.Errorf("--force alone: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}

// TestScriptDB covers the command's own part — the name resolved to its
// catalog spelling, --as passed through — with a DROP, which reads nothing
// more. A CREATE's file, option and settings reads are gosmo's to test
// (scripter_test.go): rows for them here would test nothing of gossms's.
func TestScriptDB(t *testing.T) {
	r := runScript(t, newScriptServer(t), "db", "APP", "--as", "drop")
	env := envelope(t, r.stdout)
	script, _ := dataField(t, env, "script").(string)
	if r.exit != 0 || dataField(t, env, "database") != "app" || dataField(t, env, "verb") != "drop" || env["database"] != "app" ||
		!strings.Contains(script, "USE [master];\nGO\nIF DB_ID(N'app') IS NOT NULL\n    DROP DATABASE [app];\nGO\n") {
		t.Errorf("exit %d, env %v", r.exit, env)
	}

	if r = runScript(t, newScriptServer(t), "db", "nosuch"); r.exit != 8 {
		t.Errorf("unknown database: exit %d\n%s", r.exit, r.stdout)
	}
	if r = runScript(t, newScriptServer(t), "db", "app", "--as", "alter"); r.exit != 2 || !strings.Contains(r.stdout+r.stderr, "One of: create, drop, drop-and-create") {
		t.Errorf("--as alter: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}

// TestScriptDBObjectsRefusals: the two flag combinations script db --objects
// cannot honour are usage errors before any connection is made.
func TestScriptDBObjectsRefusals(t *testing.T) {
	for _, argv := range [][]string{
		{"db", "app", "--include-permissions"},
		{"db", "app", "--objects", "--as", "drop"},
	} {
		s := newScriptServer(t)
		r := runScript(t, s, argv...)
		if r.exit != 2 {
			t.Errorf("%v: exit %d\n%s%s", argv, r.exit, r.stdout, r.stderr)
		}
		if reads := scriptReads(s); len(reads) > 0 {
			t.Errorf("%v: scripting read before refusing: %v", argv, reads)
		}
	}
}
