//go:build livedb

// Live check of review-plan Q1: a digit in a GO line's trailing comment is not
// a repeat count, and a count on the script's last line, with no newline after
// it, is honoured.
//
//	go test -tags livedb ./internal/query/ -run TestLiveGoBatchCounts -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
package query

import "testing"

func TestLiveGoBatchCounts(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()

	s, _, err := Open(ctx, db, "tempdb")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	for _, tt := range []struct{ script, want string }{
		{"CREATE TABLE #t(i int)\nGO\nINSERT #t VALUES(1)\nGO -- step 2\nSELECT COUNT(*) FROM #t\nDROP TABLE #t", "1"},
		{"CREATE TABLE #t(i int)\nGO\nINSERT #t VALUES(1)\nGO 3", ""},
	} {
		res := s.Execute(ctx, tt.script)
		if res.HasErrors() {
			t.Fatalf("%q: %v", tt.script, messageTexts(res))
		}
		if tt.want != "" {
			if got := res.Sets[len(res.Sets)-1].Rows[0][0]; got != tt.want {
				t.Errorf("%q: COUNT(*) = %s, want %s", tt.script, got, tt.want)
			}
		}
	}
	res := s.Execute(ctx, "SELECT COUNT(*) FROM #t\nDROP TABLE #t")
	if res.HasErrors() || res.Sets[0].Rows[0][0] != "3" {
		t.Errorf("after GO 3 on the last line: %v / %v, want a count of 3", messageTexts(res), res.Sets)
	}
}
