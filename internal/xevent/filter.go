package xevent

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Filter is the viewer's client-side filter: SSMS's Filters dialog as one
// line of text. It narrows what the grid shows, never what the session
// collects — the session's predicate is the Properties dialog's.
//
// Two forms:
//
//   - an expression — terms of the form `column op value`, joined by AND and
//     OR, AND binding tighter (duration > 1000000 and database_name = 'app');
//   - anything else is free text, matched case-insensitively against every
//     value the event carries (the Log File Viewer's filter).
//
// A column is resolved against the event itself: name, timestamp and package
// are the built-ins; any other name is a field, and failing that an action of
// that name. field:x and action:x name one kind explicitly. An event without the column is NULL to every operator but
// IS NULL — SQL's rule, and the one SSMS's dialog follows.
type Filter struct {
	text   string
	groups [][]term // OR of ANDs; nil for free text
	needle string   // free text, lowercased
}

// Op is a comparison operator.
type Op int

const (
	OpEq Op = iota
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpContains
	OpNotContains
	OpStartsWith
	OpIsNull
	OpIsNotNull
)

type term struct {
	column string
	op     Op
	value  string
}

// ParseFilter parses s. An empty s is a nil filter, which matches everything.
// Text that is not a valid expression is free text — unless it uses one of the
// comparison symbols (= < > ! ~), which only an expression would, and then
// the parse error is returned: someone who typed `duration >` meant a
// filter, and matching the literal text would silently show nothing.
func ParseFilter(s string) (*Filter, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	groups, err := parseExpr(s)
	if err == nil {
		return &Filter{text: s, groups: groups}, nil
	}
	if strings.ContainsAny(s, "=<>!~") {
		return nil, err
	}
	return &Filter{text: s, needle: strings.ToLower(s)}, nil
}

// String is the text the filter was parsed from.
func (f *Filter) String() string {
	if f == nil {
		return ""
	}
	return f.text
}

// IsFreeText reports whether f is a plain substring search.
func (f *Filter) IsFreeText() bool { return f != nil && f.groups == nil }

// Match reports whether e passes f. A nil filter passes everything.
func (f *Filter) Match(e *Event) bool {
	if f == nil {
		return true
	}
	if f.groups == nil {
		return matchesText(e, f.needle)
	}
	for _, and := range f.groups {
		ok := true
		for _, t := range and {
			if !t.match(e) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func matchesText(e *Event, needle string) bool {
	has := func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }
	if has(e.Name) {
		return true
	}
	for _, vs := range [][]Value{e.Fields, e.Actions} {
		for _, v := range vs {
			if has(v.Value) || (v.Text != "" && has(v.Text)) {
				return true
			}
		}
	}
	return false
}

// resolve finds column name in e: a built-in, a field, then an action. A
// field: or action: prefix names one kind only — how a field called timestamp,
// or an action sharing a field's name, is reached at all.
func resolve(e *Event, name string) (Value, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "field:"):
		return e.Field(name[len("field:"):])
	case strings.HasPrefix(lower, "action:"):
		return e.Action(name[len("action:"):])
	}
	switch lower {
	case "name":
		return e.Value(NameColumn)
	case "timestamp":
		return e.Value(TimestampColumn)
	case "package":
		return e.Value(PackageColumn)
	}
	if v, ok := e.Field(name); ok {
		return v, true
	}
	return e.Action(name)
}

func (t term) match(e *Event) bool {
	v, ok := resolve(e, t.column)
	switch t.op {
	case OpIsNull:
		return !ok
	case OpIsNotNull:
		return ok
	}
	if !ok {
		return false
	}
	// A map field matches on either its text or its key, so wait_type =
	// PAGEIOLATCH_SH and wait_type = 66 both find it.
	candidates := []string{v.Display()}
	if v.Text != "" && v.Text != v.Value {
		candidates = append(candidates, v.Value)
	}
	for _, c := range candidates {
		if compare(c, t.op, t.value) {
			return true
		}
	}
	return false
}

// compare applies op to a (the event's value) and b (the filter's): as
// numbers when both are, else as case-insensitive text.
func compare(a string, op Op, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	switch op {
	case OpContains:
		return strings.Contains(la, lb)
	case OpNotContains:
		return !strings.Contains(la, lb)
	case OpStartsWith:
		return strings.HasPrefix(la, lb)
	}
	var c int
	if na, errA := strconv.ParseFloat(strings.TrimSpace(a), 64); errA == nil {
		if nb, errB := strconv.ParseFloat(strings.TrimSpace(b), 64); errB == nil {
			switch {
			case na < nb:
				c = -1
			case na > nb:
				c = 1
			}
			return cmpResult(c, op)
		}
	}
	return cmpResult(strings.Compare(la, lb), op)
}

func cmpResult(c int, op Op) bool {
	switch op {
	case OpEq:
		return c == 0
	case OpNe:
		return c != 0
	case OpLt:
		return c < 0
	case OpLe:
		return c <= 0
	case OpGt:
		return c > 0
	case OpGe:
		return c >= 0
	}
	return false
}

// Term is one `column op value` comparison, for a caller building a filter
// rather than parsing one. Value is ignored by OpIsNull and OpIsNotNull.
type Term struct {
	Column string
	Op     Op
	Value  string
}

// AndEquals returns a filter passing what f passes and also has column equal
// to value — AndTerms with the one term.
func (f *Filter) AndEquals(column, value string) *Filter {
	return f.AndTerms(Term{Column: column, Op: OpEq, Value: value})
}

// AndTerms returns a filter passing what f passes and every one of terms:
// f's every OR branch with the terms ANDed on, since the syntax has no
// parentheses to write "(f) AND terms". A nil or free-text f gives the terms
// alone. f itself is not changed.
func (f *Filter) AndTerms(terms ...Term) *Filter {
	ts := make([]term, len(terms))
	for i, t := range terms {
		ts[i] = term{column: t.Column, op: t.Op, value: t.Value}
		if t.Op == OpIsNull || t.Op == OpIsNotNull {
			ts[i].value = ""
		}
	}
	if f == nil || f.groups == nil {
		g := [][]term{ts}
		return &Filter{text: groupsText(g), groups: g}
	}
	g := make([][]term, len(f.groups))
	for i, and := range f.groups {
		g[i] = append(append([]term(nil), and...), ts...)
	}
	return &Filter{text: groupsText(g), groups: g}
}

var opText = map[Op]string{
	OpEq: "=", OpNe: "<>", OpLt: "<", OpLe: "<=", OpGt: ">", OpGe: ">=",
	OpContains: "contains", OpNotContains: "not contains", OpStartsWith: "starts with",
	OpIsNull: "is null", OpIsNotNull: "is not null",
}

// groupsText writes groups back as an expression ParseFilter reads the same.
func groupsText(groups [][]term) string {
	ors := make([]string, len(groups))
	for i, and := range groups {
		parts := make([]string, len(and))
		for j, t := range and {
			parts[j] = t.column + " " + opText[t.op]
			if t.op != OpIsNull && t.op != OpIsNotNull {
				parts[j] += " " + Quote(t.value)
			}
		}
		ors[i] = strings.Join(parts, " and ")
	}
	return strings.Join(ors, " or ")
}

// -- Parsing -------------------------------------------------------------------

type tokKind int

const (
	tokWord tokKind = iota // a bare word: a column, a keyword or an unquoted value
	tokString
	tokSymbol
)

type token struct {
	kind tokKind
	text string
}

var symbols = []string{"<>", "!=", "<=", ">=", "!~", "=", "<", ">", "~"}

func tokenize(s string) ([]token, error) {
	var out []token
	for i := 0; i < len(s); {
		// Decoded, not rune(s[i]): a UTF-8 continuation byte such as 0x85 or
		// 0xA0 would otherwise read as a space and split a word.
		r, _ := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '\'' || r == '"':
			var b strings.Builder
			j := i + 1
			closed := false
			for j < len(s) {
				if s[j] == byte(r) {
					// A doubled quote is a literal one, as in T-SQL.
					if j+1 < len(s) && s[j+1] == byte(r) {
						b.WriteByte(byte(r))
						j += 2
						continue
					}
					closed = true
					j++
					break
				}
				b.WriteByte(s[j])
				j++
			}
			if !closed {
				return nil, errors.New("unterminated quoted value")
			}
			out = append(out, token{tokString, b.String()})
			i = j
		default:
			matched := false
			for _, sym := range symbols {
				if strings.HasPrefix(s[i:], sym) {
					out = append(out, token{tokSymbol, sym})
					i += len(sym)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			j := i
			for j < len(s) {
				c, size := utf8.DecodeRuneInString(s[j:])
				if unicode.IsSpace(c) || strings.ContainsRune("=<>!~'\"", c) {
					break
				}
				j += size
			}
			out = append(out, token{tokWord, s[i:j]})
			i = j
		}
	}
	return out, nil
}

var symbolOps = map[string]Op{
	"=": OpEq, "<>": OpNe, "!=": OpNe, "<": OpLt, "<=": OpLe, ">": OpGt, ">=": OpGe,
	"~": OpContains, "!~": OpNotContains,
}

func isKeyword(t token, kw string) bool { return t.kind == tokWord && strings.EqualFold(t.text, kw) }

func parseExpr(s string) ([][]term, error) {
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	var groups [][]term
	var and []term
	for {
		t, err := p.term()
		if err != nil {
			return nil, err
		}
		and = append(and, t)
		if p.done() {
			break
		}
		switch next := p.next(); {
		case isKeyword(next, "and"):
		case isKeyword(next, "or"):
			groups = append(groups, and)
			and = nil
		default:
			return nil, fmt.Errorf("expected AND or OR, found %q", next.text)
		}
	}
	return append(groups, and), nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) done() bool { return p.pos >= len(p.toks) }

func (p *parser) next() token {
	t := p.toks[p.pos]
	p.pos++
	return t
}

func (p *parser) peek() (token, bool) {
	if p.done() {
		return token{}, false
	}
	return p.toks[p.pos], true
}

// term parses `column op value`, `column IS [NOT] NULL`, `column [NOT]
// CONTAINS value` or `column STARTS WITH value`.
func (p *parser) term() (term, error) {
	if p.done() {
		return term{}, errors.New("expected a column name")
	}
	col := p.next()
	if col.kind != tokWord || isKeyword(col, "and") || isKeyword(col, "or") {
		return term{}, fmt.Errorf("expected a column name, found %q", col.text)
	}
	t := term{column: col.text}
	if p.done() {
		return term{}, fmt.Errorf("expected an operator after %q", col.text)
	}
	opTok := p.next()
	switch {
	case opTok.kind == tokSymbol:
		t.op = symbolOps[opTok.text]
	case isKeyword(opTok, "is"):
		t.op = OpIsNull
		if n, ok := p.peek(); ok && isKeyword(n, "not") {
			p.next()
			t.op = OpIsNotNull
		}
		if n, ok := p.peek(); !ok || !isKeyword(n, "null") {
			return term{}, errors.New("expected NULL after IS")
		}
		p.next()
		return t, nil
	case isKeyword(opTok, "contains"):
		t.op = OpContains
	case isKeyword(opTok, "not"):
		if n, ok := p.peek(); !ok || !isKeyword(n, "contains") {
			return term{}, errors.New("expected CONTAINS after NOT")
		}
		p.next()
		t.op = OpNotContains
	case isKeyword(opTok, "starts"):
		if n, ok := p.peek(); !ok || !isKeyword(n, "with") {
			return term{}, errors.New("expected WITH after STARTS")
		}
		p.next()
		t.op = OpStartsWith
	default:
		return term{}, fmt.Errorf("expected an operator after %q, found %q", col.text, opTok.text)
	}
	if p.done() {
		return term{}, fmt.Errorf("expected a value after %q", opTok.text)
	}
	v := p.next()
	if v.kind == tokSymbol {
		return term{}, fmt.Errorf("expected a value, found %q", v.text)
	}
	t.value = v.text
	return t, nil
}

// Quote renders value as a filter literal, quoted when it has to be — what
// "Filter by this Value" appends to the expression.
func Quote(value string) string {
	if value != "" && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("=<>!~'\"", r)
	}) && !strings.EqualFold(value, "and") && !strings.EqualFold(value, "or") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// ContainsText reports whether any value e carries — its name, a field's or
// an action's value or map text — contains s, case-insensitively: the
// viewer's Find, which is the free-text filter's match without the filtering.
func (e *Event) ContainsText(s string) bool { return matchesText(e, strings.ToLower(s)) }
