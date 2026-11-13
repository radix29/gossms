package sqltext

import (
	"math"
	"testing"
)

// goSeparatorLineCases is the definition of a "GO" batch separator. The editor,
// IntelliSense and the executor all call GoSeparatorAt, so this is the one copy.
//
// go-mssqldb's batch.Split, which the executor used before, disagreed with it
// in both directions (measured against v1.11.0): it ran "GO -- step 2" as a
// repeat count of 2, ran "GO 2" as 1 when it was the script's last line with
// no newline, and split on "GO;", "GO x", "GO_" and "GO/*c*/", leaving the
// junk at the head of the next batch for the server to reject.
var goSeparatorLineCases = []struct {
	line  string
	want  bool
	count int
}{
	{"GO", true, 1},
	{"go", true, 1},
	{"Go", true, 1},
	{"gO", true, 1},
	{" GO ", true, 1},
	{"\tGO\t", true, 1},
	{"  go  ", true, 1},
	{"GO\r", true, 1},
	{"GO 5", true, 5},
	{"GO 5 ", true, 5},
	{"GO\t10", true, 10},
	{"GO 0", true, 0},
	{"GO 007", true, 7},
	{"GO 1000000", true, 1000000},
	{"GO 99999999999999999999999", true, math.MaxInt},
	{"GO -- comment", true, 1},
	{"GO--x", true, 1},
	{"GO -- 5 items", true, 1},
	{"GO -- step 2", true, 1},
	{"GO -- v2", true, 1},
	{"GO 5 -- twice", true, 5},
	{"GO 5-- twice", true, 5},
	{"", false, 0},
	{" ", false, 0},
	{"\t", false, 0},
	{"G", false, 0},
	{"O", false, 0},
	{"G O", false, 0},
	{"GOO", false, 0},
	{"GOTO", false, 0},
	{"gone", false, 0},
	{"XGO", false, 0},
	{"SELECT 1", false, 0},
	{"GO5", false, 0},
	{"GO_", false, 0},
	{"GO x", false, 0},
	{"GO 5x", false, 0},
	{"GO 5-x", false, 0},
	{"GO 1 2", false, 0},
	{"GO ٥", false, 0}, // ARABIC-INDIC DIGIT FIVE: a digit, but not a count
	{"GO;", false, 0},
	{";GO", false, 0},
	{"GO/*c*/", false, 0},
	{"ＧＯ", false, 0},
}

func TestGoSeparatorLineCases(t *testing.T) {
	for _, tt := range goSeparatorLineCases {
		if got := IsGoSeparatorLine([]rune(tt.line)); got != tt.want {
			t.Errorf("IsGoSeparatorLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
		_, count, ok := GoSeparatorAt([]rune(tt.line), 0, len([]rune(tt.line)))
		if ok && count != tt.count {
			t.Errorf("GoSeparatorAt(%q) count = %d, want %d", tt.line, count, tt.count)
		}
	}
}

// Inside a buffer the line ends at '\n', not at limit, and next is the line
// after it.
func TestGoSeparatorAtStopsAtNewline(t *testing.T) {
	buf := []rune("select 1\n  GO 3\nselect 2")
	next, count, ok := GoSeparatorAt(buf, 9, len(buf))
	if !ok || count != 3 || next != 16 {
		t.Fatalf("GoSeparatorAt = (%d, %d, %v), want (16, 3, true)", next, count, ok)
	}
	if _, _, ok := GoSeparatorAt(buf, 0, len(buf)); ok {
		t.Fatal(`"select 1" line read as a separator`)
	}
	// A blank line is not a separator, and the scan must not run on into the
	// next line's "GO".
	buf = []rune("\nGO")
	if _, _, ok := GoSeparatorAt(buf, 0, len(buf)); ok {
		t.Fatal("blank line followed by GO read as a separator")
	}
}
