package query

import (
	"database/sql/driver"
	"testing"
)

// A cap stops the scan at max rows and marks the set Truncated only when a
// further row exists: a set exactly max long is complete.
func TestScanResultSetMaxRows(t *testing.T) {
	rows := func() [][]driver.Value {
		return [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}}
	}
	for _, tt := range []struct {
		max, wantRows int
		wantTrunc     bool
	}{
		{0, 3, false},
		{2, 2, true},
		{3, 3, false},
		{5, 3, false},
	} {
		db := openFakeRowsDB([]string{"n"}, rows())
		r := queryFakeRows(t, db)
		var prog Progress
		rs, err := scanResultSet(r, &prog, tt.max)
		r.Close()
		db.Close()
		if err != nil {
			t.Fatalf("max %d: %v", tt.max, err)
		}
		if len(rs.Rows) != tt.wantRows || rs.Truncated != tt.wantTrunc {
			t.Errorf("max %d: %d rows truncated=%v, want %d rows truncated=%v",
				tt.max, len(rs.Rows), rs.Truncated, tt.wantRows, tt.wantTrunc)
		}
		if prog.Rows() != tt.wantRows {
			t.Errorf("max %d: progress %d, want %d", tt.max, prog.Rows(), tt.wantRows)
		}
	}
}

func TestWithMaxRowsClampsNegative(t *testing.T) {
	r := newRun([]Option{WithMaxRows(-5)})
	if r.opts.maxRows != 0 || r.res.MaxRows != 0 {
		t.Errorf("WithMaxRows(-5) set %d (Result.MaxRows %d), want 0 (no cap)", r.opts.maxRows, r.res.MaxRows)
	}
}

// The streamed path stops at the cap too, reporting the set unexhausted (so
// scanNext drains it) without an error, and closes it with the rows written.
func TestStreamResultSetMaxRows(t *testing.T) {
	for _, tt := range []struct {
		max, wantRows int
		wantExhausted bool
	}{
		{0, 3, true},
		{2, 2, false},
		{3, 3, true},
	} {
		db := openFakeRowsDB([]string{"n"}, [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
		r := queryFakeRows(t, db)
		sink := &recordingSink{}
		n, exhausted, err := streamResultSet(r, sink, nil, tt.max)
		r.Close()
		db.Close()
		if err != nil {
			t.Fatalf("max %d: %v", tt.max, err)
		}
		if n != tt.wantRows || len(sink.rows) != tt.wantRows || exhausted != tt.wantExhausted {
			t.Errorf("max %d: n=%d sink=%d exhausted=%v, want %d rows exhausted=%v",
				tt.max, n, len(sink.rows), exhausted, tt.wantRows, tt.wantExhausted)
		}
		if len(sink.endRows) != 1 || sink.endRows[0] != tt.wantRows {
			t.Errorf("max %d: EndSet got %v, want [%d]", tt.max, sink.endRows, tt.wantRows)
		}
	}
}
