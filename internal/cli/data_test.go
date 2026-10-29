package cli

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
)

// dataDeps is queryDeps with objectFake (Sales.Customer, Sales.vCustomer,
// …) as the catalog: the data commands resolve through the catalog and run
// their SELECT on sess. opened records the database each session opened in.
func dataDeps(t *testing.T, cat cmd.Catalog, sess *fakeSession) (cmd.Deps, *int, *[]string) {
	d, dialled := queryDeps(t, sess)
	d.Catalog = func(*db.ServerConn) cmd.Catalog { return cat }
	opened := new([]string)
	d.OpenSession = func(_ context.Context, _ *db.ServerConn, database string) (cmd.Session, error) {
		*opened = append(*opened, database)
		return sess, nil
	}
	return d, dialled, opened
}

func runData(t *testing.T, d cmd.Deps, argv ...string) runResult {
	t.Helper()
	return runQuery(t, d, append(append([]string{"data"}, argv...), "-p", "dev", "-o", "json")...)
}

// oneRow streams a one-column, one-row set.
func oneRow(_ context.Context, _ string, sink query.RowSink) *query.Result {
	ts := sink.(query.TypedRowSink)
	ts.BeginTypedSet([]string{"id"}, []string{"int"}, []bool{false})
	ts.Row([]string{"7"}, []bool{false})
	ts.EndSet(1)
	return &query.Result{Batches: 1, Messages: []query.Message{
		{Text: "(1 row affected)", RowsAffected: &query.RowCount{N: 1, AfterSet: true}}}}
}

func TestDataTopComposesTheSelect(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		// The TUI's Select Top 1000 Rows, with --rows.
		{[]string{"Sales.Customer"}, "SELECT TOP (100) *\nFROM [Sales].[Customer]"},
		{[]string{"vCustomer", "--rows", "0"}, "SELECT TOP (0) *\nFROM [Sales].[vCustomer]"},
		// --columns is matched to the catalog's spelling, in the order given.
		{[]string{"Sales.Customer", "--columns", "customername, [id]"},
			"SELECT TOP (100) [CustomerName], [id]\nFROM [Sales].[Customer]"},
		// Each fragment ends at a line break: a trailing -- comment cannot
		// swallow the closing parenthesis or what follows.
		{[]string{"Sales.Customer", "--where", "id > 1 -- big ones", "--order-by", "id DESC -- newest"},
			"SELECT TOP (100) *\nFROM [Sales].[Customer]\nWHERE (id > 1 -- big ones\n)\nORDER BY id DESC -- newest\n"},
	} {
		sess := &fakeSession{t: t, run: oneRow}
		d, _, opened := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, append([]string{"top"}, tc.argv...)...)
		if r.exit != 0 {
			t.Errorf("%v: exit %d\n%s", tc.argv, r.exit, r.stdout)
			continue
		}
		if len(sess.executed) != 1 || sess.executed[0] != tc.want {
			t.Errorf("%v: sent %q, want %q", tc.argv, sess.executed, tc.want)
		}
		env := envelope(t, r.stdout)
		if dataField(t, env, "tsql") != tc.want || dataField(t, env, "type") == nil {
			t.Errorf("%v: record %v", tc.argv, env["data"])
		}
		if rows := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any); len(rows) != 1 || rows[0].([]any)[0] != 7.0 {
			t.Errorf("%v: rows %v", tc.argv, rows)
		}
		if len(*opened) != 1 || (*opened)[0] != "app" || !sess.closed {
			t.Errorf("%v: sessions %q, closed %v", tc.argv, *opened, sess.closed)
		}
	}
}

func TestDataTopRecord(t *testing.T) {
	sess := &fakeSession{t: t, run: oneRow}
	d, _, _ := dataDeps(t, newObjectFake(), sess)
	env := envelope(t, runData(t, d, "top", "vCustomer").stdout)
	if dataField(t, env, "object") != "[Sales].[vCustomer]" || dataField(t, env, "type") != "view" {
		t.Errorf("record %v", env["data"])
	}
}

// A fragment that would make the SELECT more than one read statement is
// BLOCKED before anything connects.
func TestDataFragmentsBlocked(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		why  string // the message's reason
	}{
		{[]string{"top", "Sales.Customer", "--where", "1=1); DELETE Sales.Customer WHERE (1=1"}, "';'"},
		{[]string{"top", "Sales.Customer", "--where", "1=1) SELECT 1 WHERE (1=1"}, "2 statements"},
		{[]string{"top", "Sales.Customer", "--where", "1=1\nGO\nSELECT (1"}, "GO line"},
		{[]string{"top", "Sales.Customer", "--order-by", "id; SELECT 1"}, "';'"},
		{[]string{"top", "Sales.Customer", "--where", "id IN (SELECT a FROM OPENQUERY(srv, 'DELETE t'))"}, "not a read"},
		{[]string{"top", "Sales.Customer", "--where", "1=1) UPDATE Sales.Customer SET id = 1 WHERE (1=1"}, "2 statements"},
		{[]string{"top", "Sales.Customer", "--where", "1=1) WAITFOR DELAY '0:0:5' --"}, "2 statements"},
		{[]string{"count", "Sales.Customer", "--where", "1=1); EXEC('DROP TABLE t'); --"}, "';'"},
		{[]string{"count", "Sales.Customer", "--where", "1=1;"}, "';'"},
	} {
		sess := &fakeSession{t: t}
		d, dialled, _ := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, tc.argv...)
		if r.exit != 7 || *dialled != 0 {
			t.Errorf("%q: exit %d, dialled %d\n%s", tc.argv, r.exit, *dialled, r.stdout)
			continue
		}
		env := envelope(t, r.stdout)
		if env["status"] != "blocked" || dataField(t, env, "statements") == nil ||
			!strings.Contains(firstError(t, env)["message"].(string), tc.why) {
			t.Errorf("%q: want %q in %v", tc.argv, tc.why, env)
		}
	}
	// A ';' inside a literal or a comment ends nothing.
	sess := &fakeSession{t: t, run: oneRow}
	d, _, _ := dataDeps(t, newObjectFake(), sess)
	if r := runData(t, d, "top", "Sales.Customer", "--where", "CustomerName = 'a;b' /* ; */"); r.exit != 0 {
		t.Errorf("literal ';': exit %d\n%s", r.exit, r.stdout)
	}
}

func TestDataUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"top", "Sales.Customer", "--rows", "-1"}, "negative"},
		{[]string{"top", "Sales.Customer", "--where", " "}, "empty"},
		{[]string{"top", "Sales.Customer", "--where", "CustomerName = 'x"}, "string literal is not closed"},
		{[]string{"top", "Sales.Customer", "--order-by", "id /* rest"}, "comment is not closed"},
		{[]string{"top", "Sales.Customer", "--where", "[id = 1"}, "quoted name is not closed"},
		{[]string{"top", "Sales.Customer", "--columns", "id,"}, "ends in a comma"},
		{[]string{"top", "Sales.Customer", "--columns", "id name"}, "separated by commas"},
		{[]string{"top", "Sales.Customer", "--columns", "id+1"}, "unexpected"},
		{[]string{"top", "Sales.Customer", "--columns", "id, 'x'"}, "unexpected"},
		{[]string{"count", "Sales.Customer", "--approximate", "--where", "id > 1"}, "both given"},
	} {
		sess := &fakeSession{t: t}
		d, dialled, _ := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, tc.argv...)
		if r.exit != 2 || *dialled != 0 || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%q: exit %d, dialled %d, want %q\n%s", tc.argv, r.exit, *dialled, tc.want, r.stdout)
		}
	}
}

func TestDataObjectChecks(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		exit int
		want string
	}{
		{[]string{"top", "Sales.usp_Get"}, 2, "Give a table or a view"},
		{[]string{"count", "dbo.sn"}, 2, "base object"},
		{[]string{"top", "Sales.Nope"}, 8, "no object"},
		{[]string{"top", "Customer"}, 2, "2 schemas"},
		{[]string{"count", "vCustomer", "--approximate"}, 2, "is a view"},
		// A column the object lacks: NOT_FOUND, naming close ones.
		{[]string{"top", "Sales.Customer", "--columns", "Name"}, 8, "Did you mean CustomerName?"},
		{[]string{"top", "Sales.Customer", "--columns", "zz"}, 8, "columns: id, CustomerName, pct"},
		{[]string{"top", "Sales.Customer", "--columns", "id,ID"}, 2, "named twice"},
	} {
		sess := &fakeSession{t: t}
		d, _, _ := dataDeps(t, newObjectFake(), sess)
		r := runData(t, d, tc.argv...)
		if r.exit != tc.exit || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%q: exit %d, want %d and %q\n%s", tc.argv, r.exit, tc.exit, tc.want, r.stdout)
		}
		if len(sess.executed) != 0 {
			t.Errorf("%q: ran %q", tc.argv, sess.executed)
		}
	}
}

// countSession is a session whose Execute answers the count.
type countSession struct {
	*fakeSession
	answer *query.Result
}

func (c *countSession) Execute(_ context.Context, script string, _ ...query.Option) *query.Result {
	c.executed = append(c.executed, script)
	return c.answer
}

func TestDataCount(t *testing.T) {
	set := query.ResultSet{Columns: []string{"rows"}, Rows: [][]string{{"42"}}, ColumnTypes: []string{"bigint"}}
	sess := &countSession{fakeSession: &fakeSession{t: t}, answer: &query.Result{Sets: []query.ResultSet{set}, Batches: 1,
		Messages: []query.Message{{Text: "(1 row affected)", RowsAffected: &query.RowCount{N: 1, AfterSet: true}}}}}
	d, _, _ := dataDeps(t, newObjectFake(), sess.fakeSession)
	d.OpenSession = func(context.Context, *db.ServerConn, string) (cmd.Session, error) { return sess, nil }

	r := runData(t, d, "count", "Sales.Customer", "--where", "id > 1")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	const want = "SELECT COUNT_BIG(*) AS [rows]\nFROM [Sales].[Customer]\nWHERE (id > 1\n)"
	env := envelope(t, r.stdout)
	if dataField(t, env, "rows") != "42" || dataField(t, env, "method") != "exact" || dataField(t, env, "tsql") != want {
		t.Errorf("record %v", env["data"])
	}
	if len(sess.executed) != 1 || sess.executed[0] != want {
		t.Errorf("sent %q", sess.executed)
	}
	// The count is a record field, not a result set, and the set's count
	// is not echoed.
	if sets, _ := env["resultSets"].([]any); len(sets) != 0 {
		t.Errorf("count answered a set: %v", env["resultSets"])
	}
	if msgs, _ := env["messages"].([]any); len(msgs) != 0 {
		t.Errorf("messages %v", msgs)
	}

	// A failed count: the server's error, rows NULL.
	se := &gosmo.SQLError{Number: 229, Class: 14, State: 5, Message: "The SELECT permission was denied on the object 'Customer'"}
	sess.answer = &query.Result{Batches: 1, FailedBatches: 1, Messages: []query.Message{{Text: se.Header(), IsError: true, SQL: se}}}
	r = runData(t, d, "count", "Sales.Customer")
	if r.exit != 9 {
		t.Fatalf("denied: exit %d\n%s", r.exit, r.stdout)
	}
	env = envelope(t, r.stdout)
	if dataField(t, env, "rows") != nil || !strings.Contains(firstError(t, env)["hint"].(string), "needs SELECT on it") {
		t.Errorf("denied: %v", env)
	}
}

func TestDataCountApproximate(t *testing.T) {
	sess := &fakeSession{t: t}
	d, _, opened := dataDeps(t, newObjectFake(), sess)
	r := runData(t, d, "count", "Sales.Customer", "--approximate")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	env := envelope(t, r.stdout)
	if dataField(t, env, "rows") != "1234" || dataField(t, env, "method") != "approximate" || dataField(t, env, "tsql") != nil {
		t.Errorf("record %v", env["data"])
	}
	// Partition statistics come from the catalog: no session, nothing run.
	if len(*opened) != 0 || len(sess.executed) != 0 {
		t.Errorf("sessions %q, ran %q", *opened, sess.executed)
	}
}

// --timeout cuts the SELECT: TIMEOUT, exit 6.
func TestDataTopTimeout(t *testing.T) {
	sess := &fakeSession{t: t, run: func(ctx context.Context, _ string, _ query.RowSink) *query.Result {
		<-ctx.Done()
		return &query.Result{Batches: 1, Messages: []query.Message{{Text: "Query was cancelled by user.", IsError: true}}}
	}}
	d, _, _ := dataDeps(t, newObjectFake(), sess)
	r := runData(t, d, "top", "Sales.Customer", "--timeout", "1")
	if r.exit != 6 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if !strings.Contains(r.stdout, "--timeout 1") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
}
