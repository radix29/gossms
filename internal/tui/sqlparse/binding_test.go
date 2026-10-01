package sqlparse

import (
	"fmt"
	"strings"
	"testing"
)

// tokenTexts renders a whole script's identifier tokens, so a test can pin
// what the tokenizer kept of a sigil without reading offsets.
func tokenTexts(sql string) []string {
	buf := []rune(sql)
	tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	var out []string
	for _, t := range tokens {
		if t.Kind == TokenIdent {
			out = append(out, t.Text)
		}
	}
	return out
}

// Dropping the sigil made "#Orders" and a catalog table named "Orders"
// indistinguishable, which is how "FROM #Orders" came to offer that table's
// columns.
func TestTokenizerKeepsSigils(t *testing.T) {
	cases := []struct{ sql, want string }{
		{"SELECT * FROM #t", "#t"},
		{"SELECT a FROM ##global", "a ##global"},
		{"DECLARE @t TABLE (a int)", "@t a int"},
		{"SELECT @@ROWCOUNT", "@@ROWCOUNT"},
		{"SELECT TOP @n * FROM T", "@n T"},
		// A sigil with nothing after it is still a token: it is the first
		// keystroke of a name, and the completion popup replaces what it
		// finds there.
		{"SELECT a FROM T WHERE x = @", "a T x @"},
		{"SELECT * FROM #", "#"},
		// A bracketed temp table reports the same text as a bare one, so both
		// spellings resolve to the same binding.
		{"SELECT * FROM [#t]", "#t"},
	}
	for _, c := range cases {
		if got := strings.Join(tokenTexts(c.sql), " "); got != c.want {
			t.Errorf("%q: idents = %q, want %q", c.sql, got, c.want)
		}
	}
}

// bindingSpecs renders ScanBindings' answer as one line per binding:
// "#t: a int not null, b nvarchar(50)" for a declared shape, "#t: <query>"
// for a SELECT ... INTO.
func bindingSpecs(t *testing.T, sql string) []string {
	t.Helper()
	buf := []rune(sql)
	tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	return specsOf(ScanBindings(tokens))
}

// specsOf renders bindings the way bindingSpecs does.
func specsOf(bindings []Binding) []string {
	var out []string
	for _, b := range bindings {
		if b.Query != nil {
			out = append(out, b.Name+": <query "+refNames(b.Query)+">")
			continue
		}
		parts := make([]string, 0, len(b.Columns))
		for _, c := range b.Columns {
			s := c.Name + " " + c.Type
			if len(c.TypeArgs) > 0 {
				s += "(" + strings.Join(c.TypeArgs, ",") + ")"
			}
			if !c.Nullable {
				s += " not null"
			}
			parts = append(parts, s)
		}
		out = append(out, b.Name+": "+strings.Join(parts, ", "))
	}
	return out
}

func TestScanBindings(t *testing.T) {
	cases := []struct {
		name, sql string
		want      []string
	}{{
		name: "create temp table",
		sql:  "CREATE TABLE #t (Id int NOT NULL, Name nvarchar(50), Note varchar(MAX))",
		want: []string{"#t: Id int not null, Name nvarchar(50), Note varchar(MAX)"},
	}, {
		name: "declare table variable",
		sql:  "DECLARE @t TABLE (Id int, Amount decimal(18, 2) NOT NULL)",
		want: []string{"@t: Id int, Amount decimal(18,2) not null"},
	}, {
		name: "declare with AS",
		sql:  "DECLARE @t AS TABLE (Id int)",
		want: []string{"@t: Id int"},
	}, {
		name: "global temp table",
		sql:  "CREATE TABLE ##g (Id int)",
		want: []string{"##g: Id int"},
	}, {
		// A table-level constraint is not a column.
		name: "table constraints skipped",
		sql:  "CREATE TABLE #t (Id int NOT NULL, PRIMARY KEY (Id), CONSTRAINT ck CHECK (Id > 0), UNIQUE (Id))",
		want: []string{"#t: Id int not null"},
	}, {
		name: "select into",
		sql:  "SELECT c.Id, c.Name INTO #t FROM dbo.Customers c",
		want: []string{"#t: <query dbo.Customers c>"},
	}, {
		// The table already exists; the insert says nothing about its shape.
		name: "insert into is not a binding",
		sql:  "INSERT INTO #t (Id) VALUES (1)",
		want: nil,
	}, {
		// An ordinary table's columns come from the catalog.
		name: "create of a real table is not a binding",
		sql:  "CREATE TABLE dbo.Real (Id int)",
		want: nil,
	}, {
		name: "several declarations",
		sql:  "CREATE TABLE #a (x int)\nDECLARE @b TABLE (y int)\nSELECT * INTO #c FROM #a",
		want: []string{"#a: x int", "@b: y int", "#c: <query #a>"},
	}, {
		// A scalar DECLARE is the common case and binds nothing.
		name: "scalar declare",
		sql:  "DECLARE @n int = 3",
		want: nil,
	}, {
		// A CREATE TABLE inside a parenthesised anything is not top level, and
		// a half-typed one never closes its group.
		name: "unclosed column list",
		sql:  "CREATE TABLE #t (Id int",
		want: nil,
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bindingSpecs(t, c.sql)
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("bindings = %v, want %v", got, c.want)
			}
		})
	}
}

// The INTO target is not part of what fills the table, so it must not stay in
// the bound query's own FROM list — otherwise "SELECT * INTO #t FROM Orders"
// would resolve #t partly against itself.
func TestSelectIntoDropsItsOwnTarget(t *testing.T) {
	got := bindingSpecs(t, "SELECT * INTO #t FROM dbo.Orders o")
	want := "#t: <query dbo.Orders o>"
	if len(got) != 1 || got[0] != want {
		t.Errorf("bindings = %v, want [%q]", got, want)
	}
}

// carriedSpecs folds CarryTempBindings over every batch of script but the
// last, the one a cursor at the end would be in, splitting on GO lines the way
// BatchCache does.
func carriedSpecs(script string) []string {
	buf := []rune(script)
	var carried []Binding
	start := 0
	lexSQL(buf, 0, len(buf), false, LexNormal, nil, allLines(buf), nil, func(lineStart, goNext int) bool {
		if goNext >= 0 {
			tokens, _, _, _ := TokenizeRange(buf, start, lineStart, false)
			carried = CarryTempBindings(carried, tokens)
			start = goNext
		}
		return false
	})
	return specsOf(carried)
}

// A temp table outlives GO; a table variable does not (N1a).
func TestCarryTempBindings(t *testing.T) {
	cases := []struct {
		name, sql string
		want      []string
	}{{
		name: "temp table carried, variable not",
		sql:  "CREATE TABLE #t (Id int)\nDECLARE @v TABLE (x int)\nGO\nSELECT * FROM #t",
		want: []string{"#t: Id int"},
	}, {
		name: "through several batches",
		sql:  "CREATE TABLE #t (Id int)\nGO\nSELECT 1\nGO\nCREATE TABLE ##g (y int)\nGO\n",
		want: []string{"#t: Id int", "##g: y int"},
	}, {
		name: "select into carried",
		sql:  "SELECT Id INTO #t FROM dbo.Orders\nGO\n",
		want: []string{"#t: <query dbo.Orders>"},
	}, {
		// Resolved in the cursor's batch, @v would be another variable or none.
		name: "select into from a table variable not carried",
		sql:  "DECLARE @v TABLE (x int)\nSELECT * INTO #t FROM @v\nGO\n",
		want: nil,
	}, {
		name: "select into reading a variable in a derived table not carried",
		sql:  "DECLARE @v TABLE (x int)\nSELECT * INTO #t FROM (SELECT x FROM @v) d\nGO\n",
		want: nil,
	}, {
		name: "dropped in a later batch",
		sql:  "CREATE TABLE #t (Id int)\nCREATE TABLE #u (z int)\nGO\nDROP TABLE #t\nGO\n",
		want: []string{"#u: z int"},
	}, {
		name: "drop if exists, a list, qualified",
		sql:  "CREATE TABLE #a (x int)\nCREATE TABLE #b (x int)\nCREATE TABLE #c (x int)\nGO\nDROP TABLE IF EXISTS #a, tempdb..#b\nGO\n",
		want: []string{"#c: x int"},
	}, {
		// Order within a batch decides: the usual re-runnable script.
		name: "drop then recreate",
		sql:  "CREATE TABLE #t (old int)\nGO\nIF OBJECT_ID('tempdb..#t') IS NOT NULL DROP TABLE #t\nCREATE TABLE #t (new int)\nGO\n",
		want: []string{"#t: new int"},
	}, {
		name: "create then drop in one batch",
		sql:  "CREATE TABLE #t (Id int)\nDROP TABLE #t\nGO\n",
		want: nil,
	}, {
		name: "redeclared in a later batch replaces",
		sql:  "CREATE TABLE #t (old int)\nGO\nCREATE TABLE #T (new int)\nGO\n",
		want: []string{"#T: new int"},
	}, {
		name: "drop is case-insensitive",
		sql:  "CREATE TABLE #Tmp (x int)\nGO\nDROP TABLE #tmp\nGO\n",
		want: nil,
	}, {
		// A module's temp tables die when it returns.
		name: "procedure body not carried",
		sql:  "CREATE PROCEDURE dbo.p AS\nCREATE TABLE #t (Id int)\nGO\n",
		want: nil,
	}, {
		name: "create or alter proc body not carried",
		sql:  "CREATE OR ALTER PROC dbo.p AS\nCREATE TABLE #t (Id int)\nGO\n",
		want: nil,
	}, {
		name: "trigger and function bodies not carried",
		sql:  "CREATE TABLE #keep (x int)\nGO\nALTER TRIGGER tr ON t AFTER INSERT AS DROP TABLE #keep\nGO\nCREATE FUNCTION f() RETURNS int AS BEGIN CREATE TABLE #f (x int) RETURN 1 END\nGO\n",
		want: []string{"#keep: x int"},
	}, {
		// A real table is the catalog's.
		name: "drop of a real table touches nothing",
		sql:  "CREATE TABLE #t (Id int)\nGO\nDROP TABLE dbo.t\nGO\n",
		want: []string{"#t: Id int"},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := carriedSpecs(c.sql); fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("carried = %v, want %v", got, c.want)
			}
		})
	}
}

// CarryTempBindings hands back a new slice: the cache keeps the input as its
// fold state, and the bindings it returns are appended to.
func TestCarryTempBindingsLeavesItsInputAlone(t *testing.T) {
	buf := []rune("DROP TABLE #a\nCREATE TABLE #c (x int)")
	tokens, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
	in := []Binding{{Name: "#a"}, {Name: "#b"}}
	out := CarryTempBindings(in, tokens)
	if fmt.Sprint(specsOf(in)) != "[#a:  #b: ]" {
		t.Errorf("input changed to %v", specsOf(in))
	}
	if got := fmt.Sprint(specsOf(out)); got != "[#b:  #c: x int]" {
		t.Errorf("out = %v", got)
	}
}
