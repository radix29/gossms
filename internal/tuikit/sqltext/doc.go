// Package sqltext holds the T-SQL text rules more than one package must agree
// on, as plain functions over runes and strings. It imports nothing but the
// standard library — no tcell, no other tuikit package — so the editor
// (controls), IntelliSense (internal/tui/sqlparse) and the script executor
// (internal/query) can all share it without any of them importing the others.
//
//   - lexer.go        — Next, the one T-SQL lexer: comments (nested), literals,
//     quoted identifiers, words with their #/@ sigils, numbers
//   - go_separator.go — the "GO" batch-separator line rule and its repeat count
//   - split.go        — SplitBatches, the executor's script → GO-batch splitter
//   - statement.go    — StatementAt, the editor's Ctrl+Enter statement bounds,
//     and the statement-verb sets IntelliSense splits a batch with
package sqltext
