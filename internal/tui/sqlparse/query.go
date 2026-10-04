package sqlparse

import "strings"

// ---------------------------------------------------------------------------
// Query tree: CTEs, derived tables and subqueries
//
// The scans in scope.go and statement.go read the token stream flat, skipping
// whatever sits inside parentheses. The parser below builds a shallow tree of
// queries so a CTE body, a derived table and the cursor's own innermost SELECT
// each have a shape of their own. It is a lexical approximation, not a T-SQL
// parser: anything unrecognised becomes an unnamed item or a skipped span,
// never a guess.
// ---------------------------------------------------------------------------

// Query is one SELECT's completion-relevant shape.
type Query struct {
	CTEs   []CTE        // WITH-bound names this query and its children see
	Select []SelectItem // the select list, in order
	From   []FromRef    // FROM/JOIN/APPLY refs at this query's own level

	// Subqueries holds parenthesised sub-SELECTs that bind no name into this query
	// (an EXISTS or IN predicate, a scalar subquery in the select list) plus the
	// second and later branches of a UNION/EXCEPT/INTERSECT chain, whose result
	// names T-SQL takes from the first branch. Nothing resolves against them; they
	// are kept so a cursor inside one still finds its own scope.
	Subqueries []*Query

	Start int // rune offset of the query's first token
	End   int // rune offset just past its last token, or of its closing ')'

	// bounded records that End is a real end (a ')' or a set operator closed the
	// query) rather than "as far as the tokens went". A cursor past an unbounded
	// query's End is still inside it: the half-typed statement completion exists
	// for.
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

// ScopeAt parses one statement's tokens into a Query tree and returns what is in
// scope at upTo: the innermost query containing it, the CTEs visible from there
// (its own plus every enclosing query's), and the clause state within that
// query.
//
// tokens must span a whole statement: the forward half matters as much as the
// prefix, since a CTE body typed below the cursor still defines the name the
// cursor completes against.
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
	// >= rather than >: two queries at one depth can both contain upTo only when
	// the earlier is unbounded, and then the later one is where the cursor is.
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
// lands on this query's clauses, not an enclosing statement's.
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
// onto it, returning the first (whose column names are the chain's) with the
// rest hung off its Subqueries.
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
// following WITH (a table hint, EXECUTE ... WITH RESULT SETS) is not a CTE
// clause, so the parse is abandoned unless the first binding matches, and only
// the WITH itself is consumed.
//
// Bindings that did parse are kept even when a later one does not. The tail of
// a half-typed clause (from the comma just typed onwards) stops matching until
// the next binding's name arrives, and dropping earlier bindings would empty
// the completion popup on exactly the script this package exists for.
func (p *queryParser) parseCTEs(q *Query) {
	p.i++ // WITH
	// resume is where the branch loop picks up if a binding fails to parse: just
	// past the last complete one, so the abandoned tail is walked as ordinary
	// tokens exactly once and bodies already stored in ctes aren't re-walked as
	// subqueries.
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
	// A binding didn't match: keep the ones that did and let the branch loop walk
	// what follows as ordinary tokens. With none at all this is not a CTE clause,
	// and resume is still just past WITH.
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
			// The group's contents are a sub-SELECT's or an expression's, not this item's
			// shape; keeping the brackets alone makes "COUNT(*) c" read as an expression
			// with a trailing alias.
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
// PERCENT and TIES are not in sqlKeywordList (TIES is not even reserved), so
// they are matched as identifiers, and only where TOP has already been seen.
func (p *queryParser) skipSelectModifiers() {
	for {
		switch {
		case p.atKeyword("DISTINCT"), p.atKeyword("ALL"):
			p.i++
		case p.atKeyword("TOP"):
			p.i++
			// The count: "TOP (expr)", or the bare "10"/"@n" the lexer hands back as an
			// identifier (it keeps digits and '@'). Never a select item.
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
		// A table-valued function's argument list, or a legacy "t (NOLOCK)" hint.
		// Either way the alias, if any, follows the group (and, for a rowset function,
		// its WITH column list).
		p.skipParenGroup()
		ref.Call = true
		ref.Rowset = p.parseRowset(ref)
	}
	p.parseRefTail(&ref)
	q.From = append(q.From, ref)
	return true
}

// multipartName reads the dotted name starting at the identifier tokens[i]
// ("t", "s.t", "db.s.t", "db..t", "srv.db.s.t") and returns its parts (an empty
// one for each ".."), and the index just past it. A trailing dot is left
// unconsumed: "FROM dbo." is a name being typed, and the dot is the qualifier
// the cursor completes after.
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
// linked-server ref so it resolves to nothing rather than a guess.
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
// Both orders occur ("t PIVOT (...) AS p" and "(SELECT ...) AS src PIVOT (...)
// AS p"); the clause is checked first in each, because parseAlias would take
// the bare word PIVOT for the alias. When a clause is found, the alias after it
// is the reference's: a pivoted source's own alias is addressable only inside
// the clause.
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
// that opens a query is parsed as one and recorded on q.Subqueries (it binds no
// name outward, but the cursor can be inside it); any other group is skipped,
// nested groups included.
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
// reports two runes short, since Text drops the delimiters; this only shifts an
// unbounded query's End, which nothing compares against.
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
