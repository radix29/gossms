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
// ambiguous is reported as unknown rather than guessed at.
//
// Two levels of scanning live here. The flat one — ParseFromScope,
// CurrentClause — skips parenthesised contents outright and answers for the
// statement as a whole. ScopeAt parses the same tokens into a shallow Query
// tree instead, so what is inside parentheses is structure rather than a gap:
// WITH bindings (including an explicit column list and a CTE referencing an
// earlier one), derived tables in FROM/JOIN/APPLY, sub-SELECTs anywhere,
// UNION/EXCEPT/INTERSECT chains, and the PIVOT/UNPIVOT clause reshaping the
// reference it follows. Given a cursor offset it reports the innermost query
// containing it, the CTE definitions visible from there, and the clause state
// within that query rather than within the statement. The tree is still only
// structure — resolving any of those names to columns is the caller's job.
//
// ScanBindings is the one scan that reaches past a statement: temp tables and
// table variables are declared in one statement and used in the next, so it
// collects CREATE TABLE / DECLARE ... TABLE / SELECT ... INTO shapes across a
// whole GO-delimited batch. The tokenizer keeps the '#'/'@' sigil as part of
// the identifier, which is what makes those names impossible to confuse with
// a catalog object's.
//
// Still out of scope, and reported as nothing rather than guessed: the result
// shape of a table-valued function, the WITH column lists of
// OPENJSON/OPENROWSET, and cross-database three-part chains.
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
