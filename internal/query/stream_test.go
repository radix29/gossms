package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

// The streaming path (streamResultSet, behind ExecuteToSink) and the
// buffering path (scanResultSet, behind Execute) must render a row
// identically — the switch to streaming was meant to change when a row is
// written, not what it looks like. These tests drive both over the same fake
// result set and compare.

// fakeRowsConn returns one fixed result set for any query.
type fakeRowsConn struct {
	cols []string
	rows [][]driver.Value
}

func (c *fakeRowsConn) Prepare(string) (driver.Stmt, error) { return nil, errFakeRowsUnsupported }
func (c *fakeRowsConn) Close() error                        { return nil }
func (c *fakeRowsConn) Begin() (driver.Tx, error)           { return nil, errFakeRowsUnsupported }

func (c *fakeRowsConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{cols: c.cols, rows: c.rows}, nil
}

func (c *fakeRowsConn) ExecContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

var errFakeRowsUnsupported = errors.New("fakeRowsConn: unsupported")

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

type fakeRowsConnector struct{ conn *fakeRowsConn }

func (f *fakeRowsConnector) Connect(context.Context) (driver.Conn, error) { return f.conn, nil }
func (f *fakeRowsConnector) Driver() driver.Driver                        { return nil }

var (
	_ driver.Conn           = (*fakeRowsConn)(nil)
	_ driver.QueryerContext = (*fakeRowsConn)(nil)
	_ driver.ExecerContext  = (*fakeRowsConn)(nil)
)

// recordingSink captures what a RowSink is handed, so a test can compare it
// against what the buffering path produced.
type recordingSink struct {
	begins  [][]string
	rows    [][]string
	endRows []int
	failOn  int // 1-based row index to fail on; 0 never fails
}

func (s *recordingSink) BeginSet(cols []string) error {
	s.begins = append(s.begins, append([]string(nil), cols...))
	return nil
}

func (s *recordingSink) Row(cells []string) error {
	if s.failOn > 0 && len(s.rows)+1 == s.failOn {
		return errors.New("sink write failed")
	}
	s.rows = append(s.rows, append([]string(nil), cells...))
	return nil
}

func (s *recordingSink) EndSet(n int) error {
	s.endRows = append(s.endRows, n)
	return nil
}

// openFakeRowsDB returns a *sql.DB serving one fixed result set.
func openFakeRowsDB(cols []string, rows [][]driver.Value) *sql.DB {
	return sql.OpenDB(&fakeRowsConnector{conn: &fakeRowsConn{cols: cols, rows: rows}})
}

func queryFakeRows(t *testing.T, db *sql.DB) *sql.Rows {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return rows
}

var streamTestCols = []string{"n", "s", "t", "b", "nul"}

func streamTestRows() [][]driver.Value {
	ts := time.Date(2026, 7, 30, 14, 5, 6, 0, time.UTC)
	return [][]driver.Value{
		{int64(1), "alpha", ts, []byte{0xDE, 0xAD}, nil},
		{int64(2), "beta", ts, []byte{0x00}, nil},
		{int64(3), "gamma", ts, []byte{}, nil},
	}
}

// The core equivalence claim: same rows in, same cells out.
func TestStreamAndScanRenderIdenticalCells(t *testing.T) {
	bufDB := openFakeRowsDB(streamTestCols, streamTestRows())
	defer bufDB.Close()
	bufRows := queryFakeRows(t, bufDB)
	rs, truncated, err := scanResultSet(bufRows, 0)
	bufRows.Close()
	if err != nil {
		t.Fatalf("scanResultSet: %v", err)
	}
	if truncated {
		t.Fatal("scanResultSet reported truncation with maxRows=0")
	}

	streamDB := openFakeRowsDB(streamTestCols, streamTestRows())
	defer streamDB.Close()
	streamRows := queryFakeRows(t, streamDB)
	sink := &recordingSink{}
	n, err := streamResultSet(streamRows, sink)
	streamRows.Close()
	if err != nil {
		t.Fatalf("streamResultSet: %v", err)
	}

	if n != len(rs.Rows) {
		t.Fatalf("streamed %d rows, buffered %d", n, len(rs.Rows))
	}
	if len(sink.begins) != 1 {
		t.Fatalf("BeginSet called %d times, want 1", len(sink.begins))
	}
	for i := range rs.Columns {
		if sink.begins[0][i] != rs.Columns[i] {
			t.Errorf("column %d: streamed %q, buffered %q", i, sink.begins[0][i], rs.Columns[i])
		}
	}
	for i := range rs.Rows {
		for j := range rs.Rows[i] {
			if sink.rows[i][j] != rs.Rows[i][j] {
				t.Errorf("row %d col %d: streamed %q, buffered %q",
					i, j, sink.rows[i][j], rs.Rows[i][j])
			}
		}
	}
	if len(sink.endRows) != 1 || sink.endRows[0] != n {
		t.Errorf("EndSet got %v, want [%d]", sink.endRows, n)
	}
}

// The streaming path must ignore any row cap: an export writes everything the
// query returned, which is the whole reason it exists.
func TestStreamResultSetIgnoresNoCap(t *testing.T) {
	rows := make([][]driver.Value, 500)
	for i := range rows {
		rows[i] = []driver.Value{int64(i), "x", time.Time{}, []byte(nil), nil}
	}
	db := openFakeRowsDB(streamTestCols, rows)
	defer db.Close()

	r := queryFakeRows(t, db)
	sink := &recordingSink{}
	n, err := streamResultSet(r, sink)
	r.Close()
	if err != nil {
		t.Fatalf("streamResultSet: %v", err)
	}
	if n != 500 || len(sink.rows) != 500 {
		t.Errorf("streamed n=%d, sink got %d rows; want 500 of each", n, len(sink.rows))
	}
}

// A sink that fails mid-set reports the error and the count of rows that did
// make it, so the caller can say how much of the file is real.
func TestStreamResultSetReportsSinkFailure(t *testing.T) {
	db := openFakeRowsDB(streamTestCols, streamTestRows())
	defer db.Close()

	r := queryFakeRows(t, db)
	sink := &recordingSink{failOn: 2}
	n, err := streamResultSet(r, sink)
	r.Close()
	if err == nil {
		t.Fatal("streamResultSet returned nil error after the sink failed")
	}
	if n != 1 {
		t.Errorf("n = %d, want 1 — the count of rows written before the failure", n)
	}
}

// ExecuteToSink itself is not unit-tested end to end: it runs through
// sqlexp's ReturnMessage protocol, which a fake driver cannot reproduce — the
// message loop needs the driver to populate MsgNext/MsgNextResultSet, and a
// fake that ignores the retmsg out-param just ends the loop immediately,
// producing a test that passes without exercising anything. The rows-to-cells
// logic it adds is covered above via streamResultSet; the wiring
// (Sets stays empty, RowsWritten totals the rows, the redundant "Commands
// completed successfully." is suppressed) needs the live-server check
// CLAUDE.md calls for.
