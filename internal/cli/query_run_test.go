package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
)

// fakeSession stands in for query.Session: Execute (the command's own setup
// batches) succeeds, and ExecuteToSink (the script) answers run.
type fakeSession struct {
	t        *testing.T
	run      func(ctx context.Context, script string, sink query.RowSink) *query.Result
	executed []string // every script, setup batches included, in order
	ended    []bool   // EndTransactions' commit arguments
	closed   bool
}

func (f *fakeSession) Execute(_ context.Context, script string, _ ...query.Option) *query.Result {
	f.executed = append(f.executed, script)
	return &query.Result{Messages: []query.Message{{Text: "Commands completed successfully."}}}
}

func (f *fakeSession) ExecuteToSink(ctx context.Context, script string, sink query.RowSink, _ ...query.Option) *query.Result {
	f.executed = append(f.executed, script)
	if f.run == nil {
		return &query.Result{Batches: 1, State: &query.SessionState{TranCount: 0}}
	}
	return f.run(ctx, script, sink)
}

func (f *fakeSession) EndTransactions(_ context.Context, commit bool) error {
	f.ended = append(f.ended, commit)
	return nil
}

func (f *fakeSession) Close() { f.closed = true }

// queryDeps is profileDeps (dev: ceiling write, saved password) with a
// connection that always succeeds and sess as its query session. dialled
// counts the connects.
func queryDeps(t *testing.T, sess *fakeSession) (cmd.Deps, *int) {
	dialled := new(int)
	d := profileDeps(t, func(_ context.Context, c config.Connection, r db.Role) (*db.ServerConn, error) {
		*dialled++
		if r != db.RoleCLI {
			t.Errorf("dialled as %v", r)
		}
		return &db.ServerConn{}, nil
	})
	d.OpenSession = func(context.Context, *db.ServerConn, string) (cmd.Session, error) { return sess, nil }
	return d, dialled
}

func runQuery(t *testing.T, d cmd.Deps, argv ...string) runResult {
	t.Helper()
	return runTree(t, context.Background(), newCommandTree(d), argv...)
}

// rowsThenUpdate streams one two-row set, then reports a SELECT's count (after
// the set) and an UPDATE's.
func rowsThenUpdate(tranCount int) func(context.Context, string, query.RowSink) *query.Result {
	return func(_ context.Context, _ string, sink query.RowSink) *query.Result {
		ts := sink.(query.TypedRowSink)
		ts.BeginTypedSet([]string{"id", "name"}, []string{"int", "nvarchar(10)"}, []bool{false, true})
		ts.Row([]string{"1", "a"}, []bool{false, false})
		ts.Row([]string{"2", "NULL"}, []bool{false, true})
		ts.EndSet(2)
		return &query.Result{
			Batches: 2,
			State:   &query.SessionState{Database: "app", TranCount: tranCount},
			Messages: []query.Message{
				{Text: "(2 rows affected)", RowsAffected: &query.RowCount{N: 2, AfterSet: true}},
				{Text: "(3 rows affected)", RowsAffected: &query.RowCount{N: 3}},
			},
			Database: "app",
		}
	}
}

func sqlErrorResult(number int32, msg string, line int32) *query.Result {
	se := &gosmo.SQLError{Number: number, Class: 16, State: 1, LineNo: line, Message: msg}
	return &query.Result{
		Batches: 1, FailedBatches: 1, StoppedOnError: true,
		State: &query.SessionState{TranCount: 1},
		Messages: []query.Message{
			{Text: se.Header(), IsError: true, Line: int(line), SQL: se},
			{Text: msg, IsError: true, Line: int(line)},
		},
	}
}

func TestQueryRunReturnsRowsAndCounts(t *testing.T) {
	sess := &fakeSession{t: t, run: rowsThenUpdate(0)}
	d, dialled := queryDeps(t, sess)
	r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT id, name FROM t", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	// The SELECT's count is not a change; the UPDATE's is.
	if env["rowsAffected"] != 3.0 || env["server"] != "devsrv,1500" || env["database"] != "app" {
		t.Errorf("header: rowsAffected %v, server %v, database %v", env["rowsAffected"], env["server"], env["database"])
	}
	set := env["resultSets"].([]any)[0].(map[string]any)
	rows := set["rows"].([]any)
	if len(rows) != 2 || rows[0].([]any)[0] != 1.0 || rows[1].([]any)[1] != nil {
		t.Errorf("rows %v", rows)
	}
	if msgs := env["messages"].([]any); len(msgs) != 2 {
		t.Errorf("messages %v", msgs)
	}
	if _, ok := env["data"]; ok {
		t.Error("a run outside a transaction answered a record")
	}
	if *dialled != 1 || !sess.closed || len(sess.executed) != 1 {
		t.Errorf("dialled %d, closed %v, executed %q", *dialled, sess.closed, sess.executed)
	}
}

// The check runs before anything connects: a refusal never dials, and the
// ceiling is the profile's.
func TestQueryRunBlocksBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		hint string
	}{
		{[]string{"--sql", "UPDATE dbo.t SET a = 1"}, "--allow write"},
		{[]string{"--sql", "DROP TABLE dbo.t", "--allow", "admin"}, "ceiling"},
		{[]string{"--sql", "SET NOEXEC ON", "--allow", "write"}, "Remove the SET"},
		{[]string{"--sql", "BEGIN TRAN; UPDATE dbo.t SET a = 1", "--allow", "write", "--transaction", "rollback"}, "BEGIN TRAN"},
		{[]string{"--sql", "EXEC dbo.p", "--allow", "write", "--max-rows-affected", "5"}, "--transaction rollback"},
	} {
		sess := &fakeSession{t: t}
		d, dialled := queryDeps(t, sess)
		r := runQuery(t, d, append([]string{"query", "run", "-p", "dev", "-o", "json"}, tc.argv...)...)
		if r.exit != 7 || *dialled != 0 {
			t.Errorf("%v: exit %d, dialled %d\n%s", tc.argv, r.exit, *dialled, r.stdout)
			continue
		}
		env := envelope(t, r.stdout)
		if env["status"] != "blocked" || !strings.Contains(firstError(t, env)["hint"].(string), tc.hint) {
			t.Errorf("%v: %v", tc.argv, env)
		}
		data := env["data"].(map[string]any)
		if data["detectedClass"] == nil || data["allowedClass"] == nil || data["statements"] == "" {
			t.Errorf("%v: data %v", tc.argv, data)
		}
	}
}

func TestQueryRunDryRun(t *testing.T) {
	sess := &fakeSession{t: t}
	d, dialled := queryDeps(t, sess)
	r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "UPDATE dbo.t SET a = 1", "--allow", "admin", "--dry-run", "-q")
	if r.exit != 0 || *dialled != 0 {
		t.Fatalf("exit %d, dialled %d\n%s", r.exit, *dialled, r.stdout)
	}
	// The allowed class is the effective one: dev's ceiling is write.
	if !strings.Contains(r.stdout, "DETECTED_CLASS: write\nALLOWED_CLASS: write\n") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	r = runQuery(t, d, "query", "run", "-p", "dev", "--sql", "TRUNCATE TABLE dbo.t", "--allow", "admin", "--dry-run")
	if r.exit != 7 || *dialled != 0 || !strings.Contains(r.stdout, "STATEMENTS: TRUNCATE") {
		t.Errorf("blocked dry run: exit %d\n%s", r.exit, r.stdout)
	}
	// Ad hoc, a dry run needs no server at all.
	r = runQuery(t, d, "query", "run", "--sql", "SELECT 1", "--dry-run")
	if r.exit != 0 {
		t.Errorf("ad-hoc dry run: exit %d\n%s", r.exit, r.stdout)
	}
}

func TestQueryRunSQLErrors(t *testing.T) {
	for _, tc := range []struct {
		number int32
		exit   int
		code   string
	}{
		{208, 5, "SQL_ERROR"},
		{229, 9, "PERMISSION_DENIED"},
	} {
		sess := &fakeSession{t: t, run: func(context.Context, string, query.RowSink) *query.Result {
			return sqlErrorResult(tc.number, "the server says no", 3)
		}}
		d, _ := queryDeps(t, sess)
		r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT 1\nGO\nSELECT 2", "-o", "json")
		if r.exit != tc.exit {
			t.Errorf("Msg %d: exit %d, want %d\n%s", tc.number, r.exit, tc.exit, r.stdout)
			continue
		}
		env := envelope(t, r.stdout)
		e := firstError(t, env)
		if e["code"] != tc.code || e["sqlErrorNumber"] != float64(tc.number) || e["line"] != 3.0 ||
			e["severity"] != 16.0 || e["message"] != "the server says no" {
			t.Errorf("Msg %d: error %v", tc.number, e)
		}
		if len(env["errors"].([]any)) != 1 || len(env["messages"].([]any)) != 0 {
			t.Errorf("Msg %d: the error's text line is reported twice: %v / %v", tc.number, env["errors"], env["messages"])
		}
		// One of two batches sent: the stop is said.
		if w := env["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "1 of 2 batches") {
			t.Errorf("Msg %d: warnings %v", tc.number, w)
		}
	}
}

func TestQueryRunPartial(t *testing.T) {
	sess := &fakeSession{t: t, run: func(context.Context, string, query.RowSink) *query.Result {
		r := sqlErrorResult(208, "Invalid object name 'x'.", 3)
		r.Batches, r.StoppedOnError = 2, false
		return r
	}}
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT 1\nGO\nSELECT * FROM x", "--continue-on-error")
	if r.exit != 10 || !strings.Contains(r.stdout, "ERROR_CODE: PARTIAL\nMESSAGE: 1 of 2 batches failed") {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
	// Every batch failed: an error, not PARTIAL.
	sess.run = func(context.Context, string, query.RowSink) *query.Result {
		r := sqlErrorResult(208, "Invalid object name 'x'.", 1)
		r.StoppedOnError = false
		return r
	}
	if r = runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT * FROM x", "--continue-on-error"); r.exit != 5 {
		t.Errorf("all batches failed: exit %d\n%s", r.exit, r.stdout)
	}
	if r = runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT 1", "--continue-on-error",
		"--transaction", "rollback"); r.exit != 2 {
		t.Errorf("--continue-on-error in a transaction: exit %d", r.exit)
	}
}

func TestQueryRunTransactions(t *testing.T) {
	const wrap = "SET XACT_ABORT ON; BEGIN TRANSACTION;"
	for _, tc := range []struct {
		name    string
		argv    []string
		run     func(context.Context, string, query.RowSink) *query.Result
		exit    int
		ended   []bool
		outcome string
	}{
		{"rollback", []string{"--transaction", "rollback"}, rowsThenUpdate(1), 0, []bool{false}, "rolled-back"},
		{"commit", []string{"--transaction", "commit"}, rowsThenUpdate(1), 0, []bool{true}, "committed"},
		{"commit after an error", []string{"--transaction", "commit"},
			func(context.Context, string, query.RowSink) *query.Result { return sqlErrorResult(547, "conflict", 1) },
			5, []bool{false}, "rolled-back"},
		{"XACT_ABORT rolled back", []string{"--transaction", "commit"},
			func(context.Context, string, query.RowSink) *query.Result {
				r := sqlErrorResult(547, "conflict", 1)
				r.State.TranCount = 0
				return r
			}, 5, nil, "rolled-back"},
		{"ended by the script", []string{"--transaction", "rollback"}, rowsThenUpdate(0), 5, nil, "ended-by-script"},
		// A procedure's COMMIT: Msg 266 as it returns, the wrapper gone.
		{"a procedure committed", []string{"--transaction", "rollback"},
			func(context.Context, string, query.RowSink) *query.Result {
				r := sqlErrorResult(266, "Transaction count after EXECUTE indicates a mismatching number", 1)
				r.State.TranCount = 0
				return r
			}, 5, nil, "ended-by-script"},
		{"within the limit", []string{"--max-rows-affected", "3"}, rowsThenUpdate(1), 0, []bool{true}, "committed"},
		{"over the limit", []string{"--max-rows-affected", "2"}, rowsThenUpdate(1), 7, []bool{false}, "rolled-back"},
		{"over the limit, rollback asked", []string{"--max-rows-affected", "2", "--transaction", "rollback"},
			rowsThenUpdate(1), 7, []bool{false}, "rolled-back"},
	} {
		sess := &fakeSession{t: t, run: tc.run}
		d, _ := queryDeps(t, sess)
		argv := append([]string{"query", "run", "-p", "dev", "--sql", "UPDATE dbo.t SET a = 1", "--allow", "write",
			"--statistics", "io,TIME", "-o", "json"}, tc.argv...)
		r := runQuery(t, d, argv...)
		if r.exit != tc.exit {
			t.Errorf("%s: exit %d, want %d\n%s", tc.name, r.exit, tc.exit, r.stdout)
			continue
		}
		if want := []string{"SET STATISTICS IO, TIME ON; " + wrap, "UPDATE dbo.t SET a = 1"}; !slices.Equal(sess.executed, want) {
			t.Errorf("%s: executed %q, want %q", tc.name, sess.executed, want)
		}
		if !slices.Equal(sess.ended, tc.ended) {
			t.Errorf("%s: EndTransactions %v, want %v", tc.name, sess.ended, tc.ended)
		}
		env := envelope(t, r.stdout)
		if got := env["data"].(map[string]any)["transaction"]; got != tc.outcome {
			t.Errorf("%s: transaction %v, want %s", tc.name, got, tc.outcome)
		}
	}
}

func TestQueryRunTimeout(t *testing.T) {
	sess := &fakeSession{t: t, run: func(ctx context.Context, _ string, _ query.RowSink) *query.Result {
		<-ctx.Done()
		return &query.Result{Batches: 1, State: &query.SessionState{TranCount: 1},
			Messages: []query.Message{{Text: "Query was cancelled by user.", IsError: true}}}
	}}
	d, _ := queryDeps(t, sess)
	start := time.Now()
	r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "UPDATE dbo.t SET a = 1", "--allow", "write",
		"--transaction", "commit", "--timeout", "1")
	if r.exit != 6 || !strings.Contains(r.stdout, "TRANSACTION: rolled-back") || len(sess.ended) != 0 || !sess.closed {
		t.Errorf("exit %d, ended %v, closed %v\n%s", r.exit, sess.ended, sess.closed, r.stdout)
	}
	if strings.Contains(r.stdout, "cancelled by user") {
		t.Errorf("the executor's cancel notice reached the output:\n%s", r.stdout)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("--timeout 1 did not bound the run")
	}
}

func TestQueryRunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess := &fakeSession{t: t, run: func(rctx context.Context, _ string, _ query.RowSink) *query.Result {
		cancel()
		<-rctx.Done()
		return &query.Result{Batches: 1}
	}}
	d, _ := queryDeps(t, sess)
	r := runTree(t, ctx, newCommandTree(d), "query", "run", "-p", "dev", "--sql", "WAITFOR DELAY '00:01'")
	if r.exit != 130 || !sess.closed {
		t.Errorf("exit %d, closed %v\n%s", r.exit, sess.closed, r.stdout)
	}
}

func TestQueryRunSessionLost(t *testing.T) {
	sess := &fakeSession{t: t, run: func(context.Context, string, query.RowSink) *query.Result {
		return &query.Result{Batches: 1, SessionLost: true,
			Messages: []query.Message{{Text: "read tcp: connection reset by peer", IsError: true}}}
	}}
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT 1")
	if r.exit != 3 || !strings.Contains(r.stdout, "connection reset by peer") || strings.Contains(r.stdout, "GENERAL_ERROR") {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
}

func TestQueryParse(t *testing.T) {
	sess := &fakeSession{t: t, run: func(context.Context, string, query.RowSink) *query.Result {
		r := sqlErrorResult(102, "Incorrect syntax near 'FORM'.", 4)
		r.Batches = 2
		return r
	}}
	d, _ := queryDeps(t, sess)
	// A write needs no --allow: nothing runs.
	r := runQuery(t, d, "query", "parse", "-p", "dev", "--sql", "SELECT 1\nGO\n\nDELETE FORM t", "-o", "json")
	if r.exit != 5 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	if want := []string{"SET PARSEONLY ON", "SELECT 1\nGO\n\nDELETE FORM t", "SET PARSEONLY OFF"}; !slices.Equal(sess.executed, want) {
		t.Errorf("executed %q, want %q", sess.executed, want)
	}
	env := envelope(t, r.stdout)
	data := env["data"].(map[string]any)
	if data["valid"] != "false" || data["batchCount"] != "2" || data["detectedClass"] != "write" {
		t.Errorf("data %v", data)
	}
	if e := firstError(t, env); e["sqlErrorNumber"] != 102.0 || e["line"] != 4.0 {
		t.Errorf("error %v", e)
	}

	sess = &fakeSession{t: t}
	d, _ = queryDeps(t, sess)
	if r = runQuery(t, d, "query", "parse", "-p", "dev", "--sql", "SELECT 1", "-q"); r.exit != 0 || !strings.HasPrefix(r.stdout, "VALID: true\n") {
		t.Errorf("valid script: exit %d\n%s", r.exit, r.stdout)
	}

	// The script may not switch the mode off: what follows would run.
	sess = &fakeSession{t: t}
	d, dialled := queryDeps(t, sess)
	if r = runQuery(t, d, "query", "parse", "-p", "dev", "--sql", "SET PARSEONLY OFF; DROP TABLE dbo.t"); r.exit != 7 || *dialled != 0 {
		t.Errorf("SET PARSEONLY OFF: exit %d, dialled %d\n%s", r.exit, *dialled, r.stdout)
	}
}

// --param sends the script inside sp_executesql, classified as written: a
// SELECT stays read although sp_executesql alone would be write.
func TestQueryRunParams(t *testing.T) {
	sess := &fakeSession{t: t}
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT * FROM t WHERE id = @id",
		"--param", "id=42:int", "--param", "n=a,b:nvarchar(5)")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	want := "EXEC sp_executesql N'SELECT * FROM t WHERE id = @id', N'@id int, @n nvarchar(5)', @id = N'42', @n = N'a,b';"
	if len(sess.executed) != 1 || sess.executed[0] != want {
		t.Errorf("sent %q", sess.executed)
	}

	// The class is the script's: an UPDATE needs --allow write as without
	// parameters, refused before connecting.
	sess = &fakeSession{t: t}
	d, dialled := queryDeps(t, sess)
	r = runQuery(t, d, "query", "run", "-p", "dev", "--sql", "UPDATE t SET a = @a", "--param", "a=1:int")
	if r.exit != 7 || *dialled != 0 {
		t.Errorf("update: exit %d, dialled %d\n%s", r.exit, *dialled, r.stdout)
	}

	// Usage errors, before connecting, a dry run included.
	for _, argv := range [][]string{
		{"--sql", "SELECT @a\nGO\nSELECT 1", "--param", "a=1:int"},
		{"--sql", "SELECT @a", "--param", "a=1:int; DROP TABLE t"},
		{"--sql", "SELECT @a", "--param", "a=1:int", "--param", "a=2:int"},
		{"--sql", "SELECT @a\nGO\nSELECT 1", "--param", "a=1:int", "--dry-run"},
	} {
		sess = &fakeSession{t: t}
		d, dialled = queryDeps(t, sess)
		r = runQuery(t, d, append([]string{"query", "run", "-p", "dev"}, argv...)...)
		if r.exit != 2 || *dialled != 0 {
			t.Errorf("%v: exit %d, dialled %d\n%s", argv, r.exit, *dialled, r.stdout)
		}
	}
}
