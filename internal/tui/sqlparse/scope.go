package sqlparse

import "slices"

// ---------------------------------------------------------------------------
// Cursor context: what's being typed, and whether it's already dot-qualified
// ---------------------------------------------------------------------------

// TokenContext inspects the tail of tokens (already scanned up to upTo) and
// reports:
//   - prefix: the identifier characters immediately touching the cursor
//     ("" if the cursor sits after whitespace/punctuation instead)
//   - replaceFrom: where that prefix starts (== upTo when there's no prefix)
//   - qualifier, hasQualifier: the identifier immediately before a '.' that
//     itself immediately precedes prefix/the cursor, if any
//
// A keyword token touching the cursor counts as a prefix too: the word being
// typed may only collide with a keyword by accident ("OR" on the way to
// Orders, "sys.all" on the way to sys.all_objects), and anything else would
// make a commit append instead of replace. Keyword tokens carry uppercased
// text, fine since prefix matching is case-insensitive downstream.
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

// QualifierChain is TokenContext's qualifier with every part before it: the
// dotted parts ahead of the word being typed (or of the cursor, right after a
// dot), outermost first. "db.dbo.Or|" gives [db dbo], "db..|" gives [db ""]
// (the default schema), "c.|" gives [c], and an unqualified word gives nil.
// TokenContext's qualifier is the chain's last part when that part is an
// identifier. A chain starting with an empty part ("x = ..a") names nothing and
// gives nil too.
func QualifierChain(tokens []Token, upTo int) []string {
	n := len(tokens)
	if n == 0 {
		return nil
	}
	i := n - 1
	if last := tokens[i]; (last.Kind == TokenIdent || last.Kind == TokenKeyword) && last.Start+len([]rune(last.Text)) == upTo {
		i--
	}
	var parts []string
	for i >= 1 && tokens[i].Kind == TokenDot {
		switch tokens[i-1].Kind {
		case TokenIdent:
			parts = append(parts, tokens[i-1].Text)
			i -= 2
		case TokenDot:
			parts = append(parts, "")
			i--
		default:
			return nil
		}
	}
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return nil
	}
	slices.Reverse(parts)
	return parts
}

// ---------------------------------------------------------------------------
// FROM-scope: which tables/views/aliases are in play for the statement the
// cursor is in
// ---------------------------------------------------------------------------

// FromRef is one table/view reference parsed out of a FROM/JOIN/INTO/
// UPDATE/DELETE clause, with its optional AS alias.
type FromRef struct {
	Schema, Name, Alias string

	// Database is the first part of a three-part name ("db.schema.t", or "db..t"
	// with Schema empty for the default schema). Server is the first part of a
	// four-part, linked-server name, which nothing resolves: its catalog is another
	// instance's.
	Database, Server string

	// Derived is the query behind "( ... ) [AS] alias", with Schema and Name
	// empty. Only the tree parser below sets it; ParseFromScope never does.
	Derived *Query

	// Pivot is the PIVOT/UNPIVOT clause applied to this reference, reshaping what
	// it puts in scope (see pivot.go). Alias is then the pivoted result's name; the
	// source's own alias isn't addressable past the clause and is not kept. Only
	// the tree parser sets it.
	Pivot *Pivot

	// Rowset is set when Name is a rowset function — OPENJSON, OPENROWSET,
	// OPENXML — whose columns come from its own WITH clause rather than the
	// catalog (see rowset.go). Only the tree parser sets it.
	Rowset *Rowset

	// Call is set when the name is followed by a parenthesised group: a
	// table-valued function's argument list, or a legacy "t (NOLOCK)" hint. The
	// parser can't tell the two apart; the catalog can, since a table and a
	// function never share a name in one schema. A ref without it is never a
	// function (one can't be named without its argument list). Only the tree
	// parser sets it.
	Call bool
}

// ParseFromScope walks tokens looking for table references introduced by FROM,
// JOIN, INTO, UPDATE, or DELETE, each optionally schema-qualified and
// optionally aliased (bare "AS alias" or a trailing identifier). Subquery
// contents (inside parentheses) are skipped rather than mis-parsed, a
// documented limitation (see the package doc comment).
func ParseFromScope(tokens []Token) []FromRef {
	var refs []FromRef
	depth := 0
	expectRef, merge := false, false
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
			case "MERGE":
				expectRef, merge = true, merge || !isJoinHint(tokens, i)
			case "WHERE", "ON", "GROUP", "ORDER", "HAVING", "SET", "VALUES", "AND", "OR",
				"UNION", "EXCEPT", "INTERSECT":
				expectRef = false
			}
			continue
		}
		if atMergeUsing(tokens, i, merge) {
			expectRef = true
			continue
		}
		if expectRef && t.Kind == TokenIdent {
			parts, j := multipartName(tokens, i)
			ref := refFromParts(parts)
			ref.Alias, j = aliasAt(tokens, j)
			refs = append(refs, ref)
			i = j - 1
			// Only a comma carries the list on ("FROM a, b"); any other word after a
			// reference and its alias is a clause ("FOR JSON PATH", "OUTPUT
			// deleted.id"), not another table.
			expectRef = j < len(tokens) && tokens[j].Kind == TokenComma
		}
	}
	return refs
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
	merge := false
	for i, t := range tokens {
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
		if atMergeUsing(tokens, i, merge) {
			clause = ClauseTable
			continue
		}
		if t.Kind != TokenKeyword {
			continue
		}
		switch t.Text {
		case "SELECT", "WHERE", "ON", "HAVING", "SET", "AND", "OR", "BY":
			clause = ClauseColumn
		case "FROM", "JOIN", "INTO", "UPDATE", "DELETE", "TABLE":
			clause = ClauseTable
		case "MERGE":
			clause, merge = ClauseTable, merge || !isJoinHint(tokens, i)
		}
	}
	return clause
}
