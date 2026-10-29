package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
)

// readPlanFixture is one of internal/showplan's test plans as the executor
// hands it over: decoded text.
func readPlanFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "showplan", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := showplan.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return p.XML
}

// planSession answers ExecuteEstimatedPlan with docs as Result.PlanXML.
func planSession(t *testing.T, docs ...string) *fakeSession {
	return &fakeSession{t: t, estimated: func(context.Context, string) *query.Result {
		return &query.Result{Batches: 1, PlanXML: docs, Database: "app", State: &query.SessionState{Database: "app"}}
	}}
}

var durationLine = regexp.MustCompile(`(DURATION_MS: |"durationMs": )\d+`)

// The three formats over showplan's test plans, an estimated one and an
// actual one (the summary's actual columns come from the same code plan
// actual will use).
func TestPlanEstimatedGoldens(t *testing.T) {
	cases := []struct {
		fixture, format, output string
	}{
		{"estimated_plan.xml", "summary", "text"},
		{"estimated_plan.xml", "summary", "json"},
		{"estimated_plan.xml", "tree", "text"},
		{"estimated_plan.xml", "xml", "text"},
		{"actual_plan.sqlplan", "summary", "text"},
		{"actual_plan.sqlplan", "tree", "text"},
	}
	for _, c := range cases {
		sess := planSession(t, readPlanFixture(t, c.fixture))
		d, _ := queryDeps(t, sess)
		r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--plan-format", c.format, "-o", c.output)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d\n%s%s", c, r.exit, r.stdout, r.stderr)
		}
		got := durationLine.ReplaceAllString(r.stdout, "${1}0")
		base := strings.TrimSuffix(c.fixture, filepath.Ext(c.fixture))
		path := filepath.Join("testdata", "plan_"+base+"_"+c.format+"."+c.output+".golden")
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
}

// missingIndexDoc is a two-statement batch: a trivial statement whose lone
// operator is 100% of it, and a costlier one with a missing index and a
// statement-level warning.
const missingIndexDoc = `<ShowPlanXML Version="1.599" Build="17.0.1125.2"><BatchSequence><Batch><Statements>
<StmtSimple StatementText="SELECT 1" StatementType="SELECT" StatementSubTreeCost="0.0000011" StatementEstRows="1">
<QueryPlan><RelOp NodeId="0" PhysicalOp="Constant Scan" LogicalOp="Constant Scan" EstimateRows="1" EstimatedTotalSubtreeCost="0.0000011"/></QueryPlan>
</StmtSimple>
<StmtSimple StatementText="SELECT *&#xA;  FROM dbo.Appointments WHERE DoctorID = 3" StatementType="SELECT" StatementSubTreeCost="4.5" StatementEstRows="20">
<QueryPlan DegreeOfParallelism="1">
<Warnings><PlanAffectingConvert ConvertIssue="Seek Plan" Expression="CONVERT_IMPLICIT(int,[x],0)"/></Warnings>
<MissingIndexes><MissingIndexGroup Impact="96.4">
<MissingIndex Database="[HealthClinic]" Schema="[dbo]" Table="[Appointments]">
<ColumnGroup Usage="EQUALITY"><Column Name="[DoctorID]" ColumnId="3"/></ColumnGroup>
<ColumnGroup Usage="INCLUDE"><Column Name="[Status]" ColumnId="6"/><Column Name="[Notes]" ColumnId="7"/></ColumnGroup>
</MissingIndex></MissingIndexGroup></MissingIndexes>
<RelOp NodeId="0" PhysicalOp="Parallelism" LogicalOp="Gather Streams" EstimateRows="20" EstimatedTotalSubtreeCost="4.5" Parallel="1"><Parallelism>
<RelOp NodeId="1" PhysicalOp="Table Scan" LogicalOp="Table Scan" EstimateRows="20" EstimatedTotalSubtreeCost="4.4" Parallel="1">
<TableScan><Object Database="[HealthClinic]" Schema="[dbo]" Table="[Appointments]"/></TableScan>
</RelOp></Parallelism></RelOp>
</QueryPlan></StmtSimple>
</Statements></Batch></BatchSequence></ShowPlanXML>`

func setNamed(t *testing.T, env map[string]any, name string) map[string]any {
	t.Helper()
	for _, s := range env["resultSets"].([]any) {
		if set := s.(map[string]any); set["name"] == name {
			return set
		}
	}
	t.Fatalf("no set %q in %v", name, env["resultSets"])
	return nil
}

func TestPlanEstimatedSummaryAcrossStatements(t *testing.T) {
	sess := planSession(t, missingIndexDoc)
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--top", "2", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	data := env["data"].(map[string]any)
	if data["planType"] != "estimated" || data["statementCount"] != "2" || data["detectedClass"] != "read" {
		t.Errorf("record %v", data)
	}
	// Ranked by own cost: the scan (4.4), then the gather (0.1); the constant
	// scan, 100% of its trivial statement, is cut by --top 2.
	ops := setNamed(t, env, "operators")["rows"].([]any)
	if len(ops) != 2 {
		t.Fatalf("operators %v", ops)
	}
	scan, gather := ops[0].([]any), ops[1].([]any)
	if scan[0] != 2.0 || scan[1] != 1.0 || scan[2] != "Table Scan" || scan[4] != "dbo.Appointments" || scan[5] != 97.8 || scan[9] != true {
		t.Errorf("first operator %v", scan)
	}
	if gather[2] != "Parallelism" || gather[12] != nil {
		t.Errorf("second operator %v", gather)
	}
	stmts := setNamed(t, env, "statements")["rows"].([]any)
	if s2 := stmts[1].([]any); s2[1] != "SELECT" || s2[2] != 4.5 || s2[4] != nil || s2[7] != 1.0 || s2[9] != 1.0 ||
		s2[10] != "SELECT * FROM dbo.Appointments WHERE DoctorID = 3" {
		t.Errorf("statement 2 %v", s2)
	}
	mi := setNamed(t, env, "missing_indexes")["rows"].([]any)
	if len(mi) != 1 {
		t.Fatalf("missing indexes %v", mi)
	}
	if row := mi[0].([]any); row[0] != 2.0 || row[2] != "HealthClinic" || row[3] != "dbo.Appointments" ||
		row[4] != "DoctorID" || row[5] != nil || row[6] != "Status, Notes" ||
		row[7] != "CREATE NONCLUSTERED INDEX [<Name of Missing Index, sysname,>] ON [dbo].[Appointments] ([DoctorID]) INCLUDE ([Status],[Notes])" {
		t.Errorf("missing index %v", row)
	}
	w := setNamed(t, env, "warnings")["rows"].([]any)
	if len(w) != 1 || w[0].([]any)[0] != 2.0 || w[0].([]any)[1] != nil {
		t.Errorf("warnings %v", w)
	}

	// --top 0 keeps every operator.
	r = runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--top", "0", "-o", "json")
	if ops := setNamed(t, envelope(t, r.stdout), "operators")["rows"].([]any); len(ops) != 3 {
		t.Errorf("--top 0: %d operators", len(ops))
	}
}

// A mode option anywhere, OFF included, is refused before connecting: an OFF
// would switch SHOWPLAN_XML off and run what follows.
func TestPlanEstimatedRefusesModeOptions(t *testing.T) {
	for _, sql := range []string{"SET SHOWPLAN_XML OFF\nGO\nDELETE t", "SET NOEXEC ON", "SET PARSEONLY OFF"} {
		sess := planSession(t)
		d, dialled := queryDeps(t, sess)
		r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", sql)
		if r.exit != 7 || *dialled != 0 || !strings.Contains(r.stdout, "plan estimated runs under itself") {
			t.Errorf("%q: exit %d, dialled %d\n%s", sql, r.exit, *dialled, r.stdout)
		}
	}
}

// Nothing runs, so a script above the profile's ceiling (dev: write) needs no
// --allow and is sent as written. There is no --param: SHOWPLAN_XML plans an
// sp_executesql call as the EXEC alone.
func TestPlanEstimatedSendsAnyClass(t *testing.T) {
	sess := planSession(t, missingIndexDoc)
	d, _ := queryDeps(t, sess)
	const script = "DROP TABLE dbo.t"
	r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", script, "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if data := envelope(t, r.stdout)["data"].(map[string]any); data["detectedClass"] != "ddl" {
		t.Errorf("record %v", data)
	}
	if len(sess.executed) != 1 || sess.executed[0] != script {
		t.Errorf("sent %q", sess.executed)
	}
	if r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", script, "--param", "id=1:int"); r.exit != 2 {
		t.Errorf("--param: exit %d", r.exit)
	}
}

// Msg 262 (no SHOWPLAN) is PERMISSION_DENIED, exit 9; Msg 208 stays a SQL
// error, with a hint naming the temp-table case. Both carry a hint.
func TestPlanEstimatedErrors(t *testing.T) {
	cases := []struct {
		number int32
		exit   int
		hint   string
	}{
		{262, 9, "GRANT SHOWPLAN"},
		{208, 5, "#temp table"},
	}
	for _, c := range cases {
		sess := &fakeSession{t: t, estimated: func(context.Context, string) *query.Result {
			return sqlErrorResult(c.number, "denied or missing", 1)
		}}
		d, _ := queryDeps(t, sess)
		r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT * FROM #t", "-o", "json")
		if r.exit != c.exit || !strings.Contains(r.stdout, c.hint) {
			t.Errorf("Msg %d: exit %d, want %d with hint %q\n%s", c.number, r.exit, c.exit, c.hint, r.stdout)
		}
	}
}

func TestPlanEstimatedNoPlan(t *testing.T) {
	sess := planSession(t)
	d, _ := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "-o", "json")
	env := envelope(t, r.stdout)
	if r.exit != 0 || env["data"].(map[string]any)["statementCount"] != "0" || len(env["warnings"].([]any)) != 1 {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
}

// --plan-file writes the plan document as the TUI's Save Execution Plan As
// does, refuses an existing file without --force before connecting, and is
// not written when the command fails.
func TestPlanEstimatedPlanFile(t *testing.T) {
	doc := readPlanFixture(t, "estimated_plan.xml")
	path := filepath.Join(t.TempDir(), "q.sqlplan")
	sess := planSession(t, doc)
	d, dialled := queryDeps(t, sess)
	r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--plan-file", path, "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if got, _ := os.ReadFile(path); string(got) != doc {
		t.Errorf("plan file differs from the document (%d bytes, want %d)", len(got), len(doc))
	}
	if data := envelope(t, r.stdout)["data"].(map[string]any); data["planFile"] != path {
		t.Errorf("record %v", data)
	}

	*dialled = 0
	r = runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--plan-file", path)
	if r.exit != 2 || *dialled != 0 || !strings.Contains(r.stdout+r.stderr, "the file exists") {
		t.Errorf("existing file: exit %d, dialled %d\n%s%s", r.exit, *dialled, r.stdout, r.stderr)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--plan-file", path, "--force")
	if got, _ := os.ReadFile(path); r.exit != 0 || string(got) != doc {
		t.Errorf("--force: exit %d, file %d bytes\n%s", r.exit, len(got), r.stdout)
	}

	// A failed compile leaves the file as it was, with a warning.
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	failing := &fakeSession{t: t, estimated: func(context.Context, string) *query.Result {
		res := sqlErrorResult(208, "Invalid object name '#t'.", 2)
		res.PlanXML = []string{doc}
		return res
	}}
	d, _ = queryDeps(t, failing)
	r = runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--plan-file", path, "--force")
	if got, _ := os.ReadFile(path); r.exit != 5 || string(got) != "old" || !strings.Contains(r.stdout, "was not written") {
		t.Errorf("failed run: exit %d, file %q\n%s", r.exit, got, r.stdout)
	}
}

// --force is a usage error without a file to replace, and names the files
// the command can write.
func TestForceNamesTheCommandsFiles(t *testing.T) {
	d, _ := queryDeps(t, planSession(t))
	r := runQuery(t, d, "plan", "estimated", "-p", "dev", "--sql", "SELECT 1", "--force")
	if r.exit != 2 || !strings.Contains(r.stdout+r.stderr, "--force: only with --out-file or --plan-file") {
		t.Errorf("plan estimated: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	r = runQuery(t, d, "query", "run", "-p", "dev", "--sql", "SELECT 1", "--force")
	if out := r.stdout + r.stderr; r.exit != 2 || !strings.Contains(out, "--force: only with --out-file") || strings.Contains(out, "--plan-file") {
		t.Errorf("query run: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}
