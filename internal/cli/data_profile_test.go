package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
)

// dataProfileFake is objectFake with Sales.Customer's columns replaced, and the
// database's alias types counted as read.
type dataProfileFake struct {
	*objectFake
	cols      []*gosmo.Column
	typesRead int
}

func (c *dataProfileFake) TableColumns(context.Context, *gosmo.Table) ([]*gosmo.Column, error) {
	return c.cols, nil
}

func (c *dataProfileFake) UserDefinedDataTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedDataType, error) {
	c.typesRead++
	return []*gosmo.UserDefinedDataType{{Schema: "dbo", Name: "Phone", BaseType: "varchar"}}, nil
}

// profileColumns is one column per aggregate shape, an alias type, and a
// graph column the profile leaves out.
func profileColumns() []*gosmo.Column {
	return []*gosmo.Column{
		{Name: "id", DataType: "int", TypeSchema: "sys"},
		{Name: "flag", DataType: "bit", TypeSchema: "sys", IsNullable: true},
		{Name: "name", DataType: "nvarchar", MaxLength: 100, TypeSchema: "sys", IsNullable: true},
		{Name: "doc", DataType: "xml", MaxLength: -1, TypeSchema: "sys", IsNullable: true},
		{Name: "phone", DataType: "Phone", TypeSchema: "dbo", IsUserDefinedType: true, IsNullable: true},
		{Name: "$node_id_X", DataType: "nvarchar", MaxLength: 1000, GraphType: gosmo.GraphColumnIDComputed},
	}
}

// dataProfileSession answers Execute with answer(script).
type dataProfileSession struct {
	*fakeSession
	answer func(script string) *query.Result
}

func (p *dataProfileSession) Execute(_ context.Context, script string, _ ...query.Option) *query.Result {
	p.executed = append(p.executed, script)
	return p.answer(script)
}

// oneRowSet is a one-row set of the given columns and cells; a nil cell is NULL.
func oneRowSet(cols []string, cells []*string) *query.Result {
	rs := query.ResultSet{Columns: cols, Rows: [][]string{make([]string, len(cells))}}
	for i, c := range cells {
		if c == nil {
			rs.Rows[0][i] = "NULL"
			rs.MarkNull(0, i)
		} else {
			rs.Rows[0][i] = *c
		}
	}
	return &query.Result{Sets: []query.ResultSet{rs}, Batches: 1, Messages: []query.Message{
		{Text: "Warning: Null value is eliminated by an aggregate or other SET operation."},
		{Text: "(1 row affected)", RowsAffected: &query.RowCount{N: 1, AfterSet: true}},
	}}
}

func cell(v string) *string { return &v }

func dataProfileDeps(t *testing.T, cat cmd.Catalog, answer func(string) *query.Result) (cmd.Deps, *dataProfileSession, *int) {
	sess := &dataProfileSession{fakeSession: &fakeSession{t: t}, answer: answer}
	d, dialled, _ := dataDeps(t, cat, sess.fakeSession)
	d.OpenSession = func(context.Context, *db.ServerConn, string) (cmd.Session, error) { return sess, nil }
	return d, sess, dialled
}

const wantProfileStatement = "SELECT COUNT_BIG(*) AS [rows],\n" +
	"    COUNT_BIG(CASE WHEN [id] IS NULL THEN 1 END) AS [nulls_0],\n" +
	"    COUNT_BIG(DISTINCT [id]) AS [distinct_0],\n" +
	"    MIN([id]) AS [min_0],\n" +
	"    MAX([id]) AS [max_0],\n" +
	"    COUNT_BIG(CASE WHEN [flag] IS NULL THEN 1 END) AS [nulls_1],\n" +
	"    COUNT_BIG(DISTINCT [flag]) AS [distinct_1],\n" +
	"    MIN(CAST([flag] AS tinyint)) AS [min_1],\n" +
	"    MAX(CAST([flag] AS tinyint)) AS [max_1],\n" +
	"    COUNT_BIG(CASE WHEN [name] IS NULL THEN 1 END) AS [nulls_2],\n" +
	"    COUNT_BIG(DISTINCT [name]) AS [distinct_2],\n" +
	"    MIN([name]) AS [min_2],\n" +
	"    MAX([name]) AS [max_2],\n" +
	"    MIN(LEN([name])) AS [min_len_2],\n" +
	"    MAX(LEN([name])) AS [max_len_2],\n" +
	"    COUNT_BIG(CASE WHEN [doc] IS NULL THEN 1 END) AS [nulls_3],\n" +
	"    COUNT_BIG(CASE WHEN [phone] IS NULL THEN 1 END) AS [nulls_4],\n" +
	"    COUNT_BIG(DISTINCT [phone]) AS [distinct_4],\n" +
	"    MIN([phone]) AS [min_4],\n" +
	"    MAX([phone]) AS [max_4],\n" +
	"    MIN(LEN([phone])) AS [min_len_4],\n" +
	"    MAX(LEN([phone])) AS [max_len_4]\n" +
	"FROM [Sales].[Customer]\nWHERE (id > 0 -- positive\n)"

// profileAnswer answers the aggregate statement with fixed values by alias,
// and each top-values statement with two rows, one of them NULL.
func profileAnswer(script string) *query.Result {
	if !strings.Contains(script, "GROUP BY") {
		vals := map[string]*string{
			"rows": cell("4"), "nulls_0": cell("0"), "distinct_0": cell("4"), "min_0": cell("1"), "max_0": cell("9"),
			"nulls_1": cell("1"), "distinct_1": cell("2"), "min_1": cell("0"), "max_1": cell("1"),
			"nulls_2": cell("4"), "distinct_2": cell("0"), "min_2": nil, "max_2": nil, "min_len_2": nil, "max_len_2": nil,
			"nulls_3": cell("2"),
			"nulls_4": cell("0"), "distinct_4": cell("1"), "min_4": cell("555"), "max_4": cell("555"), "min_len_4": cell("3"), "max_len_4": cell("3"),
		}
		var cols []string
		var cells []*string
		for _, line := range strings.Split(script, "\n") {
			if i := strings.LastIndex(line, " AS ["); i >= 0 {
				alias := strings.TrimSuffix(strings.TrimSuffix(line[i+5:], ","), "]")
				cols, cells = append(cols, alias), append(cells, vals[alias])
			}
		}
		return oneRowSet(cols, cells)
	}
	col := script[strings.Index(script, "(2) ")+4 : strings.Index(script, " AS [value]")]
	rs := query.ResultSet{Columns: []string{"value", "count"}, Rows: [][]string{{"v" + col, "3"}, {"NULL", "1"}}}
	rs.MarkNull(1, 0)
	return &query.Result{Sets: []query.ResultSet{rs}, Batches: 1}
}

func TestDataProfile(t *testing.T) {
	cat := &dataProfileFake{objectFake: newObjectFake(), cols: profileColumns()}
	d, sess, _ := dataProfileDeps(t, cat, profileAnswer)
	r := runData(t, d, "profile", "Sales.Customer", "--where", "id > 0 -- positive", "--top-values", "2")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	// The aggregate SELECT, then one GROUP BY per column with distinct (doc,
	// xml, has none; the graph column is left out).
	wantTop := func(c string) string {
		return "SELECT TOP (2) " + c + " AS [value], COUNT_BIG(*) AS [count]\nFROM [Sales].[Customer]\nWHERE (id > 0 -- positive\n)" +
			"\nGROUP BY " + c + "\nORDER BY COUNT_BIG(*) DESC, " + c
	}
	want := []string{wantProfileStatement, wantTop("[id]"), wantTop("[flag]"), wantTop("[name]"), wantTop("[phone]")}
	if !slices.Equal(sess.executed, want) {
		t.Errorf("sent:\n%s\nwant:\n%s", strings.Join(sess.executed, "\n----\n"), strings.Join(want, "\n----\n"))
	}
	if cat.typesRead != 1 {
		t.Errorf("alias types read %d times", cat.typesRead)
	}
	env := envelope(t, r.stdout)
	if dataField(t, env, "tsql") != wantProfileStatement || dataField(t, env, "type") != "table" {
		t.Errorf("record %v", env["data"])
	}
	sets := env["resultSets"].([]any)
	if len(sets) != 2 {
		t.Fatalf("sets %v", sets)
	}
	cols := sets[0].(map[string]any)
	if cols["name"] != "columns" {
		t.Errorf("set %v", cols["name"])
	}
	got := fmt.Sprint(cols["rows"])
	const wantRows = "[[id int 4 0 0 4 1 9 <nil> <nil>] [flag bit 4 1 25 2 0 1 <nil> <nil>] " +
		"[name nvarchar(50) 4 4 100 0 <nil> <nil> <nil> <nil>] [doc xml 4 2 50 <nil> <nil> <nil> <nil> <nil>] " +
		"[phone [dbo].[Phone] 4 0 0 1 555 555 3 3]]"
	if got != wantRows {
		t.Errorf("columns rows\n got %s\nwant %s", got, wantRows)
	}
	top := sets[1].(map[string]any)
	const wantTopRows = "[[id v[id] 3] [id <nil> 1] [flag v[flag] 3] [flag <nil> 1] [name v[name] 3] [name <nil> 1] [phone v[phone] 3] [phone <nil> 1]]"
	if top["name"] != "top_values" || fmt.Sprint(top["rows"]) != wantTopRows {
		t.Errorf("top_values %v %v", top["name"], top["rows"])
	}
	// The aggregate's NULL warning and the sets' own counts are dropped.
	if msgs, _ := env["messages"].([]any); len(msgs) != 0 {
		t.Errorf("messages %v", msgs)
	}
}

func TestDataProfileColumnsAndNoAliasRead(t *testing.T) {
	cat := &dataProfileFake{objectFake: newObjectFake(), cols: profileColumns()}
	d, sess, _ := dataProfileDeps(t, cat, profileAnswer)
	r := runData(t, d, "profile", "Sales.Customer", "--columns", "NAME,id")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	// In the order given; no alias type among them, so none is read; no
	// --top-values, so one statement.
	if len(sess.executed) != 1 || !strings.Contains(sess.executed[0], "[nulls_0],\n    COUNT_BIG(DISTINCT [name])") ||
		strings.Contains(sess.executed[0], "flag") || cat.typesRead != 0 {
		t.Errorf("sent %q, types read %d", sess.executed, cat.typesRead)
	}
	if rows := envelope(t, r.stdout)["resultSets"].([]any); len(rows) != 1 {
		t.Errorf("sets %v", rows)
	}
	// A graph column cannot be named.
	r = runData(t, d, "profile", "Sales.Customer", "--columns", "[$node_id_X]")
	if r.exit != 8 {
		t.Errorf("graph column: exit %d\n%s", r.exit, r.stdout)
	}
}

// More columns than one statement takes: several statements, each column
// read from its own statement's answer.
func TestDataProfileChunks(t *testing.T) {
	var cols []*gosmo.Column
	for i := range 230 {
		cols = append(cols, &gosmo.Column{Name: fmt.Sprintf("c%d", i+1), DataType: "xml"})
	}
	cat := &dataProfileFake{objectFake: newObjectFake(), cols: cols}
	n := 0
	d, sess, _ := dataProfileDeps(t, cat, func(script string) *query.Result {
		n++
		var names []string
		var cells []*string
		names, cells = append(names, "rows"), append(cells, cell("10"))
		for i := range strings.Count(script, "IS NULL") {
			names, cells = append(names, fmt.Sprintf("nulls_%d", i)), append(cells, cell(fmt.Sprintf("%d", n)))
		}
		return oneRowSet(names, cells)
	})
	r := runData(t, d, "profile", "Sales.Customer", "--max-rows", "0")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if len(sess.executed) != 3 {
		t.Fatalf("%d statements", len(sess.executed))
	}
	env := envelope(t, r.stdout)
	if tsql := dataField(t, env, "tsql").(string); strings.Count(tsql, "\nGO\n") != 2 {
		t.Errorf("tsql has %d GO", strings.Count(tsql, "\nGO\n"))
	}
	rows := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)
	if len(rows) != 230 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, c := range []struct {
		i     int
		nulls float64
	}{{0, 1}, {99, 1}, {100, 2}, {199, 2}, {200, 3}, {229, 3}} {
		row := rows[c.i].([]any)
		if row[0] != fmt.Sprintf("c%d", c.i+1) || row[3] != c.nulls {
			t.Errorf("row %d: %v", c.i, row)
		}
	}
}

// A failed aggregate: the server's error, no sets, nothing more sent.
func TestDataProfileDenied(t *testing.T) {
	se := &gosmo.SQLError{Number: 230, Class: 14, State: 1, Message: "The SELECT permission was denied on the column 'doc'"}
	cat := &dataProfileFake{objectFake: newObjectFake(), cols: profileColumns()}
	d, sess, _ := dataProfileDeps(t, cat, func(string) *query.Result {
		return &query.Result{Batches: 1, FailedBatches: 1, Messages: []query.Message{{Text: se.Header(), IsError: true, SQL: se}}}
	})
	r := runData(t, d, "profile", "Sales.Customer", "--top-values", "3")
	if r.exit != 9 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	env := envelope(t, r.stdout)
	if sets, _ := env["resultSets"].([]any); len(sets) != 0 || len(sess.executed) != 1 {
		t.Errorf("sets %v, sent %d", sets, len(sess.executed))
	}
	if !strings.Contains(firstError(t, env)["hint"].(string), "needs SELECT") {
		t.Errorf("hint %v", firstError(t, env))
	}
}

func TestDataSampleComposesTheSelect(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"Sales.Customer"}, "SELECT TOP (100) *\nFROM [Sales].[Customer]\nORDER BY NEWID()\n"},
		{[]string{"vCustomer", "--rows", "5", "--columns", "customername", "--where", "id > 1 -- x"},
			"SELECT TOP (5) [CustomerName]\nFROM [Sales].[vCustomer]\nWHERE (id > 1 -- x\n)\nORDER BY NEWID()\n"},
	} {
		sess := &fakeSession{t: t, run: oneRow}
		d, _, _ := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, append([]string{"sample"}, tc.argv...)...)
		if r.exit != 0 {
			t.Errorf("%v: exit %d\n%s", tc.argv, r.exit, r.stdout)
			continue
		}
		if len(sess.executed) != 1 || sess.executed[0] != tc.want || dataField(t, envelope(t, r.stdout), "tsql") != tc.want {
			t.Errorf("%v: sent %q, want %q", tc.argv, sess.executed, tc.want)
		}
	}
}

func TestDataSampleAndProfileRefusals(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		exit int
		want string
	}{
		{[]string{"sample", "Sales.Customer", "--where", "1=1; DROP TABLE t"}, 7, "';'"},
		{[]string{"profile", "Sales.Customer", "--where", "1=1) SELECT (1"}, 7, "2 statements"},
		{[]string{"profile", "Sales.Customer", "--where", "1=1\nGO\nSELECT (1"}, 7, "GO line"},
		{[]string{"sample", "Sales.Customer", "--rows", "-1"}, 2, "negative"},
		{[]string{"profile", "Sales.Customer", "--top-values", "-1"}, 2, "negative"},
		{[]string{"profile", "Sales.Customer", "--columns", "id,"}, 2, "ends in a comma"},
		{[]string{"sample", "Sales.Customer", "--order-by", "id"}, 2, "--order-by"},
	} {
		sess := &fakeSession{t: t}
		d, dialled, _ := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, tc.argv...)
		if r.exit != tc.exit || *dialled != 0 || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%q: exit %d, dialled %d, want %d and %q\n%s", tc.argv, r.exit, *dialled, tc.exit, tc.want, r.stdout)
		}
	}
}
