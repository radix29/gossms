package sqlparse

import "strings"

// ---------------------------------------------------------------------------
// Batch bindings: temp tables and table variables
//
// #t and @t are the two names a script introduces itself rather than finding
// in the catalog, and they are the reason this file exists: nothing in the
// statement the cursor sits in says what they hold, so the declaration has to
// be found elsewhere in the batch. That is the one piece of cross-statement
// state the package keeps, and the caller pays for it only when a sigil is
// actually in play — see HasSigil.
//
// Scoped to the batch, not the whole script: a table variable dies at GO, and
// a batch is the largest span a declaration is certainly still in scope over.
// A temp table does outlive GO, so a "CREATE TABLE #t" in one batch and a
// "SELECT ... FROM #t" in the next answers with nothing rather than with the
// columns — the package's usual trade, an empty popup over a wrong one.
// ---------------------------------------------------------------------------

// Binding is one name a declaration puts in scope for the rest of the batch:
// a local or global temp table (#t, ##t) or a table variable (@t). Name
// carries the sigil, exactly as the tokenizer reports it, so it can never
// collide with a catalog object's name.
//
// Exactly one of Columns and Query is set: Columns for a declaration that
// spells the shape out (CREATE TABLE, DECLARE ... TABLE), Query for a
// "SELECT ... INTO #t" that takes it from the select list.
type Binding struct {
	Name    string
	Columns []BindingColumn
	Query   *Query
	Start   int // rune offset of the declared name
}

// BindingColumn is one column of a declared table, as written. The type is
// kept as text — this package knows nothing about T-SQL's type system, and
// rendering it is the caller's job.
type BindingColumn struct {
	Name string

	// Type is the declared type's name without its arguments ("nvarchar"), or
	// the last part of a qualified user type ("dbo.Money" -> "Money"). Empty
	// when the definition names no type at all — a computed column.
	Type string

	// TypeArgs are the arguments as written: ["50"], ["18", "2"], ["MAX"].
	TypeArgs []string

	// Nullable is false only where the definition says NOT NULL. A column
	// declaration is nullable by default, and guessing the other way would
	// make every column the scanner reads least about claim NOT NULL.
	Nullable bool
}

// HasSigil reports whether name is a temp table or variable name rather than
// something the catalog could hold. It is one half of the gate on the batch
// scan: the caller runs ScanBindings only once a name in play carries a
// sigil, so a script with no temp tables pays nothing per keystroke.
func HasSigil(name string) bool {
	return strings.HasPrefix(name, "#") || strings.HasPrefix(name, "@")
}

// ContainsSigil reports whether buf[from:] holds a '#' or '@' at all — the
// other half of the gate, for the one context where no name is in play yet
// and the answer still depends on the declarations: an empty prefix in a FROM
// clause, where every temp table in the batch should be offered. A raw rune
// scan, no lexer state and no allocation, so a script with no sigil anywhere
// is ruled out for a fraction of what the scan itself would cost. A sigil
// inside a comment or a literal gets through, and pays for one scan that
// finds nothing.
func ContainsSigil(buf []rune, from int) bool {
	for i := max(from, 0); i < len(buf); i++ {
		if buf[i] == '#' || buf[i] == '@' {
			return true
		}
	}
	return false
}

// ScanBindings collects every temp-table and table-variable declaration in
// tokens, which must span one batch. Anything whose shape does not match
// exactly is skipped rather than half-read: a name bound to nothing resolves
// to nothing, which is what the package promises for a shape it cannot parse.
func ScanBindings(tokens []Token) []Binding {
	var out []Binding
	starts := DMLStatementStarts(tokens)
	depth := 0
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
		if depth != 0 || t.Kind != TokenKeyword {
			continue
		}
		switch t.Text {
		case "CREATE":
			if b, next, ok := scanCreateTableBinding(tokens, i); ok {
				out = append(out, b)
				i = next - 1
			}
		case "DECLARE":
			if b, next, ok := scanDeclareTableBinding(tokens, i); ok {
				out = append(out, b)
				i = next - 1
			}
		case "INTO":
			if b, ok := scanSelectIntoBinding(tokens, starts, i); ok {
				out = append(out, b)
			}
		}
	}
	return out
}

// scanCreateTableBinding reads "CREATE TABLE #t ( ... )" at index i, returning
// the binding and the index just past the closing ')'. A CREATE TABLE naming
// an ordinary table is not a binding: the catalog is where that one's columns
// come from.
func scanCreateTableBinding(tokens []Token, i int) (Binding, int, bool) {
	if i+3 >= len(tokens) ||
		tokens[i+1].Kind != TokenKeyword || tokens[i+1].Text != "TABLE" ||
		tokens[i+2].Kind != TokenIdent || !strings.HasPrefix(tokens[i+2].Text, "#") {
		return Binding{}, i, false
	}
	cols, next, ok := parseTableColumnDefs(tokens, i+3)
	if !ok {
		return Binding{}, i, false
	}
	return Binding{Name: tokens[i+2].Text, Columns: cols, Start: tokens[i+2].Start}, next, true
}

// scanDeclareTableBinding reads "DECLARE @t [AS] TABLE ( ... )" at index i.
// A DECLARE of anything but a table variable — the overwhelmingly common
// case — fails the TABLE test and costs three index comparisons.
func scanDeclareTableBinding(tokens []Token, i int) (Binding, int, bool) {
	if i+1 >= len(tokens) || tokens[i+1].Kind != TokenIdent || !strings.HasPrefix(tokens[i+1].Text, "@") {
		return Binding{}, i, false
	}
	j := i + 2
	if j < len(tokens) && tokens[j].Kind == TokenKeyword && tokens[j].Text == "AS" {
		j++
	}
	if j >= len(tokens) || tokens[j].Kind != TokenKeyword || tokens[j].Text != "TABLE" {
		return Binding{}, i, false
	}
	cols, next, ok := parseTableColumnDefs(tokens, j+1)
	if !ok {
		return Binding{}, i, false
	}
	return Binding{Name: tokens[i+1].Text, Columns: cols, Start: tokens[i+1].Start}, next, true
}

// scanSelectIntoBinding reads the "INTO #t" of a "SELECT ... INTO #t FROM ..."
// at index i and binds the name to the statement that produces it.
//
// The statement is re-parsed here rather than reused from the cursor's own
// scope: the declaration is usually not the statement the cursor is in, which
// is the whole point. INTO is one of the parser's refIntroducers, so #t lands
// in the parsed query's own FROM list; it is dropped there, since the query's
// shape is what fills the table, not something the table contributes to it.
//
// An "INSERT INTO #t" is not a binding — the table already exists, and the
// insert says nothing about its columns — so only a statement led by SELECT or
// WITH counts.
func scanSelectIntoBinding(tokens []Token, starts []int, i int) (Binding, bool) {
	if i+1 >= len(tokens) || tokens[i+1].Kind != TokenIdent || !strings.HasPrefix(tokens[i+1].Text, "#") {
		return Binding{}, false
	}
	lo, hi := statementTokenRange(tokens, starts, tokens[i].Start)
	if lo >= hi || tokens[lo].Kind != TokenKeyword ||
		(tokens[lo].Text != "SELECT" && tokens[lo].Text != "WITH") {
		return Binding{}, false
	}
	name := tokens[i+1].Text
	p := &queryParser{toks: tokens[lo:hi]}
	q := p.parseChain()
	if q == nil {
		return Binding{}, false
	}
	q.From = dropRefNamed(q.From, name)
	return Binding{Name: name, Query: q, Start: tokens[i+1].Start}, true
}

// dropRefNamed removes the INTO target from a SELECT ... INTO's own FROM list.
func dropRefNamed(refs []FromRef, name string) []FromRef {
	out := refs[:0]
	for _, r := range refs {
		if r.Derived == nil && r.Schema == "" && strings.EqualFold(r.Name, name) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// statementTokenRange returns the token index range of the statement
// containing off, given the statement start offsets DMLStatementStarts
// reported. With no start at or before off the range begins at the first
// token, which is what a batch whose first statement has no recognised leader
// wants.
func statementTokenRange(tokens []Token, starts []int, off int) (lo, hi int) {
	loOff, hiOff := -1, -1
	for _, s := range starts {
		if s <= off {
			loOff = s
		} else {
			hiOff = s
			break // ascending order
		}
	}
	lo, hi = 0, len(tokens)
	for i, t := range tokens {
		if loOff >= 0 && t.Start <= loOff {
			lo = i
		}
		if hiOff >= 0 && t.Start >= hiOff {
			hi = i
			break
		}
	}
	return lo, hi
}

// tableConstraintLeaders are the words that open a table-level constraint
// rather than a column. The keyword-spelled ones (CONSTRAINT, PRIMARY,
// FOREIGN, CHECK, KEY, INDEX) are already excluded by the ident test in
// classifyColumnDef; UNIQUE and PERIOD are not in sqlKeywordList, so they are
// matched here by name.
var tableConstraintLeaders = map[string]bool{"UNIQUE": true, "PERIOD": true}

// parseTableColumnDefs parses the "( name type [...], ... )" list beginning at
// the '(' at index i, returning the columns and the index just past the
// matching ')'. It reports false for a group it never sees closed, or one
// holding no column definition at all.
func parseTableColumnDefs(tokens []Token, i int) ([]BindingColumn, int, bool) {
	if i >= len(tokens) || tokens[i].Kind != TokenParenOpen {
		return nil, i, false
	}
	var cols []BindingColumn
	var item []Token
	flush := func() {
		if c, ok := classifyColumnDef(item); ok {
			cols = append(cols, c)
		}
		item = item[:0]
	}
	depth := 1
	for i++; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case t.Kind == TokenParenOpen:
			depth++
			item = append(item, t)
		case t.Kind == TokenParenClose:
			if depth--; depth == 0 {
				flush()
				return cols, i + 1, len(cols) > 0
			}
			item = append(item, t)
		case t.Kind == TokenComma && depth == 1:
			flush()
		default:
			item = append(item, t)
		}
	}
	return nil, i, false
}

// classifyColumnDef reads one entry of a column-definition list. A table-level
// constraint, and anything else that does not open with an identifier followed
// by something, is not a column.
func classifyColumnDef(item []Token) (BindingColumn, bool) {
	if len(item) < 2 || item[0].Kind != TokenIdent || tableConstraintLeaders[strings.ToUpper(item[0].Text)] {
		return BindingColumn{}, false
	}
	col := BindingColumn{Name: item[0].Text, Nullable: true}
	// The type: a name, possibly schema-qualified, possibly with arguments.
	// "AS" instead means a computed column, whose type nothing here can work
	// out — it stays empty, and the caller renders it as an untyped column.
	j := 1
	if item[j].Kind == TokenIdent {
		col.Type = item[j].Text
		j++
		for j+1 < len(item) && item[j].Kind == TokenDot && item[j+1].Kind == TokenIdent {
			col.Type = item[j+1].Text
			j += 2
		}
		if j < len(item) && item[j].Kind == TokenParenOpen {
			col.TypeArgs, j = typeArgs(item, j)
		}
	}
	for k := j; k+1 < len(item); k++ {
		if item[k].Kind == TokenKeyword && item[k].Text == "NOT" &&
			item[k+1].Kind == TokenKeyword && item[k+1].Text == "NULL" {
			col.Nullable = false
			break
		}
	}
	return col, true
}

// typeArgs reads the "(50)" / "(18, 2)" / "(MAX)" after a type name, returning
// the arguments and the index just past the ')'. Anything else in the group
// yields no arguments, so an unrecognised type renders bare rather than with a
// length invented from a fragment.
func typeArgs(item []Token, i int) ([]string, int) {
	var args []string
	for i++; i < len(item); i++ {
		switch item[i].Kind {
		case TokenParenClose:
			return args, i + 1
		case TokenIdent:
			args = append(args, item[i].Text)
		case TokenComma:
		default:
			return nil, i + 1
		}
	}
	return nil, i
}
