package sqlparse

import (
	"strings"
	"testing"
)

// fuzzMaxRunes bounds a fuzz input: each cursor position re-tokenizes the
// prefix, so the per-input cost is quadratic.
const fuzzMaxRunes = 600

// FuzzScope runs every scope entry point at every cursor position of
// arbitrary text, as the editor does at every keystroke while completion is
// open. The seeds are the golden corpus (diffCorpus and its fragments) and run
// in plain go test; fuzzing is on demand:
//
//	go test ./internal/tui/sqlparse -run XXX -fuzz FuzzScope -fuzztime 60s
func FuzzScope(f *testing.F) {
	for _, name := range sortedCorpusNames() {
		f.Add(diffCorpus[name])
	}
	for _, s := range fragments {
		f.Add(s)
	}
	f.Add(strings.Join(fragments, "\n"))
	f.Fuzz(func(t *testing.T, text string) {
		buf := []rune(text)
		if len(buf) > fuzzMaxRunes {
			return
		}
		all, _, _, _ := TokenizeRange(buf, 0, len(buf), false)
		checkTokens(t, text, all, 0, len(buf))

		prev := -1
		for _, off := range DMLStatementStarts(all) {
			if off <= prev || off < 0 || off > len(buf) {
				t.Fatalf("DMLStatementStarts(%q): %d after %d, outside [0,%d] or out of order", text, off, prev, len(buf))
			}
			prev = off
		}
		ScanBindings(all)
		CarryTempBindings(CarryTempBindings(nil, all), all)

		for upTo := 0; upTo <= len(buf); upTo++ {
			pre, _, semi, _ := TokenizeRange(buf, 0, upTo, false)
			checkTokens(t, text, pre, 0, upTo)
			if semi < 0 || semi > upTo {
				t.Fatalf("TokenizeRange(%q, 0, %d): boundary %d out of range", text, upTo, semi)
			}
			if _, _, from, _ := TokenContext(pre, upTo); from < 0 || from > upTo {
				t.Fatalf("TokenContext(%q, %d): replaceFrom %d out of range", text, upTo, from)
			}
			QualifierChain(pre, upTo)
			CurrentClause(pre)
			ParseFromScope(pre)
			ScopeAt(all, upTo)
			if start, end := NarrowToDMLStatement(all, 0, len(buf), upTo); start < 0 || start > end || end > len(buf) {
				t.Fatalf("NarrowToDMLStatement(%q, %d) = %d, %d: out of range", text, upTo, start, end)
			}
		}
	})
}

// checkTokens fails unless tokens start in [from, upTo) in ascending order.
func checkTokens(t *testing.T, text string, tokens []Token, from, upTo int) {
	t.Helper()
	prev := from - 1
	for _, tok := range tokens {
		if tok.Start <= prev || tok.Start >= upTo {
			t.Fatalf("TokenizeRange(%q, %d, %d): token %+v after %d or past the limit", text, from, upTo, tok, prev)
		}
		prev = tok.Start
	}
}
