package format

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ---------------------------------------------------------------------------
// The SQL styles' layout pass
// ---------------------------------------------------------------------------
//
// One walk over the tokens decides, for each, whether it starts a new line
// and at what indent; nothing but that whitespace (and keyword case) is
// produced. Statement boundaries come first, from sqltext's statement-leader
// rule plus the control flow it leaves out (statements). The walk then keeps
// a stack of frames: the script, a BEGIN ... END block, an IF/WHILE/ELSE's
// one-statement body, and each open parenthesis — a subquery (laid out as a
// statement of its own, indented), a table definition's column list (one
// column per line), or any other group (inline).
//
// Every decision depends on the tokens alone, never on the source's layout,
// except where the source's choice is kept on purpose: blank lines between
// statements (at most one), whether a comment stood on its own line, the
// line breaks of a statement the style has no rules for, and whether two
// tokens were written touching (N'x', $(var), "( " is not one of those). Each
// of those survives into the output unchanged, so formatting twice gives
// what formatting once did.

type frKind uint8

const (
	frRoot  frKind = iota // the script
	frBlock               // BEGIN ... END
	frBody                // an IF, WHILE or ELSE's one-statement body
	frSub                 // ( SELECT ... ): a statement of its own
	frCols                // a table definition's column list
	frParen               // any other parenthesis, laid out inline
)

// sqlFrame is one level of nesting. A frame of kind root, block, body or
// sub owns a statement (stmt); a paren or cols frame lives inside its
// owner's statement.
type sqlFrame struct {
	kind   frKind
	base   int    // indent the statements of an owner frame start at
	open   int    // block, sub, cols: indent of the line that opened it, where its closer goes
	ifInd  int    // body: indent of the IF/WHILE/ELSE it belongs to
	elseOK bool   // body of an IF: an ELSE may close it
	filled bool   // body: its statement has begun
	first  int    // sub: its first token, which starts its statement
	opener string // paren: the word before "(", e.g. CAST
	hints  bool   // paren: a table hint list, WITH (NOLOCK, INDEX(ix)) after a table in DML
	expand bool   // cols: one column per line (more than one column)
	item   int    // cols: first token of the current column definition
	ctx    int    // indent of the current logical line — continuations sit one level deeper
	cases  []sqlCase
	st     stmtState
}

type sqlCase struct {
	expand bool // WHEN / ELSE / END each on a line of their own
	ind    int  // indent of CASE's line, where END goes
}

// stmtState is an owner frame's statement in progress.
type stmtState struct {
	kind     string // its leader: SELECT, UPDATE, ..., MODULE (CREATE PROC/VIEW/...), TABLE (CREATE TABLE/TYPE)
	rules    bool   // laid out by clause rules; false keeps the source's line breaks
	clause   string // the clause in progress: SELECT, FROM, JOIN, WHERE, SET, CTE, ...
	list     bool   // top-level commas start a new line, leading
	listInd  int    // indent of the list's items
	deferAt  int    // a token that must start a line at deferInd (a list's first item, a MERGE action); -1 when none
	deferInd int
	pred     bool // top-level AND/OR start a new line (WHERE, HAVING, ON)
	between  int  // BETWEENs awaiting their AND
	act      bool // inside a MERGE action (after WHEN ... THEN): clauses one level deeper
	colsDone bool // CREATE TABLE's column list has been opened
	assigned bool // a SET statement has passed its "=": what follows is an expression
}

type sqlLayout struct {
	lx      *sqlLexed
	sty     sqlStyle
	t       []sqlTok // lx.toks, or a reordered copy when sty.aliasEquals moved an alias
	w       int
	prefix  string // Options.LineIndent, before every line the layout starts
	start   []bool // the token begins a statement
	prevC   []int  // previous non-comment token, -1 at none
	nextC   []int  // next non-comment token, -1 at none
	match   []int  // a parenthesis's partner
	frames  []*sqlFrame
	b       strings.Builder
	wrote   bool // anything is on the page
	empty   bool // the current line holds only its indent
	last    int  // last code token written, -1 at none
	afterLC bool // a "--" comment ended the line: the next token needs a new one
	afterGo bool // a GO line was written: the next batch is one blank line away
	pending []int
	block   *int      // a BEGIN block's indent, until its first statement opens the block
	body    *sqlFrame // an IF/WHILE/ELSE's body frame, pushed when its statement begins
	elseIf  int       // an IF directly after ELSE, kept on ELSE's line; -1 when none
	semi    int       // a ";" that was a statement of its own (";WITH"); -1 when none

	inlineComment bool // a comment was written after the last code token, on its line
	goLed         bool // the current line is a word "go" and maybe numbers: see putC
	vfOnly        bool // the current line holds only its indent and \v/\f tokens, which the GO rule reads as blanks
}

func newSQLLayout(lx *sqlLexed, o Options, sty sqlStyle) *sqlLayout {
	w := o.IndentWidth
	if w <= 0 {
		w = DefaultIndentWidth
	}
	l := &sqlLayout{lx: lx, sty: sty, t: lx.toks, w: w, prefix: o.LineIndent, last: -1, elseIf: -1, semi: -1}
	l.links()
	l.statements()
	if sty.aliasEquals {
		l.equalsAliases()
	}
	return l
}

func (l *sqlLayout) run() string {
	l.frames = []*sqlFrame{{kind: frRoot, st: freshStmt()}}
	for i := range l.t {
		t := &l.t[i]
		switch {
		case t.comment():
			l.comment(i)
		case t.goSep:
			l.goLine(i)
		default:
			l.code(i)
		}
	}
	l.flush(l.owner().base, 1)
	return l.b.String()
}

// ---------------------------------------------------------------------------
// Pre-pass: neighbours, paren partners, statement starts
// ---------------------------------------------------------------------------

func (l *sqlLayout) links() {
	n := len(l.t)
	l.prevC, l.nextC, l.match = make([]int, n), make([]int, n), make([]int, n)
	p := -1
	var open []int
	for i := range l.t {
		l.prevC[i], l.match[i] = p, -1
		if l.t[i].comment() {
			continue
		}
		p = i
		switch {
		case l.t[i].isPunct("("):
			open = append(open, i)
		case l.t[i].isPunct(")") && len(open) > 0:
			o := open[len(open)-1]
			open = open[:len(open)-1]
			l.match[o], l.match[i] = i, o
		}
	}
	nx := -1
	for i := n - 1; i >= 0; i-- {
		l.nextC[i] = nx
		if !l.t[i].comment() {
			nx = i
		}
	}
}

func (l *sqlLayout) tok(i int) *sqlTok {
	if i < 0 || i >= len(l.t) {
		return &sqlTok{}
	}
	return &l.t[i]
}

// prevUp and nextUp are the upper-cased words either side of token i
// (comments skipped); "" past either end or for i < 0.
func (l *sqlLayout) prevUp(i int) string {
	if i < 0 {
		return ""
	}
	return l.tok(l.prevC[i]).up
}

func (l *sqlLayout) nextUp(i int) string {
	if i < 0 {
		return ""
	}
	return l.tok(l.nextC[i]).up
}

// qualified reports whether word i is a part of a dotted name (dbo.t, t.date),
// which is never a keyword.
func (l *sqlLayout) qualified(i int) bool {
	return l.tok(l.prevC[i]).isPunct(".") || (l.nextC[i] >= 0 && l.t[l.nextC[i]].isPunct("."))
}

// statements marks the tokens that begin a statement: sqltext's leader rule
// (StatementCutter), the token after each ";" and GO line, and the control
// flow sqltext leaves to its callers — ELSE, a block's END and what follows
// it, the first statement inside BEGIN, a module's body after AS, a label,
// and the leaders sqlExtraLeaders adds.
func (l *sqlLayout) statements() {
	l.start = make([]bool, len(l.t))
	cut := sqltext.NewStatementCutter(func(pos int) { l.start[pos] = true })
	depth, cases := 0, 0
	nextStart, module := true, false
	after := -1  // the token after this one starts a statement
	atomic := -1 // BEGIN ATOMIC WITH (...): the ")" that ends its header
	for i := range l.t {
		t := &l.t[i]
		if t.comment() {
			continue
		}
		if t.goSep {
			cut.Reset()
			depth, cases, nextStart, module = 0, 0, true, false
			continue
		}
		if nextStart {
			l.start[i] = true
			nextStart = false
		}
		for _, p := range l.lx.parts[t.p0:t.p1] {
			cut.Feed(l.lx.buf, p, i)
		}
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth = max(depth-1, 0)
			if i == atomic {
				nextStart = true
			}
		case t.isPunct(";"):
			if depth == 0 {
				nextStart, module = true, false
			}
		case t.isPunct(":") && !t.sp && depth == 0 && cases == 0:
			if p := i - 1; p >= 0 && l.t[p].kind == sqltext.KindWord && !l.qualified(p) {
				l.start[p] = true // a GOTO label, its ":" kept with it
				l.start[i], nextStart = false, true
			}
		case t.kind == sqltext.KindWord && !l.qualified(i):
			up := t.up
			switch {
			case up == "CASE":
				cases++
			case up == "END" && cases > 0:
				cases--
			case depth > 0 || cases > 0:
			case up == "END":
				l.start[i] = true
				switch l.nextUp(i) {
				case "TRY", "CATCH":
					after = l.nextC[i]
				case "CONVERSATION":
				default:
					nextStart = true
				}
			case up == "ELSE":
				l.start[i] = true
				nextStart = true
			case up == "BEGIN":
				switch nx := l.nextUp(i); {
				case sqlBlockBeginNot[nx]:
				case nx == "TRY" || nx == "CATCH":
					after = l.nextC[i]
				case nx == "ATOMIC":
					// BEGIN ATOMIC WITH (options): the header ends with its ")".
					for k := l.nextC[i]; k >= 0; k = l.nextC[k] {
						if l.t[k].isPunct("(") {
							atomic = l.match[k]
							break
						}
					}
				default:
					nextStart = true
				}
			case sqlExtraLeaders[up]:
				l.start[i] = true
			case (up == "CREATE" || up == "ALTER") && l.moduleHeader(i):
				module = true
			case up == "AS" && module && l.prevUp(i) != "EXECUTE" && l.prevUp(i) != "EXEC":
				nextStart, module = true, false
			}
		}
		if i == after {
			nextStart = true
		}
	}
}

// moduleHeader reports whether CREATE/ALTER at i heads a procedure,
// function, trigger or view, whose body after AS is statements.
func (l *sqlLayout) moduleHeader(i int) bool {
	k := l.nextC[i]
	if l.tok(k).up == "OR" && l.nextUp(k) == "ALTER" {
		k = l.nextC[l.nextC[k]]
	}
	return sqlModuleKinds[l.tok(k).up]
}

// ---------------------------------------------------------------------------
// Frames
// ---------------------------------------------------------------------------

func freshStmt() stmtState { return stmtState{deferAt: -1} }

func (l *sqlLayout) top() *sqlFrame { return l.frames[len(l.frames)-1] }

// owner is the innermost frame that owns a statement.
func (l *sqlLayout) owner() *sqlFrame {
	for k := len(l.frames) - 1; k > 0; k-- {
		if f := l.frames[k]; f.kind != frParen && f.kind != frCols {
			return f
		}
	}
	return l.frames[0]
}

func (l *sqlLayout) push(f *sqlFrame) { l.frames = append(l.frames, f) }

func (l *sqlLayout) pop() *sqlFrame {
	f := l.top()
	if len(l.frames) > 1 {
		l.frames = l.frames[:len(l.frames)-1]
	}
	return f
}

// afterLCInd is where the line a "--" comment forced continues: where the
// token would have begun its own line anyway when the comment trails a list
// comma or an opening parenthesis, one level under the line otherwise.
func (l *sqlLayout) afterLCInd() int {
	if p := l.tok(l.last); p.isPunct(",") || p.isPunct("(") {
		return l.top().ctx
	}
	return l.cont()
}

// cont is where a line the rules didn't ask for (after a "--" comment, or a
// kept source break) continues: one level under the current logical line.
func (l *sqlLayout) cont() int { return l.top().ctx + l.w }

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

const (
	nlNone  = iota // on the current line
	nlLine         // on a new line
	nlStmt         // on a new line, a blank one before it kept if the source had one
	nlBatch        // after a blank line
)

func (l *sqlLayout) newline(blank bool, ind int) {
	if l.wrote {
		l.b.WriteByte('\n')
		if blank {
			l.b.WriteByte('\n')
		}
	}
	l.b.WriteString(l.prefix)
	l.b.WriteString(strings.Repeat(" ", ind))
	l.wrote, l.empty, l.vfOnly = true, true, true
}

func (l *sqlLayout) write(s string) {
	l.b.WriteString(s)
	l.wrote, l.empty = true, false
}

// put writes code token i: on the current line (nl nlNone) or on a new one
// at ind. Own-line comments waiting before it go on lines of their own at
// the same indent.
func (l *sqlLayout) put(i, nl, ind int) { l.putC(i, nl, ind, ind) }

// putC is put with the waiting comments at cind (END's comments belong to the
// block it closes, one level in from END itself).
func (l *sqlLayout) putC(i, nl, ind, cind int) {
	t := &l.t[i]
	if f := l.owner(); f.st.deferAt == i {
		if nl == nlNone {
			nl, ind, cind = nlLine, f.st.deferInd, f.st.deferInd
		}
		f.st.deferAt = -1
	}
	// A line holding only the word "go" (an identifier: GO is not
	// reserved) and a count is a batch separator, so the layout never starts
	// one by rule, and a line that starts with one anyway (after a comment)
	// keeps what followed it on the source line, which made it no separator.
	// \v and \f are punctuation to the lexer but leading blanks to the GO
	// rule, so they never start a line by rule either.
	goWord := t.kind == sqltext.KindWord && t.up == "GO"
	blankish := goWord || t.isPunct("\v") || t.isPunct("\f")
	if (blankish && l.wrote && !l.afterLC && !l.afterGo && len(l.pending) == 0) || (l.goLed && t.nl == 0) {
		nl = nlNone
	}
	if l.afterGo && nl != nlNone {
		nl = nlBatch
	}
	if len(l.pending) > 0 {
		if nl == nlNone {
			nl, ind = nlLine, l.cont()
			cind = ind
		}
		last := l.flush(cind, nl)
		if !last.lineComment() && t.nl == 0 {
			l.write(" ")
		} else {
			l.newline(nl >= nlStmt && t.nl >= 2, ind)
		}
	} else if nl != nlNone {
		l.newline(nl == nlBatch || (nl == nlStmt && t.nl >= 2), ind)
	} else if l.afterLC {
		l.newline(false, l.afterLCInd())
	} else if !l.empty && l.wrote && (l.last < 0 || l.inlineComment || l.space(l.last, i)) {
		l.write(" ")
	}
	lineStart := l.vfOnly || !l.wrote
	l.write(l.text(i))
	l.vfOnly = lineStart && (t.isPunct("\v") || t.isPunct("\f"))
	l.last, l.afterLC, l.afterGo, l.inlineComment = i, false, false, false
	switch {
	case goWord && lineStart:
		l.goLed = true
	case t.kind != sqltext.KindNumber && !blankish:
		l.goLed = false
	}
}

// flush writes the waiting own-line comments, the first on a new line at ind
// (after a blank one for nlBatch, or as the source had it for nlStmt), and
// returns the last.
func (l *sqlLayout) flush(ind, nl int) *sqlTok {
	var last *sqlTok
	for k, c := range l.pending {
		t := &l.t[c]
		switch {
		case k > 0 && t.nl == 0 && !last.lineComment():
			l.write(" ")
		default:
			blank := (k == 0 && nl == nlBatch) || (nl >= nlStmt && t.nl >= 2)
			l.newline(blank, ind)
		}
		l.write(t.text)
		l.vfOnly = false
		last = t
	}
	l.pending = l.pending[:0]
	if last != nil {
		l.afterLC = last.lineComment()
		l.afterGo = false
		l.inlineComment = false
	}
	return last
}

// comment places comment i: on the current line when the source had it
// there, else held until the next code token decides its line.
func (l *sqlLayout) comment(i int) {
	t := &l.t[i]
	if t.nl > 0 || !l.wrote || len(l.pending) > 0 {
		l.pending = append(l.pending, i)
		return
	}
	l.write(" " + t.text)
	l.afterLC = t.lineComment()
	l.inlineComment, l.goLed, l.vfOnly = true, false, false
}

func (l *sqlLayout) goLine(i int) {
	l.frames = l.frames[:1]
	l.frames[0].st = freshStmt()
	l.block, l.body = nil, nil
	l.put(i, nlLine, 0)
	l.afterGo = true
}
