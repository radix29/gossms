package format

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ---------------------------------------------------------------------------
// "name = expr" aliases (sqlStyle.aliasEquals)
// ---------------------------------------------------------------------------
//
// A select-list item "expr AS name" and "name = expr" are the same column to
// SQL Server, so an aliasEquals style writes the second. It is the one place
// a style reorders tokens, so both halves are deliberately narrow:
//
//   - The layout (equalsAliases) moves an alias only in an item it can see
//     whole: no comment inside, nothing at the item's own level but a scalar
//     expression (sqlAliasExprWords, no "="), one AS, and the alias a plain or
//     quoted name ending the item. A string alias ('n' = expr is deprecated),
//     an implicit alias (expr name) and anything else stay as written.
//   - The guard (sqlAliasSwaps) re-derives the select lists and their items
//     from the input's lexer tokens and sqltext's statement cutter alone, not
//     from the layout's frames, and accepts "name = expr" in the output only
//     for an input item it finds in that form by the same rules. A layout bug
//     that moves anything else is still a guard refusal.

// equalsAliases rewrites, in l.t, every select-list item "expr AS name" the
// rules above allow into "name = expr", carrying l.start over and relinking.
// It runs after links and statements, which the rewrite leaves valid: no
// statement starts inside a moved item, so only positions shift.
func (l *sqlLayout) equalsAliases() {
	items := map[int]int{} // an item's first token -> its AS
	for s := range l.t {
		if l.t[s].isWord("SELECT") && !l.qualified(s) {
			for j := l.selectItems(s); j >= 0; {
				as, end, ok := l.aliasItem(j)
				if ok {
					items[j] = as
				}
				if end < 0 || !l.t[end].isPunct(",") {
					break
				}
				j = l.nextC[end]
			}
		}
	}
	if len(items) == 0 {
		return
	}
	toks := make([]sqlTok, 0, len(l.t)+len(items))
	start := make([]bool, 0, len(l.t)+len(items))
	var emit func(a, b int)
	emit = func(a, b int) {
		for i := a; i < b; i++ {
			as, ok := items[i]
			if !ok {
				toks, start = append(toks, l.t[i]), append(start, l.start[i])
				continue
			}
			// name takes the item's place and its line break; the moved
			// expression follows " = ". name is always spaced: the item's
			// first token may have been written touching what came before
			// (SELECT(1) AS a), and a word there would join it. An item
			// nested in this one (a subquery's) starts after i, so emit finds
			// it in turn.
			name, first := l.t[as+1], l.t[i]
			name.nl, name.sp = first.nl, true
			first.nl, first.sp = 0, true
			toks = append(toks, name, sqlTok{kind: sqltext.KindPunct, text: "=", sp: true}, first)
			start = append(start, false, false, l.start[i])
			emit(i+1, as)
			i = as + 1
		}
	}
	emit(0, len(l.t))
	l.t, l.start = toks, start
	l.links()
}

// aliasItem examines the select-list item starting at code token j. It
// returns the item's AS (-1 at none), the code token that ended the item — a
// list comma, ")", ";", a GO line, a statement start or a word that ends a
// select list; -1 at the end of the text — and whether the item may be
// rewritten.
func (l *sqlLayout) aliasItem(j int) (as, end int, ok bool) {
	as, cases, clean := -1, 0, true
	k := j
	for ; k >= 0; k = l.nextC[k] {
		t := &l.t[k]
		word := t.kind == sqltext.KindWord && !l.qualified(k)
		if t.goSep || (k != j && l.start[k]) || t.isPunct(";") || t.isPunct(")") ||
			(cases == 0 && (t.isPunct(",") ||
				word && selectEnds[t.up] && !(t.up == "GROUP" && l.prevUp(k) == "WITHIN"))) {
			break
		}
		if as >= 0 && k > as+1 {
			clean = false // more after the alias: the AS was not an alias's
		}
		switch {
		case t.isPunct("("):
			k = l.match[k] // checkParens: every "(" has its ")"
		case word && t.up == "CASE":
			cases++
		case word && t.up == "END" && cases > 0:
			cases--
		case cases > 0:
		case word && t.up == "AS":
			if as >= 0 || k == j {
				clean = false
			}
			as = k
		case word && sqlReserved[t.up] && !sqlAliasExprWords[t.up]:
			clean = false
		case t.kind == sqltext.KindPunct && strings.Contains(t.text, "="):
			clean = false
		}
	}
	end = k
	// The item must run through the name: a statement can start at it
	// (CREATE VIEW v AS x, the body after AS), which is no alias.
	if !clean || as <= j || as+1 >= len(l.t) || end == as+1 || l.nextC[as+1] != end ||
		!aliasName(&l.t[as+1]) || l.qualified(as+1) {
		return as, end, false
	}
	for x := j; x <= as+1; x++ {
		if l.t[x].comment() {
			return as, end, false
		}
	}
	return as, end, true
}

// aliasName reports whether t can be moved before "=" as a column alias: a
// plain name (not a variable, temp name or reserved word) or a quoted one.
// A string alias stays put — 'n' = expr is a deprecated form.
func aliasName(t *sqlTok) bool {
	switch t.kind {
	case sqltext.KindQuotedIdent:
		return true
	case sqltext.KindWord:
		return !strings.HasPrefix(t.text, "@") && !strings.HasPrefix(t.text, "#") && !sqlReserved[t.up]
	}
	return false
}

// ---------------------------------------------------------------------------
// The guard's half
// ---------------------------------------------------------------------------

// sigView is a lexed script's guard stream with what sqlAliasSwaps reads of
// each token precomputed.
type sigView struct {
	lx    *sqlLexed
	sig   []sigTok
	kind  []sqltext.Kind // KindEnd for a GO line
	text  []string
	up    []string // upper-cased words, "" otherwise
	start []bool   // sqltext.StatementCutter's statement starts
	match []int    // a parenthesis's partner, -1 otherwise
}

// newSigView builds the view of lx's guard stream sig (sigStream(lx)).
func newSigView(lx *sqlLexed, sig []sigTok) *sigView {
	v := &sigView{lx: lx, sig: sig}
	n := len(v.sig)
	v.kind, v.text, v.up = make([]sqltext.Kind, n), make([]string, n), make([]string, n)
	v.start, v.match = make([]bool, n), make([]int, n)
	cut := sqltext.NewStatementCutter(func(pos int) { v.start[pos] = true })
	var open []int
	for x, s := range v.sig {
		v.match[x] = -1
		if s.part < 0 {
			cut.Reset()
			open = open[:0]
			continue
		}
		p := lx.parts[s.part]
		v.kind[x], v.text[x] = p.Kind, string(lx.buf[p.Start:p.End])
		if p.Kind == sqltext.KindWord {
			v.up[x] = strings.ToUpper(v.text[x])
		}
		cut.Feed(lx.buf, p, x)
		switch {
		case v.punct(x, "("):
			open = append(open, x)
		case v.punct(x, ")") && len(open) > 0:
			o := open[len(open)-1]
			open = open[:len(open)-1]
			v.match[o], v.match[x] = x, o
		}
	}
	return v
}

func (v *sigView) goSep(x int) bool { return v.sig[x].part < 0 }

func (v *sigView) punct(x int, s string) bool {
	return x >= 0 && x < len(v.sig) && v.kind[x] == sqltext.KindPunct && v.text[x] == s
}

func (v *sigView) comment(x int) bool { return v.kind[x] == sqltext.KindComment }

// nextCode and prevCode skip comments; -1 past either end.
func (v *sigView) nextCode(x int) int {
	for x++; x < len(v.sig); x++ {
		if !v.comment(x) {
			return x
		}
	}
	return -1
}

func (v *sigView) prevCode(x int) int {
	for x--; x >= 0; x-- {
		if !v.comment(x) {
			return x
		}
	}
	return -1
}

// keyword is x's upper-cased spelling when x is an unqualified word, "" else.
func (v *sigView) keyword(x int) string {
	if x < 0 || v.kind[x] != sqltext.KindWord || v.punct(v.prevCode(x), ".") || v.punct(v.nextCode(x), ".") {
		return ""
	}
	return v.up[x]
}

// firstItem is the first select-list item after SELECT at x, past ALL,
// DISTINCT and TOP n|(n) [PERCENT] [WITH TIES]; len(sig) at none.
func (v *sigView) firstItem(x int) int {
	j := v.nextCode(x)
	for j >= 0 {
		switch v.keyword(j) {
		case "ALL", "DISTINCT":
			j = v.nextCode(j)
			continue
		case "TOP":
			j = v.nextCode(j)
			if v.punct(j, "(") && v.match[j] >= 0 {
				j = v.match[j]
			}
			if j >= 0 {
				j = v.nextCode(j)
			}
			if v.keyword(j) == "PERCENT" {
				j = v.nextCode(j)
			}
			if v.keyword(j) == "WITH" && v.keyword(v.nextCode(j)) == "TIES" {
				j = v.nextCode(v.nextCode(j))
			}
			continue
		}
		break
	}
	if j < 0 {
		return len(v.sig)
	}
	return j
}

// sqlAliasSwaps finds the input's select-list items "expr AS name" that the
// output may write "name = expr": the guard stream index of each item's first
// token, mapped to its AS. See the section comment for why it is separate
// from equalsAliases.
func sqlAliasSwaps(v *sigView) map[int]int {
	type item struct {
		depth, cases, start, as int
		bad                     bool
	}
	swaps := map[int]int{}
	var lists []*item
	n := len(v.sig)
	finish := func(it *item, end int) {
		nm := it.as + 1
		if it.bad || it.as <= it.start || nm >= n || v.nextCode(nm) != end {
			return
		}
		switch v.kind[nm] {
		case sqltext.KindQuotedIdent:
		case sqltext.KindWord:
			if strings.HasPrefix(v.text[nm], "@") || strings.HasPrefix(v.text[nm], "#") || sqlReserved[v.up[nm]] {
				return
			}
		default:
			return
		}
		if end >= 0 && !v.goSep(end) && !v.start[end] && v.kind[end] != sqltext.KindWord &&
			!v.punct(end, ",") && !v.punct(end, ")") && !v.punct(end, ";") {
			return
		}
		swaps[it.start] = it.as
	}
	// endLists finishes and drops the lists at depth d and deeper.
	endLists := func(end, d int) {
		for len(lists) > 0 && lists[len(lists)-1].depth >= d {
			finish(lists[len(lists)-1], end)
			lists = lists[:len(lists)-1]
		}
	}
	depth := 0
	for x := range n {
		if v.comment(x) {
			// A comment inside an item, down to its alias, rules it out.
			for _, it := range lists {
				if x > it.start && (it.as < 0 || x <= it.as+1) {
					it.bad = true
				}
			}
			continue
		}
		if v.goSep(x) {
			endLists(x, 0)
			depth = 0
			continue
		}
		if v.start[x] {
			endLists(x, depth)
		}
		var top *item
		if len(lists) > 0 {
			top = lists[len(lists)-1]
		}
		if top != nil && top.depth == depth && top.cases == 0 && top.as >= 0 && x > top.as+1 {
			// Past the alias: the item ended here.
			finish(top, x)
			if v.punct(x, ",") {
				*top = item{depth: depth, start: v.nextCode(x), as: -1}
				if top.start < 0 {
					top.start = n
				}
				continue
			}
			// Anything else (ELSE, which the cutter doesn't cut, or a stray
			// token) leaves the list open for a later comma, with nothing
			// more to swap in this item.
			*top = item{depth: depth, start: x, as: -1, bad: true}
		}
		kw := v.keyword(x)
		switch {
		case v.punct(x, "("):
			depth++
		case v.punct(x, ")"):
			endLists(x, depth)
			depth = max(depth-1, 0)
		case v.punct(x, ";"):
			endLists(x, depth)
		case kw == "SELECT":
			endLists(x, depth)
			lists = append(lists, &item{depth: depth, start: v.firstItem(x), as: -1})
		case top == nil || top.depth != depth || x < top.start:
		case kw == "CASE":
			top.cases++
		case kw == "END" && top.cases > 0:
			top.cases--
		case top.cases > 0:
		case kw == "AS":
			if top.as >= 0 || x == top.start {
				top.bad = true
			}
			top.as = x
		case selectEnds[kw] && !(kw == "GROUP" && v.keyword(v.prevCode(x)) == "WITHIN"):
			endLists(x, depth)
		case v.punct(x, ","):
			finish(top, x)
			*top = item{depth: depth, start: v.nextCode(x), as: -1}
			if top.start < 0 {
				top.start = n
			}
		case sqlReserved[kw] && !sqlAliasExprWords[kw]:
			top.bad = true
		case v.punct(x, "="):
			top.bad = true
		}
	}
	endLists(-1, 0)
	return swaps
}
