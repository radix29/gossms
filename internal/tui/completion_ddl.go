package tui

import (
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
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

func ddlColumnTypeChoices(tokens []sqlparse.Token, types ddlTypes) ([]string, bool) {
	open := slices.IndexFunc(tokens, func(tok sqlparse.Token) bool { return tok.Kind == sqlparse.TokenParenOpen })
	if open < 1 || !keywordAliasPosition(tokens[:open], 0) {
		return nil, false
	}
	return ddlColumnDefinitionChoices(tokens[open+1:], types)
}

func ddlColumnDefinitionChoices(tokens []sqlparse.Token, types ddlTypes) ([]string, bool) {
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
	if len(column) == 1 {
		return types.withUser(ddlDataTypes(types.fabric), false), true
	}
	return ddlNullabilityChoices(column[1:], types), true
}

// ddlNullabilityChoices completes after a column's data type, written at
// rest[0]: NULL and NOT NULL once the type and its length are, NULL after a
// NOT. A table type takes neither.
func ddlNullabilityChoices(rest []sqlparse.Token, types ddlTypes) []string {
	end, typ, ok := types.match(rest, 0)
	if !ok || typ.Kind == gosmo.CatalogTableType {
		return nil
	}
	end, ok = ddlSkipTypeLength(rest, end)
	if !ok {
		return nil
	}
	if end == len(rest) {
		return []string{"NULL", "NOT NULL"}
	}
	if end+1 == len(rest) && keywordIs(rest[end], "NOT") {
		return []string{"NULL"}
	}
	return nil
}

// ddlSkipTypeLength steps end, the index after a data type's name, past its
// "(length)"; ok is false while that is still open.
func ddlSkipTypeLength(tokens []sqlparse.Token, end int) (int, bool) {
	if end < len(tokens) && tokens[end].Kind == sqlparse.TokenParenOpen {
		close := slices.IndexFunc(tokens[end+1:], func(tok sqlparse.Token) bool { return tok.Kind == sqlparse.TokenParenClose })
		if close < 0 {
			return end, false
		}
		end += close + 2
	}
	return end, true
}

// ddlTypes is what a DDL data-type position offers: the system types for the
// edition, then the database's user-defined ones, schema-qualified — alias
// and CLR types wherever a type goes, table types in a parameter list only.
// user is empty on Fabric, which has no CREATE TYPE, and until the
// database's inventory has loaded (ddlTypes reads it, never starts it).
type ddlTypes struct {
	fabric    bool
	user      []gosmo.CatalogType
	collation string
	// schemas is where an unqualified type name resolves after the system
	// types, in the server's order: the login's default schema, then dbo
	// (completionInventory.qualifierSchemas). Never another schema.
	schemas []string
}

// ddlTypes is the type set p's database offers a DDL data-type position.
func (p *QueryPanel) ddlTypes() ddlTypes {
	t := ddlTypes{fabric: p.conn.IsFabricDW()}
	if t.fabric || p.app == nil {
		return t
	}
	inv := p.app.completion.inventories[completionInventoryKey(p.conn.Opts, p.database)]
	if inv != nil && !inv.loading && inv.err == nil && inv.catalog != nil {
		t.user, t.collation, t.schemas = inv.catalog.Types, inv.collation, inv.qualifierSchemas("")
	}
	return t
}

// ddlUserTypeLabel is the schema-qualified name a user-defined type is offered
// and inserted as.
func ddlUserTypeLabel(typ gosmo.CatalogType) string {
	return gosmo.QuoteNameIfNeeded(typ.Schema) + "." + gosmo.QuoteNameIfNeeded(typ.Name)
}

// ddlUserTypeDetail is a user-defined type's popup detail.
func ddlUserTypeDetail(typ gosmo.CatalogType) string {
	switch typ.Kind {
	case gosmo.CatalogTableType:
		return "table type"
	case gosmo.CatalogClrType:
		return "CLR type"
	}
	return "alias type"
}

// withUser appends the user-defined types to names, table types only when
// table (a parameter list).
func (t ddlTypes) withUser(names []string, table bool) []string {
	for _, typ := range t.user {
		if table || typ.Kind != gosmo.CatalogTableType {
			names = append(names, ddlUserTypeLabel(typ))
		}
	}
	return names
}

// withKind appends the user-defined types of one kind to names.
func (t ddlTypes) withKind(names []string, kind gosmo.CatalogTypeKind) []string {
	for _, typ := range t.user {
		if typ.Kind == kind {
			names = append(names, ddlUserTypeLabel(typ))
		}
	}
	return names
}

// userType is the user-defined type offered as label, if one is.
func (t ddlTypes) userType(label string) (gosmo.CatalogType, bool) {
	for _, typ := range t.user {
		if ddlUserTypeLabel(typ) == label {
			return typ, true
		}
	}
	return gosmo.CatalogType{}, false
}

// match finds the data type written at tokens[at]: a system type or an
// unqualified user-defined one (one token), or schema.type (three). end is
// the index after it; typ is the user-defined type matched, zero for a
// system one. Names compare under the database collation. An unqualified
// name resolves as the server does: system type, then t.schemas in order —
// a type of that name in any other schema is not it.
func (t ddlTypes) match(tokens []sqlparse.Token, at int) (end int, typ gosmo.CatalogType, ok bool) {
	if at >= len(tokens) || tokens[at].Kind != sqlparse.TokenIdent {
		return 0, typ, false
	}
	if at+2 < len(tokens) && tokens[at+1].Kind == sqlparse.TokenDot && tokens[at+2].Kind == sqlparse.TokenIdent {
		for _, u := range t.user {
			if gosmo.SameName(t.collation, u.Schema, tokens[at].Text) && gosmo.SameName(t.collation, u.Name, tokens[at+2].Text) {
				return at + 3, u, true
			}
		}
		return 0, typ, false
	}
	if slices.ContainsFunc(ddlDataTypes(t.fabric), func(name string) bool { return keywordIs(tokens[at], strings.TrimSuffix(name, "(")) }) {
		return at + 1, typ, true
	}
	for _, schema := range t.schemas {
		for _, u := range t.user {
			if gosmo.SameName(t.collation, u.Schema, schema) && gosmo.SameName(t.collation, u.Name, tokens[at].Text) {
				return at + 1, u, true
			}
		}
	}
	return 0, typ, false
}

// qualifiedTypeCandidates answers "schema.|" where a DDL data type goes: that
// schema's user-defined types, inserted by bare name after the typed
// qualifier. handled is false where the qualifier does not start a data
// type, leaving the position to the catalog-name path (which would offer
// the schema's tables here).
func (p *QueryPanel) qualifiedTypeCandidates(buf []rune, tokens []sqlparse.Token, query *sqlparse.Query, from, cursor int, prefix string) (items []editor.CompletionItem, handled bool) {
	end := 0
	for end < len(tokens) && tokens[end].Start < from {
		end++
	}
	if end < 2 || tokens[end-1].Kind != sqlparse.TokenDot || tokens[end-2].Kind != sqlparse.TokenIdent ||
		end >= 3 && tokens[end-3].Kind == sqlparse.TokenDot {
		return nil, false
	}
	schema := tokens[end-2]
	types := p.ddlTypes()
	system := ddlDataTypes(types.fabric)
	at, _ := p.keywordCandidates(buf, tokens, query, schema.Start, cursor, "")
	for _, item := range at {
		typ, ok := types.userType(item.Label)
		if !ok {
			handled = handled || slices.ContainsFunc(system, func(name string) bool { return strings.TrimSuffix(name, "(") == item.Label })
			continue
		}
		handled = true
		if !gosmo.SameName(types.collation, typ.Schema, schema.Text) {
			continue
		}
		if ok, partial := nameMatch(typ.Name, strings.ToLower(prefix)); ok {
			item.Text, item.Soft, item.Partial = gosmo.QuoteNameIfNeeded(typ.Name), prefix == "", partial
			items = append(items, item)
		}
	}
	return items, handled
}

// ddlRoutineDataTypes is the system types a routine signature accepts.
func ddlRoutineDataTypes(fabric bool) []string {
	return slices.DeleteFunc(ddlDataTypes(fabric), func(name string) bool {
		return name == "IMAGE" || name == "NTEXT" || name == "TEXT" || name == "ROWVERSION"
	})
}

// ddlVariableDataTypes is the system types a local variable takes: not text,
// ntext or image (Msg 2739).
func ddlVariableDataTypes(fabric bool) []string {
	return slices.DeleteFunc(ddlDataTypes(fabric), func(name string) bool {
		return name == "IMAGE" || name == "NTEXT" || name == "TEXT"
	})
}

// ddlAliasBaseTypes is the system types CREATE TYPE … FROM takes: not the
// CLR, XML, JSON and vector types, rowversion or sysname (Msgs 222, 15226,
// 13657, 42212 on SQL Server 2025).
func ddlAliasBaseTypes(fabric bool) []string {
	return slices.DeleteFunc(ddlDataTypes(fabric), func(name string) bool {
		switch name {
		case "GEOGRAPHY", "GEOMETRY", "HIERARCHYID", "JSON", "ROWVERSION", "SYSNAME", "VECTOR(", "XML":
			return true
		}
		return false
	})
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

func ddlParameterChoices(buf []rune, tokens []sqlparse.Token, from int, function, parenthesized bool, types ddlTypes) ([]string, bool) {
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
	at := 1
	if at < len(param) && keywordIs(param[at], "AS") {
		at++
	}
	if at == len(param) {
		return types.withUser(ddlRoutineDataTypes(types.fabric), true), true
	}
	if depth > 0 {
		return ddlMaxLengthChoices(param[at:]), true
	}
	// A table type must be READONLY, so that is all it is offered.
	typeEnd, typ, ok := types.match(param, at)
	if !ok {
		typeEnd = at + 1
	}
	if typ.Kind == gosmo.CatalogTableType && typ.Name != "" && typeEnd == len(param) {
		return []string{"READONLY"}, false
	}
	afterType := keywordNextToken(buf, param[typeEnd-1].Start).End
	complete, last := keywordExpressionComplete(buf, afterType, from, "VALUE")
	tail := keywordNextToken(buf[:from], afterType)
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
func ddlRoutineChoices(buf []rune, tail []sqlparse.Token, objectType string, from int, types ddlTypes) ([]string, bool, bool) {
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
	return ddlRoutineSignatureChoices(buf, tail, h, from, function, types)
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
func ddlRoutineSignatureChoices(buf []rune, tail []sqlparse.Token, h routineHeader, from int, function bool, types ddlTypes) ([]string, bool, bool) {
	if h.returns >= 0 {
		result := tail[h.returns+1:]
		if len(result) == 0 {
			return append(types.withUser(ddlRoutineDataTypes(types.fabric), false), "TABLE"), true, true
		}
		if sqlparse.HasSigil(result[0].Text) {
			if len(result) == 1 {
				return []string{"TABLE("}, true, true
			}
			if len(result) < 3 || !keywordIs(result[1], "TABLE") || result[2].Kind != sqlparse.TokenParenOpen {
				return nil, false, true
			}
			if h.depth > 0 {
				choices, _ := ddlColumnDefinitionChoices(result[3:], types)
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
			choices, datatype := ddlParameterChoices(buf, tail[1:], from, function, true, types)
			return choices, datatype, true
		}
		choices, datatype := ddlParameterChoices(buf, tail, from, function, false, types)
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
	choices, datatype := ddlParameterChoices(buf, tail, from, function, false, types)
	return choices, datatype, true
}
