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
// are not valid continuations.
func (p *QueryPanel) keywordCandidates(buf []rune, tokens []sqlparse.Token, query *sqlparse.Query, from, cursor int, prefix string) (items []editor.CompletionItem, sourceComplete bool) {
	if sqlparse.HasSigil(prefix) || from < len(buf) && (buf[from] == '[' || buf[from] == '"') ||
		cursor < len(buf) && (sqltext.IsWordContinue(buf[cursor]) || buf[cursor] == '.') {
		return nil, false
	}
	fabric := p.conn.IsFabricDW()
	aggregates := aggregateCompletions
	if fabric {
		aggregates = fabricAggregateCompletions
	}
	context := p.analyzeCompletionContext(buf, tokens, query, from)
	columnType := context.datatype
	ddlAction, ddlType, ddlTail := ddlCompletionTarget(tokens, from)
	if ddlAction == "CREATE" && ddlType == "TABLE" {
		if types, inside := ddlColumnTypeChoices(ddlTail, fabric); inside {
			context.only, context.choices, columnType = true, types, true
		}
	}
	orAlter, ifExists, namedWindow := true, true, true
	if p.conn.Server != nil {
		if info := p.conn.Server.Info(); info != nil && !info.IsAzure() && info.VersionMajor != 0 {
			ifExists = info.VersionMajor >= 13
			orAlter = info.VersionMajor > 13 || info.VersionMajor == 13 && info.VersionBuild >= 4001
			namedWindow = info.VersionMajor >= 16
		}
	}
	f := context.frame
	statement, statementStart := keywordDMLStatement(f.tokens)
	defer func() {
		if !sourceComplete {
			if context.scalar && strings.HasPrefix("CASE", strings.ToUpper(prefix)) {
				items = append(items, caseCompletionItem(buf, cursor))
			}
			items = append(items, expressionCompletionItems(buf, cursor, prefix, context.scalar, context.condition, context.aggregate, context.analytic, fabric, items)...)
		}
		items = slices.DeleteFunc(items, func(item editor.CompletionItem) bool {
			return !p.builtinCompletionSupported(item.Label)
		})
	}()
	var choices []string
	jsonOptions, jsonBefore := false, 0
	sourceFunctions := false
	// aliasable: the word being typed may be a new alias ("FROM Orders o"), so
	// a keyword it starts (OUTER APPLY) is Soft: Tab takes it, Enter is a
	// newline. A reserved word typed in full ("where") can't be an alias. With
	// nothing typed ("WHERE o.name |"), every keyword is Soft the same way: Enter
	// at the end of a line is a line break, not "=".
	aliasable := false
	clause, queryDepth := context.clause, context.queryDepth
	selectStart, hasFrom := context.selectStart, context.hasFrom
	if context.only {
		choices, sourceComplete = context.choices, true
	} else if context.caseDepth > 0 && clause != "WHEN" || context.parenthesized {
		return nil, false
	} else if len(f.tokens) == 0 {
		if f.window {
			choices = []string{"PARTITION BY", "ORDER BY"}
		} else if !f.predicate {
			choices = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "CREATE", "ALTER", "DROP", "TRUNCATE", "EXEC", "EXECUTE"}
			if fabric {
				choices = append(choices, "FROM")
			}
		}
	} else {
		last := f.tokens[len(f.tokens)-1]
		expressionStart := last.Start
		if clause == "SELECT" && last.Kind == sqlparse.TokenStar {
			expressionStart = context.clauseStart
		}
		complete, tail := keywordExpressionComplete(buf, expressionStart, from, clause)
		sourceComplete = complete && (clause == "FROM" || clause == "JOIN")
		direction := keywordIs(last, "ASC") || keywordIs(last, "DESC")
		switch {
		case len(f.tokens) == 1 && (keywordIs(last, "CREATE") || keywordIs(last, "ALTER") || keywordIs(last, "DROP")):
			sourceComplete = true
			choices = []string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION", "SCHEMA"}
			if !fabric {
				choices = append(choices, "INDEX", "DATABASE", "TRIGGER", "SEQUENCE")
				if !keywordIs(last, "ALTER") {
					choices = append(choices, "TYPE", "SYNONYM")
				}
			}
			if keywordIs(last, "CREATE") && orAlter {
				choices = append(choices, "OR ALTER")
			}
		case len(f.tokens) == 2 && keywordIs(f.tokens[0], "CREATE") && keywordIs(last, "OR"):
			sourceComplete = true
			if orAlter {
				choices = []string{"ALTER"}
			}
		case len(f.tokens) == 3 && keywordIs(f.tokens[0], "CREATE") && keywordIs(f.tokens[1], "OR") && keywordIs(last, "ALTER"):
			sourceComplete = true
			if orAlter {
				choices = []string{"VIEW", "PROCEDURE", "FUNCTION"}
				if !fabric {
					choices = append(choices, "TRIGGER")
				}
			}
		case ddlAction == "DROP" && len(ddlTail) == 0 && ifExists:
			choices = []string{"IF EXISTS"}
		case ddlAction == "DROP" && len(ddlTail) == 1 && keywordIs(ddlTail[0], "IF"):
			sourceComplete = true
			if ifExists {
				choices = []string{"EXISTS"}
			}
		case clause == "" && keywordIs(f.tokens[0], "UPDATE") && complete &&
			keywordAliasPosition(keywordProjection(f.tokens[1:]), f.start):
			choices, sourceComplete = []string{"SET"}, true
		case clause == "" && statement == "DELETE" && complete &&
			keywordAliasPosition(keywordProjection(f.tokens[statementStart+1:]), f.start):
			choices, sourceComplete = []string{"WHERE", "FROM"}, true
		case clause == "FOR":
			sourceComplete = true
			switch {
			case keywordIs(last, "FOR"):
				if !fabric || queryDepth == 0 {
					choices = []string{"JSON"}
				}
				if !fabric {
					choices = append(choices, "XML", "BROWSE")
				}
			case keywordIs(last, "JSON"):
				choices = []string{"PATH", "AUTO"}
			case !fabric && keywordIs(last, "XML"):
				choices = []string{"PATH", "AUTO", "RAW", "EXPLICIT"}
			}
			format := f.tokens
			for len(format) > 0 && format[0].Start < context.clauseStart {
				format = format[1:]
			}
			if len(format) >= 2 && keywordIs(format[0], "JSON") &&
				(keywordIs(format[1], "PATH") || keywordIs(format[1], "AUTO")) {
				jsonOptions = true
				for _, option := range []string{"WITHOUT_ARRAY_WRAPPER", "INCLUDE_NULL_VALUES"} {
					if !slices.ContainsFunc(format, func(tok sqlparse.Token) bool { return keywordIs(tok, option) }) {
						choices = append(choices, option)
					}
				}
				if last.Kind != sqlparse.TokenComma {
					end := keywordNextToken(buf, last.Start).End
					if strings.Trim(string(buf[end:from]), " \t") == "" {
						jsonBefore = from - end
					}
				}
			}
		case keywordIs(last, "CROSS"):
			choices = []string{"JOIN", "APPLY"}
		case keywordIs(last, "OUTER"):
			choices = []string{"APPLY"}
			if len(f.tokens) > 1 {
				prev := f.tokens[len(f.tokens)-2]
				if keywordIs(prev, "LEFT") || keywordIs(prev, "RIGHT") || keywordIs(prev, "FULL") {
					choices = []string{"JOIN"}
				}
			}
		case keywordIs(last, "LEFT"), keywordIs(last, "RIGHT"), keywordIs(last, "FULL"), keywordIs(last, "INNER"):
			choices = []string{"JOIN"}
		case keywordIs(last, "ORDER"), keywordIs(last, "GROUP"), f.window && keywordIs(last, "PARTITION"):
			choices = []string{"BY"}
		case (clause == "FROM" || clause == "JOIN") && !complete &&
			(keywordIs(last, "FROM") || keywordIs(last, "JOIN") || keywordIs(last, "APPLY") || last.Kind == sqlparse.TokenComma):
			choices = tableValuedCompletions
			if context.apply {
				choices = append(slices.Clone(choices), "VALUES(")
			}
			sourceFunctions = true
		case clause == "SELECT":
			projection := keywordProjection(f.tokens[selectStart:])
			empty := len(projection) == 0 && tail.Kind != sqltext.KindString
			if empty || !complete {
				complete = false
				if empty {
					choices = []string{"*"}
					if keywordIs(last, "SELECT") {
						choices = append(choices, "DISTINCT", "TOP")
					}
				}
				if !keywordIs(last, "TOP") {
					choices = append(choices, projectionCompletions...)
					choices = append(choices, scalarCompletions...)
				}
			} else if !hasFrom {
				choices = []string{"FROM"}
			}
		case clause == "HAVING" && !complete:
			choices = aggregates
		case clause == "QUALIFY" && !complete && fabric:
			choices = windowCompletions
		case clause == "ORDER" && !complete && !f.window:
			choices = windowCompletions
		case clause == "GROUP" && (keywordIs(last, "BY") || last.Kind == sqlparse.TokenComma):
			choices = []string{"ROLLUP(", "CUBE(", "GROUPING SETS("}
			if fabric && keywordIs(last, "BY") {
				choices = append([]string{"ALL"}, choices...)
			}
		case complete:
			switch clause {
			case "FROM":
				choices = []string{"WHERE", "JOIN", "INNER JOIN", "LEFT JOIN", "RIGHT JOIN", "FULL JOIN", "CROSS JOIN", "CROSS APPLY", "OUTER APPLY", "GROUP BY", "ORDER BY"}
			case "JOIN":
				choices = []string{"ON"}
			case "SET":
				if statement == "UPDATE" {
					if value, done := assignmentValue(buf, context.clauseStart, from); value && done {
						choices = []string{"FROM", "WHERE"}
					}
				}
			case "WHERE":
				choices = []string{"AND", "OR", "GROUP BY", "ORDER BY"}
			case "ON":
				choices = []string{"AND", "OR", "WHERE", "JOIN", "CROSS APPLY", "OUTER APPLY", "GROUP BY", "ORDER BY"}
			case "GROUP":
				choices = []string{"HAVING", "ORDER BY"}
			case "WINDOW":
				choices = []string{"ORDER BY"}
			case "HAVING", "QUALIFY":
				choices = []string{"AND", "OR", "ORDER BY"}
			case "ORDER":
				choices = []string{"ASC", "DESC"}
			case "PARTITION":
				if f.window {
					choices = []string{"ORDER BY"}
				}
			}
		}
		if complete && (clause == "SELECT" || clause == "FROM" || clause == "JOIN") {
			aliasTokens := f.tokens
			if clause == "SELECT" {
				aliasTokens = keywordProjection(aliasTokens[selectStart:])
			}
			if keywordAliasPosition(aliasTokens, context.clauseStart) {
				choices = append(choices, "AS")
				aliasable = prefix != "" && !gosmo.IsReservedKeyword(prefix)
			}
		}
		if fabric && selectStart < 0 && complete && (clause == "FROM" || clause == "ON") {
			choices = append(choices, "SELECT")
		}
		if !f.window && (complete || clause == "ORDER" && direction) {
			switch clause {
			case "SELECT":
				if hasFrom {
					choices = append(choices, "WHERE", "GROUP BY", "ORDER BY")
				}
				fallthrough
			case "FROM", "WHERE", "ON", "GROUP", "HAVING":
				if namedWindow && (clause != "SELECT" || hasFrom) {
					choices = append(choices, "WINDOW")
				}
				if fabric {
					choices = append(choices, "QUALIFY")
				}
				fallthrough
			case "QUALIFY", "ORDER":
				if !fabric || queryDepth == 0 {
					choices = append(choices, "FOR JSON")
				}
				if !fabric {
					choices = append(choices, "FOR XML", "FOR BROWSE")
				}
			}
		}
	}

	if predicateClause(clause) && (clause != "QUALIFY" || fabric) {
		var only bool
		choices, _, only = predicateChoices(buf, context.clauseStart, from, choices)
		sourceComplete = sourceComplete || only
	}
	if statement != "" && statement != "SELECT" || f.predicate && selectStart < 0 && !hasFrom {
		joins := true
		if statement == "DELETE" {
			tail := f.tokens[statementStart+1:]
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
		choices = slices.DeleteFunc(choices, func(choice string) bool {
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
	if len(choices) == 0 {
		return nil, sourceComplete
	}

	// Only singleton clauses block later clauses. Functions, joins, boolean
	// operators and per-column modifiers can repeat at this nesting level.
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
	nextToken := keywordNextToken(buf, cursor)
	nextWord := ""
	if nextToken.Kind == sqltext.KindWord {
		nextWord = strings.ToUpper(string(buf[nextToken.Start:nextToken.End]))
	}
	prefixUpper := strings.ToUpper(prefix)
	for _, candidate := range choices {
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
		if context.apply && label == "VALUES" {
			items = append(items, editor.CompletionItem{Text: "(VALUES ())", Label: label, Detail: "table-valued constructor", CursorOffset: -2})
			continue
		}
		if function {
			detail := "function"
			if sourceFunctions {
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
		if jsonOptions && f.tokens[len(f.tokens)-1].Kind != sqlparse.TokenComma {
			text, before = ", "+text, jsonBefore
		}
		items = append(items, editor.CompletionItem{Text: text, Label: label, Detail: "keyword", ReplaceBefore: before,
			AppendSpace: !columnType && clause != "DATEPART", Soft: aliasable || prefix == ""})
	}
	return items, sourceComplete
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

func ddlCompletionTarget(tokens []sqlparse.Token, from int) (action, objectType string, tail []sqlparse.Token) {
	end := 0
	for end < len(tokens) && tokens[end].Start < from {
		end++
	}
	tokens = tokens[:end]
	if len(tokens) < 2 || !(keywordIs(tokens[0], "CREATE") || keywordIs(tokens[0], "ALTER") || keywordIs(tokens[0], "DROP")) {
		return "", "", nil
	}
	action, at := strings.ToUpper(tokens[0].Text), 1
	if action == "CREATE" && len(tokens) >= 3 && keywordIs(tokens[1], "OR") && keywordIs(tokens[2], "ALTER") {
		action, at = "ALTER", 3
	}
	if at >= len(tokens) || tokens[at].Quoted {
		return "", "", nil
	}
	objectType = strings.ToUpper(tokens[at].Text)
	if objectType == "PROC" {
		objectType = "PROCEDURE"
	}
	switch objectType {
	case "TABLE", "VIEW", "PROCEDURE", "FUNCTION":
		return action, objectType, tokens[at+1:]
	case "SCHEMA", "INDEX", "DATABASE", "TRIGGER", "SEQUENCE", "TYPE", "SYNONYM":
		if action == "DROP" {
			return action, objectType, tokens[at+1:]
		}
	}
	return "", "", nil
}

func ddlColumnTypeChoices(tokens []sqlparse.Token, fabric bool) ([]string, bool) {
	open := slices.IndexFunc(tokens, func(tok sqlparse.Token) bool { return tok.Kind == sqlparse.TokenParenOpen })
	if open < 1 || !keywordAliasPosition(tokens[:open], 0) {
		return nil, false
	}
	return ddlColumnDefinitionChoices(tokens[open+1:], fabric)
}

func ddlColumnDefinitionChoices(tokens []sqlparse.Token, fabric bool) ([]string, bool) {
	depth, start := 1, 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i].Kind {
		case sqlparse.TokenParenOpen:
			depth++
		case sqlparse.TokenParenClose:
			depth--
			if depth == 0 {
				return nil, false
			}
		case sqlparse.TokenComma:
			if depth == 1 {
				start = i + 1
			}
		}
	}
	column := tokens[start:]
	if depth > 1 {
		return ddlMaxLengthChoices(column), true
	}
	if depth != 1 || len(column) == 0 || column[0].Kind != sqlparse.TokenIdent ||
		keywordIs(column[0], "CONSTRAINT") || keywordIs(column[0], "PRIMARY") || keywordIs(column[0], "UNIQUE") ||
		keywordIs(column[0], "FOREIGN") || keywordIs(column[0], "CHECK") {
		return nil, true
	}
	types := ddlDataTypes(fabric)
	if len(column) == 1 {
		return types, true
	}
	if !slices.ContainsFunc(types, func(name string) bool { return keywordIs(column[1], strings.TrimSuffix(name, "(")) }) {
		return nil, true
	}
	end := 2
	if end < len(column) && column[end].Kind == sqlparse.TokenParenOpen {
		close := slices.IndexFunc(column[end+1:], func(tok sqlparse.Token) bool { return tok.Kind == sqlparse.TokenParenClose })
		if close < 0 {
			return nil, true
		}
		end += close + 2
	}
	if end == len(column) {
		return []string{"NULL", "NOT NULL"}, true
	}
	if end+1 == len(column) && keywordIs(column[end], "NOT") {
		return []string{"NULL"}, true
	}
	return nil, true
}

func ddlDataTypes(fabric bool) []string {
	types := []string{"BIGINT", "BIT", "CHAR(", "DATE", "DATETIME2", "DECIMAL(", "FLOAT", "INT", "NUMERIC(",
		"REAL", "SMALLINT", "TIME", "UNIQUEIDENTIFIER", "VARBINARY(", "VARCHAR("}
	if !fabric {
		types = append(types, "BINARY(", "DATETIME", "DATETIMEOFFSET", "IMAGE", "MONEY", "NCHAR(", "NTEXT",
			"NVARCHAR(", "ROWVERSION", "SMALLDATETIME", "SMALLMONEY", "SQL_VARIANT", "TEXT", "TINYINT", "XML")
	}
	return types
}

func routineCompletionTokens(buf []rune, pre sqlparse.PrefixScan, from int) []sqlparse.Token {
	head := keywordNextToken(buf[:from], pre.GoStart)
	if !keywordNextIs(buf[:from], head.Start, "CREATE") && !keywordNextIs(buf[:from], head.Start, "ALTER") {
		return nil
	}
	at := head.End
	// Split, not one "CREATE OR ALTER" literal: this matches typed text and
	// emits nothing, which TestOnlyKnownSitesEmitCreateOrAlter cannot tell.
	if strings.EqualFold(string(buf[head.Start:head.End]), "CREATE") && keywordNextIs(buf[:from], at, "OR ALTER") {
		at = keywordNextToken(buf[:from], keywordNextToken(buf[:from], at).End).End
	}
	if !keywordNextIs(buf[:from], at, "PROCEDURE") && !keywordNextIs(buf[:from], at, "PROC") &&
		!keywordNextIs(buf[:from], at, "FUNCTION") {
		return nil
	}
	if len(pre.Tokens) > 0 && pre.Tokens[0].Start == head.Start {
		end := 0
		for end < len(pre.Tokens) && pre.Tokens[end].Start < from {
			end++
		}
		return pre.Tokens[:end]
	}
	// PrefixCache narrows to a body statement, not the enclosing routine.
	// Reconstruct only routine batches; ordinary queries stay cached.
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, pre.GoStart, from, false)
	return tokens
}

func ddlMaxLengthChoices(tokens []sqlparse.Token) []string {
	if len(tokens) < 2 || tokens[len(tokens)-1].Kind != sqlparse.TokenParenOpen {
		return nil
	}
	for _, typ := range []string{"VARCHAR", "NVARCHAR", "VARBINARY"} {
		if keywordIs(tokens[len(tokens)-2], typ) {
			return []string{"MAX"}
		}
	}
	return nil
}

func ddlParameterChoices(buf []rune, tokens []sqlparse.Token, from int, function, parenthesized, fabric bool) ([]string, bool) {
	start, depth := 0, 0
	for i, tok := range tokens {
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			depth++
		case sqlparse.TokenParenClose:
			depth--
		case sqlparse.TokenComma:
			if depth == 0 {
				start = i + 1
			}
		}
	}
	param := tokens[start:]
	if len(param) == 0 || !sqlparse.HasSigil(param[0].Text) || param[0].Quoted {
		return nil, false
	}
	types := slices.DeleteFunc(ddlDataTypes(fabric), func(name string) bool {
		return name == "IMAGE" || name == "NTEXT" || name == "TEXT" || name == "ROWVERSION"
	})
	at := 1
	if at < len(param) && keywordIs(param[at], "AS") {
		at++
	}
	if at == len(param) {
		return types, true
	}
	if depth > 0 {
		return ddlMaxLengthChoices(param[at:]), true
	}
	complete, last := keywordExpressionComplete(buf, keywordNextToken(buf, param[at].Start).End, from, "VALUE")
	tail := keywordNextToken(buf[:from], keywordNextToken(buf, param[at].Start).End)
	if tail.Kind != sqltext.KindEnd && !complete {
		return nil, false
	}
	if function {
		return nil, false
	}
	choices := []string{"AS"}
	if parenthesized {
		choices = nil
	}
	if !keywordIs(param[len(param)-1], "OUTPUT") && !keywordIs(param[len(param)-1], "OUT") &&
		!(last.Kind == sqltext.KindWord && strings.EqualFold(string(buf[last.Start:last.End]), "READONLY")) {
		choices = append(choices, "OUTPUT", "OUT")
	}
	return choices, false
}

// Only routine signatures and their first body keywords; SQL inside the body
// continues through the normal query completion path.
func ddlRoutineChoices(buf []rune, tail []sqlparse.Token, objectType string, from int, fabric bool) ([]string, bool, bool) {
	if len(tail) == 0 || tail[0].Kind != sqlparse.TokenIdent {
		return nil, false, false
	}
	nameEnd := 1
	for nameEnd+1 < len(tail) && tail[nameEnd].Kind == sqlparse.TokenDot && tail[nameEnd+1].Kind == sqlparse.TokenIdent {
		nameEnd += 2
	}
	if nameEnd < len(tail) && tail[nameEnd].Kind == sqlparse.TokenDot {
		return nil, false, false
	}
	tail = tail[nameEnd:]
	function := objectType == "FUNCTION"
	depth, returns, as := 0, -1, -1
	options, optionExpected := false, false
	for i, tok := range tail {
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			depth++
		case sqlparse.TokenParenClose:
			depth--
		default:
			if depth == 0 {
				if function && returns >= 0 && i > returns+1 && !optionExpected &&
					(keywordIs(tok, "BEGIN") || keywordIs(tok, "RETURN") && keywordIs(tail[returns+1], "TABLE")) {
					as = i - 1
					break
				}
				if keywordIs(tok, "WITH") {
					options, optionExpected = true, true
					continue
				}
				if options {
					if tok.Kind == sqlparse.TokenComma && !optionExpected {
						optionExpected = true
						continue
					}
					if optionExpected && (keywordIs(tok, "SCHEMABINDING") ||
						keywordIs(tok, "ENCRYPTION") || keywordIs(tok, "RECOMPILE")) {
						optionExpected = false
						continue
					}
					if !keywordIs(tok, "AS") || optionExpected {
						return nil, false, false
					}
				}
				if keywordIs(tok, "RETURNS") {
					returns = i
				}
				parameterAS := i > 0 && sqlparse.HasSigil(tail[i-1].Text) &&
					(i == 1 || tail[i-2].Kind == sqlparse.TokenComma)
				if keywordIs(tok, "AS") && !parameterAS {
					as = i
				}
			}
		}
		if as >= 0 {
			break
		}
	}
	if options && optionExpected {
		return nil, false, true
	}
	if options && as < 0 {
		return []string{"AS"}, false, true
	}
	inline := returns >= 0 && returns+1 < len(tail) && keywordIs(tail[returns+1], "TABLE")
	if as >= 0 {
		body := tail[as+1:]
		if len(body) == 0 {
			if function && inline {
				return []string{"RETURN"}, false, true
			}
			if !function {
				return []string{"BEGIN", "SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "EXEC", "EXECUTE", "RETURN", "SET", "WITH"}, false, true
			}
			return []string{"BEGIN"}, false, true
		}
		if len(body) == 1 && keywordIs(body[0], "BEGIN") {
			if function && !sqlparse.HasSigil(tail[returns+1].Text) {
				return []string{"DECLARE", "SET", "SELECT", "RETURN"}, false, true
			}
			if function {
				return []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "SET", "RETURN"}, false, true
			}
			return []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "EXEC", "EXECUTE", "RETURN", "SET", "WITH", "END"}, false, true
		}
		if function && inline && keywordIs(body[0], "RETURN") &&
			(len(body) == 1 || len(body) == 2 && body[1].Kind == sqlparse.TokenParenOpen) {
			return []string{"SELECT"}, false, true
		}
		if !inline && len(body) > 0 && keywordIs(body[0], "BEGIN") {
			blocks, cases, parens, lastReturn := 0, 0, 0, -1
			for i, tok := range body {
				switch tok.Kind {
				case sqlparse.TokenParenOpen:
					parens++
				case sqlparse.TokenParenClose:
					parens--
				}
				if keywordIs(tok, "CASE") {
					cases++
				} else if keywordIs(tok, "RETURN") && cases == 0 && parens == 0 {
					lastReturn = i
				} else if keywordIs(tok, "BEGIN") {
					transaction := i+1 < len(body) && (keywordIs(body[i+1], "TRAN") ||
						keywordIs(body[i+1], "TRANSACTION") || keywordIs(body[i+1], "DISTRIBUTED"))
					if !transaction {
						blocks++
					}
				} else if keywordIs(tok, "END") {
					if cases > 0 {
						cases--
					} else {
						blocks--
					}
				}
			}
			if blocks <= 0 {
				return nil, false, false
			}
			if function && cases == 0 && parens == 0 && lastReturn >= 0 &&
				keywordStatementStart(buf, body[lastReturn].Start, from) == body[lastReturn].Start {
				complete, last := keywordExpressionComplete(buf, body[lastReturn].Start+len("RETURN"), from, "VALUE")
				if complete || returns+1 < len(tail) && sqlparse.HasSigil(tail[returns+1].Text) {
					return []string{"END"}, false, true
				}
				if last.Kind != sqltext.KindPunct || buf[last.Start] != ';' {
					return scalarCompletions, false, true
				}
			}
			last := keywordNextToken(buf, body[len(body)-1].Start)
			for next := keywordNextToken(buf[:from], last.End); next.Kind != sqltext.KindEnd; next = keywordNextToken(buf[:from], last.End) {
				last = next
			}
			if blocks > 0 && cases == 0 && last.Kind == sqltext.KindPunct && buf[last.Start] == ';' {
				choices := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "SET", "RETURN", "END"}
				if !function {
					choices = append(choices, "EXEC", "EXECUTE", "WITH")
				}
				return choices, false, true
			}
			if blocks > 0 && cases == 0 && from < len(buf) && sqltext.IsWordRune(buf[from]) &&
				strings.HasPrefix("END", strings.ToUpper(string(buf[from:keywordNextToken(buf, from).End]))) {
				last := body[len(body)-1]
				start := last.Start
				if keywordIs(last, "RETURN") {
					start += len("RETURN")
				}
				if complete, _ := keywordExpressionComplete(buf, start, from, "VALUE"); complete {
					return []string{"END"}, false, true
				}
			}
		}
		return nil, false, false
	}
	types := slices.DeleteFunc(ddlDataTypes(fabric), func(name string) bool {
		return name == "IMAGE" || name == "NTEXT" || name == "TEXT" || name == "ROWVERSION"
	})
	if returns >= 0 {
		result := tail[returns+1:]
		if len(result) == 0 {
			return append(types, "TABLE"), true, true
		}
		if sqlparse.HasSigil(result[0].Text) {
			if len(result) == 1 {
				return []string{"TABLE("}, true, true
			}
			if len(result) < 3 || !keywordIs(result[1], "TABLE") || result[2].Kind != sqlparse.TokenParenOpen {
				return nil, false, true
			}
			if depth > 0 {
				choices, _ := ddlColumnDefinitionChoices(result[3:], fabric)
				return choices, true, true
			}
		}
		if depth == 0 {
			return []string{"AS"}, false, true
		}
		return ddlMaxLengthChoices(result), true, true
	}
	if depth > 0 {
		if tail[0].Kind == sqlparse.TokenParenOpen {
			choices, datatype := ddlParameterChoices(buf, tail[1:], from, function, true, fabric)
			return choices, datatype, true
		}
		choices, datatype := ddlParameterChoices(buf, tail, from, function, false, fabric)
		return choices, datatype, true
	}
	if function {
		if len(tail) > 0 && tail[len(tail)-1].Kind == sqlparse.TokenParenClose {
			return []string{"RETURNS"}, false, true
		}
		return nil, false, true
	}
	if len(tail) == 0 {
		return []string{"AS"}, false, true
	}
	if tail[0].Kind == sqlparse.TokenParenOpen && tail[len(tail)-1].Kind == sqlparse.TokenParenClose {
		return []string{"AS"}, false, true
	}
	choices, datatype := ddlParameterChoices(buf, tail, from, function, false, fabric)
	return choices, datatype, true
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

type builtinKind uint16

const (
	builtinScalar builtinKind = 1 << iota
	builtinAggregate
	builtinFabricAggregate
	builtinWindow
	builtinTable
	builtinPredicate
	builtinOrdered
	builtinNoArgs
	builtinPartitionOnly
	builtinWarehouseOnly
	builtinBare
)

type completionBuiltin struct {
	name        string
	kind        builtinKind
	since       int
	noWarehouse bool
}

// Kind, call shape and availability belong to the same entry. The slices
// below are indexes, not independent inventories to keep in sync.
var builtinDefinitions = func() []completionBuiltin {
	defs := []completionBuiltin{
		{"COUNT", builtinAggregate | builtinWindow, 0, false},
		{"COUNT_BIG", builtinAggregate | builtinWindow, 0, false},
		{"AVG", builtinAggregate | builtinWindow, 0, false},
		{"SUM", builtinAggregate | builtinWindow, 0, false},
		{"MIN", builtinAggregate | builtinWindow, 0, false},
		{"MAX", builtinAggregate | builtinWindow, 0, false},
		{"STRING_AGG", builtinAggregate, 14, false},
		{"APPROX_COUNT_DISTINCT", builtinAggregate, 15, false},
		{"APPROX_PERCENTILE_CONT", builtinAggregate | builtinOrdered, 16, false},
		{"APPROX_PERCENTILE_DISC", builtinAggregate | builtinOrdered, 16, false},
		{"JSON_ARRAYAGG", builtinAggregate, 17, false},
		{"JSON_OBJECTAGG", builtinAggregate, 17, false},
		{"ANY_VALUE", builtinFabricAggregate | builtinWindow | builtinWarehouseOnly, 0, false},
		{"PRODUCT", builtinFabricAggregate | builtinWindow | builtinWarehouseOnly, 0, false},
		{"MEDIAN", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"QUANTILE", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"APPROX_MEDIAN", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"APPROX_QUANTILE", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"PERCENTILE_CONT", builtinFabricAggregate | builtinWindow | builtinOrdered | builtinPartitionOnly, 0, false},
		{"PERCENTILE_DISC", builtinFabricAggregate | builtinWindow | builtinOrdered | builtinPartitionOnly, 0, false},
		{"VAR", builtinAggregate | builtinWindow, 0, false},
		{"VARP", builtinAggregate | builtinWindow, 0, false},
		{"STDEV", builtinAggregate | builtinWindow, 0, false},
		{"STDEVP", builtinAggregate | builtinWindow, 0, false},
		{"CHECKSUM_AGG", builtinAggregate | builtinWindow, 0, false},
		{"GROUPING", builtinAggregate, 0, false},
		{"GROUPING_ID", builtinAggregate, 0, false},
		{"ROW_NUMBER", builtinWindow | builtinNoArgs, 0, false},
		{"RANK", builtinWindow | builtinNoArgs, 0, false},
		{"DENSE_RANK", builtinWindow | builtinNoArgs, 0, false},
		{"PERCENT_RANK", builtinWindow | builtinNoArgs, 0, false},
		{"CUME_DIST", builtinWindow | builtinNoArgs, 0, false},
		{"NTILE", builtinWindow, 0, false},
		{"LAG", builtinWindow, 0, false},
		{"LEAD", builtinWindow, 0, false},
		{"FIRST_VALUE", builtinWindow, 0, false},
		{"LAST_VALUE", builtinWindow, 0, false},
		{"ABS", builtinScalar, 0, false},
		{"CEILING", builtinScalar, 0, false},
		{"FLOOR", builtinScalar, 0, false},
		{"POWER", builtinScalar, 0, false},
		{"ROUND", builtinScalar, 0, false},
		{"SQRT", builtinScalar, 0, false},
		{"CHARINDEX", builtinScalar, 0, false},
		{"CONCAT", builtinScalar, 0, false},
		{"CONCAT_WS", builtinScalar, 14, false},
		{"FORMAT", builtinScalar, 0, false},
		{"LEFT", builtinScalar, 0, false},
		{"LEN", builtinScalar, 0, false},
		{"LOWER", builtinScalar, 0, false},
		{"LTRIM", builtinScalar, 0, false},
		{"REPLACE", builtinScalar, 0, false},
		{"REPLICATE", builtinScalar, 0, false},
		{"REVERSE", builtinScalar, 0, false},
		{"RIGHT", builtinScalar, 0, false},
		{"RTRIM", builtinScalar, 0, false},
		{"STUFF", builtinScalar, 0, false},
		{"SUBSTRING", builtinScalar, 0, false},
		{"TRIM", builtinScalar, 14, false},
		{"UPPER", builtinScalar, 0, false},
		{"CAST", builtinScalar, 0, false},
		{"COALESCE", builtinScalar, 0, false},
		{"CONVERT", builtinScalar, 0, false},
		{"ISNULL", builtinScalar, 0, false},
		{"NULLIF", builtinScalar, 0, false},
		{"NULL", builtinScalar | builtinBare, 0, false},
		{"TRY_CAST", builtinScalar, 0, false},
		{"TRY_CONVERT", builtinScalar, 0, false},
		{"DATEADD", builtinScalar, 0, false},
		{"DATEDIFF", builtinScalar, 0, false},
		{"DATENAME", builtinScalar, 0, false},
		{"DATEPART", builtinScalar, 0, false},
		{"DAY", builtinScalar, 0, false},
		{"GETDATE", builtinScalar | builtinNoArgs, 0, false},
		{"GETUTCDATE", builtinScalar | builtinNoArgs, 0, false},
		{"MONTH", builtinScalar, 0, false},
		{"YEAR", builtinScalar, 0, false},
		{"ISJSON", builtinScalar, 0, false},
		{"JSON_QUERY", builtinScalar, 0, false},
		{"JSON_VALUE", builtinScalar, 0, false},
		{"JSON_MODIFY", builtinScalar, 0, false},
		{"JSON_ARRAY", builtinScalar, 16, false},
		{"JSON_OBJECT", builtinScalar, 16, false},
		{"JSON_PATH_EXISTS", builtinScalar, 16, false},
		{"JSON_CONTAINS", builtinScalar, 17, true},
		{"DATE_BUCKET", builtinScalar, 16, false},
		{"DATETRUNC", builtinScalar, 16, false},
		{"DATEDIFF_BIG", builtinScalar, 0, false},
		{"EOMONTH", builtinScalar, 0, false},
		{"SYSDATETIME", builtinScalar | builtinNoArgs, 0, false},
		{"SYSUTCDATETIME", builtinScalar | builtinNoArgs, 0, false},
		{"SYSDATETIMEOFFSET", builtinScalar | builtinNoArgs, 0, false},
		{"PI", builtinScalar | builtinNoArgs, 0, false},
		{"NEWID", builtinScalar | builtinNoArgs, 0, false},
		{"APP_NAME", builtinScalar | builtinNoArgs, 0, false},
		{"HOST_ID", builtinScalar | builtinNoArgs, 0, false},
		{"HOST_NAME", builtinScalar | builtinNoArgs, 0, false},
		{"ORIGINAL_LOGIN", builtinScalar | builtinNoArgs, 0, false},
		{"CURRENT_TIMESTAMP", builtinScalar | builtinBare, 0, false},
		{"CURRENT_USER", builtinScalar | builtinBare, 0, false},
		{"SESSION_USER", builtinScalar | builtinBare, 0, false},
		{"SYSTEM_USER", builtinScalar | builtinBare, 0, false},
		{"USER", builtinScalar | builtinBare, 0, false},
		{"IS_SRVROLEMEMBER", builtinScalar, 0, true},
		{"TRANSLATE", builtinScalar, 14, false},
		{"GREATEST", builtinScalar, 16, false},
		{"LEAST", builtinScalar, 16, false},
		{"BIT_COUNT", builtinScalar, 16, false},
		{"GET_BIT", builtinScalar, 16, false},
		{"SET_BIT", builtinScalar, 16, false},
		{"LEFT_SHIFT", builtinScalar, 16, false},
		{"RIGHT_SHIFT", builtinScalar, 16, false},
		{"REGEXP_COUNT", builtinScalar, 17, false},
		{"REGEXP_INSTR", builtinScalar, 17, false},
		{"REGEXP_REPLACE", builtinScalar, 17, false},
		{"REGEXP_SUBSTR", builtinScalar, 17, false},
		{"EDIT_DISTANCE", builtinScalar, 17, true},
		{"EDIT_DISTANCE_SIMILARITY", builtinScalar, 17, true},
		{"JARO_WINKLER_DISTANCE", builtinScalar, 17, true},
		{"JARO_WINKLER_SIMILARITY", builtinScalar, 17, true},
		{"BASE64_ENCODE", builtinScalar, 17, false},
		{"BASE64_DECODE", builtinScalar, 17, false},
		{"VECTOR_DISTANCE", builtinScalar, 17, true},
		{"VECTOR_NORM", builtinScalar, 17, true},
		{"VECTOR_NORMALIZE", builtinScalar, 17, true},
		{"VECTORPROPERTY", builtinScalar, 17, true},
		{"OPENJSON", builtinTable, 0, false},
		{"OPENROWSET", builtinTable, 0, false},
		{"STRING_SPLIT", builtinTable, 0, false},
		{"GENERATE_SERIES", builtinTable, 16, false},
		{"REGEXP_MATCHES", builtinTable, 17, true},
		{"REGEXP_SPLIT_TO_TABLE", builtinTable, 17, true},
		{"VECTOR_SEARCH", builtinTable, 17, true},
		{"EXISTS", builtinPredicate, 0, false},
		{"NOT EXISTS", builtinPredicate, 0, false},
		{"REGEXP_LIKE", builtinPredicate, 17, false},
		{"CONTAINS", builtinPredicate, 0, true},
		{"FREETEXT", builtinPredicate, 0, true},
		{"||", 0, 17, false},
	}
	for _, name := range strings.Fields(
		"ACOS ASIN ATAN ATN2 COS COT DEGREES EXP LOG LOG10 RADIANS RAND SIGN SIN SQUARE TAN " +
			"ASCII CHAR DATALENGTH DIFFERENCE NCHAR PATINDEX QUOTENAME SOUNDEX SPACE STR STRING_ESCAPE UNICODE " +
			"DATEFROMPARTS DATETIMEFROMPARTS DATETIME2FROMPARTS DATETIMEOFFSETFROMPARTS " +
			"SMALLDATETIMEFROMPARTS TIMEFROMPARTS ISDATE ISNUMERIC SWITCHOFFSET TODATETIMEOFFSET " +
			"DB_ID DB_NAME SCHEMA_ID SCHEMA_NAME OBJECT_ID OBJECT_NAME OBJECT_SCHEMA_NAME OBJECT_DEFINITION " +
			"OBJECTPROPERTY OBJECTPROPERTYEX COLUMNPROPERTY COL_LENGTH COL_NAME TYPE_ID TYPE_NAME TYPEPROPERTY " +
			"DATABASEPROPERTYEX SERVERPROPERTY CONNECTIONPROPERTY SESSION_CONTEXT " +
			"USER_ID USER_NAME SUSER_ID SUSER_NAME SUSER_SID SUSER_SNAME IS_MEMBER IS_ROLEMEMBER HAS_PERMS_BY_NAME " +
			"HASHBYTES CHECKSUM BINARY_CHECKSUM") {
		defs = append(defs, completionBuiltin{name, builtinScalar, 0, false})
	}
	for _, typ := range []string{"geometry", "geography"} {
		for _, method := range spatialConstructors {
			defs = append(defs, completionBuiltin{typ + "::" + strings.TrimSuffix(method, "("), builtinScalar, 0, false})
		}
	}
	return defs
}()

var completionBuiltins = func() map[string]completionBuiltin {
	index := make(map[string]completionBuiltin, len(builtinDefinitions))
	for _, builtin := range builtinDefinitions {
		index[builtin.name] = builtin
	}
	return index
}()

func builtinCompletionNames(kind builtinKind) []string {
	var names []string
	for _, builtin := range builtinDefinitions {
		if builtin.kind&kind != 0 {
			names = append(names, builtin.name+"(")
		}
	}
	return names
}

var (
	scalarCompletions          = builtinCompletionNames(builtinScalar)
	aggregateCompletions       = builtinCompletionNames(builtinAggregate)
	fabricAggregateCompletions = builtinCompletionNames(builtinAggregate | builtinFabricAggregate)
	projectionCompletions      = builtinCompletionNames(builtinAggregate | builtinFabricAggregate | builtinWindow)
	windowCompletions          = builtinCompletionNames(builtinWindow)
	tableValuedCompletions     = builtinCompletionNames(builtinTable)
	predicateCompletions       = builtinCompletionNames(builtinPredicate)
)
