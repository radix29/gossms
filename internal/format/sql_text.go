package format

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ---------------------------------------------------------------------------
// Keyword case
// ---------------------------------------------------------------------------

// text is code token i as written: its source spelling, upper-cased (lower-
// cased in a lowerKeywords style) when it is a keyword where it stands (see
// sql_rules.go).
func (l *sqlLayout) text(i int) string {
	t := &l.t[i]
	if t.kind != sqltext.KindWord {
		return t.text
	}
	want := t.up
	if l.sty.lowerKeywords {
		want = strings.ToLower(t.text) // no allocation when already lower
	}
	if t.text == want || strings.HasPrefix(t.text, "@") || l.qualified(i) {
		return t.text
	}
	up := t.up
	switch {
	case sqlReserved[up]:
	case sqlFunctions[up] && l.tok(l.nextC[i]).isPunct("(") && !l.namePos(i):
	case sqlTypes[up] && l.typePos(i):
	case sqlSetOptions[up] && l.setOption():
	case sqlPhraseWords[up] && l.phraseWord(i):
	case sqlTableHints[up] && l.top().hints && (l.tok(l.prevC[i]).isPunct("(") || l.tok(l.prevC[i]).isPunct(",")):
	case sqlForWords[up] && l.top() == l.owner() && l.owner().st.clause == "FOR":
	case sqlModuleWords[up] && l.top() == l.owner() && l.owner().st.kind == "MODULE":
	default:
		return t.text
	}
	return want
}

// namePos reports whether word i stands where an object name does, so that
// a "(" after it opens a column list or a table hint rather than a call:
// INSERT INTO log (msg), CREATE TABLE year (...), WITH rank (r) AS,
// JOIN log (NOLOCK), a row source's alias and column list (... ) AS log
// (msg), ... ) count (a)) and CREATE TABLE ... ON scheme (col). A position
// missed here costs only casing; one wrongly counted would re-case a name,
// so each is a place no call can stand.
func (l *sqlLayout) namePos(i int) bool {
	p := l.prevC[i]
	f, o := l.top(), l.owner()
	cte := f == o && len(f.cases) == 0 && o.st.kind == "WITH" && o.st.clause == "CTE"
	switch pt := l.tok(p); {
	case pt.isPunct(","):
		return cte
	case pt.isPunct(")"):
		return !l.start[i] // nothing calls a function straight after ")"
	case pt.isWord("AS"):
		// Not a computed column, c AS YEAR(d): only a row source ends in ")".
		return l.tok(l.prevC[p]).isPunct(")")
	}
	switch l.tok(p).up {
	case "INTO", "INSERT", "TABLE", "UPDATE", "MERGE", "REFERENCES", "STATISTICS",
		"VIEW", "PROC", "PROCEDURE", "FUNCTION", "TRIGGER":
		return true
	case "FROM", "JOIN", "APPLY":
		// Not TRIM(' ' FROM LEFT(s, 3)) or a IS DISTINCT FROM LEFT(b, 1).
		return !sqlTableFunctions[l.t[i].up] && f.kind != frParen && l.prevUp(p) != "DISTINCT"
	case "WITH":
		return cte
	case "ON":
		// CREATE INDEX ix ON log (msg), CREATE STATISTICS, GRANT SELECT ON
		// log (msg): not JOIN ... ON, where a predicate begins.
		for k := p; k >= 0; k = l.prevC[k] {
			if t := &l.t[k]; t.kind == sqltext.KindWord && !l.qualified(k) {
				switch t.up {
				case "INDEX", "STATISTICS", "GRANT", "DENY", "REVOKE", "TABLE":
					return true
				case "JOIN", "USING":
					return false
				}
			}
			if l.start[k] {
				break
			}
		}
	}
	return false
}

// typePos reports whether word i stands where a data type does.
func (l *sqlLayout) typePos(i int) bool {
	p := l.prevC[i]
	pt := l.tok(p)
	f, o := l.top(), l.owner()
	isVar := func(t *sqlTok) bool {
		return t.kind == sqltext.KindWord && strings.HasPrefix(t.text, "@") && !strings.HasPrefix(t.text, "@@")
	}
	switch {
	case isVar(pt), pt.up == "AS" && isVar(l.tok(l.prevC[p])):
		return o.st.kind == "DECLARE" || o.st.kind == "MODULE"
	case pt.up == "AS" && f.kind == frParen:
		return f.opener == "CAST" || f.opener == "TRY_CAST"
	case pt.isPunct("(") && f.kind == frParen:
		return f.opener == "CONVERT" || f.opener == "TRY_CONVERT"
	case pt.up == "RETURNS":
		return true
	case f.kind == frCols:
		return p == f.item && p >= 0
	}
	return false
}

// setOption reports whether the word being written names a SET statement's
// option (before any "=").
func (l *sqlLayout) setOption() bool {
	o := l.owner()
	return l.top() == o && o.st.kind == "SET" && !o.st.assigned
}

// phraseWord reports whether non-reserved word i is a keyword in the phrase
// around it.
func (l *sqlLayout) phraseWord(i int) bool {
	pu, nu := l.prevUp(i), l.nextUp(i)
	o := l.owner()
	switch l.t[i].up {
	case "TRY", "CATCH":
		return pu == "BEGIN" || pu == "END"
	case "ATOMIC":
		return pu == "BEGIN"
	case "MATCHED":
		return pu == "WHEN" || (pu == "NOT" && l.prevUp(l.prevC[i]) == "WHEN")
	case "TARGET", "SOURCE":
		return pu == "BY" && l.prevUp(l.prevC[i]) == "MATCHED"
	case "APPLY":
		return pu == "CROSS" || pu == "OUTER"
	case "HASH", "LOOP", "REMOTE":
		return l.joinHint(i)
	case "OUTPUT":
		return (l.top() == o && dmlKinds[o.st.kind] && l.clauseWord(i)) || o.st.kind == "MODULE"
	case "OUT", "READONLY":
		return o.st.kind == "MODULE"
	case "USING":
		return o.st.kind == "MERGE"
	case "OFFSET":
		return l.top() == o && o.st.clause == "ORDER BY" && l.clauseWord(i)
	case "THROW":
		return l.start[i]
	case "RETURNS":
		return o.st.kind == "MODULE"
	case "PARTITION":
		return nu == "BY"
	case "ROWS", "ROW", "NEXT", "FIRST", "ONLY":
		return l.top() == o && o.st.clause == "OFFSET"
	case "TIES":
		return pu == "WITH" && o.st.clause == "SELECT"
	}
	return false
}

// ---------------------------------------------------------------------------
// Spacing within a line
// ---------------------------------------------------------------------------

func cmpOp(s string) bool {
	switch s {
	case "=", "<", ">":
		return true
	}
	return len(s) == 2 && s != "::" && sqlCompound[s]
}

func arithOp(s string) bool {
	switch s {
	case "+", "-", "*", "/", "%", "&", "|", "^":
		return true
	}
	return false
}

func punctOf(t *sqlTok) string {
	if t.kind == sqltext.KindPunct {
		return t.text
	}
	return ""
}

// space reports whether a space goes between code tokens p and i on one
// line. Operators and commas get one, parentheses hug their contents, and
// everywhere else the source's choice stands — never a space where the
// source had none between tokens that may mean something together (N'x',
// $(var), a.b, 1e+5 is joined at lexing).
func (l *sqlLayout) space(p, i int) bool {
	a, b := &l.t[p], &l.t[i]
	ap, bp := punctOf(a), punctOf(b)
	switch {
	case bp == "," || bp == ";" || bp == ")":
		return false
	case ap == "(":
		return false
	case ap == ",":
		return true
	case ap == ";", ap == ".", bp == ".", ap == "::", bp == "::", bp == "(":
		return b.sp
	case cmpOp(bp), cmpOp(ap), arithOp(bp):
		return true
	case arithOp(ap):
		// Binary, or SELECT *: spaced. Unary + or - keeps its operand as written.
		return l.binary(p) || ap == "*" || b.sp
	}
	return b.sp
}

// binary reports whether operator i follows a value, making it binary: x - 1,
// not SELECT -1 or (+2).
func (l *sqlLayout) binary(i int) bool {
	t := l.tok(l.prevC[i])
	switch t.kind {
	case sqltext.KindNumber, sqltext.KindString, sqltext.KindQuotedIdent:
		return true
	case sqltext.KindPunct:
		return t.text == ")"
	case sqltext.KindWord:
		return !sqlReserved[t.up] || t.up == "END" || t.up == "NULL"
	}
	return false
}
