package tui

import (
	"fmt"
	"strings"
	"testing"
)

// benchScript builds a script of roughly n statements, shaped like a real
// one: batch separators, comments, string literals, and multi-line
// statements, so the tokenizer does representative work rather than racing
// through uniform filler.
func benchScript(n int) [][]rune {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "-- report section %d\n", i)
		fmt.Fprintf(&b, "SELECT c.CustomerID, c.Name, o.OrderDate, o.Total\n")
		fmt.Fprintf(&b, "FROM   dbo.Customers AS c\n")
		fmt.Fprintf(&b, "JOIN   dbo.Orders    AS o ON o.CustomerID = c.CustomerID\n")
		fmt.Fprintf(&b, "WHERE  c.Region = N'north-%d' AND o.Total > %d;\n", i, i*10)
		if i%20 == 0 {
			b.WriteString("GO\n")
		}
	}
	lines := strings.Split(b.String(), "\n")
	out := make([][]rune, len(lines))
	for i, ln := range lines {
		out[i] = []rune(ln)
	}
	return out
}

// The completion provider re-flattens and re-scans the buffer from offset 0
// on every keystroke while the popup is open (see sqlCompletionCandidates).
// These benchmarks measure what that costs as the script grows, with the
// cursor at the end — the worst case, since the prefix scan runs from the
// start of the buffer to the cursor.
//
// This is the production path: scanCompletionPrefix lexes the prefix without
// materialising tokens, then tokenizes only the cursor's statement. Compare
// against BenchmarkCompletionPrefixScanReference_* below, which is the
// tokenize-everything-then-discard approach it replaced.
func benchmarkPrefixScan(b *testing.B, stmts int) {
	lines := benchScript(stmts)
	row := len(lines) - 2
	col := len(lines[row])
	var reuse []rune // QueryPanel.completionBuf, kept across keystrokes
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reuse = flattenLinesInto(reuse, lines)
		upTo := offsetForCursor(lines, row, col)
		scanCompletionPrefix(lines, reuse, row, upTo)
	}
}

func BenchmarkCompletionPrefixScan_100Stmts(b *testing.B)  { benchmarkPrefixScan(b, 100) }
func BenchmarkCompletionPrefixScan_1000Stmts(b *testing.B) { benchmarkPrefixScan(b, 1000) }

// benchmarkPrefixScanReference measures the approach scanCompletionPrefix
// replaced, so the difference stays visible and a regression toward it is
// obvious. referenceScanCompletionPrefix also backs the differential tests
// in completion_prefix_scan_test.go.
func benchmarkPrefixScanReference(b *testing.B, stmts int) {
	lines := benchScript(stmts)
	row := len(lines) - 2
	col := len(lines[row])
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := flattenLines(lines)
		upTo := offsetForCursor(lines, row, col)
		referenceScanCompletionPrefix(lines, buf, row, upTo)
	}
}

func BenchmarkCompletionPrefixScanReference_100Stmts(b *testing.B) {
	benchmarkPrefixScanReference(b, 100)
}
func BenchmarkCompletionPrefixScanReference_1000Stmts(b *testing.B) {
	benchmarkPrefixScanReference(b, 1000)
}

// sqlKeywordCanonical uppercases into a fixed-size stack array, so a keyword
// longer than it would be silently unrecognisable — clause detection and
// FROM-scope parsing would both quietly misread it as an identifier.
func TestKeywordsFitCanonicalScratch(t *testing.T) {
	for _, kw := range sqlKeywordList {
		if len(kw) > maxSQLKeywordLen {
			t.Errorf("keyword %q is %d chars, over maxSQLKeywordLen (%d) — raise the constant",
				kw, len(kw), maxSQLKeywordLen)
		}
	}
}

// The canonical lookup must agree with the list it's derived from,
// for either input case, and must reject non-keywords and the non-ASCII
// words it deliberately skips.
func TestSQLKeywordCanonicalMatchesTable(t *testing.T) {
	for _, in := range []string{"SELECT", "select", "SeLeCt", "from", "REFERENCES"} {
		got, ok := sqlKeywordCanonical([]rune(in), 0, len([]rune(in)))
		if !ok {
			t.Errorf("sqlKeywordCanonical(%q) reported not-a-keyword", in)
			continue
		}
		if want := strings.ToUpper(in); got != want {
			t.Errorf("sqlKeywordCanonical(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"Customers", "dbo", "", "sélect", "notakeywordatallreallylong"} {
		r := []rune(in)
		if got, ok := sqlKeywordCanonical(r, 0, len(r)); ok {
			t.Errorf("sqlKeywordCanonical(%q) = (%q, true), want not-a-keyword", in, got)
		}
	}
}
