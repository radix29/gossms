package showplan

import (
	"fmt"
	"strings"

	"github.com/radix29/gosmo/tsql"
)

// missingIndexNamePlaceholder is SSMS's template token for a name to choose.
const missingIndexNamePlaceholder = "<Name of Missing Index, sysname,>"

// Keys returns the key columns in declaration order: EQUALITY first, then
// INEQUALITY.
func (m MissingIndex) Keys() []string {
	keys := make([]string, 0, len(m.Equality)+len(m.Inequality))
	keys = append(keys, m.Equality...)
	return append(keys, m.Inequality...)
}

// CreateStatement returns the one-line CREATE NONCLUSTERED INDEX for the
// suggestion, as the banner shows it. The name is a placeholder; SQL Server
// suggests only columns.
func (m MissingIndex) CreateStatement() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE NONCLUSTERED INDEX [%s] ON %s (%s)",
		missingIndexNamePlaceholder, m.qualifiedTable(), bracketList(m.Keys()))
	if len(m.Include) > 0 {
		fmt.Fprintf(&sb, " INCLUDE (%s)", bracketList(m.Include))
	}
	return sb.String()
}

// Script returns SSMS's "Missing Index Details" block: impact comment, then
// USE/CREATE, commented out as SSMS does since the name is still a placeholder.
func (m MissingIndex) Script() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "/*\nThe Query Processor estimates that implementing the following index could improve the query cost by %.4f%%.\n*/\n\n/*\n", m.Impact)
	if m.Database != "" {
		fmt.Fprintf(&sb, "USE %s\nGO\n", tsql.QuoteName(m.Database))
	}
	fmt.Fprintf(&sb, "CREATE NONCLUSTERED INDEX [%s]\nON %s (%s)\n",
		missingIndexNamePlaceholder, m.qualifiedTable(), bracketList(m.Keys()))
	if len(m.Include) > 0 {
		fmt.Fprintf(&sb, "INCLUDE (%s)\n", bracketList(m.Include))
	}
	sb.WriteString("GO\n*/\n")
	return sb.String()
}

// qualifiedTable is "[schema].[table]"; CREATE INDEX doesn't accept a
// three-part name, so the database goes in the USE.
func (m MissingIndex) qualifiedTable() string {
	return tsql.QualifiedName(m.Schema, m.Table)
}

// MissingIndexScript joins one statement's suggestions into one script, in
// reported order.
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
		out[i] = tsql.QuoteName(c)
	}
	return strings.Join(out, ",")
}
