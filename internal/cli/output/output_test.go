package output

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current renderers")

func str(s string) *string { return &s }

// goldenCases are the envelopes the renderers are pinned on, one per shape.
var goldenCases = map[string]*Result{
	// A record answer, as `gossms version` gives it.
	"record": {
		Status: StatusOK, Command: "version",
		Fields: []Field{{"version", "v0.2.0"}, {"commit", "72a0e3c"}, {"cliSchema", "1.0"}},
	},
	// Two result sets: wide (CJK, emoji) and combining cells, NULL, a cell
	// with a line break and tab, truncation, rows affected, a short row.
	"sets": {
		Status: StatusOK, Command: "query run", Server: `localhost\SQL2022`, Database: "AdventureWorks",
		Duration: 38 * time.Millisecond,
		Sets: []Set{
			{
				Columns: []Column{{"name", "sysname", false}, {"note", "nvarchar", true}},
				Rows: [][]*string{
					{str("Customer"), str("日本語")},
					{str("Café"), nil},
					{str("multi\r\nline\there"), str("<a & b>")},
					{str("short")},
				},
				TotalRowCount: 18432, Truncated: true,
			},
			{
				Columns:       []Column{{"n", "", false}},
				Rows:          [][]*string{{str("1")}},
				TotalRowCount: 1, RowsAffected: new(int64(1)),
			},
			{Columns: []Column{{"empty", "int", true}}},
		},
		Messages: []Message{{"info", "(1 row affected)"}},
		Warnings: []string{"a warning\nspanning lines"},
	},
	// A usage error, as Main renders a parse failure.
	"error": {
		Status: StatusError, Command: "",
		Errors: []*Error{NewError(CodeUsageError, `unknown command "verison"`, "Did you mean `gossms version`?")},
	},
	"cancelled": {
		Status: StatusCancelled, Command: "version",
		Errors: []*Error{NewError(CodeCancelled, "cancelled by interrupt", "")},
	},
}

// TestGolden renders every goldenCases entry as text, quiet text and JSON and
// compares with testdata/<case>.<variant>.golden. Run with -update to
// regenerate, then read the diff.
func TestGolden(t *testing.T) {
	variants := []struct {
		name   string
		format Format
		quiet  bool
	}{
		{"txt", FormatText, false},
		{"quiet.txt", FormatText, true},
		{"json", FormatJSON, false},
	}
	for name, r := range goldenCases {
		for _, v := range variants {
			t.Run(name+"."+v.name, func(t *testing.T) {
				var buf bytes.Buffer
				if err := Render(&buf, v.format, r, v.quiet); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join("testdata", name+"."+v.name+".golden")
				if *update {
					if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if buf.String() != string(want) {
					t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
				}
			})
		}
	}
}

// The JSON envelope's keys come in a fixed order, and a record's data keeps
// Fields order rather than encoding/json's sorted map order.
func TestJSONKeyOrder(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatJSON, goldenCases["record"], false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	order := []string{`"schemaVersion"`, `"status"`, `"command"`, `"server"`, `"database"`, `"durationMs"`,
		`"data"`, `"version"`, `"commit"`, `"cliSchema"`, `"resultSets"`, `"messages"`, `"warnings"`, `"errors"`}
	last := -1
	for _, k := range order {
		i := strings.Index(out[last+1:], k+":")
		if i < 0 {
			t.Fatalf("%s out of order in\n%s", k, out)
		}
		last += 1 + i
	}
	if !strings.HasSuffix(out, "}\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("want exactly one trailing newline: %q", out[len(out)-5:])
	}
	var v map[string]any
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["schemaVersion"] != SchemaVersion {
		t.Errorf("schemaVersion = %v", v["schemaVersion"])
	}
}

// A tabular or error envelope has no "data" key.
func TestJSONDataOnlyForRecords(t *testing.T) {
	for name, r := range goldenCases {
		var buf bytes.Buffer
		if err := Render(&buf, FormatJSON, r, false); err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(buf.String(), `"data"`); has != (len(r.Fields) > 0) {
			t.Errorf("%s: data present = %v", name, has)
		}
	}
}

func TestExitCodes(t *testing.T) {
	seen := map[int]bool{}
	for _, e := range ExitCodes {
		if seen[e.Exit] {
			t.Errorf("exit %d twice", e.Exit)
		}
		seen[e.Exit] = true
		if e.Code.Exit() != e.Exit {
			t.Errorf("%s.Exit() = %d, want %d", e.Code, e.Code.Exit(), e.Exit)
		}
	}
	for code, want := range map[Code]int{CodeOK: 0, CodeGeneralError: 1, CodeUsageError: 2, CodeCancelled: 130, "NOPE": 1} {
		if got := code.Exit(); got != want {
			t.Errorf("%s.Exit() = %d, want %d", code, got, want)
		}
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"cliSchema": "CLI_SCHEMA", "version": "VERSION", "durationMs": "DURATION_MS"} {
		if got := upperSnake(in); got != want {
			t.Errorf("upperSnake(%q) = %q", in, got)
		}
	}
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 18432: "18,432", 1234567: "1,234,567", -1234: "-1,234"} {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	if err := Render(&bytes.Buffer{}, "csv", &Result{}, false); err == nil {
		t.Error("csv rendered; want an error until its renderer exists")
	}
}
