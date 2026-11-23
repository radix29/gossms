package tui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

type completionFrame struct {
	tokens                      []sqlparse.Token
	start                       int
	window, predicate, subquery bool
	having                      bool
	aggregate, analytic         bool
}

type completionContext struct {
	frame                        completionFrame
	clause                       string
	clauseStart, selectStart     int
	queryDepth, caseDepth        int
	hasFrom, parenthesized, only bool
	scalar, condition, aggregate bool
	analytic                     bool
	apply                        bool
	datatype                     bool
	choices                      []string
}

// completionCaseArm is one open CASE in clauseScan: when is where the
// current searched WHEN's condition starts (-1 outside a condition).
type completionCaseArm struct {
	when     int
	searched bool
}

// analyzeCompletionContext classifies the cursor position at from. Its
// phases run in a fixed order, each either answering or passing on: the
// data-type positions (completionTypeChoices), the parenthesis frame walk
// (completionFrames), a parenthesized position's argument
// (parenthesizedPosition), an empty predicate subquery, the clause and CASE
// scan of the innermost frame (clauseScan), an open CASE arm (caseArmPosition)
// and finally the clause's own scalar/aggregate decision (classifyPosition).
//
// Keep the existing structural parser; inspect raw spans only where its
// reduced tokens omit expression operands, literals or operators.
func (p *QueryPanel) analyzeCompletionContext(buf []rune, tokens []sqlparse.Token, query *sqlparse.Query, from int) (c completionContext) {
	if choices, datatype, handled := p.completionTypeChoices(buf, tokens, from); handled {
		c.choices, c.datatype, c.only = choices, datatype, true
		return
	}
	start := 0
	if query != nil {
		start = query.Start
	}
	fabric := p.conn.IsFabricDW()
	stack, queryDepth := completionFrames(buf, tokens, start, from, fabric)
	c.frame, c.selectStart, c.queryDepth = stack[len(stack)-1], -1, queryDepth
	f := c.frame
	if len(stack) > 1 && !f.window && !f.predicate && !f.subquery && p.parenthesizedPosition(buf, stack, from, fabric, &c) {
		return
	}
	if f.subquery && len(f.tokens) == 0 {
		c.choices, c.only = []string{"SELECT"}, true
		if fabric {
			c.choices = append(c.choices, "FROM")
		}
		return
	}
	cases := c.clauseScan(buf)
	if c.caseArmPosition(buf, from, cases) || c.parenthesized {
		return
	}
	c.classifyPosition(buf, from, fabric)
	return
}

// completionTypeChoices answers the positions that take only a data type: a
// CREATE/ALTER PROCEDURE or FUNCTION header, then any other data-type
// position (dataTypeChoices). handled means the caller offers choices alone.
func (p *QueryPanel) completionTypeChoices(buf []rune, tokens []sqlparse.Token, from int) (choices []string, datatype, handled bool) {
	types := p.ddlTypes()
	action, objectType, tail := ddlCompletionTarget(tokens, from)
	if action != "DROP" && (objectType == "PROCEDURE" || objectType == "FUNCTION") {
		if choices, datatype, handled := ddlRoutineChoices(buf, tail, objectType, from, types); handled {
			return choices, datatype, true
		}
	}
	return dataTypeChoices(buf, tokens, from, types)
}

// completionFrames walks the tokens of the statement starting at start, up to
// from, into a stack of parenthesis frames; the last is the cursor's. Each
// frame carries whether it is a window spec, a predicate or predicate
// subquery, and whether aggregates and analytic functions are allowed in it,
// inherited from its parent and narrowed by the call or clause that opened
// it. queryDepth counts the parentheses open before start.
func completionFrames(buf []rune, tokens []sqlparse.Token, start, from int, fabric bool) (stack []completionFrame, queryDepth int) {
	stack = []completionFrame{{start: start}}
	for _, tok := range tokens {
		if tok.Start < start {
			switch tok.Kind {
			case sqlparse.TokenParenOpen:
				queryDepth++
			case sqlparse.TokenParenClose:
				queryDepth--
			}
			continue
		}
		if tok.Start >= from {
			break
		}
		f := &stack[len(stack)-1]
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			child := completionFrame{start: tok.Start + 1, predicate: f.predicate && len(f.tokens) == 0, aggregate: f.aggregate, analytic: f.analytic, having: f.having}
			child.subquery = predicateSubquery(buf, f.tokens, f.start, tok.Start)
			if len(f.tokens) > 0 {
				last := f.tokens[len(f.tokens)-1]
				child.window = keywordIs(last, "OVER")
				if len(f.tokens) >= 3 && keywordIs(last, "AS") &&
					f.tokens[len(f.tokens)-2].Kind == sqlparse.TokenIdent && keywordIs(f.tokens[len(f.tokens)-3], "WINDOW") {
					child.window = true
				}
				child.predicate = !last.Quoted && (predicateClause(strings.ToUpper(last.Text)) ||
					keywordIs(last, "AND") || keywordIs(last, "OR") || keywordIs(last, "NOT"))
				child.predicate = child.predicate &&
					keywordNextToken(buf[:tok.Start], last.Start+len([]rune(last.Text))).Kind == sqltext.KindEnd
				if !last.Quoted && (len(f.tokens) < 2 || f.tokens[len(f.tokens)-2].Kind != sqlparse.TokenDot) {
					kind := completionBuiltins[strings.ToUpper(last.Text)].kind
					windowed := false
					if kind&builtinWindow != 0 {
						end, ok := keywordCallEnd(buf, keywordNextToken(buf, tok.Start))
						windowed = ok && keywordNextIs(buf, end, "OVER")
					}
					child.aggregate = child.aggregate && !keywordIs(last, "TOP") && (kind&(builtinAggregate|builtinFabricAggregate) == 0 || windowed)
					child.analytic = child.analytic && !keywordIs(last, "TOP") && kind&(builtinAggregate|builtinFabricAggregate|builtinWindow) == 0
				}
				if child.window {
					child.analytic, child.having = false, false
				}
			}
			stack = append(stack, child)
		case sqlparse.TokenParenClose:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			stack[len(stack)-1].tokens = append(stack[len(stack)-1].tokens, tok)
		default:
			f.tokens = append(f.tokens, tok)
			if !tok.Quoted && !sqlparse.IsDistinctFrom(f.tokens, len(f.tokens)-1) {
				switch strings.ToUpper(tok.Text) {
				case "SELECT":
					f.having = false
					f.aggregate = true
					f.analytic = len(stack) == 1
				case "ORDER":
					f.having = false
					f.aggregate, f.analytic = len(stack) == 1 || f.window, len(stack) == 1
				case "PARTITION":
					f.aggregate, f.analytic = f.window, false
				case "HAVING":
					f.having = true
					f.aggregate, f.analytic = true, false
				case "QUALIFY":
					f.having = false
					f.aggregate, f.analytic = false, len(stack) == 1 && fabric
				case "FROM", "JOIN", "APPLY", "WHERE", "ON", "GROUP", "SET", "VALUES":
					f.having = false
					f.aggregate, f.analytic = false, false
				}
			}
		}
	}
	return stack, queryDepth
}

// parenthesizedPosition classifies a cursor inside a call's or expression's
// parentheses (not a window spec, predicate or predicate subquery): a JSON
// path argument and a DATEPART argument answer alone (done); otherwise c is
// marked parenthesized and scalar where the argument takes an expression.
func (p *QueryPanel) parenthesizedPosition(buf []rune, stack []completionFrame, from int, fabric bool, c *completionContext) (done bool) {
	f := c.frame
	parent := stack[len(stack)-2].tokens
	if len(parent) > 0 && (len(parent) < 2 || parent[len(parent)-2].Kind != sqlparse.TokenDot) {
		fn := parent[len(parent)-1]
		if keywordIs(fn, "JSON_VALUE") || keywordIs(fn, "JSON_QUERY") || keywordIs(fn, "JSON_MODIFY") {
			commas := 0
			for _, tok := range f.tokens {
				if tok.Kind == sqlparse.TokenComma {
					commas++
				}
			}
			if commas == 1 {
				c.only = true // Keep generic SQL suggestions out of the JSON path argument.
				return true
			}
		}
	}
	if parts, handled := datePartCompletionChoices(buf, parent, f.tokens, f.start, from); handled {
		c.choices, c.only, c.clause = parts, true, "DATEPART"
		return true
	}
	c.parenthesized = true
	expressionParent, expressionStart := parent, f.start
	for i := len(stack) - 2; len(expressionParent) == 0 && i > 0; i-- {
		expressionParent, expressionStart = stack[i-1].tokens, stack[i].start
	}
	c.scalar = scalarParenthesizedPosition(buf, expressionParent, f.tokens, expressionStart, from, fabric, p.sourceFunctionKnown(expressionParent))
	c.aggregate = c.scalar && f.aggregate
	c.analytic = c.scalar && f.analytic
	return false
}

// clauseScan reads the innermost frame's tokens for the clause the cursor is
// in (c.clause, c.clauseStart, c.selectStart, c.hasFrom, c.apply; a predicate
// frame starts as WHERE) and returns the CASE expressions still open at the
// cursor, outermost first. Clause keywords inside an open CASE are ignored.
func (c *completionContext) clauseScan(buf []rune) []completionCaseArm {
	f := c.frame
	if f.predicate {
		c.clause, c.clauseStart = "WHERE", f.start
	}
	var cases []completionCaseArm
	for i, tok := range f.tokens {
		if tok.Quoted || sqlparse.IsDistinctFrom(f.tokens, i) {
			continue
		}
		word := strings.ToUpper(tok.Text)
		switch word {
		case "CASE":
			cases = append(cases, completionCaseArm{-1, keywordNextIs(buf, tok.Start+len("CASE"), "WHEN")})
		case "END":
			if len(cases) > 0 {
				cases = cases[:len(cases)-1]
			}
		case "WHEN":
			if len(cases) > 0 && cases[len(cases)-1].searched {
				cases[len(cases)-1].when = tok.Start + len("WHEN")
			}
		case "THEN", "ELSE":
			if len(cases) > 0 {
				cases[len(cases)-1].when = -1
			}
		default:
			if len(cases) > 0 {
				continue
			}
			if tok.Kind == sqlparse.TokenComma && c.clause == "FROM" {
				c.apply = false
			}
			switch word {
			case "SELECT", "FROM", "JOIN", "WHERE", "ON", "GROUP", "HAVING", "QUALIFY", "ORDER", "PARTITION", "FOR", "SET":
				if word == "FOR" && (i >= 2 && keywordIs(f.tokens[i-1], "VALUE") && keywordIs(f.tokens[i-2], "NEXT") ||
					keywordNextIs(buf, tok.Start+len("FOR"), "SYSTEM_TIME")) {
					continue
				}
				c.clause, c.clauseStart = word, tok.Start+len(word)
				c.apply = false
				if word == "JOIN" && i > 0 && keywordIs(f.tokens[i-1], "CROSS") {
					c.clause = "FROM"
				}
				if word == "SELECT" {
					c.selectStart = i + 1
				}
				c.hasFrom = c.hasFrom || word == "FROM"
			case "APPLY":
				c.clause, c.clauseStart, c.apply = "FROM", tok.Start+len("APPLY"), true
			case "WINDOW":
				if i+2 < len(f.tokens) && f.tokens[i+1].Kind == sqlparse.TokenIdent && keywordIs(f.tokens[i+2], "AS") &&
					(i == 0 || f.tokens[i-1].Kind != sqlparse.TokenDot && !keywordIs(f.tokens[i-1], "FROM") && !keywordIs(f.tokens[i-1], "JOIN")) {
					c.clause, c.clauseStart = "WINDOW", tok.Start+len("WINDOW")
				}
			}
		}
	}
	return cases
}

// caseArmPosition applies the innermost open CASE: inside a searched WHEN's
// condition the clause becomes WHEN (a condition, even within parentheses);
// in any other arm the position is a scalar value unless one is already
// complete, and that answers (done).
func (c *completionContext) caseArmPosition(buf []rune, from int, cases []completionCaseArm) (done bool) {
	c.caseDepth = len(cases)
	if len(cases) == 0 {
		return false
	}
	f := c.frame
	if arm := cases[len(cases)-1]; arm.when >= 0 {
		c.clause, c.clauseStart = "WHEN", arm.when
		c.parenthesized = false
		return false
	}
	complete, _ := keywordExpressionComplete(buf, f.tokens[len(f.tokens)-1].Start, from, "VALUE")
	c.scalar = !complete
	c.aggregate = c.scalar && f.aggregate
	c.analytic = c.scalar && f.analytic
	return true
}

// classifyPosition is the per-clause decision for an unparenthesized cursor:
// AS and COLLATE take a name alone; otherwise whether a scalar (and an
// aggregate or analytic function) fits where the clause's expression is not
// yet complete, and a predicate clause's condition position.
func (c *completionContext) classifyPosition(buf []rune, from int, fabric bool) {
	f := c.frame
	if len(f.tokens) > 0 {
		last := f.tokens[len(f.tokens)-1]
		if keywordIs(last, "AS") || keywordIs(last, "COLLATE") {
			c.only = true
			return
		}
		expressionStart := last.Start
		if c.clause == "SELECT" && last.Kind == sqlparse.TokenStar {
			expressionStart = c.clauseStart
		}
		complete, _ := keywordExpressionComplete(buf, expressionStart, from, c.clause)
		switch c.clause {
		case "SELECT":
			c.scalar, c.aggregate = !complete && !keywordIs(last, "TOP"), !complete && !keywordIs(last, "TOP")
		case "GROUP", "ORDER", "PARTITION":
			c.scalar = !complete && (c.clause != "GROUP" || !fabric)
			c.aggregate = c.scalar && f.aggregate
		case "SET":
			c.scalar = scalarAssignmentPosition(buf, f.tokens, from)
		}
	}
	if predicateClause(c.clause) && (c.clause != "QUALIFY" || fabric) {
		c.scalar, c.condition = predicateExpressionPosition(buf, c.clauseStart, from)
		c.aggregate = c.scalar && (c.clause == "HAVING" || f.aggregate)
	}
	c.analytic = c.scalar && f.analytic
}

func (p *QueryPanel) sourceFunctionKnown(tokens []sqlparse.Token) bool {
	if len(tokens) == 0 {
		return false
	}
	name, schema := tokens[len(tokens)-1], ""
	if !name.Quoted && completionBuiltins[strings.ToUpper(name.Text)].kind&builtinTable != 0 {
		return true
	}
	if len(tokens) >= 3 && tokens[len(tokens)-2].Kind == sqlparse.TokenDot {
		schema = tokens[len(tokens)-3].Text
	}
	inv := p.currentInventory()
	sys := p.app.completion.sysInventories[sysCompletionInventoryKey(p.conn.Opts)]
	return findCatalogFunction(inv, sys, schema, name.Text) != nil
}

func completionStartsSource(tokens []sqlparse.Token, i int) bool {
	return keywordIs(tokens[i], "JOIN") || keywordIs(tokens[i], "APPLY") ||
		keywordIs(tokens[i], "FROM") && !sqlparse.IsDistinctFrom(tokens, i)
}

func completionEndsSource(tokens []sqlparse.Token, i int) bool {
	if tokens[i].Quoted {
		return false
	}
	switch strings.ToUpper(tokens[i].Text) {
	case "SELECT", "WHERE", "GROUP", "ORDER", "HAVING", "QUALIFY", "SET", "VALUES", "UNION", "EXCEPT", "INTERSECT":
		return true
	}
	return false
}

func caseCompletionItem(buf []rune, cursor int) editor.CompletionItem {
	item := editor.CompletionItem{
		Text: "CASE WHEN  THEN  END", Label: "CASE", Detail: "expression",
		CursorOffset: -len(" THEN  END"),
	}
	if when, ok := caseWhenAhead(buf, cursor); ok {
		item.Text, item.CursorOffset = "CASE", keywordNextToken(buf, when.End).Start-cursor
	}
	return item
}

func caseWhenAhead(buf []rune, cursor int) (sqltext.Token, bool) {
	depth, cases := 0, 0
	for tok := keywordNextToken(buf, cursor); tok.Kind != sqltext.KindEnd; tok = keywordNextToken(buf, tok.End) {
		switch tok.Kind {
		case sqltext.KindPunct:
			switch buf[tok.Start] {
			case '(':
				depth++
			case ')':
				if depth == 0 {
					return sqltext.Token{}, false
				}
				depth--
			case ',', ';':
				if depth == 0 && cases == 0 {
					return sqltext.Token{}, false
				}
			}
		case sqltext.KindWord:
			if depth != 0 {
				continue
			}
			switch strings.ToUpper(string(buf[tok.Start:tok.End])) {
			case "CASE":
				cases++
			case "END":
				if cases == 0 {
					return sqltext.Token{}, false
				}
				cases--
			case "WHEN":
				if cases == 0 {
					return tok, true
				}
			case "SELECT", "FROM", "WHERE", "GROUP", "HAVING", "ORDER", "QUALIFY", "UNION", "AS":
				return sqltext.Token{}, false
			}
		}
	}
	return sqltext.Token{}, false
}

// expressionCompletionItems offers the builtins cc's position takes. The
// caller gates them (builtinCompletionSupported) with everything else.
func expressionCompletionItems(buf []rune, cursor int, prefix string, cc completionContext, fabric bool, existing []editor.CompletionItem) []editor.CompletionItem {
	var items []editor.CompletionItem
	seen := make(map[string]bool, len(existing))
	for _, item := range existing {
		seen[item.Label] = true
	}
	add := func(label, detail string, window bool) {
		if seen[label] || !strings.HasPrefix(strings.ToUpper(label), strings.ToUpper(prefix)) {
			return
		}
		seen[label] = true
		items = append(items, completionCallItem(buf, cursor, label, detail, window))
	}
	if cc.scalar {
		for _, function := range scalarCompletions {
			add(strings.TrimSuffix(function, "("), "function", false)
		}
		if cc.aggregate {
			functions := aggregateCompletions
			if fabric {
				functions = fabricAggregateCompletions
			}
			for _, function := range functions {
				add(strings.TrimSuffix(function, "("), "function", false)
			}
		}
		if cc.analytic {
			for _, function := range windowCompletions {
				add(strings.TrimSuffix(function, "("), "function", true)
			}
		}
	}
	if cc.condition {
		for _, function := range predicateCompletions {
			add(strings.TrimSuffix(function, "("), "predicate", false)
		}
	}
	return items
}

func datePartCompletionChoices(buf []rune, parent, current []sqlparse.Token, start, from int) ([]string, bool) {
	if len(parent) == 0 || len(parent) >= 2 && parent[len(parent)-2].Kind == sqlparse.TokenDot ||
		slices.ContainsFunc(current, func(tok sqlparse.Token) bool { return tok.Kind == sqlparse.TokenComma }) {
		return nil, false
	}
	name := strings.ToUpper(parent[len(parent)-1].Text)
	if !slices.Contains([]string{"DATE_BUCKET", "DATETRUNC", "DATEADD", "DATEDIFF", "DATEDIFF_BIG", "DATENAME", "DATEPART"}, name) {
		return nil, false
	}
	if complete, _ := keywordExpressionComplete(buf, start, from, "VALUE"); complete {
		return nil, true
	}
	parts := []string{"YEAR", "QUARTER", "MONTH", "DAY", "WEEK", "HOUR", "MINUTE", "SECOND", "MILLISECOND"}
	switch name {
	case "DATETRUNC":
		parts = append(parts, "DAYOFYEAR", "ISO_WEEK", "MICROSECOND")
	case "DATEADD", "DATEDIFF", "DATEDIFF_BIG", "DATENAME", "DATEPART":
		parts = append(parts, "DAYOFYEAR", "WEEKDAY", "MICROSECOND", "NANOSECOND")
		if name == "DATENAME" || name == "DATEPART" {
			parts = append(parts, "ISO_WEEK", "TZOFFSET")
		}
	}
	return parts, true
}

func reuseDistinctCompletionTail(item editor.CompletionItem, buf []rune, cursor int) editor.CompletionItem {
	text, end := completionPhrasePrefix(buf, cursor, item.Label)
	if end != cursor {
		item.Text = text
		item.CursorOffset = keywordNextToken(buf, end).Start - cursor
		if cursor < len(buf) && buf[cursor] == ' ' {
			item.CursorOffset--
		}
	}
	return item
}

func completionPhrasePrefix(buf []rune, cursor int, label string) (text string, end int) {
	words := strings.Fields(label)
	for i := 1; i < len(words); i++ {
		if keywordNextIs(buf, cursor, strings.Join(words[i:], " ")) {
			end = cursor
			for range words[i:] {
				end = keywordNextToken(buf, end).End
			}
			return strings.Join(words[:i], " "), end
		}
	}
	return label, cursor
}

func scalarParenthesizedPosition(buf []rune, parent, current []sqlparse.Token, start, from int, fabric, sourceFunction bool) bool {
	if len(parent) == 0 {
		return false
	}
	if complete, _ := keywordExpressionComplete(buf, start, from, "VALUE"); complete {
		return false
	}
	source, fromList, grouping, insertTarget := false, false, false, false
	for i, tok := range parent {
		if sqlparse.IsDistinctFrom(parent, i) {
			continue
		}
		if completionEndsSource(parent, i) {
			source, fromList = false, false
		}
		switch {
		case keywordIs(tok, "EXEC"), keywordIs(tok, "EXECUTE"):
			return false
		case keywordIs(tok, "TABLE"):
			return false
		case keywordIs(tok, "INSERT"):
			insertTarget = true
		case keywordIs(tok, "VALUES"):
			insertTarget = false
		case completionStartsSource(parent, i):
			source, fromList = true, true
			grouping = false
		case fromList && tok.Kind == sqlparse.TokenComma:
			source = true
		case !tok.Quoted && predicateClause(tok.Text), keywordIs(tok, "SELECT"), keywordIs(tok, "SET"):
			source = false
			insertTarget = false
			grouping = false
		case keywordIs(tok, "GROUP"):
			grouping = true
		case keywordIs(tok, "ORDER"):
			grouping = false
		}
	}
	if insertTarget || fabric && grouping {
		return false
	}
	last := parent[len(parent)-1]
	if keywordIs(last, "WITH") || keywordIs(last, "TABLESAMPLE") ||
		keywordIs(last, "CONTAINS") || keywordIs(last, "FREETEXT") {
		return false
	}
	// Operators and literals are absent from parent: distinguish an argument
	// list from an arithmetic group by checking the gap before '('.
	next := keywordNextToken(buf[:start-1], keywordNextToken(buf[:start-1], last.Start).End)
	call := next.Kind == sqltext.KindEnd && (last.Kind == sqlparse.TokenIdent || last.Kind == sqlparse.TokenKeyword)
	if !call {
		return !source
	}
	name := strings.ToUpper(last.Text)
	if name == "INDEX" {
		return false
	}
	if name == "CAST" || name == "TRY_CAST" {
		if slices.ContainsFunc(current, func(tok sqlparse.Token) bool { return keywordIs(tok, "AS") }) {
			return false
		}
	}
	comma := slices.ContainsFunc(current, func(tok sqlparse.Token) bool { return tok.Kind == sqlparse.TokenComma })
	if !comma {
		switch name {
		case "CONVERT", "TRY_CONVERT", "DATEADD", "DATEDIFF", "DATEDIFF_BIG", "DATENAME", "DATEPART", "DATE_BUCKET", "DATETRUNC":
			return false
		}
	}
	if !last.Quoted && (name == "TOP" || name == "VALUES" || name == "IN" || name == "ROLLUP" || name == "CUBE") {
		return true
	}
	return !source || sourceFunction
}

// builtinCompletionSupported is the one version and edition gate for
// completion items: a label with no row in builtinDefinitions passes. A
// datatype position looks the label up among the data types instead.
func (p *QueryPanel) builtinCompletionSupported(label string, datatype bool) bool {
	builtin := completionBuiltins[label]
	if datatype {
		builtin = completionDataTypes[label]
	}
	fabric := p.conn.IsFabricDW()
	if fabric && builtin.kind&builtinNoWarehouse != 0 || !fabric && builtin.kind&builtinWarehouseOnly != 0 {
		return false
	}
	if builtin.since == 0 || p.conn.Server == nil {
		return true
	}
	return p.conn.Server.Info().AtLeast(builtin.since)
}

func scalarAssignmentPosition(buf []rune, tokens []sqlparse.Token, from int) bool {
	set := -1
	for _, tok := range tokens {
		if keywordIs(tok, "SET") {
			set = tok.Start + len("SET")
		}
	}
	if set < 0 {
		return false
	}
	value, complete := assignmentValue(buf, set, from)
	return value && !complete
}

// assignmentValue tracks the last SET assignment, excluding commas in calls
// and CASE expressions. OUTPUT belongs to the clause only after a value, not
// where the assignment expects a column (including a qualified member).
func assignmentValue(buf []rune, set, from int) (value, complete bool) {
	depth, cases, valueStart := 0, 0, -1
	var last sqltext.Token
	target := false
	for pos := set; pos < from; {
		tok, _ := sqltext.Next(buf, pos, from, sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		pos = tok.End
		if tok.Kind == sqltext.KindComment || tok.Kind == sqltext.KindNewline {
			continue
		}
		if tok.Kind == sqltext.KindWord {
			switch strings.ToUpper(string(buf[tok.Start:tok.End])) {
			case "WRITE":
				if depth == 0 && cases == 0 && target && valueStart < 0 &&
					last.Kind == sqltext.KindPunct && buf[last.Start] == '.' {
					open := keywordNextToken(buf[:from], tok.End)
					if open.Kind == sqltext.KindPunct && buf[open.Start] == '(' {
						valueStart = open.End
					}
				}
			case "CASE":
				cases++
			case "END":
				if cases > 0 {
					cases--
				}
			case "OUTPUT":
				if depth == 0 && cases == 0 && valueStart >= 0 && last.End > valueStart &&
					keywordExpressionTailComplete(buf, last, "VALUE") {
					return false, false
				}
			}
		}
		if depth == 0 && cases == 0 && valueStart < 0 && (tok.Kind == sqltext.KindWord || tok.Kind == sqltext.KindQuotedIdent) {
			target = true
		}
		if tok.Kind == sqltext.KindPunct {
			switch buf[tok.Start] {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 && cases == 0 {
					valueStart, target = -1, false
				}
			case '=':
				if depth == 0 && cases == 0 && target && valueStart < 0 {
					valueStart = tok.End
				}
			}
		}
		last = tok
	}
	return valueStart >= 0, valueStart >= 0 && depth == 0 && cases == 0 && last.End > valueStart &&
		keywordExpressionTailComplete(buf, last, "VALUE")
}

// sqlparse omits operators and string literals. Inspect the short tail with
// the shared lexer so "WHERE Name = ord|" differs from "Name = 'x' ord|".
func keywordExpressionComplete(buf []rune, start, end int, clause string) (bool, sqltext.Token) {
	var last, previous sqltext.Token
	for i := start; i < end; {
		tok, _ := sqltext.Next(buf, i, end, sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		i = tok.End
		if tok.Kind != sqltext.KindComment && tok.Kind != sqltext.KindNewline {
			previous, last = last, tok
		}
	}
	complete := keywordExpressionTailComplete(buf, last, clause)
	if complete && clause == "SELECT" && last.Kind == sqltext.KindPunct && buf[last.Start] == '*' {
		complete = previous.Kind == sqltext.KindEnd ||
			previous.Kind == sqltext.KindPunct && (buf[previous.Start] == '.' || buf[previous.Start] == ',') ||
			previous.Kind == sqltext.KindWord && slices.Contains([]string{"SELECT", "DISTINCT", "ALL"}, strings.ToUpper(string(buf[previous.Start:previous.End])))
		top := keywordNextToken(buf, start)
		if top.Kind == sqltext.KindWord && (strings.EqualFold(string(buf[top.Start:top.End]), "DISTINCT") ||
			strings.EqualFold(string(buf[top.Start:top.End]), "ALL")) {
			top = keywordNextToken(buf, top.End)
		}
		if !complete && top.Kind == sqltext.KindWord && strings.EqualFold(string(buf[top.Start:top.End]), "TOP") {
			arg := keywordNextToken(buf, top.End)
			end := arg.End
			if arg.Kind == sqltext.KindPunct && buf[arg.Start] == '(' {
				end, _ = keywordCallEnd(buf, arg)
			}
			if keywordNextIs(buf, end, "PERCENT") {
				end = keywordNextToken(buf, end).End
			}
			if keywordNextIs(buf, end, "WITH TIES") {
				end = keywordNextToken(buf, keywordNextToken(buf, end).End).End
			}
			complete = previous.End == end
		}
	}
	return complete, last
}

func keywordExpressionTailComplete(buf []rune, last sqltext.Token, clause string) bool {
	switch last.Kind {
	case sqltext.KindNumber, sqltext.KindString, sqltext.KindQuotedIdent:
		return true
	case sqltext.KindPunct:
		return buf[last.Start] == ')' || clause == "SELECT" && buf[last.Start] == '*'
	case sqltext.KindWord:
		switch strings.ToUpper(string(buf[last.Start:last.End])) {
		case "SELECT", "FROM", "JOIN", "APPLY", "WHERE", "ON", "BY", "HAVING", "QUALIFY", "FOR", "AND", "OR", "NOT",
			"AS", "IS", "LIKE", "ESCAPE", "IN", "BETWEEN", "DISTINCT", "TOP", "WHEN", "THEN", "ELSE", "WITH",
			"OVER", "PARTITION", "ORDER", "GROUP", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "OUTER", "ASC", "DESC":
			return false
		}
		return true
	}
	return false
}

func keywordWhitespace(r rune) bool {
	return strings.ContainsRune(" \t\r\n", r) || r > unicode.MaxASCII && unicode.IsSpace(r)
}

func keywordNextIs(buf []rune, cursor int, word string) bool {
	for _, part := range strings.Fields(word) {
		tok := keywordNextToken(buf, cursor)
		if (tok.Kind != sqltext.KindWord && tok.Kind != sqltext.KindPunct) || !strings.EqualFold(string(buf[tok.Start:tok.End]), part) {
			return false
		}
		cursor = tok.End
	}
	return true
}

func keywordNextToken(buf []rune, cursor int) sqltext.Token {
	for cursor < len(buf) {
		tok, _ := sqltext.Next(buf, cursor, len(buf), sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			return tok
		}
		cursor = tok.End
		if tok.Kind != sqltext.KindComment && tok.Kind != sqltext.KindNewline {
			return tok
		}
	}
	return sqltext.Token{Kind: sqltext.KindEnd, Start: cursor, End: cursor}
}
