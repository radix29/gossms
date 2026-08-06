// Package sqlparse is the lexical T-SQL scanner behind the query editor's
// completion: it turns editor lines into a token stream, locates the
// statement the cursor sits in, and reports what that statement puts in
// scope.
//
// This is a lexical approximation, not a T-SQL parser — the same spirit as
// controls.Editor.SelectStatementAtCursor (tuikit/controls/sql_statement.go).
// It recognises enough of the grammar (comments, string and quoted-identifier
// literals, GO batch separators, FROM/JOIN/WHERE/... clause keywords,
// dot-qualified names) to get common queries right; anything genuinely
// ambiguous is reported as unknown rather than guessed at. Subquery contents
// (inside parentheses) are skipped rather than mis-parsed, and CTEs, derived
// tables, temp tables, table variables and cross-database chains are out of
// scope.
//
// The package knows nothing about connections, catalogs, or the application
// — resolving a name against a real database is the caller's job (see
// internal/tui/completion_candidates.go). Everything here is a pure function
// over runes, which is why it can be tested exhaustively: prefix_scan_test.go
// sweeps every cursor position in a corpus of scripts against a golden file.
//
// Statement boundaries come from three sources, all established in one lexer
// pass so that a ';' or a "GO" inside a comment or a literal ends nothing:
// top-level semicolons, bare "GO" separator lines (ScanPrefix,
// StatementEndOffset), and DML leader keywords for statements stacked with no
// ';' between them (NarrowToDMLStatement).
package sqlparse
