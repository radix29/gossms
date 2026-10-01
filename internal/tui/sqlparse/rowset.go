package sqlparse

import "strings"

// ---------------------------------------------------------------------------
// Rowset functions: OPENJSON, OPENROWSET, OPENXML
//
// A rowset function in a FROM clause has no catalog object behind it: its
// columns are whatever its own WITH clause declares, a list with the shape of a
// CREATE TABLE's ("name type [path] [AS JSON]"). Before this parsed, the WITH
// read as the start of a new statement and the alias after the list was lost,
// so "j." offered nothing.
// ---------------------------------------------------------------------------

// Rowset is the column list a rowset function puts in scope.
type Rowset struct {
	// Function is the function's name, uppercased: OPENJSON, OPENROWSET or
	// OPENXML.
	Function string

	// Columns is the WITH list, or OPENJSON's fixed key/value/type shape when
	// it has none. Nil when the shape is unknown — a WITH that doesn't parse,
	// OPENROWSET or OPENXML with no WITH at all — so the reference resolves to
	// nothing rather than a guess.
	Columns []BindingColumn
}

// rowsetFunctions are the built-ins whose WITH clause declares their columns.
var rowsetFunctions = map[string]bool{"OPENJSON": true, "OPENROWSET": true, "OPENXML": true}

// openJSONDefaultColumns is what OPENJSON returns without a WITH clause, as
// sys.dm_exec_describe_first_result_set reports it on SQL Server 2025.
var openJSONDefaultColumns = []BindingColumn{
	{Name: "key", Type: "nvarchar", TypeArgs: []string{"4000"}},
	{Name: "value", Type: "nvarchar", TypeArgs: []string{"MAX"}, Nullable: true},
	{Name: "type", Type: "tinyint"},
}

// parseRowset reads what follows a rowset function's argument list, with p.i
// just past its ')': an optional "WITH ( column definitions )". It returns nil
// for any other function, leaving p.i alone.
//
// A WITH whose list does not parse — half-typed, or holding no column at all —
// gives a Rowset with no columns and is left unconsumed for the branch loop to
// walk, the same as any token this parser does not recognise.
func (p *queryParser) parseRowset(ref FromRef) *Rowset {
	fn := strings.ToUpper(ref.Name)
	if ref.Schema != "" || ref.Database != "" || !rowsetFunctions[fn] {
		return nil
	}
	rs := &Rowset{Function: fn}
	if p.atKeyword("WITH") && p.i+1 < len(p.toks) && p.toks[p.i+1].Kind == TokenParenOpen {
		if cols, next, ok := parseTableColumnDefs(p.toks, p.i+1); ok {
			rs.Columns, p.i = cols, next
		}
		return rs
	}
	if fn == "OPENJSON" {
		rs.Columns = openJSONDefaultColumns
	}
	return rs
}
