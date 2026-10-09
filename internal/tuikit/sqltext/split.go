package sqltext

import "strings"

// Batch is one GO-delimited batch of a script and how many times it runs.
type Batch struct {
	Text  string
	Count int
}

// SplitBatches splits script into its GO batches, dropping whitespace-only ones.
//
// A line is a separator by GoSeparatorAt's rule (shared with statement select
// and IntelliSense) and only when Next's lexer reaches its start in ModeNormal,
// so a GO line inside a string, quoted/bracketed identifier or (nesting) block
// comment is batch text. An unterminated literal or comment runs to the end of
// the script, which goes to the server as one batch for it to reject.
//
// Lines are decoded into one reused buffer, so a large script costs its longest
// line rather than a []rune copy of the whole.
//
// The repeat count is not capped: SSMS runs "GO 100000" as asked; callers check
// for cancellation between repetitions.
func SplitBatches(script string) []Batch {
	var batches []Batch
	emit := func(text string, count int) {
		if strings.TrimSpace(text) != "" {
			batches = append(batches, Batch{Text: text, Count: count})
		}
	}

	var line []rune
	var st State
	batchStart := 0
	for pos := 0; pos < len(script); {
		// pos is the byte offset of a line start, and next of the line after it.
		end := strings.IndexByte(script[pos:], '\n')
		next := len(script)
		if end >= 0 {
			end += pos
			next = end + 1
		} else {
			end = len(script)
		}
		line = line[:0]
		for _, r := range script[pos:end] {
			line = append(line, r)
		}
		if st.Mode == ModeNormal {
			if _, count, ok := GoSeparatorAt(line, 0, len(line)); ok {
				emit(script[batchStart:pos], count)
				batchStart, pos = next, next
				continue
			}
		}
		st = LineEnd(line, st)
		pos = next
	}
	emit(script[batchStart:], 1)
	return batches
}
