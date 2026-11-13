// Package sqledit is what makes a tuikit editor.Editor a T-SQL editor: the
// syntax highlighter, the line-state cache behind it, Ctrl+Enter's
// select-the-statement-at-the-cursor, the smart-indent rule and the word rules
// completion uses. Each is plugged into the Editor through its generic hooks
// (SetHighlighter, editor.LineStateAt, SelectRange, SetIndentRule,
// SetWordStart, SetCompletionTrigger), so tuikit knows no T-SQL.
//
// All of it lexes with internal/sqltext, gossms's one T-SQL lexer
// (docs/decisions.md § T-SQL lexing). Completion itself — what to offer — is
// sqlparse plus the app's provider; this package only says where a name starts.
package sqledit
