package tui

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
