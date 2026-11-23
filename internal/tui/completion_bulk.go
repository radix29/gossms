package tui

import (
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
)

type bulkCompletionOption struct {
	name          string
	since         gosmo.ServerVersion
	warehouseOnly bool
	noWarehouse   bool
	bare          bool
}

var bulkCompletionOptions = []bulkCompletionOption{
	// Fabric's syntax block lists DATA_SOURCE, but its explicit limitations
	// still require absolute URIs and mark DATA_SOURCE unavailable.
	{name: "DATA_SOURCE", since: gosmo.SQLServer2017, noWarehouse: true},
	{name: "CODEPAGE"},
	{name: "DATAFILETYPE"},
	{name: "FORMAT", since: gosmo.SQLServer2017},
	{name: "FORMATFILE", noWarehouse: true},
	{name: "FORMATFILE_DATA_SOURCE", since: gosmo.SQLServer2017, noWarehouse: true},
	{name: "SINGLE_BLOB", noWarehouse: true, bare: true},
	{name: "SINGLE_CLOB", noWarehouse: true, bare: true},
	{name: "SINGLE_NCLOB", noWarehouse: true, bare: true},
	{name: "ROWTERMINATOR"},
	{name: "FIELDTERMINATOR"},
	{name: "FIELDQUOTE", since: gosmo.SQLServer2017},
	{name: "ESCAPECHAR", warehouseOnly: true},
	{name: "HEADER_ROW", warehouseOnly: true},
	{name: "PARSER_VERSION", warehouseOnly: true},
	{name: "MAXERRORS"},
	{name: "ERRORFILE"},
	{name: "ERRORFILE_DATA_SOURCE", since: gosmo.SQLServer2017, noWarehouse: true},
	{name: "FIRSTROW"},
	{name: "LASTROW"},
	{name: "ORDER", noWarehouse: true, bare: true},
	{name: "ROWS_PER_BATCH"},
}

func bulkArgumentValue(buf []rune, args [][]sqltext.Token, name string) string {
	for _, arg := range args {
		if len(arg) < 3 || !argumentTokenIs(buf, arg[0], name) || !argumentTokenIs(buf, arg[1], "=") {
			continue
		}
		value := arg[2:]
		if len(value) == 2 && argumentTokenIs(buf, value[0], "N") &&
			value[0].End == value[1].Start && value[1].Kind == sqltext.KindString {
			value = value[1:]
		}
		if len(value) == 1 {
			return strings.ToUpper(strings.Trim(string(buf[value[0].Start:value[0].End]), "'"))
		}
	}
	return ""
}

func (p *QueryPanel) bulkOptionChoices(buf []rune, a completionArguments, from, cursor int) ([]string, bool) {
	args := splitCompletionArguments(buf, a.tokens)
	if len(a.tokens) == 0 {
		return []string{"BULK"}, true
	}
	if !argumentTokenIs(buf, a.tokens[0], "BULK") {
		return nil, false // The provider form is not the BULK grammar.
	}
	if len(args) == 1 {
		return nil, true // A path, not an identifier or scalar expression.
	}
	allArgs := splitCompletionArguments(buf, a.all)
	format := bulkArgumentValue(buf, allArgs, "FORMAT")
	fabric := p.conn.IsFabricDW()
	if format == "" && fabric {
		for _, tok := range args[0][1:] {
			if tok.Kind != sqltext.KindString {
				continue
			}
			path, _, _ := strings.Cut(strings.ToLower(strings.Trim(string(buf[tok.Start:tok.End]), "'")), "?")
			switch {
			case strings.HasSuffix(path, ".csv"), strings.HasSuffix(path, ".tsv"):
				format = "CSV"
			case strings.HasSuffix(path, ".parquet"), strings.HasSuffix(path, ".parq"):
				format = "PARQUET"
			case strings.HasSuffix(path, ".jsonl"), strings.HasSuffix(path, ".ldjson"), strings.HasSuffix(path, ".ndjson"):
				format = "JSONL"
			}
			break
		}
	}
	current := args[len(args)-1]
	if len(current) > 0 {
		name := strings.ToUpper(string(buf[current[0].Start:current[0].End]))
		if name == "ORDER" {
			if len(current) == 2 && buf[current[1].End-1] == ')' {
				return []string{"UNIQUE"}, true
			}
			return nil, true
		}
		if len(current) == 1 && !strings.HasPrefix(name, "SINGLE_") {
			return []string{"="}, true
		}
		if len(current) != 2 || !argumentTokenIs(buf, current[1], "=") {
			return nil, true
		}
		switch name {
		case "FORMAT":
			return p.bulkFormatChoices(), true
		case "CODEPAGE":
			return []string{"'ACP'", "'OEM'", "'RAW'", "'65001'"}, true
		case "DATAFILETYPE":
			return []string{"'char'", "'widechar'"}, true
		case "PARSER_VERSION":
			if fabric {
				return []string{"'1.0'", "'2.0'"}, true
			}
		case "HEADER_ROW":
			if fabric {
				return []string{"TRUE", "FALSE"}, true
			}
		}
		return nil, true
	}
	used := argumentOptionNames(buf, a.all, from, cursor)
	parser := bulkArgumentValue(buf, allArgs, "PARSER_VERSION")
	single := used["SINGLE_BLOB"] || used["SINGLE_CLOB"] || used["SINGLE_NCLOB"]
	var choices []string
	for _, opt := range bulkCompletionOptions {
		if used[opt.name] || fabric && opt.noWarehouse || !fabric && opt.warehouseOnly {
			continue
		}
		if p.conn.Server != nil {
			info := p.conn.Server.Info()
			if opt.since != 0 && !info.AtLeast(opt.since) ||
				opt.name == "CODEPAGE" && strings.EqualFold(info.Platform, "Linux") {
				continue
			}
		}
		if format != "" && strings.HasPrefix(opt.name, "SINGLE_") ||
			single && (strings.HasPrefix(opt.name, "SINGLE_") || opt.name == "FORMAT" || strings.HasPrefix(opt.name, "FORMATFILE")) {
			continue
		}
		switch format {
		case "PARQUET", "DELTA":
			if opt.name != "DATA_SOURCE" && opt.name != "ROWS_PER_BATCH" && opt.name != "MAXERRORS" {
				continue
			}
		case "JSONL":
			switch opt.name {
			case "ROWTERMINATOR", "FIELDTERMINATOR", "FIELDQUOTE", "ESCAPECHAR", "PARSER_VERSION", "HEADER_ROW", "LASTROW":
				continue
			}
		}
		if fabric && (parser == "1.0" && opt.name == "HEADER_ROW" || parser == "2.0" && opt.name == "LASTROW") {
			continue
		}
		switch {
		case opt.name == "ORDER":
			choices = append(choices, "ORDER(")
		case opt.bare:
			choices = append(choices, opt.name)
		default:
			choices = append(choices, opt.name+" =")
		}
	}
	return choices, true
}

func (p *QueryPanel) bulkFormatChoices() []string {
	if p.conn.IsFabricDW() {
		return []string{"'CSV'", "'PARQUET'", "'JSONL'"}
	}
	choices := []string{"'CSV'"}
	if p.conn.Server != nil && !p.conn.Server.Info().AtLeast(gosmo.SQLServer2017) {
		return nil
	}
	if p.conn.Server == nil || p.conn.Server.Info().AtLeast(gosmo.SQLServer2022) {
		choices = append(choices, "'PARQUET'")
		if p.conn.Server == nil || !p.conn.Server.Info().IsFabricSQLDatabase() {
			choices = append(choices, "'DELTA'")
		}
	}
	return choices
}

// rowsetCallBefore recognizes an immediately preceding built-in rowset call,
// never a similarly named schema-qualified function or a table's WITH hints.
func rowsetCallBefore(buf []rune, tokens []sqlparse.Token, close int) (string, bool) {
	if close < 0 || tokens[close].Kind != sqlparse.TokenParenClose {
		return "", false
	}
	depth := 1
	for i := close - 1; i > 0; i-- {
		switch tokens[i].Kind {
		case sqlparse.TokenParenClose:
			depth++
		case sqlparse.TokenParenOpen:
			depth--
		}
		if depth != 0 {
			continue
		}
		if tokens[i-1].Quoted || i >= 2 && tokens[i-2].Kind == sqlparse.TokenDot {
			return "", false
		}
		name := strings.ToUpper(tokens[i-1].Text)
		if name == "OPENJSON" {
			return name, true
		}
		if name != "OPENROWSET" || !keywordNextIs(buf, tokens[i].Start+1, "BULK") {
			return "", false
		}
		args := splitCompletionArguments(buf, directArgumentTokens(buf, tokens[i].Start+1, tokens[close].Start))
		for _, arg := range args[1:] {
			if len(arg) > 0 && (argumentTokenIs(buf, arg[0], "SINGLE_BLOB") ||
				argumentTokenIs(buf, arg[0], "SINGLE_CLOB") || argumentTokenIs(buf, arg[0], "SINGLE_NCLOB")) {
				return "", false
			}
		}
		return name, true
	}
	return "", false
}

func (p *QueryPanel) rowsetSchemaChoices(buf []rune, tokens []sqlparse.Token, opens []int, from, cursor int) (choices []string, only, handled bool) {
	if _, ok := rowsetCallBefore(buf, tokens, len(tokens)-1); ok {
		if from == cursor && keywordNextIs(buf, cursor, "WITH") {
			return nil, false, true
		}
		return []string{"WITH("}, false, true
	}
	for k := len(opens) - 1; k >= 0; k-- {
		open := opens[k]
		if open < 2 || !keywordIs(tokens[open-1], "WITH") {
			continue
		}
		name, ok := rowsetCallBefore(buf, tokens, open-2)
		if !ok {
			continue
		}
		column, depth := tokens[open+1:], 0
		for i, tok := range column {
			switch tok.Kind {
			case sqlparse.TokenParenOpen:
				depth++
			case sqlparse.TokenParenClose:
				depth--
			case sqlparse.TokenComma:
				if depth == 0 {
					column = tokens[open+2+i:]
				}
			}
		}
		if len(column) == 0 || column[0].Kind != sqlparse.TokenIdent {
			return nil, true, true
		}
		if depth > 0 {
			return ddlMaxLengthChoices(column), true, true
		}
		if len(column) == 1 {
			for _, typ := range ddlDataTypes(p.conn.IsFabricDW()) {
				if p.builtinCompletionSupported(strings.TrimSuffix(typ, "("), true) {
					choices = append(choices, typ)
				}
			}
			return choices, true, true
		}
		if name != "OPENJSON" || !keywordIs(column[1], "NVARCHAR") || len(column) < 4 ||
			!keywordIs(column[3], "MAX") {
			return nil, true, true
		}
		end, closed := ddlSkipTypeLength(column, 2)
		if !closed {
			return nil, true, true
		}
		for _, tok := range column[end:] {
			if keywordIs(tok, "AS") {
				if keywordIs(column[len(column)-1], "AS") {
					return []string{"JSON"}, true, true
				}
				return nil, true, true
			}
		}
		return []string{"AS JSON"}, true, true
	}
	return nil, false, false
}
