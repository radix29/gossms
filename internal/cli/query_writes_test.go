package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// targetServer answers the reads of --max-rows-affected's check on the write
// targets (cmd.checkWriteTargets) through gosmo's own queries: the session's
// database, databases by name, objects by name, tables, their triggers, the
// foreign keys referencing them and synonyms. Everything lives in the
// databases of objects; a database not there does not exist.
type targetServer struct {
	*gosmotest.Fake
	current string
	// objects by database; ObjectID must be unique across them.
	objects  map[string][]*gosmo.ObjectInfo
	triggers map[int][]*gosmo.Trigger    // by the table's ObjectID
	refs     map[int][]*gosmo.ForeignKey // the keys referencing the table, by its ObjectID
	synonyms map[int]string              // base object, by the synonym's ObjectID
}

func tableObject(id int, schema, name string) *gosmo.ObjectInfo {
	return &gosmo.ObjectInfo{ObjectID: id, Schema: schema, Name: name, Type: "U", TypeDesc: "USER_TABLE", Created: objectsAt, Modified: objectsAt}
}

// newTargetServer is a server whose database app holds dbo.t, a table with
// neither triggers nor referencing keys.
func newTargetServer(t *testing.T) *targetServer {
	s := &targetServer{Fake: gosmotest.New(t), current: "app",
		objects:  map[string][]*gosmo.ObjectInfo{"app": {tableObject(1, "dbo", "t")}},
		triggers: map[int][]*gosmo.Trigger{}, refs: map[int][]*gosmo.ForeignKey{}, synonyms: map[int]string{}}
	on := func(fn func(gosmotest.Query) [][]any, subs ...string) {
		s.OnFunc(func(q gosmotest.Query) bool { return has(q, subs...) },
			func(q gosmotest.Query) gosmotest.Result {
				return gosmotest.Result{Sets: []gosmotest.Set{{Rows: fn(q)}}}
			})
	}
	on(func(gosmotest.Query) [][]any { return [][]any{{s.current}} }, "SELECT DB_NAME()")
	on(func(q gosmotest.Query) [][]any {
		for name := range s.objects {
			if strings.EqualFold(name, str(q.Arg(0))) {
				return [][]any{clitest.DatabaseRow(name, 5, objectsAt)}
			}
		}
		return nil
	}, "FROM sys.databases", "WHERE name = @p1")
	on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, o := range s.objects[q.Database] {
			if has(q, "@p2") && o.Schema == q.Arg(0) && o.Name == q.Arg(1) || !has(q, "@p2") && o.Name == q.Arg(0) {
				rows = append(rows, clitest.ObjectInfoRow(o))
			}
		}
		return rows
	}, "OBJECT_SCHEMA_NAME(o.parent_object_id)", "FROM   sys.all_objects o")
	on(func(q gosmotest.Query) [][]any {
		for _, o := range s.objects[q.Database] {
			if o.Type == "U" && o.Schema == q.Arg(0) && o.Name == q.Arg(1) {
				return [][]any{clitest.TableRow(&gosmo.Table{ObjectID: o.ObjectID, Schema: o.Schema, Name: o.Name}, objectsAt)}
			}
		}
		return nil
	}, "SELECT t.object_id, SCHEMA_NAME(t.schema_id), t.name", "= @p2")
	on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, tr := range s.triggers[int(q.Arg(0).(int64))] {
			rows = append(rows, []any{tr.Name, tr.TableName, tr.Schema, !tr.IsEnabled, jsonValues(tr.Events)})
		}
		return rows
	}, "FROM   sys.triggers tr", "tr.parent_id = @p1")
	on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, fk := range s.refs[int(q.Arg(0).(int64))] {
			rows = append(rows, []any{fk.Name, fk.IsDisabled, fk.IsNotTrusted, fk.IsNotForReplication,
				fk.DeleteAction, fk.UpdateAction, fk.ReferencedSchema, fk.ReferencedTable,
				jsonValues(fk.Columns), jsonValues(fk.ReferencedColumns), fk.Schema, fk.TableName})
		}
		return rows
	}, "FROM   sys.foreign_keys fk", "fk.referenced_object_id = @p1")
	on(func(q gosmotest.Query) [][]any {
		for _, o := range s.objects[q.Database] {
			if o.Type == "SN" && o.Schema == q.Arg(0) && o.Name == q.Arg(1) {
				return [][]any{{o.Name, o.Schema, o.ObjectID, s.synonyms[o.ObjectID]}}
			}
		}
		return nil
	}, "FROM   sys.synonyms", "= @p2")
	return s
}

// jsonValues is vs as gosmo's jsonList column: [{"v":…},…].
func jsonValues(vs []string) string {
	type v struct {
		V string `json:"v"`
	}
	out := []v{}
	for _, s := range vs {
		out = append(out, v{s})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// queryDepsOn is queryDeps dialling ts.
func queryDepsOn(t *testing.T, sess *fakeSession, ts *targetServer) (cmd.Deps, *int) {
	dialled := new(int)
	d := profileDeps(t, func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
		*dialled++
		srv, err := ts.Connect(ctx)
		if err != nil {
			return nil, err
		}
		return &db.ServerConn{Server: srv}, nil
	})
	d.OpenSession = func(context.Context, *db.ServerConn, string) (cmd.Session, error) { return sess, nil }
	return d, dialled
}

// A write's target with a trigger the verb fires, or referenced by a foreign
// key whose action it fires, is refused after connecting and before the
// script is sent; a temporary table, a table variable, a trigger for another
// verb, NO ACTION and a disabled key or trigger are not.
func TestMaxRowsAffectedChecksTheTargets(t *testing.T) {
	newServer := func(t *testing.T) *targetServer {
		ts := newTargetServer(t)
		ts.objects["app"] = append(ts.objects["app"],
			tableObject(2, "dbo", "parent"), tableObject(3, "dbo", "audited"), tableObject(4, "Sales", "t"),
			&gosmo.ObjectInfo{ObjectID: 5, Schema: "dbo", Name: "v", Type: "V", TypeDesc: "VIEW"},
			&gosmo.ObjectInfo{ObjectID: 6, Schema: "dbo", Name: "syn", Type: "SN", TypeDesc: "SYNONYM"},
			&gosmo.ObjectInfo{ObjectID: 7, Schema: "dbo", Name: "remote", Type: "SN", TypeDesc: "SYNONYM"},
			&gosmo.ObjectInfo{ObjectID: 8, Schema: "dbo", Name: "p", Type: "P", TypeDesc: "SQL_STORED_PROCEDURE"})
		ts.objects["other"] = []*gosmo.ObjectInfo{tableObject(20, "dbo", "audited")}
		ts.refs[2] = []*gosmo.ForeignKey{
			{Name: "fk_child", Schema: "dbo", TableName: "child", ReferencedSchema: "dbo", ReferencedTable: "parent",
				DeleteAction: "CASCADE", UpdateAction: "NO_ACTION"},
			{Name: "fk_off", Schema: "dbo", TableName: "other", DeleteAction: "SET_NULL", UpdateAction: "SET_NULL", IsDisabled: true},
		}
		ts.triggers[3] = []*gosmo.Trigger{
			{Name: "tr_upd", TableName: "audited", Schema: "dbo", IsEnabled: true, Events: []string{"UPDATE"}},
			{Name: "tr_off", TableName: "audited", Schema: "dbo", IsEnabled: false, Events: []string{"INSERT"}},
		}
		ts.triggers[20] = []*gosmo.Trigger{{Name: "tr_all", TableName: "audited", Schema: "dbo", IsEnabled: true,
			Events: []string{"INSERT", "UPDATE", "DELETE"}}}
		ts.synonyms[6] = "[app].[dbo].[audited]"
		ts.synonyms[7] = "[srv].[db].[dbo].[t]"
		return ts
	}
	for _, tc := range []struct {
		name, script string
		statements   string // "" runs
	}{
		{"a clear table", "UPDATE dbo.t SET a = 1", ""},
		{"local tables", "INSERT #t VALUES (1)\nDELETE @t", ""},
		{"a cascade", "DELETE dbo.parent WHERE id = 1",
			"DELETE dbo.parent (line 1): foreign key [fk_child] on [dbo].[child] is ON DELETE CASCADE"},
		{"NO ACTION and a disabled key", "UPDATE dbo.parent SET a = 1", ""},
		{"a trigger the verb fires", "INSERT dbo.t VALUES (1)\nUPDATE audited SET a = 1",
			"UPDATE audited (line 2): [dbo].[audited] has trigger [dbo].[tr_upd]"},
		{"a trigger for another verb", "INSERT dbo.audited VALUES (1)\nDELETE dbo.audited", ""},
		{"MERGE fires every action", "MERGE dbo.parent AS p USING dbo.t AS s ON p.id = s.id WHEN MATCHED THEN DELETE;",
			"MERGE dbo.parent (line 1): foreign key [fk_child] on [dbo].[child] is ON DELETE CASCADE"},
		{"an alias", "DELETE p FROM dbo.parent p JOIN dbo.t ON t.id = p.id",
			"DELETE dbo.parent (line 1): foreign key [fk_child] on [dbo].[child] is ON DELETE CASCADE"},
		{"nested behind IF", "IF 1 = 1\n  DELETE dbo.parent",
			"DELETE dbo.parent (line 2): foreign key [fk_child] on [dbo].[child] is ON DELETE CASCADE"},
		{"a database a USE names", "USE other\nDELETE dbo.audited",
			"DELETE dbo.audited (line 2): [dbo].[audited] has trigger [dbo].[tr_all]"},
		{"a three-part name", "INSERT other.dbo.audited VALUES (1)",
			"INSERT other.dbo.audited (line 1): [dbo].[audited] has trigger [dbo].[tr_all]"},
		{"a synonym", "UPDATE dbo.syn SET a = 1", "UPDATE dbo.syn (line 1): [dbo].[audited] has trigger [dbo].[tr_upd]"},
		{"a synonym for a linked server", "UPDATE dbo.remote SET a = 1",
			"UPDATE dbo.remote (line 1): a synonym for a linked server's table, which the check cannot read"},
		{"a view", "UPDATE dbo.v SET a = 1", "UPDATE dbo.v (line 1): [dbo].[v] is a view, whose base tables the check cannot see"},
		{"not a table", "DELETE dbo.p", "DELETE dbo.p (line 1): [dbo].[p] is a sql stored procedure, not a table"},
		{"not found", "DELETE dbo.nope", "DELETE dbo.nope (line 1): no table by that name in app that the login can see"},
		{"a one-part name in every schema", "DELETE t", ""},
		{"a USE of a database the server lacks", "USE gone\nDELETE dbo.t", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := &fakeSession{t: t, run: rowsThenUpdate(1)}
			d, dialled := queryDepsOn(t, sess, newServer(t))
			r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", tc.script, "--allow", "write",
				"--max-rows-affected", "10", "-o", "json")
			env := envelope(t, r.stdout)
			if tc.statements == "" {
				if r.exit != 0 {
					t.Fatalf("exit %d, want 0\n%s", r.exit, r.stdout)
				}
				return
			}
			if r.exit != 7 || *dialled != 1 {
				t.Fatalf("exit %d (dialled %d), want 7 after connecting\n%s", r.exit, *dialled, r.stdout)
			}
			if got := env["data"].(map[string]any)["statements"]; got != tc.statements {
				t.Errorf("statements\n got %q\nwant %q", got, tc.statements)
			}
			if len(sess.executed) != 0 {
				t.Errorf("sent %q, want nothing", sess.executed)
			}
			errs := env["errors"].([]any)
			if e := errs[0].(map[string]any); e["code"] != "BLOCKED" || !strings.Contains(e["hint"].(string), "--transaction rollback") {
				t.Errorf("error %v", e)
			}
		})
	}
}

// The check runs only under --max-rows-affected, and reads nothing for a
// script without writes.
func TestMaxRowsAffectedTargetCheckScope(t *testing.T) {
	ts := newTargetServer(t)
	ts.triggers[1] = []*gosmo.Trigger{{Name: "tr", TableName: "t", Schema: "dbo", IsEnabled: true, Events: []string{"UPDATE"}}}
	d, _ := queryDepsOn(t, &fakeSession{t: t, run: rowsThenUpdate(1)}, ts)
	if r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "UPDATE dbo.t SET a = 1", "--allow", "write",
		"--transaction", "commit"); r.exit != 0 {
		t.Errorf("--transaction commit: exit %d, want 0\n%s", r.exit, r.stdout)
	}
	before := len(ts.Queries())
	if r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT 1", "--max-rows-affected", "10"); r.exit != 0 {
		t.Errorf("SELECT: exit %d\n%s", r.exit, r.stdout)
	}
	for _, q := range ts.Queries()[before:] {
		if slices.ContainsFunc([]string{"sys.all_objects", "sys.triggers", "sys.foreign_keys"}, func(s string) bool { return strings.Contains(q.Text, s) }) {
			t.Errorf("a script without writes read %q", q.Text)
		}
	}
}
