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

// Keep the existing structural parser; inspect raw spans only where its
// reduced tokens omit expression operands, literals or operators.
func (p *QueryPanel) analyzeCompletionContext(buf []rune, tokens []sqlparse.Token, query *sqlparse.Query, from int) (c completionContext) {
	action, objectType, tail := ddlCompletionTarget(tokens, from)
	if action != "DROP" && (objectType == "PROCEDURE" || objectType == "FUNCTION") {
		if choices, datatype, handled := ddlRoutineChoices(buf, tail, objectType, from, p.conn.IsFabricDW()); handled {
			c.choices, c.datatype, c.only = choices, datatype, true
			return
		}
	}
	start := 0
	if query != nil {
		start = query.Start
	}
	stack := []completionFrame{{start: start}}
	for _, tok := range tokens {
		if tok.Start < start {
			if tok.Kind == sqlparse.TokenParenOpen {
				c.queryDepth++
			} else if tok.Kind == sqlparse.TokenParenClose {
				c.queryDepth--
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
			if !tok.Quoted && !keywordDistinctFrom(f.tokens, len(f.tokens)-1) {
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
					f.aggregate, f.analytic = false, len(stack) == 1 && p.conn.IsFabricDW()
				case "FROM", "JOIN", "APPLY", "WHERE", "ON", "GROUP", "SET", "VALUES":
					f.having = false
					f.aggregate, f.analytic = false, false
				}
			}
		}
	}
	c.frame, c.selectStart = stack[len(stack)-1], -1
	f := c.frame
	if len(stack) > 1 && !f.window && !f.predicate && !f.subquery {
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
					return
				}
			}
		}
		if parts, handled := datePartCompletionChoices(buf, parent, f.tokens, f.start, from); handled {
			c.choices, c.only, c.clause = parts, true, "DATEPART"
			return
		}
		c.parenthesized = true
		expressionParent, expressionStart := parent, f.start
		for i := len(stack) - 2; len(expressionParent) == 0 && i > 0; i-- {
			expressionParent, expressionStart = stack[i-1].tokens, stack[i].start
		}
		c.scalar = scalarParenthesizedPosition(buf, expressionParent, f.tokens, expressionStart, from, p.conn.IsFabricDW(), p.sourceFunctionKnown(expressionParent))
		c.aggregate = c.scalar && f.aggregate
		c.analytic = c.scalar && f.analytic
	}
	if f.subquery && len(f.tokens) == 0 {
		c.choices, c.only = []string{"SELECT"}, true
		if p.conn.IsFabricDW() {
			c.choices = append(c.choices, "FROM")
		}
		return
	}
	if f.predicate {
		c.clause, c.clauseStart = "WHERE", f.start
	}
	type caseArm struct {
		when     int
		searched bool
	}
	var cases []caseArm
	for i, tok := range f.tokens {
		if tok.Quoted || keywordDistinctFrom(f.tokens, i) {
			continue
		}
		word := strings.ToUpper(tok.Text)
		switch word {
		case "CASE":
			cases = append(cases, caseArm{-1, keywordNextIs(buf, tok.Start+len("CASE"), "WHEN")})
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
	c.caseDepth = len(cases)
	if len(cases) > 0 {
		if arm := cases[len(cases)-1]; arm.when >= 0 {
			c.clause, c.clauseStart = "WHEN", arm.when
			c.parenthesized = false
		} else {
			complete, _ := keywordExpressionComplete(buf, f.tokens[len(f.tokens)-1].Start, from, "VALUE")
			c.scalar = !complete
			c.aggregate = c.scalar && f.aggregate
			c.analytic = c.scalar && f.analytic
			return
		}
	}
	if c.parenthesized {
		return
	}
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
			c.scalar = !complete && (c.clause != "GROUP" || !p.conn.IsFabricDW())
			c.aggregate = c.scalar && f.aggregate
		case "SET":
			c.scalar = scalarAssignmentPosition(buf, f.tokens, from)
		}
	}
	if predicateClause(c.clause) && (c.clause != "QUALIFY" || p.conn.IsFabricDW()) {
		c.scalar, c.condition = predicateExpressionPosition(buf, c.clauseStart, from)
		c.aggregate = c.scalar && (c.clause == "HAVING" || f.aggregate)
	}
	c.analytic = c.scalar && f.analytic
	return
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
	inv := p.app.completion.inventories[completionInventoryKey(p.conn.Opts, p.database)]
	sys := p.app.completion.sysInventories[sysCompletionInventoryKey(p.conn.Opts)]
	return findCatalogFunction(inv, sys, schema, name.Text) != nil
}

func keywordDistinctFrom(tokens []sqlparse.Token, i int) bool {
	return sqlparse.IsDistinctFrom(tokens, i)
}

func completionStartsSource(tokens []sqlparse.Token, i int) bool {
	return keywordIs(tokens[i], "JOIN") || keywordIs(tokens[i], "APPLY") ||
		keywordIs(tokens[i], "FROM") && !keywordDistinctFrom(tokens, i)
}

func completionEndsSource(tokens []sqlparse.Token, i int) bool {
	return !tokens[i].Quoted && slices.Contains([]string{"SELECT", "WHERE", "GROUP", "ORDER", "HAVING", "QUALIFY", "SET", "VALUES", "UNION", "EXCEPT", "INTERSECT"}, strings.ToUpper(tokens[i].Text))
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

func expressionCompletionItems(buf []rune, cursor int, prefix string, scalar, condition, aggregate, analytic, fabric bool, existing []editor.CompletionItem) []editor.CompletionItem {
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
	if scalar {
		for _, function := range scalarCompletions {
			add(strings.TrimSuffix(function, "("), "function", false)
		}
		if aggregate {
			functions := aggregateCompletions
			if fabric {
				functions = fabricAggregateCompletions
			}
			for _, function := range functions {
				add(strings.TrimSuffix(function, "("), "function", false)
			}
		}
		if analytic {
			for _, function := range windowCompletions {
				add(strings.TrimSuffix(function, "("), "function", true)
			}
		}
	}
	if condition {
		for _, function := range predicateCompletions {
			label := strings.TrimSuffix(function, "(")
			if !fabric || !completionBuiltins[label].noWarehouse {
				add(label, "predicate", false)
			}
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
		if keywordDistinctFrom(parent, i) {
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

func (p *QueryPanel) builtinCompletionSupported(label string) bool {
	builtin := completionBuiltins[label]
	fabric := p.conn.IsFabricDW()
	if fabric && builtin.noWarehouse || !fabric && builtin.kind&builtinWarehouseOnly != 0 {
		return false
	}
	if builtin.since == 0 || p.conn.Server == nil {
		return true
	}
	info := p.conn.Server.Info()
	return info == nil || info.IsAzure() || info.VersionMajor == 0 || info.VersionMajor >= builtin.since
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

func completionCallItem(buf []rune, cursor int, label, detail string, window bool) editor.CompletionItem {
	if completionBuiltins[label].kind&builtinBare != 0 {
		return editor.CompletionItem{Text: label, Label: label, Detail: detail}
	}
	text, after, offset := keywordFunctionTemplate(buf, cursor, label, window)
	return editor.CompletionItem{Text: text, Label: label, Detail: detail, ReplaceAfter: after, CursorOffset: offset}
}

// consumed is an absolute buffer offset; caret is relative to inserted text.
// Keep trivia and existing call contents verbatim when reusing the suffix.
func completionCallEdit(buf []rune, cursor int, label string, rebuild bool) (text string, consumed, caret int, closed bool) {
	text, consumed = completionPhrasePrefix(buf, cursor, label)
	open := keywordNextToken(buf, consumed)
	if open.Kind == sqltext.KindPunct && buf[open.Start] == '(' {
		caret = len([]rune(text)) + open.End - cursor
		if !rebuild && completionBuiltins[label].kind&builtinNoArgs == 0 {
			return text, cursor, caret, true
		}
		end, ok := keywordCallEnd(buf, open)
		if !ok || !rebuild {
			if ok && completionBuiltins[label].kind&builtinNoArgs != 0 {
				caret = len([]rune(text)) + end - cursor
			}
			return text, cursor, caret, ok
		}
		text += string(buf[cursor:end])
		if completionBuiltins[label].kind&builtinNoArgs != 0 {
			caret = len([]rune(text))
		}
		return text, end, caret, true
	}
	text += string(buf[cursor:consumed])
	if label == "VALUES" || strings.HasSuffix(label, "EXISTS") {
		text += " "
	}
	text += "()"
	caret = len([]rune(text))
	if completionBuiltins[label].kind&builtinNoArgs == 0 {
		caret--
	}
	return text, consumed, caret, true
}

func keywordFunctionTemplate(buf []rune, cursor int, label string, window bool) (text string, replaceAfter, offset int) {
	text, consumed, caret, closed := completionCallEdit(buf, cursor, label, window || completionBuiltins[label].kind&builtinOrdered != 0)
	if !closed {
		return text, consumed - cursor, caret - len([]rune(text))
	}
	appendClause := func(words, template string, focus bool) bool {
		first, _, _ := strings.Cut(words, " ")
		next := keywordNextToken(buf, consumed)
		if next.Kind == sqltext.KindWord {
			word := strings.ToUpper(string(buf[next.Start:next.End]))
			if word != first && strings.HasPrefix(first, word) {
				if focus {
					caret = len([]rune(text)) + next.End - consumed
				}
				return false
			}
		}
		if keywordNextIs(buf, consumed, first) && !keywordNextIs(buf, consumed, words) {
			return false
		}
		if keywordNextIs(buf, consumed, words) {
			pos := consumed
			for range strings.Fields(words) {
				pos = keywordNextToken(buf, pos).End
			}
			open := keywordNextToken(buf, pos)
			end, ok := keywordCallEnd(buf, open)
			if !ok {
				if focus {
					target := open.Start
					if open.Kind == sqltext.KindEnd {
						target = pos
						for target < len(buf) && keywordWhitespace(buf[target]) {
							target++
						}
					} else if open.Kind == sqltext.KindPunct && buf[open.Start] == '(' {
						target = open.End
					}
					caret = len([]rune(text)) + target - consumed
				}
				return false
			}
			if focus {
				caret = len([]rune(text)) + open.End - consumed
			}
			text += string(buf[consumed:end])
			consumed = end
			return true
		}
		text += template
		if focus {
			caret = len([]rune(text)) - 1
		}
		return true
	}
	if completionBuiltins[label].kind&builtinOrdered != 0 {
		if !appendClause("WITHIN GROUP", " WITHIN GROUP (ORDER BY )", false) {
			return text, consumed - cursor, caret - len([]rune(text))
		}
	}
	if window {
		if slices.Contains([]string{"LAG", "LEAD", "FIRST_VALUE", "LAST_VALUE"}, label) {
			modifier := keywordNextToken(buf, consumed)
			if modifier.Kind == sqltext.KindWord {
				word := strings.ToUpper(string(buf[modifier.Start:modifier.End]))
				if strings.HasPrefix("IGNORE", word) || strings.HasPrefix("RESPECT", word) {
					if !keywordNextIs(buf, consumed, "IGNORE NULLS") && !keywordNextIs(buf, consumed, "RESPECT NULLS") {
						return text, consumed - cursor, caret - len([]rune(text))
					}
					end := keywordNextToken(buf, modifier.End).End
					text += string(buf[consumed:end])
					consumed = end
				}
			}
		}
		kind := completionBuiltins[label].kind
		template := " OVER ()"
		if kind&builtinPartitionOnly == 0 && kind&(builtinAggregate|builtinFabricAggregate) == 0 {
			template = " OVER (ORDER BY )"
		}
		appendClause("OVER", template, kind&builtinNoArgs != 0)
	}
	return text, consumed - cursor, caret - len([]rune(text))
}

func keywordCallEnd(buf []rune, open sqltext.Token) (int, bool) {
	if open.Kind != sqltext.KindPunct || buf[open.Start] != '(' {
		return 0, false
	}
	depth := 0
	for pos := open.Start; pos < len(buf); {
		tok, _ := sqltext.Next(buf, pos, len(buf), sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		pos = tok.End
		if tok.Kind != sqltext.KindPunct {
			continue
		}
		switch buf[tok.Start] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return tok.End, true
			}
		}
	}
	return 0, false
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

var predicateOperators = []string{
	"=", "<>", "!=", ">", ">=", "<", "<=",
	"+", "-", "*", "/", "%", "||",
	"IS NULL", "IS NOT NULL", "IS DISTINCT FROM", "IS NOT DISTINCT FROM",
	"LIKE", "NOT LIKE", "IN", "NOT IN", "BETWEEN", "NOT BETWEEN",
}

type predicateFrame struct {
	operand, comparison, value bool
	group                      bool
	between, is, cases         int
	not, quantified            bool
}

func scanPredicate(buf []rune, start, end int) (predicateFrame, bool) {
	stack := []predicateFrame{{}}
	previousDot, afterComparison := false, false
	for i := start; i < end; {
		tok, _ := sqltext.Next(buf, i, end, sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		i = tok.End
		f := &stack[len(stack)-1]
		if tok.Kind == sqltext.KindComment || tok.Kind == sqltext.KindNewline {
			continue
		}
		qualified := previousDot
		previousDot = tok.Kind == sqltext.KindPunct && buf[tok.Start] == '.'
		quantifierExpected := afterComparison
		afterComparison = false
		word := ""
		if tok.Kind == sqltext.KindWord {
			word = strings.ToUpper(string(buf[tok.Start:tok.End]))
		}
		if word == "CASE" {
			f.cases++
			f.operand = false
			continue
		}
		if f.cases > 0 {
			if word == "END" {
				f.cases--
				f.operand = f.cases == 0
			}
			continue
		}
		if tok.Kind == sqltext.KindPunct {
			switch buf[tok.Start] {
			case '(':
				stack = append(stack, predicateFrame{group: !f.operand && !f.comparison && !f.not && !f.value})
			case ')':
				if len(stack) > 1 {
					child := *f
					stack = stack[:len(stack)-1]
					f = &stack[len(stack)-1]
					f.comparison = f.comparison || child.group && child.comparison
				}
				f.operand = true
			case '=', '<', '>', '!':
				op := buf[tok.Start]
				afterComparison = f.operand && op != '!'
				if i < end && (op == '<' && buf[i] == '>' ||
					(op == '<' || op == '>') && buf[i] == '=' ||
					op == '!' && strings.ContainsRune("=<>", buf[i])) {
					i++
					afterComparison = f.operand
				}
				f.comparison, f.operand = true, false
			default:
				f.operand = false
				f.value = f.value || strings.ContainsRune("+-*/%|", buf[tok.Start])
			}
			continue
		}
		switch word {
		case "SELECT":
			f.group, f.operand = false, false
		case "AND", "OR":
			if word == "AND" && f.between == 1 {
				f.between, f.operand = 2, false
			} else {
				group := f.group
				*f = predicateFrame{group: group}
			}
		case "IS":
			f.comparison, f.operand, f.is = true, false, 1
		case "NOT":
			if f.is > 0 {
				f.is = 2
			} else if f.operand && !f.comparison {
				f.not, f.operand = true, false
			}
		case "NULL":
			f.is, f.operand = 0, true
		case "DISTINCT":
			if f.is > 0 {
				f.is, f.operand = 3, false
			}
		case "FROM":
			if f.is == 3 {
				f.is, f.operand = 0, false
			}
		case "LIKE", "IN", "BETWEEN":
			f.comparison, f.operand, f.not = true, false, false
			if word == "BETWEEN" {
				f.between = 1
			}
		case "ALL", "ANY", "SOME":
			if quantifierExpected {
				f.quantified = true
			} else {
				f.operand = true
			}
		case "EXISTS", "CONTAINS", "FREETEXT", "REGEXP_LIKE":
			if qualified {
				f.operand = true
				continue
			}
			f.comparison, f.operand, f.quantified = true, false, true
		case "COLLATE", "ESCAPE":
			f.operand = false
		default:
			f.operand = true
		}
	}
	f := stack[len(stack)-1]
	return f, !f.comparison && !f.value && (len(stack) == 1 || f.group)
}

func predicateSubquery(buf []rune, parent []sqlparse.Token, start, open int) bool {
	n := len(parent)
	if n == 0 {
		return false
	}
	last := parent[n-1]
	if keywordIs(last, "EXISTS") {
		return true
	}
	if !keywordIs(last, "ALL") && !keywordIs(last, "ANY") && !keywordIs(last, "SOME") ||
		keywordNextToken(buf[:open], last.Start+len(last.Text)).Kind != sqltext.KindEnd {
		return false
	}
	// Use the raw tail; the reduced stream has neither the comparison
	// operator nor a string literal that might be its left operand.
	if n >= 2 {
		start = parent[n-2].Start
	}
	f, _ := scanPredicate(buf, start, open)
	return f.quantified && !f.operand
}

func predicateExpressionPosition(buf []rune, start, end int) (scalar, condition bool) {
	f, condition := scanPredicate(buf, start, end)
	scalar = !f.operand && f.is == 0 && !f.not && !f.quantified && f.cases == 0
	return scalar, scalar && condition
}

// The reduced sqlparse stream omits operators and literals, so inspect the
// current predicate with the shared lexer.
func predicateChoices(buf []rune, start, end int, fallback []string) (choices []string, operatorExpected, keywordsOnly bool) {
	f, _ := scanPredicate(buf, start, end)
	if f.cases > 0 {
		return nil, false, false
	}
	switch {
	case f.is == 1:
		return []string{"NULL", "NOT NULL", "DISTINCT FROM", "NOT DISTINCT FROM"}, false, true
	case f.is == 2:
		return []string{"NULL", "DISTINCT FROM"}, false, true
	case f.is == 3:
		return []string{"FROM"}, false, true
	case f.not:
		return []string{"LIKE", "IN", "BETWEEN"}, false, true
	case f.quantified && !f.operand:
		return nil, false, true
	case !f.operand:
		return fallback, false, false
	case f.between == 1:
		return []string{"AND"}, false, true
	case !f.comparison:
		return predicateOperators, true, true
	default:
		return fallback, false, false
	}
}

func predicateClause(word string) bool {
	return word == "WHERE" || word == "ON" || word == "HAVING" || word == "QUALIFY" || word == "WHEN"
}
