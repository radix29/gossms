//go:build livedb

// Live check that a multi-statement batch's plans all reach Result.PlanXML. The
// shape is the server's: STATISTICS XML sends one plan set per statement,
// SHOWPLAN_XML one combined document per batch — which a scripted driver can't
// establish.
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
	// SHOWPLAN_XML returns one document holding every statement's plan.
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

// planProbeSetup creates the two procedures the probe EXECs and returns the
// cleanup. No sp_ prefix: outside master that resolves to master's copy,
// failing CREATE and making DROP delete master's.
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

// planShapes are batch shapes run under both SET options, each a candidate for
// a server splitting one showplan set across rows (tolerated by scanPlanXML,
// never observed).
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

// showplanSetRowCounts runs one batch with setOpt on and returns each showplan
// set's row count. Reads the driver directly, since Result flattens sets into
// PlanXML.
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

// Checks scanPlanXML's assumption that no batch puts more than one row in a
// showplan set. A failure is new information, not a defect (the loop handles
// it), and means scanPlanXML's comment, gosmo's capturePlan and
// docs/open-threads.md need updating.
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

// SHOWPLAN_XML answers a batch with one combined document; STATISTICS XML
// answers each executed statement separately. Pins what the plan tab shows for
// a batch calling a procedure.
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
	// The outer SELECT plus the procedure's two statements, including its EXEC
	// of the inner one.
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
