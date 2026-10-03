// Package sqlparse is the lexical T-SQL scanner behind the query editor's
// completion: it turns editor lines into a token stream, locates the
// statement the cursor sits in, and reports what that statement puts in
// scope.
//
// The lexing itself is sqltext.Next, the lexer the executor splits batches by
// and the editor selects statements and colours text by; this package turns
// its output into Tokens. Like sqltext.StatementAt, it is a lexical
// approximation, not a T-SQL parser: it recognises enough of the grammar (comments, string and quoted-identifier
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
// UNION/EXCEPT/INTERSECT chains, the PIVOT/UNPIVOT clause reshaping the
// reference it follows, and the WITH column list of an OPENJSON, OPENROWSET or
// OPENXML call (rowset.go). Given a cursor offset it reports the innermost query
// containing it, the CTE definitions visible from there, and the clause state
// within that query rather than within the statement. The tree is still only
// structure — resolving any of those names to columns is the caller's job.
//
// ScanBindings is the one scan that reaches past a statement: temp tables and
// table variables are declared in one statement and used in the next, so it
// collects CREATE TABLE / DECLARE ... TABLE / SELECT ... INTO shapes across a
// whole GO-delimited batch, and CarryTempBindings hands temp tables (not
// variables) on to the batches below until a DROP TABLE. The tokenizer keeps
// the '#'/'@' sigil as part of the identifier, which is what makes those names
// impossible to confuse with a catalog object's. BatchCache answers both per
// keystroke, re-lexing only the part of the script an edit changed.
//
// Names are read whole, up to four parts: FromRef carries a three-part name's
// database and a four-part name's linked server, and QualifierChain gives
// every part ahead of the word being typed ("db.dbo.|"). Which database a part
// names is the caller's to look up.
//
// The package knows nothing about connections, catalogs, or the application
// — resolving a name against a real database is the caller's job (see
// internal/tui/completion_candidates.go). Everything here but the two caches
// (PrefixCache, BatchCache) is a pure function over runes, which is why it can
// be tested exhaustively: prefix_scan_test.go sweeps every cursor position in
// a corpus of scripts against a golden file, and each cache is checked against
// that pure answer after thousands of random edits.
//
// Statement boundaries come from three sources, all established in one lexer
// pass so that a ';' or a "GO" inside a comment or a literal ends nothing:
// top-level semicolons, bare "GO" separator lines (PrefixCache,
// NarrowStatementForward), and DML leader keywords for statements stacked with
// no ';' between them (NarrowToDMLStatement, NarrowStatementForward).
package sqlparse
