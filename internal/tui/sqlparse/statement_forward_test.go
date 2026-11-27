package sqlparse

import (
	"fmt"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/sqltext"
)

// statementEndOffset finds where the statement containing the cursor ends: the
// next top-level ';' at or after upTo, the start of the next bare "GO" line
// after cursorRow, or len(buf), whichever comes first. It is what the
// completion provider ran before NarrowStatementForward, kept here as half of
// the reference that function is checked against: it lexes everything below
// the cursor up to that boundary, which in a script with no ';' is the rest of
// the batch (B11).
func statementEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	r := lexSQL(buf, upTo, len(buf), true, nil,
		goScan{lo: OffsetForCursor(lines, cursorRow+1, 0), hi: len(buf)}, nil, nil)
	end := r.boundary
	if r.firstGo >= 0 && r.firstGo < end {
		end = r.firstGo
	}
	return end
}

// narrowReference is the old three-pass composition — statementEndOffset,
// TokenizeRange over the whole span, NarrowToDMLStatement — plus the one thing
// NarrowStatementForward adds on purpose: a top-level sqltext.EndsDML
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
			if depth == 0 && t.Start > upTo && t.Start < batchEnd && sqltext.EndsDML(t.Text) {
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
				full, _, _, _ := TokenizeRange(buf, pre.BatchStart, upTo, false)
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
	"FROM t JOIN u ON t.id = u.id SELECT t.a\nSELECT b FROM v",
	"FROM t SELECT a UNION ALL FROM u SELECT b\nSELECT c FROM v",
	"WITH c AS (FROM t SELECT a) FROM c SELECT a",
	"FROM t WHERE a = 1\nSELECT b FROM u",
	"WITH c AS (FROM t) FROM c ORDER BY ALL\nSELECT b FROM u",
	"INSERT INTO archive FROM t WHERE a = 1\nSELECT b FROM u",
	"FROM t SELECT a\nFROM u SELECT b",
	"SELECT a FROM t\nFROM u SELECT b",
	"FROM t SELECT a UNION SELECT b FROM u\nFROM v SELECT c",
	"SELECT * FROM t FOR SYSTEM_TIME FROM @a TO @b",
	"DELETE FROM t FROM t JOIN u ON t.a = u.a",
	"FROM [\u540d] AS [FROM] /* SELECT */ SELECT [FROM].a\nFROM [\u03b2] SELECT b",
	"FROM t AS window SELECT a WINDOW w AS (ORDER BY a) QUALIFY a > 0\nFROM u SELECT b",
	"FROM t FOR JSON PATH\nSELECT a FROM u",
	"FROM t SELECT a UNION ALL FROM u WHERE b = 1\nSELECT c FROM v",
	"INSERT INTO t VALUES (1)\nFROM u SELECT b",
	"INSERT INTO t EXEC p\nFROM u SELECT b",
	"FROM t JOIN u ON t.a IS DISTINCT FROM u.a SELECT t.a",
	"FROM t JOIN u ON t.a IS NOT DISTINCT FROM u.a SELECT t.a",
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
	"CREATE TYPE dbo.X FROM int NOT NULL\nSELECT b FROM u",
}

// The cases the plan names, asserted by content rather than only against the
// reference: the forward tokens of the cursor's statement, as source text.
func TestNarrowStatementForwardContinuations(t *testing.T) {
	cases := []struct {
		sql, want string // sql holds one '|'; want is buf[start:end], trimmed
	}{
		{"FROM t JOIN u ON t.id = u.id SELECT |\nSELECT b FROM v", "FROM t JOIN u ON t.id = u.id SELECT "},
		{"FROM t |SELECT a\nSELECT b FROM v", "FROM t SELECT a"},
		{"FROM t WHERE a = |1\nSELECT b FROM v", "FROM t WHERE a = 1"},
		{"FROM t GROUP BY |a\nSELECT b FROM v", "FROM t GROUP BY a"},
		{"FROM t HAVING COUNT(*) > |0\nSELECT b FROM v", "FROM t HAVING COUNT(*) > 0"},
		{"FROM t QUALIFY a = |1\nSELECT b FROM v", "FROM t QUALIFY a = 1"},
		{"FROM t ORDER BY |ALL\nSELECT b FROM v", "FROM t ORDER BY ALL"},
		{"FROM t WINDOW w AS (ORDER BY |a)\nSELECT b FROM v", "FROM t WINDOW w AS (ORDER BY a)"},
		{"FROM t FOR JSON |PATH\nSELECT b FROM v", "FROM t FOR JSON PATH"},
		{"FROM t SELECT |a\nFROM u SELECT b", "FROM t SELECT a"},
		{"FROM t SELECT a\nFROM u SELECT |b", "FROM u SELECT b"},
		{"SELECT |a FROM t\nFROM u SELECT b", "SELECT a FROM t"},
		{"SELECT * FROM t FOR SYSTEM_TIME FROM @a TO @b WHERE |a = 1", "SELECT * FROM t FOR SYSTEM_TIME FROM @a TO @b WHERE a = 1"},
		{"DELETE FROM t FROM t JOIN u ON t.a = u.a WHERE |a = 1", "DELETE FROM t FROM t JOIN u ON t.a = u.a WHERE a = 1"},
		{"INSERT INTO t VALUES (1)\nFROM u SELECT |b", "FROM u SELECT b"},
		{"INSERT INTO t EXEC p\nFROM u SELECT |b", "FROM u SELECT b"},
		{"WITH c AS (FROM t) FROM c WHERE a = |1\nSELECT b FROM v", "WITH c AS (FROM t) FROM c WHERE a = 1"},
		{"INSERT INTO archive FROM t WHERE a = |1\nSELECT b FROM v", "INSERT INTO archive FROM t WHERE a = 1"},
		{"FROM t SELECT a UNION ALL FROM u SELECT |\nSELECT c FROM v", "FROM t SELECT a UNION ALL FROM u SELECT "},
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
		// An alias type's FROM is its base type, not a FROM-first query.
		{"CREATE TYPE dbo.X FROM |int\nSELECT b FROM u", "CREATE TYPE dbo.X FROM int"},
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

// TestDMLEndersAreKeywords pins what NarrowToDMLStatement and the completion
// provider's DML-ender scan rely on to test t.Text without upper-casing it:
// every sqltext.EndsDML word, in any case, is a TokenKeyword whose Text is
// the upper-case spelling, and a quoted one is not a keyword (O1).
func TestDMLEndersAreKeywords(t *testing.T) {
	buf := []rune("declare Create aLTER drop TRUNCATE [create]")
	toks, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	if len(toks) != 6 {
		t.Fatalf("got %d tokens, want 6: %+v", len(toks), toks)
	}
	for _, tok := range toks[:5] {
		if tok.Kind != TokenKeyword || !sqltext.EndsDML(tok.Text) {
			t.Errorf("%+v: want a keyword token sqltext.EndsDML accepts", tok)
		}
	}
	if q := toks[5]; q.Kind == TokenKeyword || !q.Quoted {
		t.Errorf("%+v: a bracketed [create] must be a quoted identifier", q)
	}
}

func TestSecurityStatementBounds(t *testing.T) {
	for _, tc := range []struct {
		sql       string
		reference bool
	}{
		{"[[START]]GRANT SELECT ON dbo.t TO u | WITH GRANT OPTION\n[[END]]SELECT 1", true},
		{"GRANT SELECT ON dbo.t TO u WITH GRANT OPTION\n[[START]]SELECT | 1[[END]]", true},
		{"GRANT SELECT ON dbo.t TO u\n[[START]]WITH c AS (SELECT 1 AS n) SELECT | n FROM c[[END]]", true},
		{"[[START]]REVOKE GRANT OPTION FOR SELECT ON dbo.t FROM u | CASCADE\n[[END]]SELECT 1", true},
		{"SELECT 1\n[[START]]GRANT SELECT | ON dbo.t TO u[[END]]", true},
		{"UPDATE dbo.t SET n = 1\n[[START]]DENY UPDATE | ON dbo.t TO u[[END]]", true},
		{"SELECT 1\n[[START]]REVOKE SELECT | ON dbo.t FROM u[[END]]", true},
		{"SELECT 1\n[[START]]GRANT |[[END]]", false},
		{"CREATE PROCEDURE p AS [[START]]GRANT |[[END]]", false},
		{"CREATE OR ALTER PROCEDURE p AS [[START]]DENY |[[END]]", false},
		{"BEGIN\n[[START]]GRANT |\nEND[[END]]", false},
		{"BEGIN [[START]]GRANT |[[END]]", false},
		{"IF 1=1 [[START]]GRANT |[[END]]", false},
		{"IF 1=1 SELECT 1 ELSE [[START]]GRANT |[[END]]", false},
		{"[[START]]SELECT CASE WHEN 1=1 THEN\n grant\n ELSE\n deny\n END FROM dbo.t WHERE | n = 1[[END]]", true},
		{"IF EXISTS (SELECT 1) [[START]]DENY |[[END]]", false},
		{"WHILE 1=1 [[START]]REVOKE |[[END]]", false},
		{"IF 1=1 [[START]]SELECT n AS\n grant,\n | n FROM dbo.t[[END]]", true},
		{"[[START]]SELECT * FROM dbo.t\nrevoke\nINNER JOIN dbo.u ON u.n = revoke.n WHERE | n = 1[[END]]", true},
		{"[[START]]SELECT * FROM dbo.t\ndeny\nORDER BY | n[[END]]", true},
		{"[[START]]SELECT * FROM dbo.t\ngrant\nGROUP BY | n[[END]]", true},
		{"[[START]]SELECT * FROM dbo.t\nrevoke\nUNION SELECT | 1[[END]]", true},
		{"[[START]]SELECT * FROM dbo.t\ndeny\nOPTION | (RECOMPILE)[[END]]", true},
		{"[[START]]SELECT * FROM dbo.t\nrevoke\nFOR XML | PATH[[END]]", true},
		{"[[START]]SELECT n,\n grant,\n | n FROM dbo.t[[END]]", true},
		{"[[START]]SELECT (GRANT SELECT ON dbo.t TO u) | FROM dbo.t[[END]]", true},
		{"[[START]]SELECT [grant], | n FROM dbo.t[[END]]", true},
		{"[[START]]SELECT * FROM (SELECT [deny] FROM dbo.t) q WHERE | n = 1[[END]]", true},
		{"GRANT SELECT ON dbo.t\n[[START]]SELECT | n FROM dbo.t[[END]]", false},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			clean := func(s string) string {
				for _, marker := range []string{"[[START]]", "[[END]]", "|"} {
					s = strings.ReplaceAll(s, marker, "")
				}
				return s
			}
			offset := func(marker string) int {
				i := strings.Index(tc.sql, marker)
				if i < 0 {
					t.Fatalf("missing marker %s", marker)
				}
				return len([]rune(clean(tc.sql[:i])))
			}
			wantStart, wantEnd, upTo := offset("[[START]]"), offset("[[END]]"), offset("|")
			lines := splitRunes(clean(tc.sql))
			buf := flattenFresh(lines)
			row := strings.Count(string(buf[:upTo]), "\n")
			pre := ScanPrefix(lines, buf, row, upTo)
			start, end, _ := NarrowStatementForward(lines, buf, row, pre.BatchStart, upTo, upTo, pre.Tokens)
			if start != wantStart || end != wantEnd {
				t.Fatalf("forward bounds %d..%d, want %d..%d", start, end, wantStart, wantEnd)
			}
			if tc.reference {
				all, _, _, _ := TokenizeRange(buf, pre.BatchStart, len(buf), false)
				start, end := NarrowToDMLStatement(all, pre.BatchStart, len(buf), upTo)
				if start != wantStart || end != wantEnd {
					t.Fatalf("token bounds %d..%d, want %d..%d", start, end, wantStart, wantEnd)
				}
			}
		})
	}
}
