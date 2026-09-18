package sqlparse

import (
	"fmt"
	"strings"
	"testing"
)

// scopeAtCursor tokenizes sql, whose single '|' marks the cursor, and returns
// ScopeAt's answer there. The marker is removed before tokenizing, so offsets
// are the ones a real buffer would have.
func scopeAtCursor(t *testing.T, sql string) Scope {
	t.Helper()
	cursor := strings.Index(sql, "|")
	if cursor < 0 || strings.Count(sql, "|") != 1 {
		t.Fatalf("sql must hold exactly one '|' cursor marker: %q", sql)
	}
	buf := []rune(strings.Replace(sql, "|", "", 1))
	tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	return ScopeAt(tokens, len([]rune(sql[:cursor])))
}

// refNames renders a query's FROM refs as "[schema.]name[ alias]", a derived
// table as "(derived)[ alias]", so one string pins a whole clause.
func refNames(q *Query) string {
	if q == nil {
		return "<nil>"
	}
	parts := make([]string, 0, len(q.From))
	for _, r := range q.From {
		s := r.Name
		if r.Schema != "" {
			s = r.Schema + "." + r.Name
		}
		if r.Derived != nil {
			s = "(derived)"
		}
		if r.Alias != "" {
			s += " " + r.Alias
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// selectItems renders a select list the same way: "*", "q.*", "a.col",
// "col", each with "=alias" appended when one is present.
func selectItems(q *Query) string {
	if q == nil {
		return "<nil>"
	}
	parts := make([]string, 0, len(q.Select))
	for _, it := range q.Select {
		var s string
		switch {
		case it.Star && it.StarQual != "":
			s = it.StarQual + ".*"
		case it.Star:
			s = "*"
		case it.Qualifier != "":
			s = it.Qualifier + "." + it.Name
		default:
			s = it.Name
		}
		if it.Alias != "" {
			s += "=" + it.Alias
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func cteNames(s Scope) string {
	parts := make([]string, 0, len(s.CTEs))
	for _, c := range s.CTEs {
		n := c.Name
		if c.Columns != nil {
			n += "(" + strings.Join(c.Columns, ",") + ")"
		}
		parts = append(parts, n)
	}
	return strings.Join(parts, ", ")
}

var clauseNames = [...]string{"unknown", "table", "column"}

func clauseName(c Clause) string {
	if int(c) < len(clauseNames) {
		return clauseNames[c]
	}
	return fmt.Sprintf("clause%d", int(c))
}

// TestScopeAt pins the whole grammar the tree parser recognises: which query
// the cursor is in, what that query selects and reads from, and which CTEs are
// visible there.
func TestScopeAt(t *testing.T) {
	cases := []struct {
		name   string
		sql    string
		clause Clause
		from   string // refNames of the innermost query
		sel    string // selectItems of the innermost query
		ctes   string // cteNames of the scope
	}{{
		name:   "cursor in the main query of a WITH",
		sql:    "WITH t1 AS (SELECT * FROM sys.databases)\nSELECT | FROM t1",
		clause: ClauseColumn,
		from:   "t1",
		sel:    "",
		ctes:   "t1",
	}, {
		name:   "cursor in a CTE body sees that body's clause, not the outer one",
		sql:    "WITH t1 AS (SELECT * FROM |)\nSELECT * FROM t1",
		clause: ClauseTable,
		from:   "",
		sel:    "*",
		ctes:   "t1",
	}, {
		name:   "derived table keeps its own select list",
		sql:    "SELECT * FROM (SELECT name, state FROM sys.databases) d WHERE d.|",
		clause: ClauseColumn,
		from:   "(derived) d",
		sel:    "*",
		ctes:   "",
	}, {
		name:   "cursor inside the derived table",
		sql:    "SELECT * FROM (SELECT name, | FROM sys.databases) d",
		clause: ClauseColumn,
		from:   "sys.databases",
		sel:    "name",
		ctes:   "",
	}, {
		name:   "nested derived tables",
		sql:    "SELECT * FROM (SELECT x.a FROM (SELECT a, b FROM t) x) y WHERE y.|",
		clause: ClauseColumn,
		from:   "(derived) y",
		sel:    "*",
		ctes:   "",
	}, {
		name:   "explicit CTE column list",
		sql:    "WITH t1 (a, b) AS (SELECT name, state FROM sys.databases) SELECT | FROM t1",
		clause: ClauseColumn,
		from:   "t1",
		ctes:   "t1(a,b)",
	}, {
		name:   "a CTE referencing an earlier CTE",
		sql:    "WITH a AS (SELECT database_id id FROM sys.databases),\n     b AS (SELECT id FROM a)\nSELECT | FROM b",
		clause: ClauseColumn,
		from:   "b",
		ctes:   "a, b",
	}, {
		name:   "second CTE body sees the first",
		sql:    "WITH a AS (SELECT database_id id FROM sys.databases),\n     b AS (SELECT | FROM a)\nSELECT * FROM b",
		clause: ClauseColumn,
		from:   "a",
		ctes:   "a, b",
	}, {
		name:   "a recursive CTE parses without looping",
		sql:    "WITH r AS (SELECT id FROM r) SELECT | FROM r",
		clause: ClauseColumn,
		from:   "r",
		ctes:   "r",
	}, {
		name:   "WITH (NOLOCK) is a hint, not a CTE",
		sql:    "SELECT * FROM t1 WITH (NOLOCK) WHERE |",
		clause: ClauseColumn,
		from:   "t1",
		sel:    "*",
		ctes:   "",
	}, {
		name:   "a WITH that is not a CTE binding is abandoned whole",
		sql:    "WITH t1 AS SELECT name FROM sys.databases WHERE |",
		clause: ClauseColumn,
		from:   "sys.databases",
		sel:    "name",
		ctes:   "",
	}, {
		name:   "aliases, AS and bare, plus qualified stars",
		sql:    "SELECT a.*, b.name AS n, b.state s, | FROM x a JOIN dbo.y AS b ON a.id = b.id",
		clause: ClauseColumn,
		from:   "x a, dbo.y b",
		sel:    "a.*, b.name=n, b.state=s",
		ctes:   "",
	}, {
		name:   "select modifiers are not select items",
		sql:    "SELECT DISTINCT TOP 10 PERCENT name, | FROM sys.databases",
		clause: ClauseColumn,
		from:   "sys.databases",
		sel:    "name",
		ctes:   "",
	}, {
		name:   "an expression keeps only its alias",
		sql:    "SELECT COUNT(*) c, | FROM t",
		clause: ClauseColumn,
		from:   "t",
		sel:    "=c",
		ctes:   "",
	}, {
		name:   "CROSS APPLY introduces a ref",
		sql:    "SELECT * FROM t a CROSS APPLY dbo.f(a.id) x WHERE |",
		clause: ClauseColumn,
		from:   "t a, dbo.f x",
		sel:    "*",
		ctes:   "",
	}, {
		name:   "cursor inside an EXISTS predicate gets that subquery",
		sql:    "SELECT * FROM t WHERE EXISTS (SELECT * FROM u WHERE u.|)",
		clause: ClauseColumn,
		from:   "u",
		sel:    "*",
		ctes:   "",
	}, {
		name:   "cursor in a UNION's second branch",
		sql:    "SELECT a FROM x UNION ALL SELECT | FROM y",
		clause: ClauseColumn,
		from:   "y",
		sel:    "",
		ctes:   "",
	}, {
		name:   "cursor in a UNION's first branch",
		sql:    "SELECT | FROM x UNION ALL SELECT b FROM y",
		clause: ClauseColumn,
		from:   "x",
		sel:    "",
		ctes:   "",
	}, {
		name:   "no tokens at all",
		sql:    "|",
		clause: ClauseUnknown,
		from:   "<nil>",
		sel:    "<nil>",
		ctes:   "",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := scopeAtCursor(t, c.sql)
			if got := clauseName(s.Clause); got != clauseName(c.clause) {
				t.Errorf("clause = %s, want %s", got, clauseName(c.clause))
			}
			if got := refNames(s.Query); got != c.from {
				t.Errorf("from = %q, want %q", got, c.from)
			}
			if got := selectItems(s.Query); got != c.sel {
				t.Errorf("select = %q, want %q", got, c.sel)
			}
			if got := cteNames(s); got != c.ctes {
				t.Errorf("ctes = %q, want %q", got, c.ctes)
			}
		})
	}
}

// TestScopeAtDerivedShape reaches into the tree a derived ref carries, which
// the flat renderings above only show as "(derived)".
func TestScopeAtDerivedShape(t *testing.T) {
	s := scopeAtCursor(t, "SELECT * FROM (SELECT name, state FROM sys.databases) d WHERE d.|")
	if s.Query == nil || len(s.Query.From) != 1 || s.Query.From[0].Derived == nil {
		t.Fatalf("want one derived ref, got %q", refNames(s.Query))
	}
	d := s.Query.From[0].Derived
	if got := selectItems(d); got != "name, state" {
		t.Errorf("derived select = %q, want %q", got, "name, state")
	}
	if got := refNames(d); got != "sys.databases" {
		t.Errorf("derived from = %q, want %q", got, "sys.databases")
	}
}

// TestScopeAtCTEBody pins that a CTE's body is reachable from the scope's CTE
// list, which is how the resolver computes its columns.
func TestScopeAtCTEBody(t *testing.T) {
	s := scopeAtCursor(t, "WITH t1 AS (SELECT name, state FROM sys.databases) SELECT | FROM t1")
	if len(s.CTEs) != 1 || s.CTEs[0].Body == nil {
		t.Fatalf("want one CTE with a body, got %q", cteNames(s))
	}
	if got := selectItems(s.CTEs[0].Body); got != "name, state" {
		t.Errorf("CTE body select = %q, want %q", got, "name, state")
	}
	if got := refNames(s.CTEs[0].Body); got != "sys.databases" {
		t.Errorf("CTE body from = %q, want %q", got, "sys.databases")
	}
}

// TestScopeAtTerminatesOnPathologicalInput guards the parser's advance
// invariant: every loop consumes a token, so unbalanced or nonsense input ends
// rather than spinning. A hang here fails the package's test timeout.
func TestScopeAtTerminatesOnPathologicalInput(t *testing.T) {
	for _, sql := range []string{
		"(((((|", ")))))|", "WITH |", "WITH t1 AS (|", "SELECT ((((|",
		"SELECT * FROM ( ) x WHERE |", "WITH a AS (WITH b AS (SELECT | FROM x) SELECT * FROM b) SELECT * FROM a",
		"SELECT , , , FROM , WHERE |", "UNION UNION SELECT |",
	} {
		t.Run(sql, func(t *testing.T) { scopeAtCursor(t, sql) })
	}
}
