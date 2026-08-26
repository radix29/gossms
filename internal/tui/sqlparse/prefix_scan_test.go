package sqlparse

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ScanPrefix replaced a scan that tokenized the whole prefix and
// then threw away everything before the current statement. Until 2026-08-04
// these tests pinned the two together differentially, against a second T-SQL
// lexer written here purely as a baseline — two copies of the same semantics
// to keep in sync forever (docs/open-threads.md, "The two lexer
// implementations"). That baseline is gone; what it established is frozen
// below as golden output.
//
// The trade is deliberate and worth stating: a golden file pins *current*
// behavior, so it catches a regression but cannot find a bug that was always
// there. The equivalence itself was what the differential sweep proved, over
// this corpus, at every cursor position, across many releases. From here the
// corpus's job is to make any change in what the scan produces visible in a
// diff rather than silent.
//
// Regenerate after an intended change, and read the diff:
//
//	go test ./internal/tui/sqlparse -run TestScanCompletionPrefixGolden -update-golden

var updateGolden = flag.Bool("update-golden", false,
	"rewrite the completion prefix-scan golden file instead of comparing against it")

const prefixScanGoldenPath = "testdata/completion_prefix_scan.golden"

// splitRunes turns a script into the [][]rune shape the editor hands the
// completion provider.
func splitRunes(script string) [][]rune {
	parts := strings.Split(script, "\n")
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	return lines
}

// flattenFresh is FlattenLinesInto's always-allocate form. Production never
// wants it — the point of FlattenLinesInto is that QueryPanel keeps one
// buffer across keystrokes — but a test comparing a recycled buffer against a
// clean one needs both, and a buffer-reusing call cannot check its own reuse.
func flattenFresh(lines [][]rune) []rune { return FlattenLinesInto(nil, lines) }

var tokKindNames = [...]string{"id", "kw", "dot", "comma", "lparen", "rparen"}

var lexStateNames = [...]string{"normal", "linecomment", "blockcomment", "singlequote", "bracket", "doublequote"}

func tokKindName(k TokenKind) string {
	if int(k) < len(tokKindNames) {
		return tokKindNames[k]
	}
	return fmt.Sprintf("kind%d", int(k))
}

func lexStateName(s LexState) string {
	if int(s) < len(lexStateNames) {
		return lexStateNames[s]
	}
	return fmt.Sprintf("state%d", int(s))
}

// formatScan renders one scan result as the single golden line that stands
// for it: everything sqlCompletionCandidates reads, and nothing else.
//
// quoteStart is only meaningful in the two quoted states (see
// TokenizeRange), so it is written as "-" elsewhere rather than freezing a
// stale value that no caller looks at and any refactor could legitimately
// change.
func formatScan(s PrefixScan) string {
	quote := "-"
	if s.State == LexBracket || s.State == LexDoubleQuote {
		quote = fmt.Sprintf("%d", s.QuoteStart)
	}
	return fmt.Sprintf("%s batch=%d quote=%s |%s", lexStateName(s.State), s.BatchStart, quote, dumpTokens(s.Tokens))
}

func dumpTokens(ts []Token) string {
	var b strings.Builder
	for _, t := range ts {
		fmt.Fprintf(&b, " %s:%q@%d", tokKindName(t.Kind), t.Text, t.Start)
	}
	return b.String()
}

// sweepCursorPositions scans at every (row, col) in the script — exhaustive
// rather than sampled, since the interesting divergences are all about
// exactly where the cursor lands.
func sweepCursorPositions(script string) []string {
	lines := splitRunes(script)
	buf := flattenFresh(lines)
	var out []string
	for row := range lines {
		for col := 0; col <= len(lines[row]); col++ {
			upTo := OffsetForCursor(lines, row, col)
			out = append(out, fmt.Sprintf("%d,%d %s", row, col,
				formatScan(ScanPrefix(lines, buf, row, upTo))))
		}
	}
	return out
}

// sweepWhileTyping scans after every keystroke of the script, cursor at the
// end — the access pattern the completion popup actually produces, and the
// one that puts an unterminated comment/string/bracket at the cursor.
func sweepWhileTyping(script string) []string {
	runes := []rune(script)
	out := make([]string, 0, len(runes)+1)
	for n := 0; n <= len(runes); n++ {
		lines := splitRunes(string(runes[:n]))
		buf := flattenFresh(lines)
		row := len(lines) - 1
		upTo := OffsetForCursor(lines, row, len(lines[row]))
		out = append(out, fmt.Sprintf("%d %s", n, formatScan(ScanPrefix(lines, buf, row, upTo))))
	}
	return out
}

// digest condenses a sweep to one line of golden. Used for the two bulk
// sections — 400 generated scripts and the typing sweep — where the full
// streams would run to megabytes and nobody would read the diff anyway. The
// curated corpus's cursor sweep is written out in full instead, because that
// is the one a human reviews when the scan changes.
func digest(lines []string) string {
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:8])
}

// diffCorpus is aimed squarely at the assumptions the two-pass scan makes:
// that the statement boundary is findable without tokens, and that tokenizing
// from it reproduces the same tokens a full-prefix pass produced there. Half
// the entries are "GO" in a context that does not separate batches.
var diffCorpus = map[string]string{
	"plain": "SELECT a, b FROM dbo.T WHERE x = 1",

	"go separated": "SELECT 1 FROM A\nGO\nSELECT 2 FROM B\nGO\nSELECT 3 FROM C",

	"semicolons": "SELECT 1 FROM A; SELECT 2 FROM B; SELECT 3 FROM C",

	// A GO line inside a block comment is not a real batch separator, so
	// nothing here starts a new batch.
	"go inside block comment": "SELECT 1\n/*\nGO\n*/\nFROM T",

	// The same, with tokens after the comment closes on the GO-following line.
	"go inside block comment then code": "SELECT x\n/* hide\nGO\n*/ FROM dbo.Orders o WHERE o.id = 1",

	// A bracket identifier spanning the GO line — the GO is inside it, so the
	// whole thing stays one batch and the ']' still closes the identifier it
	// opened on line 1.
	"bracket spans go line": "SELECT [weird\nGO\nname] FROM T",

	"double quote spans go line": "SELECT \"weird\nGO\nname\" FROM T",

	// A string literal that contains what looks like a separator.
	"semicolon in string":  "SELECT 'a;b' AS c FROM T WHERE y = 'GO'",
	"go word in string":    "SELECT 'GO' FROM T",
	"string spans go line": "SELECT 'weird\nGO\nname' FROM T",

	"line comment with semicolon": "SELECT 1 -- ;not a boundary\nFROM T",
	"line comment with go":        "SELECT 1 -- GO\nFROM T",

	"semicolon inside brackets": "SELECT [a;b] FROM [c;d]",

	// Unterminated openers: the states completion either continues in
	// (bracket) or bails out of.
	"unterminated bracket":       "SELECT * FROM [Cus",
	"unterminated block comment": "SELECT 1\n/* never closed\nGO\nSELECT 2",
	"unterminated string":        "SELECT 'never closed\nGO\nSELECT 2",

	"escaped bracket":     "SELECT [a]]b] FROM T",
	"escaped quote":       "SELECT 'it''s' FROM T",
	"escaped doublequote": "SELECT \"a\"\"b\" FROM T",

	"go casing and spacing": "SELECT 1\n  go  \nSELECT 2\n\tGo\t\nSELECT 3",

	// The commented-out GO in full: the alias on line 1 must stay in scope
	// for the column completion on the last line.
	"commented go with trailing alias": "SELECT * FROM dbo.Patients p\n/*\nGO\n*/\nWHERE p.",

	// A repeat count and a trailing line comment are part of a separator; a
	// "GO" that is the head of a longer word is not one at all.
	"go with a count":   "SELECT 1\nGO 5\nSELECT 2\nXGO\nSELECT 3\nGOO\nSELECT 4",
	"go with a comment": "SELECT 1\nGO -- run it\nSELECT 2 FROM dbo.T t\nWHERE t.",

	"semicolon then go":     "SELECT 1;\nGO\nSELECT 2",
	"go then semicolon":     "SELECT 1\nGO\nSELECT 2; SELECT 3",
	"empty":                 "",
	"only go":               "GO",
	"leading go":            "GO\nSELECT 1 FROM T",
	"trailing semicolon":    "SELECT 1 FROM T;",
	"consecutive semicolon": "SELECT 1;;;SELECT 2",
	"qualified names":       "SELECT o.* FROM [my db].dbo.[Ord.ers] AS o JOIN a.b.c d ON d.x = o.y",
	"parens and commas":     "SELECT f(a, b), (c), COUNT(*) FROM T GROUP BY a, b",
	"unicode":               "SELECT [Ку], 'пароль' FROM [密码] WHERE x = N'密'",
}

// fragments are recombined into synthetic scripts below. The set is chosen so
// that concatenation routinely produces unbalanced comments, strings, and
// brackets straddling GO lines — the shapes that break a resumed scan.
var fragments = []string{
	"SELECT a, b FROM dbo.T",
	"SELECT * FROM [Cus",
	"GO",
	" go ",
	"GO 3",
	";",
	"; SELECT 2 FROM B",
	"/*",
	"*/",
	"/* inline */",
	"-- trailing comment",
	"'unterminated",
	"'closed'",
	"''",
	"[bracketed]",
	"[unterminated",
	"]",
	"\"dq\"",
	"\"unterminated",
	"WHERE x = 1",
	"JOIN o ON o.id = t.id",
	"a.b.c",
	"f(x, y)",
	"",
	"   ",
	"N'lit;eral'",
	"SELECT 'GO'",
}

// generatedScripts assembles the random corpus. Seeded, so the golden file
// means something: the same 400 scripts come out on every run and on every
// machine.
func generatedScripts() []string {
	rng := rand.New(rand.NewSource(20260730))
	out := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		var b strings.Builder
		nLines := 1 + rng.Intn(6)
		for l := 0; l < nLines; l++ {
			if l > 0 {
				b.WriteByte('\n')
			}
			for f := 0; f < 1+rng.Intn(3); f++ {
				if f > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(fragments[rng.Intn(len(fragments))])
			}
		}
		out = append(out, b.String())
	}
	return out
}

func sortedCorpusNames() []string {
	names := make([]string, 0, len(diffCorpus))
	for name := range diffCorpus {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildGolden renders the whole golden file. Map iteration order is random,
// hence the sorted names — a golden file that reshuffles itself on every
// regeneration is worthless as a diff.
func buildGolden() string {
	var b strings.Builder
	b.WriteString("# goSSMS — ScanPrefix golden output. Do not hand-edit.\n")
	b.WriteString("# Regenerate: go test ./internal/tui/sqlparse -run TestScanCompletionPrefixGolden -update-golden\n")
	b.WriteString("#\n")
	b.WriteString("# [cursor-sweep] one line per cursor position, in full:\n")
	b.WriteString("#   <row>,<col> <lexer state> batch=<statement start> quote=<offset|-> | <tokens>\n")
	b.WriteString("# Tokens are <kind>:<text>@<rune offset>. quote is \"-\" outside the two\n")
	b.WriteString("# quoted states, where no caller reads it.\n")
	b.WriteString("#\n")
	b.WriteString("# [typing] and [generated] are digests: one hash per script over the same\n")
	b.WriteString("# per-position stream. Full streams there would run to megabytes. On a\n")
	b.WriteString("# mismatch, re-run that script through sweepWhileTyping/sweepCursorPositions\n")
	b.WriteString("# by hand to see what moved.\n")

	b.WriteString("\n[cursor-sweep]\n")
	for _, name := range sortedCorpusNames() {
		fmt.Fprintf(&b, "\n== %s\nscript %q\n", name, diffCorpus[name])
		for _, line := range sweepCursorPositions(diffCorpus[name]) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	b.WriteString("\n[typing]\n")
	for _, name := range sortedCorpusNames() {
		fmt.Fprintf(&b, "%s %s\n", digest(sweepWhileTyping(diffCorpus[name])), name)
	}

	b.WriteString("\n[generated]\n")
	for i, script := range generatedScripts() {
		fmt.Fprintf(&b, "%s script%03d %q\n", digest(sweepCursorPositions(script)), i, script)
	}
	return b.String()
}

// TestScanCompletionPrefixGolden is the frozen form of what the differential
// sweep used to prove on every run. A failure here is not automatically a bug
// — it means the scan's output changed. Read the diff, decide whether the
// change was intended, and only then regenerate with -update-golden.
func TestScanCompletionPrefixGolden(t *testing.T) {
	got := buildGolden()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(prefixScanGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prefixScanGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", prefixScanGoldenPath, len(got))
		return
	}
	wantBytes, err := os.ReadFile(prefixScanGoldenPath)
	if err != nil {
		t.Fatalf("%v — regenerate with: go test ./internal/tui/sqlparse -run TestScanCompletionPrefixGolden -update-golden", err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("%s differs at line %d:\n got: %s\nwant: %s\n\nRead the whole diff before regenerating:\n"+
				"  go test ./internal/tui/sqlparse -run TestScanCompletionPrefixGolden -update-golden && git diff %s",
				prefixScanGoldenPath, i+1, gotLines[i], wantLines[i], prefixScanGoldenPath)
		}
	}
	t.Fatalf("%s differs in length: got %d lines, want %d", prefixScanGoldenPath, len(gotLines), len(wantLines))
}

// compareScans reports the first difference between two scans, or "".
func compareScans(want, got PrefixScan) string {
	if w, g := formatScan(want), formatScan(got); w != g {
		return fmt.Sprintf("\n got: %s\nwant: %s", g, w)
	}
	return ""
}

// goSeparatorLineCases is the definition of a "GO" batch separator, shared
// verbatim with internal/tuikit/controls's TestGoSeparatorLineCases. The
// two implementations are duplicated because tuikit must not import tui, so
// the table is what keeps them from drifting apart; change one list and the
// other package fails.
//
// The executor's own splitter (github.com/microsoft/go-mssqldb/batch.Split) is
// looser, and deliberately not copied — measured against v1.11.0, it also
// splits on "GO;", "GO x", "GO_" and "GO/*c*/", each of which leaves the junk
// at the head of the next batch for the server to reject, and it reads "GO5"
// as a repeat count of 5 while refusing "GO -- 5 items" because of the digit
// in the comment.
var goSeparatorLineCases = []struct {
	line string
	want bool
}{
	{"GO", true},
	{"go", true},
	{"Go", true},
	{"gO", true},
	{" GO ", true},
	{"\tGO\t", true},
	{"  go  ", true},
	{"GO 5", true},
	{"GO 5 ", true},
	{"GO\t10", true},
	{"GO 0", true},
	{"GO -- comment", true},
	{"GO--x", true},
	{"GO -- 5 items", true},
	{"GO 5 -- twice", true},
	{"", false},
	{" ", false},
	{"\t", false},
	{"G", false},
	{"O", false},
	{"G O", false},
	{"GOO", false},
	{"GOTO", false},
	{"gone", false},
	{"XGO", false},
	{"SELECT 1", false},
	{"GO5", false},
	{"GO_", false},
	{"GO x", false},
	{"GO 5x", false},
	{"GO;", false},
	{";GO", false},
	{"GO/*c*/", false},
	{"\uff27\uff2f", false},
}

func TestGoSeparatorLineCases(t *testing.T) {
	for _, tt := range goSeparatorLineCases {
		if got := isGoSeparatorLine([]rune(tt.line)); got != tt.want {
			t.Errorf("isGoSeparatorLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

// The bug this whole GO-detection rework exists for: a "GO" commented out
// with a block comment is not a batch separator, so the alias declared above
// it is still in scope and `p.` completes against it. Asserted as the scope
// boundary rather than as candidates, since the boundary is what was wrong.
//
// Stated as its own assertion rather than left to the golden file: the golden
// would happily freeze the broken answer if this ever regressed and someone
// regenerated without reading the diff.
func TestCommentedOutGoDoesNotStartANewBatch(t *testing.T) {
	cases := map[string]string{
		"block comment": "SELECT * FROM dbo.Patients p\n/*\nGO\n*/\nWHERE p.",
		"string literal": "SELECT * FROM dbo.Patients p WHERE note = 'x\nGO\ny'\n" +
			"AND p.",
		"bracket identifier": "SELECT * FROM dbo.[Pat\nGO\nients] p\nWHERE p.",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			lines := splitRunes(script)
			buf := flattenFresh(lines)
			row := len(lines) - 1
			got := ScanPrefix(lines, buf, row, OffsetForCursor(lines, row, len(lines[row])))
			if got.BatchStart != 0 {
				t.Errorf("batchStart = %d, want 0 — the commented-out GO was treated as a batch separator, "+
					"scoping completion past the table alias", got.BatchStart)
			}
		})
	}
}

// The inverse: a real GO still ends the batch, so an alias above it is gone.
func TestRealGoStillStartsANewBatch(t *testing.T) {
	script := "SELECT * FROM dbo.Patients p\nGO\nWHERE p."
	lines := splitRunes(script)
	buf := flattenFresh(lines)
	row := len(lines) - 1
	want := OffsetForCursor(lines, 2, 0)
	if got := ScanPrefix(lines, buf, row, OffsetForCursor(lines, row, len(lines[row]))).BatchStart; got != want {
		t.Errorf("batchStart = %d, want %d (the line after the GO)", got, want)
	}
}

// FlattenLinesInto reuses the caller's buffer across keystrokes, so a shorter
// script following a longer one must not leave the previous run's tail
// visible.
func TestFlattenLinesIntoReuseMatchesFreshAllocation(t *testing.T) {
	scripts := []string{
		"SELECT a, b, c FROM dbo.SomeVeryLongTableName WHERE x = 1 AND y = 2",
		"SELECT 1",
		"",
		"a\nb\nc",
		"SELECT * FROM T\nGO\nSELECT * FROM U",
		"x",
	}
	var reused []rune
	for _, script := range scripts {
		lines := splitRunes(script)
		want := flattenFresh(lines)
		reused = FlattenLinesInto(reused, lines)
		if string(reused) != string(want) {
			t.Fatalf("FlattenLinesInto(reused) = %q, want %q (stale tail from a previous call?)", string(reused), string(want))
		}
		if len(reused) != len(want) {
			t.Fatalf("len = %d, want %d", len(reused), len(want))
		}
	}
}

// The whole scan must be unaffected by whether its buffer was freshly
// allocated or recycled. Still differential, and legitimately so: both sides
// are the production scan, so there is no second implementation here.
func TestScanCompletionPrefixUnaffectedByBufferReuse(t *testing.T) {
	var reused []rune
	// Prime the buffer with the longest script so later shorter ones recycle it.
	longest := ""
	for _, s := range diffCorpus {
		if len(s) > len(longest) {
			longest = s
		}
	}
	reused = FlattenLinesInto(reused, splitRunes(longest))

	for name, script := range diffCorpus {
		t.Run(name, func(t *testing.T) {
			lines := splitRunes(script)
			fresh := flattenFresh(lines)
			reused = FlattenLinesInto(reused, lines)
			for row := range lines {
				for col := 0; col <= len(lines[row]); col++ {
					upTo := OffsetForCursor(lines, row, col)
					want := ScanPrefix(lines, fresh, row, upTo)
					got := ScanPrefix(lines, reused, row, upTo)
					if diff := compareScans(want, got); diff != "" {
						t.Fatalf("cursor (row=%d,col=%d) in %q: recycled buffer differs: %s", row, col, script, diff)
					}
				}
			}
		})
	}
}
