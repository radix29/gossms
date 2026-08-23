//go:build livedb

// Live verification that a multi-statement batch's plans all reach
// Result.PlanXML. The shape they arrive in is the server's, not the fake
// driver's: SET STATISTICS XML appends one plan result set per statement,
// while SET SHOWPLAN_XML returns a single combined document for the whole
// batch — which is exactly the difference a test with a scripted driver
// cannot establish.
//
//	go test -tags livedb ./internal/query/ -run TestLivePlan -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
package query

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/golang-sql/sqlexp"
)

const livePlanBatch = "SELECT TOP (1) name FROM sys.objects;\n" +
	"SELECT TOP (2) name FROM sys.columns;\n" +
	"SELECT TOP (1) name FROM sys.types;"

func livePlanDB(t *testing.T) (*sql.DB, context.Context, func()) {
	t.Helper()
	if *liveDSN == "" {
		t.Skip("no -livedb DSN given")
	}
	db, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	return db, ctx, func() { cancel(); db.Close() }
}

func TestLivePlanActualKeepsOnePlanPerStatement(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()

	res := ExecuteWithPlan(ctx, db, "master", livePlanBatch)
	if res.HasErrors() {
		t.Fatalf("batch failed: %+v", res.Messages)
	}
	if len(res.PlanXML) != 3 {
		t.Fatalf("PlanXML = %d documents, want one per statement (3)", len(res.PlanXML))
	}
	for i, p := range res.PlanXML {
		if !strings.Contains(p, "<ShowPlanXML") {
			t.Errorf("PlanXML[%d] is not a showplan document: %.60s", i, p)
		}
	}
	if len(res.Sets) != 3 {
		t.Errorf("Sets = %d, want the three statements' own result sets", len(res.Sets))
	}
}

func TestLivePlanEstimatedIsOneCombinedDocument(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()

	res := ExecuteEstimatedPlan(ctx, db, "master", livePlanBatch)
	if res.HasErrors() {
		t.Fatalf("batch failed: %+v", res.Messages)
	}
	// SET SHOWPLAN_XML returns one row for the whole batch, holding every
	// statement's plan in a single document — so one document here is the
	// correct answer, and the per-statement plans are inside it.
	if len(res.PlanXML) != 1 {
		t.Fatalf("PlanXML = %d documents, want the batch's single combined one", len(res.PlanXML))
	}
	if n := strings.Count(res.PlanXML[0], "<StmtSimple"); n != 3 {
		t.Errorf("combined document holds %d statements, want 3", n)
	}
	if len(res.Sets) != 0 {
		t.Errorf("Sets = %d, want none: an estimated plan runs nothing", len(res.Sets))
	}
}

// planProbeSetup creates the two procedures the shape probe EXECs, and
// returns the cleanup. Named without an sp_ prefix: an sp_-prefixed procedure
// outside master resolves to master's copy, so CREATE fails and DROP would
// delete master's.
func planProbeSetup(t *testing.T, ctx context.Context, db *sql.DB) func() {
	t.Helper()
	drop := func() {
		for _, n := range []string{"gossms_plan_inner", "gossms_plan_probe"} {
			if _, err := db.ExecContext(ctx, "USE tempdb; DROP PROCEDURE IF EXISTS dbo."+n); err != nil {
				t.Logf("drop %s: %v", n, err)
			}
		}
	}
	drop()
	for _, ddl := range []string{
		"USE tempdb; EXEC('CREATE PROCEDURE dbo.gossms_plan_inner AS SELECT TOP (1) name FROM sys.types;')",
		"USE tempdb; EXEC('CREATE PROCEDURE dbo.gossms_plan_probe AS BEGIN SELECT TOP (1) name FROM sys.objects; EXEC dbo.gossms_plan_inner; END')",
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			drop()
			t.Fatalf("setup: %v", err)
		}
	}
	return drop
}

// planShapes are the batch shapes the probe runs under both SET options. Each
// is a candidate for a server splitting one showplan set across rows — the
// shape scanPlanXML tolerates but nothing has been seen to send.
var planShapes = []struct {
	name  string
	batch string
}{
	{"three statements", livePlanBatch},
	{"exec a two-statement procedure", "EXEC dbo.gossms_plan_probe;"},
	{"statement then exec", "SELECT TOP (1) name FROM sys.types;\nEXEC dbo.gossms_plan_probe;"},
	{"control flow", "IF 1 = 1 SELECT TOP (1) name FROM sys.objects ELSE SELECT TOP (1) name FROM sys.types;"},
	{"while loop", "DECLARE @i int = 0;\nWHILE @i < 2 BEGIN SELECT TOP (1) name FROM sys.objects; SET @i += 1; END"},
	{"dynamic sql", "EXEC sp_executesql N'SELECT TOP (1) name FROM sys.objects; SELECT TOP (1) name FROM sys.types;';"},
	{"cursor", "DECLARE c CURSOR FOR SELECT TOP (2) name FROM sys.objects; OPEN c; DECLARE @n sysname; FETCH NEXT FROM c INTO @n; CLOSE c; DEALLOCATE c;"},
}

// showplanSetRowCounts runs one batch with setOpt on and returns the number of
// rows in each showplan result set the server sent, in order. It reads the
// driver directly rather than through execute, since Result flattens every set
// into one PlanXML slice and the row-per-set shape is exactly what is under
// test.
func showplanSetRowCounts(t *testing.T, ctx context.Context, db *sql.DB, setOpt, sqlText string) []int {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "USE tempdb"); err != nil {
		t.Fatalf("use tempdb: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SET "+setOpt+" ON"); err != nil {
		t.Fatalf("set %s on: %v", setOpt, err)
	}
	defer conn.ExecContext(context.Background(), "SET "+setOpt+" OFF")

	retmsg := &sqlexp.ReturnMessage{}
	rows, err := conn.QueryContext(ctx, sqlText, retmsg)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var counts []int
	for active := true; active; {
		switch m := retmsg.Message(ctx).(type) {
		case sqlexp.MsgError:
			t.Fatalf("batch failed: %v", m.Error)
		case sqlexp.MsgNext:
			cols, err := rows.Columns()
			if err != nil {
				t.Fatalf("columns: %v", err)
			}
			plan := isShowplanResultSet(cols)
			n := 0
			for rows.Next() {
				n++
			}
			if plan {
				counts = append(counts, n)
			}
		case sqlexp.MsgNextResultSet:
			active = rows.NextResultSet()
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return counts
}

// The assumption behind scanPlanXML's append loop, checked against a real
// server rather than reasoned about: no batch shape makes SQL Server put more
// than one row in a single showplan result set. A failure here is new
// information, not a defect — the loop already handles the split shape
// correctly. What it would mean is that scanPlanXML's comment, gosmo's
// capturePlan comment and docs/open-threads.md all name a shape that has now
// been observed, and should say so.
func TestLivePlanEveryShowplanSetHoldsOneRow(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	defer planProbeSetup(t, ctx, db)()

	for _, shape := range planShapes {
		for _, setOpt := range []string{"SHOWPLAN_XML", "STATISTICS XML"} {
			t.Run(shape.name+"/"+setOpt, func(t *testing.T) {
				counts := showplanSetRowCounts(t, ctx, db, setOpt, shape.batch)
				if len(counts) == 0 {
					t.Fatalf("no showplan result set came back at all")
				}
				for i, n := range counts {
					if n != 1 {
						t.Errorf("showplan set %d held %d rows, want 1", i, n)
					}
				}
			})
		}
	}
}

// The other half of the same shape: SHOWPLAN_XML answers a batch with one
// combined document holding every statement, while STATISTICS XML answers
// each executed statement with its own. Both counts reach Result.PlanXML
// through scanPlanXML, so this pins what the plan tab actually shows for a
// batch that calls a procedure.
func TestLivePlanProcedureCallPlansAllReachResult(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	defer planProbeSetup(t, ctx, db)()

	const batch = "SELECT TOP (1) name FROM sys.types;\nEXEC dbo.gossms_plan_probe;"

	est := ExecuteEstimatedPlan(ctx, db, "tempdb", batch)
	if est.HasErrors() {
		t.Fatalf("estimated batch failed: %+v", est.Messages)
	}
	if len(est.PlanXML) != 1 {
		t.Fatalf("estimated PlanXML = %d documents, want the batch's single combined one", len(est.PlanXML))
	}
	// The outer SELECT plus the procedure's two statements, the procedure's
	// own EXEC of the inner one included.
	if n := strings.Count(est.PlanXML[0], "<StmtSimple"); n < 3 {
		t.Errorf("combined document holds %d statements, want the batch's and the procedure's", n)
	}

	act := ExecuteWithPlan(ctx, db, "tempdb", batch)
	if act.HasErrors() {
		t.Fatalf("actual batch failed: %+v", act.Messages)
	}
	if len(act.PlanXML) != 3 {
		t.Fatalf("actual PlanXML = %d documents, want one per executed statement (3)", len(act.PlanXML))
	}
	if len(act.Sets) != 3 {
		t.Errorf("Sets = %d, want the three statements' own result sets", len(act.Sets))
	}
}
