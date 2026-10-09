// Package sqlparse is the lexical T-SQL scanner behind the query editor's
// completion: it turns editor lines into a token stream, locates the
// statement the cursor sits in, and reports what that statement puts in
// scope.
//
// The lexing itself is sqltext.Next, the lexer the executor splits batches by
// and the editor selects statements and colours text by; this package turns
// its output into Tokens. Like sqltext.StatementAt, it is a lexical
// approximation, not a T-SQL parser: it recognises enough of the grammar
// (comments, string and quoted-identifier literals, GO batch separators,
// FROM/JOIN/WHERE/... clause keywords, dot-qualified names) for common queries;
// anything genuinely ambiguous is reported as unknown, not guessed.
//
// Two levels of scanning live here. The flat one (ParseFromScope,
// CurrentClause) skips parenthesised contents and answers for the whole
// statement. ScopeAt parses the same tokens into a shallow Query tree, so
// parenthesised content is structure, not a gap: WITH bindings (explicit column
// list, a CTE referencing an earlier one), derived tables in FROM/JOIN/APPLY,
// sub-SELECTs anywhere, UNION/EXCEPT/INTERSECT chains, PIVOT/UNPIVOT reshaping
// the reference it follows, and the WITH column list of an OPENJSON,
// OPENROWSET or OPENXML call (rowset.go). Given a cursor offset it reports the
// innermost containing query, the CTE definitions visible from there, and the
// clause state within that query. The tree is only structure: resolving names
// to columns is the caller's job.
//
// ScanBindings is the one scan reaching past a statement: temp tables and table
// variables are declared in one statement and used in the next, so it collects
// CREATE TABLE / DECLARE ... TABLE / SELECT ... INTO shapes across a whole
// GO-delimited batch, and CarryTempBindings hands temp tables (not variables)
// on to later batches until a DROP TABLE. The tokenizer keeps the '#'/'@'
// sigil in the identifier, so those names can't be confused with a catalog
// object's. BatchCache answers both per keystroke, re-lexing only the part of
// the script an edit changed.
//
// Names are read whole, up to four parts: FromRef carries a three-part name's
// database and a four-part name's linked server, and QualifierChain gives
// every part ahead of the word being typed ("db.dbo.|"). Which database a part
// names is the caller's to look up.
//
// The package knows nothing about connections, catalogs or the application
// (see internal/tui/completion_candidates.go). Everything but the two caches
// (PrefixCache, BatchCache) is a pure function over runes, hence exhaustively
// testable: prefix_scan_test.go sweeps every cursor position in a corpus
// against a golden file, and each cache is checked against that pure answer
// after thousands of random edits.
//
// Statement boundaries come from three sources, all established in one lexer
// pass so a ';' or "GO" inside a comment or literal ends nothing: top-level
// semicolons, bare "GO" separator lines (PrefixCache, NarrowStatementForward),
// and DML leader keywords for statements stacked with no ';' between them
// (NarrowToDMLStatement, NarrowStatementForward).
package sqlparse
