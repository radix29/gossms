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
