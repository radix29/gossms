package sqltext

import (
	"slices"
	"testing"
)

func TestSplitBatches(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []Batch
	}{
		{"no separator", "select 1", []Batch{{"select 1", 1}}},
		{"empty", "", nil},
		{"only GO", "GO\nGO 5\n", nil},
		{"two batches", "select 1\nGO\nselect 2", []Batch{{"select 1\n", 1}, {"select 2", 1}}},
		{"CRLF", "select 1\r\nGO\r\nselect 2\r\n", []Batch{{"select 1\r\n", 1}, {"select 2\r\n", 1}}},
		// The review-plan Q1 rows: batch.Split got every one of these wrong.
		{"digit in trailing comment", "select 1\nGO -- step 2\nselect 3",
			[]Batch{{"select 1\n", 1}, {"select 3", 1}}},
		{"count on last line, no newline", "select 1\nGO 2", []Batch{{"select 1\n", 2}}},
		{"GO; is not a separator", "select 1\nGO;\nselect 2", []Batch{{"select 1\nGO;\nselect 2", 1}}},
		{"GO/*x*/ is not a separator", "select 1\nGO/*x*/\nselect 2", []Batch{{"select 1\nGO/*x*/\nselect 2", 1}}},
		{"count with comment", "insert t values(1)\nGO 5 -- five rows\n", []Batch{{"insert t values(1)\n", 5}}},
		{"leading separator", "GO\nselect 1\n", []Batch{{"select 1\n", 1}}},
		// A GO line inside a literal, identifier or comment is text.
		{"inside string", "select 'a\nGO\nb'\nGO\nselect 2",
			[]Batch{{"select 'a\nGO\nb'\n", 1}, {"select 2", 1}}},
		{"inside string with doubled quote", "select 'it''s\nGO\n'\nGO",
			[]Batch{{"select 'it''s\nGO\n'\n", 1}}},
		{"inside quoted identifier", "select 1 as \"a\nGO\nb\"\nGO",
			[]Batch{{"select 1 as \"a\nGO\nb\"\n", 1}}},
		{"inside bracket with doubled bracket", "select 1 as [a]]\nGO\nb]\nGO",
			[]Batch{{"select 1 as [a]]\nGO\nb]\n", 1}}},
		{"inside block comment", "select 1\n/*\nGO\n*/\nGO",
			[]Batch{{"select 1\n/*\nGO\n*/\n", 1}}},
		{"inside nested block comment", "select 1\n/* /* */\nGO\n*/\nGO",
			[]Batch{{"select 1\n/* /* */\nGO\n*/\n", 1}}},
		{"after closed block comment", "select 1 /* x */\nGO\nselect 2",
			[]Batch{{"select 1 /* x */\n", 1}, {"select 2", 1}}},
		// A quote inside a line comment opens nothing.
		{"quote in line comment", "select 1 -- don't\nGO\nselect 2",
			[]Batch{{"select 1 -- don't\n", 1}, {"select 2", 1}}},
		{"block opener in line comment", "select 1 -- /*\nGO\nselect 2",
			[]Batch{{"select 1 -- /*\n", 1}, {"select 2", 1}}},
		{"unterminated string", "select 'a\nGO\nselect 2",
			[]Batch{{"select 'a\nGO\nselect 2", 1}}},
	}
	for _, tt := range tests {
		if got := SplitBatches(tt.script); !slices.Equal(got, tt.want) {
			t.Errorf("%s: SplitBatches(%q)\n got %#v\nwant %#v", tt.name, tt.script, got, tt.want)
		}
	}
}

// Every separator line the rule accepts ends a batch with its own count, and
// every line it refuses stays inside the batch as text.
func TestSplitBatchesAppliesTheLineRule(t *testing.T) {
	for _, tt := range goSeparatorLineCases {
		script := "select 1\n" + tt.line + "\nselect 2"
		got := SplitBatches(script)
		var want []Batch
		if tt.want {
			want = []Batch{{"select 1\n", tt.count}, {"select 2", 1}}
		} else {
			want = []Batch{{script, 1}}
		}
		if !slices.Equal(got, want) {
			t.Errorf("line %q: got %#v, want %#v", tt.line, got, want)
		}
	}
}
