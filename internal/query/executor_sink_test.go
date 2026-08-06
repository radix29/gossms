package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/golang-sql/sqlexp"
)

// ExecuteToSink's wiring — Result.Sets staying empty, RowsWritten totalling,
// the per-set "(N row(s) written)" notice, and the suppressed "Commands
// completed successfully." — runs through sqlexp's ReturnMessage protocol
// rather than through plain database/sql calls, which is why it went
// untested for a long time. A fake that merely ignores the retmsg out-param
// ends runBatch's message loop immediately and passes without exercising
// anything; these tests instead implement the protocol the way the sqlexp
// docs specify a driver must (driver.NamedValueChecker intercepts the
// *ReturnMessage, ReturnMessageInit sets it up, ReturnMessageEnqueue feeds
// it), so the loop runs for real.

// fakeMsgKind is one scripted message in a batch's stream.
type fakeMsgKind int

const (
	msgSet fakeMsgKind = iota // a result set: MsgNext, then its rows
	msgNotice
	msgError
	msgAffected
)

type fakeMsg struct {
	kind  fakeMsgKind
	text  string
	err   error
	count int64
	cols  []string
	rows  [][]driver.Value
}

// fakeMsgConn replays one scripted batch per QueryContext call carrying a
// *sqlexp.ReturnMessage. Any other query (acquireConn's prologue,
// currentDatabase's SELECT DB_NAME()) is answered directly.
type fakeMsgConn struct {
	batches [][]fakeMsg
	next    int
	retmsg  *sqlexp.ReturnMessage
	dbName  string
}

func (c *fakeMsgConn) Prepare(string) (driver.Stmt, error) { return nil, errFakeMsgUnsupported }
func (c *fakeMsgConn) Close() error                        { return nil }
func (c *fakeMsgConn) Begin() (driver.Tx, error)           { return nil, errFakeMsgUnsupported }

var errFakeMsgUnsupported = errors.New("fakeMsgConn: unsupported")

// CheckNamedValue implements the driver half of the sqlexp contract: take
// the *ReturnMessage out of the argument list, initialise it, and omit it
// from the arguments the query actually sees.
func (c *fakeMsgConn) CheckNamedValue(nv *driver.NamedValue) error {
	if rm, ok := nv.Value.(*sqlexp.ReturnMessage); ok {
		sqlexp.ReturnMessageInit(rm)
		c.retmsg = rm
		return driver.ErrRemoveArgument
	}
	return driver.ErrSkip
}

func (c *fakeMsgConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (c *fakeMsgConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.retmsg == nil {
		if strings.Contains(q, "DB_NAME()") {
			return &fakeMsgRows{sets: []fakeMsg{{cols: []string{"", ""}[:1], rows: [][]driver.Value{{c.dbName}}}}}, nil
		}
		return &fakeMsgRows{sets: []fakeMsg{{}}}, nil
	}
	rm := c.retmsg
	c.retmsg = nil

	if c.next >= len(c.batches) {
		return nil, errors.New("fakeMsgConn: no scripted batch left")
	}
	script := c.batches[c.next]
	c.next++

	rows := &fakeMsgRows{}
	for _, m := range script {
		switch m.kind {
		case msgSet:
			rows.sets = append(rows.sets, m)
		}
	}
	// A batch that returned no result set is still a rows with one empty,
	// zero-column set — the same shape TDS reports for an INSERT.
	if len(rows.sets) == 0 {
		rows.sets = append(rows.sets, fakeMsg{})
	}

	// The buffer is 15, and enqueueing before returning from Query is what
	// keeps these tests single-goroutine; keep scripts short.
	enqueue := func(raw sqlexp.RawMessage) error { return sqlexp.ReturnMessageEnqueue(ctx, rm, raw) }
	for _, m := range script {
		var err error
		switch m.kind {
		case msgSet:
			// MsgNext announces a set; the MsgNextResultSet that closes it
			// out is what makes the client call rows.NextResultSet.
			err = enqueue(sqlexp.MsgNext{})
			if err == nil {
				err = enqueue(sqlexp.MsgNextResultSet{})
			}
		case msgNotice:
			err = enqueue(sqlexp.MsgNotice{Message: fakeNotice(m.text)})
		case msgError:
			err = enqueue(sqlexp.MsgError{Error: m.err})
		case msgAffected:
			err = enqueue(sqlexp.MsgRowsAffected{Count: m.count})
		}
		if err != nil {
			return nil, err
		}
	}
	// The final one ends the loop: NextResultSet has nothing left to
	// advance to and returns false.
	if err := enqueue(sqlexp.MsgNextResultSet{}); err != nil {
		return nil, err
	}
	return rows, nil
}

type fakeNotice string

func (n fakeNotice) String() string { return string(n) }

// fakeMsgRows serves the batch's result sets in order.
type fakeMsgRows struct {
	sets []fakeMsg
	set  int
	i    int
}

func (r *fakeMsgRows) Columns() []string { return r.sets[r.set].cols }
func (r *fakeMsgRows) Close() error      { return nil }

func (r *fakeMsgRows) Next(dest []driver.Value) error {
	s := r.sets[r.set]
	if r.i >= len(s.rows) {
		return io.EOF
	}
	copy(dest, s.rows[r.i])
	r.i++
	return nil
}

func (r *fakeMsgRows) HasNextResultSet() bool { return r.set+1 < len(r.sets) }

func (r *fakeMsgRows) NextResultSet() error {
	if !r.HasNextResultSet() {
		return io.EOF
	}
	r.set++
	r.i = 0
	return nil
}

type fakeMsgConnector struct{ conn *fakeMsgConn }

func (f *fakeMsgConnector) Connect(context.Context) (driver.Conn, error) { return f.conn, nil }
func (f *fakeMsgConnector) Driver() driver.Driver                        { return nil }

var (
	_ driver.Conn              = (*fakeMsgConn)(nil)
	_ driver.QueryerContext    = (*fakeMsgConn)(nil)
	_ driver.ExecerContext     = (*fakeMsgConn)(nil)
	_ driver.NamedValueChecker = (*fakeMsgConn)(nil)
	_ driver.RowsNextResultSet = (*fakeMsgRows)(nil)
)

// openFakeMsgDB serves one scripted batch per GO batch of the script.
func openFakeMsgDB(batches ...[]fakeMsg) *sql.DB {
	db := sql.OpenDB(&fakeMsgConnector{conn: &fakeMsgConn{batches: batches, dbName: "testdb"}})
	// One physical connection, so the scripted batches are consumed in
	// order by whichever conn executeWithSink acquires.
	db.SetMaxOpenConns(1)
	return db
}

func set(cols []string, rows ...[]driver.Value) fakeMsg {
	return fakeMsg{kind: msgSet, cols: cols, rows: rows}
}

// firstSetFailSink fails on one row of the *first* set only, then accepts
// everything after — recordingSink.failOn counts rows across the whole run,
// which would fail the recovery set too and hide what the test is checking.
type firstSetFailSink struct {
	recordingSink
	failOn int // 1-based row index within the first set
	set    int
}

func (s *firstSetFailSink) BeginSet(cols []string) error {
	s.set++
	return s.recordingSink.BeginSet(cols)
}

func (s *firstSetFailSink) Row(cells []string) error {
	if s.set == 1 && s.failOn > 0 && len(s.rows)+1 == s.failOn {
		return errors.New("sink write failed")
	}
	return s.recordingSink.Row(cells)
}

func hasMessage(res *Result, text string) bool {
	for _, m := range res.Messages {
		if m.Text == text {
			return true
		}
	}
	return false
}

func messageTexts(res *Result) []string {
	out := make([]string, len(res.Messages))
	for i, m := range res.Messages {
		out[i] = m.Text
	}
	return out
}

// The whole point of the sink path: rows reach the sink, Result.Sets stays
// empty, and RowsWritten totals across every set.
func TestExecuteToSinkStreamsSetsAndRetainsNothing(t *testing.T) {
	db := openFakeMsgDB([]fakeMsg{
		set([]string{"n"}, []driver.Value{int64(1)}, []driver.Value{int64(2)}),
		set([]string{"s"}, []driver.Value{"alpha"}),
	})
	defer db.Close()

	sink := &recordingSink{}
	res := ExecuteToSink(context.Background(), db, "", "SELECT 1", sink)

	if len(res.Sets) != 0 {
		t.Errorf("Result.Sets has %d sets, want 0 — the sink path must retain nothing", len(res.Sets))
	}
	if res.RowsWritten != 3 {
		t.Errorf("RowsWritten = %d, want 3", res.RowsWritten)
	}
	if len(sink.begins) != 2 {
		t.Fatalf("BeginSet called %d times, want 2: %v", len(sink.begins), sink.begins)
	}
	if got := sink.begins[0][0]; got != "n" {
		t.Errorf("first set's column = %q, want %q", got, "n")
	}
	if len(sink.rows) != 3 || sink.rows[0][0] != "1" || sink.rows[2][0] != "alpha" {
		t.Errorf("sink rows = %v, want [[1] [2] [alpha]]", sink.rows)
	}
	if len(sink.endRows) != 2 || sink.endRows[0] != 2 || sink.endRows[1] != 1 {
		t.Errorf("EndSet counts = %v, want [2 1]", sink.endRows)
	}
	if !hasMessage(res, "(2 row(s) written)") || !hasMessage(res, "(1 row(s) written)") {
		t.Errorf("messages = %v, want a per-set row count for each set", messageTexts(res))
	}
	if hasMessage(res, "Commands completed successfully.") {
		t.Errorf("messages = %v, want no success notice when result sets were streamed", messageTexts(res))
	}
}

// An empty result set is still a result set: it must report "(0 row(s)
// written)" and must NOT also claim "Commands completed successfully.",
// which is what reading RowsWritten instead of sinkSets used to do.
func TestExecuteToSinkEmptySetIsStillASet(t *testing.T) {
	db := openFakeMsgDB([]fakeMsg{set([]string{"n"})})
	defer db.Close()

	sink := &recordingSink{}
	res := ExecuteToSink(context.Background(), db, "", "SELECT 1 WHERE 1=0", sink)

	if res.RowsWritten != 0 {
		t.Errorf("RowsWritten = %d, want 0", res.RowsWritten)
	}
	if len(sink.begins) != 1 || len(sink.endRows) != 1 || sink.endRows[0] != 0 {
		t.Errorf("sink saw begins=%v endRows=%v, want one set of zero rows", sink.begins, sink.endRows)
	}
	if !hasMessage(res, "(0 row(s) written)") {
		t.Errorf("messages = %v, want (0 row(s) written)", messageTexts(res))
	}
	if hasMessage(res, "Commands completed successfully.") {
		t.Errorf("messages = %v, want no success notice — an empty set is still a set", messageTexts(res))
	}
}

// A script that returns no result set at all is the case the success notice
// exists for.
func TestExecuteToSinkNoResultSetReportsSuccess(t *testing.T) {
	db := openFakeMsgDB([]fakeMsg{{kind: msgAffected, count: 4}})
	defer db.Close()

	sink := &recordingSink{}
	res := ExecuteToSink(context.Background(), db, "", "UPDATE t SET c = 1", sink)

	if len(sink.begins) != 0 {
		t.Errorf("BeginSet called %d times, want 0", len(sink.begins))
	}
	if !hasMessage(res, "(4 rows affected)") {
		t.Errorf("messages = %v, want (4 rows affected)", messageTexts(res))
	}
	if !hasMessage(res, "Commands completed successfully.") {
		t.Errorf("messages = %v, want the success notice when no result set happened", messageTexts(res))
	}
}

// Notices and server errors raised mid-batch reach Messages on the sink
// path exactly as they do on the buffering one, and an error suppresses the
// success notice.
func TestExecuteToSinkForwardsNoticesAndErrors(t *testing.T) {
	db := openFakeMsgDB([]fakeMsg{
		{kind: msgNotice, text: "Warning: null value eliminated."},
		{kind: msgError, err: errors.New("Invalid column name 'nope'.")},
	})
	defer db.Close()

	res := ExecuteToSink(context.Background(), db, "", "SELECT nope", &recordingSink{})

	if !hasMessage(res, "Warning: null value eliminated.") {
		t.Errorf("messages = %v, want the notice forwarded", messageTexts(res))
	}
	if !res.HasErrors() {
		t.Errorf("messages = %v, want the server error recorded as an error", messageTexts(res))
	}
	if hasMessage(res, "Commands completed successfully.") {
		t.Errorf("messages = %v, want no success notice after an error", messageTexts(res))
	}
}

// A sink that fails part-way abandons that set — and the batch's *next* set
// must still arrive, with the abandoned set closed out at the rows that did
// make it.
//
// What this does NOT pin is runBatch's drain loop. Whether an abandoned set
// has to be drained before the message loop can advance, and whether an
// extra Next() past an exhausted one makes the driver swallow the pending
// message, are both properties of go-mssqldb's TDS handling; a fake whose
// NextResultSet just moves an index behaves identically either way. Removing
// the drain still passes here. That gate stays a live-server check.
func TestExecuteToSinkRecoversAfterASinkFailure(t *testing.T) {
	db := openFakeMsgDB([]fakeMsg{
		set([]string{"n"}, []driver.Value{int64(1)}, []driver.Value{int64(2)}, []driver.Value{int64(3)}),
		set([]string{"s"}, []driver.Value{"after"}),
	})
	defer db.Close()

	sink := &firstSetFailSink{failOn: 2}
	res := ExecuteToSink(context.Background(), db, "", "SELECT 1", sink)

	if !res.HasErrors() {
		t.Errorf("messages = %v, want the sink failure reported", messageTexts(res))
	}
	if len(sink.begins) != 2 {
		t.Fatalf("BeginSet called %d times, want 2 — the second set must still be streamed: %v",
			len(sink.begins), messageTexts(res))
	}
	if len(sink.endRows) != 2 || sink.endRows[0] != 1 {
		t.Errorf("EndSet counts = %v, want the abandoned set closed at 1 row then the next set", sink.endRows)
	}
	if got := sink.rows[len(sink.rows)-1][0]; got != "after" {
		t.Errorf("last streamed cell = %q, want %q", got, "after")
	}
}

// Execute over the identical scripted stream is the control: it retains the
// rows in Sets and writes nothing to a sink.
func TestExecuteRetainsWhatExecuteToSinkStreams(t *testing.T) {
	db := openFakeMsgDB([]fakeMsg{set([]string{"n"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})})
	defer db.Close()

	res := Execute(context.Background(), db, "", "SELECT 1")

	if len(res.Sets) != 1 || len(res.Sets[0].Rows) != 2 {
		t.Fatalf("Execute produced %d sets, want 1 of 2 rows", len(res.Sets))
	}
	if res.RowsWritten != 0 {
		t.Errorf("RowsWritten = %d, want 0 on the buffering path", res.RowsWritten)
	}
	if res.Database != "testdb" {
		t.Errorf("Result.Database = %q, want %q", res.Database, "testdb")
	}
}
