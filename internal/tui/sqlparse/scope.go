package sqlparse

import (
	"slices"
	"strings"
)

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

// QualifierChain is TokenContext's qualifier with every part before it: the
// dotted parts ahead of the word being typed (or of the cursor, right after a
// dot), outermost first. "db.dbo.Or|" gives [db dbo], "db..|" gives [db ""]
// (the default schema), "c.|" gives [c], and an unqualified word gives nil.
// TokenContext's qualifier is the chain's last part when that part is an
// identifier.
//
// A chain that starts with an empty part ("x = ..a") names nothing, and gives
// nil too.
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
// cursor is currently in
// ---------------------------------------------------------------------------

// FromRef is one table/view reference parsed out of a FROM/JOIN/INTO/
// UPDATE/DELETE clause, with its optional AS alias.
type FromRef struct {
	Schema, Name, Alias string

	// Database is the first part of a three-part name ("db.schema.t", or
	// "db..t" with Schema empty for the default schema). Server is the first
	// part of a four-part, linked-server name, which nothing resolves: its
	// catalog is another instance's.
	Database, Server string

	// Derived is the query behind "( ... ) [AS] alias", with Schema and Name
	// empty. Only the tree parser below sets it; ParseFromScope never does.
	Derived *Query

	// Pivot is the PIVOT/UNPIVOT clause applied to this reference, reshaping
	// what it puts in scope (see pivot.go). Alias is then the pivoted result's
	// name — the source's own alias is not addressable past the clause, so it
	// is not kept. Only the tree parser sets it.
	Pivot *Pivot

	// Rowset is set when Name is a rowset function — OPENJSON, OPENROWSET,
	// OPENXML — whose columns come from its own WITH clause rather than the
	// catalog (see rowset.go). Only the tree parser sets it.
	Rowset *Rowset

	// Call is set when the name is followed by a parenthesised group: a
	// table-valued function's argument list, or a legacy "t (NOLOCK)" hint.
	// The parser can't tell the two apart; the catalog can, since a table and
	// a function never share a name in one schema. A ref without it is never
	// a function — one can't be named without its argument list. Only the
	// tree parser sets it.
	Call bool
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
			parts, j := multipartName(tokens, i)
			ref := refFromParts(parts)
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
//     intervening VALUES or EXEC is that statement's own main query/source
//     (CTE's SELECT, INSERT ... SELECT), not a new one — only WITH/INSERT
//     itself is the boundary; an INSERT ... VALUES has no such SELECT to
//     suppress, so a later, genuinely separate SELECT stacked right after
//     it with no ';' is (rarely) missed — a known limitation
//   - a WITH directly followed by '(' is a table hint ("t WITH (NOLOCK)") or
//     a rowset function's column list ("OPENJSON(@j) WITH (a int)"), never a
//     CTE, which names itself first; a WITH that is the last token is not
//     decided either way, and is not reported
//
// Combined with the ';'/GO boundaries PrefixCache and
// NarrowStatementForward already apply, this narrows FROM-scope/clause
// analysis to the actual statement under the cursor even when the editor
// holds several statements back to back with no ';' between them.
func DMLStatementStarts(tokens []Token) []int {
	var starts []int
	var s dmlSplitter
	for _, t := range tokens {
		if st, ok := s.feed(t); ok {
			starts = append(starts, st.Start)
		}
	}
	return starts
}

// dmlSplitter is DMLStatementStarts' state machine, fed one token at a time so
// a forward scan can stop at the first statement start instead of tokenizing
// everything after the cursor (see NarrowStatementForward).
type dmlSplitter struct {
	depth                   int
	prevKeyword, prevPrevKw string
	pendingMainSelect       bool

	// with is a top-level WITH whose role the token after it decides: a '('
	// makes it a hint or a WITH column list, anything else a CTE clause.
	with        Token
	withPending bool
}

// feed advances the splitter past t and reports the token that begins a new
// statement, if one does. That is usually t itself, but a WITH is reported
// one token late, when the token after it shows what it is. A token that would
// itself start a statement right after a WITH reported that way — no valid
// script has one — continues the WITH's statement.
func (s *dmlSplitter) feed(t Token) (Token, bool) {
	if s.withPending {
		s.withPending = false
		if t.Kind != TokenParenOpen {
			s.pendingMainSelect = true
			s.advance(t)
			return s.with, true
		}
	}
	if s.advance(t) {
		return t, true
	}
	return Token{}, false
}

// advance is feed for every token but the one after a pending WITH's verdict.
func (s *dmlSplitter) advance(t Token) bool {
	switch t.Kind {
	case TokenParenOpen:
		s.depth++
		return false
	case TokenParenClose:
		if s.depth > 0 {
			s.depth--
		}
		return false
	}
	if s.depth != 0 || t.Kind != TokenKeyword {
		return false
	}
	starts := false
	switch {
	case t.Text == "VALUES", t.Text == "EXEC", t.Text == "EXECUTE":
		// INSERT ... VALUES and INSERT ... EXEC have no main SELECT to
		// wait for, so the next top-level SELECT is a statement of its own.
		s.pendingMainSelect = false
	case t.Text == "WITH":
		s.with, s.withPending = t, true
	case dmlStatementLeaders[t.Text]:
		continuesUnion := s.prevKeyword == "UNION" || s.prevKeyword == "EXCEPT" || s.prevKeyword == "INTERSECT" ||
			(s.prevKeyword == "ALL" && s.prevPrevKw == "UNION")
		switch {
		case t.Text == "SELECT" && s.pendingMainSelect:
			s.pendingMainSelect = false
		case t.Text == "SELECT" && continuesUnion:
			// UNION-chain continuation of the same statement.
		default:
			starts = true
			s.pendingMainSelect = t.Text == "INSERT"
		}
	}
	s.prevPrevKw = s.prevKeyword
	s.prevKeyword = t.Text
	return starts
}

// forwardStatementEnders are top-level keywords that cannot occur inside a DML
// statement, so one after the cursor ends the cursor's statement even though
// DMLStatementStarts does not split on it. EXEC is not one: INSERT ... EXEC
// continues the INSERT.
var forwardStatementEnders = map[string]bool{
	"DECLARE": true, "CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true,
}

// NarrowToDMLStatement tightens [batchStart, batchEnd) — the ';'/GO-
// delimited boundaries ScanPrefix/statementEndOffset already
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

// NarrowStatementForward does what a ';'/GO end scan, TokenizeRange and
// NarrowToDMLStatement did in three passes (the reference composition in
// statement_forward_test.go), in one bounded pass. prefix holds the tokens of
// [batchStart, upTo) as ScanPrefix returns them; the forward half is lexed from
// from (upTo, or just past a bracket identifier the cursor sits in) and stops at
// the first top-level ';', the next "GO" line below cursorRow, or the first
// keyword after upTo that starts a new statement — whichever comes first. It
// returns the cursor's statement bounds and the forward tokens in [from, end).
//
// Stopping at the next statement is the point: in a script that ends no
// statement with ';', the old ';'/GO scan lexed everything below the cursor —
// 200–250 ms and 53 MB per keystroke on a 20 k-line script (B11). The leader
// test is DMLStatementStarts' own, seeded with the prefix, so a SELECT that
// continues an INSERT, a WITH or a UNION before the cursor still continues it.
func NarrowStatementForward(lines [][]rune, buf []rune, cursorRow, batchStart, from, upTo int, prefix []Token) (start, end int, tail []Token) {
	start, end = batchStart, len(buf)
	var s dmlSplitter
	for _, t := range prefix {
		if st, ok := s.feed(t); ok && st.Start <= upTo && st.Start > start {
			start = st.Start
		}
	}
	tail = make([]Token, 0, 64)
	seen := 0
	found := false
	// scan feeds the tokens lexed since the last call, stopping at the first
	// one that ends the cursor's statement.
	scan := func() {
		for ; seen < len(tail) && !found; seen++ {
			t := tail[seen]
			st, leader := s.feed(t)
			switch {
			case leader && st.Start <= upTo:
				if st.Start > start {
					start = st.Start
				}
			case leader:
				end, found = st.Start, true
			case t.Start > upTo && s.depth == 0 && t.Kind == TokenKeyword && forwardStatementEnders[t.Text]:
				end, found = t.Start, true
			}
		}
	}
	r := lexSQL(buf, from, len(buf), true, LexNormal, &tail,
		goScan{lo: OffsetForCursor(lines, cursorRow+1, 0), hi: len(buf)}, nil,
		func(_, goNext int) bool {
			scan()
			return found || goNext >= 0
		})
	scan()
	if !found {
		end = r.boundary
		if r.firstGo >= 0 && r.firstGo < end {
			end = r.firstGo
		}
	}
	for i, t := range tail {
		if t.Start >= end {
			tail = tail[:i]
			break
		}
	}
	return start, end, tail
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

// ---------------------------------------------------------------------------
// Query tree: CTEs, derived tables and subqueries
//
// Everything above this line reads the token stream flat, skipping whatever
// sits inside parentheses. The parser below does not skip: it builds a shallow
// tree of queries so a CTE body, a derived table and the cursor's own innermost
// SELECT each have a shape of their own. It stays a lexical approximation, not
// a T-SQL parser — anything it does not recognise becomes an unnamed item or a
// skipped span, never a guess.
// ---------------------------------------------------------------------------

// Query is one SELECT's completion-relevant shape.
type Query struct {
	CTEs   []CTE        // WITH-bound names this query and its children see
	Select []SelectItem // the select list, in order
	From   []FromRef    // FROM/JOIN/APPLY refs at this query's own level

	// Subqueries holds parenthesised sub-SELECTs that bind no name into this
	// query — an EXISTS or IN predicate, a scalar subquery in the select list —
	// plus the second and later branches of a UNION/EXCEPT/INTERSECT chain,
	// whose result names T-SQL takes from the first branch. Nothing resolves
	// against them; they are kept only so a cursor inside one still finds its
	// own scope.
	Subqueries []*Query

	Start int // rune offset of the query's first token
	End   int // rune offset just past its last token, or of its closing ')'

	// bounded records that End is a real end — a ')' or a set operator closed
	// the query — rather than "as far as the tokens went". A cursor past an
	// unbounded query's End is still inside it, which is the half-typed
	// statement the completion popup exists for.
	bounded bool

	// tokStart/tokEnd bound this query's own tokens within the stream ScopeAt
	// was handed, for the clause scan.
	tokStart, tokEnd int
}

// SelectItem is one entry of a select list.
type SelectItem struct {
	Star      bool // "*" (StarQual "") or "q.*" (StarQual "q")
	StarQual  string
	Qualifier string // "a" in "a.col"
	Name      string // "col" in "a.col" / "col"; "" for an expression
	Alias     string // AS alias, or a trailing bare identifier
}

// CTE is one WITH binding.
type CTE struct {
	Name    string
	Columns []string // explicit "(a, b, c)" list; nil when absent
	Body    *Query
}

// Scope is what is in scope at one cursor offset.
type Scope struct {
	Query  *Query // innermost query containing the offset; nil if none parsed
	CTEs   []CTE  // visible CTE definitions, innermost first
	Clause Clause // clause state within Query, not within the statement
}

// ScopeAt parses one statement's tokens into a Query tree and returns what is
// in scope at upTo: the innermost query containing it, the CTEs visible from
// there (its own plus every enclosing query's), and the clause state within
// that query.
//
// tokens must span a whole statement — the forward half matters as much as the
// prefix, since a CTE body typed below the cursor still defines the name the
// cursor is completing against.
func ScopeAt(tokens []Token, upTo int) Scope {
	p := &queryParser{toks: tokens}
	root := p.parseChain()
	if root == nil {
		return Scope{}
	}
	f := scopeFinder{upTo: upTo}
	f.visit(root, nil, 0)
	if f.best == nil {
		return Scope{}
	}
	return Scope{Query: f.best, CTEs: f.ctes, Clause: clauseWithin(tokens, f.best, upTo)}
}

// scopeFinder walks the tree for the deepest query containing upTo, carrying
// the CTEs each level adds down with it.
type scopeFinder struct {
	upTo  int
	best  *Query
	depth int
	ctes  []CTE
}

func (f *scopeFinder) visit(q *Query, enclosing []CTE, depth int) {
	// Innermost first, so a name rebound by an inner WITH wins.
	visible := enclosing
	if len(q.CTEs) > 0 {
		visible = append(append([]CTE{}, q.CTEs...), enclosing...)
	}
	// >= rather than >: two queries at one depth can both contain upTo only
	// when the earlier is unbounded, and then the later one is where the
	// cursor really is.
	if q.contains(f.upTo) && (f.best == nil || depth >= f.depth) {
		f.best, f.depth, f.ctes = q, depth, visible
	}
	for i := range q.CTEs {
		if q.CTEs[i].Body != nil {
			f.visit(q.CTEs[i].Body, visible, depth+1)
		}
	}
	for _, r := range q.From {
		if r.Derived != nil {
			f.visit(r.Derived, visible, depth+1)
		}
	}
	for _, s := range q.Subqueries {
		f.visit(s, visible, depth+1)
	}
}

func (q *Query) contains(off int) bool {
	if off < q.Start {
		return false
	}
	return !q.bounded || off <= q.End
}

// clauseWithin runs CurrentClause over q's own tokens up to the cursor. q's
// tokens start at its own paren depth 0, so CurrentClause's depth skipping
// lands on this query's clauses rather than an enclosing statement's.
func clauseWithin(tokens []Token, q *Query, upTo int) Clause {
	lo, hi := q.tokStart, q.tokEnd
	for hi > lo && tokens[hi-1].Start >= upTo {
		hi--
	}
	return CurrentClause(tokens[lo:hi])
}

// ---------------------------------------------------------------------------
// The parser
// ---------------------------------------------------------------------------

type queryParser struct {
	toks []Token
	i    int
}

func (p *queryParser) cur() (Token, bool) {
	if p.i < len(p.toks) {
		return p.toks[p.i], true
	}
	return Token{}, false
}

func (p *queryParser) at(k TokenKind) bool {
	t, ok := p.cur()
	return ok && t.Kind == k
}

func (p *queryParser) atKeyword(text string) bool {
	t, ok := p.cur()
	return ok && t.Kind == TokenKeyword && t.Text == text
}

// setOperators end a query branch: what follows is a sibling SELECT, not a
// continuation of this one.
var setOperators = map[string]bool{"UNION": true, "EXCEPT": true, "INTERSECT": true}

// refIntroducers are the keywords a table reference can follow. APPLY covers
// both CROSS and OUTER APPLY, whose leading keyword is skipped as noise.
var refIntroducers = map[string]bool{
	"FROM": true, "JOIN": true, "INTO": true, "UPDATE": true, "DELETE": true, "APPLY": true,
}

// selectListEnders stop a select list. FROM is the usual one; the rest catch a
// select list with no FROM at all.
var selectListEnders = map[string]bool{
	"FROM": true, "INTO": true, "WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true,
}

// parseChain parses one query and the UNION/EXCEPT/INTERSECT branches chained
// onto it, returning the first — the branch whose column names are the chain's
// — with the rest hung off its Subqueries.
func (p *queryParser) parseChain() *Query {
	head := p.parseBranch()
	if head == nil {
		return nil
	}
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		if t.Kind != TokenKeyword || !setOperators[t.Text] {
			break
		}
		p.i++
		if p.atKeyword("ALL") {
			p.i++
		}
		if next := p.parseBranch(); next != nil {
			head.Subqueries = append(head.Subqueries, next)
		}
	}
	return head
}

// parseBranch parses one query up to its closing ')', a set operator, or the
// end of the tokens, leaving that terminator unconsumed.
func (p *queryParser) parseBranch() *Query {
	if p.i >= len(p.toks) {
		return nil
	}
	q := &Query{tokStart: p.i, Start: p.toks[p.i].Start}
	if p.atKeyword("WITH") {
		p.parseCTEs(q)
	}
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		if t.Kind == TokenParenClose || (t.Kind == TokenKeyword && setOperators[t.Text]) {
			break
		}
		switch {
		case t.Kind == TokenKeyword && t.Text == "SELECT":
			p.i++
			q.Select = p.parseSelectList(q)
		case t.Kind == TokenKeyword && refIntroducers[t.Text]:
			p.i++
			p.parseFromRefs(q)
		case t.Kind == TokenParenOpen:
			p.parseParenGroup(q)
		default:
			p.i++
		}
	}
	if p.i == q.tokStart {
		return nil // nothing here — an empty "()" or a stray terminator
	}
	q.tokEnd = p.i
	if t, ok := p.cur(); ok {
		q.End, q.bounded = t.Start, true
	} else {
		q.End = tokenEnd(p.toks[p.i-1])
	}
	return q
}

// parseCTEs parses "WITH name [(cols)] AS ( query ) {, ...}". Anything else
// following WITH — a table hint, EXECUTE ... WITH RESULT SETS — is not a CTE
// clause, so the whole parse is abandoned unless the first binding matches,
// and only the WITH itself is consumed.
//
// Bindings that did parse are kept even when a later one does not. The tail of
// a half-typed clause — everything from the comma the user just typed onwards —
// stops matching for the keystrokes before the next binding's name arrives, and
// dropping the earlier bindings there would empty the completion popup on the
// exact script the package exists for.
func (p *queryParser) parseCTEs(q *Query) {
	p.i++ // WITH
	// resume is where the branch loop picks up if a binding fails to parse:
	// just past the last complete one, so the abandoned tail is walked as
	// ordinary tokens exactly once and the bodies already stored in ctes are
	// not re-walked as subqueries.
	resume := p.i
	var ctes []CTE
	for {
		t, ok := p.cur()
		if !ok || t.Kind != TokenIdent {
			break
		}
		cte := CTE{Name: t.Text}
		p.i++
		if p.at(TokenParenOpen) {
			cols, ok := p.parseColumnNameList()
			if !ok {
				break
			}
			cte.Columns = cols
		}
		if !p.atKeyword("AS") {
			break
		}
		p.i++
		if !p.at(TokenParenOpen) {
			break
		}
		p.i++
		cte.Body = p.parseChain()
		if p.at(TokenParenClose) {
			p.i++
		}
		ctes = append(ctes, cte)
		resume = p.i
		if p.at(TokenComma) {
			p.i++
			continue
		}
		q.CTEs = ctes
		return
	}
	// A binding didn't match: keep the ones that did and let the branch loop
	// walk what follows as ordinary tokens. With none at all this is not a CTE
	// clause, and resume is still just past WITH.
	q.CTEs = ctes
	p.i = resume
}

// parseColumnNameList parses "( a, b, c )" at p.i, reporting false — and
// consuming nothing — for a parenthesised group holding anything else.
func (p *queryParser) parseColumnNameList() ([]string, bool) {
	start := p.i
	p.i++ // '('
	var names []string
	for {
		t, ok := p.cur()
		switch {
		case !ok:
			p.i = start
			return nil, false
		case t.Kind == TokenParenClose:
			p.i++
			return names, len(names) > 0
		case t.Kind == TokenIdent:
			names = append(names, t.Text)
			p.i++
		case t.Kind == TokenComma:
			p.i++
		default:
			p.i = start
			return nil, false
		}
	}
}

// parseSelectList parses from just past SELECT to the list's end, leaving p.i
// on the terminator.
func (p *queryParser) parseSelectList(q *Query) []SelectItem {
	p.skipSelectModifiers()
	var items []SelectItem
	var item []Token
	flush := func() {
		if it, ok := classifySelectItem(item); ok {
			items = append(items, it)
		}
		item = item[:0]
	}
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		switch {
		case t.Kind == TokenParenClose:
			flush()
			return items
		case t.Kind == TokenKeyword && (selectListEnders[t.Text] || setOperators[t.Text]):
			flush()
			return items
		case t.Kind == TokenComma:
			flush()
			p.i++
		case t.Kind == TokenParenOpen:
			// The group's contents are a sub-SELECT's or an expression's, not
			// this item's shape; keeping the brackets alone is enough to make
			// "COUNT(*) c" read as an expression with a trailing alias.
			p.parseParenGroup(q)
			item = append(item, t)
			if p.i > 0 && p.toks[p.i-1].Kind == TokenParenClose {
				item = append(item, p.toks[p.i-1])
			}
		default:
			item = append(item, t)
			p.i++
		}
	}
	flush()
	return items
}

// skipSelectModifiers consumes DISTINCT/ALL/TOP n/TOP (n)/PERCENT/WITH TIES.
// PERCENT and TIES are not in sqlKeywordList — TIES is not even reserved — so
// they are matched as identifiers, and only where TOP has already been seen.
func (p *queryParser) skipSelectModifiers() {
	for {
		switch {
		case p.atKeyword("DISTINCT"), p.atKeyword("ALL"):
			p.i++
		case p.atKeyword("TOP"):
			p.i++
			// The count: "TOP (expr)", or the bare "10"/"@n" the lexer hands
			// back as an identifier (it keeps digits, and keeps the '@').
			// Whatever it is, it is never a select item.
			if p.at(TokenParenOpen) {
				p.skipParenGroup()
			} else if p.at(TokenIdent) && !p.atIdentFold("PERCENT") {
				p.i++
			}
			if p.atIdentFold("PERCENT") {
				p.i++
			}
			if p.atKeyword("WITH") && p.i+1 < len(p.toks) && identFold(p.toks[p.i+1], "TIES") {
				p.i += 2
			}
		default:
			return
		}
	}
}

func (p *queryParser) atIdentFold(upper string) bool {
	t, ok := p.cur()
	return ok && identFold(t, upper)
}

func identFold(t Token, upper string) bool {
	return t.Kind == TokenIdent && strings.EqualFold(t.Text, upper)
}

// classifySelectItem reads one select-list item's tokens. Anything it does not
// recognise is an expression: named only if it carries an alias, dropped
// otherwise.
func classifySelectItem(item []Token) (SelectItem, bool) {
	if len(item) == 0 {
		return SelectItem{}, false
	}
	var it SelectItem
	if n := len(item); n >= 2 && item[n-1].Kind == TokenIdent {
		switch prev := item[n-2]; {
		case prev.Kind == TokenKeyword && prev.Text == "AS":
			it.Alias = item[n-1].Text
			item = item[:n-2]
		case prev.Kind == TokenIdent, prev.Kind == TokenParenClose, prev.Kind == TokenStar:
			it.Alias = item[n-1].Text
			item = item[:n-1]
		}
	}
	switch {
	case len(item) == 1 && item[0].Kind == TokenStar:
		it.Star = true
	case len(item) == 3 && item[0].Kind == TokenIdent && item[1].Kind == TokenDot && item[2].Kind == TokenStar:
		it.Star, it.StarQual = true, item[0].Text
	case len(item) == 1 && item[0].Kind == TokenIdent:
		it.Name = item[0].Text
	case len(item) == 3 && item[0].Kind == TokenIdent && item[1].Kind == TokenDot && item[2].Kind == TokenIdent:
		it.Qualifier, it.Name = item[0].Text, item[2].Text
	default:
		if it.Alias == "" {
			return SelectItem{}, false
		}
	}
	return it, true
}

// parseFromRefs parses the comma-separated reference list introduced by one
// FROM/JOIN/APPLY keyword. A JOIN later in the same clause comes back through
// the branch loop.
func (p *queryParser) parseFromRefs(q *Query) {
	for p.parseRef(q) {
		if !p.at(TokenComma) {
			return
		}
		p.i++
	}
}

// parseRef parses one table reference: "( query ) [AS] alias" into a Derived
// ref, or "[schema.]name [[AS] alias]" into an ordinary one.
func (p *queryParser) parseRef(q *Query) bool {
	t, ok := p.cur()
	if !ok {
		return false
	}
	if t.Kind == TokenParenOpen {
		p.i++
		sub := p.parseChain()
		if p.at(TokenParenClose) {
			p.i++
		}
		if sub == nil {
			return false
		}
		ref := FromRef{Derived: sub}
		p.parseRefTail(&ref)
		q.From = append(q.From, ref)
		return true
	}
	if t.Kind != TokenIdent {
		return false
	}
	parts, next := multipartName(p.toks, p.i)
	ref := refFromParts(parts)
	p.i = next
	if p.at(TokenParenOpen) {
		// A table-valued function's argument list, or a legacy "t (NOLOCK)"
		// hint. Either way the alias, if any, follows the group — and, for a
		// rowset function, its WITH column list.
		p.skipParenGroup()
		ref.Call = true
		ref.Rowset = p.parseRowset(ref)
	}
	p.parseRefTail(&ref)
	q.From = append(q.From, ref)
	return true
}

// multipartName reads the dotted name starting at the identifier tokens[i] —
// "t", "s.t", "db.s.t", "db..t", "srv.db.s.t" — and returns its parts, an
// empty one for each "..", and the index just past it. A trailing dot is left
// unconsumed: "FROM dbo." is a name being typed, and the dot is the
// qualifier the cursor completes after.
func multipartName(tokens []Token, i int) ([]string, int) {
	parts := []string{tokens[i].Text}
	j := i + 1
	for j < len(tokens) && tokens[j].Kind == TokenDot {
		switch {
		case j+1 < len(tokens) && tokens[j+1].Kind == TokenIdent:
			parts = append(parts, tokens[j+1].Text)
			j += 2
		case j+2 < len(tokens) && tokens[j+1].Kind == TokenDot && tokens[j+2].Kind == TokenIdent:
			parts = append(parts, "", tokens[j+2].Text)
			j += 3
		default:
			return parts, j
		}
	}
	return parts, j
}

// refFromParts names a reference from multipartName's parts, last part first.
// More than four parts is no name SQL Server accepts; it is kept as a
// linked-server ref, so it resolves to nothing rather than to a guess.
func refFromParts(parts []string) FromRef {
	n := len(parts)
	ref := FromRef{Name: parts[n-1]}
	if n >= 2 {
		ref.Schema = parts[n-2]
	}
	if n >= 3 {
		ref.Database = parts[n-3]
	}
	if n >= 4 {
		ref.Server = strings.Join(parts[:n-3], ".")
	}
	return ref
}

// parseRefTail consumes what can follow a table reference's name: an optional
// PIVOT/UNPIVOT clause and an alias.
//
// Both orders occur — "t PIVOT (...) AS p" and "(SELECT ...) AS src PIVOT (...)
// AS p" — and the clause is checked first in each, because parseAlias would
// otherwise take the bare word PIVOT for the alias it is not. When a clause is
// found, the alias that follows it is the reference's: a pivoted source's own
// alias is only addressable inside the clause.
func (p *queryParser) parseRefTail(ref *FromRef) {
	ref.Pivot = p.parsePivot()
	ref.Alias = p.parseAlias()
	if ref.Pivot != nil {
		return
	}
	if pv := p.parsePivot(); pv != nil {
		ref.Pivot, ref.Alias = pv, p.parseAlias()
	}
}

// parseAlias consumes an optional "AS name" or bare trailing name.
func (p *queryParser) parseAlias() string {
	if p.atKeyword("AS") {
		p.i++
	}
	if t, ok := p.cur(); ok && t.Kind == TokenIdent {
		p.i++
		return t.Text
	}
	return ""
}

// parseParenGroup walks the group at p.i and leaves p.i past its ')'. A group
// that opens a query is parsed as one and recorded on q.Subqueries — it binds
// no name outward, but the cursor can still be inside it; any other group is
// skipped, its own nested groups included.
func (p *queryParser) parseParenGroup(q *Query) {
	p.i++ // '('
	if p.atKeyword("SELECT") || p.atKeyword("WITH") {
		if sub := p.parseChain(); sub != nil {
			q.Subqueries = append(q.Subqueries, sub)
		}
	} else {
		for p.i < len(p.toks) {
			switch p.toks[p.i].Kind {
			case TokenParenClose:
				p.i++
				return
			case TokenParenOpen:
				p.parseParenGroup(q)
			default:
				p.i++
			}
		}
		return
	}
	if p.at(TokenParenClose) {
		p.i++
	}
}

// skipParenGroup walks the group at p.i without recording anything in it.
func (p *queryParser) skipParenGroup() {
	depth := 0
	for p.i < len(p.toks) {
		switch p.toks[p.i].Kind {
		case TokenParenOpen:
			depth++
		case TokenParenClose:
			depth--
		}
		p.i++
		if depth == 0 {
			return
		}
	}
}

// tokenEnd is the offset just past t. A bracketed or double-quoted identifier
// reports two runes short, since Text drops the delimiters — it only ever
// shifts an unbounded query's End, which nothing compares against.
func tokenEnd(t Token) int {
	switch t.Kind {
	case TokenIdent, TokenKeyword:
		return t.Start + len([]rune(t.Text))
	default:
		return t.Start + 1
	}
}

// FromRefs is q's own FROM/JOIN/APPLY refs, and nil for a nil q — so a caller
// holding a Scope whose Query never parsed can read it without a nil check.
func (q *Query) FromRefs() []FromRef {
	if q == nil {
		return nil
	}
	return q.From
}
