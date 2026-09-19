package sqlparse

import (
	"fmt"
	"testing"
)

// TestLexSQLReportsEveryBoundary pins lexSQL's onBoundary sink, whose one
// caller is PrefixCache (prefix_cache.go) — the cache's correctness rests on
// the boundary list being complete. lexResult keeps only the last ';' and the
// last "GO", so the golden corpus cannot see the ones in between, and the
// cache's differential tests read the sink only through its answers.
//
// The script's decoys are why the sink lives inside the state machine rather
// than in a textual pass: a ';' inside a block comment, a string literal or a
// bracketed identifier ends no statement, and a "GO" inside a block comment or
// preceded by "--" starts no batch.
func TestLexSQLReportsEveryBoundary(t *testing.T) {
	const script = "SELECT 1;\n" +
		"SELECT 2 /* ; not a boundary\n" +
		"GO also not */ ;\n" +
		"GO\n" +
		"SELECT '; GO';\n" +
		"-- GO\n" +
		"GO 2\n" +
		"SELECT [a; GO b] ;\n" +
		"SELECT 3"

	lines := splitRunes(script)
	buf := flattenFresh(lines)

	var got []string
	var offs []int
	r := lexSQL(buf, 0, len(buf), false, LexNormal, nil, goScan{lo: 0, hi: len(buf)},
		func(off int, isGo bool) {
			kind := "semi"
			if isGo {
				kind = "go"
			}
			row, col := rowColForOffset(lines, off)
			got = append(got, fmt.Sprintf("%d:%d %s", row, col, kind))
			offs = append(offs, off)
		})

	want := []string{
		"0:9 semi",  // SELECT 1;
		"2:16 semi", // the ';' after the block comment closes
		"4:0 go",    // the bare GO on line 3 — resume on line 4
		"4:14 semi", // the real ';', not the one inside the literal
		"7:0 go",    // "GO 2" is a separator; "-- GO" above it is not
		"7:18 semi", // the ';' at the end, not the one inside brackets
	}
	if len(got) != len(want) {
		t.Fatalf("boundaries:\n got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("boundary %d = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}

	// PrefixCache stores these in a sorted slice and binary-searches it, so the
	// sink owes it strict ascent, not merely "roughly in order".
	for i := 1; i < len(offs); i++ {
		if offs[i] <= offs[i-1] {
			t.Errorf("boundary offsets not strictly ascending at %d: %v", i, offs)
		}
	}

	// The sink and lexResult must agree about the last of each kind: the cache
	// resumes at a sink offset and then trusts lexResult's fields from there.
	lastSemi, lastGo := -1, -1
	for i, off := range offs {
		if got[i][len(got[i])-2:] == "go" {
			lastGo = off
		} else {
			lastSemi = off
		}
	}
	if r.boundary != lastSemi {
		t.Errorf("lexResult.boundary = %d, last ';' boundary reported = %d", r.boundary, lastSemi)
	}
	if r.lastGo != lastGo {
		t.Errorf("lexResult.lastGo = %d, last GO boundary reported = %d", r.lastGo, lastGo)
	}
}

// rowColForOffset is OffsetForCursor's inverse, for readable failures.
func rowColForOffset(lines [][]rune, off int) (int, int) {
	for row, line := range lines {
		if off <= len(line) {
			return row, off
		}
		off -= len(line) + 1 // the '\n'
	}
	return len(lines) - 1, off
}
