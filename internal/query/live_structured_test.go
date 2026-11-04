//go:build livedb

// Live checks of the executor's structured messages (phase 2.a of the CLI
// plan): which counts SQL Server sends and in what order, whether the driver
// reports nullability, and the error value on the header line. A fake can't
// answer these — they are the server's and go-mssqldb's TDS behaviour.
//
//	go test -tags livedb ./internal/query/ -run TestLiveStructured -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
package query

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// liveCounts returns the run's structured row counts in order.
func liveCounts(res *Result) []RowCount {
	var out []RowCount
	for _, m := range res.Messages {
		if m.RowsAffected != nil {
			out = append(out, *m.RowsAffected)
		}
	}
	return out
}

func TestLiveStructuredRowCounts(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	s := livePlanSession(t, ctx, db, "tempdb")

	// A trigger needs a permanent table: a throwaway pair in tempdb.
	tbl := fmt.Sprintf("gossms_p2a_%d", time.Now().UnixNano())
	setup := fmt.Sprintf("CREATE TABLE dbo.%[1]s(i int)\nCREATE TABLE dbo.%[1]s_log(i int)\nGO\n"+
		"CREATE TRIGGER dbo.%[1]s_tr ON dbo.%[1]s AFTER INSERT AS INSERT dbo.%[1]s_log SELECT i FROM inserted", tbl)
	if res := s.Execute(ctx, setup); res.HasErrors() {
		t.Fatalf("setup: %q", messageTexts(res))
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		db.ExecContext(cctx, fmt.Sprintf("USE tempdb; DROP TABLE IF EXISTS dbo.%[1]s; DROP TABLE IF EXISTS dbo.%[1]s_log", tbl))
	})

	for _, tt := range []struct {
		name, script string
		want         []RowCount
	}{
		{"select then update",
			"CREATE TABLE #c(i int); INSERT #c VALUES (1),(2),(3);\nSELECT i FROM #c;\nUPDATE #c SET i = i + 1 WHERE i > 1;\nDROP TABLE #c",
			[]RowCount{{N: 3}, {N: 3, AfterSet: true}, {N: 2}}},
		{"empty select then delete",
			"CREATE TABLE #c(i int); SELECT i FROM #c; INSERT #c VALUES (1); DELETE #c; DROP TABLE #c",
			[]RowCount{{N: 0, AfterSet: true}, {N: 1}, {N: 1}}},
		{"nocount on",
			"SET NOCOUNT ON; CREATE TABLE #c(i int); INSERT #c VALUES (1),(2); SELECT i FROM #c; UPDATE #c SET i = 0; DROP TABLE #c; SET NOCOUNT OFF",
			nil},
		{"select into",
			"SELECT TOP (4) object_id INTO #c FROM sys.objects; DROP TABLE #c",
			[]RowCount{{N: 4}}},
		// The trigger's INSERT reports its own count (DONEINPROC), before the
		// statement's.
		{"trigger",
			fmt.Sprintf("INSERT dbo.%s VALUES (1),(2)", tbl),
			[]RowCount{{N: 2}, {N: 2}}},
		// The protocol message drops the DONE token's command: OUTPUT rows make a
		// DML count look like a SELECT's (RowCount.AfterSet's documented limit).
		{"update with output",
			"CREATE TABLE #c(i int); INSERT #c VALUES (1),(2);\nUPDATE #c SET i = 0 OUTPUT inserted.i;\nDROP TABLE #c",
			[]RowCount{{N: 2}, {N: 2, AfterSet: true}}},
	} {
		res := s.Execute(ctx, tt.script)
		if res.HasErrors() {
			t.Errorf("%s: %q", tt.name, messageTexts(res))
			continue
		}
		if got := liveCounts(res); !slices.Equal(got, tt.want) {
			t.Errorf("%s: counts %+v, want %+v (messages %q)", tt.name, got, tt.want, messageTexts(res))
		}
	}
}

func TestLiveStructuredNullability(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	s := livePlanSession(t, ctx, db, "tempdb")

	sink := &typedSink{}
	res := s.ExecuteToSink(ctx, "CREATE TABLE #n(a int NOT NULL, b nvarchar(10) NULL, c decimal(9,2) NOT NULL)\n"+
		"INSERT #n VALUES (1, NULL, 1.5)\n"+
		"SELECT a, b, c, a + 1 AS e, CAST(NULL AS int) AS f, ISNULL(b, N'') AS g FROM #n\nDROP TABLE #n", sink)
	if res.HasErrors() {
		t.Fatalf("%q", messageTexts(res))
	}
	if len(sink.nullable) != 1 {
		t.Fatalf("sets = %d, want 1", len(sink.nullable))
	}
	// The server's own description, as sys.dm_exec_describe_first_result_set
	// gives it: a + 1 is nullable (arithmetic can yield NULL), ISNULL(b, N'') is
	// not — so "nullable" is the server's claim, not a promise a column holds one.
	if want := []bool{false, true, false, true, true, false}; !slices.Equal(sink.nullable[0], want) {
		t.Errorf("nullable = %v, want %v", sink.nullable[0], want)
	}
	if want := []string{"int", "nvarchar(10)", "decimal(9,2)", "int", "int", "nvarchar(10)"}; !slices.Equal(sink.types[0], want) {
		t.Errorf("types = %q, want %q", sink.types[0], want)
	}
}

func TestLiveStructuredError(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	s := livePlanSession(t, ctx, db, "tempdb")

	res := s.Execute(ctx, "SELECT 1\nGO\n\nSELECT * FROM dbo.gossms_no_such_table", WithStopOnError())
	var se []*Message
	for i := range res.Messages {
		if res.Messages[i].SQL != nil {
			se = append(se, &res.Messages[i])
		}
	}
	if len(se) != 1 {
		t.Fatalf("errors carrying SQL = %d, want 1: %q", len(se), messageTexts(res))
	}
	if e := se[0].SQL; e.Number != 208 || e.Class != 16 || e.LineNo != 4 || se[0].Line != 4 {
		t.Errorf("SQL = %+v (Line %d), want 208, level 16, script line 4", *e, se[0].Line)
	}
	if !res.StoppedOnError {
		t.Error("StoppedOnError = false after the last batch failed")
	}
}
