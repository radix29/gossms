package query

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
)

// The script reaches the server split by the editor's GO rule, each batch sent
// as many times as its count asks. go-mssqldb's batch.Split, used here before,
// sent "select 1" twice for the first script (it read the 2 in the comment as a
// count) and once for the second (it dropped the count of a GO on the script's
// last line when no newline followed).
func TestExecuteRunsGoBatchesByTheEditorsRule(t *testing.T) {
	tests := []struct {
		script string
		want   []string
		notice string
	}{
		{"select 1\nGO -- step 2\nselect 3", []string{"select 1\n", "select 3"}, ""},
		{"select 1\nGO 2", []string{"select 1\n", "select 1\n"}, "Batch execution completed 2 times."},
		{"select 1\nGO;\nselect 2", []string{"select 1\nGO;\nselect 2"}, ""},
		{"select 1\nGO/*x*/\nselect 2", []string{"select 1\nGO/*x*/\nselect 2"}, ""},
		{"select 1\nGO 0\nselect 2", []string{"select 2"}, ""},
	}
	for _, tt := range tests {
		batches := make([][]fakeMsg, 4)
		s, fc, _ := openFakeSession(t, batches...)
		res := s.Execute(context.Background(), tt.script)
		if got := fc.conn.batchTexts; !slices.Equal(got, tt.want) {
			t.Errorf("script %q sent batches %q, want %q", tt.script, got, tt.want)
		}
		if tt.notice != "" && !hasMessage(res, tt.notice) {
			t.Errorf("script %q: messages %q, want %q among them", tt.script, messageTexts(res), tt.notice)
		}
		s.Close()
	}
}

// divideByZero is the error SQL Server raises for select 1/0 on the first line
// of its batch: LineNo counts from the batch, not the script.
func divideByZero(proc string, line int32) fakeMsg {
	return fakeMsg{kind: msgError, err: mssql.Error{
		Number: 8134, Class: 16, State: 1, LineNo: line, ProcName: proc,
		Message: "Divide by zero error encountered.",
	}}
}

// An error's "Line N" is the script's line, as SSMS shows it: the batch's own
// line plus the lines before the batch, plus the selection's start for an
// executed selection. An error inside a procedure keeps the procedure's line.
func TestErrorLineCountsFromTheScript(t *testing.T) {
	script := "print 'a';\nselect 1\nGO\nselect 1/0;\nGO\n\nexec p\nGO 2\nselect 1/0"
	tests := []struct {
		name   string
		offset int
		want   []string
		lines  []int
	}{
		{"whole script", 0,
			[]string{"Msg 8134, Level 16, State 1, Line 4", "Msg 8134, Level 16, State 1, Procedure p, Line 3",
				"Msg 8134, Level 16, State 1, Procedure p, Line 3", "Msg 8134, Level 16, State 1, Line 9"},
			[]int{4, 0, 0, 9}},
		{"selection from editor line 11", 10,
			[]string{"Msg 8134, Level 16, State 1, Line 14", "Msg 8134, Level 16, State 1, Procedure p, Line 3",
				"Msg 8134, Level 16, State 1, Procedure p, Line 3", "Msg 8134, Level 16, State 1, Line 19"},
			[]int{14, 0, 0, 19}},
	}
	for _, tt := range tests {
		s, _, _ := openFakeSession(t,
			nil,
			[]fakeMsg{divideByZero("", 1)},
			[]fakeMsg{divideByZero("p", 3)}, // GO 2: the batch runs twice
			[]fakeMsg{divideByZero("p", 3)},
			[]fakeMsg{divideByZero("", 1)},
		)
		res := s.Execute(context.Background(), script, WithLineOffset(tt.offset))
		var headers []string
		var lines []int
		for _, m := range res.Messages {
			if strings.HasPrefix(m.Text, "Msg ") {
				headers = append(headers, m.Text)
				lines = append(lines, m.Line)
			} else if m.IsError && m.Line != lines[len(lines)-1] {
				t.Errorf("%s: message %q has Line %d, its header %d", tt.name, m.Text, m.Line, lines[len(lines)-1])
			}
		}
		if !slices.Equal(headers, tt.want) {
			t.Errorf("%s: headers\n got %q\nwant %q", tt.name, headers, tt.want)
		}
		if !slices.Equal(lines, tt.lines) {
			t.Errorf("%s: Message.Line = %v, want %v", tt.name, lines, tt.lines)
		}
		s.Close()
	}
}

// ErrorMessages has no script to count in: the server's line, and no Line to
// jump to.
func TestErrorMessagesKeepsTheServersLine(t *testing.T) {
	msgs := ErrorMessages(divideByZero("", 2).err)
	if msgs[0].Text != "Msg 8134, Level 16, State 1, Line 2" || msgs[0].Line != 0 {
		t.Errorf("ErrorMessages = %+v, want the server's Line 2 and no Line", msgs)
	}
}

// goRepeatedBatches scripts n executions of a batch printing "(1 row
// affected)", the middle one failing: "GO n" over an INSERT.
func goRepeatedBatches(n int) [][]fakeMsg {
	batches := make([][]fakeMsg, n)
	for i := range batches {
		batches[i] = []fakeMsg{{kind: msgAffected, count: 1}}
	}
	batches[n/2] = []fakeMsg{divideByZero("", 1)}
	return batches
}

// A GO n run counts each execution's errors from that execution's messages
// alone, so FailedBatches is exact and the run stays linear in n (it rescanned
// every message so far per execution: GO 100000 was ~10¹⁰ comparisons).
func TestGoRepeatCountsFailedExecutions(t *testing.T) {
	const n = 5000
	s, _, _ := openFakeSession(t, goRepeatedBatches(n)...)
	defer s.Close()
	res := s.Execute(context.Background(), "insert t default values\nGO 5000")
	if res.Batches != n || res.FailedBatches != 1 {
		t.Errorf("Batches %d, FailedBatches %d; want %d, 1", res.Batches, res.FailedBatches, n)
	}
	if !res.HasErrors() {
		t.Error("HasErrors = false, want true")
	}
}

// BenchmarkGoRepeat scales linearly with n; compare ns/op across the sizes.
func BenchmarkGoRepeat(b *testing.B) {
	for _, n := range []int{2000, 8000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			script := "insert t default values\nGO " + strconv.Itoa(n)
			for b.Loop() {
				b.StopTimer()
				s, _, _ := openFakeSession(b, goRepeatedBatches(n)...)
				b.StartTimer()
				if res := s.Execute(context.Background(), script); res.Batches != n {
					b.Fatalf("Batches %d, want %d", res.Batches, n)
				}
				b.StopTimer()
				s.Close()
				b.StartTimer()
			}
		})
	}
}
