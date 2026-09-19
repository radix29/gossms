package sqlparse

import (
	"fmt"
	"strings"
	"testing"
)

// benchScript builds a script of roughly n statements shaped like a real one:
// batch separators, comments, string literals and multi-line statements, so
// the tokenizer does representative work rather than racing through uniform
// filler.
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

// The completion provider re-flattens and re-scans the buffer from offset 0 on
// every keystroke while the popup is open (see sqlCompletionCandidates). These
// measure what that costs as the script grows, with the cursor at the end —
// the worst case, since the prefix scan runs from offset 0 to the cursor.
//
// The production path: ScanPrefix lexes the prefix without materialising
// tokens, then tokenizes only the cursor's statement. Compare against
// BenchmarkCompletionPrefixScanReference_* below, the
// tokenize-everything-then-discard approach it replaced.
func benchmarkPrefixScan(b *testing.B, stmts int) {
	lines := benchScript(stmts)
	row := len(lines) - 2
	col := len(lines[row])
	var reuse []rune // QueryPanel.completionBuf, kept across keystrokes
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reuse = FlattenLinesInto(reuse, lines)
		upTo := OffsetForCursor(lines, row, col)
		ScanPrefix(lines, reuse, row, upTo)
	}
}

func BenchmarkCompletionPrefixScan_100Stmts(b *testing.B)  { benchmarkPrefixScan(b, 100) }
func BenchmarkCompletionPrefixScan_1000Stmts(b *testing.B) { benchmarkPrefixScan(b, 1000) }

// The two above measure a cold scan — the first keystroke, the one case an
// incremental cache cannot help with. These four model typing into an open
// popup: an edit lands somewhere in the buffer and the prefix is rescanned,
// over and over. Two things separate them.
//
// The edit row:
//
//   - CursorLine: the edit is on the cursor's own line, at the end of a large
//     script. Every boundary in the prefix is below the edit, so this is the
//     case an incremental scan must turn into O(statement).
//   - FirstLine: the edit is on line 1, so no boundary survives and the scan
//     runs from 0 regardless — the guard rail: the worst case must not come
//     out slower than the uncached scan it replaces.
//
// And the path:
//
//   - Uncached: bare ScanPrefix, the code the cache replaced — the baseline
//     the cached pair is read against, and what the two cold benchmarks above
//     measure per keystroke.
//   - Cached: PrefixCache.Scan carrying a real revision, what QueryPanel does
//     on every keystroke.
//
// The edit alternates appending and removing a rune so the script neither
// grows without bound nor settles into an unchanged buffer, and it bumps the
// revision exactly as Document.setLine does — one version per edit, DirtyFrom
// at the edited row. editRow of -1 means the cursor's own line.
//
// Both cached benchmarks still pay for FlattenLinesInto, which is O(script)
// and which the cache does not touch: what is left after the cached numbers
// drop is mostly that copy, not lexing.
func benchmarkPrefixScanTyping(b *testing.B, stmts, editRow int, cached bool) {
	lines := benchScript(stmts)
	row := len(lines) - 2
	if editRow < 0 {
		editRow = row
	}
	var reuse []rune // QueryPanel.completionBuf, kept across keystrokes
	var cache PrefixCache
	doc := new(int) // stands in for the *Document the real revision carries
	var version uint64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			lines[editRow] = append(lines[editRow], 'x')
		} else {
			lines[editRow] = lines[editRow][:len(lines[editRow])-1]
		}
		version++
		col := len(lines[row])
		reuse = FlattenLinesInto(reuse, lines)
		upTo := OffsetForCursor(lines, row, col)
		if cached {
			cache.Scan(lines, reuse, row, upTo,
				TextRevision{Doc: doc, Version: version, DirtyFrom: editRow})
		} else {
			ScanPrefix(lines, reuse, row, upTo)
		}
	}
}

func BenchmarkCompletionPrefixScanTypingCursorLineUncached_1000Stmts(b *testing.B) {
	benchmarkPrefixScanTyping(b, 1000, -1, false)
}

func BenchmarkCompletionPrefixScanTypingFirstLineUncached_1000Stmts(b *testing.B) {
	benchmarkPrefixScanTyping(b, 1000, 0, false)
}

func BenchmarkCompletionPrefixScanTypingCursorLine_1000Stmts(b *testing.B) {
	benchmarkPrefixScanTyping(b, 1000, -1, true)
}

func BenchmarkCompletionPrefixScanTypingFirstLine_1000Stmts(b *testing.B) {
	benchmarkPrefixScanTyping(b, 1000, 0, true)
}

// benchmarkPrefixScanReference measures the approach ScanPrefix replaced —
// flatten into a fresh buffer, tokenize the whole prefix, then discard every
// token before the statement start — so a regression toward it stays obvious.
//
// It reconstructs that shape out of the production pieces. Only the ';'
// boundary is applied, not the GO scan: what this measures is the cost of
// materialising every token in the prefix and throwing most away, which is
// unchanged by where the discard line falls.
func benchmarkPrefixScanReference(b *testing.B, stmts int) {
	lines := benchScript(stmts)
	row := len(lines) - 2
	col := len(lines[row])
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := flattenFresh(lines)
		upTo := OffsetForCursor(lines, row, col)
		tokens, _, semiStart, _ := TokenizeRange(buf, 0, upTo, false)
		TokensFrom(tokens, semiStart)
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
// FROM-scope parsing would misread it as an identifier.
func TestKeywordsFitCanonicalScratch(t *testing.T) {
	for _, kw := range sqlKeywordList {
		if len(kw) > maxSQLKeywordLen {
			t.Errorf("keyword %q is %d chars, over maxSQLKeywordLen (%d) — raise the constant",
				kw, len(kw), maxSQLKeywordLen)
		}
	}
}

// The canonical lookup must agree with the list it derives from, for either
// input case, and must reject non-keywords and the non-ASCII words it skips.
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

// benchStatement is one statement of the shape the query tree exists for: a
// CTE chain over derived tables and a join, all of it inside parentheses the
// flat scan used to skip and the parser now walks.
func benchStatement() []Token {
	const sql = `WITH recent AS (
    SELECT o.CustomerID, o.OrderDate, o.Total
    FROM   dbo.Orders AS o
    WHERE  o.OrderDate > '2026-01-01'
), ranked AS (
    SELECT r.CustomerID, SUM(r.Total) AS Spend
    FROM   recent AS r
    GROUP BY r.CustomerID
)
SELECT c.Name, k.Spend, d.LastOrder
FROM   dbo.Customers AS c
JOIN   ranked AS k ON k.CustomerID = c.CustomerID
JOIN   (SELECT CustomerID, MAX(OrderDate) AS LastOrder FROM dbo.Orders GROUP BY CustomerID) AS d
       ON d.CustomerID = c.CustomerID
WHERE  c.Region = N'north' AND k.Spend > 1000`
	buf := []rune(sql)
	toks, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	return toks
}

// ScopeAt runs once per keystroke while the popup is open, on the cursor's
// statement only. ParseFromScope, the flat scan it replaced, is the reference:
// the tree parse costs more because it no longer skips paren contents, and a
// regression in that cost shows up here.
func BenchmarkScopeAt(b *testing.B) {
	toks := benchStatement()
	upTo := toks[len(toks)-1].Start
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScopeAt(toks, upTo)
	}
}

func BenchmarkParseFromScopeReference(b *testing.B) {
	toks := benchStatement()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseFromScope(toks)
	}
}
