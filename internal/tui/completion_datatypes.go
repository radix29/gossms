package tui

import (
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
)

// Data-type positions outside CREATE TABLE and routine headers (DECLARE,
// CAST/CONVERT, ALTER TABLE columns, CREATE TYPE … FROM), answered with the
// type lists and ddlTypes of completion_ddl.go.

// dataTypeKind names a data-type position outside CREATE TABLE and routine
// headers (dataTypePosition).
type dataTypeKind int

const (
	dataTypeVariable      dataTypeKind = iota + 1 // DECLARE @v [AS] ‸, each comma-separated @v after it
	dataTypeTableVariable                         // DECLARE @t TABLE ( column definitions
	dataTypeColumn                                // ALTER TABLE … ADD c ‸, ALTER TABLE … ALTER COLUMN c ‸
	dataTypeConversion                            // CAST(… AS ‸, TRY_CAST, CONVERT(‸, TRY_CONVERT
	dataTypeAliasBase                             // CREATE TYPE s.n FROM ‸
)

// dataTypePosition finds the data-type position the tokens before from end
// in, beside ddlCompletionTarget's CREATE TABLE and routine headers. rest is
// what follows the position's lead-in: from where the type goes (variable,
// conversion, alias base), or the column definitions (table variable,
// column; the column name first, ddlColumnDefinitionChoices's shape).
func dataTypePosition(buf []rune, tokens []sqlparse.Token, from int) (kind dataTypeKind, rest []sqlparse.Token) {
	end := 0
	for end < len(tokens) && tokens[end].Start < from {
		end++
	}
	tokens = tokens[:end]
	if rest, ok := conversionTypeTokens(tokens); ok {
		return dataTypeConversion, rest
	}
	if len(tokens) >= 2 && keywordIs(tokens[0], "CREATE") && keywordIs(tokens[1], "TYPE") {
		if nameEnd := ddlNameEnd(tokens, 2); nameEnd < len(tokens) && keywordIs(tokens[nameEnd], "FROM") {
			return dataTypeAliasBase, tokens[nameEnd+1:]
		}
		return 0, nil
	}
	if action, objectType, tail := ddlCompletionTarget(tokens, from); action == "ALTER" && objectType == "TABLE" {
		if nameEnd := ddlNameEnd(tail, 0); nameEnd > 0 && nameEnd < len(tail) {
			switch {
			case keywordIs(tail[nameEnd], "ADD"):
				return dataTypeColumn, tail[nameEnd+1:]
			case nameEnd+1 < len(tail) && keywordIs(tail[nameEnd], "ALTER") && keywordIs(tail[nameEnd+1], "COLUMN"):
				return dataTypeColumn, tail[nameEnd+2:]
			}
		}
		return 0, nil
	}
	return declareTypePosition(buf, tokens, from)
}

// qualifiedDataTypePosition reports "schema.‸" where a dataTypePosition's
// type goes, for qualifiedTypeCandidates.
func qualifiedDataTypePosition(buf []rune, tokens []sqlparse.Token, from int) bool {
	end := 0
	for end < len(tokens) && tokens[end].Start < from {
		end++
	}
	if end < 2 || tokens[end-1].Kind != sqlparse.TokenDot || tokens[end-2].Kind != sqlparse.TokenIdent {
		return false
	}
	kind, rest := dataTypePosition(buf, tokens, tokens[end-2].Start)
	switch kind {
	case dataTypeVariable, dataTypeConversion, dataTypeAliasBase:
		return len(rest) == 0
	case dataTypeTableVariable, dataTypeColumn:
		return true // qualifiedTypeCandidates asks the keyword path whether a type goes there
	}
	return false
}

// ddlNameEnd is the index after the dotted name starting at tokens[at], at
// itself when none does.
func ddlNameEnd(tokens []sqlparse.Token, at int) int {
	if at >= len(tokens) || tokens[at].Kind != sqlparse.TokenIdent {
		return at
	}
	end := at + 1
	for end+1 < len(tokens) && tokens[end].Kind == sqlparse.TokenDot && tokens[end+1].Kind == sqlparse.TokenIdent {
		end += 2
	}
	return end
}

// conversionTypeTokens is the type argument a CAST/TRY_CAST (after its AS) or
// CONVERT/TRY_CONVERT (before its first comma) the cursor is in has so far,
// looking through a "(length)" being typed after it.
func conversionTypeTokens(tokens []sqlparse.Token) ([]sqlparse.Token, bool) {
	var opens []int
	for i, tok := range tokens {
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			opens = append(opens, i)
		case sqlparse.TokenParenClose:
			if len(opens) > 0 {
				opens = opens[:len(opens)-1]
			}
		}
	}
	for k := len(opens) - 1; k >= 0 && k >= len(opens)-2; k-- {
		open := opens[k]
		if open == 0 || tokens[open-1].Quoted || open >= 2 && tokens[open-2].Kind == sqlparse.TokenDot {
			continue
		}
		args := tokens[open+1:]
		depth, as, comma := 0, -1, false
		for i, tok := range args {
			switch tok.Kind {
			case sqlparse.TokenParenOpen:
				depth++
			case sqlparse.TokenParenClose:
				depth--
			case sqlparse.TokenComma:
				comma = comma || depth == 0
			default:
				if depth == 0 && keywordIs(tok, "AS") {
					as = i
				}
			}
		}
		var rest []sqlparse.Token
		switch strings.ToUpper(tokens[open-1].Text) {
		case "CAST", "TRY_CAST":
			if as < 0 {
				return nil, false
			}
			rest = args[as+1:]
		case "CONVERT", "TRY_CONVERT":
			if comma {
				return nil, false
			}
			rest = args
		default:
			continue
		}
		// An outer conversion holds the cursor only through its type's
		// "(length)": "CAST(x AS VARCHAR(‸".
		if k < len(opens)-1 && (len(rest) < 2 || rest[0].Kind != sqlparse.TokenIdent || rest[1].Kind != sqlparse.TokenParenOpen) {
			return nil, false
		}
		return rest, true
	}
	return nil, false
}

// declareTypePosition finds a DECLARE variable's type position: the last
// depth-0 DECLARE in tokens, still the statement the cursor is in, whose
// current comma-separated item is "@v [AS] …".
func declareTypePosition(buf []rune, tokens []sqlparse.Token, from int) (dataTypeKind, []sqlparse.Token) {
	declare, depth := -1, 0
	for i, tok := range tokens {
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			depth++
		case sqlparse.TokenParenClose:
			depth--
		default:
			if depth == 0 && keywordIs(tok, "DECLARE") && (i == 0 || tokens[i-1].Kind != sqlparse.TokenDot) {
				declare = i
			}
		}
	}
	if declare < 0 || keywordStatementStart(buf, tokens[declare].Start, from) != tokens[declare].Start {
		return 0, nil
	}
	start, depth := declare+1, 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i].Kind {
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
	item := tokens[start:]
	if len(item) == 0 || item[0].Quoted || !strings.HasPrefix(item[0].Text, "@") {
		return 0, nil
	}
	at := 1
	if at < len(item) && keywordIs(item[at], "AS") {
		at++
	}
	rest := item[at:]
	if len(rest) >= 2 && keywordIs(rest[0], "TABLE") && rest[1].Kind == sqlparse.TokenParenOpen {
		// The column list, as ddlColumnDefinitionChoices takes it.
		return dataTypeTableVariable, rest[2:]
	}
	return dataTypeVariable, rest
}

// dataTypeChoices completes a dataTypePosition: the types the position
// takes, the length's MAX, and what follows a written type — NULL/NOT NULL
// for a column, '=' (an initializer) for a scalar variable, nothing after a
// conversion's type but its ')'. handled is false where the position is
// past what this completes (an initializer, an unknown type), leaving it to
// the rest of completion.
func dataTypeChoices(buf []rune, tokens []sqlparse.Token, from int, types ddlTypes) (choices []string, datatype, handled bool) {
	kind, rest := dataTypePosition(buf, tokens, from)
	if len(rest) > 0 && rest[len(rest)-1].Kind == sqlparse.TokenDot {
		return nil, false, false // "schema.‸": qualifiedTypeCandidates answers
	}
	switch kind {
	case dataTypeTableVariable:
		choices, handled = ddlColumnDefinitionChoices(rest, types)
		return choices, true, handled
	case dataTypeColumn:
		// ADD may start a constraint or another table element instead.
		column, depth := rest, 0
		for i, tok := range rest {
			switch tok.Kind {
			case sqlparse.TokenParenOpen:
				depth++
			case sqlparse.TokenParenClose:
				depth--
			case sqlparse.TokenComma:
				if depth == 0 {
					column = rest[i+1:]
				}
			}
		}
		if len(column) == 0 || column[0].Kind != sqlparse.TokenIdent ||
			slices.ContainsFunc([]string{"CONSTRAINT", "PRIMARY", "UNIQUE", "FOREIGN", "CHECK", "DEFAULT", "INDEX", "PERIOD"},
				func(word string) bool { return keywordIs(column[0], word) }) {
			return nil, false, false
		}
		choices, handled = ddlColumnDefinitionChoices(column, types)
		return choices, true, handled
	case dataTypeConversion, dataTypeAliasBase:
		system := ddlDataTypes(types.fabric)
		if kind == dataTypeAliasBase {
			system = ddlAliasBaseTypes(types.fabric)
		}
		if len(rest) == 0 {
			if kind == dataTypeConversion {
				// Alias types can't be cast to (Msg 243); CLR types can.
				system = types.withKind(system, gosmo.CatalogClrType)
			}
			return system, true, true
		}
		if _, ok := ddlSkipTypeLength(rest, 1); !ok {
			return ddlMaxLengthChoices(rest), true, true
		}
		if kind == dataTypeAliasBase {
			return ddlNullabilityChoices(rest, ddlTypes{fabric: types.fabric}), true, true
		}
		return nil, false, true
	case dataTypeVariable:
		if len(rest) == 0 {
			return append(types.withUser(ddlVariableDataTypes(types.fabric), true), "TABLE", "CURSOR"), true, true
		}
		if len(rest) == 1 && (keywordIs(rest[0], "TABLE") || keywordIs(rest[0], "CURSOR")) {
			return nil, false, true
		}
		end, typ, ok := types.match(rest, 0)
		if !ok {
			return nil, false, false
		}
		if end, ok = ddlSkipTypeLength(rest, end); !ok {
			return ddlMaxLengthChoices(rest[:end+1]), true, true
		}
		if end < len(rest) {
			return nil, false, false
		}
		if typ.Kind == gosmo.CatalogTableType {
			return nil, false, true
		}
		// Operators are not tokens: "= 1" leaves the tokens as they were.
		afterType := keywordNextToken(buf, rest[end-1].Start).End
		if keywordNextToken(buf[:from], afterType).Kind != sqltext.KindEnd {
			return nil, false, false
		}
		return []string{"="}, false, true
	}
	return nil, false, false
}
