package query

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
)

// The structured halves of messages (Message.SQL, Message.RowsAffected) and the
// options the command line runs with: what the TUI reads as text, the CLI reads
// as values.

// A SQL error's header line carries the error as a value, its line counted from
// the script as Message.Line is; the text line after it carries none.
func TestBatchErrorCarriesTheSQLError(t *testing.T) {
	s, _, _ := openFakeSession(t, nil, []fakeMsg{{kind: msgError, err: mssql.Error{
		Number: 208, Class: 16, State: 1, LineNo: 2, ProcName: "",
		Message: "Invalid object name 'nope'.",
	}}})
	defer s.Close()

	res := s.Execute(context.Background(), "select 1\nGO\n\nselect * from nope")
	var withSQL []Message
	for _, m := range res.Messages {
		if m.SQL != nil {
			withSQL = append(withSQL, m)
		}
	}
	if len(withSQL) != 1 {
		t.Fatalf("messages carrying SQL = %d, want exactly the header: %+v", len(withSQL), res.Messages)
	}
	m := withSQL[0]
	if m.Text != "Msg 208, Level 16, State 1, Line 4" {
		t.Errorf("SQL on %q, want the header line", m.Text)
	}
	if se := m.SQL; se.Number != 208 || se.Class != 16 || se.State != 1 || se.LineNo != 4 || se.Message != "Invalid object name 'nope'." {
		t.Errorf("SQL = %+v, want 208/16/1, script line 4 and the text", *se)
	}
}

// A procedure's error keeps the module's line in SQL.LineNo, as in its header.
func TestModuleErrorKeepsTheModulesLine(t *testing.T) {
	s, _, _ := openFakeSession(t, nil, []fakeMsg{divideByZero("p", 3)})
	defer s.Close()

	res := s.Execute(context.Background(), "select 1\nGO\nexec p")
	for _, m := range res.Messages {
		if m.SQL != nil && (m.SQL.LineNo != 3 || m.SQL.ProcName != "p" || m.Line != 0) {
			t.Errorf("module error SQL = %+v, Line %d; want the module's line 3 and no script line", *m.SQL, m.Line)
		}
	}
}

// A count closing a statement that returned rows is AfterSet; one with no set
// before it (an UPDATE) is not, even straight after a set's statement.
func TestRowsAffectedSaysWhetherASetPrecededIt(t *testing.T) {
	selected := set([]string{"n"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})
	selected.counted, selected.count = true, 2
	s, _, _ := openFakeSession(t, []fakeMsg{
		selected,
		{kind: msgDone, count: 5},
		{kind: msgDone, count: 1},
	})
	defer s.Close()

	res := s.Execute(context.Background(), "SELECT n FROM t; UPDATE t SET n = 1; DELETE t WHERE n = 3")
	var got []RowCount
	var texts []string
	for _, m := range res.Messages {
		if m.RowsAffected != nil {
			got = append(got, *m.RowsAffected)
			texts = append(texts, m.Text)
		}
	}
	want := []RowCount{{N: 2, AfterSet: true}, {N: 5}, {N: 1}}
	if !slices.Equal(got, want) {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
	if !slices.Equal(texts, []string{"(2 rows affected)", "(5 rows affected)", "(1 row affected)"}) {
		t.Errorf("count texts = %q — the TUI's wording must not change", texts)
	}
}

// typedSink records BeginTypedSet; BeginSet must not be called on it.
type typedSink struct {
	recordingSink
	types    [][]string
	nullable [][]bool
	plain    int
}

func (s *typedSink) BeginSet(cols []string) error {
	s.plain++
	return s.recordingSink.BeginSet(cols)
}

func (s *typedSink) BeginTypedSet(cols, types []string, nullable []bool) error {
	s.types = append(s.types, slices.Clone(types))
	s.nullable = append(s.nullable, slices.Clone(nullable))
	return s.recordingSink.BeginSet(cols)
}

// A TypedRowSink gets each set's declared types and nullability; a column the
// driver can't describe reads as nullable.
func TestTypedRowSinkGetsTypesAndNullability(t *testing.T) {
	described := set([]string{"id", "name"}, []driver.Value{int64(1), "a"})
	described.types, described.nullable = []string{"INT", "NVARCHAR"}, []bool{false, true}
	undescribed := set([]string{"x"}, []driver.Value{"b"})
	s, _, _ := openFakeSession(t, []fakeMsg{described, undescribed})
	defer s.Close()

	sink := &typedSink{}
	s.ExecuteToSink(context.Background(), "SELECT 1", sink)

	if sink.plain != 0 {
		t.Errorf("BeginSet called %d times on a TypedRowSink, want 0", sink.plain)
	}
	if len(sink.types) != 2 || !slices.Equal(sink.types[0], []string{"int", "nvarchar"}) {
		t.Errorf("types = %q, want [[int nvarchar] …]", sink.types)
	}
	if len(sink.nullable) != 2 || !slices.Equal(sink.nullable[0], []bool{false, true}) || !slices.Equal(sink.nullable[1], []bool{true}) {
		t.Errorf("nullable = %v, want [[false true] [true]]", sink.nullable)
	}
	if len(sink.rows) != 2 {
		t.Errorf("rows = %v, want both sets' rows still streamed", sink.rows)
	}
}

// WithStopOnError sends no batch after the first that failed, GO n repetitions
// included; without it every batch runs.
func TestStopOnErrorEndsTheRunAfterTheFailingBatch(t *testing.T) {
	script := "select 1\nGO\nselect 1/0\nGO 3\nselect 2"
	failing := []fakeMsg{divideByZero("", 1)}

	s, fc, _ := openFakeSession(t, nil, failing, failing, failing, nil)
	res := s.Execute(context.Background(), script, WithStopOnError())
	if got := len(fc.conn.batchTexts); got != 2 {
		t.Errorf("WithStopOnError sent %d batches, want 2 (the failing one, then nothing)", got)
	}
	if !res.StoppedOnError {
		t.Error("StoppedOnError = false, want true")
	}
	s.Close()

	s, fc, _ = openFakeSession(t, nil, failing, failing, failing, nil)
	res = s.Execute(context.Background(), script)
	if got := len(fc.conn.batchTexts); got != 5 {
		t.Errorf("without the option %d batches ran, want 5", got)
	}
	if res.StoppedOnError {
		t.Error("StoppedOnError = true without the option")
	}
	s.Close()

	// A clean run is not stopped.
	s, fc, _ = openFakeSession(t, nil, nil)
	res = s.Execute(context.Background(), "select 1\nGO\nselect 2", WithStopOnError())
	if len(fc.conn.batchTexts) != 2 || res.StoppedOnError {
		t.Errorf("clean run: %d batches, StoppedOnError %v; want 2, false", len(fc.conn.batchTexts), res.StoppedOnError)
	}
	s.Close()
}

// WithoutSinkNotices drops the sink path's own wording, keeping the server's
// messages and the counters.
func TestWithoutSinkNoticesKeepsTheServersMessages(t *testing.T) {
	selected := set([]string{"n"}, []driver.Value{int64(1)}, []driver.Value{int64(2)}, []driver.Value{int64(3)})
	s, _, _ := openFakeSession(t, []fakeMsg{
		{kind: msgNotice, text: "hello"},
		selected,
	})
	defer s.Close()

	res := s.ExecuteToSink(context.Background(), "SELECT 1", &recordingSink{}, WithoutSinkNotices(), WithMaxRows(2))
	if got := messageTexts(res); !slices.Equal(got, []string{"hello"}) {
		t.Errorf("messages = %q, want only the server's", got)
	}
	if res.RowsWritten != 2 || res.SinkSetsTruncated != 1 {
		t.Errorf("RowsWritten %d, SinkSetsTruncated %d; want 2, 1", res.RowsWritten, res.SinkSetsTruncated)
	}
}
