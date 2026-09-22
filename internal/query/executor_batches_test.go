package query

import (
	"context"
	"slices"
	"testing"
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
