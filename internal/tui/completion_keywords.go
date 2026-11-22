package tui

import (
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

// keywordCandidates supplements identifiers with SQL keywords and expressions.
// It recognizes common clauses and DDL headers, not the full T-SQL grammar.
// sourceComplete reports a keyword-only position, where catalog identifiers
// are not valid continuations. With an open '[' at from (prefix is what
// follows it), only a data-type position answers, with its user-defined
// types: no keyword or system type is written bracketed.
func (p *QueryPanel) keywordCandidates(buf []rune, tokens []sqlparse.Token, query *sqlparse.Query, from, cursor int, prefix string) (items []editor.CompletionItem, sourceComplete bool) {
	bracket := from < len(buf) && buf[from] == '['
	if sqlparse.HasSigil(prefix) || from < len(buf) && buf[from] == '"' ||
		cursor < len(buf) && (sqltext.IsWordContinue(buf[cursor]) || buf[cursor] == '.') {
		return nil, false
	}
	fabric := p.conn.IsFabricDW()
	cc := p.analyzeCompletionContext(buf, tokens, query, from)
	columnType := cc.datatype
	ddlAction, ddlType, ddlTail := ddlCompletionTarget(tokens, from)
	userTypes := ddlTypes{fabric: fabric}
	if columnType || ddlAction == "CREATE" && ddlType == "TABLE" {
		userTypes = p.ddlTypes()
	}
	if ddlAction == "CREATE" && ddlType == "TABLE" {
		if types, inside := ddlColumnTypeChoices(ddlTail, userTypes); inside {
			cc.only, cc.choices, columnType = true, types, true
		}
	}
	if bracket && !columnType {
		return nil, false
	}
	// CREATE OR ALTER (2016 SP1) and DROP … IF EXISTS (2016) are at the
	// supported floor (docs/open-threads.md § Version support), so every
	// server is offered both; the WINDOW clause is SQL Server 2022's, gated
	// with the builtins (builtinDefinitions).
	f := cc.frame
	statement, statementStart := keywordDMLStatement(f.tokens)
	defer func() {
		if !sourceComplete && !bracket {
			if cc.scalar && strings.HasPrefix("CASE", strings.ToUpper(prefix)) {
				items = append(items, caseCompletionItem(buf, cursor))
			}
			items = append(items, expressionCompletionItems(buf, cursor, prefix, cc, fabric, items)...)
		}
		items = slices.DeleteFunc(items, func(item editor.CompletionItem) bool {
			return !p.builtinCompletionSupported(item.Label, columnType)
		})
	}()
	s := keywordScope{buf: buf, from: from, fabric: fabric, cc: cc, f: f, clause: cc.clause,
		statement: statement, statementStart: statementStart, ddlAction: ddlAction, ddlTail: ddlTail}
	// aliasable: the word being typed may be a new alias ("FROM Orders o"), so
	// a keyword it starts (OUTER APPLY) is Soft: Tab takes it, Enter is a
	// newline. A reserved word typed in full ("where") can't be an alias. With
	// nothing typed ("WHERE o.name |"), every keyword is Soft the same way: Enter
	// at the end of a line is a line break, not "=".
	aliasable := false
	clause := cc.clause
	if cc.only {
		s.choices, s.sourceComplete = cc.choices, true
	} else if cc.caseDepth > 0 && clause != "WHEN" || cc.parenthesized {
		return nil, false
	} else if len(f.tokens) == 0 {
		if f.window {
			s.choices = []string{"PARTITION BY", "ORDER BY"}
		} else if !f.predicate {
			s.choices = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "CREATE", "ALTER", "DROP", "TRUNCATE", "EXEC", "EXECUTE"}
			if fabric {
				s.choices = append(s.choices, "FROM")
			}
		}
	} else {
		s.last = f.tokens[len(f.tokens)-1]
		expressionStart := s.last.Start
		if clause == "SELECT" && s.last.Kind == sqlparse.TokenStar {
			expressionStart = cc.clauseStart
		}
		s.complete, s.tail = keywordExpressionComplete(buf, expressionStart, from, clause)
		s.sourceComplete = s.complete && (clause == "FROM" || clause == "JOIN")
		s.applyClauseRules()
		aliasable = s.aliasChoices(prefix)
		if fabric && cc.selectStart < 0 && s.complete && (clause == "FROM" || clause == "ON") {
			s.choices = append(s.choices, "SELECT")
		}
		s.trailingClauseChoices()
	}

	if predicateClause(clause) && (clause != "QUALIFY" || fabric) {
		var only bool
		s.choices, _, only = predicateChoices(buf, cc.clauseStart, from, s.choices)
		s.sourceComplete = s.sourceComplete || only
	}
	if statement != "" && statement != "SELECT" || f.predicate && cc.selectStart < 0 && !cc.hasFrom {
		s.choices = dmlClauseChoices(s.choices, f.tokens, statement, statementStart, clause, s.sourceComplete)
	}
	choices, sourceComplete := s.choices, s.sourceComplete
	if len(choices) == 0 {
		return nil, sourceComplete
	}

	blocked := blockedClauses(tokens, cursor, f)
	nextToken := keywordNextToken(buf, cursor)
	nextWord := ""
	if nextToken.Kind == sqltext.KindWord {
		nextWord = strings.ToUpper(string(buf[nextToken.Start:nextToken.End]))
	}
	prefixUpper, prefixLower := strings.ToUpper(prefix), strings.ToLower(prefix)
	for _, candidate := range choices {
		if typ, ok := userTypes.userType(candidate); ok && columnType {
			// Typed as the start of the qualified name, or anywhere in the
			// bare one as catalog names match (nameMatch).
			match, partial := strings.HasPrefix(strings.ToUpper(candidate), prefixUpper), false
			if !match {
				match, partial = nameMatch(typ.Name, prefixLower)
			}
			if match {
				items = append(items, editor.CompletionItem{Text: candidate, Label: candidate, Detail: ddlUserTypeDetail(typ),
					Soft: prefix == "", Partial: partial})
			}
			continue
		}
		if bracket {
			continue
		}
		function := strings.HasSuffix(candidate, "(")
		label := strings.TrimSuffix(candidate, "(")
		word, _, phrase := strings.Cut(label, " ")
		nextIsWord := nextWord == word
		if (word == "ASC" || word == "DESC") && (nextWord == "ASC" || nextWord == "DESC") {
			nextIsWord = true
		}
		if columnType && (label == "NULL" || label == "NOT NULL") && (nextWord == "NULL" || nextWord == "NOT") {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(label), prefixUpper) || !function && (blocked[word] || nextIsWord) {
			continue
		}
		text := label
		if cc.apply && label == "VALUES" {
			items = append(items, editor.CompletionItem{Text: "(VALUES ())", Label: label, Detail: "table-valued constructor", CursorOffset: -2})
			continue
		}
		if function {
			detail := "function"
			if s.sourceFunctions {
				detail = "table-valued function"
			} else if clause == "GROUP" || columnType {
				detail = "keyword"
			}
			kind := completionBuiltins[label].kind
			window := !columnType && (clause == "QUALIFY" || kind&builtinWindow != 0 && kind&builtinAggregate == 0 &&
				(!fabric || kind&builtinFabricAggregate == 0))
			items = append(items, completionCallItem(buf, cursor, label, detail, window))
			continue
		}
		if phrase {
			text, _ = completionPhrasePrefix(buf, cursor, label)
		}
		before := 0
		if s.jsonOptions && f.tokens[len(f.tokens)-1].Kind != sqlparse.TokenComma {
			text, before = ", "+text, s.jsonBefore
		}
		items = append(items, editor.CompletionItem{Text: text, Label: label, Detail: "keyword", ReplaceBefore: before,
			AppendSpace: !columnType && clause != "DATEPART", Soft: aliasable || prefix == ""})
	}
	return items, sourceComplete
}

// keywordScope is one keywordCandidates request as the clause rules see it:
// the analyzed context and the last token before the cursor (inputs), and
// the choices plus their side facts (outputs).
type keywordScope struct {
	buf            []rune
	from           int
	fabric         bool
	cc             completionContext
	f              completionFrame
	last           sqlparse.Token
	clause         string
	statement      string
	statementStart int
	ddlAction      string
	ddlTail        []sqlparse.Token
	// complete: the expression before the cursor is finished. selectChoices
	// clears it for an unfinished projection, which the later alias and
	// trailing-clause steps read.
	complete bool
	tail     sqltext.Token

	choices         []string
	sourceComplete  bool
	sourceFunctions bool // choices are table-valued functions
	jsonOptions     bool // choices are FOR JSON options, joined with ", "
	jsonBefore      int  // blanks between the last option and the cursor
}

// clauseRule is one arm of the keyword ladder. Rules are tried in order and
// the first whose match holds supplies the choices; complete marks a
// keyword-only position (sourceComplete).
type clauseRule struct {
	match    func(s *keywordScope) bool
	choices  func(s *keywordScope) []string
	complete bool
}

// fixedChoices returns a fresh slice per call: later steps append to and
// filter choices in place.
func fixedChoices(words ...string) func(*keywordScope) []string {
	return func(*keywordScope) []string { return slices.Clone(words) }
}

func lastIs(words ...string) func(s *keywordScope) bool {
	return func(s *keywordScope) bool {
		return slices.ContainsFunc(words, func(word string) bool { return keywordIs(s.last, word) })
	}
}

var clauseRules = []clauseRule{
	{match: func(s *keywordScope) bool {
		return len(s.f.tokens) == 1 && lastIs("CREATE", "ALTER", "DROP")(s)
	}, choices: ddlObjectTypeChoices, complete: true},
	{match: func(s *keywordScope) bool {
		return len(s.f.tokens) == 2 && keywordIs(s.f.tokens[0], "CREATE") && keywordIs(s.last, "OR")
	}, choices: fixedChoices("ALTER"), complete: true},
	{match: func(s *keywordScope) bool {
		return len(s.f.tokens) == 3 && keywordIs(s.f.tokens[0], "CREATE") && keywordIs(s.f.tokens[1], "OR") && keywordIs(s.last, "ALTER")
	}, choices: func(s *keywordScope) []string {
		choices := []string{"VIEW", "PROCEDURE", "FUNCTION"}
		if !s.fabric {
			choices = append(choices, "TRIGGER")
		}
		return choices
	}, complete: true},
	{match: func(s *keywordScope) bool {
		return s.ddlAction == "DROP" && len(s.ddlTail) == 0
	}, choices: fixedChoices("IF EXISTS")},
	{match: func(s *keywordScope) bool {
		return s.ddlAction == "DROP" && len(s.ddlTail) == 1 && keywordIs(s.ddlTail[0], "IF")
	}, choices: fixedChoices("EXISTS"), complete: true},
	{match: func(s *keywordScope) bool {
		return s.clause == "" && keywordIs(s.f.tokens[0], "UPDATE") && s.complete &&
			keywordAliasPosition(keywordProjection(s.f.tokens[1:]), s.f.start)
	}, choices: fixedChoices("SET"), complete: true},
	{match: func(s *keywordScope) bool {
		return s.clause == "" && s.statement == "DELETE" && s.complete &&
			keywordAliasPosition(keywordProjection(s.f.tokens[s.statementStart+1:]), s.f.start)
	}, choices: fixedChoices("WHERE", "FROM"), complete: true},
	{match: func(s *keywordScope) bool { return s.clause == "FOR" }, choices: forClauseChoices, complete: true},
	{match: lastIs("CROSS"), choices: fixedChoices("JOIN", "APPLY")},
	{match: lastIs("OUTER"), choices: func(s *keywordScope) []string {
		if len(s.f.tokens) > 1 {
			prev := s.f.tokens[len(s.f.tokens)-2]
			if keywordIs(prev, "LEFT") || keywordIs(prev, "RIGHT") || keywordIs(prev, "FULL") {
				return []string{"JOIN"}
			}
		}
		return []string{"APPLY"}
	}},
	{match: lastIs("LEFT", "RIGHT", "FULL", "INNER"), choices: fixedChoices("JOIN")},
	{match: func(s *keywordScope) bool {
		return lastIs("ORDER", "GROUP")(s) || s.f.window && keywordIs(s.last, "PARTITION")
	}, choices: fixedChoices("BY")},
	{match: func(s *keywordScope) bool {
		return (s.clause == "FROM" || s.clause == "JOIN") && !s.complete &&
			(lastIs("FROM", "JOIN", "APPLY")(s) || s.last.Kind == sqlparse.TokenComma)
	}, choices: func(s *keywordScope) []string {
		s.sourceFunctions = true
		if s.cc.apply {
			return append(slices.Clone(tableValuedCompletions), "VALUES(")
		}
		return tableValuedCompletions
	}},
	{match: func(s *keywordScope) bool { return s.clause == "SELECT" }, choices: selectChoices},
	{match: func(s *keywordScope) bool { return s.clause == "HAVING" && !s.complete }, choices: func(s *keywordScope) []string {
		if s.fabric {
			return fabricAggregateCompletions
		}
		return aggregateCompletions
	}},
	{match: func(s *keywordScope) bool {
		return s.clause == "QUALIFY" && !s.complete && s.fabric
	}, choices: func(*keywordScope) []string { return windowCompletions }},
	{match: func(s *keywordScope) bool {
		return s.clause == "ORDER" && !s.complete && !s.f.window
	}, choices: func(*keywordScope) []string { return windowCompletions }},
	{match: func(s *keywordScope) bool {
		return s.clause == "GROUP" && (keywordIs(s.last, "BY") || s.last.Kind == sqlparse.TokenComma)
	}, choices: func(s *keywordScope) []string {
		choices := []string{"ROLLUP(", "CUBE(", "GROUPING SETS("}
		if s.fabric && keywordIs(s.last, "BY") {
			choices = append([]string{"ALL"}, choices...)
		}
		return choices
	}},
	{match: func(s *keywordScope) bool { return s.complete }, choices: nextClauseChoices},
}

func (s *keywordScope) applyClauseRules() {
	for _, rule := range clauseRules {
		if rule.match(s) {
			if rule.complete {
				s.sourceComplete = true
			}
			s.choices = rule.choices(s)
			return
		}
	}
}

// ddlObjectTypeChoices follows a bare CREATE, ALTER or DROP.
func ddlObjectTypeChoices(s *keywordScope) []string {
	choices := []string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION", "SCHEMA"}
	if !s.fabric {
		choices = append(choices, "INDEX", "DATABASE", "TRIGGER", "SEQUENCE")
		if !keywordIs(s.last, "ALTER") {
			choices = append(choices, "TYPE", "SYNONYM")
		}
	}
	if keywordIs(s.last, "CREATE") {
		choices = append(choices, "OR ALTER")
	}
	return choices
}

// forClauseChoices completes FOR JSON|XML|BROWSE, their modes, and the
// FOR JSON options not yet given.
func forClauseChoices(s *keywordScope) (choices []string) {
	switch {
	case keywordIs(s.last, "FOR"):
		if !s.fabric || s.cc.queryDepth == 0 {
			choices = []string{"JSON"}
		}
		if !s.fabric {
			choices = append(choices, "XML", "BROWSE")
		}
	case keywordIs(s.last, "JSON"):
		choices = []string{"PATH", "AUTO"}
	case !s.fabric && keywordIs(s.last, "XML"):
		choices = []string{"PATH", "AUTO", "RAW", "EXPLICIT"}
	}
	format := s.f.tokens
	for len(format) > 0 && format[0].Start < s.cc.clauseStart {
		format = format[1:]
	}
	if len(format) >= 2 && keywordIs(format[0], "JSON") &&
		(keywordIs(format[1], "PATH") || keywordIs(format[1], "AUTO")) {
		s.jsonOptions = true
		for _, option := range []string{"WITHOUT_ARRAY_WRAPPER", "INCLUDE_NULL_VALUES"} {
			if !slices.ContainsFunc(format, func(tok sqlparse.Token) bool { return keywordIs(tok, option) }) {
				choices = append(choices, option)
			}
		}
		if s.last.Kind != sqlparse.TokenComma {
			end := keywordNextToken(s.buf, s.last.Start).End
			if strings.Trim(string(s.buf[end:s.from]), " \t") == "" {
				s.jsonBefore = s.from - end
			}
		}
	}
	return choices
}

// selectChoices completes the projection list, or FROM after a finished one.
func selectChoices(s *keywordScope) (choices []string) {
	projection := keywordProjection(s.f.tokens[s.cc.selectStart:])
	empty := len(projection) == 0 && s.tail.Kind != sqltext.KindString
	if empty || !s.complete {
		s.complete = false
		if empty {
			choices = []string{"*"}
			if keywordIs(s.last, "SELECT") {
				choices = append(choices, "DISTINCT", "TOP")
			}
		}
		if !keywordIs(s.last, "TOP") {
			choices = append(choices, projectionCompletions...)
			choices = append(choices, scalarCompletions...)
		}
	} else if !s.cc.hasFrom {
		choices = []string{"FROM"}
	}
	return choices
}

// nextClauseChoices follows a finished expression in the current clause.
func nextClauseChoices(s *keywordScope) []string {
	switch s.clause {
	case "FROM":
		return []string{"WHERE", "JOIN", "INNER JOIN", "LEFT JOIN", "RIGHT JOIN", "FULL JOIN", "CROSS JOIN", "CROSS APPLY", "OUTER APPLY", "GROUP BY", "ORDER BY"}
	case "JOIN":
		return []string{"ON"}
	case "SET":
		if s.statement == "UPDATE" {
			if value, done := assignmentValue(s.buf, s.cc.clauseStart, s.from); value && done {
				return []string{"FROM", "WHERE"}
			}
		}
	case "WHERE":
		return []string{"AND", "OR", "GROUP BY", "ORDER BY"}
	case "ON":
		return []string{"AND", "OR", "WHERE", "JOIN", "CROSS APPLY", "OUTER APPLY", "GROUP BY", "ORDER BY"}
	case "GROUP":
		return []string{"HAVING", "ORDER BY"}
	case "WINDOW":
		return []string{"ORDER BY"}
	case "HAVING", "QUALIFY":
		return []string{"AND", "OR", "ORDER BY"}
	case "ORDER":
		return []string{"ASC", "DESC"}
	case "PARTITION":
		if s.f.window {
			return []string{"ORDER BY"}
		}
	}
	return nil
}

// aliasChoices adds AS where the finished expression may take an alias and
// reports whether the typed word may itself be that alias.
func (s *keywordScope) aliasChoices(prefix string) (aliasable bool) {
	if !s.complete || s.clause != "SELECT" && s.clause != "FROM" && s.clause != "JOIN" {
		return false
	}
	aliasTokens := s.f.tokens
	if s.clause == "SELECT" {
		aliasTokens = keywordProjection(aliasTokens[s.cc.selectStart:])
	}
	if !keywordAliasPosition(aliasTokens, s.cc.clauseStart) {
		return false
	}
	s.choices = append(s.choices, "AS")
	return prefix != "" && !gosmo.IsReservedKeyword(prefix)
}

// trailingClauseChoices adds the clauses that may follow the current one in
// a query: WHERE … WINDOW, QUALIFY (Fabric) and FOR JSON|XML|BROWSE.
func (s *keywordScope) trailingClauseChoices() {
	direction := keywordIs(s.last, "ASC") || keywordIs(s.last, "DESC")
	if s.f.window || !(s.complete || s.clause == "ORDER" && direction) {
		return
	}
	switch s.clause {
	case "SELECT":
		if s.cc.hasFrom {
			s.choices = append(s.choices, "WHERE", "GROUP BY", "ORDER BY")
		}
		fallthrough
	case "FROM", "WHERE", "ON", "GROUP", "HAVING":
		if s.clause != "SELECT" || s.cc.hasFrom {
			s.choices = append(s.choices, "WINDOW")
		}
		if s.fabric {
			s.choices = append(s.choices, "QUALIFY")
		}
		fallthrough
	case "QUALIFY", "ORDER":
		if !s.fabric || s.cc.queryDepth == 0 {
			s.choices = append(s.choices, "FOR JSON")
		}
		if !s.fabric {
			s.choices = append(s.choices, "FOR XML", "FOR BROWSE")
		}
	}
}

// dmlClauseChoices drops query-only clauses from a DML statement's choices,
// and joins where a DELETE has a single FROM source; that DELETE's source
// may take a second FROM instead.
func dmlClauseChoices(choices []string, tokens []sqlparse.Token, statement string, statementStart int, clause string, sourceComplete bool) []string {
	joins := true
	if statement == "DELETE" {
		tail := tokens[statementStart+1:]
		if len(tail) >= 2 && keywordIs(tail[0], "TOP") {
			tail = tail[2:]
			if len(tail) > 0 && keywordIs(tail[0], "PERCENT") {
				tail = tail[1:]
			}
		}
		if len(tail) > 0 && keywordIs(tail[0], "FROM") {
			sources := 0
			for i, tok := range tail {
				if keywordIs(tok, "FROM") && !keywordDistinctFrom(tail, i) {
					sources++
				}
			}
			joins = sources > 1
		}
	}
	if !joins && clause == "FROM" && sourceComplete {
		choices = append(choices, "FROM")
	}
	return slices.DeleteFunc(choices, func(choice string) bool {
		word, _, _ := strings.Cut(choice, " ")
		switch word {
		case "GROUP", "HAVING", "ORDER", "WINDOW", "QUALIFY", "FOR":
			return true
		case "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "OUTER", "ON", "AS":
			return !joins
		}
		return false
	})
}

// blockedClauses names the clauses a later one after the cursor already
// rules out. Only singleton clauses block later clauses. Functions, joins,
// boolean operators and per-column modifiers can repeat at this nesting level.
func blockedClauses(tokens []sqlparse.Token, cursor int, f completionFrame) map[string]bool {
	future := make(map[string]bool)
	depth := 0
futureScan:
	for i, tok := range tokens {
		if tok.Start < cursor {
			continue
		}
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			depth++
		case sqlparse.TokenParenClose:
			if depth == 0 {
				break futureScan
			}
			depth--
		default:
			if depth == 0 && !tok.Quoted && !keywordDistinctFrom(tokens, i) {
				future[strings.ToUpper(tok.Text)] = true
			}
		}
	}
	clauses := []string{"FROM", "WHERE", "GROUP", "HAVING", "WINDOW", "QUALIFY", "ORDER", "FOR"}
	if f.window {
		clauses = []string{"PARTITION", "ORDER"}
	} else if len(f.tokens) > 0 && keywordIs(f.tokens[0], "FROM") {
		clauses[0] = "SELECT"
	}
	blocked := make(map[string]bool)
	for i, word := range clauses {
		if future[word] {
			for _, later := range clauses[i:] {
				blocked[later] = true
			}
			break
		}
	}
	return blocked
}

func keywordDMLStatement(tokens []sqlparse.Token) (kind string, start int) {
	for i, tok := range tokens {
		if tok.Quoted || i > 0 && tokens[i-1].Kind == sqlparse.TokenDot || kind == "MERGE" {
			continue
		}
		switch strings.ToUpper(tok.Text) {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE":
			kind, start = strings.ToUpper(tok.Text), i
		}
	}
	return
}

func keywordStatementStart(buf []rune, start, end int) int {
	cutter := sqltext.NewStatementCutter(func(pos int) { start = pos })
	for pos := start; pos < end; {
		tok, _ := sqltext.Next(buf, pos, end, sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		pos = tok.End
		cutter.Feed(buf, tok, tok.Start)
		if tok.Kind == sqltext.KindPunct && buf[tok.Start] == ';' {
			start = tok.End
		}
	}
	return start
}

func keywordAliasPosition(tokens []sqlparse.Token, start int) bool {
	first := 0
	for i, tok := range tokens {
		if tok.Start < start || tok.Kind == sqlparse.TokenComma {
			first = i + 1
		}
	}
	tokens = tokens[first:]
	if len(tokens) == 0 {
		return false
	}
	if tokens[len(tokens)-1].Kind == sqlparse.TokenParenClose {
		tokens = tokens[:len(tokens)-1]
		if len(tokens) == 0 {
			return true
		}
	}
	for i, tok := range tokens {
		if i%2 == 0 && tok.Kind != sqlparse.TokenIdent || i%2 == 1 && tok.Kind != sqlparse.TokenDot {
			return false
		}
	}
	return len(tokens)%2 == 1
}

// Parenthesized TOP arguments have already collapsed to their closing token.
func keywordProjection(tokens []sqlparse.Token) []sqlparse.Token {
	if len(tokens) > 0 && (keywordIs(tokens[0], "DISTINCT") || keywordIs(tokens[0], "ALL")) {
		tokens = tokens[1:]
	}
	if len(tokens) >= 2 && keywordIs(tokens[0], "TOP") {
		tokens = tokens[2:]
		if len(tokens) > 0 && keywordIs(tokens[0], "PERCENT") {
			tokens = tokens[1:]
		}
		if len(tokens) >= 2 && keywordIs(tokens[0], "WITH") && keywordIs(tokens[1], "TIES") {
			tokens = tokens[2:]
		}
	}
	return tokens
}

func keywordIs(tok sqlparse.Token, word string) bool {
	return !tok.Quoted && strings.EqualFold(tok.Text, word)
}
