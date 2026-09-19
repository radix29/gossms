package sqlparse

import (
	"strings"
	"testing"
)

// pivotSpec renders a ref's PIVOT/UNPIVOT clause as one string:
// "PIVOT agg=Total for=Year in=[2005 2006]". "-" for a ref with no clause, so
// a test that expects none says so explicitly rather than by omission.
func pivotSpec(pv *Pivot) string {
	if pv == nil {
		return "-"
	}
	kind, head := "PIVOT", "agg="+pv.Agg
	if pv.Unpivot {
		kind, head = "UNPIVOT", "value="+pv.Value
	}
	return kind + " " + head + " for=" + pv.For + " in=[" + strings.Join(pv.In, " ") + "]"
}

// onlyRef returns the cursor's query's single FROM ref.
func onlyRef(t *testing.T, sql string) FromRef {
	t.Helper()
	scope := scopeAtCursor(t, sql)
	if scope.Query == nil {
		t.Fatalf("no query in scope for %q", sql)
	}
	if len(scope.Query.From) != 1 {
		t.Fatalf("got %d FROM refs (%s), want 1", len(scope.Query.From), refNames(scope.Query))
	}
	return scope.Query.From[0]
}

func TestParsePivotShapes(t *testing.T) {
	cases := []struct {
		name, sql, alias, pivot string
	}{{
		name:  "pivot",
		sql:   "SELECT | FROM dbo.Orders PIVOT (SUM(Total) FOR Year IN ([2005], [2006])) AS p",
		alias: "p",
		pivot: "PIVOT agg=Total for=Year in=[2005 2006]",
	}, {
		// The aggregate's argument is qualified, and the FOR column too: both
		// name the column with their last part.
		name:  "qualified names",
		sql:   "SELECT | FROM dbo.Orders o PIVOT (SUM(o.Total) FOR o.Year IN ([2005])) p",
		alias: "p",
		pivot: "PIVOT agg=Total for=Year in=[2005]",
	}, {
		name:  "unpivot",
		sql:   "SELECT | FROM dbo.Orders UNPIVOT (Amount FOR Quarter IN (Q1, Q2, Q3)) AS u",
		alias: "u",
		pivot: "UNPIVOT value=Amount for=Quarter in=[Q1 Q2 Q3]",
	}, {
		// A derived table keeps its own alias only until the clause; the
		// reference's alias is the pivoted one, which is all that is
		// addressable outside.
		name:  "derived source with its own alias",
		sql:   "SELECT | FROM (SELECT Total, Year FROM dbo.Orders) src PIVOT (SUM(Total) FOR Year IN ([2005])) AS p",
		alias: "p",
		pivot: "PIVOT agg=Total for=Year in=[2005]",
	}, {
		// COUNT(*) aggregates no column, so nothing is dropped from the
		// source: an empty Agg, not a guess.
		name:  "aggregate over no column",
		sql:   "SELECT | FROM dbo.Orders PIVOT (COUNT(*) FOR Year IN ([2005])) AS p",
		alias: "p",
		pivot: "PIVOT agg= for=Year in=[2005]",
	}, {
		// PIVOT is a legal alias. Without the '(' it is one.
		name:  "pivot as a plain alias",
		sql:   "SELECT | FROM dbo.Orders PIVOT",
		alias: "PIVOT",
		pivot: "-",
	}, {
		// No FOR: not a pivot clause at all, and the parse rewinds to what it
		// did before rather than keeping half a shape.
		name:  "malformed clause rewinds",
		sql:   "SELECT | FROM dbo.Orders PIVOT (SUM(Total)) AS p",
		alias: "PIVOT",
		pivot: "-",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref := onlyRef(t, c.sql)
			if ref.Alias != c.alias {
				t.Errorf("alias = %q, want %q", ref.Alias, c.alias)
			}
			if got := pivotSpec(ref.Pivot); got != c.pivot {
				t.Errorf("pivot = %q, want %q", got, c.pivot)
			}
		})
	}
}

// The clause ends where its own ')' does: a WHERE after it is the query's, not
// swallowed by the pivot parse.
func TestPivotClauseEndsAtItsOwnParen(t *testing.T) {
	scope := scopeAtCursor(t,
		"SELECT * FROM dbo.Orders PIVOT (SUM(Total) FOR Year IN ([2005])) AS p WHERE p.| = 1")
	if scope.Clause != ClauseColumn {
		t.Errorf("clause = %v, want ClauseColumn — the WHERE was swallowed by the pivot clause", scope.Clause)
	}
}
