package tui

import (
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

type completionArguments struct {
	name   string
	start  int
	tokens []sqltext.Token
	all    []sqltext.Token
}

// Keep nested calls, literals and comments out of the option grammar. A nested
// expression occupies one span; only the enclosing call's commas split arguments.
func directArgumentTokens(buf []rune, start, end int) []sqltext.Token {
	var tokens []sqltext.Token
	depth, nested := 0, 0
	for pos := start; pos < end; {
		tok := keywordNextToken(buf[:end], pos)
		if tok.Kind == sqltext.KindEnd {
			break
		}
		pos = tok.End
		if tok.Kind == sqltext.KindPunct {
			switch buf[tok.Start] {
			case '(':
				if depth == 0 {
					nested = tok.Start
				}
				depth++
				continue
			case ')':
				depth--
				if depth == 0 {
					tokens = append(tokens, sqltext.Token{Kind: sqltext.KindPunct, Start: nested, End: tok.End})
				}
				continue
			}
		}
		if depth == 0 {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

func argumentTokenIs(buf []rune, tok sqltext.Token, text string) bool {
	return tok.Kind != sqltext.KindString && tok.Kind != sqltext.KindQuotedIdent &&
		strings.EqualFold(string(buf[tok.Start:tok.End]), text)
}

func splitCompletionArguments(buf []rune, tokens []sqltext.Token) [][]sqltext.Token {
	args := [][]sqltext.Token{nil}
	for _, tok := range tokens {
		if argumentTokenIs(buf, tok, ",") {
			args = append(args, nil)
		} else {
			args[len(args)-1] = append(args[len(args)-1], tok)
		}
	}
	return args
}

func (p *QueryPanel) functionOptionCandidates(buf []rune, tokens []sqlparse.Token, from, cursor int, prefix string) ([]editor.CompletionItem, bool) {
	var opens []int
	count := 0
	for i, tok := range tokens {
		if tok.Start >= from {
			break
		}
		count = i + 1
		switch tok.Kind {
		case sqlparse.TokenParenOpen:
			opens = append(opens, i)
		case sqlparse.TokenParenClose:
			if len(opens) > 0 {
				opens = opens[:len(opens)-1]
			}
		}
	}
	tokens = tokens[:count]
	if choices, only, handled := p.rowsetSchemaChoices(buf, tokens, opens, from, cursor); handled {
		return functionOptionItems(buf, cursor, prefix, choices), only
	}
	if len(opens) == 0 {
		return nil, false
	}
	open := opens[len(opens)-1]
	if open == 0 || tokens[open-1].Quoted ||
		open >= 2 && tokens[open-2].Kind == sqlparse.TokenDot {
		return nil, false
	}
	name := strings.ToUpper(tokens[open-1].Text)
	switch name {
	case "OPENROWSET", "ORDER", "AI_GENERATE_CHUNKS", "AI_GENERATE_EMBEDDINGS",
		"AI_ANALYZE_SENTIMENT", "AI_CLASSIFY", "AI_EXTRACT", "AI_FIX_GRAMMAR",
		"AI_GENERATE_RESPONSE", "AI_SUMMARIZE", "AI_TRANSLATE",
		"JSON_VALUE", "JSON_QUERY", "JSON_ARRAY", "JSON_OBJECT", "JSON_ARRAYAGG", "JSON_OBJECTAGG", "ISJSON":
	default:
		return nil, false
	}
	start := tokens[open].Start
	fn := keywordNextToken(buf, tokens[open-1].Start)
	if keywordNextToken(buf[:start], fn.End).Kind != sqltext.KindEnd {
		return nil, false
	}
	end, closed := keywordCallEnd(buf, sqltext.Token{Kind: sqltext.KindPunct, Start: start, End: start + 1})
	if !closed {
		end = len(buf) + 1
	}
	a := completionArguments{name: name, start: start + 1,
		tokens: directArgumentTokens(buf, start+1, from)}
	if name == "OPENROWSET" || name == "AI_GENERATE_CHUNKS" {
		a.all = directArgumentTokens(buf, start+1, end-1)
	}
	var choices []string
	var only bool
	switch name {
	case "ORDER":
		if p.conn.IsFabricDW() || len(opens) < 2 {
			return nil, false
		}
		outer := opens[len(opens)-2]
		if outer == 0 || !keywordIs(tokens[outer-1], "OPENROWSET") ||
			outer >= 2 && tokens[outer-2].Kind == sqlparse.TokenDot ||
			!keywordNextIs(buf, tokens[outer].Start+1, "BULK") {
			return nil, false
		}
		args := splitCompletionArguments(buf, a.tokens)
		arg := args[len(args)-1]
		if len(arg) > 0 && !argumentTokenIs(buf, arg[len(arg)-1], "ASC") && !argumentTokenIs(buf, arg[len(arg)-1], "DESC") &&
			argumentExpressionComplete(buf, arg[0].Start, from) {
			choices = []string{"ASC", "DESC"}
		}
		only = true
	case "OPENROWSET":
		choices, only = p.bulkOptionChoices(buf, a, from, cursor)
	case "AI_GENERATE_CHUNKS":
		if !p.builtinCompletionSupported(name, false) {
			return nil, true
		}
		args := splitCompletionArguments(buf, a.tokens)
		current := args[len(args)-1]
		if len(current) == 0 {
			used := argumentOptionNames(buf, a.all, from, cursor)
			for _, name := range []string{"SOURCE", "CHUNK_TYPE", "CHUNK_SIZE", "OVERLAP", "ENABLE_CHUNK_SET_ID"} {
				if !used[name] {
					choices = append(choices, name+" =")
				}
			}
			only = true
		} else if len(current) == 1 {
			choices = []string{"="}
			only = true
		} else if argumentTokenIs(buf, current[0], "CHUNK_TYPE") {
			if len(current) == 2 && argumentTokenIs(buf, current[1], "=") {
				choices = []string{"FIXED"}
			}
			only = true
		}
	case "AI_GENERATE_EMBEDDINGS":
		if p.builtinCompletionSupported(name, false) {
			choices, only = embeddingOptionChoices(buf, a, from)
		} else {
			only = true
		}
	default:
		if strings.HasPrefix(name, "AI_") {
			if !p.conn.IsFabricDW() {
				return nil, true
			}
			choices, only = aiErrorChoices(buf, a, from)
		} else {
			choices, only = p.jsonFunctionChoices(buf, a, from)
		}
	}
	return functionOptionItems(buf, cursor, prefix, choices), only
}

func argumentOptionNames(buf []rune, tokens []sqltext.Token, from, cursor int) map[string]bool {
	used := map[string]bool{}
	for _, arg := range splitCompletionArguments(buf, tokens) {
		if len(arg) > 0 && arg[0].Kind == sqltext.KindWord && (arg[0].End <= from || arg[0].Start >= cursor) {
			used[strings.ToUpper(string(buf[arg[0].Start:arg[0].End]))] = true
		}
	}
	return used
}

func functionOptionItems(buf []rune, cursor int, prefix string, choices []string) []editor.CompletionItem {
	var items []editor.CompletionItem
	for _, choice := range choices {
		label := strings.TrimSuffix(choice, " =")
		label = strings.TrimSuffix(label, "(")
		if !strings.HasPrefix(strings.ToUpper(strings.Trim(label, "'")), strings.ToUpper(prefix)) {
			continue
		}
		if strings.HasSuffix(choice, "(") {
			items = append(items, completionCallItem(buf, cursor, label, "keyword", false))
			continue
		}
		text, _ := completionPhrasePrefix(buf, cursor, choice)
		next := keywordNextToken(buf, cursor)
		if choice == "=" && argumentTokenIs(buf, next, "=") {
			continue
		}
		if strings.HasSuffix(choice, " =") && argumentTokenIs(buf, next, "=") {
			text = label
		}
		items = append(items, editor.CompletionItem{Text: text, Label: label, Detail: "keyword",
			AppendSpace: !strings.HasPrefix(choice, "'"), Soft: prefix == ""})
	}
	return items
}

func argumentExpressionComplete(buf []rune, start, from int) bool {
	complete, last := keywordExpressionComplete(buf, start, from, "VALUE")
	if !complete && (argumentTokenIs(buf, last, "ASC") || argumentTokenIs(buf, last, "DESC")) &&
		slices.ContainsFunc(directArgumentTokens(buf, start, from), func(tok sqltext.Token) bool {
			return argumentTokenIs(buf, tok, "ORDER")
		}) {
		complete, _ = keywordExpressionComplete(buf, start, last.Start, "VALUE")
	}
	return complete
}

// NULL is also a valid argument value. It starts an error/null-handling clause
// only when a complete expression already precedes it in this argument.
func argumentHandlingChoices(buf []rune, arg []sqltext.Token, from int, target string, allowDefault bool) ([]string, bool) {
	if len(arg) == 0 {
		return nil, false
	}
	for i, tok := range arg {
		word := strings.ToUpper(string(buf[tok.Start:tok.End]))
		if tok.Kind != sqltext.KindWord || word != "NULL" && word != "ERROR" && word != "DEFAULT" && word != "ABSENT" ||
			i == 0 || !argumentExpressionComplete(buf, arg[0].Start, tok.Start) {
			continue
		}
		if target == "NULL" && word != "NULL" && word != "ABSENT" ||
			target == "ERROR" && (word == "ABSENT" || word == "DEFAULT" && !allowDefault) {
			continue
		}
		rest := arg[i+1:]
		if word == "DEFAULT" {
			if len(rest) == 0 {
				return nil, false
			}
			on := slices.IndexFunc(rest, func(tok sqltext.Token) bool { return argumentTokenIs(buf, tok, "ON") })
			if on < 0 {
				if argumentExpressionComplete(buf, rest[0].Start, from) {
					return []string{"ON " + target}, true
				}
				return nil, false
			}
			rest = rest[on:]
		}
		if len(rest) == 0 {
			return []string{"ON " + target}, true
		}
		if len(rest) == 1 && argumentTokenIs(buf, rest[0], "ON") {
			return []string{target}, true
		}
		return nil, true
	}
	return nil, false
}

func aiErrorChoices(buf []rune, a completionArguments, from int) ([]string, bool) {
	args := splitCompletionArguments(buf, a.tokens)
	minArgs := 1
	if a.name == "AI_CLASSIFY" || a.name == "AI_EXTRACT" || a.name == "AI_TRANSLATE" {
		minArgs = 2
	}
	if len(args) < minArgs {
		return nil, false
	}
	arg := args[len(args)-1]
	if choices, handled := argumentHandlingChoices(buf, arg, from, "ERROR", true); handled {
		return choices, true
	}
	if len(arg) == 0 || slices.ContainsFunc(arg, func(tok sqltext.Token) bool { return argumentTokenIs(buf, tok, "DEFAULT") }) ||
		!argumentExpressionComplete(buf, arg[0].Start, from) {
		return nil, false
	}
	return []string{"NULL ON ERROR", "ERROR ON ERROR", "DEFAULT"}, true
}

func embeddingOptionChoices(buf []rune, a completionArguments, from int) ([]string, bool) {
	use := slices.IndexFunc(a.tokens, func(tok sqltext.Token) bool { return argumentTokenIs(buf, tok, "USE") })
	if use < 0 {
		if argumentExpressionComplete(buf, a.start, from) {
			return []string{"USE MODEL"}, true
		}
		return nil, false
	}
	rest := a.tokens[use+1:]
	if len(rest) == 0 {
		return []string{"MODEL"}, true
	}
	if !argumentTokenIs(buf, rest[0], "MODEL") {
		return nil, true
	}
	if len(rest) == 1 {
		return nil, true
	}
	if len(rest) == 2 && (rest[1].Kind == sqltext.KindWord || rest[1].Kind == sqltext.KindQuotedIdent) {
		return []string{"PARAMETERS"}, true
	}
	if len(rest) > 2 && argumentTokenIs(buf, rest[2], "PARAMETERS") {
		return nil, false
	}
	return nil, true
}

func (p *QueryPanel) jsonFunctionChoices(buf []rune, a completionArguments, from int) ([]string, bool) {
	args := splitCompletionArguments(buf, a.tokens)
	arg := args[len(args)-1]
	if a.name == "JSON_ARRAYAGG" {
		arg = a.tokens // Its ORDER BY commas do not start new value arguments.
	}
	nativeJSON := p.builtinCompletionSupported("JSON", true)
	switch a.name {
	case "ISJSON":
		if len(args) != 2 {
			return nil, false
		}
		if len(arg) == 0 && (p.conn.Server == nil || p.conn.Server.Info().AtLeast(gosmo.SQLServer2022) &&
			p.conn.Server.Info().EngineEdition != gosmo.EngineAzureSynapse) {
			return []string{"VALUE", "ARRAY", "OBJECT", "SCALAR"}, true
		}
		return nil, true
	case "JSON_VALUE", "JSON_QUERY":
		if len(args) != 2 && a.name == "JSON_VALUE" || len(args) > 2 {
			return nil, false
		}
		if !nativeJSON {
			return nil, len(args) == 2
		}
		if a.name == "JSON_QUERY" {
			for i, tok := range arg {
				if argumentTokenIs(buf, tok, "WITH") {
					switch len(arg) - i {
					case 1:
						return []string{"ARRAY WRAPPER"}, true
					case 2:
						if argumentTokenIs(buf, arg[i+1], "ARRAY") {
							return []string{"WRAPPER"}, true
						}
					}
					return nil, true
				}
			}
			if len(arg) > 0 && argumentExpressionComplete(buf, arg[0].Start, from) {
				return []string{"WITH ARRAY WRAPPER"}, true
			}
			return nil, len(args) == 2
		}
		for i, tok := range arg {
			if argumentTokenIs(buf, tok, "RETURNING") {
				if i == len(arg)-1 {
					return strings.Fields("TINYINT SMALLINT INT BIGINT DECIMAL( NUMERIC( FLOAT( REAL CHAR( VARCHAR( NCHAR( NVARCHAR( DATE TIME( DATETIME2( DATETIMEOFFSET("), true
				}
				return nil, true
			}
		}
		if len(arg) > 0 && argumentExpressionComplete(buf, arg[0].Start, from) {
			return []string{"RETURNING"}, true
		}
		return nil, true
	}
	if !p.builtinCompletionSupported(a.name, false) {
		return nil, true
	}
	if len(arg) == 0 && len(args) == 1 && (a.name == "JSON_ARRAY" || a.name == "JSON_OBJECT") {
		choices := []string{"NULL ON NULL", "ABSENT ON NULL"}
		if nativeJSON {
			choices = append(choices, "RETURNING")
		}
		return choices, false
	}
	returning := slices.IndexFunc(a.tokens, func(tok sqltext.Token) bool { return argumentTokenIs(buf, tok, "RETURNING") })
	if returning >= 0 {
		if returning == len(a.tokens)-1 && nativeJSON {
			return []string{"JSON"}, true
		}
		return nil, true
	}
	if choices, handled := argumentHandlingChoices(buf, arg, from, "NULL", false); handled {
		if len(choices) == 0 && nativeJSON {
			choices = []string{"RETURNING"}
		}
		return choices, true
	}
	if len(arg) == 0 || !argumentExpressionComplete(buf, arg[0].Start, from) {
		return nil, false
	}
	if strings.HasPrefix(a.name, "JSON_OBJECT") &&
		!slices.ContainsFunc(arg, func(tok sqltext.Token) bool { return argumentTokenIs(buf, tok, ":") }) {
		return nil, false
	}
	choices := []string{"NULL ON NULL", "ABSENT ON NULL"}
	if a.name == "JSON_ARRAYAGG" && !slices.ContainsFunc(arg, func(tok sqltext.Token) bool { return argumentTokenIs(buf, tok, "ORDER") }) {
		choices = append(choices, "ORDER BY")
	}
	if nativeJSON {
		choices = append(choices, "RETURNING")
	}
	return choices, true
}
