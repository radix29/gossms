package tui

import (
	"context"
	"testing"

	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/controls/editor"
)

// TestResultColumnAlignsRightAlignsNumbersOnly: every numeric SQL Server type
// right-aligns in the results grid; bit (left in SSMS), text, dates and binary
// stay left.
func TestResultColumnAlignsRightAlignsNumbersOnly(t *testing.T) {
	set := query.ResultSet{ColumnTypes: []string{
		"int", "bigint", "smallint", "tinyint", "decimal(10,2)", "numeric(5,0)",
		"money", "smallmoney", "float", "real",
		"bit", "nvarchar(50)", "datetime2(7)", "uniqueidentifier", "varbinary(max)",
	}}
	got := resultColumnAligns(set)
	for i, typ := range set.ColumnTypes {
		want := controls.AlignLeft
		if i < 10 {
			want = controls.AlignRight
		}
		if got[i] != want {
			t.Errorf("%s: align = %v, want %v", typ, got[i], want)
		}
	}
}

// TestResultsAsTextRightAlignsNumericColumns: Results to Text pads a numeric
// column's values on the left and a text column's on the right; headers and
// the dashed separator stay left.
func TestResultsAsTextRightAlignsNumericColumns(t *testing.T) {
	set := query.ResultSet{
		Columns:     []string{"amount", "name"},
		ColumnTypes: []string{"decimal(10,2)", "nvarchar(20)"},
		Rows:        [][]string{{"1.50", "a"}, {"1234.00", "bcd"}},
	}
	lines, err := formatResultsAsText(context.Background(), set, 256, 4)
	if err != nil {
		t.Fatal(err)
	}
	e := editor.NewEditor(nil)
	e.SetLineBuffer(lines)
	want := "amount  name\n------- ----\n   1.50 a   \n1234.00 bcd "
	if got := e.Text(); got != want {
		t.Errorf("text =\n%q\nwant\n%q", got, want)
	}
}
