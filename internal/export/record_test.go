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

// formulaSet has a cell of every formula-starting character, in a text column
// and in numeric ones, with a formula-looking column name.
var formulaSet = fixtureSet{[]string{"=cmd", "n", "d", "plain"}, []row{
	{[]string{"=1+1", "-5", "-1.50", "+x"}, nil},
	{[]string{"@SUM(A1)", "+7", "-0.5", "-y"}, nil},
	{[]string{"\tlead", "1", "2", "\rcr"}, nil},
	{[]string{"NULL", "", "x=1", "'already"}, []bool{true, false, false, false}},
}}

var formulaTypes = []string{"nvarchar(50)", "int", "decimal(9,2)", "varchar(max)"}

func writeFormulas(sep rune, neutralize bool) string {
	var b bytes.Buffer
	rw := NewRecordWriter(&b, sep)
	rw.NeutralizeFormulas = neutralize
	rw.BeginSet(formulaSet.cols)
	rw.SetColumnTypes(formulaTypes)
	for _, r := range formulaSet.rows {
		rw.WriteRecord(r.cells, r.null)
	}
	rw.Flush()
	return b.String()
}

// With NeutralizeFormulas a text cell (header included) starting with =, +, -,
// @, tab or CR gets a leading ', in both separators; numeric columns, NULL, ""
// and a cell with the character later on are left alone. "-1.50" in the
// decimal column stays a number although JSON treats decimal as a string.
func TestNeutralizeFormulas(t *testing.T) {
	cases := map[rune]string{
		CSV: "'=cmd,n,d,plain\n" +
			"'=1+1,-5,-1.50,'+x\n" +
			"'@SUM(A1),+7,-0.5,'-y\n" +
			"'\tlead,1,2,\"'\rcr\"\n" +
			",\"\",x=1,'already\n",
		TSV: "'=cmd\tn\td\tplain\n" +
			"'=1+1\t-5\t-1.50\t'+x\n" +
			"'@SUM(A1)\t+7\t-0.5\t'-y\n" +
			"\"'\tlead\"\t1\t2\t\"'\rcr\"\n" +
			"\t\"\"\tx=1\t'already\n",
	}
	for sep, want := range cases {
		if got := writeFormulas(sep, true); got != want {
			t.Errorf("sep %q\n got %q\nwant %q", sep, got, want)
		}
	}
}

// Off (the default) the writer stays RecordReader's lossless inverse: every
// formula-looking cell reads back as written.
func TestNeutralizeFormulasOffRoundTrips(t *testing.T) {
	for _, sep := range []rune{CSV, TSV} {
		recs, nulls := readAll(t, writeFormulas(sep, false), sep)
		if !slices.Equal(recs[0], formulaSet.cols) {
			t.Errorf("sep %q header = %q", sep, recs[0])
		}
		for i, r := range formulaSet.rows {
			want := slices.Clone(r.cells)
			for j := range want {
				if j < len(r.null) && r.null[j] {
					want[j] = ""
				}
			}
			if got := recs[i+1]; !slices.Equal(got, want) {
				t.Errorf("sep %q row %d = %q, want %q", sep, i+1, got, want)
			}
			if wantNull := len(r.null) > 0 && r.null[0]; nulls[i+1][0] != wantNull {
				t.Errorf("sep %q row %d null = %v", sep, i+1, nulls[i+1])
			}
		}
	}
}

// A new set forgets the last one's types: its column 1 is text, so -5 there is
// neutralized.
func TestNeutralizeFormulasTypesPerSet(t *testing.T) {
	var b bytes.Buffer
	rw := NewRecordWriter(&b, CSV)
	rw.NeutralizeFormulas = true
	rw.BeginSet([]string{"a", "b"})
	rw.SetColumnTypes([]string{"int", "int"})
	rw.WriteRecord([]string{"-1", "-2"}, nil)
	rw.BeginHeaderlessSet()
	rw.WriteRecord([]string{"-1", "-2"}, nil)
	rw.Flush()
	if got, want := b.String(), "a,b\n-1,-2\n\n'-1,'-2\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteTableNeutralizesCSVOnly(t *testing.T) {
	tbl := tableOf{rows: [][]string{{"=1+1", "-5"}}}
	cols, types := []string{"s", "n"}, []string{"nvarchar(10)", "bigint"}
	var b bytes.Buffer
	if err := WriteTable(&b, FormatCSV, cols, types, tbl, true); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "s,n\n'=1+1,-5\n"; got != want {
		t.Errorf("csv got %q, want %q", got, want)
	}
	b.Reset()
	if err := WriteTable(&b, FormatJSON, cols, types, tbl, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "'") {
		t.Errorf("json neutralized: %s", b.String())
	}
}
