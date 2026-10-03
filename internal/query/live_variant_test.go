//go:build livedb

// Live check of appendVariant against what go-mssqldb really hands back for a
// sql_variant of each inner type: the rules there are inferred from the
// driver's decoding, which a fake can't vouch for.
//
//	go test -tags livedb ./internal/query/ -run TestLiveVariantCells -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
package query

import "testing"

func TestLiveVariantCells(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()

	s, _, err := Open(ctx, db, "tempdb")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// SSMS's text alongside, where gossms still differs: the driver drops the
	// inner type, so a date, a time and a GUID can't be told from a datetime
	// and a binary(16) (see appendVariant).
	for _, tt := range []struct{ expr, want string }{
		{"CAST(1.50 AS decimal(5,2))", "1.50"},
		{"CAST(-1.50 AS decimal(5,2))", "-1.50"},
		{"CAST(12.34 AS money)", "12.3400"},      // SSMS: 12.34
		{"CAST(12.34 AS smallmoney)", "12.3400"}, // SSMS: 12.34
		{"CAST('2024-01-02T03:04:05.1234567' AS datetime2(7))", "2024-01-02 03:04:05.1234567"},
		{"CAST('2024-01-02T03:04:05.123' AS datetime)", "2024-01-02 03:04:05.123"},
		{"CAST('2024-01-02T03:04:05.100+02:00' AS datetimeoffset(3))", "2024-01-02 03:04:05.100 +02:00"},
		{"CAST('2024-01-02T03:04:05-05:30' AS datetimeoffset(0))", "2024-01-02 03:04:05.000 -05:30"}, // SSMS: …05 -05:30
		{"CAST('2024-01-02' AS date)", "2024-01-02 00:00:00.000"},                                    // SSMS: 2024-01-02
		{"CAST('03:04:05.12' AS time(2))", "0001-01-01 03:04:05.120"},                                // SSMS: 03:04:05.12
		{"CAST(0x01FF AS varbinary(2))", "0x01FF"},
		{"CAST(42 AS int)", "42"},
		{"CAST(1.5e0 AS float)", "1.5"},
		{"CAST(N'abc' AS nvarchar(10))", "abc"},
		{"CAST(NULL AS int)", "NULL"},
	} {
		res := s.Execute(ctx, "SELECT CAST("+tt.expr+" AS sql_variant)")
		if res.HasErrors() {
			t.Fatalf("%s: %v", tt.expr, messageTexts(res))
		}
		if got := res.Sets[0].Rows[0][0]; got != tt.want {
			t.Errorf("%s: cell = %q, want %q", tt.expr, got, tt.want)
		}
	}
}
