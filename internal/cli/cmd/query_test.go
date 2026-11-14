package cmd

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/audit"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/sqltext"
)

// --file - and --password-stdin both want stdin: refused on a command that
// has both, before either is read.
func TestReadSQLStdinConflict(t *testing.T) {
	stdin := strings.NewReader("SELECT 1")
	inv := &spec.Invocation{
		Values: map[string]any{optFile: "-", optSQL: "", optSQLBase64: "", "password-stdin": true},
		Given:  map[string]bool{optFile: true},
		Stdin:  stdin,
	}
	_, err := readSQL(inv)
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError || !strings.Contains(e.Message, "--password-stdin") {
		t.Errorf("got %v", err)
	}
	if stdin.Len() != len("SELECT 1") {
		t.Error("stdin was read")
	}
}

// readSQL decodes --file through textenc.Decode and refuses what it reports
// lossy: running a script with U+FFFD where the file had bytes it could not
// decode is a silent change to the script. (The decoder's own cases are in
// internal/textenc.)
func TestReadSQLDecodesAndRefusesLossy(t *testing.T) {
	for _, tt := range []struct {
		in   []byte
		want string
		ok   bool
	}{
		{[]byte("SELECT 1"), "SELECT 1", true},
		{[]byte("\xEF\xBB\xBFSELECT 1"), "SELECT 1", true},
		{[]byte{0xFF, 0xFE, 'A', 0, 0x3D, 0xD8, 0x00, 0xDE}, "A😀", true}, // UTF-16LE, a surrogate pair
		{[]byte{0xFE, 0xFF, 0, 'A', 0, 'B'}, "AB", true},                 // UTF-16BE
		{[]byte{0xFF, 0xFE, 0x3D, 0xD8, 'A', 0}, "", false},              // lone high surrogate
		{[]byte{0xFF, 0xFE, 'A'}, "", false},                             // odd length
		{[]byte{0xC3, 0x28}, "", false},                                  // invalid UTF-8
	} {
		inv := &spec.Invocation{
			Values:   map[string]any{optFile: "x.sql", optSQL: "", optSQLBase64: ""},
			Given:    map[string]bool{optFile: true},
			ReadFile: func(string) ([]byte, error) { return tt.in, nil },
		}
		got, err := readSQL(inv)
		if (err == nil) != tt.ok || (err == nil && got != tt.want) {
			t.Errorf("readSQL(% x) = %q, %v; want %q, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

// A CR-only (classic Mac) script's GO is a separator: readSQL folds line
// endings to LF, which sqltext.SplitBatches needs. Unfolded, GO reached the
// server as T-SQL ("Incorrect syntax near 'GO'"). The audit hash is still of
// the script as read.
func TestReadSQLFoldsLoneCR(t *testing.T) {
	in := "select 1\rGO\rselect 2"
	inv := &spec.Invocation{
		Values:   map[string]any{optFile: "x.sql", optSQL: "", optSQLBase64: ""},
		Given:    map[string]bool{optFile: true},
		ReadFile: func(string) ([]byte, error) { return []byte(in), nil },
	}
	got, err := readSQL(inv)
	if err != nil || got != "select 1\nGO\nselect 2" {
		t.Fatalf("readSQL = %q, %v", got, err)
	}
	if n := len(sqltext.SplitBatches(got)); n != 2 {
		t.Errorf("%d batches, want 2", n)
	}
	if inv.Audit.SQLSha256 != audit.Hash(in) {
		t.Error("audit hash is not of the script as read")
	}
}
