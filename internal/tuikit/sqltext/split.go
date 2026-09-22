package sqltext

import "strings"

// Batch is one GO-delimited batch of a script and how many times it runs.
type Batch struct {
	Text  string
	Count int
}

// SplitBatches splits script into its GO batches, dropping any that are
// nothing but whitespace.
//
// A line is a separator only by GoSeparatorAt's rule — the one the editor
// selects statements by and IntelliSense scopes completion by — and only when
// it begins outside a 'string', "quoted identifier", [bracketed identifier] or
// /* block comment */, so a GO line inside any of those is text of the batch.
// Block comments nest, as the server's own lexer nests them. An unterminated
// literal or comment runs to the end of the script, which then goes to the
// server as one batch for it to reject.
//
// The count is not capped: SSMS runs "GO 100000" as asked, and a caller
// checks for cancellation between repetitions.
func SplitBatches(script string) []Batch {
	const (
		stNormal = iota
		stBlockComment
		stSingleQuote
		stDoubleQuote
		stBracket
	)
	buf := []rune(script)
	n := len(buf)
	var batches []Batch
	emit := func(from, to, count int) {
		if text := string(buf[from:to]); strings.TrimSpace(text) != "" {
			batches = append(batches, Batch{Text: text, Count: count})
		}
	}

	state, depth := stNormal, 0
	batchStart := 0
	for i := 0; i < n; {
		// i is the start of a line.
		if state == stNormal {
			if next, count, ok := GoSeparatorAt(buf, i, n); ok {
				emit(batchStart, i, count)
				batchStart, i = next, next
				continue
			}
		}
		for i < n {
			c := buf[i]
			i++
			if c == '\n' {
				break
			}
			switch state {
			case stNormal:
				switch {
				case c == '-' && i < n && buf[i] == '-':
					// Line comment: skip to (not past) the newline.
					for i < n && buf[i] != '\n' {
						i++
					}
				case c == '/' && i < n && buf[i] == '*':
					state, depth = stBlockComment, 1
					i++
				case c == '\'':
					state = stSingleQuote
				case c == '"':
					state = stDoubleQuote
				case c == '[':
					state = stBracket
				}
			case stBlockComment:
				switch {
				case c == '/' && i < n && buf[i] == '*':
					depth++
					i++
				case c == '*' && i < n && buf[i] == '/':
					i++
					if depth--; depth == 0 {
						state = stNormal
					}
				}
			case stSingleQuote:
				// A doubled quote leaves and re-enters the literal, so '' needs
				// no case of its own.
				if c == '\'' {
					state = stNormal
				}
			case stDoubleQuote:
				if c == '"' {
					state = stNormal
				}
			case stBracket:
				if c == ']' {
					if i < n && buf[i] == ']' {
						i++
					} else {
						state = stNormal
					}
				}
			}
		}
	}
	emit(batchStart, n, 1)
	return batches
}
