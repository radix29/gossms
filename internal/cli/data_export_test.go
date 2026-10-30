package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/query"
)

// nullsAndEmpties streams one set whose rows hold what an export must keep
// apart: NULL, the empty string, the string NULL, and text that needs
// quoting.
func nullsAndEmpties(_ context.Context, _ string, sink query.RowSink) *query.Result {
	ts := sink.(query.TypedRowSink)
	ts.BeginTypedSet([]string{"id", "name"}, []string{"int", "nvarchar(20)"}, []bool{false, true})
	ts.Row([]string{"1", "NULL"}, []bool{false, true})
	ts.Row([]string{"2", ""}, []bool{false, false})
	ts.Row([]string{"3", "NULL"}, []bool{false, false})
	ts.Row([]string{"4", "a,\"b\"\nc"}, []bool{false, false})
	ts.EndSet(4)
	return &query.Result{Batches: 1, State: &query.SessionState{}, Messages: []query.Message{
		{Text: "(4 rows affected)", RowsAffected: &query.RowCount{N: 4, AfterSet: true}}}}
}

const nullsAndEmptiesCSV = "id,name\n1,\n2,\"\"\n3,NULL\n4,\"a,\"\"b\"\"\nc\"\n"

func TestDataExportObject(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{nil, "SELECT *\nFROM [Sales].[Customer]"},
		{[]string{"--columns", "customername,id", "--where", "id > 1 -- big", "--order-by", "id"},
			"SELECT [CustomerName], [id]\nFROM [Sales].[Customer]\nWHERE (id > 1 -- big\n)\nORDER BY id\n"},
	} {
		path := filepath.Join(t.TempDir(), "c.csv")
		sess := &fakeSession{t: t, run: nullsAndEmpties}
		d, _, opened := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, append([]string{"export", "Sales.Customer", "--out-file", path}, tc.argv...)...)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d\n%s", tc.argv, r.exit, r.stdout)
		}
		if len(sess.executed) != 1 || sess.executed[0] != tc.want {
			t.Errorf("%v: sent %q, want %q", tc.argv, sess.executed, tc.want)
		}
		env := envelope(t, r.stdout)
		if dataField(t, env, "object") != "[Sales].[Customer]" || dataField(t, env, "tsql") != tc.want || env["outFile"] != path {
			t.Errorf("%v: envelope %v", tc.argv, env)
		}
		set := env["resultSets"].([]any)[0].(map[string]any)
		if set["totalRowCount"] != 4.0 || len(set["rows"].([]any)) != 0 {
			t.Errorf("%v: set %v", tc.argv, set)
		}
		if data, _ := os.ReadFile(path); string(data) != nullsAndEmptiesCSV {
			t.Errorf("%v: file %q, want %q", tc.argv, data, nullsAndEmptiesCSV)
		}
		if len(*opened) != 1 || (*opened)[0] != "app" {
			t.Errorf("%v: sessions %q", tc.argv, *opened)
		}
	}
}

// Every --out-file extension takes the export, in its format.
func TestDataExportFormats(t *testing.T) {
	for ext, marker := range map[string]string{
		".csv":   nullsAndEmptiesCSV,
		".tsv":   "id\tname\n1\t\n2\t\"\"\n3\tNULL\n",
		".jsonl": `{"id":1,"name":null}` + "\n" + `{"id":2,"name":""}`,
		".json":  `"rows": [`,
		".md":    `| 4  | a,"b"<br>c |`,
		".txt":   "=== RESULT 1 (4 rows, truncated: no) ===",
	} {
		path := filepath.Join(t.TempDir(), "x"+ext)
		sess := &fakeSession{t: t, run: nullsAndEmpties}
		d, _, _ := dataDeps(t, newObjectFake(), sess)
		if r := runData(t, d, "export", "--sql", "SELECT id, name FROM t", "--out-file", path); r.exit != 0 {
			t.Errorf("%s: exit %d\n%s", ext, r.exit, r.stdout)
			continue
		}
		if data, _ := os.ReadFile(path); !strings.Contains(string(data), marker) {
			t.Errorf("%s: no %q in\n%s", ext, marker, data)
		}
	}
}

func TestDataExportScript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.csv")
	sess := &fakeSession{t: t, run: nullsAndEmpties}
	d, _, _ := dataDeps(t, newObjectFake(), sess)
	r := runData(t, d, "export", "-Q", "SELECT id, name FROM t", "--out-file", path)
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	env := envelope(t, r.stdout)
	if dataField(t, env, "detectedClass") != "read" || dataField(t, env, "statementCount") != "1" || env["outFile"] != path {
		t.Errorf("envelope %v", env)
	}
	if len(sess.executed) != 1 || sess.executed[0] != "SELECT id, name FROM t" {
		t.Errorf("sent %q", sess.executed)
	}
	if data, _ := os.ReadFile(path); string(data) != nullsAndEmptiesCSV {
		t.Errorf("file %q", data)
	}

	// --param wraps the script in sp_executesql, as query run's.
	path = filepath.Join(t.TempDir(), "p.csv")
	sess = &fakeSession{t: t, run: nullsAndEmpties}
	d, _, _ = dataDeps(t, newObjectFake(), sess)
	if r := runData(t, d, "export", "-Q", "SELECT * FROM t WHERE id = @id", "--param", "id=4:int", "--out-file", path); r.exit != 0 ||
		len(sess.executed) != 1 || !strings.Contains(sess.executed[0], "sp_executesql") {
		t.Errorf("--param: exit %d, sent %q\n%s", r.exit, sess.executed, r.stdout)
	}
}

// A failed export leaves an existing --out-file as it was.
func TestDataExportFailureKeepsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.csv")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failing := func(ctx context.Context, script string, sink query.RowSink) *query.Result {
		qr := nullsAndEmpties(ctx, script, sink)
		qr.Messages = append(qr.Messages, query.Message{IsError: true, Text: "boom"})
		return qr
	}
	sess := &fakeSession{t: t, run: failing}
	d, _, _ := dataDeps(t, newObjectFake(), sess)
	r := runData(t, d, "export", "Sales.Customer", "--out-file", path, "--force")
	if r.exit == 0 || !strings.Contains(r.stdout, "was not written") {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
	if data, _ := os.ReadFile(path); string(data) != "old\n" {
		t.Errorf("file %q", data)
	}
}

// Refusals: nothing dials.
func TestDataExportRefusals(t *testing.T) {
	dir := t.TempDir()
	out := func(name string) string { return filepath.Join(dir, name) }
	for _, tc := range []struct {
		argv []string
		exit int
		want string
	}{
		{[]string{"Sales.Customer"}, 2, "needs --out-file"},
		{[]string{"-Q", "SELECT 1"}, 2, "needs --out-file"},
		{[]string{"--out-file", out("a.csv")}, 2, "no T-SQL given"},
		{[]string{"Sales.Customer", "-Q", "SELECT 1", "--out-file", out("b.csv")}, 2, "both given"},
		{[]string{"Sales.Customer", "--param", "id=1:int", "--out-file", out("b.csv")}, 2, "both given"},
		{[]string{"-Q", "SELECT 1", "--where", "1=1", "--out-file", out("c.csv")}, 2, "only with an object"},
		{[]string{"Sales.Customer", "--out-file", out("d.xlsx")}, 2, "names no format"},
		{[]string{"Sales.Customer", "--where", "1=1); DELETE Sales.Customer WHERE (1=1", "--out-file", out("e.csv")}, 7, "';'"},
		{[]string{"-Q", "DELETE t", "--out-file", out("f.csv")}, 7, "allowed class is read"},
		{[]string{"-Q", "SELECT 1 INTO #t", "--out-file", out("g.csv")}, 7, "allowed class is read"},
		{[]string{"-Q", "SET NOEXEC ON\nSELECT 1", "--out-file", out("h.csv")}, 7, "NOEXEC"},
	} {
		sess := &fakeSession{t: t}
		d, dialled, _ := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, append([]string{"export"}, tc.argv...)...)
		if r.exit != tc.exit || *dialled != 0 || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%q: exit %d (want %d), dialled %d, want %q\n%s", tc.argv, r.exit, tc.exit, *dialled, tc.want, r.stdout)
		}
	}
	// A write's hint points at query run, not at an --allow export lacks.
	d, _, _ := dataDeps(t, newObjectFake(), &fakeSession{t: t})
	r := runData(t, d, "export", "-Q", "UPDATE t SET a = 1", "--out-file", out("i.csv"))
	if hint, _ := firstError(t, envelope(t, r.stdout))["hint"].(string); !strings.Contains(hint, "query run --allow write") {
		t.Errorf("hint %q", hint)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left: %v", entries)
	}
}
