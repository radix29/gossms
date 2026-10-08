package sqlparse

import (
	"strings"
	"testing"
)

// TestClauseWordsAreNotAliases pins U1: a reserved clause word after a table
// reference is the clause, not the reference's alias. Taken for an alias, the
// table's own name stopped resolving ("SELECT Orders.| FROM Orders FOR JSON
// PATH" offered nothing) and what followed the clause was lost.
func TestClauseWordsAreNotAliases(t *testing.T) {
	cases := []struct {
		name, sql, want string
	}{
		{"FOR JSON", "SELECT Orders.| FROM Orders FOR JSON PATH", "Orders"},
		{"FOR XML", "SELECT | FROM dbo.Orders FOR XML AUTO", "dbo.Orders"},
		{"OPTION", "SELECT | FROM Orders OPTION (RECOMPILE)", "Orders"},
		{"TABLESAMPLE", "SELECT | FROM Orders TABLESAMPLE (10 PERCENT)", "Orders"},
		{"TABLESAMPLE after the alias", "SELECT o.| FROM Orders o TABLESAMPLE SYSTEM (10 PERCENT)", "Orders o"},
		{"next statement", "SELECT | FROM Orders\nIF @@ROWCOUNT = 0 PRINT 'none'", "Orders"},
		{"DELETE OUTPUT", "DELETE FROM Orders OUTPUT deleted.| WHERE id = 1", "Orders"},
		{"INSERT OUTPUT", "INSERT INTO Orders OUTPUT inserted.| VALUES (1)", "Orders"},
		{"MERGE USING", "MERGE INTO dbo.T USING dbo.S s ON |", "dbo.T, dbo.S s"},
		// OUTPUT and USING are legal aliases (probed on 17); only a following
		// name or '(' makes them the clause.
		{"output as an alias", "SELECT output.| FROM Orders output", "Orders output"},
		{"using as an alias", "SELECT | FROM Orders using WHERE 1 = 1", "Orders using"},
		{"quoted reserved word", "SELECT | FROM Orders [FOR]", "Orders FOR"},
		{"after AS", "SELECT | FROM Orders AS [OPTION]", "Orders OPTION"},
		// FOR SYSTEM_TIME sits between the name and the alias.
		{"AS OF a literal", "SELECT h.| FROM dbo.T FOR SYSTEM_TIME AS OF '2020-01-01' AS h", "dbo.T h"},
		{"AS OF a variable", "SELECT h.| FROM dbo.T FOR SYSTEM_TIME AS OF @d h", "dbo.T h"},
		{"FROM TO", "SELECT h.| FROM dbo.T FOR SYSTEM_TIME FROM @a TO @b h", "dbo.T h"},
		{"BETWEEN AND", "SELECT h.| FROM dbo.T FOR SYSTEM_TIME BETWEEN '2020' AND '2021' h", "dbo.T h"},
		{"CONTAINED IN", "SELECT h.| FROM dbo.T FOR SYSTEM_TIME CONTAINED IN ('2020', @b) h", "dbo.T h"},
		{"ALL then a join", "SELECT | FROM dbo.T FOR SYSTEM_TIME ALL h JOIN u ON h.id = u.id", "dbo.T h, u"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refNames(scopeAtCursor(t, c.sql).Query); got != c.want {
				t.Errorf("refs = %q, want %q", got, c.want)
			}
		})
	}
}

// TestClauseWordsEndASelectWithNoFrom: FOR and OPTION close a select list
// that has no FROM, rather than becoming the last item's alias.
func TestClauseWordsEndASelectWithNoFrom(t *testing.T) {
	for sql, want := range map[string]string{
		"SELECT @x AS v FOR XML PATH|":     "@x=v",
		"SELECT a, b OPTION (MAXDOP 1)|":   "a, b",
		"SELECT [for] FROM t WHERE |":      "for",
		"SELECT name [option] FROM t |":    "name=option",
		"SELECT name FOR JSON AUTO, ROOT|": "name",
	} {
		if got := selectItems(scopeAtCursor(t, sql).Query); got != want {
			t.Errorf("%q: select = %q, want %q", sql, got, want)
		}
	}
}

// TestParseFromScopeSharesTheAliasRule: the flat fallback reads aliases with
// the same rule.
func TestParseFromScopeSharesTheAliasRule(t *testing.T) {
	for sql, want := range map[string]string{
		"SELECT * FROM Orders FOR JSON PATH":         "Orders",
		"DELETE FROM Orders OUTPUT deleted.id":       "Orders",
		"MERGE dbo.T AS t USING dbo.S AS s ON 1 = 1": "dbo.T t, dbo.S s",
		"SELECT * FROM Orders output":                "Orders output",
	} {
		buf := []rune(sql)
		tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
		if got := refNames(&Query{From: ParseFromScope(tokens)}); got != want {
			t.Errorf("%q: refs = %q, want %q", sql, got, want)
		}
	}
}

// TestMergeScope pins U2: a MERGE's target and source are both in scope in
// every clause after them, and the THEN action is part of the statement.
func TestMergeScope(t *testing.T) {
	const head = "MERGE dbo.Target AS t\nUSING dbo.Source AS s\nON t.id = s.id\n"
	cases := []struct {
		name, sql string
		clause    Clause
	}{
		{"ON", "MERGE dbo.Target AS t USING dbo.Source AS s ON t.id = s.|", ClauseColumn},
		{"THEN UPDATE SET", head + "WHEN MATCHED THEN UPDATE SET t.x = s.|", ClauseColumn},
		{"WHEN MATCHED AND", head + "WHEN MATCHED AND s.| > 0 THEN DELETE", ClauseColumn},
		{"THEN INSERT VALUES", head + "WHEN NOT MATCHED THEN INSERT (id) VALUES (s.|)", ClauseColumn},
		{"INTO", "MERGE INTO dbo.Target AS t USING dbo.Source AS s ON |", ClauseColumn},
		{"derived source", "MERGE dbo.Target AS t USING (SELECT 1 AS id) AS s ON s.|", ClauseColumn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql := c.sql
			cursor := strings.Index(sql, "|")
			script := sql[:cursor] + sql[cursor+1:]
			lines := splitRunes(script)
			buf := flattenFresh(lines)
			upTo := len([]rune(sql[:cursor]))
			row := strings.Count(sql[:cursor], "\n")
			pre := ScanPrefix(lines, buf, row, upTo)
			start, _, fwd := NarrowStatementForward(lines, buf, row, pre.BatchStart, upTo, upTo, pre.Tokens)
			if start != 0 {
				t.Fatalf("statement starts at %d (%q), want the MERGE at 0", start, string(buf[start:]))
			}
			toks := append(append([]Token{}, TokensFrom(pre.Tokens, start)...), fwd...)
			scope := ScopeAt(toks, upTo)
			want := "dbo.Target t, dbo.Source s"
			if c.name == "derived source" {
				want = "dbo.Target t, (derived) s"
			}
			if got := refNames(scope.Query); got != want {
				t.Errorf("refs = %q, want %q", got, want)
			}
			if scope.Clause != c.clause {
				t.Errorf("clause = %s, want %s", clauseName(scope.Clause), clauseName(c.clause))
			}
		})
	}
}

// TestMergeSourceIsATableClause: right after USING the cursor names a table.
func TestMergeSourceIsATableClause(t *testing.T) {
	for _, sql := range []string{
		"MERGE dbo.Target AS t USING |",
		"MERGE INTO dbo.Target USING dbo.|",
	} {
		if got := scopeAtCursor(t, sql).Clause; got != ClauseTable {
			t.Errorf("%q: clause = %s, want table", sql, clauseName(got))
		}
		buf := []rune(strings.Replace(sql, "|", "", 1))
		tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
		if got := CurrentClause(tokens); got != ClauseTable {
			t.Errorf("%q: flat clause = %s, want table", sql, clauseName(got))
		}
	}
}

// TestMergeJoinHintIsNoStatement: "INNER MERGE JOIN" is a join hint, so the
// SELECT it sits in stays one statement.
func TestMergeJoinHintIsNoStatement(t *testing.T) {
	buf := []rune("SELECT a.x FROM a INNER MERGE JOIN b ON a.id = b.id")
	tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	if got := DMLStatementStarts(tokens); len(got) != 1 || got[0] != 0 {
		t.Errorf("statement starts = %v, want [0]", got)
	}
}

// TestQuotedIdentifiersAreUnescaped pins U3: a doubled closing delimiter
// inside a quoted identifier is one character of the name.
func TestQuotedIdentifiersAreUnescaped(t *testing.T) {
	for in, want := range map[string]string{
		"[a]]b]":   "a]b",
		`"a""b"`:   `a"b`,
		"[a]]]]b]": "a]]b",
		"[]]]":     "]",
		`[a"b]`:    `a"b`,
		`"a]b"`:    "a]b",
		"[plain]":  "plain",
	} {
		buf := []rune("SELECT " + in)
		tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
		if len(tokens) != 2 || tokens[1].Kind != TokenIdent || !tokens[1].Quoted {
			t.Fatalf("%s: tokens = %+v, want SELECT and one quoted identifier", in, tokens)
		}
		if tokens[1].Text != want {
			t.Errorf("%s: Text = %q, want %q", in, tokens[1].Text, want)
		}
	}
	if got := refNames(scopeAtCursor(t, "SELECT | FROM [dbo].[Order]]s] AS [x]]y]").Query); got != "dbo.Order]s x]y" {
		t.Errorf("refs = %q", got)
	}
}
