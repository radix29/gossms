package query

import (
	"fmt"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

func TestFormatValue(t *testing.T) {
	ts := time.Date(2024, 1, 5, 13, 45, 30, 123_000_000, time.UTC)
	tests := []struct {
		in            any
		isDecimalLike bool
		want          string
	}{
		{nil, false, "NULL"},
		{true, false, "1"},
		{false, false, "0"},
		{[]byte{0xDE, 0xAD}, false, "0xDEAD"},
		{[]byte("0.070312"), true, "0.070312"},
		{ts, false, "2024-01-05 13:45:30.123"},
		{"plain", false, "plain"},
		{int64(42), false, "42"},
		{3.14, false, "3.14"},
	}
	for _, tt := range tests {
		if got := formatValue(tt.in, tt.isDecimalLike); got != tt.want {
			t.Errorf("formatValue(%v, %v) = %q, want %q", tt.in, tt.isDecimalLike, got, tt.want)
		}
	}
}

func TestFormatGUID(t *testing.T) {
	g := mssql.NullUniqueIdentifier{
		Valid: true,
		UUID:  mssql.UniqueIdentifier{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10},
	}
	if got := formatGUID(g); got != "01020304-0506-0708-090A-0B0C0D0E0F10" {
		t.Errorf("formatGUID = %q, want dashed uppercase GUID", got)
	}
	if got := formatGUID(mssql.NullUniqueIdentifier{Valid: false}); got != "NULL" {
		t.Errorf("formatGUID(invalid) = %q, want NULL", got)
	}
}

func TestResultHelpers(t *testing.T) {
	r := &Result{
		Sets: []ResultSet{
			{Columns: []string{"a"}, Rows: [][]string{{"1"}, {"2"}}},
			{Columns: []string{"b"}, Rows: [][]string{{"3"}}},
		},
		Messages: []Message{{Text: "(2 rows affected)"}},
	}
	if got := r.TotalRows(); got != 3 {
		t.Errorf("TotalRows = %d, want 3", got)
	}
	if r.HasErrors() {
		t.Errorf("HasErrors = true, want false")
	}
	r.addError(errFake("boom"))
	if !r.HasErrors() {
		t.Errorf("HasErrors after addError = false, want true")
	}
}

// TestAddErrorSQLServer verifies a driver SQL error is split into the SSMS
// "Msg …" status line and the message text, as two separate error messages.
func TestAddErrorSQLServer(t *testing.T) {
	r := &Result{}
	// Wrapped, to prove the unwrap path works end to end.
	r.addError(fmt.Errorf("run batch: %w", mssql.Error{
		Number:  208,
		State:   1,
		Class:   16,
		LineNo:  4,
		Message: "Invalid object name 'foo'.",
	}))

	if len(r.Messages) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(r.Messages), r.Messages)
	}
	if want := "Msg 208, Level 16, State 1, Line 4"; r.Messages[0].Text != want {
		t.Errorf("header = %q, want %q", r.Messages[0].Text, want)
	}
	if r.Messages[1].Text != "Invalid object name 'foo'." {
		t.Errorf("message = %q", r.Messages[1].Text)
	}
	for i, m := range r.Messages {
		if !m.IsError {
			t.Errorf("Messages[%d].IsError = false, want true", i)
		}
	}
}

// TestAddErrorNonSQL keeps a plain Go error as a single message.
func TestAddErrorNonSQL(t *testing.T) {
	r := &Result{}
	r.addError(errFake("boom"))
	if len(r.Messages) != 1 || r.Messages[0].Text != "boom" {
		t.Fatalf("messages = %+v, want single 'boom'", r.Messages)
	}
}

// TestErrorMessagesSQLServer mirrors TestAddErrorSQLServer but calls
// ErrorMessages directly — the entry point QueryPanel's execution-plan
// paths use, since they capture errors from gosmo calls outside Execute.
func TestErrorMessagesSQLServer(t *testing.T) {
	msgs := ErrorMessages(fmt.Errorf("capture execution plan: %w", mssql.Error{
		Number:  208,
		State:   1,
		Class:   16,
		LineNo:  4,
		Message: "Invalid object name 'foo'.",
	}))

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(msgs), msgs)
	}
	if want := "Msg 208, Level 16, State 1, Line 4"; msgs[0].Text != want {
		t.Errorf("header = %q, want %q", msgs[0].Text, want)
	}
	if msgs[1].Text != "Invalid object name 'foo'." {
		t.Errorf("message = %q", msgs[1].Text)
	}
	for i, m := range msgs {
		if !m.IsError {
			t.Errorf("msgs[%d].IsError = false, want true", i)
		}
	}
}

// TestErrorMessagesNonSQL keeps a plain Go error as a single message.
func TestErrorMessagesNonSQL(t *testing.T) {
	msgs := ErrorMessages(errFake("boom"))
	if len(msgs) != 1 || msgs[0].Text != "boom" || !msgs[0].IsError {
		t.Fatalf("msgs = %+v, want single IsError 'boom'", msgs)
	}
}

// TestIsShowplanResultSet checks the column-name match ExecuteWithPlan
// relies on to separate a captured execution plan (SET STATISTICS XML ON's
// extra result set) from a query's own real result sets.
func TestIsShowplanResultSet(t *testing.T) {
	tests := []struct {
		name string
		cols []string
		want bool
	}{
		{"showplan column alone", []string{showplanColumnName}, true},
		{"real single column, different name", []string{"DoctorID"}, false},
		{"showplan name alongside another column", []string{showplanColumnName, "Extra"}, false},
		{"no columns", nil, false},
	}
	for _, tt := range tests {
		if got := isShowplanResultSet(tt.cols); got != tt.want {
			t.Errorf("%s: isShowplanResultSet(%v) = %v, want %v", tt.name, tt.cols, got, tt.want)
		}
	}
}

// TestAddNotice confirms a notice lands as a non-error Message, unlike
// addError — HasErrors must stay false after only notices are added.
func TestAddNotice(t *testing.T) {
	r := &Result{}
	r.addNotice("(1 row affected)")
	if len(r.Messages) != 1 || r.Messages[0].Text != "(1 row affected)" {
		t.Fatalf("messages = %+v, want single '(1 row affected)'", r.Messages)
	}
	if r.Messages[0].IsError {
		t.Error("addNotice message must not be IsError")
	}
	if r.HasErrors() {
		t.Error("HasErrors = true after only a notice, want false")
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
