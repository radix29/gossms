package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// SQL-aware completion.Provider for the query editor — the only caller of
// controls.Editor.SetCompletionProvider in the app. Resolves the identifier
// at the cursor against the connected database's completionInventory
// (completion_inventory.go): schemas, tables, views, and columns, with
// schema/alias/table-dot member lookup and FROM-clause alias resolution.
//
// This is a lexical approximation, not a full T-SQL parser — the same
// spirit as controls.Editor's own SelectStatementAtCursor (see
// tuikit/controls/sql_statement.go's doc comment). It recognises enough of
// the grammar (comments, string/quoted-identifier literals, FROM/JOIN/
// WHERE/... clause keywords, dot-qualified names) to get common queries
// right; anything genuinely ambiguous just offers nothing rather than
// guessing wrong. Keyword completion, CTEs/derived tables, temp tables and
// table variables, and cross-database chains are out of scope for now.
// ---------------------------------------------------------------------------

// newCompletionProvider builds the controls.CompletionProvider installed on
// this panel's editor (see NewQueryPanel). p's conn/database are read fresh
// on every call, so reconnecting or switching database (a mid-script USE)
// takes effect on the very next keystroke without needing to rebuild it.
func (p *QueryPanel) newCompletionProvider() controls.CompletionProvider {
	return func(lines [][]rune, row, col int) ([]controls.CompletionItem, int) {
		return p.sqlCompletionCandidates(lines, row, col)
	}
}

// loadingCompletionItem is shown, alone, while the backing inventory hasn't
// finished its first load yet — see completion_inventory.go's
// refreshCompletionPopups, which re-queries this provider once the real
// data lands so this placeholder gets replaced without another keystroke.
var loadingCompletionItem = controls.CompletionItem{Label: "Loading suggestions...", Placeholder: true}

func (p *QueryPanel) sqlCompletionCandidates(lines [][]rune, row, col int) ([]controls.CompletionItem, int) {
	if p.app.cfg.IntelliSenseDisabled {
		return nil, col
	}
	if p.conn == nil || !p.app.isConnected(p.conn) {
		return nil, col
	}

	buf := flattenLines(lines)
	upTo := offsetForCursor(lines, row, col)
	tokens, state, semiStart, quoteStart := tokenizeSQLPrefix(buf, upTo)

	// Scope every lookup below to the current statement — a table named in
	// an earlier ';'- or GO-separated statement must not leak into this
	// one's FROM-scope/clause detection (see statementStartOffset).
	batchStart := statementStartOffset(lines, row, semiStart)
	tokens = tokensFrom(tokens, batchStart)

	var qualifier, prefix string
	var replaceFrom int
	var hasQualifier bool
	switch state {
	case sqlLexNormal:
		qualifier, prefix, replaceFrom, hasQualifier = completionTokenContext(tokens, upTo)
	case sqlLexBracket:
		// An unterminated bracket identifier ("FROM [Cus|") is the one
		// non-normal lexer state completion still works in: everything
		// after the '[' is the prefix, and the whole "[..." span gets
		// replaced on commit (bracketIfNeeded re-quotes the inserted name
		// only when it actually needs quoting).
		qualifier, _, _, hasQualifier = completionTokenContext(tokens, quoteStart)
		prefix = string(buf[quoteStart+1 : upTo])
		replaceFrom = quoteStart
	default:
		return nil, col // inside a string literal or comment
	}

	// Everything above works in flattened-buffer offsets, but the
	// controls.CompletionProvider contract wants a column on the cursor's
	// row — the editor replaces [replaceFrom, col) there and anchors the
	// popup at it. The replaced span always starts on the cursor's own row
	// (identifiers can't span lines; a '[' on an earlier row is malformed
	// anyway and bails), so subtracting the row's start offset converts it.
	rowStart := offsetForCursor(lines, row, 0)
	if replaceFrom < rowStart {
		return nil, col
	}
	replaceFrom -= rowStart

	inv := p.app.ensureCompletionInventory(p.conn, p.database)
	if inv.loading {
		return []controls.CompletionItem{loadingCompletionItem}, replaceFrom
	}
	if inv.err != nil || inv.catalog == nil {
		return nil, replaceFrom
	}
	sysInv := p.app.ensureSysCompletionInventory(p.conn)

	// FROM-scope/clause analysis looks at the whole current statement, not
	// just the part already typed — a table named in "SELECT | FROM
	// Customers c" (cursor still in the column list) must resolve just as
	// well as one already fully typed above the cursor, matching how SSMS
	// itself parses the entire statement regardless of cursor position.
	// The forward scan must resume in normal lexer state; when the cursor
	// sits inside an unterminated bracket identifier, skip past its closing
	// ']' (if any) first.
	forwardFrom := upTo
	if state == sqlLexBracket {
		for forwardFrom < len(buf) && buf[forwardFrom] != ']' {
			forwardFrom++
		}
		if forwardFrom < len(buf) {
			forwardFrom++
		}
	}
	batchEnd := statementEndOffset(lines, buf, row, forwardFrom)
	forwardTokens, _, _, _ := tokenizeSQLRange(buf, forwardFrom, batchEnd, false)

	// Multiple statements stacked in the editor with no ';' between them —
	// extremely common in ad hoc SSMS-style scripts, where a trailing ';'
	// is optional — still parse as one ';'/GO-delimited batch above;
	// narrow further to the actual DML statement containing the cursor so
	// a bare column context in one statement doesn't pick up FROM refs
	// from an unrelated statement stacked above or below it (see
	// dmlStatementStarts/narrowToDMLStatement).
	combined := append(append([]sqlToken{}, tokens...), forwardTokens...)
	stmtStart, stmtEnd := narrowToDMLStatement(combined, batchStart, batchEnd, upTo)
	tokens = tokensFrom(tokens, stmtStart)
	if stmtEnd < batchEnd {
		forwardTokens, _, _, _ = tokenizeSQLRange(buf, forwardFrom, stmtEnd, false)
	}
	refs := parseFromScope(append(append([]sqlToken{}, tokens...), forwardTokens...))

	switch {
	case hasQualifier:
		return p.memberCandidates(inv, sysInv, refs, qualifier, prefix), replaceFrom
	case currentClause(tokens) == clauseTable:
		return p.tableCandidates(inv, sysInv, prefix), replaceFrom
	case len(refs) == 0:
		// Column context but nothing's been FROM'd yet — nothing to pull
		// columns from, so fall back to the object list.
		return p.tableCandidates(inv, sysInv, prefix), replaceFrom
	default:
		return p.scopedColumnCandidates(inv, sysInv, refs, prefix), replaceFrom
	}
}

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

type sqlTokKind int

const (
	sqlTokIdent sqlTokKind = iota
	sqlTokKeyword
	sqlTokDot
	sqlTokComma
	sqlTokParenOpen
	sqlTokParenClose
)

type sqlToken struct {
	kind  sqlTokKind
	text  string // sqlTokIdent: unwrapped name; sqlTokKeyword: uppercased
	start int    // rune offset into the flattened buffer
}

type sqlLexState int

const (
	sqlLexNormal sqlLexState = iota
	sqlLexLineComment
	sqlLexBlockComment
	sqlLexSingleQuote
	sqlLexBracket
	sqlLexDoubleQuote
)

// flattenLines joins a multi-line buffer into one rune slice with '\n'
// separators, so the tokenizer can scan linearly without juggling
// (row, col) pairs — a comment or string literal spanning several lines
// then falls out of the same state machine for free.
func flattenLines(lines [][]rune) []rune {
	var buf []rune
	for i, l := range lines {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, l...)
	}
	return buf
}

// offsetForCursor converts an Editor (row, col) into an offset into
// flattenLines' output.
func offsetForCursor(lines [][]rune, row, col int) int {
	off := 0
	for i := 0; i < row && i < len(lines); i++ {
		off += len(lines[i]) + 1
	}
	if row < len(lines) {
		off += core.Clamp(col, 0, len(lines[row]))
	}
	return off
}

// tokenizeSQLPrefix scans buf[:upTo] into a token stream, and reports the
// lexer's state at upTo (sqlLexBracket means upTo sits inside an
// unterminated bracket identifier, which sqlCompletionCandidates still
// completes; any other non-normal state suppresses completion entirely
// rather than guessing) plus the offset right after the last top-level ';'
// seen — one of the two inputs statementStartOffset combines with GO-line
// detection to scope FROM/clause analysis to the current statement only —
// and the quoteStart offset (see tokenizeSQLRange).
func tokenizeSQLPrefix(buf []rune, upTo int) ([]sqlToken, sqlLexState, int, int) {
	return tokenizeSQLRange(buf, 0, upTo, false)
}

// tokenizeSQLRange scans buf[from:upTo] into a token stream, always
// starting in sqlLexNormal state — valid both for tokenizeSQLPrefix's
// from-buffer-start scan and for a scan resumed exactly at the cursor
// (statementEndOffset, and sqlCompletionCandidates' own forward scan),
// since callers only ever resume there after confirming the lexer state at
// that offset is already sqlLexNormal (see sqlCompletionCandidates' "inside
// a string/quoted-identifier/comment" bail-out).
//
// stopAtSemicolon changes what the third return value means and, for a
// resumed/forward scan, where scanning stops:
//   - false (tokenizeSQLPrefix's use, and the forward token scan that
//     extends FROM-scope analysis past the cursor): scanning continues
//     through every top-level ';' up to upTo, and the third return is the
//     offset right after the LAST one seen — statementStartOffset's other
//     input.
//   - true (statementEndOffset's use): scanning stops at the FIRST
//     top-level ';', and the third return is that ';'s own offset (or upTo
//     if none was found) — the statement's end boundary.
//
// The fourth return is the offset of the opening '[' or '"' when the final
// state is sqlLexBracket/sqlLexDoubleQuote — how sqlCompletionCandidates
// finds where an unterminated bracket identifier's replace span starts.
// Meaningless (stale or zero) in every other final state.
func tokenizeSQLRange(buf []rune, from, upTo int, stopAtSemicolon bool) ([]sqlToken, sqlLexState, int, int) {
	var tokens []sqlToken
	state := sqlLexNormal
	quoteStart := 0
	semiStart := from
	i := from
	for i < upTo {
		c := buf[i]
		switch state {
		case sqlLexLineComment:
			if c == '\n' {
				state = sqlLexNormal
			}
			i++
			continue
		case sqlLexBlockComment:
			if c == '*' && i+1 < upTo && buf[i+1] == '/' {
				state = sqlLexNormal
				i += 2
			} else {
				i++
			}
			continue
		case sqlLexSingleQuote:
			if c == '\'' {
				if i+1 < upTo && buf[i+1] == '\'' {
					i += 2
					continue
				}
				state = sqlLexNormal
				i++
				continue
			}
			i++
			continue
		case sqlLexDoubleQuote:
			if c == '"' {
				if i+1 < upTo && buf[i+1] == '"' {
					i += 2
					continue
				}
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: string(buf[quoteStart+1 : i]), start: quoteStart})
				state = sqlLexNormal
				i++
				continue
			}
			i++
			continue
		case sqlLexBracket:
			if c == ']' {
				if i+1 < upTo && buf[i+1] == ']' {
					i += 2
					continue
				}
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: string(buf[quoteStart+1 : i]), start: quoteStart})
				state = sqlLexNormal
				i++
				continue
			}
			i++
			continue
		}

		// state == sqlLexNormal
		switch {
		case c == '-' && i+1 < upTo && buf[i+1] == '-':
			state = sqlLexLineComment
			i += 2
		case c == '/' && i+1 < upTo && buf[i+1] == '*':
			state = sqlLexBlockComment
			i += 2
		case c == '\'':
			state = sqlLexSingleQuote
			i++
		case c == '"':
			state = sqlLexDoubleQuote
			quoteStart = i
			i++
		case c == '[':
			state = sqlLexBracket
			quoteStart = i
			i++
		case c == '.':
			tokens = append(tokens, sqlToken{kind: sqlTokDot, start: i})
			i++
		case c == ',':
			tokens = append(tokens, sqlToken{kind: sqlTokComma, start: i})
			i++
		case c == '(':
			tokens = append(tokens, sqlToken{kind: sqlTokParenOpen, start: i})
			i++
		case c == ')':
			tokens = append(tokens, sqlToken{kind: sqlTokParenClose, start: i})
			i++
		case c == ';':
			if stopAtSemicolon {
				return tokens, state, i, quoteStart
			}
			semiStart = i + 1
			i++
		case core.IsWordRune(c):
			start := i
			for i < upTo && core.IsWordRune(buf[i]) {
				i++
			}
			word := string(buf[start:i])
			if sqlKeywords[strings.ToUpper(word)] {
				tokens = append(tokens, sqlToken{kind: sqlTokKeyword, text: strings.ToUpper(word), start: start})
			} else {
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: word, start: start})
			}
		default:
			i++ // whitespace, operators, semicolons, @/# sigils, numeric literals, ...
		}
	}
	if stopAtSemicolon {
		return tokens, state, upTo, quoteStart
	}
	return tokens, state, semiStart, quoteStart
}

// statementEndOffset finds where the statement containing the cursor ends —
// the next top-level ';' at or after upTo, the start of the next bare "GO"
// batch-separator line after cursorRow, or len(buf), whichever comes first.
// Combined with statementStartOffset, this lets FROM-scope/clause analysis
// see the whole current statement regardless of where in it the cursor
// sits — a table named in "SELECT | FROM Customers c" resolves the same as
// one already fully typed above the cursor.
func statementEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	end := len(buf)
	if _, _, stop, _ := tokenizeSQLRange(buf, upTo, len(buf), true); stop < end {
		end = stop
	}
	for row := cursorRow + 1; row < len(lines); row++ {
		if strings.EqualFold(strings.TrimSpace(string(lines[row])), "GO") {
			if goStart := offsetForCursor(lines, row, 0); goStart < end {
				end = goStart
			}
			break
		}
	}
	return end
}

// statementStartOffset combines the last top-level ';' tokenizeSQLPrefix
// saw (semiStart) with the nearest bare "GO" batch-separator line strictly
// above cursorRow, and returns whichever is later — the same two
// boundaries controls.Editor.SelectStatementAtCursor recognises (see
// tuikit/controls/sql_statement.go's isGoSeparatorLine), reimplemented
// here in the simpler form completion context needs: no repeat-count or
// trailing-comment parsing, just "this line is exactly GO".
func statementStartOffset(lines [][]rune, cursorRow int, semiStart int) int {
	start := semiStart
	for i := 0; i < cursorRow && i < len(lines); i++ {
		if strings.EqualFold(strings.TrimSpace(string(lines[i])), "GO") {
			if goStart := offsetForCursor(lines, i+1, 0); goStart > start {
				start = goStart
			}
		}
	}
	return start
}

// tokensFrom returns the suffix of tokens (already in ascending start
// order) beginning at the first one whose start is >= from.
func tokensFrom(tokens []sqlToken, from int) []sqlToken {
	for i, t := range tokens {
		if t.start >= from {
			return tokens[i:]
		}
	}
	return nil
}

// sqlKeywords recognises enough T-SQL clause/reserved words for clause
// detection, FROM-scope parsing, and deciding when a candidate name needs
// bracket-quoting — not the engine's full reserved-word list.
var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "JOIN": true, "INNER": true,
	"LEFT": true, "RIGHT": true, "FULL": true, "OUTER": true, "CROSS": true,
	"ON": true, "GROUP": true, "ORDER": true, "BY": true, "HAVING": true,
	"INSERT": true, "INTO": true, "VALUES": true, "UPDATE": true, "SET": true,
	"DELETE": true, "TRUNCATE": true, "TABLE": true, "AS": true, "AND": true,
	"OR": true, "NOT": true, "NULL": true, "IS": true, "IN": true,
	"EXISTS": true, "BETWEEN": true, "LIKE": true, "DISTINCT": true, "TOP": true,
	"UNION": true, "EXCEPT": true, "INTERSECT": true, "ALL": true, "CASE": true, "WHEN": true, "THEN": true,
	"ELSE": true, "END": true, "CAST": true, "CONVERT": true, "DECLARE": true,
	"EXEC": true, "EXECUTE": true, "PROCEDURE": true, "FUNCTION": true,
	"VIEW": true, "INDEX": true, "PRIMARY": true, "KEY": true, "FOREIGN": true,
	"REFERENCES": true, "DEFAULT": true, "CHECK": true, "CONSTRAINT": true,
	"ALTER": true, "DROP": true, "CREATE": true, "WITH": true, "MERGE": true,
}

// ---------------------------------------------------------------------------
// Cursor context: what's being typed, and whether it's already dot-qualified
// ---------------------------------------------------------------------------

// completionTokenContext inspects the tail of tokens (already scanned up to
// upTo) and reports:
//   - prefix: the identifier characters immediately touching the cursor
//     ("" if the cursor sits after whitespace/punctuation instead)
//   - replaceFrom: where that prefix starts (== upTo when there's no prefix)
//   - qualifier, hasQualifier: the identifier immediately before a '.' that
//     itself immediately precedes prefix/the cursor, if any
//
// A keyword token touching the cursor counts as a prefix too — the word
// being typed may only collide with a keyword by accident ("OR" on the way
// to Orders, "sys.all" on the way to sys.all_objects), and treating it as
// anything else would make a commit append instead of replace. Keyword
// tokens carry uppercased text, which is fine: prefix matching is
// case-insensitive everywhere downstream.
func completionTokenContext(tokens []sqlToken, upTo int) (qualifier, prefix string, replaceFrom int, hasQualifier bool) {
	n := len(tokens)
	if n == 0 {
		return "", "", upTo, false
	}
	last := tokens[n-1]
	lastIsWord := last.kind == sqlTokIdent || last.kind == sqlTokKeyword
	switch {
	case lastIsWord && last.start+len([]rune(last.text)) == upTo:
		prefix = last.text
		replaceFrom = last.start
		if n >= 3 && tokens[n-2].kind == sqlTokDot && tokens[n-3].kind == sqlTokIdent {
			qualifier = tokens[n-3].text
			hasQualifier = true
		}
	case last.kind == sqlTokDot && last.start+1 == upTo:
		replaceFrom = upTo
		if n >= 2 && tokens[n-2].kind == sqlTokIdent {
			qualifier = tokens[n-2].text
			hasQualifier = true
		}
	default:
		replaceFrom = upTo
	}
	return
}

// ---------------------------------------------------------------------------
// FROM-scope: which tables/views/aliases are in play for the statement the
// cursor is currently in
// ---------------------------------------------------------------------------

// fromRef is one table/view reference parsed out of a FROM/JOIN/INTO/
// UPDATE/DELETE clause, with its optional AS alias.
type fromRef struct {
	schema, name, alias string
}

// parseFromScope walks tokens looking for table references introduced by
// FROM, JOIN, INTO, UPDATE, or DELETE, each optionally schema-qualified and
// optionally aliased (bare "AS alias" or just a trailing identifier).
// Subquery contents (inside parentheses) are skipped rather than
// mis-parsed — a documented v1 limitation, not a bug: see the package doc
// comment above.
func parseFromScope(tokens []sqlToken) []fromRef {
	var refs []fromRef
	depth := 0
	expectRef := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch t.kind {
		case sqlTokParenOpen:
			depth++
			continue
		case sqlTokParenClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if t.kind == sqlTokKeyword {
			switch t.text {
			case "FROM", "JOIN", "INTO", "UPDATE", "DELETE":
				expectRef = true
			case "WHERE", "ON", "GROUP", "ORDER", "HAVING", "SET", "VALUES", "AND", "OR",
				"UNION", "EXCEPT", "INTERSECT":
				expectRef = false
			}
			continue
		}
		if expectRef && t.kind == sqlTokIdent {
			ref := fromRef{name: t.text}
			j := i + 1
			if j+1 < len(tokens) && tokens[j].kind == sqlTokDot && tokens[j+1].kind == sqlTokIdent {
				ref.schema = t.text
				ref.name = tokens[j+1].text
				j += 2
			}
			if j < len(tokens) && tokens[j].kind == sqlTokKeyword && tokens[j].text == "AS" {
				j++
			}
			if j < len(tokens) && tokens[j].kind == sqlTokIdent {
				ref.alias = tokens[j].text
				j++
			}
			refs = append(refs, ref)
			i = j - 1
		}
	}
	return refs
}

// dmlStatementLeaders are the T-SQL keywords that can only ever begin a
// new statement — used by dmlStatementStarts to split multiple statements
// stacked in the editor with no ';' between them, the common way ad hoc
// scripts get written in SSMS (a trailing ';' is optional, not required).
var dmlStatementLeaders = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "WITH": true,
}

// dmlStatementStarts scans tokens — already correctly depth-tracked from
// its own start, since a ';'/GO textual boundary always falls outside any
// paren in valid SQL — and returns, in ascending order, the offset of
// every top-level dmlStatementLeaders keyword that actually begins a new
// statement rather than continuing the current one:
//   - a SELECT chained onto the previous top-level clause by
//     UNION[ ALL]/EXCEPT/INTERSECT is the same statement, not a new one
//   - the first top-level SELECT after WITH or after an INSERT with no
//     intervening VALUES is that statement's own main query/source
//     (CTE's SELECT, INSERT ... SELECT), not a new one — only WITH/INSERT
//     itself is the boundary; an INSERT ... VALUES has no such SELECT to
//     suppress, so a later, genuinely separate SELECT stacked right after
//     it with no ';' is (rarely) missed — a known best-effort limitation,
//     not a full parser, matching parseFromScope's own documented ones
//
// Combined with the ';'/GO boundaries statementStartOffset/
// statementEndOffset already apply, this narrows FROM-scope/clause
// analysis to the actual statement under the cursor even when the editor
// holds several statements back to back with no ';' between them.
func dmlStatementStarts(tokens []sqlToken) []int {
	var starts []int
	depth := 0
	prevKeyword, prevPrevKeyword := "", ""
	pendingMainSelect := false
	for _, t := range tokens {
		switch t.kind {
		case sqlTokParenOpen:
			depth++
			continue
		case sqlTokParenClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || t.kind != sqlTokKeyword {
			continue
		}
		switch {
		case t.text == "VALUES":
			pendingMainSelect = false
		case dmlStatementLeaders[t.text]:
			continuesUnion := prevKeyword == "UNION" || prevKeyword == "EXCEPT" || prevKeyword == "INTERSECT" ||
				(prevKeyword == "ALL" && prevPrevKeyword == "UNION")
			switch {
			case t.text == "SELECT" && pendingMainSelect:
				pendingMainSelect = false
			case t.text == "SELECT" && continuesUnion:
				// UNION-chain continuation of the same statement.
			default:
				starts = append(starts, t.start)
				pendingMainSelect = t.text == "WITH" || t.text == "INSERT"
			}
		}
		prevPrevKeyword = prevKeyword
		prevKeyword = t.text
	}
	return starts
}

// narrowToDMLStatement tightens [batchStart, batchEnd) — the ';'/GO-
// delimited boundaries statementStartOffset/statementEndOffset already
// computed — to the actual DML statement containing upTo, using
// dmlStatementStarts on tokens (which must already span the same
// [batchStart, batchEnd) range so its depth tracking starts at 0).
func narrowToDMLStatement(tokens []sqlToken, batchStart, batchEnd, upTo int) (start, end int) {
	start, end = batchStart, batchEnd
	for _, off := range dmlStatementStarts(tokens) {
		switch {
		case off <= upTo && off > start:
			start = off
		case off > upTo && off < end:
			return start, off // ascending order: first hit is the closest
		}
	}
	return start, end
}

// sqlClause is the coarse "what kind of name is expected here" state
// currentClause tracks — the last clause-introducing keyword before the
// cursor wins, ignoring subquery contents (paren depth > 0).
type sqlClause int

const (
	clauseUnknown sqlClause = iota
	clauseTable
	clauseColumn
)

func currentClause(tokens []sqlToken) sqlClause {
	clause := clauseUnknown
	depth := 0
	for _, t := range tokens {
		switch t.kind {
		case sqlTokParenOpen:
			depth++
			continue
		case sqlTokParenClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || t.kind != sqlTokKeyword {
			continue
		}
		switch t.text {
		case "SELECT", "WHERE", "ON", "HAVING", "SET", "AND", "OR", "BY":
			clause = clauseColumn
		case "FROM", "JOIN", "INTO", "UPDATE", "DELETE", "TABLE":
			clause = clauseTable
		}
	}
	return clause
}

// ---------------------------------------------------------------------------
// Candidate resolution against a completionInventory
// ---------------------------------------------------------------------------

// resolveQualifierToObject resolves a dot-qualifier to the CatalogObject it
// names: first an alias or bare table name already in refs (the common
// case), falling back to a direct name match across the whole inventory for
// a table the FROM-scope parse missed or that isn't in scope yet. sysInv is
// consulted too, so an alias/bare-name over a "sys.xxx" reference (e.g.
// "FROM sys.objects o") resolves its columns the same way a user table
// would.
func resolveQualifierToObject(inv, sysInv *completionInventory, refs []fromRef, qualifier string) *gosmo.CatalogObject {
	ql := strings.ToLower(qualifier)
	for _, ref := range refs {
		if ref.alias != "" && strings.ToLower(ref.alias) == ql {
			return findCatalogObject(inv, sysInv, ref.schema, ref.name)
		}
	}
	for _, ref := range refs {
		if ref.alias == "" && strings.ToLower(ref.name) == ql {
			return findCatalogObject(inv, sysInv, ref.schema, ref.name)
		}
	}
	return findCatalogObjectByName(inv, sysInv, qualifier)
}

func findCatalogObject(inv, sysInv *completionInventory, schema, name string) *gosmo.CatalogObject {
	if schema == "" {
		return findCatalogObjectByName(inv, sysInv, name)
	}
	key := strings.ToLower(schema) + "." + strings.ToLower(name)
	if obj, ok := inv.byQualifiedName[key]; ok {
		return obj
	}
	if sysInv != nil {
		if obj, ok := sysInv.byQualifiedName[key]; ok {
			return obj
		}
	}
	return nil
}

func findCatalogObjectByName(inv, sysInv *completionInventory, name string) *gosmo.CatalogObject {
	nl := strings.ToLower(name)
	for i := range inv.catalog.Objects {
		if strings.ToLower(inv.catalog.Objects[i].Name) == nl {
			return &inv.catalog.Objects[i]
		}
	}
	if sysInv != nil && sysInv.catalog != nil {
		for i := range sysInv.catalog.Objects {
			if strings.ToLower(sysInv.catalog.Objects[i].Name) == nl {
				return &sysInv.catalog.Objects[i]
			}
		}
	}
	return nil
}

// memberCandidates resolves "qualifier.prefix": qualifier is tried first as
// a FROM-scope alias/table (-> that object's columns), then as a schema
// name in the connected database (-> every table/view in it), then as a
// schema name in the sys-schema inventory ("sys" being the only one that
// ever matters there). Nothing matching returns nil, closing the popup
// rather than showing something wrong.
func (p *QueryPanel) memberCandidates(inv, sysInv *completionInventory, refs []fromRef, qualifier, prefix string) []controls.CompletionItem {
	if obj := resolveQualifierToObject(inv, sysInv, refs, qualifier); obj != nil {
		return p.columnItemsFor(obj, prefix)
	}
	if objs, ok := inv.bySchema[strings.ToLower(qualifier)]; ok {
		return p.objectItems(objs, prefix)
	}
	if sysInv != nil {
		if objs, ok := sysInv.bySchema[strings.ToLower(qualifier)]; ok {
			return p.objectItems(objs, prefix)
		}
		if sysInv.loading && strings.EqualFold(qualifier, "sys") {
			return []controls.CompletionItem{loadingCompletionItem}
		}
	}
	return nil
}

// tableCandidates offers every schema (the connected database's own, plus
// "sys" once its inventory has loaded) and every table/view whose name
// starts with prefix — the FROM/JOIN/INTO/UPDATE/DELETE/TRUNCATE TABLE
// context, and the fallback when a column context has no FROM-scope yet.
// The sys-schema inventory's own objects are deliberately not mixed into
// the unqualified list below (unlike the connected database's own tables) —
// there are hundreds of them, so offering them only once a query actually
// qualifies with "sys." (see memberCandidates) keeps this list from
// drowning in system catalog views nobody typed a prefix for.
func (p *QueryPanel) tableCandidates(inv, sysInv *completionInventory, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, schema := range inv.catalog.Schemas {
		if !strings.HasPrefix(strings.ToLower(schema), pl) {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
		})
	}
	if sysInv != nil && sysInv.catalog != nil {
		for _, schema := range sysInv.catalog.Schemas {
			if !strings.HasPrefix(strings.ToLower(schema), pl) {
				continue
			}
			items = append(items, controls.CompletionItem{
				Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
			})
		}
	}
	for i := range inv.catalog.Objects {
		obj := &inv.catalog.Objects[i]
		if strings.HasPrefix(strings.ToLower(obj.Name), pl) {
			items = append(items, p.objectItem(obj))
		}
	}
	sortCompletionItems(items)
	return items
}

// objectItems offers every table/view in objs whose name starts with
// prefix — a schema's member list ("dbo.").
func (p *QueryPanel) objectItems(objs []*gosmo.CatalogObject, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, obj := range objs {
		if strings.HasPrefix(strings.ToLower(obj.Name), pl) {
			items = append(items, p.objectItem(obj))
		}
	}
	sortCompletionItems(items)
	return items
}

func (p *QueryPanel) objectItem(obj *gosmo.CatalogObject) controls.CompletionItem {
	detail := "table"
	if obj.Type == gosmo.CatalogView {
		detail = "view"
	}
	return controls.CompletionItem{
		Text: bracketIfNeeded(obj.Name), Label: obj.Schema + "." + obj.Name,
		Detail: detail, Icon: p.tableIcon(obj.Type),
	}
}

// columnItemsFor offers every column of obj whose name starts with prefix —
// the "alias." / "table." member-lookup result.
func (p *QueryPanel) columnItemsFor(obj *gosmo.CatalogObject, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, col := range obj.Columns {
		if !strings.HasPrefix(strings.ToLower(col.Name), pl) {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(col.Name), Label: col.Name,
			Detail: formatColumnType(col), Icon: p.columnIcon(),
		})
	}
	sortCompletionItems(items)
	return items
}

// scopedColumnCandidates offers the union of every FROM-scope ref's
// columns (deduplicated by name — a column present on more than one joined
// table shows once) plus each ref's alias/table name itself, so typing
// "c." after "c" was just offered still works — the unqualified SELECT/
// WHERE/ON/GROUP BY/ORDER BY/HAVING/SET context.
func (p *QueryPanel) scopedColumnCandidates(inv, sysInv *completionInventory, refs []fromRef, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	seenCol := make(map[string]bool)
	seenRef := make(map[string]bool)
	for _, ref := range refs {
		obj := findCatalogObject(inv, sysInv, ref.schema, ref.name)
		if obj != nil {
			for _, col := range obj.Columns {
				key := strings.ToLower(col.Name)
				if seenCol[key] || !strings.HasPrefix(key, pl) {
					continue
				}
				seenCol[key] = true
				qname := ref.alias
				if qname == "" {
					qname = ref.name
				}
				items = append(items, controls.CompletionItem{
					Text: bracketIfNeeded(col.Name), Label: col.Name,
					Detail: formatColumnType(col) + " — " + qname, Icon: p.columnIcon(),
				})
			}
		}
		qname := ref.alias
		if qname == "" {
			qname = ref.name
		}
		qkey := strings.ToLower(qname)
		if qname == "" || seenRef[qkey] || !strings.HasPrefix(qkey, pl) {
			continue
		}
		seenRef[qkey] = true
		objType := gosmo.CatalogTable
		if obj != nil {
			objType = obj.Type
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(qname), Label: qname, Detail: "table reference", Icon: p.tableIcon(objType),
		})
	}
	sortCompletionItems(items)
	return items
}

func sortCompletionItems(items []controls.CompletionItem) {
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
}

// ---------------------------------------------------------------------------
// Icons and formatting
// ---------------------------------------------------------------------------

func (p *QueryPanel) tableIcon(t gosmo.CatalogObjectType) rune {
	nt := NodeTable
	if t == gosmo.CatalogView {
		nt = NodeView
	}
	return nodeIcon(nodeData{Type: nt}, p.app.cfg.IconStyle, false)
}

func (p *QueryPanel) columnIcon() rune {
	return nodeIcon(nodeData{Type: NodeColumn}, p.app.cfg.IconStyle, false)
}

func (p *QueryPanel) schemaIcon() rune {
	return nodeIcon(nodeData{Type: NodeSchema}, p.app.cfg.IconStyle, false)
}

// formatColumnType renders a CatalogColumn's type the way SQL Server itself
// would print it: length for the (n)(var)char/binary family (nvarchar/
// nchar's MaxLength is stored in bytes, so it's halved back to characters),
// precision/scale for decimal/numeric, and scale alone for the fractional-
// seconds types — MAX for a -1 MaxLength either way.
func formatColumnType(col gosmo.CatalogColumn) string {
	t := strings.ToLower(string(col.DataType))
	switch t {
	case "varchar", "char", "varbinary", "binary":
		switch {
		case col.MaxLength == -1:
			t += "(MAX)"
		case col.MaxLength > 0:
			t += fmt.Sprintf("(%d)", col.MaxLength)
		}
	case "nvarchar", "nchar":
		switch {
		case col.MaxLength == -1:
			t += "(MAX)"
		case col.MaxLength > 0:
			t += fmt.Sprintf("(%d)", col.MaxLength/2)
		}
	case "decimal", "numeric":
		if col.Precision > 0 {
			t += fmt.Sprintf("(%d,%d)", col.Precision, col.Scale)
		}
	case "datetime2", "time", "datetimeoffset":
		if col.Scale > 0 {
			t += fmt.Sprintf("(%d)", col.Scale)
		}
	}
	if !col.IsNullable {
		t += ", not null"
	}
	return t
}

var regularIdentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// bracketIfNeeded returns name as-is when it's a plain identifier and not
// one of sqlKeywords, or "[name]" (with any ']' doubled) otherwise — so a
// committed candidate never silently changes what it names by needing
// quoting SQL Server would otherwise require.
func bracketIfNeeded(name string) string {
	if regularIdentPattern.MatchString(name) && !sqlKeywords[strings.ToUpper(name)] {
		return name
	}
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
