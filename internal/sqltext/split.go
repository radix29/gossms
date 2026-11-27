package sqltext

import "strings"

// Batch is one GO-delimited batch of a script and how many times it runs.
type Batch struct {
	Text  string
	Count int

	// StartLine is the 0-based script line Text starts on, a CRLF, a lone CR
	// and an LF each ending one line, as an editor shows them. SQL Server numbers
	// an error's line from the batch's first line, so adding it gives the script
	// line (see query.WithLineOffset). The server itself counts LFs only (on 17,
	// "SELECT 1<CR>SELECT 1/0" fails on line 1), so the two agree on any LF or
	// CRLF script; a lone CR inside a batch is the one place they part.
	StartLine int
}

// SplitBatches splits script into its GO batches, dropping whitespace-only ones.
//
// A line is a separator by GoSeparatorAt's rule (shared with statement select
// and IntelliSense) and only when Next's lexer reaches its start in ModeNormal,
// so a GO line inside a string, quoted/bracketed identifier or (nesting) block
// comment is batch text. An unterminated literal or comment runs to the end of
// the script, which goes to the server as one batch for it to reject.
//
// A line ends at a CRLF, a lone CR or an LF, so a CR-only (classic Mac)
// script's GO is a separator too. Each batch's Text is a verbatim slice of the
// script: its line ends, and those inside its literals, reach the server as
// written.
//
// Lines are decoded into one reused buffer, so a large script costs its longest
// line rather than a []rune copy of the whole.
//
// The repeat count is not capped: SSMS runs "GO 100000" as asked; callers check
// for cancellation between repetitions.
func SplitBatches(script string) []Batch {
	var batches []Batch
	emit := func(text string, count, startLine int) {
		if strings.TrimSpace(text) != "" {
			batches = append(batches, Batch{Text: text, Count: count, StartLine: startLine})
		}
	}

	var line []rune
	var st State
	batchStart, batchLine := 0, 0
	for pos, lineNo := 0, 0; pos < len(script); lineNo++ {
		// pos is the byte offset of a line start, and next of the line after it.
		end := strings.IndexAny(script[pos:], "\r\n")
		next := len(script)
		if end >= 0 {
			end += pos
			next = end + 1
			if script[end] == '\r' && next < len(script) && script[next] == '\n' {
				next++
			}
		} else {
			end = len(script)
		}
		line = line[:0]
		for _, r := range script[pos:end] {
			line = append(line, r)
		}
		if st.Mode == ModeNormal {
			if _, count, ok := GoSeparatorAt(line, 0, len(line)); ok {
				emit(script[batchStart:pos], count, batchLine)
				batchStart, pos, batchLine = next, next, lineNo+1
				continue
			}
		}
		st = LineEnd(line, st)
		pos = next
	}
	emit(script[batchStart:], 1, batchLine)
	return batches
}
