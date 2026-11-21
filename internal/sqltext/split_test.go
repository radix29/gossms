package sqltext

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitBatches(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []Batch
	}{
		{"no separator", "select 1", []Batch{{"select 1", 1, 0}}},
		{"empty", "", nil},
		{"only GO", "GO\nGO 5\n", nil},
		{"two batches", "select 1\nGO\nselect 2", []Batch{{"select 1\n", 1, 0}, {"select 2", 1, 0}}},
		{"CRLF", "select 1\r\nGO\r\nselect 2\r\n", []Batch{{"select 1\r\n", 1, 0}, {"select 2\r\n", 1, 0}}},
		// The review-plan Q1 rows: batch.Split got every one of these wrong.
		{"digit in trailing comment", "select 1\nGO -- step 2\nselect 3",
			[]Batch{{"select 1\n", 1, 0}, {"select 3", 1, 0}}},
		{"count on last line, no newline", "select 1\nGO 2", []Batch{{"select 1\n", 2, 0}}},
		{"GO; is not a separator", "select 1\nGO;\nselect 2", []Batch{{"select 1\nGO;\nselect 2", 1, 0}}},
		{"GO/*x*/ is not a separator", "select 1\nGO/*x*/\nselect 2", []Batch{{"select 1\nGO/*x*/\nselect 2", 1, 0}}},
		{"count with comment", "insert t values(1)\nGO 5 -- five rows\n", []Batch{{"insert t values(1)\n", 5, 0}}},
		{"leading separator", "GO\nselect 1\n", []Batch{{"select 1\n", 1, 0}}},
		// A GO line inside a literal, identifier or comment is text.
		{"inside string", "select 'a\nGO\nb'\nGO\nselect 2",
			[]Batch{{"select 'a\nGO\nb'\n", 1, 0}, {"select 2", 1, 0}}},
		{"inside string with doubled quote", "select 'it''s\nGO\n'\nGO",
			[]Batch{{"select 'it''s\nGO\n'\n", 1, 0}}},
		{"inside quoted identifier", "select 1 as \"a\nGO\nb\"\nGO",
			[]Batch{{"select 1 as \"a\nGO\nb\"\n", 1, 0}}},
		{"inside bracket with doubled bracket", "select 1 as [a]]\nGO\nb]\nGO",
			[]Batch{{"select 1 as [a]]\nGO\nb]\n", 1, 0}}},
		{"inside block comment", "select 1\n/*\nGO\n*/\nGO",
			[]Batch{{"select 1\n/*\nGO\n*/\n", 1, 0}}},
		{"inside nested block comment", "select 1\n/* /* */\nGO\n*/\nGO",
			[]Batch{{"select 1\n/* /* */\nGO\n*/\n", 1, 0}}},
		{"after closed block comment", "select 1 /* x */\nGO\nselect 2",
			[]Batch{{"select 1 /* x */\n", 1, 0}, {"select 2", 1, 0}}},
		// A quote inside a line comment opens nothing.
		{"quote in line comment", "select 1 -- don't\nGO\nselect 2",
			[]Batch{{"select 1 -- don't\n", 1, 0}, {"select 2", 1, 0}}},
		{"block opener in line comment", "select 1 -- /*\nGO\nselect 2",
			[]Batch{{"select 1 -- /*\n", 1, 0}, {"select 2", 1, 0}}},
		{"unterminated string", "select 'a\nGO\nselect 2",
			[]Batch{{"select 'a\nGO\nselect 2", 1, 0}}},
	}
	for _, tt := range tests {
		got := SplitBatches(tt.script)
		assertStartLines(t, tt.script, got)
		if got = withoutStartLines(got); !slices.Equal(got, tt.want) {
			t.Errorf("%s: SplitBatches(%q)\n got %#v\nwant %#v", tt.name, tt.script, got, tt.want)
		}
	}
}

// The tables above leave StartLine to assertStartLines, which checks it on every
// case, so they compare Text and Count only.
func withoutStartLines(bs []Batch) []Batch {
	for i := range bs {
		bs[i].StartLine = 0
	}
	return bs
}

// assertStartLines checks that each batch's Text is what the script holds from
// the start of line StartLine: the line a server error's "Line 1" is.
func assertStartLines(t *testing.T, script string, bs []Batch) {
	t.Helper()
	lines := strings.SplitAfter(script, "\n")
	for _, b := range bs {
		if b.StartLine < 0 || b.StartLine >= len(lines) {
			t.Errorf("script %q: batch %q has StartLine %d of %d lines", script, b.Text, b.StartLine, len(lines))
			continue
		}
		if from := strings.Join(lines[b.StartLine:], ""); !strings.HasPrefix(from, b.Text) {
			t.Errorf("script %q: batch %q does not start on line %d (%q)", script, b.Text, b.StartLine, from)
		}
	}
}

// A batch's StartLine counts the separator lines and dropped empty batches before
// it, a GO line inside a literal does not end one, and a batch's own leading blank
// lines are part of it (the server counts them).
func TestSplitBatchesStartLine(t *testing.T) {
	script := "print 1\nGO\n\nGO\n\nselect 'a\nGO\nb'\nGO 2\nselect 1/0"
	var got []int
	for _, b := range SplitBatches(script) {
		got = append(got, b.StartLine)
	}
	if want := []int{0, 4, 9}; !slices.Equal(got, want) {
		t.Errorf("StartLines = %v, want %v", got, want)
	}
}

// Every separator line the rule accepts ends a batch with its own count, and
// every line it refuses stays inside the batch as text.
func TestSplitBatchesAppliesTheLineRule(t *testing.T) {
	for _, tt := range goSeparatorLineCases {
		script := "select 1\n" + tt.line + "\nselect 2"
		got := SplitBatches(script)
		assertStartLines(t, script, got)
		got = withoutStartLines(got)
		var want []Batch
		if tt.want {
			want = []Batch{{"select 1\n", tt.count, 0}, {"select 2", 1, 0}}
		} else {
			want = []Batch{{script, 1, 0}}
		}
		if !slices.Equal(got, want) {
			t.Errorf("line %q: got %#v, want %#v", tt.line, got, want)
		}
	}
}

// StatementCutter reports the statement starts StatementAt cuts at, by the
// caller's token positions — including a leader whose role the next token
// settles (WITH), reported once that token arrives.
func TestStatementCutter(t *testing.T) {
	src := []rune("select 1 insert t values (1) with c as (select 1) select * from c; " +
		"update t set a = 1 merge t using s on 1 = 1 when matched then update set a = 1;")
	var cuts []string
	var words []string
	c := NewStatementCutter(func(pos int) { cuts = append(cuts, words[pos]) })
	var st State
	for i := 0; ; {
		var tok Token
		tok, st = Next(src, i, len(src), st)
		if tok.Kind == KindEnd {
			break
		}
		i = tok.End
		words = append(words, string(src[tok.Start:tok.End]))
		c.Feed(src, tok, len(words)-1)
	}
	if got, want := strings.Join(cuts, " "), "select insert with update merge"; got != want {
		t.Errorf("cuts at %q, want %q", got, want)
	}
}

// A statement led by a verb outside the original allow-list, or a second SET
// after an UPDATE's own, starts its own statement; the clause forms of the
// same words still continue the statement above.
func TestStatementAtLeaderVerbs(t *testing.T) {
	prevs := []string{"select 1", "update t set a = 1", "delete t", "declare @x int", "exec p", "set nocount on"}
	nexts := []string{
		"throw 50000, 'x', 1", "return", "goto lbl", "break", "continue", "revert", "setuser",
		"disable trigger tr on t", "enable trigger tr on t", "readtext t.c @p 0 1", "writetext t.c @p 'x'",
		"updatetext t.c @p 0 0 'x'", "add signature to p by certificate c", "copy into t from 'u'",
		"set nocount off",
	}
	for _, p := range prevs {
		for _, n := range nexts {
			lines := [][]rune{[]rune(p), []rune(n)}
			r, _, _, _, ok := StatementAt(lines, 1, 0)
			if !ok || r != 1 {
				t.Errorf("%q then %q: statement starts at row %d, want 1", p, n, r)
			}
		}
	}
	for _, src := range []string{
		"select * from t where throw = 1",
		"select a from t order by revert",
		"update t set revert = 1 where id = 2",
	} {
		lines := [][]rune{[]rune(src)}
		if r, c, _, _, ok := StatementAt(lines, 0, len(lines[0])); !ok || r != 0 || c != 0 {
			t.Errorf("%q: statement starts at %d:%d, want 0:0", src, r, c)
		}
	}
	for _, src := range []string{
		"alter table t\ndisable trigger x",
		"alter table t\nenable trigger x",
		"alter table t\nadd c int",
		"alter database d\nset recovery simple",
		"merge t using s on 1 = 1\nwhen matched then update set a = 1",
		"create trigger tr on t\nafter update as select 1",
	} {
		lines := [][]rune{[]rune(strings.Split(src, "\n")[0]), []rune(strings.Split(src, "\n")[1])}
		if r, _, _, _, _ := StatementAt(lines, 1, 0); r != 0 {
			t.Errorf("%q: second line starts its own statement (row %d), want it to continue", src, r)
		}
	}
}
