package export

import (
	"bytes"
	"encoding/csv"
	"os"
	"slices"
	"strings"
	"testing"
)

type row struct {
	cells []string
	null  []bool
}

type fixtureSet struct {
	cols []string
	rows []row
}

// edgeCases is the fixture testdata/results_to_file.csv was written from by the
// TUI's csvSink before the writer moved here: NULL against "" against 'NULL',
// \., a leading space or tab, quotes, line breaks, a separator in a field, a
// leading no-break space, an isNull shorter than the row, a lone NULL column, an
// empty set, and a set after it.
var edgeCases = []fixtureSet{
	{[]string{"id", "text", "note"}, []row{
		{[]string{"1", "NULL", "x"}, []bool{false, true, false}},
		{[]string{"2", "", "y"}, nil},
		{[]string{"3", "NULL", "'NULL' string"}, nil},
		{[]string{"4", `\.`, ""}, []bool{false, false, false}},
		{[]string{"5", " lead", "a,b"}, nil},
		{[]string{"6", `q"uote`, "line\nbreak"}, nil},
		{[]string{"7", "cr\rx", "tab\tx"}, nil},
		{[]string{"8", "é ünï 日本", " nbsp"}, nil},
		{[]string{"9", "z", "w"}, []bool{true}},
		{[]string{"10", "\tlead tab", `\.x`}, nil},
	}},
	{[]string{"only"}, []row{
		{[]string{"NULL"}, []bool{true}},
		{[]string{""}, nil},
		{[]string{"v"}, nil},
	}},
	{[]string{"a b", "c,d", ` "q"`}, nil},
	{[]string{"after empty"}, []row{{[]string{"x"}, nil}}},
}

func write(sep rune, sets []fixtureSet) string {
	var b bytes.Buffer
	rw := NewRecordWriter(&b, sep)
	for _, s := range sets {
		rw.BeginSet(s.cols)
		for _, r := range s.rows {
			rw.WriteRecord(r.cells, r.null)
		}
	}
	rw.Flush()
	return b.String()
}

// The extraction is byte-for-byte: the golden is the pre-extraction csvSink's
// output for the same rows.
func TestCSVMatchesTheResultsToFileGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/results_to_file.csv")
	if err != nil {
		t.Fatal(err)
	}
	if got := write(CSV, edgeCases); got != string(want) {
		t.Errorf("CSV output differs from the pre-extraction golden\n got %q\nwant %q", got, want)
	}
}

// TSV quotes where CSV does, with the tab as the separator: a tab in a field is
// quoted, a comma is not.
func TestTSVQuotesTabsNotCommas(t *testing.T) {
	got := write(TSV, []fixtureSet{{[]string{"a", "b"}, []row{
		{[]string{"x,y", "t\tz"}, nil},
		{[]string{"NULL", ""}, []bool{true}},
		{[]string{"NULL", "line\nbreak"}, nil},
	}}})
	want := "a\tb\nx,y\t\"t\tz\"\n\t\"\"\nNULL\t\"line\nbreak\"\n"
	if got != want {
		t.Errorf("TSV\n got %q\nwant %q", got, want)
	}
}

// A standard reader gets every value back, NULL and "" apart by quoting: the
// format is lossless (but for the one-column NULL row) in both separators.
func TestRecordsRoundTripThroughEncodingCSV(t *testing.T) {
	for _, sep := range []rune{CSV, TSV} {
		set := edgeCases[0]
		r := csv.NewReader(strings.NewReader(write(sep, []fixtureSet{set})))
		r.Comma = sep
		r.FieldsPerRecord = -1
		recs, err := r.ReadAll()
		if err != nil {
			t.Fatalf("sep %q: %v", sep, err)
		}
		if !slices.Equal(recs[0], set.cols) {
			t.Errorf("sep %q header = %q", sep, recs[0])
		}
		for i, row := range set.rows {
			want := slices.Clone(row.cells)
			for j := range want {
				if j < len(row.null) && row.null[j] {
					want[j] = ""
				}
			}
			if got := recs[i+1]; !slices.Equal(got, want) {
				t.Errorf("sep %q row %d = %q, want %q", sep, i+1, got, want)
			}
		}
	}
}
