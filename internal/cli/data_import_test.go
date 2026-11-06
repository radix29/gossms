package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
)

// importServer is objectServer whose Sales.Customer has importFakeColumns,
// taking data import's writes: the TRUNCATE and the bulk load, each
// recorded. Every collation is in code page 1252.
type importServer struct {
	*objectServer
	truncates []string // each TRUNCATE sent, as sent
	loads     []mssql.BulkOptions
	loadOf    []string // each load's table and columns, "[Sales].[Customer] id,Name"
	rows      [][]any  // every row the loads sent
	newLoad   bool     // the load's columns were read: its first row opens a load
	loadErr   error    // fails every row
	truncErr  error    // fails the TRUNCATE
}

func newImportFake(t *testing.T) *importServer {
	s := &importServer{}
	s.objectServer = newObjectServerWith(t, func(o *objectServer) {
		s.objectServer = o
		s.rules()
	})
	s.tableColumns = importFakeColumns()
	return s
}

// bulkConfig is the INSERTBULK statement go-mssqldb's CopyIn makes.
type bulkConfig struct {
	TableName   string
	ColumnsName []string
	Options     mssql.BulkOptions
}

func (s *importServer) rules() {
	// BulkInsert's read of the target's columns, in the session's database.
	s.on(func(q gosmotest.Query) [][]any {
		s.newLoad = true
		var rows [][]any
		for _, c := range s.tableColumns {
			length := c.MaxLength
			if c.DataType == "nvarchar" || c.DataType == "nchar" {
				length /= 2
			}
			cp := 0
			if c.Collation != "" || strings.HasSuffix(string(c.DataType), "char") {
				cp = 1252
			}
			rows = append(rows, []any{c.Name, string(c.DataType), length, c.Precision, c.Scale, cp, 1252})
		}
		return rows
	}, "FROM sys.columns c", "WHERE c.object_id = OBJECT_ID(@p1)")
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, func(q gosmotest.Query) gosmotest.Result {
		switch {
		case strings.HasPrefix(q.Text, "TRUNCATE TABLE "):
			s.truncates = append(s.truncates, q.Text)
			return gosmotest.Result{Err: s.truncErr}
		case strings.HasPrefix(q.Text, "INSERTBULK "):
			if s.newLoad {
				var bc bulkConfig
				if err := json.Unmarshal([]byte(strings.TrimPrefix(q.Text, "INSERTBULK ")), &bc); err != nil {
					s.t.Errorf("bulk config %q: %v", q.Text, err)
				}
				s.loads = append(s.loads, bc.Options)
				s.loadOf = append(s.loadOf, bc.TableName+" "+strings.Join(bc.ColumnsName, ","))
				s.newLoad = false
			}
			if s.loadErr != nil {
				return gosmotest.Result{Err: s.loadErr}
			}
			s.rows = append(s.rows, q.Args)
			return gosmotest.Result{}
		}
		s.t.Errorf("importServer: a write it does not know: %s", q)
		return gosmotest.Result{}
	})
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

// importDeps is dataDeps over s, the dev profile's ceiling raised to ddl so
// --truncate-first can pass the gate.
func importDeps(t *testing.T, s *importServer) (cmd.Deps, *int) {
	d, dialled, _ := dataDeps(t, s.objectServer, &fakeSession{t: t})
	cfg := profileConfig(t)
	dev, err := cfg.ProfileByName("dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetProfile(dev.GeneratedName(), "dev", config.AllowDDL); err != nil {
		t.Fatal(err)
	}
	d.LoadConfig = func() (*config.Config, error) { return cfg, nil }
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
	cat := newImportFake(t)
	d, _ := importDeps(t, cat)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if len(cat.loads) != 1 || len(cat.truncates) != 0 {
		t.Fatalf("loads %v, truncates %v", cat.loads, cat.truncates)
	}
	if got := cat.loadOf[0]; got != "[Sales].[Customer] id,Name,Code" {
		t.Errorf("load %q", got)
	}
	if o := cat.loads[0]; !o.KeepNulls || !o.CheckConstraints || o.FireTriggers || o.Tablock {
		t.Errorf("options %+v", o)
	}
	want := [][]any{{int64(1), "", []byte("NULL")}, {int64(2), nil, []byte("abc")}}
	if !reflect.DeepEqual(cat.rows, want) {
		t.Errorf("rows %#v, want %#v", cat.rows, want)
	}
	// Code's collation and the database's, once each (both are 1252 here).
	if !reflect.DeepEqual(cat.recorded(&cat.codePages), []string{"Latin1_General_CI_AS"}) {
		t.Errorf("code pages read for %q", cat.recorded(&cat.codePages))
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
	cat := newImportFake(t)
	d, _ := importDeps(t, cat)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write",
		"--no-check-constraints", "--fire-triggers", "--tablock")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	want := [][]any{{int64(1), nil}, {int64(2), ""}, {int64(3), "NULL"}}
	if !reflect.DeepEqual(cat.rows, want) {
		t.Errorf("rows %#v", cat.rows)
	}
	if o := cat.loads[0]; !o.KeepNulls || o.CheckConstraints || !o.FireTriggers || !o.Tablock {
		t.Errorf("options %+v", o)
	}
	if strings.Contains(r.stdout, "warnings\": [\"") {
		t.Errorf("a warning with nothing skipped:\n%s", r.stdout)
	}
}

func TestDataImportDryRunLoadsNothing(t *testing.T) {
	path := writeFile(t, "c.tsv", "id\tName\n1\ta\n2\tb\n3\t\n")
	cat := newImportFake(t)
	d, _ := importDeps(t, cat)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "ddl", "--dry-run",
		"--truncate-first", "--confirm", "Sales.Customer")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if len(cat.loads) != 0 || len(cat.truncates) != 0 {
		t.Errorf("a dry run wrote: loads %v, truncates %q", cat.loads, cat.truncates)
	}
	env := envelope(t, r.stdout)
	if dataField(t, env, "rows") != "3" || dataField(t, env, "dryRun") != "true" || dataField(t, env, "truncateFirst") != "true" {
		t.Errorf("record %v", env["data"])
	}
	if got := dataField(t, env, "tsql"); got != "USE [app];\nGO\nTRUNCATE TABLE [Sales].[Customer]\nGO\n" {
		t.Errorf("tsql on a truncating dry run: %q", got)
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
	cat := newImportFake(t)
	cat.loadErr = mssql.Error{Number: 2627, Class: 14, Message: "Violation of PRIMARY KEY constraint"}
	d, _ := importDeps(t, cat)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "ddl", "--truncate-first", "--confirm", "Sales.Customer")
	if r.exit != 5 || !reflect.DeepEqual(cat.truncates, []string{"TRUNCATE TABLE [Sales].[Customer]"}) || len(cat.loads) != 1 {
		t.Fatalf("exit %d, truncates %q, loads %d\n%s", r.exit, cat.truncates, len(cat.loads), r.stdout)
	}
	if q := cat.Queries(); !slices.ContainsFunc(q, func(q gosmotest.Query) bool {
		return q.Exec && q.Database == "app" && strings.HasPrefix(q.Text, "TRUNCATE")
	}) {
		t.Errorf("the TRUNCATE did not run in app")
	}
	if !strings.Contains(firstError(t, envelope(t, r.stdout))["hint"].(string), "it stays empty") {
		t.Errorf("hint does not say the table is empty:\n%s", r.stdout)
	}

	// A refused TRUNCATE (Msg 1088: no ALTER) is exit 9, and nothing loads.
	cat = newImportFake(t)
	cat.truncErr = mssql.Error{Number: 1088, Class: 16,
		Message: "Cannot find the object \"Customer\" because it does not exist or you do not have permissions."}
	d, _ = importDeps(t, cat)
	r = runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "ddl", "--truncate-first", "--confirm", "Sales.Customer")
	if r.exit != 9 || len(cat.loads) != 0 {
		t.Errorf("exit %d, loads %d\n%s", r.exit, len(cat.loads), r.stdout)
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
		cat := newImportFake(t)
		cat.loadErr = se
		d, _ := importDeps(t, cat)
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
		cat := newImportFake(t)
		d, dialled := importDeps(t, cat)
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
		if !tc.dialed && len(cat.loads) != 0 {
			t.Errorf("%s: loaded %v", tc.name, cat.loads)
		}
	}
}

func TestDataImportOneColumnCSVWarns(t *testing.T) {
	// RecordWriter writes a one-column NULL as "", so the file cannot say
	// which "" was a NULL: they load as empty strings, and a warning says so.
	path := writeFile(t, "n.csv", "Name\n\"\"\na\n")
	cat := newImportFake(t)
	d, _ := importDeps(t, cat)
	r := runData(t, d, "import", "Sales.Customer", "--in-file", path, "--allow", "write")
	if r.exit != 0 || !reflect.DeepEqual(cat.rows, [][]any{{""}, {"a"}}) {
		t.Fatalf("exit %d, rows %v\n%s", r.exit, cat.rows, r.stdout)
	}
	if !strings.Contains(r.stdout, "Export to .jsonl to keep NULLs") {
		t.Errorf("no one-column warning:\n%s", r.stdout)
	}
}
