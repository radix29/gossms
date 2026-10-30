package export

import (
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

// readAll reads every record from s, failing the test on an error.
func readAll(t *testing.T, s string, sep rune) (cells [][]string, nulls [][]bool) {
	t.Helper()
	rr := NewRecordReader(strings.NewReader(s), sep)
	for {
		c, n, err := rr.Read()
		if err == io.EOF {
			return cells, nulls
		}
		if err != nil {
			t.Fatalf("Read: %v (input %q)", err, s)
		}
		cells = append(cells, c)
		nulls = append(nulls, n)
	}
}

// expectRecords lays sets out as RecordReader should return them: a header,
// its rows with a NULL's cell "", a record of no fields between sets, and a
// lone NULL column read back as an empty string (the format's one loss).
func expectRecords(sets []fixtureSet) (cells [][]string, nulls [][]bool) {
	for i, s := range sets {
		if i > 0 {
			cells, nulls = append(cells, []string{}), append(nulls, []bool{})
		}
		cells, nulls = append(cells, s.cols), append(nulls, make([]bool, len(s.cols)))
		for _, r := range s.rows {
			c := slices.Clone(r.cells)
			n := make([]bool, len(c))
			for j := range c {
				if j < len(r.null) && r.null[j] {
					c[j] = ""
					n[j] = len(c) > 1
				}
			}
			cells, nulls = append(cells, c), append(nulls, n)
		}
	}
	return cells, nulls
}

func checkRoundTrip(t *testing.T, sep rune, sets []fixtureSet) {
	t.Helper()
	text := write(sep, sets)
	gotC, gotN := readAll(t, text, sep)
	wantC, wantN := expectRecords(sets)
	if len(gotC) != len(wantC) {
		t.Fatalf("sep %q: %d records, want %d\nfile %q\ngot %q", sep, len(gotC), len(wantC), text, gotC)
	}
	for i := range wantC {
		if !slices.Equal(gotC[i], wantC[i]) || !slices.Equal(gotN[i], wantN[i]) {
			t.Errorf("sep %q record %d = %q null %v, want %q null %v\nfile %q",
				sep, i, gotC[i], gotN[i], wantC[i], wantN[i], text)
		}
	}
}

// TestRecordReaderInvertsWriterOnEdgeCases reads back the TUI's Results To
// File fixture: every NULL / "" / 'NULL' distinction and every quoting case
// survives, and only the lone NULL column comes back as "".
func TestRecordReaderInvertsWriterOnEdgeCases(t *testing.T) {
	for _, sep := range []rune{CSV, TSV} {
		checkRoundTrip(t, sep, edgeCases)
	}
}

// TestRecordReaderReadsCommittedFixture reads the committed bytes, not this
// package's writer: a writer and reader that drifted together would still
// agree with each other.
func TestRecordReaderReadsCommittedFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/results_to_file.csv")
	if err != nil {
		t.Fatal(err)
	}
	gotC, gotN := readAll(t, string(b), CSV)
	wantC, wantN := expectRecords(edgeCases)
	if !slices.EqualFunc(gotC, wantC, slices.Equal) || !slices.EqualFunc(gotN, wantN, slices.Equal) {
		t.Errorf("fixture read as\n%q %v\nwant\n%q %v", gotC, gotN, wantC, wantN)
	}
}

// fieldAlphabet is what random fields are built from: every character the
// writer treats specially, the strings it does, and plain text.
var fieldAlphabet = []string{
	`"`, ",", "\t", "\r", "\n", "\r\n", " ", "NULL", `\.`, `\`, ".", "a", "Z9",
	"é", "日本", "\uFEFF", "\u00a0", "''", `""`,
}

func randomSets(r *rand.Rand) []fixtureSet {
	field := func() string {
		var b strings.Builder
		for range r.IntN(5) {
			b.WriteString(fieldAlphabet[r.IntN(len(fieldAlphabet))])
		}
		return b.String()
	}
	sets := make([]fixtureSet, 1+r.IntN(3))
	for i := range sets {
		ncol := 1 + r.IntN(4)
		cols := make([]string, ncol)
		for j := range cols {
			cols[j] = field()
		}
		rows := make([]row, r.IntN(6))
		for k := range rows {
			cells := make([]string, ncol)
			null := make([]bool, ncol)
			for j := range cells {
				if r.IntN(4) == 0 {
					null[j] = true
				} else {
					cells[j] = field()
				}
			}
			rows[k] = row{cells, null}
		}
		sets[i] = fixtureSet{cols, rows}
	}
	return sets
}

// TestRecordReaderInvertsWriterProperty: for random sets of random fields,
// writing then reading is the identity, except a lone NULL column reads as "".
func TestRecordReaderInvertsWriterProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(6, 9))
	for range 5000 {
		sets := randomSets(r)
		for _, sep := range []rune{CSV, TSV} {
			checkRoundTrip(t, sep, sets)
		}
		if t.Failed() {
			return
		}
	}
}

// FuzzRecordReader: the reader never panics, and whatever it accepts, written
// back and read again, is the same records.
func FuzzRecordReader(f *testing.F) {
	for _, s := range []string{
		"a,,\"\"\n", "\"x\r\ny\",NULL\r\n", "\"open", "a\"b", "\"a\"b", ",\n\n,", "\"\"\"\"",
		write(CSV, edgeCases),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, sep := range []rune{CSV, TSV} {
			rr := NewRecordReader(strings.NewReader(s), sep)
			var recs [][]string
			var nulls [][]bool
			for {
				c, n, err := rr.Read()
				if err != nil {
					break
				}
				if len(c) == 0 {
					// A blank line has no writer form but the set separator.
					continue
				}
				recs, nulls = append(recs, c), append(nulls, n)
			}
			var b strings.Builder
			rw := NewRecordWriter(&b, sep)
			for i := range recs {
				rw.WriteRecord(recs[i], nulls[i])
			}
			rw.Flush()
			again, againN := readAll(t, b.String(), sep)
			for i := range recs {
				if len(recs[i]) == 1 && nulls[i][0] {
					nulls[i][0] = false
				}
			}
			if !slices.EqualFunc(again, recs, slices.Equal) || !slices.EqualFunc(againN, nulls, slices.Equal) {
				t.Fatalf("sep %q: %q read as %q %v, rewritten %q read as %q %v", sep, s, recs, nulls, b.String(), again, againN)
			}
		}
	})
}

// TestRecordReaderFields pins each field form by hand.
func TestRecordReaderFields(t *testing.T) {
	cases := []struct {
		in    string
		cells []string
		nulls []bool
	}{
		{"a,,\"\"\n", []string{"a", "", ""}, []bool{false, true, false}},
		{"NULL,\"NULL\"\n", []string{"NULL", "NULL"}, []bool{false, false}},
		{",\n", []string{"", ""}, []bool{true, true}},
		{"a,\n", []string{"a", ""}, []bool{false, true}},
		{"\"q\"\"x\",b\n", []string{`q"x`, "b"}, []bool{false, false}},
		{"last", []string{"last"}, []bool{false}},
		{"a,b\r\n", []string{"a", "b"}, []bool{false, false}},
		{"\"x\ry\",z\r\n", []string{"x\ry", "z"}, []bool{false, false}},
		{" sp , x\n", []string{" sp ", " x"}, []bool{false, false}},
		{"\"\"\n", []string{""}, []bool{false}},
	}
	for _, c := range cases {
		gotC, gotN := readAll(t, c.in, CSV)
		if len(gotC) != 1 || !slices.Equal(gotC[0], c.cells) || !slices.Equal(gotN[0], c.nulls) {
			t.Errorf("%q read as %q %v, want %q %v", c.in, gotC, gotN, c.cells, c.nulls)
		}
	}
}

// TestRecordReaderKeepsQuotedLineBreaks: a line break inside quotes is kept
// as written, CRLF included, while the CR of a CRLF ending a record is not.
func TestRecordReaderKeepsQuotedLineBreaks(t *testing.T) {
	gotC, _ := readAll(t, "\"a\r\nb\nc\",x\r\ny\r\n", CSV)
	want := [][]string{{"a\r\nb\nc", "x"}, {"y"}}
	if !slices.EqualFunc(gotC, want, slices.Equal) {
		t.Errorf("got %q, want %q", gotC, want)
	}
}

// TestRecordReaderLine: Line is the line a record began on, past records that
// span several.
func TestRecordReaderLine(t *testing.T) {
	rr := NewRecordReader(strings.NewReader("h\n\"a\nb\nc\"\nnext\n\nset2\n"), CSV)
	var lines []int
	for {
		if _, _, err := rr.Read(); err != nil {
			break
		}
		lines = append(lines, rr.Line())
	}
	if want := []int{1, 2, 5, 6, 7}; !slices.Equal(lines, want) {
		t.Errorf("lines %v, want %v", lines, want)
	}
}

// TestRecordReaderRejectsMalformed: each malformed shape is a ParseError on
// the right line, never a silently different record.
func TestRecordReaderRejectsMalformed(t *testing.T) {
	cases := []struct {
		in   string
		err  error
		line int
	}{
		{"ok\na\"b,c\n", ErrBareQuote, 2},
		{"ok\n\"a\"b,c\n", ErrTextAfterQuote, 2},
		{"ok\n\"a\n\nb\n", ErrOpenQuote, 2},
		{"ok\n\"a\"\"\n", ErrOpenQuote, 2},
		{"\"a\n\"x,y\n", ErrTextAfterQuote, 2},
	}
	for _, c := range cases {
		rr := NewRecordReader(strings.NewReader(c.in), CSV)
		var err error
		for err == nil {
			_, _, err = rr.Read()
		}
		pe, ok := errors.AsType[*ParseError](err)
		if !ok || !errors.Is(err, c.err) || pe.Line != c.line {
			t.Errorf("%q: err %v, want %v on line %d", c.in, err, c.err, c.line)
		}
	}
}
