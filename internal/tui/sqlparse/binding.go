package sqlparse

import (
	"slices"
	"strings"
)

// ---------------------------------------------------------------------------
// Batch bindings: temp tables and table variables
//
// #t and @t are the two names a script introduces itself rather than finding in
// the catalog: nothing in the statement at the cursor says what they hold, so
// the declaration has to be found elsewhere in the batch. That is the one piece
// of cross-statement state the package keeps, and the caller pays for it only
// when a sigil is in play (see HasSigil).
//
// ScanBindings is scoped to the batch: a table variable dies at GO. A temp
// table lives until the session drops it, so CarryTempBindings walks the
// batches above the cursor's in script order and hands their temp tables on.
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

	// Nullable is false only where the definition says NOT NULL. A column is
	// nullable by default, and guessing the other way would make every column the
	// scanner reads least about claim NOT NULL.
	Nullable bool
}

// HasSigil reports whether name is a temp table or variable name rather than
// something the catalog could hold. It is one half of the gate on the batch
// scan: the caller runs ScanBindings only once a name in play carries a
// sigil, so a script with no temp tables pays nothing per keystroke.
func HasSigil(name string) bool {
	return strings.HasPrefix(name, "#") || strings.HasPrefix(name, "@")
}

// ContainsSigil reports whether buf[from:] holds a '#' or '@' at all: the other
// half of the gate, for the one context where no name is in play yet and the
// answer still depends on the declarations (an empty prefix in a FROM clause,
// where every temp table in the batch should be offered). A raw rune scan, no
// lexer state or allocation, so a script with no sigil is ruled out for a
// fraction of the scan's cost. A sigil inside a comment or literal gets through
// and pays for one scan that finds nothing.
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
// The statement is re-parsed here rather than reused from the cursor's scope:
// the declaration is usually not the statement the cursor is in. INTO is one of
// the parser's refIntroducers, so #t lands in the parsed query's own FROM list;
// it is dropped there, since the query's shape fills the table, not the other
// way round.
//
// "INSERT INTO #t" is not a binding (the table exists and the insert says
// nothing about its columns), so only a statement led by SELECT or WITH counts.
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
// rather than a column. The keyword-spelled ones (CONSTRAINT, PRIMARY, FOREIGN,
// CHECK, KEY, INDEX) are already excluded by the ident test in
// classifyColumnDef; UNIQUE and PERIOD are not in sqlKeywordList, so they are
// matched by name.
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

// ---------------------------------------------------------------------------
// Temp tables across GO
// ---------------------------------------------------------------------------

// maxCarryDepth bounds the walk of a carried SELECT ... INTO's query tree,
// the same order of nesting relation resolution gives up at.
const maxCarryDepth = 16

// CarryTempBindings returns the temp tables still declared after batch, given
// the ones carried into it: batch's own temp-table declarations and "DROP TABLE
// #t"s applied in script order, a redeclared name replacing the earlier entry.
// carried is not modified. Called once per batch above the cursor's, top down,
// it yields what the session holds when the cursor's batch starts; the caller
// puts that before the cursor's own bindings, so a local declaration shadows a
// carried one (findBinding takes the last).
//
// Left behind, so the name answers nothing rather than a wrong list:
//
//   - table variables, which die at GO;
//   - every declaration in a batch that defines a procedure, function or
//     trigger: a temp table created in a module body is gone when it returns;
//   - a SELECT ... INTO whose query reads a table variable: its shape would be
//     resolved against the cursor's batch, where that @name is someone else
//     or no one.
//
// A DROP in the cursor's own batch is not applied to what is carried in; the
// batch's own declarations are not ordered against the cursor either.
func CarryTempBindings(carried []Binding, batch []Token) []Binding {
	if definesModule(batch) {
		return carried
	}
	out := slices.Clone(carried)
	remove := func(name string) {
		out = slices.DeleteFunc(out, func(b Binding) bool { return strings.EqualFold(b.Name, name) })
	}
	bindings := ScanBindings(batch)
	drops := scanTempDrops(batch)
	for len(bindings) > 0 || len(drops) > 0 {
		if len(drops) == 0 || (len(bindings) > 0 && bindings[0].Start < drops[0].Start) {
			b := bindings[0]
			bindings = bindings[1:]
			if !strings.HasPrefix(b.Name, "#") || queryReadsVariable(b.Query, 0) {
				continue
			}
			remove(b.Name)
			out = append(out, b)
			continue
		}
		remove(drops[0].Text)
		drops = drops[1:]
	}
	return out
}

// definesModule reports whether batch opens with CREATE [OR ALTER] or ALTER of
// a procedure, function or trigger — which T-SQL requires to be the batch's
// first statement, so the first tokens are the whole test.
func definesModule(batch []Token) bool {
	i := 0
	switch {
	case len(batch) > 3 && isKeyword(batch[0], "CREATE") && isKeyword(batch[1], "OR") && isKeyword(batch[2], "ALTER"):
		i = 3
	case len(batch) > 1 && (isKeyword(batch[0], "CREATE") || isKeyword(batch[0], "ALTER")):
		i = 1
	default:
		return false
	}
	switch strings.ToUpper(batch[i].Text) {
	case "PROC", "PROCEDURE", "FUNCTION", "TRIGGER":
		return true
	}
	return false
}

func isKeyword(t Token, text string) bool { return t.Kind == TokenKeyword && t.Text == text }

// scanTempDrops returns the temp-table names of every "DROP TABLE [IF EXISTS]
// a, b, ..." in tokens, each as the token naming it. A qualified name keeps
// only its last part, which is where a temp table's sigil is ("tempdb..#t").
func scanTempDrops(tokens []Token) []Token {
	var out []Token
	for i := 0; i+2 < len(tokens); i++ {
		if !isKeyword(tokens[i], "DROP") || !isKeyword(tokens[i+1], "TABLE") {
			continue
		}
		j := i + 2
		if j+1 < len(tokens) && tokens[j].Kind == TokenIdent && strings.EqualFold(tokens[j].Text, "IF") &&
			isKeyword(tokens[j+1], "EXISTS") {
			j += 2
		}
		for j < len(tokens) && tokens[j].Kind == TokenIdent {
			last := tokens[j]
			// "a.b.c" and "tempdb..#t": dots, each followed by a part or not.
			for j++; j < len(tokens) && tokens[j].Kind == TokenDot; j++ {
				if j+1 < len(tokens) && tokens[j+1].Kind == TokenIdent {
					j++
					last = tokens[j]
				}
			}
			if strings.HasPrefix(last.Text, "#") {
				out = append(out, last)
			}
			if j >= len(tokens) || tokens[j].Kind != TokenComma {
				break
			}
			j++
		}
		i = j - 1
	}
	return out
}

// queryReadsVariable reports whether q, or any query nested in it, reads a
// table variable.
func queryReadsVariable(q *Query, depth int) bool {
	if q == nil {
		return false
	}
	if depth >= maxCarryDepth {
		return true // too deep to vouch for: carry nothing
	}
	for _, r := range q.From {
		if r.Derived == nil && r.Schema == "" && strings.HasPrefix(r.Name, "@") {
			return true
		}
		if queryReadsVariable(r.Derived, depth+1) {
			return true
		}
	}
	for _, sub := range q.Subqueries {
		if queryReadsVariable(sub, depth+1) {
			return true
		}
	}
	for _, cte := range q.CTEs {
		if queryReadsVariable(cte.Body, depth+1) {
			return true
		}
	}
	return false
}
