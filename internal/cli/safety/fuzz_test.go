package safety

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/spec"
)

// FuzzClassify drives Classify with arbitrary text, seeded with the lexer's own
// fuzz corpus and this package's table scripts: no panic, every class is one of
// the four, never below what the statement's leader alone gives, the script's
// class is its highest statement's, and every line is in the script.
//
//	go test ./internal/cli/safety -run XXX -fuzz FuzzClassify -fuzztime 60s -fuzzminimizetime 0
func FuzzClassify(f *testing.F) {
	files, _ := filepath.Glob("../../tuikit/sqltext/testdata/fuzz/FuzzLexer/*")
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		for line := range strings.Lines(string(b)) {
			if q, ok := strings.CutPrefix(strings.TrimSpace(line), "string("); ok {
				if s, err := strconv.Unquote(strings.TrimSuffix(q, ")")); err == nil {
					f.Add(s)
				}
			}
		}
	}
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, script string) {
		if len(script) > 4000 {
			return
		}
		sc := Classify(script)
		lines := strings.Count(script, "\n") + 1
		top := spec.ClassRead
		for _, st := range sc.Statements {
			if Rank(st.Class) < 0 {
				t.Fatalf("statement class %q is not a class: %+v", st.Class, st)
			}
			if st.Line < 1 || st.Line > lines {
				t.Fatalf("statement line %d outside 1..%d: %+v", st.Line, lines, st)
			}
			top = maxClass(top, st.Class)
		}
		if sc.Class != top {
			t.Fatalf("script class %s, highest statement %s", sc.Class, top)
		}
		checkLeaderFloor(t, script, sc)
	})
}

// checkLeaderFloor re-derives each statement's leader-only class and fails if
// Classify gave less: the floor may only raise.
func checkLeaderFloor(t *testing.T, script string, sc *Script) {
	t.Helper()
	k := 0
	for _, b := range splitForTest(script) {
		toks, cuts := lexBatch(b.text, b.startLine)
		for ci, from := range cuts {
			to := len(toks)
			if ci+1 < len(cuts) {
				to = cuts[ci+1]
			}
			if isModule(toks[from:]) {
				k++
				break
			}
			seg := toks[from:to]
			first := ci == 0
			if len(seg) >= 2 && seg[0].kind == wordKind && seg[1].punct(":") {
				if seg = seg[2:]; len(seg) == 0 {
					continue
				}
				first = false
			}
			var lead Statement
			lead.Class = spec.ClassWrite
			leaderRule(&lead, seg, first)
			if k >= len(sc.Statements) {
				t.Fatalf("Classify returned %d statements, the split has more", len(sc.Statements))
			}
			if got := sc.Statements[k].Class; Rank(got) < Rank(lead.Class) {
				t.Fatalf("statement %d: class %s below its leader's %s: %+v", k, got, lead.Class, sc.Statements[k])
			}
			k++
		}
	}
	if k != len(sc.Statements) {
		t.Fatalf("Classify returned %d statements, the split %d", len(sc.Statements), k)
	}
}

var fuzzSeeds = []string{
	"SELECT 1\nDISABLE TRIGGER tr ON t\nGO\nCREATE PROC p AS COMMIT\nGO 2\nEXEC (@x)",
	"WITH c AS (SELECT 1 AS n) SELECT n INTO #t FROM c; MERGE t USING s ON 1=1 WHEN MATCHED THEN DELETE;",
	"retry:\nBEGIN TRAN; UPDATE TOP (5) PERCENT t SET a = 1 OUTPUT inserted.* INTO @l; COMMIT",
	"EXEC @rc = [master].[dbo].[sp_who2]; EXEC master..xp_cmdshell 'x'; sp_configure",
	"SET NOCOUNT, XACT_ABORT ON SET PARSEONLY ON DBCC SQLPERF('x', CLEAR)",
	"CREATE OR ALTER TRIGGER t ON ALL SERVER FOR LOGON AS ROLLBACK",
	"DROP DATABASE IF EXISTS d; ALTER DATABASE SCOPED CONFIGURATION SET MAXDOP = 1",
	"a:", ":", "[", "EXEC", "EXEC @", "EXEC @x =", "SET", "UPDATE TOP (", "CREATE OR", "DROP X IF",
}
