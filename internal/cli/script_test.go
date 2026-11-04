package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/scripting"
)

// scriptFake is objectFake plus Script As: each call recorded, the script
// text naming what was asked for, or err when set.
type scriptFake struct {
	*objectFake
	calls []scriptCall
	err   error
}

type scriptCall struct {
	f scripting.Family
	v scripting.Verb
	t scripting.Target
}

func newScriptFake() *scriptFake {
	c := &scriptFake{objectFake: newObjectFake()}
	shipped := &gosmo.ObjectInfo{ObjectID: 99, Schema: "dbo", Name: "sp_shipped", Type: "P",
		TypeDesc: "SQL_STORED_PROCEDURE", IsMSShipped: true}
	c.objects = append(c.objects, shipped)
	for _, o := range c.objects {
		if o.Name == "PK_Customer" {
			o.ParentSchema, o.ParentName = "Sales", "Customer"
		}
	}
	return c
}

func (c *scriptFake) Script(_ context.Context, f scripting.Family, v scripting.Verb, t scripting.Target) (string, error) {
	c.calls = append(c.calls, scriptCall{f, v, t})
	if c.err != nil {
		return "", c.err
	}
	return fmt.Sprintf("USE [%s];\nGO\n-- family %d verb %d %s.%s\nGO\n", t.DBName, f, v, t.Schema, t.Name), nil
}

func runScript(t *testing.T, cat *scriptFake, argv ...string) runResult {
	t.Helper()
	return runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)),
		append(append([]string{"script"}, argv...), "-p", "dev", "-o", "json")...)
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

func TestScriptObjectFamilies(t *testing.T) {
	for _, tc := range []struct {
		object, as string
		family     scripting.Family
		verb       scripting.Verb
		kind       string
		funcType   gosmo.FunctionType
	}{
		{"Sales.Customer", "", scripting.Table, scripting.Create, "table", ""},
		{"Sales.Customer", "insert", scripting.Table, scripting.Insert, "table", ""},
		{"vCustomer", "alter", scripting.View, scripting.Alter, "view", ""},
		{"Sales.usp_Get", "execute", scripting.StoredProcedure, scripting.Execute, "procedure", ""},
		{"Sales.p_clr", "drop", scripting.StoredProcedure, scripting.Drop, "procedure", ""},
		{"fScalar", "select", scripting.Function, scripting.Select, "function", gosmo.FunctionTypeScalar},
		{"dbo.fInline", "drop-and-create", scripting.Function, scripting.DropAndCreate, "function", gosmo.FunctionTypeInlineTable},
		{"trCustomer", "alter", scripting.Trigger, scripting.Alter, "trigger", ""},
		{"dbo.sn", "", scripting.Synonym, scripting.Create, "synonym", ""},
		{"seq", "drop", scripting.Sequence, scripting.Drop, "sequence", ""},
	} {
		cat := newScriptFake()
		argv := []string{"object", tc.object}
		if tc.as != "" {
			argv = append(argv, "--as", tc.as)
		}
		r := runScript(t, cat, argv...)
		if r.exit != 0 || len(cat.calls) != 1 {
			t.Errorf("%v: exit %d, calls %v\n%s", argv, r.exit, cat.calls, r.stdout)
			continue
		}
		c := cat.calls[0]
		// Each case names the first object of its name in the fake.
		name := tc.object[strings.LastIndex(tc.object, ".")+1:]
		info := cat.objects[slices.IndexFunc(cat.objects, func(o *gosmo.ObjectInfo) bool { return o.Name == name })]
		want := scripting.Target{DBName: "app", Schema: info.Schema, Name: info.Name, FuncType: tc.funcType}
		if c.f != tc.family || c.v != tc.verb || c.t != want {
			t.Errorf("%v: Script(%v, %v, %+v), want (%v, %v, %+v)", argv, c.f, c.v, c.t, tc.family, tc.verb, want)
		}
		env := envelope(t, r.stdout)
		verb := tc.as
		if verb == "" {
			verb = "create"
		}
		if dataField(t, env, "object") != gosmo.QualifiedName(info.Schema, info.Name) ||
			dataField(t, env, "type") != tc.kind || dataField(t, env, "verb") != verb ||
			!strings.HasPrefix(dataField(t, env, "script").(string), "USE [app];\nGO\n") {
			t.Errorf("%v: record %v", argv, env["data"])
		}
	}
}

// TestScriptObjectText: the script is a Verbatim field, under its marker and
// uncut by --max-col-width.
func TestScriptObjectText(t *testing.T) {
	cat := newScriptFake()
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)),
		"script", "object", "Sales.Customer", "-p", "dev", "--max-col-width", "5")
	if r.exit != 0 || !strings.Contains(r.stdout, "=== SCRIPT (4 lines) ===\nUSE [app];\nGO\n-- family 2 verb 1 Sales.Customer\nGO\n") {
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
		cat := newScriptFake()
		r := runScript(t, cat, append([]string{"object"}, tc.argv...)...)
		if r.exit != tc.exit || len(cat.calls) != 0 || !strings.Contains(r.stdout+r.stderr, tc.want) {
			t.Errorf("%v: exit %d, calls %v, want %d and %q\n%s%s", tc.argv, r.exit, cat.calls, tc.exit, tc.want, r.stdout, r.stderr)
		}
	}
}

// TestScriptObjectErrors pins the exit codes chosen from what gosmo returns
// (found live on 17): a definition hidden from the login is 9, an encrypted
// module 11, each with its own hint.
func TestScriptObjectErrors(t *testing.T) {
	hidden := fmt.Errorf("gosmo: script stored procedure [Sales].[usp_Get]: %w; it needs VIEW DEFINITION on it", gosmo.ErrDefinitionHidden)
	encrypted := fmt.Errorf("gosmo: script stored procedure [Sales].[p_secret]: definition is not readable (encrypted): %w", gosmo.ErrUnsupported)
	for _, tc := range []struct {
		object string
		err    error
		exit   int
		code   string
		hint   string
	}{
		{"Sales.usp_Get", hidden, 9, "PERMISSION_DENIED", "needs VIEW DEFINITION"},
		{"Sales.p_secret", encrypted, 11, "UNSUPPORTED", "WITH ENCRYPTION"},
	} {
		cat := newScriptFake()
		cat.err = tc.err
		r := runScript(t, cat, "object", tc.object)
		e := firstError(t, envelope(t, r.stdout))
		if r.exit != tc.exit || e["code"] != tc.code || !strings.Contains(fmt.Sprint(e["hint"]), tc.hint) {
			t.Errorf("%s: exit %d, error %v", tc.object, r.exit, e)
		}
	}
}

func TestScriptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "customer.sql")
	cat := newScriptFake()
	r := runScript(t, cat, "object", "Sales.Customer", "--script-file", path)
	env := envelope(t, r.stdout)
	got, err := os.ReadFile(path)
	if r.exit != 0 || err != nil || string(got) != dataField(t, env, "script") || dataField(t, env, "scriptFile") != path {
		t.Fatalf("exit %d, file %q (%v), record %v", r.exit, got, err, env["data"])
	}

	// An existing file is refused before connecting, unless --force.
	cat = newScriptFake()
	r = runScript(t, cat, "object", "Sales.Customer", "--script-file", path)
	if r.exit != 2 || len(cat.calls) != 0 || !strings.Contains(r.stdout+r.stderr, "the file exists") {
		t.Errorf("existing: exit %d, calls %v\n%s", r.exit, cat.calls, r.stdout)
	}
	if r = runScript(t, cat, "object", "Sales.Customer", "--as", "drop", "--script-file", path, "--force"); r.exit != 0 {
		t.Errorf("--force: exit %d\n%s", r.exit, r.stdout)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "verb 3 ") {
		t.Errorf("--force did not replace the file: %q", got)
	}

	// A failed script writes nothing.
	fresh := filepath.Join(t.TempDir(), "none.sql")
	cat = newScriptFake()
	cat.err = fmt.Errorf("gosmo: script view: %w", gosmo.ErrDefinitionHidden)
	if r = runScript(t, cat, "object", "vCustomer", "--script-file", fresh); r.exit != 9 {
		t.Errorf("failed script: exit %d", r.exit)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("failed script wrote %s", fresh)
	}

	// --force alone names the files script object can write.
	r = runScript(t, newScriptFake(), "object", "Sales.Customer", "--force")
	if r.exit != 2 || !strings.Contains(r.stdout+r.stderr, "--force: only with --out-file or --script-file") {
		t.Errorf("--force alone: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}

func TestScriptDB(t *testing.T) {
	cat := newScriptFake()
	r := runScript(t, cat, "db", "ARCHIVE", "--as", "drop-and-create")
	env := envelope(t, r.stdout)
	want := scriptCall{scripting.Database, scripting.DropAndCreate, scripting.Target{DBName: "archive"}}
	if r.exit != 0 || len(cat.calls) != 1 || cat.calls[0] != want ||
		dataField(t, env, "database") != "archive" || dataField(t, env, "verb") != "drop-and-create" || env["database"] != "archive" {
		t.Errorf("exit %d, calls %v, env %v", r.exit, cat.calls, env)
	}

	cat = newScriptFake()
	if r = runScript(t, cat, "db", "nosuch"); r.exit != 8 || len(cat.calls) != 0 {
		t.Errorf("unknown database: exit %d, calls %v", r.exit, cat.calls)
	}
	if r = runScript(t, cat, "db", "app", "--as", "alter"); r.exit != 2 || !strings.Contains(r.stdout+r.stderr, "One of: create, drop, drop-and-create") {
		t.Errorf("--as alter: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}
