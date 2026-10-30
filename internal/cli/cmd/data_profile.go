package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/query"
)

// maxProfileColumns is the columns one profile statement aggregates. Up to
// seven expressions each keeps a statement far below SQL Server's 4096
// expressions in a SELECT list; a wider object is profiled in several.
const maxProfileColumns = 100

func dataProfileCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"data", "profile"},
		Summary: "Profile a table's or view's columns: nulls, distinct values, ranges, lengths",
		Description: "One aggregate SELECT over the object (several for more than " + strconv.Itoa(maxProfileColumns) +
			" columns), with --where when given, answered as set columns: column, type, rows, nulls, null_pct, " +
			"distinct, min, max, min_len, max_len. It reads every row --where keeps, and COUNT(DISTINCT) sorts or " +
			"hashes each column: on a large table, narrow it with --columns or --where. What a column gets depends " +
			"on its type (an alias type's base type): numbers, dates and times, strings (sysname too), binary, uniqueidentifier, " +
			"sql_variant, hierarchyid and rowversion get distinct, min and max (bit's as 0/1); strings also get " +
			"min_len/max_len in characters (LEN: trailing spaces not counted), binary in bytes. xml, text, ntext, " +
			"image, geography, geometry, vector, CLR types and Always Encrypted columns get rows and nulls alone; " +
			"the rest is NULL. A graph table's internal columns are left out. --top-values n adds set top_values " +
			"(column, value, count): each column's n most frequent values, NULL among them, most frequent first, " +
			"one SELECT … GROUP BY per column that has distinct. A record — object, type, tsql (the aggregate " +
			"SELECTs, GO between) — then the sets.",
		Args: []spec.Arg{dataObjectArg},
		Options: append(ConnectOptions(),
			columnsOption("The columns to profile, comma-separated, in that order: a,[b c]; default every column"),
			whereOption(),
			spec.Option{Name: optTopValues, Type: spec.TypeInt, Default: "0",
				Help: "Add each column's n most frequent values (set top_values); 0 adds none"},
			timeoutOption(),
		),
		Class: spec.ClassRead,
		Run:   d.dataProfile,
		Examples: []string{
			"gossms data profile -p dev Sales.Customer",
			"gossms data profile -p dev Sales.SalesOrderHeader --columns Status,OrderDate --top-values 5 -o json",
		},
	}
}

// profileAgg is what a column's type lets the profile compute beyond rows
// and nulls.
type profileAgg struct {
	distinct bool   // COUNT_BIG(DISTINCT c), and --top-values's GROUP BY c
	minMax   string // MIN/MAX's operand, %s the column; "" for none
	length   string // the length function: LEN (characters), DATALENGTH (bytes); "" for none
}

// profileAggFor is the aggregates a column of system type base gets. A
// type not named — xml, text, ntext, image, geography, geometry, vector, a
// type newer than this list — gets none: each is refused by COUNT(DISTINCT)
// or MIN, or (text, ntext) by LEN.
func profileAggFor(base string) profileAgg {
	switch strings.ToLower(base) {
	case "bit":
		// MIN(bit) is Msg 8117; as tinyint it reads 0/1, as the grid shows a bit.
		return profileAgg{distinct: true, minMax: "CAST(%s AS tinyint)"}
	case "char", "varchar", "nchar", "nvarchar", "sysname":
		// sysname is the one system alias type (nvarchar(128)), not a
		// user-defined one, so no alias lookup resolves it.
		return profileAgg{distinct: true, minMax: "%s", length: "LEN"}
	case "binary", "varbinary":
		return profileAgg{distinct: true, minMax: "%s", length: "DATALENGTH"}
	case "tinyint", "smallint", "int", "bigint", "decimal", "numeric", "money", "smallmoney", "float", "real",
		"date", "time", "datetime", "datetime2", "smalldatetime", "datetimeoffset",
		"uniqueidentifier", "sql_variant", "hierarchyid", "rowversion", "timestamp":
		return profileAgg{distinct: true, minMax: "%s"}
	}
	return profileAgg{}
}

// profiledColumn is a column with the aggregates its type gets.
type profiledColumn struct {
	col *gosmo.Column
	agg profileAgg
}

// profileable reports whether the profile lists c: a graph table's internal
// columns and a ledger table's dropped ones cannot be read by name.
func profileable(c *gosmo.Column) bool { return c.GraphType == 0 && !c.IsDroppedLedgerColumn }

// profiledColumns pairs each column with its aggregates. aliases maps an
// alias type's "schema.name" to its base type; a user-defined type not in
// it is a CLR type. A type the login cannot see has its base type as
// DataType ("" for a CLR type), and an encrypted column's ciphertext has no
// order, so neither gets more than nulls.
func profiledColumns(cols []*gosmo.Column, aliases map[string]string) []profiledColumn {
	out := make([]profiledColumn, len(cols))
	for i, c := range cols {
		base := string(c.DataType)
		if c.IsUserDefinedType && !c.IsTypeHidden {
			base = aliases[c.TypeSchema+"."+base]
		}
		var agg profileAgg
		if c.ColumnEncryptionKey == "" {
			agg = profileAggFor(base)
		}
		out[i] = profiledColumn{col: c, agg: agg}
	}
	return out
}

// The aliases of a profile statement's aggregates for its i-th column.
func nullsAlias(i int) string    { return "nulls_" + strconv.Itoa(i) }
func distinctAlias(i int) string { return "distinct_" + strconv.Itoa(i) }
func minAlias(i int) string      { return "min_" + strconv.Itoa(i) }
func maxAlias(i int) string      { return "max_" + strconv.Itoa(i) }
func minLenAlias(i int) string   { return "min_len_" + strconv.Itoa(i) }
func maxLenAlias(i int) string   { return "max_len_" + strconv.Itoa(i) }

// profileStatement is one aggregate SELECT over cols: COUNT_BIG(*) as
// [rows], then each column's aggregates, aliased by its index in cols.
// Nulls are COUNT_BIG(CASE WHEN c IS NULL …), which every type allows —
// COUNT(c) refuses text, ntext and image.
func profileStatement(cols []profiledColumn, object string, f fragments) string {
	var b strings.Builder
	b.WriteString("SELECT COUNT_BIG(*) AS [rows]")
	agg := func(expr, alias string) {
		b.WriteString(",\n    " + expr + " AS " + gosmo.QuoteName(alias))
	}
	for i, pc := range cols {
		c := gosmo.QuoteName(pc.col.Name)
		agg("COUNT_BIG(CASE WHEN "+c+" IS NULL THEN 1 END)", nullsAlias(i))
		if pc.agg.distinct {
			agg("COUNT_BIG(DISTINCT "+c+")", distinctAlias(i))
		}
		if pc.agg.minMax != "" {
			operand := fmt.Sprintf(pc.agg.minMax, c)
			agg("MIN("+operand+")", minAlias(i))
			agg("MAX("+operand+")", maxAlias(i))
		}
		if pc.agg.length != "" {
			agg("MIN("+pc.agg.length+"("+c+"))", minLenAlias(i))
			agg("MAX("+pc.agg.length+"("+c+"))", maxLenAlias(i))
		}
	}
	return b.String() + "\nFROM " + object + f.compose()
}

// topValuesStatement is --top-values's SELECT for one column: its n most
// frequent values, ties in value order.
func topValuesStatement(c *gosmo.Column, n int, object string, f fragments) string {
	q := gosmo.QuoteName(c.Name)
	return fmt.Sprintf("SELECT TOP (%d) %s AS [value], COUNT_BIG(*) AS [count]\nFROM %s", n, q, object) + f.compose() +
		"\nGROUP BY " + q + "\nORDER BY COUNT_BIG(*) DESC, " + q
}

// profileStatements splits cols into statements of maxProfileColumns each.
func profileStatements(cols []profiledColumn, object string, f fragments) []string {
	if len(cols) == 0 {
		return []string{profileStatement(nil, object, f)}
	}
	var out []string
	for chunk := range slices.Chunk(cols, maxProfileColumns) {
		out = append(out, profileStatement(chunk, object, f))
	}
	return out
}

func (d Deps) dataProfile(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	names, err := readColumnNames(inv)
	if err != nil {
		return nil, err
	}
	frag, err := readFragments(inv)
	if err != nil {
		return nil, err
	}
	topValues := inv.Values[optTopValues].(int)
	if topValues < 0 {
		return nil, usageError(fmt.Sprintf("--top-values: %d is negative", topValues), "Give the values to list per column, or 0 for none.")
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	// Checked before connecting: a refused fragment never reaches the server.
	if res, err := checkOneRead(profileStatement(nil, placeholderObject, frag)); err != nil {
		return res, err
	}

	res := &output.Result{}
	s, err := d.openDataSource(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer s.conn.Close()
	all, err := s.columns(ctx)
	if err != nil {
		return res, err
	}
	// A copy: DeleteFunc would zero the tail of the catalog's own slice.
	all = slices.DeleteFunc(slices.Clone(all), func(c *gosmo.Column) bool { return !profileable(c) })
	if names != nil {
		matched, err := matchColumns(names, all, s.name())
		if err != nil {
			return res, err
		}
		named := make([]*gosmo.Column, len(matched))
		for i, m := range matched {
			named[i] = all[slices.IndexFunc(all, func(c *gosmo.Column) bool { return c.Name == m })]
		}
		all = named
	}
	aliases, err := s.aliasTypes(ctx, all)
	if err != nil {
		return res, err
	}
	cols := profiledColumns(all, aliases)

	stmts := profileStatements(cols, s.name(), frag)
	sends := slices.Clone(stmts)
	if topValues > 0 {
		for _, pc := range cols {
			if pc.agg.distinct {
				sends = append(sends, topValuesStatement(pc.col, topValues, s.name(), frag))
			}
		}
	}
	for _, stmt := range sends {
		if r, err := checkOneRead(stmt); err != nil {
			return r, err
		}
	}
	res.Fields = append(s.fields(), output.Field{Key: "tsql", Value: strings.Join(stmts, "\nGO\n"), Verbatim: true})

	answers := make([]*query.Result, 0, len(sends))
	err = d.runDataSelect(ctx, s, res, timeout, func(ctx context.Context, sess Session, opts []query.Option) *query.Result {
		merged := &query.Result{}
		for _, stmt := range sends {
			qr := sess.Execute(ctx, stmt, opts...)
			// Each answer is a set the command reshapes: its "(n rows
			// affected)" describes nothing the answer shows. A plain notice
			// from these SELECTs is ANSI_WARNINGS' "Null value is eliminated
			// by an aggregate" (8153, in the login's language), which every
			// column holding a NULL raises: the profile counts NULLs on
			// purpose.
			qr.Messages = slices.DeleteFunc(qr.Messages, func(m query.Message) bool {
				return m.RowsAffected != nil && m.RowsAffected.AfterSet || m.RowsAffected == nil && m.SQL == nil && !m.IsError
			})
			merged.Messages = append(merged.Messages, qr.Messages...)
			merged.Batches += qr.Batches
			merged.FailedBatches += qr.FailedBatches
			merged.SessionLost = merged.SessionLost || qr.SessionLost
			if qr.HasErrors() || qr.SessionLost || ctx.Err() != nil {
				break
			}
			answers = append(answers, qr)
		}
		return merged
	})
	if err != nil || len(res.Errors) > 0 || len(answers) < len(sends) {
		return res, err
	}
	set, err := profileSet(cols, answers[:len(stmts)])
	if err != nil {
		return res, err
	}
	res.Sets = append(res.Sets, set)
	if topValues > 0 {
		res.Sets = append(res.Sets, topValuesSet(cols, answers[len(stmts):]))
	}
	return res, nil
}

// aliasTypes maps each alias type among cols' types, "schema.name", to its
// base type; nil when no column has a visible user-defined type, so only a
// profile that needs them reads the database's types.
func (s *dataSource) aliasTypes(ctx context.Context, cols []*gosmo.Column) (map[string]string, error) {
	if !slices.ContainsFunc(cols, func(c *gosmo.Column) bool { return c.IsUserDefinedType && !c.IsTypeHidden }) {
		return nil, nil
	}
	types, err := s.cat.UserDefinedDataTypes(ctx, s.dbase)
	if err != nil {
		return nil, readError(err)
	}
	m := make(map[string]string, len(types))
	for _, t := range types {
		m[t.Schema+"."+t.Name] = t.BaseType
	}
	return m, nil
}

// profileSet is set columns: one row per column, from the aggregate
// statements' one-row answers, cols chunked as profileStatements did.
func profileSet(cols []profiledColumn, answers []*query.Result) (output.Set, error) {
	set := output.Set{Name: "columns", Columns: []output.Column{
		{Name: "column"}, {Name: "type", Nullable: true}, {Name: "rows", Type: "bigint"},
		{Name: "nulls", Type: "bigint"}, {Name: "null_pct", Type: "float", Nullable: true},
		{Name: "distinct", Type: "bigint", Nullable: true}, {Name: "min", Nullable: true}, {Name: "max", Nullable: true},
		{Name: "min_len", Type: "bigint", Nullable: true}, {Name: "max_len", Type: "bigint", Nullable: true},
	}}
	k := 0
	for chunk := range slices.Chunk(cols, maxProfileColumns) {
		qr := answers[k]
		k++
		if len(qr.Sets) != 1 || qr.Sets[0].Len() != 1 {
			return set, output.NewError(output.CodeGeneralError, "the profile's aggregate SELECT returned no row", "")
		}
		rs := qr.Sets[0]
		cell := func(alias string) *string {
			i := slices.Index(rs.Columns, alias)
			if i < 0 || rs.IsNull(0, i) {
				return nil
			}
			return ptr(rs.Rows[0][i])
		}
		rows := cell("rows")
		for i, pc := range chunk {
			nulls := cell(nullsAlias(i))
			set.Rows = append(set.Rows, []*string{
				ptr(pc.col.Name), textOrNull(pc.col.TypeString()), rows, nulls, nullPct(nulls, rows),
				cell(distinctAlias(i)), cell(minAlias(i)), cell(maxAlias(i)), cell(minLenAlias(i)), cell(maxLenAlias(i)),
			})
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set, nil
}

// nullPct is nulls as a percentage of rows, two decimals; NULL with no rows.
func nullPct(nulls, rows *string) *string {
	if nulls == nil || rows == nil {
		return nil
	}
	n, err1 := strconv.ParseInt(*nulls, 10, 64)
	r, err2 := strconv.ParseInt(*rows, 10, 64)
	if err1 != nil || err2 != nil || r == 0 {
		return nil
	}
	return ptr(strconv.FormatFloat(float64(n)*100/float64(r), 'f', 2, 64))
}

// topValuesSet is set top_values: each column with distinct, its answer's
// rows in order.
func topValuesSet(cols []profiledColumn, answers []*query.Result) output.Set {
	set := output.Set{Name: "top_values", Columns: []output.Column{
		{Name: "column"}, {Name: "value", Nullable: true}, {Name: "count", Type: "bigint"},
	}}
	k := 0
	for _, pc := range cols {
		if !pc.agg.distinct {
			continue
		}
		qr := answers[k]
		k++
		if len(qr.Sets) != 1 {
			continue
		}
		rs := qr.Sets[0]
		for r := range rs.Len() {
			var value *string
			if !rs.IsNull(r, 0) {
				value = ptr(rs.Rows[r][0])
			}
			set.Rows = append(set.Rows, []*string{ptr(pc.col.Name), value, ptr(rs.Rows[r][1])})
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
