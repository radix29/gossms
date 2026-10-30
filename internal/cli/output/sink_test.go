package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/export"
)

// feed streams sets into s as the executor would: BeginTypedSet, Row with a
// reused slice, EndSet.
func feed(t *testing.T, s *Sink, sets []Set) {
	t.Helper()
	for _, set := range sets {
		names, types, nullable := make([]string, len(set.Columns)), make([]string, len(set.Columns)), make([]bool, len(set.Columns))
		for i, c := range set.Columns {
			names[i], types[i], nullable[i] = c.Name, c.Type, c.Nullable
		}
		if err := s.BeginTypedSet(names, types, nullable); err != nil {
			t.Fatal(err)
		}
		cells, isNull := make([]string, len(set.Columns)), make([]bool, len(set.Columns))
		for _, row := range set.Rows {
			for i := range cells {
				cells[i], isNull[i] = "", row[i] == nil
				if row[i] != nil {
					cells[i] = *row[i]
				}
			}
			if err := s.Row(cells, isNull); err != nil {
				t.Fatal(err)
			}
			// The executor reuses its slices: a kept row must not see this.
			for i := range cells {
				cells[i] = "CLOBBERED"
			}
		}
		if err := s.EndSet(len(set.Rows)); err != nil {
			t.Fatal(err)
		}
	}
}

func numberedSet(cols, rows int) Set {
	s := Set{}
	for c := range cols {
		s.Columns = append(s.Columns, Column{Name: "c" + strconv.Itoa(c), Type: "int"})
	}
	for r := range rows {
		row := make([]*string, cols)
		for c := range row {
			row[c] = str(strconv.Itoa(r*cols + c))
		}
		s.Rows = append(s.Rows, row)
	}
	s.TotalRowCount = rows
	return s
}

// --max-rows keeps the first N rows of every set, counts the rest exactly,
// and marks the set; the rows kept are copies, not the executor's slices.
func TestSinkMaxRows(t *testing.T) {
	o := DefaultOptions(FormatText)
	o.MaxRows = 3
	s, err := NewSink(nil, o)
	if err != nil {
		t.Fatal(err)
	}
	in := []Set{numberedSet(2, 10), numberedSet(1, 3), numberedSet(1, 0)}
	in[0].Rows[1][1] = nil
	feed(t, s, in)
	var r Result
	if err := s.Finish(&r, false); err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		kept, total int
		truncated   bool
	}{{3, 10, true}, {3, 3, false}, {0, 0, false}} {
		got := r.Sets[i]
		if len(got.Rows) != want.kept || got.TotalRowCount != want.total || got.Truncated != want.truncated {
			t.Errorf("set %d: kept %d of %d, truncated %v; want %+v", i, len(got.Rows), got.TotalRowCount, got.Truncated, want)
		}
		if want.truncated != (got.TruncatedBy == TruncatedByMaxRows) {
			t.Errorf("set %d: truncatedBy %q", i, got.TruncatedBy)
		}
	}
	if *r.Sets[0].Rows[2][1] != "5" || r.Sets[0].Rows[1][1] != nil {
		t.Errorf("rows not copied: %q %v", *r.Sets[0].Rows[2][1], r.Sets[0].Rows[1][1])
	}
	if r.Sets[0].Columns[0].Type != "int" {
		t.Errorf("column type lost: %+v", r.Sets[0].Columns[0])
	}
}

// A plain RowSink caller gets nullable, typeless columns.
func TestSinkUntypedSet(t *testing.T) {
	s, _ := NewSink(nil, DefaultOptions(FormatText))
	s.BeginSet([]string{"a"})
	s.Row([]string{"x"}, []bool{false})
	var r Result
	s.Finish(&r, false)
	if c := r.Sets[0].Columns[0]; c.Type != "" || !c.Nullable {
		t.Errorf("%+v", c)
	}
}

// jsonl streams: 100,000 rows go to stdout as they arrive and none is kept,
// whatever --max-rows says when it is 0.
func TestSinkJSONLStreamsWithoutRetaining(t *testing.T) {
	o := DefaultOptions(FormatJSONL)
	o.MaxRows, o.MaxOutputBytes = 0, 0
	var lines int
	w := lineCounter{n: &lines}
	s, err := NewSink(w, o)
	if err != nil {
		t.Fatal(err)
	}
	const rows = 100_000
	if err := s.BeginTypedSet([]string{"id", "name"}, []string{"int", "nvarchar(20)"}, []bool{false, true}); err != nil {
		t.Fatal(err)
	}
	cells, isNull := []string{"", "row"}, []bool{false, false}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range rows {
		cells[0] = strconv.Itoa(i)
		if err := s.Row(cells, isNull); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	s.EndSet(rows)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 1<<20 {
		t.Errorf("heap grew %d bytes over %d streamed rows", grew, rows)
	}
	r := Result{Status: StatusOK}
	if err := s.Finish(&r, false); err != nil {
		t.Fatal(err)
	}
	if got := r.Sets[0]; len(got.Rows) != 0 || got.TotalRowCount != rows || got.streamed != rows || got.Truncated {
		t.Errorf("set: %d rows kept, total %d, streamed %d, truncated %v", len(got.Rows), got.TotalRowCount, got.streamed, got.Truncated)
	}
	if lines != rows+1 {
		t.Errorf("%d lines streamed, want %d (the _resultSet line and the rows)", lines, rows+1)
	}
}

// lineCounter counts the newlines written to it and keeps nothing.
type lineCounter struct{ n *int }

func (l lineCounter) Write(p []byte) (int, error) {
	*l.n += bytes.Count(p, []byte{'\n'})
	return len(p), nil
}

// jsonl's lines: a _resultSet line, row objects keyed by column, the summary
// last; --max-rows stops the rows and says so.
func TestJSONLLines(t *testing.T) {
	o := DefaultOptions(FormatJSONL)
	o.MaxRows = 2
	var out bytes.Buffer
	s, _ := NewSink(&out, o)
	feed(t, s, []Set{numberedSet(2, 5)})
	r := Result{Status: StatusOK, Command: "query run"}
	s.Finish(&r, false)
	if err := Render(&out, &r, o); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines:\n%s", len(lines), out.String())
	}
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Errorf("not JSON: %s", l)
		}
	}
	if lines[1] != `{"c0":0,"c1":1}` || lines[2] != `{"c0":2,"c1":3}` {
		t.Errorf("rows: %s / %s", lines[1], lines[2])
	}
	var sum struct {
		Summary struct {
			ResultSets []struct {
				RowCount, TotalRowCount int
				Truncated               bool
				TruncatedBy             string
			}
		} `json:"_summary"`
	}
	if err := json.Unmarshal([]byte(lines[3]), &sum); err != nil {
		t.Fatal(err)
	}
	if got := sum.Summary.ResultSets[0]; got.RowCount != 2 || got.TotalRowCount != 5 || !got.Truncated || got.TruncatedBy != TruncatedByMaxRows {
		t.Errorf("summary set: %+v", got)
	}
}

// D8: whatever the cap, json and jsonl stay valid documents, never cut inside
// a value, and drop whole trailing rows (the last set's first).
func TestMaxOutputBytesNeverBreaksJSON(t *testing.T) {
	fixture := func() *Result {
		r := *goldenCases["typed"]
		r.Sets = []Set{goldenCases["typed"].Sets[0], numberedSet(2, 4)}
		return &r
	}
	var full bytes.Buffer
	Render(&full, fixture(), Options{Format: FormatJSON, NullText: "NULL"})
	for limit := int64(0); limit <= int64(full.Len())+10; limit++ {
		for _, f := range []Format{FormatJSON, FormatJSONL} {
			o := Options{Format: f, NullText: "NULL", MaxOutputBytes: limit}
			if limit == 0 {
				o.MaxOutputBytes = 1 // 0 is "no cap"
			}
			r := fixture()
			var out bytes.Buffer
			if f == FormatJSONL {
				s, _ := NewSink(&out, o)
				feed(t, s, r.Sets)
				r.Sets = nil
				s.Finish(r, false)
			}
			if err := Render(&out, r, o); err != nil {
				t.Fatal(err)
			}
			if f == FormatJSON && !json.Valid(out.Bytes()) {
				t.Fatalf("json, cap %d: invalid:\n%s", limit, out.String())
			}
			if f == FormatJSONL {
				// The rows stay under the cap; the summary line is exempt.
				body := out.Bytes()[:bytes.LastIndex(out.Bytes(), []byte(`{"_summary"`))]
				if limit > 0 && int64(len(body)) > limit {
					t.Fatalf("jsonl, cap %d: %d bytes before the summary", limit, len(body))
				}
				sc := bufio.NewScanner(&out)
				sc.Buffer(nil, 1<<20)
				for sc.Scan() {
					if !json.Valid(sc.Bytes()) {
						t.Fatalf("jsonl, cap %d: invalid line %s", limit, sc.Text())
					}
				}
			}
		}
	}
}

// The cap drops the last rows first and keeps what fits.
func TestMaxOutputBytesDropsTrailingRows(t *testing.T) {
	r := &Result{Status: StatusOK, Command: "x", Sets: []Set{numberedSet(1, 500), numberedSet(1, 500)}}
	var full bytes.Buffer
	o := Options{Format: FormatText, NullText: "NULL"}
	Render(&full, r, o)
	o.MaxOutputBytes = int64(full.Len() - 200)
	var out bytes.Buffer
	if err := Render(&out, r, o); err != nil {
		t.Fatal(err)
	}
	if int64(out.Len()) > o.MaxOutputBytes {
		t.Errorf("%d bytes over the cap %d", out.Len(), o.MaxOutputBytes)
	}
	if r.Sets[0].Truncated || len(r.Sets[0].Rows) != 500 {
		t.Errorf("the first set lost rows before the last: %d", len(r.Sets[0].Rows))
	}
	last := r.Sets[1]
	if !last.Truncated || last.TruncatedBy != TruncatedByMaxOutputBytes || len(last.Rows) == 0 || len(last.Rows) == 500 {
		t.Errorf("last set: %d rows, truncated %v by %q", len(last.Rows), last.Truncated, last.TruncatedBy)
	}
	if !strings.Contains(out.String(), "NOTE: Output truncated by --max-output-bytes") {
		t.Errorf("no note:\n%s", out.String())
	}
	// A cap below the rowless envelope still writes it, and says why.
	r = &Result{Status: StatusOK, Command: "x", Sets: []Set{numberedSet(1, 5)}}
	o.MaxOutputBytes = 10
	out.Reset()
	Render(&out, r, o)
	if len(r.Sets[0].Rows) != 0 || !strings.Contains(out.String(), "over --max-output-bytes with every row left out") {
		t.Errorf("rowless:\n%s", out.String())
	}
}

// The text helpers behind D10 and P2-12.
func TestCells(t *testing.T) {
	for in, want := range map[string]bool{"0": true, "-0": true, "12": true, "1.5": true, "1E+300": true, "-1.5E-05": true,
		"1e5": true, "": false, "01": false, ".5": false, "1.": false, "+1": false, "1E": false, "NaN": false, "Inf": false, "1 ": false} {
		if got := isJSONNumber(in); got != want {
			t.Errorf("isJSONNumber(%q) = %v", in, got)
		}
	}
	for _, tt := range []struct {
		in    string
		width int
		want  string
	}{
		{"abc", 3, "abc"}, {"abcd", 3, "abc…[+1 chars]"}, {"日本語です", 2, "日本…[+3 chars]"},
		{strings.Repeat("x", 1300), 100, strings.Repeat("x", 100) + "…[+1,200 chars]"}, {"abcd", 0, "abcd"},
	} {
		if got := clip(tt.in, tt.width); got != tt.want {
			t.Errorf("clip(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
	for typ, want := range map[string]cellKind{"int": kindNumber, "BIGINT": kindNumber, "float": kindNumber, "real": kindNumber,
		"tinyint": kindNumber, "bit": kindBool, "decimal(18,2)": kindString, "money": kindString, "nvarchar(max)": kindString, "": kindString} {
		if got := kindOf(typ); got != want {
			t.Errorf("kindOf(%q) = %v", typ, got)
		}
	}
	// A number column whose text is not a JSON number stays a string.
	if v := jsonCell(str("1,5"), kindNumber, 0); v != "1,5" {
		t.Errorf("jsonCell = %#v", v)
	}
	// A JSON column's document is a value, never clipped (that would make
	// it invalid); one that is not valid JSON is clipped text.
	if kindOf(TypeJSON) != kindJSON {
		t.Errorf("kindOf(%q) = %v", TypeJSON, kindOf(TypeJSON))
	}
	doc := `{"fields":{"a":"` + strings.Repeat("x", 50) + `"}}`
	if v, ok := jsonCell(str(doc), kindJSON, 10).(json.RawMessage); !ok || string(v) != doc {
		t.Errorf("jsonCell(document) = %#v", jsonCell(str(doc), kindJSON, 10))
	}
	if v := jsonCell(str("{not json"), kindJSON, 3); v != "{no…[+6 chars]" {
		t.Errorf("jsonCell(invalid) = %#v", v)
	}
}

func TestRowKeys(t *testing.T) {
	cols := []Column{{Name: "a"}, {Name: ""}, {Name: "a"}, {Name: "_resultSet"}, {Name: "a_3"}, {Name: "_summary"}}
	want := []string{"a", "column2", "a_3", "_resultSet_4", "a_3_5", "_summary_6"}
	if got := rowKeys(cols); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rowKeys = %v, want %v", got, want)
	}
}

// csv and tsv through the Sink are export.RecordWriter's bytes: the TUI's
// Results To File writer, NULL and ” kept apart.
func TestRecordsAreTheExportWriters(t *testing.T) {
	set := goldenCases["typed"].Sets[0]
	for _, f := range []Format{FormatCSV, FormatTSV} {
		var want bytes.Buffer
		sep := rune(export.CSV)
		if f == FormatTSV {
			sep = export.TSV
		}
		rw := export.NewRecordWriter(&want, sep)
		names := []string{}
		for _, c := range set.Columns {
			names = append(names, c.Name)
		}
		rw.BeginSet(names)
		for _, row := range set.Rows {
			cells, isNull := make([]string, len(row)), make([]bool, len(row))
			for i, c := range row {
				if c == nil {
					isNull[i] = true
				} else {
					cells[i] = *c
				}
			}
			rw.WriteRecord(cells, isNull)
		}
		rw.Flush()
		o := DefaultOptions(f)
		var got bytes.Buffer
		if err := Render(&got, &Result{Sets: []Set{set}}, o); err != nil {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Errorf("%s:\n%q\nwant\n%q", f, got.String(), want.String())
		}
		// --no-headers: the same minus the header line.
		o.NoHeaders = true
		got.Reset()
		Render(&got, &Result{Sets: []Set{set}}, o)
		_, rest, _ := strings.Cut(want.String(), "\n")
		if got.String() != rest {
			t.Errorf("%s --no-headers:\n%q\nwant\n%q", f, got.String(), rest)
		}
	}
}

func TestTextOptions(t *testing.T) {
	r := &Result{Status: StatusOK, Command: "x", Sets: []Set{{
		Columns: []Column{{Name: "a"}, {Name: "b"}}, Rows: [][]*string{{nil, str("xyz")}}, TotalRowCount: 1,
	}}, Messages: []Message{{"info", "hello"}}}
	o := Options{Format: FormatText, NullText: "<null>", NoHeaders: true, NoMessages: true, MaxColWidth: 2, Quiet: true}
	var out bytes.Buffer
	Render(&out, r, o)
	want := "=== RESULT 1 (1 row, truncated: no) ===\n<null> | xy…[+1 chars]\n"
	if out.String() != want {
		t.Errorf("got\n%q\nwant\n%q", out.String(), want)
	}
	if len(r.Messages) != 1 {
		t.Error("--no-messages changed the caller's result")
	}
}

// D6: csv and tsv write rows only; RenderSummary is the rest, for stderr.
func TestRenderSummary(t *testing.T) {
	r := *goldenCases["sets"]
	var out bytes.Buffer
	if err := RenderSummary(&out, &r, DefaultOptions(FormatCSV)); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"STATUS: OK\n", "=== RESULT 1 (4 of 18,432 rows, truncated: yes) ===\nNOTE: Output truncated by --max-rows 200.", "=== MESSAGES ===\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Customer") {
		t.Errorf("summary has rows:\n%s", s)
	}
}

// D7: --out-file takes every row, unclipped, in its extension's format; a
// failed run leaves the destination as it was; an existing file needs --force.
func TestOutFile(t *testing.T) {
	dir := t.TempDir()
	set := numberedSet(1, 500)
	set.Rows[0][0] = str(strings.Repeat("x", 1000))
	for _, ext := range []string{".csv", ".tsv", ".jsonl", ".json", ".md", ".txt"} {
		path := filepath.Join(dir, "out"+ext)
		o := DefaultOptions(FormatJSON)
		o.OutFile = path
		var stdout bytes.Buffer
		s, err := NewSink(&stdout, o)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		feed(t, s, []Set{set})
		r := Result{Command: "x", Status: StatusOK}
		if err := s.Finish(&r, false); err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if r.OutFile != path || !r.Sets[0].InFile || len(r.Sets[0].Rows) != 0 || r.Sets[0].TotalRowCount != 500 {
			t.Errorf("%s: result %+v", ext, r.Sets[0])
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: rows reached stdout", ext)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(strings.Repeat("x", 1000))) || !bytes.Contains(data, []byte("499")) {
			t.Errorf("%s: the file lacks rows or clipped a cell (%d bytes)", ext, len(data))
		}
		if ext == ".json" && !json.Valid(data) {
			t.Errorf(".json: invalid")
		}
		// Exists now: refused without --force, before anything is written.
		if _, err := NewSink(&stdout, o); err == nil || !strings.Contains(err.Error(), "exists") {
			t.Errorf("%s: existing file: %v", ext, err)
		}
	}

	// A failed run: the old contents stay and no temp file is left.
	path := filepath.Join(dir, "keep.csv")
	os.WriteFile(path, []byte("old\n"), 0o644)
	o := DefaultOptions(FormatText)
	o.OutFile, o.Force = path, true
	s, err := NewSink(nil, o)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, s, []Set{set})
	var r Result
	s.Finish(&r, true)
	if data, _ := os.ReadFile(path); string(data) != "old\n" {
		t.Errorf("failed run replaced the file: %q", data)
	}
	if r.OutFile != "" || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "unchanged") {
		t.Errorf("result %+v", r)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "keep.csv.tmp*")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}

	for _, bad := range []string{filepath.Join(dir, "x.xlsx"), filepath.Join(dir, "noext"), dir + string(os.PathSeparator)} {
		o.OutFile = bad
		_, err := NewSink(nil, o)
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeUsageError {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
