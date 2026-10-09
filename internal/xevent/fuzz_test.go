package xevent

import (
	"slices"
	"testing"
)

// FuzzParseFilter feeds the Filter Events prompt arbitrary text: every input
// returns (M1: a lone '!' looped forever on the UI goroutine), and an
// expression written back by groupsText — what AndTerms and "Filter by this
// Value" produce — parses to the same groups. The seeds run in plain go test;
// fuzzing is on demand:
//
//	go test ./internal/xevent -run XXX -fuzz FuzzParseFilter -fuzztime 60s -timeout 5m
func FuzzParseFilter(f *testing.F) {
	for _, s := range []string{
		"", "hello!", "x ! 5", "!", "!!=~", "a != b", "a !~ b", "a!b = 1",
		"name = 'a!b'", "duration > 1000000 and database_name = 'app'",
		"name = a or duration >= 5 and x is not null", "x not contains 'it''s'",
		"object_name starts with usp_", "x = 'open", "= 5", "a <> \"q\"\"r\"",
		"名前 = ä", "a\xff = \x85b",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		p, err := ParseFilter(text)
		if err != nil || p == nil || p.IsFreeText() {
			return
		}
		back := groupsText(p.groups)
		q, err := ParseFilter(back)
		if err != nil {
			t.Fatalf("ParseFilter(%q) wrote %q, which does not parse: %v", text, back, err)
		}
		if !slices.EqualFunc(p.groups, q.groups, slices.Equal[[]term]) {
			t.Fatalf("ParseFilter(%q) = %v; written back as %q it parses to %v", text, p.groups, back, q.groups)
		}
	})
}
