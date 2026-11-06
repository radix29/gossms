package format

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// FuzzFormatSQL checks the SQL formatter on arbitrary text: it never panics,
// never trips its own guard (a guard refusal is a formatter bug the user
// would see as "safety check failed"), fails only with a *SyntaxError, keeps
// the GO batches exactly, and formats its output to itself. Seeds: the
// goldens and sqltext's lexer fuzz corpus.
//
//	go test ./internal/format -run XXX -fuzz FuzzFormatSQL -fuzztime 60s -fuzzminimizetime 0
func FuzzFormatSQL(f *testing.F) {
	ins, _ := filepath.Glob(filepath.Join("testdata", "sql", "*.in"))
	corpus, _ := filepath.Glob(filepath.Join("..", "tuikit", "sqltext", "testdata", "fuzz", "*", "*"))
	for _, in := range append(ins, corpus...) {
		b, err := os.ReadFile(in)
		if err != nil {
			continue
		}
		if s, ok := fuzzCorpusString(string(b)); ok {
			f.Add(s)
		} else {
			f.Add(string(b))
		}
	}
	for _, s := range []string{
		"", "GO", "select 1\ngo 3\nselect 2", "select N'x', $(v), 1e+5, a-1, -1",
		"if 1=1 if 2=2 print 1 else print 2 else print 3",
		"select a -- c\n, b from t", "begin end", "case when", "end else",
		"create proc p @a int output as select 1", "select go from t",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4000 {
			return
		}
		for _, c := range []struct {
			style string
			o     Options
		}{{"", Options{IndentWidth: 2}}, {"", Options{IndentWidth: 4, LineIndent: "  "}}, {"lowercase", Options{}}} {
			o := c.o
			got, err := Format(LangSQL, c.style, src, o)
			if err != nil {
				if _, ok := errors.AsType[*SyntaxError](err); !ok {
					t.Fatalf("Format(%q, %+v): %v", src, o, err)
				}
				return
			}
			if a, b := batchCounts(src), batchCounts(got); a != b {
				t.Fatalf("GO batches changed: %s -> %s\n%q\n%q", a, b, src, got)
			}
			if o.LineIndent != "" {
				continue // a prefixed result is not itself unprefixed input
			}
			again, err := Format(LangSQL, c.style, got, o)
			if err != nil || again != got {
				t.Fatalf("not idempotent (%v):\n%q\n->\n%q\n->\n%q", err, src, got, again)
			}
		}
	})
}

// fuzzCorpusString decodes a "go test fuzz v1" file holding one string.
func fuzzCorpusString(b string) (string, bool) {
	rest, ok := strings.CutPrefix(b, "go test fuzz v1\n")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	lit, ok := strings.CutPrefix(rest, "string(")
	if !ok {
		return "", false
	}
	s, err := strconv.Unquote(strings.TrimSuffix(lit, ")"))
	return s, err == nil
}

func batchCounts(s string) string {
	var b strings.Builder
	for _, bt := range sqltext.SplitBatches(s) {
		b.WriteString(strconv.Itoa(bt.Count) + ",")
	}
	return b.String()
}

func formatSQLOrFail(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(LangSQL, "", src, Options{})
	if err != nil {
		t.Fatalf("Format(%q): %v", src, err)
	}
	return got
}

// Keywords are upper-cased; names never are, even spelled like a type or a
// function, since a case-sensitive database tells date from DATE.
func TestFormatSQLKeywordCase(t *testing.T) {
	for src, want := range map[string]string{
		"select date, count from t":                    "SELECT\n    date\n  , count\nFROM t",
		"select count(*), left(a, 1) from t":           "SELECT\n    COUNT(*)\n  , LEFT(a, 1)\nFROM t",
		"select dbo.count(x), t.select from t":         "SELECT\n    dbo.count(x)\n  , t.select\nFROM t",
		"declare @d date, @v varchar(5)":               "DECLARE\n    @d DATE\n  , @v VARCHAR(5)",
		"select cast(a as int), convert(date, b)":      "SELECT\n    CAST(a AS INT)\n  , CONVERT(DATE, b)",
		"select int, text from t":                      "SELECT\n    int\n  , text\nFROM t",
		"set nocount on":                               "SET NOCOUNT ON",
		"set @nocount = 1":                             "SET @nocount = 1",
		"select @@rowcount, @Select":                   "SELECT\n    @@rowcount\n  , @Select",
		"select [select], \"from\" from [t]":           "SELECT\n    [select]\n  , \"from\"\nFROM [t]",
		"exec p @x output":                             "EXEC p @x output",
		"select sysname from t":                        "SELECT sysname\nFROM t",
		"select * from t with (nolock, index(ix))":     "SELECT *\nFROM t WITH (NOLOCK, INDEX(ix))",
		"update t with (tablock) set a = 1":            "UPDATE t WITH (TABLOCK)\nSET a = 1",
		"select * from openjson(@j) with (nolock int)": "SELECT *\nFROM OPENJSON(@j) WITH (nolock int)",
		"create index ix on t (a) with (online = on)":  "CREATE INDEX ix ON t (a) WITH (online = ON)",
	} {
		if got := formatSQLOrFail(t, src); got != want {
			t.Errorf("Format(%q) =\n%s\nwant\n%s", src, got, want)
		}
	}
}

// A name spelled like a built-in function keeps its source case where an
// object name stands, though a "(" follows it — a column list or a table
// hint, not a call — while the real calls around it are still upper-cased.
func TestFormatSQLNamePositionKeepsCase(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"insert into log (msg) select left(s.x, 2) from s",
			"INSERT INTO log (msg)\nSELECT LEFT(s.x, 2)\nFROM s"},
		{"insert log (msg) select count(*) from t",
			"INSERT log (msg)\nSELECT COUNT(*)\nFROM t"},
		{"create table year (id int, m as month(getdate()))",
			"CREATE TABLE year (\n    id INT\n  , m AS MONTH(GETDATE())\n)"},
		{"create index ix on log (msg)", "CREATE INDEX ix ON log (msg)"},
		{"create statistics s on log (msg)", "CREATE STATISTICS s ON log (msg)"},
		{"update statistics log (s)", "UPDATE STATISTICS log (s)"},
		{"grant select on log (msg) to u", "GRANT SELECT ON log (msg) TO u"},
		{"with rank (r) as (select count(*) from t), month (m) as (select 1) select r from rank",
			"WITH rank (r) AS (\n    SELECT COUNT(*)\n    FROM t\n)\n, month (m) AS (\n    SELECT 1\n)\nSELECT r\nFROM rank"},
		{"create table t (id int references month (id))",
			"CREATE TABLE t (id INT REFERENCES month (id))"},
		{"select * from t join log (nolock) on left(t.a, 1) = log.b",
			"SELECT *\nFROM t\nJOIN log (nolock) ON LEFT(t.a, 1) = log.b"},
		{"select * from openjson(@j) cross apply string_split(a, ',') s",
			"SELECT *\nFROM OPENJSON(@j)\nCROSS APPLY STRING_SPLIT(a, ',') s"},
		{"select trim(' ' from left(s, 3)) from t where a is distinct from left(b, 1)",
			"SELECT TRIM(' ' FROM LEFT(s, 3))\nFROM t\nWHERE a IS DISTINCT FROM LEFT(b, 1)"},
		{"create view rank (a) as select 1", "CREATE VIEW rank (a)\nAS\nSELECT 1"},
	} {
		if got := formatSQLOrFail(t, tc.src); got != tc.want {
			t.Errorf("Format(%q) =\n%s\nwant\n%s", tc.src, got, tc.want)
		}
	}
}

// Comments, literals and quoted identifiers come through byte for byte,
// whatever the layout does around them; a "--" comment always ends its line.
func TestFormatSQLKeepsTokensVerbatim(t *testing.T) {
	keep := []string{
		"'it''s\n  two lines'", "N'ünï 😀'", "[a ]] b]", `"q ""x"""`,
		"/* nested /* block */ comment\n   kept */", "-- select from where",
		"0x1F", "1e+5", "$(var)",
	}
	src := "select " + strings.Join(keep[:4], ", ") + " " + keep[4] + " from t " + keep[5] +
		"\nwhere a = 0x1F or b = 1e+5 or c = $(var)"
	got := formatSQLOrFail(t, src)
	for _, k := range keep {
		if !strings.Contains(got, k) {
			t.Errorf("output lost %q:\n%s", k, got)
		}
	}
	if !strings.Contains(got, keep[5]+"\n") {
		t.Errorf("code followed a line comment on its line:\n%s", got)
	}
}

// A GO line is kept as written (trimmed), batches one blank line apart; a
// line the layout makes never becomes a separator, even for a column named go.
func TestFormatSQLGoLines(t *testing.T) {
	got := formatSQLOrFail(t, "select 1\n  go 2 -- twice  \n\n\nselect go, x from t\nselect 1; go")
	want := "SELECT 1\ngo 2 -- twice\n\nSELECT go\n  , x\nFROM t\nSELECT 1; go"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	if a, b := batchCounts("select go, x from t"), batchCounts(got); b != "2,1," || a != "1," {
		t.Errorf("batches %s / %s", a, b)
	}
}

func TestFormatSQLSyntaxErrors(t *testing.T) {
	for _, tc := range []struct {
		src       string
		line, col int
		msg       string
	}{
		{"select 1\nselect 'abc", 2, 8, "unterminated string literal"},
		{"select [a", 1, 8, "unterminated quoted identifier"},
		{"select \"a", 1, 8, "unterminated quoted identifier"},
		{"select 1 /* x /* y */", 1, 10, "unterminated block comment"},
		{"select (1))", 1, 11, `")" has no matching "("`},
		{"select ((1)\nfrom t", 1, 8, `"(" is never closed`},
		{"select (1\ngo\nselect 2)", 1, 8, `"(" is never closed`},
		{"select 'é😀' + \xff", 1, 15, "invalid UTF-8"}, // columns count runes
	} {
		_, err := Format(LangSQL, "", tc.src, Options{})
		se, ok := errors.AsType[*SyntaxError](err)
		if !ok {
			t.Errorf("Format(%q): err %v, want *SyntaxError", tc.src, err)
			continue
		}
		if se.Lang != LangSQL || se.Line != tc.line || se.Col != tc.col || se.Msg != tc.msg {
			t.Errorf("Format(%q): %v; want line %d col %d %q", tc.src, se, tc.line, tc.col, tc.msg)
		}
	}
	// An unterminated "--" comment at the end is no error: it ends with the text.
	if got := formatSQLOrFail(t, "select 1 -- end"); got != "SELECT 1 -- end" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSQLTrailingNewlineAndIndent(t *testing.T) {
	for src, want := range map[string]string{
		"select 1":            "SELECT 1",
		"select 1\n":          "SELECT 1\n",
		"\n\n  select 1 \n\n": "SELECT 1\n",
		"":                    "",
		"  \n":                "",
		"-- only a comment\n": "-- only a comment\n",
	} {
		if got := formatSQLOrFail(t, src); got != want {
			t.Errorf("Format(%q) = %q, want %q", src, got, want)
		}
	}
	got, err := Format(LangSQL, "", "if 1 = 1 begin select a, b from t end", Options{IndentWidth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := "IF 1 = 1\nBEGIN\n  SELECT\n    a\n  , b\n  FROM t\nEND"; got != want {
		t.Errorf("width 2:\n%s\nwant\n%s", got, want)
	}
}

// LineIndent prefixes the lines the layout starts, never a line inside a
// multi-line token: that line is part of the literal or comment.
func TestFormatSQLLineIndent(t *testing.T) {
	got, err := Format(LangSQL, "", "select 'a\nb' /* c\nd */ from t", Options{LineIndent: "\t  "})
	if err != nil {
		t.Fatal(err)
	}
	if want := "\t  SELECT 'a\nb' /* c\nd */\n\t  FROM t"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The guard compares keywords case-insensitively and everything else
// exactly, GO lines and comments included.
func TestGuardSQL(t *testing.T) {
	const src = "select Name from t -- c\ngo\nselect 'x'"
	for name, out := range map[string]string{
		"re-cases a name":      "SELECT NAME FROM t -- c\ngo\nSELECT 'x'",
		"edits a literal":      "SELECT Name FROM t -- c\ngo\nSELECT 'X'",
		"edits a comment":      "SELECT Name FROM t -- C\ngo\nSELECT 'x'",
		"comments out code":    "SELECT Name -- c FROM t\ngo\nSELECT 'x'",
		"drops the GO":         "SELECT Name FROM t -- c\nSELECT 'x'",
		"respells the GO line": "SELECT Name FROM t -- c\nGO\nSELECT 'x'",
		"makes a new batch":    "SELECT Name FROM t -- c\ngo\nSELECT\ngo\n'x'",
		"joins two tokens":     "SELECT Name FROM t -- c\ngo\nSELECTN'x'",
	} {
		if err := verify(LangSQL, src, out); !errors.Is(err, ErrGuard) {
			t.Errorf("%s: verify = %v, want ErrGuard", name, err)
		}
		// The input's lexing, as formatSQL hands it over, guards the same.
		in, err := lexSQL(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyLexed(LangSQL, src, in, out); !errors.Is(err, ErrGuard) {
			t.Errorf("%s: verifyLexed = %v, want ErrGuard", name, err)
		}
	}
	if err := verify(LangSQL, src, "SELECT  Name\n FROM T -- c  \n go \n  SELECT\n'x'"); err == nil {
		t.Error("verify accepted a re-cased table name T")
	}
	if err := verify(LangSQL, src, "SELECT  Name\n FROM t -- c  \n go \n  SELECT\n'x'"); err != nil {
		t.Errorf("verify refused a layout-only change: %v", err)
	}
}

// The Lowercase style lower-cases exactly the keywords Standard upper-cases,
// puts FROM, WHERE, GROUP BY, HAVING and ORDER BY alone on their lines with
// the joins under FROM, and writes a select-list "expr AS name" as
// "name = expr" — only where the item is plainly an expression and a name,
// with nothing in between.
func TestFormatSQLLowercase(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"SELECT Name, COUNT(*) FROM T WHERE Date > GETDATE()",
			"select\n    Name\n  , count(*)\nfrom\n    T\nwhere\n    Date > getdate()"},
		{"SELECT A AS X", "select X = A"},
		{"select count(*) as n from t", "select n = count(*)\nfrom\n    t"},
		{"select cast(a as int) as b, c + 1 as [y z], d type from t",
			"select\n    b = cast(a as int)\n  , [y z] = c + 1\n  , d type\nfrom\n    t"},
		{"select distinct top (5) with ties a as x from t", "select distinct top (5) with ties x = a\nfrom\n    t"},
		{"select @v as x, a as type", "select\n    x = @v\n  , type = a"},
		{"select a as x -- c\n, b as y from t", "select\n    x = a -- c\n  , y = b\nfrom\n    t"},
		{"select (select 1 as y) as z", "select z = (\n    select y = 1\n)"},
		{"select string_agg(a, ',') within group (order by a) as s from t",
			"select s = string_agg(a, ',') within group (order by a)\nfrom\n    t"},
		{"select case when a = 1 then 'x' end as k into #t from u",
			"select\n    k = case\n        when a = 1 then 'x'\n    end\ninto #t\nfrom\n    u"},
		{"if 1 = 1 select a as x else select b as y",
			"if 1 = 1\n    select x = a\nelse\n    select y = b"},
		{"select a as x\nprint 1", "select x = a\nprint 1"},
		{"select(1) as a", "select a = (1)"},
		{"select 0 as a b, 1 as c", "select\n    0 as a b\n  , c = 1"},
		{"create view v as select 1 as x", "create view v\nas\nselect x = 1"},
		// FROM and WHERE stand alone; joins sit with the tables, an ON's AND
		// one level under its join, a WHERE's AND with the first predicate.
		{"select a from t join u on u.id = t.id and u.x = 1 left join v on v.a = u.a where a = 1 and b = 2",
			"select a\nfrom\n    t\n    join u on u.id = t.id\n        and u.x = 1\n    left join v on v.a = u.a\nwhere\n    a = 1\n    and b = 2"},
		{"select * from (select 1 as a) as x", "select *\nfrom\n    (\n        select a = 1\n    ) as x"},
		{"delete top (10) from t where a = 1", "delete top (10) from t\nwhere\n    a = 1"},
		{"select a, count(*) as n from t group by a having count(*) > 1 and max(b) < 2 order by a desc, n",
			"select\n    a\n  , n = count(*)\nfrom\n    t\ngroup by\n    a\nhaving\n    count(*) > 1\n    and max(b) < 2\norder by\n    a desc\n  , n"},
		{"select row_number() over (partition by a order by b) as r from t",
			"select r = row_number() over (partition by a order by b)\nfrom\n    t"},
		// Left as written: a string alias (deprecated as 'n' = expr), a comment
		// inside the item, NEXT VALUE FOR, a table alias, a GRANT's AS.
		{"select a as 'n'", "select a as 'n'"},
		{"select a /* c */ as x", "select a /* c */ as x"},
		{"select next value for s as x", "select next value for s as x"},
		{"select a from t as x", "select a\nfrom\n    t as x"},
		{"grant select on t to u as dbo", "grant select on t to u as dbo"},
	} {
		got, err := Format(LangSQL, "lowercase", tc.src, Options{})
		if err != nil {
			t.Errorf("Format(%q): %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Format(%q) =\n%s\nwant\n%s", tc.src, got, tc.want)
		}
	}
}

// The guard accepts "name = expr" for an input "expr AS name" select-list
// item and nothing else that moves a token.
func TestGuardSQLAliasSwap(t *testing.T) {
	for _, tc := range []struct{ in, out string }{
		{"select a + b as c from t", "select c = a + b from t"},
		{"select (select 1 as y) as z, w", "select z = (select y = 1), w"},
		{"select a as x -- c\n, b as y", "select x = a -- c\n, y = b"},
		{"select x as x", "select x = x"},
		{"select a as x", "select a as x"}, // not moving is fine too
	} {
		if err := verify(LangSQL, tc.in, tc.out); err != nil {
			t.Errorf("verify(%q, %q) = %v, want nil", tc.in, tc.out, err)
		}
	}
	for name, tc := range map[string]struct{ in, out string }{
		"drops a token":        {"select a + b as c", "select c = a + c"},
		"moves part of it":     {"select a + b as c", "select a + c = b"},
		"keeps the AS":         {"select a as c", "select c = a as c"},
		"a table alias":        {"select a from t as x", "select x = a from t"},
		"a CAST's AS":          {"select cast(a as int)", "select cast(int = a)"},
		"a string alias":       {"select a as 'n'", "select 'n' = a"},
		"a comment inside":     {"select a /* c */ as x", "select x = a /* c */"},
		"a GRANT's AS":         {"grant select on t to u as dbo", "grant select dbo = on t to u"},
		"an assignment":        {"select @v = a as x", "select x = @v = a"},
		"a qualified alias":    {"select a as x.y", "select x.y = a"},
		"an alias not last":    {"select a as x + 1", "select x = a + 1"},
		"across items":         {"select a, b as c", "select c = a, b"},
		"an item past FROM":    {"select a from t where b as c", "select a from t where c = b"},
		"a DISTINCT in prefix": {"select distinct a as x", "select x = distinct a"},
	} {
		if err := verify(LangSQL, tc.in, tc.out); !errors.Is(err, ErrGuard) {
			t.Errorf("%s: verify = %v, want ErrGuard", name, err)
		}
	}
}

// BenchmarkFormatSQL formats a 20 000-line script.
func BenchmarkFormatSQL(b *testing.B) {
	src, err := os.ReadFile(filepath.Join("testdata", "sql", "dml.in"))
	if err != nil {
		b.Fatal(err)
	}
	unit := string(src) + "go\n"
	var sb strings.Builder
	for strings.Count(sb.String(), "\n") < 20000 {
		sb.WriteString(unit)
		sb.WriteString(strings.Repeat("select a, b from t where x = 1\n", 200))
	}
	script := sb.String()
	b.SetBytes(int64(len(script)))
	for b.Loop() {
		if _, err := Format(LangSQL, "", script, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
