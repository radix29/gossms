package sqlparse

import (
	"fmt"
	"strings"
	"testing"
)

// statementEndOffset finds where the statement containing the cursor ends: the
// next top-level ';' at or after upTo, the start of the next bare "GO" line
// after cursorRow, or len(buf), whichever comes first. It is what the
// completion provider ran before NarrowStatementForward, kept here as half of
// the reference that function is checked against: it lexes everything below
// the cursor up to that boundary, which in a script with no ';' is the rest of
// the batch (B11).
func statementEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	r := lexSQL(buf, upTo, len(buf), true, LexNormal, nil,
		goScan{lo: OffsetForCursor(lines, cursorRow+1, 0), hi: len(buf)}, nil, nil)
	end := r.boundary
	if r.firstGo >= 0 && r.firstGo < end {
		end = r.firstGo
	}
	return end
}

// narrowReference is the old three-pass composition — statementEndOffset,
// TokenizeRange over the whole span, NarrowToDMLStatement — plus the one thing
// NarrowStatementForward adds on purpose: a top-level forwardStatementEnders
// keyword after the cursor ends the statement too.
func narrowReference(lines [][]rune, buf []rune, row, batchStart, from, upTo int, prefix []Token) (start, end int, tail []Token) {
	batchEnd := statementEndOffset(lines, buf, row, from)
	fwd, _, _, _ := TokenizeRange(buf, from, batchEnd, false)
	combined := append(append([]Token{}, prefix...), fwd...)
	depth := 0
	for _, t := range combined {
		switch t.Kind {
		case TokenParenOpen:
			depth++
		case TokenParenClose:
			depth = max(depth-1, 0)
		case TokenKeyword:
			if depth == 0 && t.Start > upTo && t.Start < batchEnd && forwardStatementEnders[t.Text] {
				batchEnd = t.Start
			}
		}
		if batchEnd != statementEndOffset(lines, buf, row, from) {
			break
		}
	}
	start, end = NarrowToDMLStatement(combined, batchStart, batchEnd, upTo)
	for _, t := range fwd {
		if t.Start < end {
			tail = append(tail, t)
		}
	}
	return start, end, tail
}

// forwardFromFor mirrors the completion provider: inside an unterminated
// bracket identifier the forward scan resumes past its closing ']'.
func forwardFromFor(buf []rune, upTo int, state LexState) int {
	from := upTo
	if state == LexBracket {
		for from < len(buf) && buf[from] != ']' {
			from++
		}
		if from < len(buf) {
			from++
		}
	}
	return from
}

// TestNarrowStatementForwardMatchesReference sweeps every cursor position of
// the prefix-scan corpus (curated and generated) and checks the one-pass
// answer against the three-pass one.
func TestNarrowStatementForwardMatchesReference(t *testing.T) {
	scripts := append([]string{}, generatedScripts()...)
	for _, name := range sortedCorpusNames() {
		scripts = append(scripts, diffCorpus[name])
	}
	scripts = append(scripts, forwardCases...)
	checked := 0
	for _, script := range scripts {
		lines := splitRunes(script)
		buf := flattenFresh(lines)
		for row := range lines {
			for col := 0; col <= len(lines[row]); col++ {
				upTo := OffsetForCursor(lines, row, col)
				pre := ScanPrefix(lines, buf, row, upTo)
				if pre.State != LexNormal && pre.State != LexBracket {
					continue
				}
				from := forwardFromFor(buf, upTo, pre.State)
				// The reference sees the whole batch's prefix; the provider gets
				// it trimmed to the statement's leader (PrefixCache), which must
				// not change the answer.
				full, _, _, _ := TokenizeRangeFrom(buf, pre.BatchStart, upTo, false, LexNormal)
				ws, we, wt := narrowReference(lines, buf, row, pre.BatchStart, from, upTo, full)
				gs, ge, gt := NarrowStatementForward(lines, buf, row, pre.BatchStart, from, upTo, pre.Tokens)
				if gs != ws || ge != we || dumpTokens(gt) != dumpTokens(wt) {
					t.Fatalf("%q at %d,%d:\n got  %d..%d %s\n want %d..%d %s",
						script, row, col, gs, ge, dumpTokens(gt), ws, we, dumpTokens(wt))
				}
				checked++
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d cursor positions checked", checked)
	}
}

// forwardCases are shapes whose statement continues past a keyword that would
// otherwise start one, plus the boundaries that do end it.
var forwardCases = []string{
	"INSERT INTO t (a)\nSELECT x.a FROM x\nSELECT y.b FROM y",
	"INSERT INTO t (a) VALUES (1)\nSELECT y.b FROM y",
	"WITH c AS (SELECT a FROM t)\nSELECT c.a FROM c\nSELECT y.b FROM y",
	"SELECT a FROM t\nUNION SELECT b FROM u\nUNION ALL SELECT c FROM v\nEXCEPT SELECT d FROM w\nINTERSECT SELECT e FROM x\nSELECT f FROM y",
	"SELECT a FROM t WHERE a IN (SELECT b FROM u)\nSELECT c FROM v",
	"SELECT a FROM t\nDECLARE @x int\nSELECT b FROM u",
	"SELECT a FROM t\nCREATE TABLE #t (a int)\nDROP TABLE #t\nTRUNCATE TABLE u\nALTER TABLE u ADD b int",
	"SELECT a FROM t\nGO\nSELECT b FROM u",
	"SELECT a FROM t; SELECT b FROM u",
	"UPDATE t SET a = 1 FROM t JOIN u ON u.id = t.id\nDELETE FROM t\nMERGE t USING u ON 1 = 1",
	"SELECT a FROM [t\nSELECT b FROM u",
	"SELECT a FROM t WITH (NOLOCK) WHERE a = 1\nSELECT b FROM u",
	"SELECT j.a FROM OPENJSON(@j) WITH (a int '$.a') AS j WHERE j.a = 1\nWITH c AS (SELECT 1 x) SELECT x FROM c",
	"INSERT INTO t WITH (TABLOCK) (a)\nSELECT x.a FROM x\nSELECT y.b FROM y",
}

// The cases the plan names, asserted by content rather than only against the
// reference: the forward tokens of the cursor's statement, as source text.
func TestNarrowStatementForwardContinuations(t *testing.T) {
	cases := []struct {
		sql, want string // sql holds one '|'; want is buf[start:end], trimmed
	}{
		{"INSERT INTO t (a) |\nSELECT x.a FROM x\nSELECT y.b FROM y", "INSERT INTO t (a) \nSELECT x.a FROM x"},
		{"WITH c AS (SELECT a FROM t)\nSELECT c.| FROM c\nSELECT y.b FROM y", "WITH c AS (SELECT a FROM t)\nSELECT c. FROM c"},
		{"WITH c AS (SELECT | FROM t)\nSELECT c.a FROM c\nSELECT y.b FROM y", "WITH c AS (SELECT  FROM t)\nSELECT c.a FROM c"},
		{"SELECT | FROM t\nUNION SELECT b FROM u\nUNION ALL SELECT c FROM v\nSELECT f FROM y", "SELECT  FROM t\nUNION SELECT b FROM u\nUNION ALL SELECT c FROM v"},
		{"SELECT a FROM t\nUNION |SELECT b FROM u\nSELECT f FROM y", "SELECT a FROM t\nUNION SELECT b FROM u"},
		{"SELECT | FROM t WHERE a IN (SELECT b FROM u)\nSELECT c FROM v", "SELECT  FROM t WHERE a IN (SELECT b FROM u)"},
		{"SELECT | FROM t\nDECLARE @x int\nSELECT b FROM u", "SELECT  FROM t"},
		{"SELECT | FROM t\nCREATE TABLE #t (a int)", "SELECT  FROM t"},
		{"SELECT | FROM t\nINSERT INTO u EXEC p", "SELECT  FROM t"},
		{"INSERT INTO u (a) |EXEC p\nSELECT 1", "INSERT INTO u (a) EXEC p"},
		{"SELECT | FROM t WITH (NOLOCK) WHERE a = 1\nSELECT b FROM u", "SELECT  FROM t WITH (NOLOCK) WHERE a = 1"},
		{"SELECT j.a FROM OPENJSON(@j) WITH (a int) AS j WHERE j.|\nSELECT b FROM u", "SELECT j.a FROM OPENJSON(@j) WITH (a int) AS j WHERE j."},
		{"SELECT a FROM t\nWITH c AS (SELECT | FROM u) SELECT x FROM c", "WITH c AS (SELECT  FROM u) SELECT x FROM c"},
	}
	for _, c := range cases {
		cur := strings.IndexByte(c.sql, '|')
		script := c.sql[:cur] + c.sql[cur+1:]
		lines := splitRunes(script)
		buf := flattenFresh(lines)
		upTo := len([]rune(c.sql[:cur]))
		row := strings.Count(c.sql[:cur], "\n")
		pre := ScanPrefix(lines, buf, row, upTo)
		start, end, _ := NarrowStatementForward(lines, buf, row, pre.BatchStart, upTo, upTo, pre.Tokens)
		if got := strings.TrimSpace(string(buf[start:end])); got != strings.TrimSpace(c.want) {
			t.Errorf("%q: statement = %q, want %q", c.sql, got, c.want)
		}
	}
}

// TestNarrowStatementForwardStopsEarly is B11's point: with no ';' anywhere,
// the forward lex must stop at the next statement, not run to the end.
func TestNarrowStatementForwardStopsEarly(t *testing.T) {
	var b strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&b, "SELECT c.Name, c.Id FROM dbo.Customers c WHERE c.Id = %d\n", i)
	}
	lines := splitRunes(b.String())
	buf := flattenFresh(lines)
	upTo := OffsetForCursor(lines, 1, 9)
	pre := ScanPrefix(lines, buf, 1, upTo)
	_, end, tail := NarrowStatementForward(lines, buf, 1, pre.BatchStart, upTo, upTo, pre.Tokens)
	if want := OffsetForCursor(lines, 2, 0); end != want {
		t.Errorf("end = %d, want %d (the next line's SELECT)", end, want)
	}
	if len(tail) > 20 {
		t.Errorf("%d forward tokens kept, want only the cursor's statement's", len(tail))
	}
}
