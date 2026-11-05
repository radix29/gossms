package format

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ---------------------------------------------------------------------------
// The SQL styles' rules: where each code token goes
// ---------------------------------------------------------------------------

// Where a list's lookahead stops: the clause keywords that end the list a
// clause opened (a statement start, ";" and the enclosing ")" always do).
var (
	selectEnds  = wordSet(`FROM INTO WHERE GROUP ORDER HAVING UNION EXCEPT INTERSECT OPTION FOR WINDOW`)
	fromEnds    = wordSet(`WHERE GROUP ORDER HAVING UNION EXCEPT INTERSECT OPTION FOR WINDOW CROSS INNER LEFT RIGHT FULL OUTER JOIN PIVOT UNPIVOT`)
	byEnds      = wordSet(`HAVING ORDER UNION EXCEPT INTERSECT OPTION FOR OFFSET WINDOW`)
	setEnds     = wordSet(`FROM WHERE OUTPUT OPTION WHEN`)
	outputEnds  = wordSet(`INTO FROM WHERE VALUES SELECT DEFAULT EXEC EXECUTE`)
	valuesEnds  = wordSet(`WHEN OUTPUT OPTION`)
	declareEnds = wordSet(`CURSOR SELECT FOR`)
	paramEnds   = wordSet(`AS WITH FOR RETURNS EXTERNAL`)
	dmlKinds    = wordSet(`INSERT UPDATE DELETE MERGE`)
)

func (l *sqlLayout) code(i int) {
	t := &l.t[i]
	f := l.top()
	if f.kind == frSub && i == f.first {
		l.beginStatement(f, i, f.base, nlLine)
		return
	}
	if l.start[i] && f == l.owner() {
		l.startStmt(i)
		return
	}
	switch {
	case t.isPunct("("):
		l.openParen(i)
	case t.isPunct(")"):
		l.closeParen(i)
	case t.isPunct(","):
		l.comma(i)
	case t.kind == sqltext.KindWord:
		l.word(i)
	default:
		if o := l.owner(); f == o && o.st.kind == "SET" && cmpOp(t.text) {
			o.st.assigned = true
		}
		l.inline(i)
	}
}

// inline writes i on the current line — unless its statement keeps the
// source's line breaks and the source broke the line before it.
func (l *sqlLayout) inline(i int) {
	if o := l.owner(); !o.st.rules && l.t[i].nl > 0 {
		l.put(i, nlLine, o.base+l.w)
		return
	}
	l.put(i, nlNone, 0)
}

// startStmt places i, which begins a statement in the current owner frame,
// and moves through the control-flow frames: into a pending body or block,
// out of finished bodies, and for ELSE and END out to what they close.
func (l *sqlLayout) startStmt(i int) {
	t := &l.t[i]
	switch {
	case t.isWord("ELSE"):
		l.body, l.block = nil, nil
		ind := l.owner().base
		for l.top().kind == frBody {
			b := l.pop()
			ind = l.owner().base
			if b.elseOK {
				ind = b.ifInd
				break
			}
		}
		l.put(i, nlStmt, ind)
		f := l.top()
		f.st, f.ctx = freshStmt(), ind
		f.st.kind, f.st.rules = "ELSE", true
		l.body = &sqlFrame{kind: frBody, base: ind + l.w, ifInd: ind, ctx: ind + l.w, st: freshStmt()}
		if nx := l.nextC[i]; nx >= 0 && l.t[nx].isWord("IF") {
			l.elseIf = nx
		}
	case t.isWord("END") && l.nextUp(i) != "CONVERSATION":
		l.body = nil
		ind, cind := l.owner().base, l.owner().base
		if l.block != nil { // BEGIN END: the block never opened
			ind, cind, l.block = *l.block, *l.block+l.w, nil
		} else {
			for len(l.frames) > 1 {
				if f := l.pop(); f.kind == frBlock {
					ind, cind = f.open, f.base
					break
				}
				ind, cind = l.owner().base, l.owner().base
			}
		}
		l.putC(i, nlStmt, ind, cind)
		f := l.top()
		f.st, f.ctx = freshStmt(), ind
		f.st.kind, f.st.rules = "END", true
	default:
		switch {
		case l.block != nil:
			ind := *l.block
			l.block = nil
			l.push(&sqlFrame{kind: frBlock, open: ind, base: ind + l.w, ctx: ind + l.w, st: freshStmt()})
		case l.body != nil:
			l.push(l.body)
			l.body = nil
		default:
			for f := l.top(); f.kind == frBody && f.filled; f = l.top() {
				l.pop()
			}
		}
		f := l.top()
		ind, nl := f.base, nlStmt
		if f.kind == frBody && !f.filled {
			f.filled = true
			if i == l.elseIf || l.blockBegin(i) {
				f.base, ind = f.ifInd, f.ifInd
			}
		}
		if i == l.elseIf || (l.semi >= 0 && l.prevC[i] == l.semi) {
			nl = nlNone
		}
		l.elseIf, l.semi = -1, -1
		l.beginStatement(f, i, ind, nl)
	}
}

func (l *sqlLayout) blockBegin(i int) bool {
	return l.t[i].isWord("BEGIN") && !sqlBlockBeginNot[l.nextUp(i)]
}

// beginStatement resets f's statement for the one i begins, writes i, and
// sets up what its leader implies: a select list, a CTE, a module's
// parameters, an IF's body to come.
func (l *sqlLayout) beginStatement(f *sqlFrame, i, ind, nl int) {
	t := &l.t[i]
	f.st, f.ctx = freshStmt(), ind
	kind := t.up
	rules := sqlRulesStatements[kind]
	switch {
	case (kind == "CREATE" || kind == "ALTER") && l.moduleHeader(i):
		kind, rules = "MODULE", true
	case kind == "CREATE" && (l.nextUp(i) == "TABLE" || l.nextUp(i) == "TYPE"):
		kind, rules = "TABLE", true
	case kind == "BEGIN":
		rules = !sqlBlockBeginNot[l.nextUp(i)]
	case kind == "END":
		rules = l.nextUp(i) != "CONVERSATION"
	case t.isPunct(";"):
		l.semi = i
	}
	f.st.kind, f.st.rules = kind, rules
	l.put(i, nl, ind)
	switch kind {
	case "SELECT":
		f.st.clause = "SELECT"
		l.list(f, l.selectItems(i), ind, selectEnds, true)
	case "VALUES":
		f.st.clause = "VALUES"
		l.list(f, l.nextC[i], ind, valuesEnds, false)
	case "DECLARE":
		l.list(f, l.nextC[i], ind, declareEnds, false)
	case "WITH":
		f.st.clause = "CTE"
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		f.st.clause = "HEAD"
	case "MODULE":
		if k := l.params(i); k >= 0 {
			l.list(f, k, ind, paramEnds, false)
		}
	case "IF", "WHILE":
		l.body = &sqlFrame{kind: frBody, base: ind + l.w, ifInd: ind, elseOK: kind == "IF", ctx: ind + l.w, st: freshStmt()}
	case "BEGIN":
		if rules {
			b := ind
			l.block = &b
		}
	}
}

// list starts the list a clause opened, its first item at j, if it is worth
// a line per item: more than one item, or (a select list) a CASE to expand.
func (l *sqlLayout) list(f *sqlFrame, j, base int, ends map[string]bool, cases bool) {
	f.st.list = false
	if j < 0 || !l.expands(j, ends, cases) {
		return
	}
	f.st.list, f.st.listInd = true, base+l.w
	f.st.deferAt, f.st.deferInd = j, base+l.w
	f.ctx = base + l.w
}

// expands looks ahead from j, at its own paren depth, to the end of the list
// it begins: true at a top-level comma (or CASE, when cases).
func (l *sqlLayout) expands(j int, ends map[string]bool, cases bool) bool {
	depth, cs := 0, 0
	for k := j; k >= 0; k = l.nextC[k] {
		t := &l.t[k]
		if t.goSep || (k != j && l.start[k]) {
			return false
		}
		switch {
		case t.isPunct("("):
			depth++
			continue
		case t.isPunct(")"):
			if depth == 0 {
				return false
			}
			depth--
			continue
		}
		if depth > 0 {
			continue
		}
		word := t.kind == sqltext.KindWord && !l.qualified(k)
		switch {
		case t.isPunct(";"):
			return false
		case word && t.up == "CASE":
			if cs == 0 && cases {
				return true
			}
			cs++
		case word && t.up == "END" && cs > 0:
			cs--
		case cs > 0:
		case t.isPunct(","):
			return true
		case word && ends[t.up]:
			return false
		}
	}
	return false
}

// selectItems is the first select-list item after SELECT at i, past ALL,
// DISTINCT and TOP (n) [PERCENT] [WITH TIES].
func (l *sqlLayout) selectItems(i int) int {
	j := l.nextC[i]
	for j >= 0 {
		switch l.t[j].up {
		case "ALL", "DISTINCT":
			j = l.nextC[j]
			continue
		case "TOP":
			j = l.nextC[j]
			if j >= 0 && l.t[j].isPunct("(") && l.match[j] >= 0 {
				j = l.match[j]
			}
			if j >= 0 {
				j = l.nextC[j]
			}
			if l.tok(j).up == "PERCENT" {
				j = l.nextC[j]
			}
			if l.tok(j).up == "WITH" && l.nextUp(j) == "TIES" {
				j = l.nextC[l.nextC[j]]
			}
			continue
		}
		break
	}
	return j
}

// params is a procedure header's first parameter when the parameters are
// not parenthesised, -1 otherwise.
func (l *sqlLayout) params(i int) int {
	for k := l.nextC[i]; k >= 0; k = l.nextC[k] {
		t := &l.t[k]
		switch {
		case t.isPunct("("):
			if l.match[k] < 0 {
				return -1
			}
			k = l.match[k]
		case l.start[k], t.isPunct(";"):
			return -1
		case t.kind == sqltext.KindWord && paramEnds[t.up]:
			return -1
		case t.kind == sqltext.KindWord && strings.HasPrefix(t.text, "@"):
			return k
		}
	}
	return -1
}

func (l *sqlLayout) clauseBase(o *sqlFrame) int {
	if o.st.act {
		return o.base + l.w
	}
	return o.base
}

func (l *sqlLayout) word(i int) {
	t := &l.t[i]
	f, o := l.top(), l.owner()
	if !l.qualified(i) {
		if l.caseWord(i, f) {
			return
		}
		if f == o && len(f.cases) == 0 {
			if o.st.rules && l.clause(i, o) {
				return
			}
			switch t.up {
			case "BETWEEN":
				o.st.between++
			case "AND", "OR":
				if t.up == "AND" && o.st.between > 0 {
					o.st.between--
					break
				}
				if o.st.pred && o.st.rules {
					ind := l.clauseBase(o) + l.w
					if l.sty.indentJoins && o.st.clause == "JOIN" {
						ind += l.w
					}
					l.put(i, nlLine, ind)
					f.ctx = ind
					return
				}
			}
		}
	}
	l.inline(i)
}

// caseWord lays out CASE, WHEN, ELSE and END of a CASE expression. A CASE
// in a select list, or nested in one that is, puts WHEN, ELSE and END on
// lines of their own; any other stays inline.
func (l *sqlLayout) caseWord(i int, f *sqlFrame) bool {
	n := len(f.cases)
	switch l.t[i].up {
	case "CASE":
		exp := false
		if n > 0 {
			exp = f.cases[n-1].expand
		} else if f == l.owner() && f.st.clause == "SELECT" && f.st.list {
			exp = true
		}
		l.inline(i)
		f.cases = append(f.cases, sqlCase{expand: exp, ind: f.ctx})
		return true
	case "WHEN", "ELSE":
		if n == 0 {
			return false
		}
		if c := f.cases[n-1]; c.expand {
			l.put(i, nlLine, c.ind+l.w)
			f.ctx = c.ind + l.w
		} else {
			l.inline(i)
		}
		return true
	case "END":
		if n == 0 {
			return false
		}
		if c := f.cases[n-1]; c.expand {
			l.put(i, nlLine, c.ind)
			f.ctx = c.ind
		} else {
			l.inline(i)
		}
		f.cases = f.cases[:n-1]
		return true
	}
	return false
}

// clause places word i when it begins a clause of o's statement, reporting
// whether it did.
func (l *sqlLayout) clause(i int, o *sqlFrame) bool {
	s := &o.st
	up := l.t[i].up
	base := l.clauseBase(o)
	line := func(clause string) {
		l.put(i, nlLine, base)
		o.ctx = base
		s.clause, s.list, s.pred, s.between = clause, false, false, 0
	}
	switch up {
	case "SELECT":
		line("SELECT")
		if s.kind == "WITH" || s.kind == "INSERT" || s.kind == "DECLARE" {
			s.kind = "SELECT"
		}
		l.list(o, l.selectItems(i), base, selectEnds, true)
		return true
	case "INTO":
		if s.clause == "SELECT" || s.clause == "OUTPUT" {
			line("INTO")
			return true
		}
	case "FROM":
		p := l.prevC[i]
		if l.prevUp(i) == "DELETE" || (l.tok(p).isPunct(")") && l.prevUp(l.match[p]) == "TOP") ||
			l.prevUp(i) == "DISTINCT" {
			return false // DELETE [TOP (n)] FROM, IS [NOT] DISTINCT FROM
		}
		if s.kind == "SELECT" || s.kind == "UPDATE" || s.kind == "DELETE" {
			line("FROM")
			l.list(o, l.nextC[i], base, fromEnds, false)
			l.alone(i, o, base)
			return true
		}
	case "WHERE", "HAVING":
		line(up)
		s.pred = true
		l.alone(i, o, base)
		return true
	case "GROUP", "ORDER":
		if l.nextUp(i) == "BY" {
			line(up + " BY")
			l.list(o, l.nextC[l.nextC[i]], base, byEnds, false)
			l.alone(l.nextC[i], o, base)
			return true
		}
	case "UNION", "EXCEPT", "INTERSECT":
		line(up)
		return true
	case "VALUES":
		if l.prevUp(i) != "DEFAULT" {
			line("VALUES")
			l.list(o, l.nextC[i], base, valuesEnds, false)
			return true
		}
	case "SET":
		if s.kind == "UPDATE" || (s.kind == "MERGE" && s.act) {
			if l.prevUp(i) == "UPDATE" {
				l.inline(i) // MERGE ... THEN UPDATE SET
				s.clause = "SET"
			} else {
				line("SET")
			}
			l.list(o, l.nextC[i], base, setEnds, false)
			return true
		}
	case "OUTPUT":
		if dmlKinds[s.kind] && l.clauseWord(i) {
			s.act, base = false, o.base
			line("OUTPUT")
			l.list(o, l.nextC[i], base, outputEnds, false)
			return true
		}
	case "OPTION":
		if l.tok(l.nextC[i]).isPunct("(") {
			s.act, base = false, o.base
			line("OPTION")
			return true
		}
	case "OFFSET":
		if s.clause == "ORDER BY" && l.clauseWord(i) {
			line("OFFSET")
			return true
		}
	case "FOR":
		if nx := l.nextUp(i); nx == "XML" || nx == "JSON" || nx == "BROWSE" {
			line("FOR")
			return true
		}
	case "USING":
		if s.kind == "MERGE" {
			line("USING")
			return true
		}
	case "ON":
		if s.clause == "JOIN" || s.clause == "USING" {
			l.inline(i)
			s.pred = true
			return true
		}
	case "WHEN":
		if s.kind == "MERGE" {
			s.act, base = false, o.base
			line("WHEN")
			return true
		}
	case "THEN":
		if s.kind == "MERGE" && s.clause == "WHEN" {
			l.inline(i)
			s.act, s.clause = true, "ACTION"
			s.deferAt, s.deferInd = l.nextC[i], o.base+l.w
			o.ctx = o.base + l.w
			return true
		}
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		switch {
		case s.kind == "WITH":
			line("HEAD")
			s.kind = up
			return true
		case s.kind == "MERGE" && s.clause == "ACTION":
			l.inline(i)
			s.clause = "HEAD"
			return true
		}
	case "EXEC", "EXECUTE":
		if s.kind == "INSERT" {
			line("EXEC")
			s.rules = false // INSERT ... EXEC: the call keeps its own breaks
			return true
		}
	case "RETURNS", "WITH":
		if s.kind == "MODULE" {
			line(up)
			return true
		}
	case "AS":
		if s.kind == "MODULE" && l.prevUp(i) != "EXECUTE" && l.prevUp(i) != "EXEC" {
			line("AS")
			return true
		}
	}
	if (s.clause == "FROM" || s.clause == "JOIN") && l.joinAt(i) {
		if l.sty.indentJoins {
			base += l.w
		}
		line("JOIN")
		return true
	}
	return false
}

// alone, under sty.clauseAlone, leaves the clause keyword ending at i (FROM,
// WHERE, HAVING, GROUP BY's and ORDER BY's BY) alone on its line: the token
// after it starts the next one, a level under base.
// A clause with nothing after it in its statement (a fragment) is left be.
func (l *sqlLayout) alone(i int, o *sqlFrame, base int) {
	n := l.nextC[i]
	if !l.sty.clauseAlone || n < 0 || l.start[n] || l.t[n].goSep || l.t[n].isPunct(";") || l.t[n].isPunct(")") {
		return
	}
	o.st.deferAt, o.st.deferInd = n, base+l.w
	o.ctx = base + l.w
}

// clauseWord reports whether word i, spelled like a clause keyword that is
// not reserved (OUTPUT, OFFSET), can be one where it stands: no operand is
// due before it and its neighbor after is no operator or list punctuation.
// SET output = 1, WHERE output = 1 and ORDER BY level, offset DESC name a
// column; OUTPUT inserted.id and OFFSET 5 ROWS begin a clause.
func (l *sqlLayout) clauseWord(i int) bool {
	return !l.operandDue(i) && !l.valueNext(i)
}

// operandDue reports whether the code token before word i leaves an operand
// to come: ",", "(", an operator, or a word an expression or assignment
// follows.
func (l *sqlLayout) operandDue(i int) bool {
	p := l.tok(l.prevC[i])
	if p.kind == sqltext.KindPunct {
		return p.text == "," || p.text == "(" || cmpOp(p.text) || arithOp(p.text)
	}
	switch p.up {
	case "SET", "SELECT", "WHERE", "AND", "OR", "NOT", "ON", "BY", "WHEN", "THEN", "ELSE":
		return true
	}
	return false
}

// valueNext reports whether the code token after word i is one no keyword
// comes before — an operator, ",", ")", ";", "." or the end of the
// statement — so i is a name or a value whatever its spelling.
func (l *sqlLayout) valueNext(i int) bool {
	n := l.nextC[i]
	if n < 0 || l.start[n] {
		return true
	}
	t := &l.t[n]
	if t.kind != sqltext.KindPunct {
		return false
	}
	switch t.text {
	case ",", ")", ";", ".":
		return true
	}
	return cmpOp(t.text) || arithOp(t.text)
}

// joinHint reports whether word i is a join hint: HASH, LOOP, MERGE or
// REMOTE between a join's type and JOIN. Without the type before it the
// word is an alias (FROM t hash JOIN u).
func (l *sqlLayout) joinHint(i int) bool {
	switch l.prevUp(i) {
	case "INNER", "LEFT", "RIGHT", "FULL", "OUTER":
		return sqlJoinHints[l.t[i].up] && l.nextUp(i) == "JOIN"
	}
	return false
}

// joinAt reports whether word i opens a join phrase: [INNER|LEFT|RIGHT|FULL]
// [OUTER] [hint] JOIN, CROSS JOIN|APPLY, OUTER APPLY.
func (l *sqlLayout) joinAt(i int) bool {
	up, nx := l.t[i].up, l.nextUp(i)
	if p := l.prevC[i]; sqlJoinWords[l.prevUp(i)] || (p >= 0 && l.joinHint(p)) {
		return false // inside a phrase already begun
	}
	switch up {
	case "JOIN":
		return true
	case "INNER", "LEFT", "RIGHT", "FULL":
		return nx == "JOIN" || nx == "OUTER" || sqlJoinHints[nx]
	case "CROSS":
		return nx == "JOIN" || nx == "APPLY"
	case "OUTER":
		return nx == "APPLY"
	}
	return false
}

func (l *sqlLayout) openParen(i int) {
	f, o := l.top(), l.owner()
	nx := l.tok(l.nextC[i])
	opener := ""
	if p := l.prevC[i]; p >= 0 && l.t[p].kind == sqltext.KindWord {
		opener = l.t[p].up
	}
	l.inline(i)
	switch {
	case nx.kind == sqltext.KindWord && (nx.up == "SELECT" || nx.up == "WITH" || nx.up == "VALUES"):
		l.push(&sqlFrame{kind: frSub, base: f.ctx + l.w, open: f.ctx, first: l.nextC[i], ctx: f.ctx + l.w, st: freshStmt()})
	case f == o && l.colsParen(i, o):
		o.st.colsDone = true
		c := &sqlFrame{kind: frCols, open: f.ctx, item: l.nextC[i], ctx: f.ctx}
		if c.expand = l.expands(l.nextC[i], nil, false); c.expand {
			c.ctx = f.ctx + l.w
			o.st.deferAt, o.st.deferInd = l.nextC[i], c.ctx
		}
		l.push(c)
	default:
		l.push(&sqlFrame{kind: frParen, opener: opener, hints: opener == "WITH" && l.hintParen(i, o), ctx: f.ctx})
	}
}

// hintParen reports whether "(" at i, after WITH, opens a table hint list:
// WITH follows a table or alias (a word, not OPENJSON(...)'s ")" whose WITH
// lists columns) in a SELECT or DML statement (not CREATE INDEX ... WITH).
func (l *sqlLayout) hintParen(i int, o *sqlFrame) bool {
	w := l.prevC[i]
	return (o.st.kind == "SELECT" || dmlKinds[o.st.kind]) && w >= 0 && l.prevC[w] >= 0 &&
		l.t[l.prevC[w]].kind != sqltext.KindPunct
}

// colsParen reports whether "(" at i opens a table definition's column
// list: CREATE TABLE's first, or the one after TABLE in DECLARE @t TABLE,
// RETURNS @t TABLE and CREATE TYPE ... AS TABLE.
func (l *sqlLayout) colsParen(i int, o *sqlFrame) bool {
	switch k := o.st.kind; {
	case l.prevUp(i) == "TABLE":
		return k == "DECLARE" || k == "MODULE" || k == "TABLE"
	case k == "TABLE" && !o.st.colsDone:
		// CREATE TABLE name (: CREATE TYPE's first "(" may be varchar(10).
		for k := l.prevC[i]; k >= 0; k = l.prevC[k] {
			if l.t[k].kind == sqltext.KindWord && !l.qualified(k) && l.t[k].up == "TYPE" {
				return false
			}
			if l.start[k] {
				break
			}
		}
		return true
	}
	return false
}

func (l *sqlLayout) closeParen(i int) {
	f := l.top()
	if f.kind == frParen || f.kind == frSub || f.kind == frCols {
		l.pop()
	}
	switch {
	case f.kind == frSub:
		l.putC(i, nlLine, f.open, f.base)
	case f.kind == frCols && f.expand:
		l.putC(i, nlLine, f.open, f.ctx)
	default:
		l.inline(i)
	}
}

func (l *sqlLayout) comma(i int) {
	f, o := l.top(), l.owner()
	lead := min(2, l.w)
	switch {
	case f.kind == frCols:
		f.item = l.nextC[i]
		if f.expand {
			l.put(i, nlLine, f.ctx-lead)
			return
		}
	case f == o && len(f.cases) == 0 && o.st.list:
		l.put(i, nlLine, o.st.listInd-lead)
		f.ctx = o.st.listInd
		return
	case f == o && len(f.cases) == 0 && o.st.kind == "WITH" && o.st.clause == "CTE":
		l.put(i, nlLine, o.base)
		return
	}
	l.inline(i)
}
