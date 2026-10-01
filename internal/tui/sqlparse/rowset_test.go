package sqlparse

import (
	"strings"
	"testing"
)

// rowsetSpec renders a ref's rowset as "OPENJSON a:int b:nvarchar(50)", "-"
// for a ref that is no rowset function and "OPENJSON <unknown>" for one whose
// shape is unknown.
func rowsetSpec(rs *Rowset) string {
	if rs == nil {
		return "-"
	}
	if rs.Columns == nil {
		return rs.Function + " <unknown>"
	}
	parts := []string{rs.Function}
	for _, c := range rs.Columns {
		s := c.Name + ":" + c.Type
		if len(c.TypeArgs) > 0 {
			s += "(" + strings.Join(c.TypeArgs, ",") + ")"
		}
		if !c.Nullable {
			s += "!"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func TestParseRowsetShapes(t *testing.T) {
	cases := []struct {
		name, sql, alias, rowset string
	}{{
		// Paths and AS JSON are not part of the shape.
		name:   "openjson with list",
		sql:    "SELECT | FROM OPENJSON(@j) WITH (Id int '$.id', Name nvarchar(50) '$.name', Tags nvarchar(max) '$.tags' AS JSON) AS j",
		alias:  "j",
		rowset: "OPENJSON Id:int Name:nvarchar(50) Tags:nvarchar(max)",
	}, {
		name:   "openjson without list has the fixed shape",
		sql:    "SELECT | FROM OPENJSON(@j, '$.items') j",
		alias:  "j",
		rowset: "OPENJSON key:nvarchar(4000)! value:nvarchar(MAX) type:tinyint!",
	}, {
		name:   "openjson in cross apply, no alias keyword",
		sql:    "SELECT | FROM dbo.Orders o CROSS APPLY OPENJSON(o.Lines) WITH (Sku varchar(20), Qty decimal(10, 2)) l",
		alias:  "l",
		rowset: "OPENJSON Sku:varchar(20) Qty:decimal(10,2)",
	}, {
		name:   "openrowset bulk with list",
		sql:    "SELECT | FROM OPENROWSET(BULK 'C:\\data\\x.csv', FORMAT = 'CSV') WITH (Code char(3) COLLATE Latin1_General_BIN2 1, Amount money 2) AS r",
		alias:  "r",
		rowset: "OPENROWSET Code:char(3) Amount:money",
	}, {
		name:   "openrowset without list is unknown",
		sql:    "SELECT | FROM OPENROWSET(BULK 'C:\\x.bin', SINGLE_BLOB) AS b",
		alias:  "b",
		rowset: "OPENROWSET <unknown>",
	}, {
		name:   "openxml with list",
		sql:    "SELECT | FROM OPENXML(@h, '/root/row', 1) WITH (Id int '@id', Name sysname) x",
		alias:  "x",
		rowset: "OPENXML Id:int Name:sysname",
	}, {
		// The list never closes: no shape, rather than half of one. The alias
		// is lost with it, which resolves to the same nothing.
		name:   "half-typed list",
		sql:    "SELECT | FROM OPENJSON(@j) WITH (Id int, Name",
		alias:  "",
		rowset: "OPENJSON <unknown>",
	}, {
		// A schema-qualified name is a user function, whatever it is called.
		name:   "qualified name is not the built-in",
		sql:    "SELECT | FROM dbo.OPENJSON(@j) j",
		alias:  "j",
		rowset: "-",
	}, {
		name:   "a table hint is not a rowset",
		sql:    "SELECT | FROM dbo.Orders WITH (NOLOCK) o",
		alias:  "",
		rowset: "-",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The last ref: the function, after any table it is applied to.
			scope := scopeAtCursor(t, c.sql)
			if scope.Query == nil || len(scope.Query.From) == 0 {
				t.Fatalf("no FROM refs in scope for %q", c.sql)
			}
			ref := scope.Query.From[len(scope.Query.From)-1]
			if ref.Alias != c.alias {
				t.Errorf("alias = %q, want %q", ref.Alias, c.alias)
			}
			if got := rowsetSpec(ref.Rowset); got != c.rowset {
				t.Errorf("rowset = %q, want %q", got, c.rowset)
			}
		})
	}
}

// The WITH list ends where its own ')' does: what follows is the query's.
func TestRowsetListEndsAtItsOwnParen(t *testing.T) {
	scope := scopeAtCursor(t, "SELECT * FROM OPENJSON(@j) WITH (a int) AS j WHERE j.| = 1")
	if scope.Clause != ClauseColumn {
		t.Errorf("clause = %v, want ClauseColumn", scope.Clause)
	}
	if got := refNames(scope.Query); got != "OPENJSON j" {
		t.Errorf("refs = %q, want %q", got, "OPENJSON j")
	}
}

// A WITH followed by '(' is a hint or a rowset's list and starts nothing; a
// WITH naming a CTE still does, and a trailing one is not decided yet.
func TestDMLStatementStartsWithParenIsNotACTE(t *testing.T) {
	cases := []struct {
		sql  string
		want int
	}{
		{"SELECT * FROM t WITH (NOLOCK) WHERE a = 1", 1},
		{"SELECT * FROM OPENJSON(@j) WITH (a int) AS j", 1},
		{"INSERT INTO t WITH (TABLOCK) SELECT a FROM u", 1},
		{"UPDATE t WITH (ROWLOCK) SET a = 1", 1},
		{"SELECT a FROM t\nWITH c AS (SELECT 1 x) SELECT x FROM c", 2},
		{"SELECT a FROM t\nWITH XMLNAMESPACES ('urn:x' AS x) SELECT 1", 2},
		{"SELECT a FROM t\nWITH", 1},
	}
	for _, c := range cases {
		buf := []rune(c.sql)
		tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
		if got := DMLStatementStarts(tokens); len(got) != c.want {
			t.Errorf("%q: starts = %v, want %d", c.sql, got, c.want)
		}
	}
}
