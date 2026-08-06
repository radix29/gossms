package sqlparse

// ---------------------------------------------------------------------------
// Cursor context: what's being typed, and whether it's already dot-qualified
// ---------------------------------------------------------------------------

// TokenContext inspects the tail of tokens (already scanned up to
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
func TokenContext(tokens []Token, upTo int) (qualifier, prefix string, replaceFrom int, hasQualifier bool) {
	n := len(tokens)
	if n == 0 {
		return "", "", upTo, false
	}
	last := tokens[n-1]
	lastIsWord := last.Kind == TokenIdent || last.Kind == TokenKeyword
	switch {
	case lastIsWord && last.Start+len([]rune(last.Text)) == upTo:
		prefix = last.Text
		replaceFrom = last.Start
		if n >= 3 && tokens[n-2].Kind == TokenDot && tokens[n-3].Kind == TokenIdent {
			qualifier = tokens[n-3].Text
			hasQualifier = true
		}
	case last.Kind == TokenDot && last.Start+1 == upTo:
		replaceFrom = upTo
		if n >= 2 && tokens[n-2].Kind == TokenIdent {
			qualifier = tokens[n-2].Text
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

// FromRef is one table/view reference parsed out of a FROM/JOIN/INTO/
// UPDATE/DELETE clause, with its optional AS alias.
type FromRef struct {
	Schema, Name, Alias string
}

// ParseFromScope walks tokens looking for table references introduced by
// FROM, JOIN, INTO, UPDATE, or DELETE, each optionally schema-qualified and
// optionally aliased (bare "AS alias" or just a trailing identifier).
// Subquery contents (inside parentheses) are skipped rather than
// mis-parsed — a documented limitation, see the package doc comment.
func ParseFromScope(tokens []Token) []FromRef {
	var refs []FromRef
	depth := 0
	expectRef := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch t.Kind {
		case TokenParenOpen:
			depth++
			continue
		case TokenParenClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if t.Kind == TokenKeyword {
			switch t.Text {
			case "FROM", "JOIN", "INTO", "UPDATE", "DELETE":
				expectRef = true
			case "WHERE", "ON", "GROUP", "ORDER", "HAVING", "SET", "VALUES", "AND", "OR",
				"UNION", "EXCEPT", "INTERSECT":
				expectRef = false
			}
			continue
		}
		if expectRef && t.Kind == TokenIdent {
			ref := FromRef{Name: t.Text}
			j := i + 1
			if j+1 < len(tokens) && tokens[j].Kind == TokenDot && tokens[j+1].Kind == TokenIdent {
				ref.Schema = t.Text
				ref.Name = tokens[j+1].Text
				j += 2
			}
			if j < len(tokens) && tokens[j].Kind == TokenKeyword && tokens[j].Text == "AS" {
				j++
			}
			if j < len(tokens) && tokens[j].Kind == TokenIdent {
				ref.Alias = tokens[j].Text
				j++
			}
			refs = append(refs, ref)
			i = j - 1
		}
	}
	return refs
}

// dmlStatementLeaders are the T-SQL keywords that can only ever begin a
// new statement — used by DMLStatementStarts to split multiple statements
// stacked in the editor with no ';' between them.
var dmlStatementLeaders = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "WITH": true,
}

// DMLStatementStarts scans tokens — already correctly depth-tracked from
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
//     it with no ';' is (rarely) missed — a known limitation
//
// Combined with the ';'/GO boundaries ScanPrefix/
// StatementEndOffset already apply, this narrows FROM-scope/clause
// analysis to the actual statement under the cursor even when the editor
// holds several statements back to back with no ';' between them.
func DMLStatementStarts(tokens []Token) []int {
	var starts []int
	depth := 0
	prevKeyword, prevPrevKeyword := "", ""
	pendingMainSelect := false
	for _, t := range tokens {
		switch t.Kind {
		case TokenParenOpen:
			depth++
			continue
		case TokenParenClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || t.Kind != TokenKeyword {
			continue
		}
		switch {
		case t.Text == "VALUES":
			pendingMainSelect = false
		case dmlStatementLeaders[t.Text]:
			continuesUnion := prevKeyword == "UNION" || prevKeyword == "EXCEPT" || prevKeyword == "INTERSECT" ||
				(prevKeyword == "ALL" && prevPrevKeyword == "UNION")
			switch {
			case t.Text == "SELECT" && pendingMainSelect:
				pendingMainSelect = false
			case t.Text == "SELECT" && continuesUnion:
				// UNION-chain continuation of the same statement.
			default:
				starts = append(starts, t.Start)
				pendingMainSelect = t.Text == "WITH" || t.Text == "INSERT"
			}
		}
		prevPrevKeyword = prevKeyword
		prevKeyword = t.Text
	}
	return starts
}

// NarrowToDMLStatement tightens [batchStart, batchEnd) — the ';'/GO-
// delimited boundaries ScanPrefix/StatementEndOffset already
// computed — to the actual DML statement containing upTo, using
// DMLStatementStarts on tokens (which must already span the same
// [batchStart, batchEnd) range so its depth tracking starts at 0).
func NarrowToDMLStatement(tokens []Token, batchStart, batchEnd, upTo int) (start, end int) {
	start, end = batchStart, batchEnd
	for _, off := range DMLStatementStarts(tokens) {
		switch {
		case off <= upTo && off > start:
			start = off
		case off > upTo && off < end:
			return start, off // ascending order: first hit is the closest
		}
	}
	return start, end
}

// Clause is the coarse "what kind of name is expected here" state
// CurrentClause tracks — the last clause-introducing keyword before the
// cursor wins, ignoring subquery contents (paren depth > 0).
type Clause int

const (
	ClauseUnknown Clause = iota
	ClauseTable
	ClauseColumn
)

func CurrentClause(tokens []Token) Clause {
	clause := ClauseUnknown
	depth := 0
	for _, t := range tokens {
		switch t.Kind {
		case TokenParenOpen:
			depth++
			continue
		case TokenParenClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || t.Kind != TokenKeyword {
			continue
		}
		switch t.Text {
		case "SELECT", "WHERE", "ON", "HAVING", "SET", "AND", "OR", "BY":
			clause = ClauseColumn
		case "FROM", "JOIN", "INTO", "UPDATE", "DELETE", "TABLE":
			clause = ClauseTable
		}
	}
	return clause
}
