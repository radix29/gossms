package tui

import (
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
)

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
	// JSON and VECTOR are listed for every edition and gated by version and
	// edition in builtinDefinitions (builtinDataType).
	types := []string{"BIGINT", "BIT", "CHAR(", "DATE", "DATETIME2", "DECIMAL(", "FLOAT", "INT", "JSON", "NUMERIC(",
		"REAL", "SMALLINT", "TIME", "UNIQUEIDENTIFIER", "VARBINARY(", "VARCHAR(", "VECTOR("}
	if !fabric {
		types = append(types, "BINARY(", "DATETIME", "DATETIMEOFFSET", "GEOGRAPHY", "GEOMETRY", "HIERARCHYID",
			"IMAGE", "MONEY", "NCHAR(", "NTEXT", "NVARCHAR(", "ROWVERSION", "SMALLDATETIME", "SMALLMONEY",
			"SQL_VARIANT", "SYSNAME", "TEXT", "TINYINT", "XML")
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
	h, ok := scanRoutineHeader(tail, function)
	if !ok {
		return nil, false, false
	}
	if h.options && h.optionExpected {
		return nil, false, true
	}
	if h.options && h.as < 0 {
		return []string{"AS"}, false, true
	}
	if h.as >= 0 {
		choices, handled := ddlRoutineBodyChoices(buf, tail, h, from, function)
		return choices, false, handled
	}
	return ddlRoutineSignatureChoices(buf, tail, h, from, function, fabric)
}

// routineHeader locates a routine header's RETURNS and its body's AS (-1
// when absent) in tail, the tokens after the routine name; depth is the
// paren nesting at the end, options a WITH list (optionExpected: an option
// is still due).
type routineHeader struct {
	depth, returns, as      int
	options, optionExpected bool
}

// scanRoutineHeader is false where tail is not a routine header this
// completion understands (an unknown WITH option).
func scanRoutineHeader(tail []sqlparse.Token, function bool) (h routineHeader, ok bool) {
	h.returns, h.as = -1, -1
	for i, tok := range tail {
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			h.depth++
		case sqlparse.TokenParenClose:
			h.depth--
		default:
			if h.depth == 0 {
				if function && h.returns >= 0 && i > h.returns+1 && !h.optionExpected &&
					(keywordIs(tok, "BEGIN") || keywordIs(tok, "RETURN") && keywordIs(tail[h.returns+1], "TABLE")) {
					h.as = i - 1
					break
				}
				if keywordIs(tok, "WITH") {
					h.options, h.optionExpected = true, true
					continue
				}
				if h.options {
					if tok.Kind == sqlparse.TokenComma && !h.optionExpected {
						h.optionExpected = true
						continue
					}
					if h.optionExpected && (keywordIs(tok, "SCHEMABINDING") ||
						keywordIs(tok, "ENCRYPTION") || keywordIs(tok, "RECOMPILE")) {
						h.optionExpected = false
						continue
					}
					if !keywordIs(tok, "AS") || h.optionExpected {
						return h, false
					}
				}
				if keywordIs(tok, "RETURNS") {
					h.returns = i
				}
				parameterAS := i > 0 && sqlparse.HasSigil(tail[i-1].Text) &&
					(i == 1 || tail[i-2].Kind == sqlparse.TokenComma)
				if keywordIs(tok, "AS") && !parameterAS {
					h.as = i
				}
			}
		}
		if h.as >= 0 {
			break
		}
	}
	return h, true
}

func (h routineHeader) inline(tail []sqlparse.Token) bool {
	return h.returns >= 0 && h.returns+1 < len(tail) && keywordIs(tail[h.returns+1], "TABLE")
}

// ddlRoutineBodyChoices completes the first keywords after a routine's AS:
// RETURN for an inline function, BEGIN, the statements a BEGIN block opens
// with, and END where the block can close.
func ddlRoutineBodyChoices(buf []rune, tail []sqlparse.Token, h routineHeader, from int, function bool) ([]string, bool) {
	inline := h.inline(tail)
	body := tail[h.as+1:]
	if len(body) == 0 {
		if function && inline {
			return []string{"RETURN"}, true
		}
		if !function {
			return []string{"BEGIN", "SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "EXEC", "EXECUTE", "RETURN", "SET", "WITH"}, true
		}
		return []string{"BEGIN"}, true
	}
	if len(body) == 1 && keywordIs(body[0], "BEGIN") {
		if function && !sqlparse.HasSigil(tail[h.returns+1].Text) {
			return []string{"DECLARE", "SET", "SELECT", "RETURN"}, true
		}
		if function {
			return []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "SET", "RETURN"}, true
		}
		return []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DECLARE", "EXEC", "EXECUTE", "RETURN", "SET", "WITH", "END"}, true
	}
	if function && inline && keywordIs(body[0], "RETURN") &&
		(len(body) == 1 || len(body) == 2 && body[1].Kind == sqlparse.TokenParenOpen) {
		return []string{"SELECT"}, true
	}
	if !inline && len(body) > 0 && keywordIs(body[0], "BEGIN") {
		return ddlRoutineBlockChoices(buf, tail, body, h.returns, from, function)
	}
	return nil, false
}

// ddlRoutineBlockChoices completes inside an open BEGIN … END body: the next
// statement after a ';', a function's RETURN value, and END.
func ddlRoutineBlockChoices(buf []rune, tail, body []sqlparse.Token, returns, from int, function bool) ([]string, bool) {
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
		return nil, false
	}
	if function && cases == 0 && parens == 0 && lastReturn >= 0 &&
		keywordStatementStart(buf, body[lastReturn].Start, from) == body[lastReturn].Start {
		complete, last := keywordExpressionComplete(buf, body[lastReturn].Start+len("RETURN"), from, "VALUE")
		if complete || returns+1 < len(tail) && sqlparse.HasSigil(tail[returns+1].Text) {
			return []string{"END"}, true
		}
		if last.Kind != sqltext.KindPunct || buf[last.Start] != ';' {
			return scalarCompletions, true
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
		return choices, true
	}
	if blocks > 0 && cases == 0 && from < len(buf) && sqltext.IsWordRune(buf[from]) &&
		strings.HasPrefix("END", strings.ToUpper(string(buf[from:keywordNextToken(buf, from).End]))) {
		last := body[len(body)-1]
		start := last.Start
		if keywordIs(last, "RETURN") {
			start += len("RETURN")
		}
		if complete, _ := keywordExpressionComplete(buf, start, from, "VALUE"); complete {
			return []string{"END"}, true
		}
	}
	return nil, false
}

// ddlRoutineSignatureChoices completes a routine header before its AS: the
// parameter list, RETURNS and its type, and AS itself.
func ddlRoutineSignatureChoices(buf []rune, tail []sqlparse.Token, h routineHeader, from int, function, fabric bool) ([]string, bool, bool) {
	types := slices.DeleteFunc(ddlDataTypes(fabric), func(name string) bool {
		return name == "IMAGE" || name == "NTEXT" || name == "TEXT" || name == "ROWVERSION"
	})
	if h.returns >= 0 {
		result := tail[h.returns+1:]
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
			if h.depth > 0 {
				choices, _ := ddlColumnDefinitionChoices(result[3:], fabric)
				return choices, true, true
			}
		}
		if h.depth == 0 {
			return []string{"AS"}, false, true
		}
		return ddlMaxLengthChoices(result), true, true
	}
	if h.depth > 0 {
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
