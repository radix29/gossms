package sqlparse

import "strings"

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

	// Derived is the query behind "( ... ) [AS] alias", with Schema and Name
	// empty. Only the tree parser below sets it; ParseFromScope never does.
	Derived *Query
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
func (p *queryParser) parseCTEs(q *Query) {
	start := p.i
	p.i++ // WITH
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
		if p.at(TokenComma) {
			p.i++
			continue
		}
		q.CTEs = ctes
		return
	}
	// Not a CTE clause after all: rewind to just past WITH and let the branch
	// loop walk what follows as ordinary tokens.
	p.i = start + 1
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
			// back as an identifier (it keeps digits, and drops the '@').
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
		q.From = append(q.From, FromRef{Derived: sub, Alias: p.parseAlias()})
		return true
	}
	if t.Kind != TokenIdent {
		return false
	}
	ref := FromRef{Name: t.Text}
	p.i++
	if p.at(TokenDot) && p.i+1 < len(p.toks) && p.toks[p.i+1].Kind == TokenIdent {
		ref.Schema, ref.Name = t.Text, p.toks[p.i+1].Text
		p.i += 2
	}
	if p.at(TokenParenOpen) {
		// A table-valued function's argument list, or a legacy "t (NOLOCK)"
		// hint. Either way the alias, if any, follows the group.
		p.skipParenGroup()
	}
	ref.Alias = p.parseAlias()
	q.From = append(q.From, ref)
	return true
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
