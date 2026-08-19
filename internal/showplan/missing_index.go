package showplan

import (
	"fmt"
	"strings"
)

// missingIndexNamePlaceholder is the token SSMS leaves in place of a name
// the operator has to choose, in the same <name, type,> shape as its other
// script templates.
const missingIndexNamePlaceholder = "<Name of Missing Index, sysname,>"

// Keys returns the index's key columns in the order they have to be
// declared: every EQUALITY column first, then the INEQUALITY ones.
func (m MissingIndex) Keys() []string {
	keys := make([]string, 0, len(m.Equality)+len(m.Inequality))
	keys = append(keys, m.Equality...)
	return append(keys, m.Inequality...)
}

// CreateStatement returns the CREATE NONCLUSTERED INDEX statement that
// implements the suggestion, as one line — the form the missing-index
// banner shows. The index name is left as a placeholder: SQL Server
// suggests the columns, never a name.
func (m MissingIndex) CreateStatement() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE NONCLUSTERED INDEX [%s] ON %s (%s)",
		missingIndexNamePlaceholder, m.qualifiedTable(), bracketList(m.Keys()))
	if len(m.Include) > 0 {
		fmt.Fprintf(&sb, " INCLUDE (%s)", bracketList(m.Include))
	}
	return sb.String()
}

// Script returns the suggestion as SSMS's "Missing Index Details" block:
// the impact as a comment, then the USE/CREATE pair. The DDL is commented
// out exactly as SSMS leaves it — the index name is still a placeholder, so
// the script cannot run as it stands and must be reviewed first.
func (m MissingIndex) Script() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "/*\nThe Query Processor estimates that implementing the following index could improve the query cost by %.4f%%.\n*/\n\n/*\n", m.Impact)
	if m.Database != "" {
		fmt.Fprintf(&sb, "USE %s\nGO\n", bracket(m.Database))
	}
	fmt.Fprintf(&sb, "CREATE NONCLUSTERED INDEX [%s]\nON %s (%s)\n",
		missingIndexNamePlaceholder, m.qualifiedTable(), bracketList(m.Keys()))
	if len(m.Include) > 0 {
		fmt.Fprintf(&sb, "INCLUDE (%s)\n", bracketList(m.Include))
	}
	sb.WriteString("GO\n*/\n")
	return sb.String()
}

// qualifiedTable is the index's target as "[schema].[table]" — the form
// CREATE INDEX takes, which is why the database is not part of it (it goes
// in the USE above; a three-part name is not valid here).
func (m MissingIndex) qualifiedTable() string {
	if m.Schema == "" {
		return bracket(m.Table)
	}
	return bracket(m.Schema) + "." + bracket(m.Table)
}

// MissingIndexScript joins every suggestion of one statement into a single
// script, in the order SQL Server reported them.
func MissingIndexScript(indexes []MissingIndex) string {
	parts := make([]string, 0, len(indexes))
	for _, m := range indexes {
		parts = append(parts, m.Script())
	}
	return strings.Join(parts, "\n")
}

func bracketList(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = bracket(c)
	}
	return strings.Join(out, ",")
}

// bracket wraps one identifier the way QUOTENAME does, doubling any embedded
// closing bracket. Without the doubling a name containing ']' — legal in SQL
// Server, and reproduced verbatim from the plan XML — closes the bracket early
// and the generated CREATE INDEX no longer parses.
//
// Deliberately not gosmo.QuoteName: this package parses XML and has no other
// dependency, and reaching for QuoteName would pull the mssql driver in behind
// it just for this.
func bracket(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
