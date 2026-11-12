package sqledit

import (
	"strings"
	"unicode"

	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/tuikit/controls/editor"
)

// Configure gives e T-SQL's word rules for completion: where a name starts
// (sqltext.WordStart, so Price$ and t#1 are one name) and which keystrokes may
// open the popup (CompletionTrigger). The highlighter and the indent rule are
// separate, as not every SQL editor wants both: pass Highlighter to
// NewEditor/SetHighlighter and IndentRule to SetIndentRule.
func Configure(e *editor.Editor) {
	e.SetWordStart(sqltext.WordStart)
	e.SetCompletionTrigger(CompletionTrigger)
}

// CompletionTrigger reports whether the text left of col begins a name being
// typed, so a typed character may open the completion popup. The fragment
// touching col must start with a letter or follow a sigil a name can open
// with: '[' for a quoted identifier, '#' or '@' for a name the provider may
// bind. Each sigil also opens the popup on its own, as a name has no other
// first keystroke. A space, '.', a digit starting a numeric literal or an
// empty line never does.
//
// What a sigil means is the provider's business; this only decides that a name
// may be starting.
func CompletionTrigger(line []rune, col int) bool {
	if col <= 0 || col > len(line) {
		return false
	}
	start := sqltext.WordStart(line, col)
	if start == col {
		return isNameSigil(line[col-1])
	}
	return unicode.IsLetter(line[start]) || (start > 0 && isNameSigil(line[start-1]))
}

// isNameSigil reports whether r can introduce a name the provider might know: a
// bracket-quoted identifier, or T-SQL's '#'/'@'.
func isNameSigil(r rune) bool { return r == '[' || r == '#' || r == '@' }

// indentKeywords are the clause keywords that, as the last token on a line,
// open a block the next line belongs inside. Only these three, deliberately not
// BEGIN/END or JOIN: anything needing matching to a closer needs a parser, and a
// half-done one indents wrongly more often than not.
var indentKeywords = map[string]bool{"select": true, "from": true, "where": true}

// IndentRule is T-SQL's smart indent, for editor.Editor.SetIndentRule: one
// extra level when the text left of the cursor ends with an open parenthesis or
// with one of indentKeywords as its last token. "Last token" keeps indentation
// from drifting right across a query: `SELECT` alone indents the column list
// that follows, `SELECT a, b` does not, so the next clause starts at the same
// column as the one above.
func IndentRule(left string) int {
	if strings.HasSuffix(left, "(") {
		return 1
	}
	// The token is whatever follows the last separator; a leading "(" counts as one
	// so "VALUES (SELECT" reads as "SELECT".
	if i := strings.LastIndexAny(left, " \t("); indentKeywords[strings.ToLower(left[i+1:])] {
		return 1
	}
	return 0
}

// SelectStatementAtCursor selects the T-SQL statement containing e's cursor, by
// sqltext.StatementAt's rules — the selection is what the next F5 runs.
//
// No-ops (returns false, selection untouched) if the statement at the cursor is
// empty or all-whitespace — e.g. the cursor sits on a blank line between two GO
// separators.
func SelectStatementAtCursor(e *editor.Editor) bool {
	row, col := e.CursorPos()
	sr, sc, er, ec, ok := sqltext.StatementAt(e.Document().Lines(), row, col)
	if !ok {
		return false
	}
	e.SelectRange(sr, sc, er, ec)
	return true
}
