package cli

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// importFake is objectFake whose tables have importFakeColumns, with every
// collation in code page 1252.
type importFake struct {
	*objectFake
	cols      []*gosmo.Column
	codePages []string
}

func newImportFake() *importFake {
	return &importFake{objectFake: newObjectFake(), cols: importFakeColumns()}
}

func importFakeColumns() []*gosmo.Column {
	return []*gosmo.Column{
		{Name: "id", DataType: "int", IsIdentity: true, TypeSchema: "sys"},
		{Name: "Name", DataType: "nvarchar", MaxLength: 20, IsNullable: true, TypeSchema: "sys"},
		{Name: "Code", DataType: "varchar", MaxLength: 5, IsNullable: true, Collation: "Latin1_General_CI_AS", TypeSchema: "sys"},
		{Name: "pct", DataType: "int", IsComputed: true, ComputedText: "([id]*(2))", TypeSchema: "sys"},
		{Name: "rv", DataType: "timestamp", TypeSchema: "sys"},
		{Name: "Created", DataType: "datetime2", Scale: 3, DefaultValue: &gosmo.ColumnDefault{Definition: "(sysdatetime())"}, TypeSchema: "sys"},
		{Name: "v", DataType: "sql_variant", IsNullable: true, TypeSchema: "sys"},
	}
}

func (c *importFake) TableColumns(context.Context, *gosmo.Table) ([]*gosmo.Column, error) {
	return c.cols, nil
}

func (c *importFake) CollationCodePage(_ context.Context, collation string) (int, error) {
	c.codePages = append(c.codePages, collation)
	return 1252, nil
}

// loadWriter records data import's writes: the TRUNCATE (and whether it was
// scripted), and the bulk load with every row it consumed.
type loadWriter struct {
	cmd.Writer
	truncated []bool // one per TruncateTable call: scripted?
	loads     []gosmo.BulkCopy
	rows      [][]any
	err       error // returned by BulkInsert after consuming the rows
	truncErr  error
}

func (w *loadWriter) TruncateTable(ctx context.Context, _ *gosmo.Table) error {
	w.truncated = append(w.truncated, gosmo.Scripting(ctx))
	return w.truncErr
}

func (w *loadWriter) BulkInsert(_ context.Context, _ *gosmo.Database, bc gosmo.BulkCopy, rows iter.Seq2[[]any, error]) (int64, error) {
	w.loads = append(w.loads, bc)
	for r, err := range rows {
		if err != nil {
			return 0, fmt.Errorf("gosmo: bulk insert into x: reading row %d: %w", len(w.rows), err)
		}
		w.rows = append(w.rows, r)
	}
	if w.err != nil {
		return 0, w.err
	}
	return int64(len(w.rows)), nil
}

// importDeps is dataDeps over cat with w as the writer, the dev profile's
// ceiling raised to ddl so --truncate-first can pass the gate.
func importDeps(t *testing.T, cat cmd.Catalog, w *loadWriter) (cmd.Deps, *int) {
	d, dialled, _ := dataDeps(t, cat, &fakeSession{t: t})
	cfg := profileConfig(t)
	dev, err := cfg.ProfileByName("dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetProfile(dev.GeneratedName(), "dev", config.AllowDDL); err != nil {
		t.Fatal(err)
	}
	d.LoadConfig = func() (*config.Config, error) { return cfg, nil }
	d.Writer = func(*db.ServerConn) cmd.Writer { return w }
	return d, dialled
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDataImportCSV(t *testing.T) {
	// The NULL convention: an empty field NULL, "" the empty string, a bare
	// NULL the string; a byte order mark before the header; a file column
	// named in another case; computed and rowversion columns skipped.
	path := writeFile(t, "c.csv", "\uFEFFid,name,Code,pct,rv\r\n1,\"\",NULL,2,0x01\r\n2,,abc,4,0x02\r\n")
	cat, w := newImportFake(), &loadWriter{}
	d, _ := importDeps(t, cat, w)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if len(w.loads) != 1 || len(w.truncated) != 0 {
		t.Fatalf("loads %v, truncates %v", w.loads, w.truncated)
	}
	bc := w.loads[0]
	if bc.Schema != "Sales" || bc.Table != "Customer" || !reflect.DeepEqual(bc.Columns, []string{"id", "Name", "Code"}) {
		t.Errorf("load %+v", bc)
	}
	if o := bc.Options; !o.KeepNulls || !o.CheckConstraints || o.FireTriggers || o.TableLock {
		t.Errorf("options %+v", o)
	}
	want := [][]any{{int64(1), "", []byte("NULL")}, {int64(2), nil, []byte("abc")}}
	if !reflect.DeepEqual(w.rows, want) {
		t.Errorf("rows %#v, want %#v", w.rows, want)
	}
	// Code's collation and the database's, once each (both are 1252 here).
	if !reflect.DeepEqual(cat.codePages, []string{"Latin1_General_CI_AS"}) {
		t.Errorf("code pages read for %q", cat.codePages)
	}
	env := envelope(t, r.stdout)
	for k, v := range map[string]any{"object": "[Sales].[Customer]", "type": "table", "inFile": path, "format": "csv",
		"truncateFirst": "false", "rows": "2", "dryRun": "false"} {
		if got := dataField(t, env, k); got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
	if w := env["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "[pct] (computed), [rv] (rowversion)") {
		t.Errorf("warnings %v", w)
	}
	rows := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)
	wantRows := [][]any{
		{"id", "id", "int", "load"}, {"name", "Name", "nvarchar(10)", "load"}, {"Code", "Code", "varchar(5)", "load"},
		{"pct", "pct", "int", "skipped (computed)"}, {"rv", "rv", "timestamp", "skipped (rowversion)"},
		{nil, "Created", "datetime2(3)", "default"}, {nil, "v", "sql_variant", "default"},
	}
	if fmt.Sprint(rows) != fmt.Sprint(wantRows) {
		t.Errorf("columns set %v\nwant %v", rows, wantRows)
	}
}

func TestDataImportJSONLAndOptions(t *testing.T) {
	path := writeFile(t, "c.jsonl", `{"_resultSet":{"index":1,"name":null,"columns":[`+
		`{"name":"id","key":"id","type":"int","nullable":false},{"name":"Name","key":"Name","type":"nvarchar(10)","nullable":true}]}}
{"id":1,"Name":null}
{"id":2,"Name":""}

{"id":3,"Name":"NULL"}
{"_summary":{"status":"ok"}}
`)
	w := &loadWriter{}
	d, _ := importDeps(t, newImportFake(), w)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write",
		"--no-check-constraints", "--fire-triggers", "--tablock")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	want := [][]any{{int64(1), nil}, {int64(2), ""}, {int64(3), "NULL"}}
	if !reflect.DeepEqual(w.rows, want) {
		t.Errorf("rows %#v", w.rows)
	}
	if o := w.loads[0].Options; !o.KeepNulls || o.CheckConstraints || !o.FireTriggers || !o.TableLock {
		t.Errorf("options %+v", o)
	}
	if strings.Contains(r.stdout, "warnings\": [\"") {
		t.Errorf("a warning with nothing skipped:\n%s", r.stdout)
	}
}

func TestDataImportDryRunLoadsNothing(t *testing.T) {
	path := writeFile(t, "c.tsv", "id\tName\n1\ta\n2\tb\n3\t\n")
	w := &loadWriter{}
	d, _ := importDeps(t, newImportFake(), w)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "ddl", "--dry-run",
		"--truncate-first", "--confirm", "Sales.Customer")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if len(w.loads) != 0 || !reflect.DeepEqual(w.truncated, []bool{true}) {
		t.Errorf("a dry run wrote: loads %v, truncates (scripted?) %v", w.loads, w.truncated)
	}
	env := envelope(t, r.stdout)
	if dataField(t, env, "rows") != "3" || dataField(t, env, "dryRun") != "true" || dataField(t, env, "truncateFirst") != "true" {
		t.Errorf("record %v", env["data"])
	}
	if _, ok := env["data"].(map[string]any)["tsql"]; !ok {
		t.Errorf("no tsql on a truncating dry run: %v", env["data"])
	}

	// A bad cell fails the dry run as it would the load.
	path = writeFile(t, "bad.csv", "id\n1\nx\n")
	r = runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write", "--dry-run")
	if r.exit != 2 || !strings.Contains(r.stdout, `line 3, column [id]: \"x\" is not an integer`) {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
}

func TestDataImportTruncateThenLoad(t *testing.T) {
	path := writeFile(t, "c.csv", "id\n1\n")
	w := &loadWriter{err: mssql.Error{Number: 2627, Class: 14, Message: "Violation of PRIMARY KEY constraint"}}
	d, _ := importDeps(t, newImportFake(), w)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "ddl", "--truncate-first", "--confirm", "Sales.Customer")
	if r.exit != 5 || !reflect.DeepEqual(w.truncated, []bool{false}) || len(w.loads) != 1 {
		t.Fatalf("exit %d, truncates %v, loads %d\n%s", r.exit, w.truncated, len(w.loads), r.stdout)
	}
	if !strings.Contains(firstError(t, envelope(t, r.stdout))["hint"].(string), "it stays empty") {
		t.Errorf("hint does not say the table is empty:\n%s", r.stdout)
	}

	// A refused TRUNCATE (Msg 1088: no ALTER) is exit 9, and nothing loads.
	w = &loadWriter{truncErr: fmt.Errorf("gosmo: truncate: %w", mssql.Error{Number: 1088, Class: 16,
		Message: "Cannot find the object \"Customer\" because it does not exist or you do not have permissions."})}
	d, _ = importDeps(t, newImportFake(), w)
	r = runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "ddl", "--truncate-first", "--confirm", "Sales.Customer")
	if r.exit != 9 || len(w.loads) != 0 {
		t.Errorf("exit %d, loads %d\n%s", r.exit, len(w.loads), r.stdout)
	}
}

func TestDataImportPermissionDenied(t *testing.T) {
	// Each refusal live-captured on 17: no INSERT (and, the file supplying
	// identity values, no ALTER) is Msg 1088 on the flush, which the
	// resolved table makes a permission; unchecked constraints without ALTER
	// is Msg 5304.
	path := writeFile(t, "c.csv", "id\n1\n")
	for _, se := range []mssql.Error{
		{Number: 229, Class: 14, Message: "The INSERT permission was denied on the object 'Customer'"},
		{Number: 1088, Class: 16, Message: `Cannot find the object "Sales.Customer" because it does not exist or you do not have permissions.`},
		{Number: 5304, Class: 16, Message: "Bulk copy failed. User does not have ALTER TABLE permission on table 'Sales.Customer'."},
	} {
		w := &loadWriter{err: fmt.Errorf("gosmo: bulk insert: flush: %w", se)}
		d, _ := importDeps(t, newImportFake(), w)
		r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write")
		if r.exit != 9 || !strings.Contains(r.stdout, "the file has identity column [id]") {
			t.Errorf("Msg %d: exit %d\n%s", se.Number, r.exit, r.stdout)
		}
	}
}

func TestDataImportRefusals(t *testing.T) {
	csv := writeFile(t, "c.csv", "id\n1\n")
	for _, tc := range []struct {
		name   string
		argv   []string
		exit   int
		dialed bool
		want   string
	}{
		{"no --in-file", []string{"Sales.Customer", "--allow", "write"}, 2, false, "needs --in-file"},
		{"no --allow", []string{"Sales.Customer", "--in-file", csv}, 7, false, ""},
		{"truncate is ddl", []string{"Sales.Customer", "--in-file", csv, "--allow", "write",
			"--truncate-first", "--confirm", "Sales.Customer"}, 7, false, ""},
		{"truncate unconfirmed", []string{"Sales.Customer", "--in-file", csv, "--allow", "ddl", "--truncate-first"}, 7, false,
			"Add --confirm Sales.Customer"},
		{"truncate confirm mismatch", []string{"Sales.Customer", "--in-file", csv, "--allow", "ddl", "--truncate-first",
			"--confirm", "Customer"}, 7, false, "is not the table as given"},
		{"confirm alone", []string{"Sales.Customer", "--in-file", csv, "--allow", "write", "--confirm", "x"}, 2, false, ""},
		{".json", []string{"Sales.Customer", "--in-file", "x.json", "--allow", "write"}, 2, false, ".jsonl"},
		{"unknown extension", []string{"Sales.Customer", "--in-file", "x.dat", "--allow", "write"}, 2, false, "--in-format"},
		{"stdin without format", []string{"Sales.Customer", "--in-file", "-", "--allow", "write"}, 2, false, "--in-format"},
		{"stdin and password-stdin", []string{"Sales.Customer", "--in-file", "-", "--in-format", "csv", "--allow", "write",
			"--password-stdin"}, 2, false, "both read stdin"},
		{"missing file", []string{"Sales.Customer", "--in-file", filepath.Join(t.TempDir(), "no.csv"), "--allow", "write"}, 2, false, ""},
		{"empty file", []string{"Sales.Customer", "--in-file", writeFile(t, "e.csv", ""), "--allow", "write"}, 2, false, "header row"},
		{"not gossms jsonl", []string{"Sales.Customer", "--in-file", writeFile(t, "e.jsonl", `{"id":1}`+"\n"), "--allow", "write"}, 2, false,
			"_resultSet"},
		{"unknown column", []string{"Sales.Customer", "--in-file", writeFile(t, "u.csv", "id,nope\n1,2\n"), "--allow", "write"}, 2, true,
			"[nope] is not a column"},
		{"column twice", []string{"Sales.Customer", "--in-file", writeFile(t, "u.csv", "id,ID\n1,2\n"), "--allow", "write"}, 2, true,
			"both name [id]"},
		{"only computed", []string{"Sales.Customer", "--in-file", writeFile(t, "u.csv", "pct\n1\n"), "--allow", "write"}, 2, true,
			"no column of the file"},
		{"sql_variant", []string{"Sales.Customer", "--in-file", writeFile(t, "u.csv", "id,v\n1,2\n"), "--allow", "write"}, 11, true,
			"[v] (sql_variant)"},
		{"a view", []string{"Sales.vCustomer", "--in-file", csv, "--allow", "write"}, 2, true, "is a view"},
		{"short row", []string{"Sales.Customer", "--in-file", writeFile(t, "s.csv", "id,Name\n1,a\n2\n"), "--allow", "write"}, 2, true,
			"line 3: 1 fields, and the header row has 2"},
		{"second set", []string{"Sales.Customer", "--in-file", writeFile(t, "s.csv", "id\n1\n\nx\n2\n"), "--allow", "write"}, 2, true,
			"a second result set"},
		// nvarchar(10)'s max_length is 20 bytes; the limit is 10 characters.
		{"nvarchar too long", []string{"Sales.Customer", "--in-file", writeFile(t, "l.csv", "Name\n0123456789\n0123456789x\n"),
			"--allow", "write"}, 2, true, "line 3, column [Name]: 11 characters is longer than the column's 10"},
		{"bare quote", []string{"Sales.Customer", "--in-file", writeFile(t, "s.csv", "id,Name\n1,a\"b\n"), "--allow", "write"}, 2, true,
			"line 2"},
	} {
		w := &loadWriter{}
		d, dialled := importDeps(t, newImportFake(), w)
		r := runData(t, d, append([]string{"import"}, tc.argv...)...)
		if r.exit != tc.exit || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%s: exit %d, want %d with %q\n%s", tc.name, r.exit, tc.exit, tc.want, r.stdout)
		}
		if (*dialled > 0) != tc.dialed {
			t.Errorf("%s: dialled %d", tc.name, *dialled)
		}
		// A file fault after some rows is the load's to abandon (BulkInsert
		// sends nothing then: TestLiveBulkLoadIsAllOrNothing); before the
		// load, nothing is even offered.
		if !tc.dialed && len(w.loads) != 0 {
			t.Errorf("%s: loaded %v", tc.name, w.loads)
		}
	}
}

func TestDataImportOneColumnCSVWarns(t *testing.T) {
	// RecordWriter writes a one-column NULL as "", so the file cannot say
	// which "" was a NULL: they load as empty strings, and a warning says so.
	path := writeFile(t, "n.csv", "Name\n\"\"\na\n")
	w := &loadWriter{}
	d, _ := importDeps(t, newImportFake(), w)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write")
	if r.exit != 0 || !reflect.DeepEqual(w.rows, [][]any{{""}, {"a"}}) {
		t.Fatalf("exit %d, rows %v\n%s", r.exit, w.rows, r.stdout)
	}
	if !strings.Contains(r.stdout, "Export to .jsonl to keep NULLs") {
		t.Errorf("no one-column warning:\n%s", r.stdout)
	}
}
