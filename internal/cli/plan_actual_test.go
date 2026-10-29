package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/query"
)

// actualSession answers ExecuteToSinkWithPlan by streaming one two-row set
// and one empty set to the sink, then reporting an UPDATE's count and docs as
// the plans. modes records the capture each run asked for.
func actualSession(t *testing.T, tranCount int, docs ...string) (*fakeSession, *[]gosmo.PlanMode) {
	modes := new([]gosmo.PlanMode)
	return &fakeSession{t: t, withPlan: func(_ context.Context, _ string, mode gosmo.PlanMode, sink query.RowSink) *query.Result {
		*modes = append(*modes, mode)
		sink.BeginSet([]string{"id", "name"})
		sink.Row([]string{"1", "a"}, []bool{false, false})
		sink.Row([]string{"2", "NULL"}, []bool{false, true})
		sink.EndSet(2)
		sink.BeginSet([]string{"total"})
		sink.EndSet(0)
		return &query.Result{
			Batches: 1, PlanXML: docs, Database: "app",
			State: &query.SessionState{Database: "app", TranCount: tranCount},
			Messages: []query.Message{
				{Text: "(2 rows affected)", RowsAffected: &query.RowCount{N: 2, AfterSet: true}},
				{Text: "(1 row affected)", RowsAffected: &query.RowCount{N: 1, AfterSet: true, AfterPlan: true}},
				{Text: "(3 rows affected)", RowsAffected: &query.RowCount{N: 3}},
			},
		}
	}}, modes
}

func TestPlanActualGolden(t *testing.T) {
	sess, _ := actualSession(t, 0, readPlanFixture(t, "actual_plan.sqlplan"))
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "SELECT 1")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	got := durationLine.ReplaceAllString(r.stdout, "${1}0")
	path := filepath.Join("testdata", "plan_actual_run_summary.text.golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs (go test -update once the change is meant):\n%s", path, got)
	}
}

// The run captures the actual plan, counts the rows instead of returning
// them, keeps the DML count, and reports the plan.
func TestPlanActualCountsRows(t *testing.T) {
	sess, modes := actualSession(t, 0, missingIndexDoc)
	d, dialled := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "SELECT id, name FROM t", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if *dialled != 1 || !slices.Equal(*modes, []gosmo.PlanMode{gosmo.PlanActual}) || !sess.closed {
		t.Errorf("dialled %d, modes %v, closed %v", *dialled, *modes, sess.closed)
	}
	env := envelope(t, r.stdout)
	if env["rowsAffected"] != 3.0 {
		t.Errorf("rowsAffected %v", env["rowsAffected"])
	}
	// The plan's own count is dropped; the set's and the UPDATE's stay.
	if msgs := env["messages"].([]any); len(msgs) != 2 {
		t.Errorf("messages %v", msgs)
	}
	data := env["data"].(map[string]any)
	if data["planType"] != "actual" || data["detectedClass"] != "read" || data["statementCount"] != "2" {
		t.Errorf("record %v", data)
	}
	var names []string
	for _, s := range env["resultSets"].([]any) {
		names = append(names, s.(map[string]any)["name"].(string))
	}
	if !slices.Equal(names, []string{"statements", "operators", "missing_indexes", "warnings", "result_sets"}) {
		t.Errorf("sets %v", names)
	}
	rs := setNamed(t, env, "result_sets")["rows"].([]any)
	if len(rs) != 2 {
		t.Fatalf("result_sets %v", rs)
	}
	if a, b := rs[0].([]any), rs[1].([]any); a[0] != 1.0 || a[1] != 2.0 || a[2] != "id, name" || b[1] != 0.0 || b[2] != "total" {
		t.Errorf("result_sets %v", rs)
	}

	// Each format keeps result_sets last.
	for format, want := range map[string][]string{"tree": {"tree", "result_sets"}, "xml": {"result_sets"}} {
		r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "SELECT 1", "--plan-format", format, "-o", "json")
		var got []string
		for _, s := range envelope(t, r.stdout)["resultSets"].([]any) {
			got = append(got, s.(map[string]any)["name"].(string))
		}
		if r.exit != 0 || !slices.Equal(got, want) {
			t.Errorf("%s: exit %d, sets %v", format, r.exit, got)
		}
	}
}

// The script really runs, so plan actual applies query run's check before
// connecting, and refuses STATISTICS XML in the script.
func TestPlanActualGate(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--sql", "UPDATE dbo.t SET a = 1"}, "--allow write"},
		{[]string{"--sql", "DROP TABLE dbo.t", "--allow", "admin"}, "ceiling"},
		{[]string{"--sql", "SET NOEXEC ON"}, "Remove the SET"},
		{[]string{"--sql", "SELECT 1\nGO\nSET STATISTICS XML OFF\nSELECT 2"}, "plan actual runs under itself"},
		{[]string{"--sql", "SET STATISTICS IO, XML ON"}, "plan actual runs under itself"},
	} {
		sess, _ := actualSession(t, 0)
		d, dialled := queryDeps(t, sess)
		r := runQuery(t, d, append([]string{"plan", "actual", "-p", "dev"}, tc.argv...)...)
		if r.exit != 7 || *dialled != 0 || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%v: exit %d, dialled %d\n%s", tc.argv, r.exit, *dialled, r.stdout)
		}
	}

	// --dry-run classifies as query run's does, without connecting.
	sess, _ := actualSession(t, 0)
	d, dialled := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "UPDATE dbo.t SET a = 1", "--allow", "write", "--dry-run")
	if r.exit != 0 || *dialled != 0 || !strings.Contains(r.stdout, "DETECTED_CLASS: write\nALLOWED_CLASS: write\n") {
		t.Errorf("dry run: exit %d, dialled %d\n%s", r.exit, *dialled, r.stdout)
	}
	// --statistics is query run's alone.
	if r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "SELECT 1", "--statistics", "io"); r.exit != 2 {
		t.Errorf("--statistics: exit %d", r.exit)
	}
}

// --transaction rollback wraps the run as query run does; the record's
// transaction outcome follows the plan's fields.
func TestPlanActualTransaction(t *testing.T) {
	sess, _ := actualSession(t, 1, missingIndexDoc)
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "UPDATE dbo.t SET a = 1",
		"--allow", "write", "--transaction", "rollback", "-q")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if len(sess.executed) != 2 || !strings.Contains(sess.executed[0], "BEGIN TRANSACTION") || !slices.Equal(sess.ended, []bool{false}) {
		t.Errorf("executed %q, ended %v", sess.executed, sess.ended)
	}
	if !strings.Contains(r.stdout, "PLAN_TYPE: actual\nDETECTED_CLASS: write\nSTATEMENT_COUNT: 2\nTRANSACTION: rolled-back\n") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
}

// --param sends the script through sp_executesql, as query run does.
func TestPlanActualParam(t *testing.T) {
	sess, _ := actualSession(t, 0, missingIndexDoc)
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "SELECT * FROM t WHERE id = @id", "--param", "id=42:int")
	if r.exit != 0 || len(sess.executed) != 1 || !strings.Contains(sess.executed[0], "sp_executesql") ||
		!strings.Contains(sess.executed[0], "@id int") {
		t.Errorf("exit %d, sent %q\n%s", r.exit, sess.executed, r.stdout)
	}
}

// A failed run answers the plans of what ran with the error; Msg 262 is
// PERMISSION_DENIED with the SHOWPLAN hint; --plan-file is not written.
func TestPlanActualErrors(t *testing.T) {
	doc := readPlanFixture(t, "actual_plan.sqlplan")
	for _, c := range []struct {
		number int32
		exit   int
		hint   string
	}{
		{262, 9, "The actual plan needs SHOWPLAN"},
		{208, 5, ""},
	} {
		sess := &fakeSession{t: t, withPlan: func(context.Context, string, gosmo.PlanMode, query.RowSink) *query.Result {
			res := sqlErrorResult(c.number, "denied or missing", 3)
			res.State.TranCount = 0
			res.PlanXML = []string{doc}
			return res
		}}
		d, _ := queryDeps(t, sess)
		path := filepath.Join(t.TempDir(), "q.sqlplan")
		r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "SELECT 1", "--plan-file", path, "-o", "json")
		env := envelope(t, r.stdout)
		e := firstError(t, env)
		hint, _ := e["hint"].(string)
		if r.exit != c.exit || !strings.Contains(hint, c.hint) || (c.hint == "" && strings.Contains(hint, "#temp")) {
			t.Errorf("Msg %d: exit %d, hint %q", c.number, r.exit, hint)
		}
		if data := env["data"].(map[string]any); data["statementCount"] != "1" {
			t.Errorf("Msg %d: record %v", c.number, data)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) || !strings.Contains(r.stdout, "was not written") {
			t.Errorf("Msg %d: plan file written (%v)", c.number, err)
		}
	}
}

func TestPlanActualNoPlan(t *testing.T) {
	sess, _ := actualSession(t, 0)
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "actual", "-p", "dev", "--sql", "PRINT 1", "-o", "json")
	env := envelope(t, r.stdout)
	if r.exit != 0 || env["data"].(map[string]any)["statementCount"] != "0" || len(env["warnings"].([]any)) != 1 {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
	if rs := setNamed(t, env, "result_sets")["rows"].([]any); len(rs) != 2 {
		t.Errorf("result_sets %v", rs)
	}
}
