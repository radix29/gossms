package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// runWith is runMain with stdin and the files ReadFile can see.
func runWith(t *testing.T, stdin string, files map[string][]byte, argv ...string) runResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(string) string { return "" },
		ReadFile: func(path string) ([]byte, error) {
			if b, ok := files[path]; ok {
				return b, nil
			}
			return nil, fs.ErrNotExist
		},
	}
	exit := run(context.Background(), commandTree(), argv, env)
	return runResult{exit, stdout.String(), stderr.String()}
}

func TestQueryClassify(t *testing.T) {
	r := runWith(t, "SELECT 1\nGO\nUPDATE Sales.Customer SET a = 1", nil, "query", "classify", "--file", "-", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	data := env["data"].(map[string]any)
	if data["detectedClass"] != "write" || data["statementCount"] != "2" {
		t.Errorf("data: %v", data)
	}
	if _, ok := data["allowedClass"]; ok {
		t.Error("allowedClass without --allow")
	}
	rows := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)
	// batch, repeat and line are numbers in JSON.
	if second := rows[1].([]any); second[0] != 2.0 || second[2] != 3.0 || second[3] != "write" || second[4] != "UPDATE" || second[5] != "Sales.Customer" {
		t.Errorf("row 2: %v", second)
	}
}

// With --allow, a script above it is BLOCKED (exit 7) and the refusal names
// the statements; at or under it, OK.
func TestQueryClassifyAllow(t *testing.T) {
	r := runWith(t, "", nil, "query", "classify", "-Q", "SELECT 1; DELETE dbo.t", "--allow", "read")
	if r.exit != 7 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	for _, want := range []string{"STATUS: BLOCKED\n", "DETECTED_CLASS: write\n", "ALLOWED_CLASS: read\n",
		"STATEMENTS: DELETE dbo.t (line 1)\n", "ERROR_CODE: BLOCKED\n", "HINT: Re-run with --allow write"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("lacks %q:\n%s", want, r.stdout)
		}
	}
	if r.stderr != "gossms: BLOCKED: the script is write; the allowed class is read\n" {
		t.Errorf("stderr %q", r.stderr)
	}
	if r := runWith(t, "", nil, "query", "classify", "-Q", "DELETE dbo.t", "--allow", "write"); r.exit != 0 {
		t.Errorf("--allow write: exit %d\n%s", r.exit, r.stdout)
	}
	if r := runWith(t, "", nil, "query", "classify", "-Q", "SET PARSEONLY ON", "--allow", "admin"); r.exit != 7 {
		t.Errorf("PARSEONLY: exit %d\n%s", r.exit, r.stdout)
	}
}

// Exactly one SQL source; each decodes to the same script.
func TestSQLInput(t *testing.T) {
	const script = "UPDATE t SET a = N'é'"
	utf16le := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(script)) {
		utf16le = append(utf16le, byte(u), byte(u>>8))
	}
	files := map[string][]byte{
		"plain.sql": []byte(script),
		"bom.sql":   append([]byte{0xEF, 0xBB, 0xBF}, script...),
		"u16.sql":   utf16le,
		"bad.sql":   {0xC3, 0x28},
		"odd.sql":   {0xFF, 0xFE, 0x41},
	}
	for _, argv := range [][]string{
		{"-Q", script},
		{"--sql=" + script},
		{"--file", "plain.sql"},
		{"-i", "bom.sql"},
		{"--file", "u16.sql"},
		{"--sql-base64", base64.StdEncoding.EncodeToString([]byte(script))},
		{"--sql-base64", base64.RawStdEncoding.EncodeToString([]byte(script))},
	} {
		r := runWith(t, "", files, append([]string{"query", "classify", "-o", "json"}, argv...)...)
		if r.exit != 0 || !strings.Contains(r.stdout, `"detectedClass": "write"`) {
			t.Errorf("%v: exit %d\n%s", argv, r.exit, r.stdout)
		}
	}
	if r := runWith(t, script, nil, "query", "classify", "--file", "-"); r.exit != 0 || !strings.Contains(r.stdout, "DETECTED_CLASS: write") {
		t.Errorf("stdin: exit %d\n%s", r.exit, r.stdout)
	}

	for _, tt := range []struct {
		argv []string
		want string
	}{
		{nil, "no T-SQL given"},
		{[]string{"-Q", "x", "--file", "plain.sql"}, "--sql and --file both given"},
		{[]string{"--file", "missing.sql"}, "--file missing.sql:"},
		{[]string{"--file", "bad.sql"}, "not valid UTF-8"},
		{[]string{"--file", "odd.sql"}, "not valid UTF-8"},
		{[]string{"--sql-base64", "!!!"}, "not Base64"},
	} {
		r := runWith(t, "", files, append([]string{"query", "classify"}, tt.argv...)...)
		if r.exit != 2 || !strings.Contains(r.stdout, tt.want) {
			t.Errorf("%v: exit %d, want 2 and %q\n%s", tt.argv, r.exit, tt.want, r.stdout)
		}
	}
}

// csv and tsv put rows only on stdout and the full summary on stderr (D6).
func TestRowsOnlyFormatsSummaryOnStderr(t *testing.T) {
	r := runWith(t, "", nil, "query", "classify", "-Q", "SELECT 1", "-o", "csv")
	if r.exit != 0 {
		t.Fatalf("exit %d", r.exit)
	}
	if !strings.HasPrefix(r.stdout, "batch,repeat,line,class,leader,object,reason,flags\n1,1,1,read,SELECT,,,\"\"\n") || strings.Contains(r.stdout, "STATUS") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	if !strings.HasPrefix(r.stderr, "STATUS: OK\nCOMMAND: query classify\n") || !strings.Contains(r.stderr, "=== RESULT 1 (1 row, truncated: no) ===") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	// An error under csv: stdout empty, the error block on stderr.
	r = runWith(t, "", nil, "query", "classify", "-o", "tsv")
	if r.exit != 2 || r.stdout != "" || !strings.Contains(r.stderr, "ERROR_CODE: USAGE_ERROR\nMESSAGE: no T-SQL given") {
		t.Errorf("exit %d\nstdout %q\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	}
}

// The output options reach every command: profile list's set is cut by
// --max-rows like a query's.
func TestOutputOptionsApplyToEveryCommand(t *testing.T) {
	r := runWith(t, "", nil, "query", "classify", "-Q", "SELECT 1; SELECT 2; SELECT 3", "--max-rows", "2", "-q")
	if !strings.Contains(r.stdout, "=== RESULT 1 (2 of 3 rows, truncated: yes) ===") || !strings.Contains(r.stdout, "NOTE: Output truncated by --max-rows 2.") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	for _, argv := range [][]string{
		{"version", "--max-rows", "-1"},
		{"version", "--max-output-bytes=-5"},
		{"version", "--force"},
		{"version", "--out-file="},
		{"version", "--out-file", "x.xlsx"},
	} {
		if r := runWith(t, "", nil, argv...); r.exit != 2 {
			t.Errorf("%v: exit %d\n%s", argv, r.exit, r.stdout)
		}
	}
}

// --out-file through Main: the rows in the file, the summary on stdout naming
// it; refused before the handler runs when the file exists.
func TestOutFileThroughMain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.csv")
	r := runWith(t, "", nil, "query", "classify", "-Q", "SELECT 1", "--out-file", path, "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	env := envelope(t, r.stdout)
	if env["outFile"] != path || len(env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)) != 0 {
		t.Errorf("envelope: %v", env)
	}
	if data, _ := os.ReadFile(path); !strings.HasPrefix(string(data), "batch,repeat,line") {
		t.Errorf("file: %q", data)
	}
	r = runWith(t, "", nil, "query", "classify", "-Q", "SELECT 1", "--out-file", path)
	if r.exit != 2 || !strings.Contains(r.stdout, "the file exists") {
		t.Errorf("existing: exit %d\n%s", r.exit, r.stdout)
	}
}
