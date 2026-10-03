package tui

import (
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/sqlparse"
)

// ---------------------------------------------------------------------------
// Relations: what a FROM-scope name resolves to
//
// sqlparse.ScopeAt emits structure only — a query tree of CTEs, derived tables
// and refs, with no idea what a name means. This file turns that structure into
// columns: a catalog table/view for an ordinary ref, and a synthetic column list
// computed from the body for a derived table or a CTE.
//
// Everything here answers "no columns" rather than guessing when it runs out of
// certainty — an unresolvable name, a cycle, a shape the parser dropped. The
// popup closing is the package's stated stance; showing a plausible-looking
// wrong column list is not.
// ---------------------------------------------------------------------------

// relation is one thing a name in FROM-scope can resolve to: a catalog
// table/view, or a synthetic column list computed from a derived table or CTE
// body. name is the alias when there is one, else the table/CTE name, and is
// what a dot-qualifier has to match; it is empty for an unaliased derived
// table, which nothing can qualify. aliased records which of the two it was,
// so an alias still outranks some other ref's bare name (see findRelation).
type relation struct {
	name    string
	aliased bool
	obj     *gosmo.CatalogObject  // non-nil for a real object
	cols    []gosmo.CatalogColumn // set for a synthetic relation
}

func (r relation) columns() []gosmo.CatalogColumn {
	if r.obj != nil {
		return r.obj.Columns
	}
	return r.cols
}

// maxRelationDepth bounds how far nested derived tables and CTE bodies are
// followed. Eight is far past anything hand-written; the cap exists so a
// pathological or half-typed script can't turn one keystroke into an
// exponential walk.
const maxRelationDepth = 8

// resolveCtx carries what name resolution needs down the tree: the two
// inventories, the CTE bindings visible at this point (innermost first), the
// remaining recursion budget, and the set of CTE names currently being
// expanded. otherDB, when set, finds another database's inventory for a
// three-part name (QueryPanel.databaseInventory), linked a remote object for a
// four-part one (QueryPanel.linkedObject), and pending records that one was
// still loading.
//
// It is passed by value — depth is per-branch — but expanding is a shared map,
// deliberately: a recursive CTE (WITH r AS (SELECT * FROM r), legal T-SQL) is
// caught by the name already being on the stack, which only works if every
// branch sees the same set.
type resolveCtx struct {
	inv, sysInv *completionInventory
	ctes        []sqlparse.CTE
	bindings    []sqlparse.Binding
	depth       int
	expanding   map[string]bool
	otherDB     func(name string) (inv *completionInventory, pending bool)
	linked      func(server, database, schema, name string) (obj *gosmo.CatalogObject, pending bool)
	pending     *bool
}

// database returns the loaded inventory a three-part name's database part
// names, or false — noting a load still in flight in rc.pending.
func (rc resolveCtx) database(name string) (*completionInventory, bool) {
	if rc.otherDB == nil {
		return nil, false
	}
	inv, pending := rc.otherDB(name)
	if pending && rc.pending != nil {
		*rc.pending = true
	}
	return inv, inv != nil
}

func newResolveCtx(inv, sysInv *completionInventory, ctes []sqlparse.CTE, bindings []sqlparse.Binding) resolveCtx {
	return resolveCtx{inv: inv, sysInv: sysInv, ctes: ctes, bindings: bindings, expanding: map[string]bool{}}
}

// withCTEs returns a context that also sees ctes, innermost first, so a name
// rebound by an inner WITH shadows the outer one.
func (rc resolveCtx) withCTEs(ctes []sqlparse.CTE) resolveCtx {
	if len(ctes) == 0 {
		return rc
	}
	rc.ctes = append(append([]sqlparse.CTE{}, ctes...), rc.ctes...)
	return rc
}

// resolveRefs resolves every ref of one query level, dropping the ones that
// name nothing — an unknown table, a CTE whose body resolved to nothing.
func resolveRefs(rc resolveCtx, refs []sqlparse.FromRef) []relation {
	var rels []relation
	for _, ref := range refs {
		if rel, ok := resolveRef(rc, ref); ok {
			rels = append(rels, rel)
		}
	}
	return rels
}

// resolveRef resolves one FROM/JOIN/APPLY ref: a derived table to its body's
// columns, a bare name matching a visible CTE to that CTE's, and anything else
// to a catalog object — in another database's catalog for a three-part name.
// A schema- or database-qualified ref is never a CTE — "dbo.t1" names a real
// object even when a CTE t1 is in scope — and a linked server's four-part
// name resolves through that server's remote catalog (completion_linked.go).
func resolveRef(rc resolveCtx, ref sqlparse.FromRef) (relation, bool) {
	if ref.Pivot != nil {
		return resolvePivotRef(rc, ref)
	}
	if ref.Server != "" {
		return resolveLinkedRef(rc, ref)
	}
	if ref.Derived != nil {
		cols := queryColumns(rc, ref.Derived)
		return relation{name: ref.Alias, aliased: true, cols: cols}, len(cols) > 0
	}
	if ref.Rowset != nil {
		// A rowset function names no catalog object, so an unaliased one is
		// as unqualifiable as an unaliased derived table.
		cols := declaredColumns(ref.Rowset.Columns)
		return relation{name: ref.Alias, aliased: true, cols: cols}, len(cols) > 0
	}
	name, aliased := ref.Alias, true
	if name == "" {
		name, aliased = ref.Name, false
	}
	if ref.Schema == "" {
		if sqlparse.HasSigil(ref.Name) {
			// A temp table or table variable. The catalog can never hold one,
			// so a name the batch bound nothing to resolves to nothing rather
			// than falling through to an object sharing the name without its
			// sigil — which is what "FROM #Orders" did before the tokenizer
			// kept the '#'. A database part is ignored, as the server ignores
			// it: "tempdb..#t" and "x..#t" both name the session's #t.
			b, ok := findBinding(rc.bindings, ref.Name)
			if !ok {
				return relation{}, false
			}
			cols := bindingColumns(rc, b)
			return relation{name: name, aliased: aliased, cols: cols}, len(cols) > 0
		}
		if cte, ok := findCTE(rc.inv.collation, rc.ctes, ref.Name); ok && ref.Database == "" {
			cols := cteColumns(rc, cte)
			return relation{name: name, aliased: aliased, cols: cols}, len(cols) > 0
		}
	}
	inv, schemas := rc.inv, []string{ref.Schema}
	if ref.Database != "" {
		other, ok := rc.database(ref.Database)
		if !ok {
			return relation{}, false
		}
		inv, schemas = other, other.qualifierSchemas(ref.Schema)
	}
	for _, schema := range schemas {
		if obj := findCatalogObject(inv, rc.sysInv, schema, ref.Name); obj != nil {
			return relation{name: name, aliased: aliased, obj: obj}, true
		}
	}
	if ref.Call {
		// A table-valued function: its result columns, as the catalog
		// records them. Tried after the tables, which is what a
		// "t (NOLOCK)" hint also parses as.
		for _, schema := range schemas {
			if fn := findCatalogFunction(inv, rc.sysInv, schema, ref.Name); fn != nil {
				return relation{name: name, aliased: aliased, obj: fn}, true
			}
		}
	}
	return relation{}, false
}

// resolveLinkedRef resolves a four-part ref to the remote table or view. A
// remote table-valued function is never one: a four-part name cannot call it.
func resolveLinkedRef(rc resolveCtx, ref sqlparse.FromRef) (relation, bool) {
	if rc.linked == nil {
		return relation{}, false
	}
	obj, pending := rc.linked(ref.Server, ref.Database, ref.Schema, ref.Name)
	if pending && rc.pending != nil {
		*rc.pending = true
	}
	if obj == nil {
		return relation{}, false
	}
	name, aliased := ref.Alias, true
	if name == "" {
		name, aliased = ref.Name, false
	}
	return relation{name: name, aliased: aliased, obj: obj}, true
}

// resolvePivotRef resolves the reference a PIVOT/UNPIVOT clause is applied to
// and reshapes its columns. The source is resolved with the clause and the
// pivoted alias stripped off, so this recurses exactly once.
func resolvePivotRef(rc resolveCtx, ref sqlparse.FromRef) (relation, bool) {
	src := ref
	src.Pivot, src.Alias = nil, ""
	base, ok := resolveRef(rc, src)
	if !ok {
		return relation{}, false
	}
	cols := pivotColumns(rc.inv.collation, base.columns(), ref.Pivot)
	return relation{name: ref.Alias, aliased: ref.Alias != "", cols: cols}, len(cols) > 0
}

// pivotColumns applies a PIVOT/UNPIVOT clause to the source's columns:
//
//   - PIVOT drops the aggregated column and the one it spreads, and adds one
//     column per IN-list name, typed by pivotAggregateType — untyped for an
//     aggregate it doesn't model.
//   - UNPIVOT drops the IN-list columns and adds the value column and the name
//     column. The value column's type is the one the unpivoted columns share —
//     T-SQL requires them to share one — so it is taken from the first of them
//     that the source actually has; the name column's is unknown.
//
// A name the source doesn't carry simply drops nothing, so a half-typed clause
// costs columns it shouldn't rather than inventing ones it can't have.
func pivotColumns(collation string, src []gosmo.CatalogColumn, pv *sqlparse.Pivot) []gosmo.CatalogColumn {
	drop := newNameSet(collation)
	var added []gosmo.CatalogColumn
	if pv.Unpivot {
		for _, n := range pv.In {
			drop.Add(n)
		}
		value := gosmo.CatalogColumn{Name: pv.Value}
		for _, n := range pv.In {
			if col, ok := findColumnIn(collation, src, n); ok {
				value = col
				value.Name = pv.Value
				break
			}
		}
		added = append(added, value, gosmo.CatalogColumn{Name: pv.For})
	} else {
		drop.Add(pv.Agg)
		drop.Add(pv.For)
		arg, _ := findColumnIn(collation, src, pv.Agg)
		typed := pivotAggregateType(pv.Func, arg)
		for _, n := range pv.In {
			col := typed
			col.Name = n
			added = append(added, col)
		}
	}

	cols := make([]gosmo.CatalogColumn, 0, len(src)+len(added))
	for _, col := range src {
		if col.Name == "" || !drop.Has(col.Name) {
			cols = append(cols, col)
		}
	}
	return append(cols, added...)
}

// pivotAggregateType is the column a PIVOT's aggregate fn over arg produces —
// arg is the zero column when the aggregate names none (COUNT(*)) or the
// source lacks it. The built-in aggregates PIVOT accepts are modelled, by the
// rules sys.dm_exec_describe_first_result_set reports (on 13 and 17):
//
//   - COUNT is int, COUNT_BIG and APPROX_COUNT_DISTINCT bigint, over anything;
//   - MIN/MAX keep the argument's type;
//   - SUM/AVG widen it: tinyint/smallint/int to int, bigint stays, decimal and
//     numeric to precision 38 (AVG's scale at least 6), smallmoney to money,
//     real to float;
//   - STDEV/STDEVP/VAR/VARP are float over any numeric argument.
//
// CHECKSUM_AGG and STRING_AGG are absent because PIVOT refuses them (Msg 406,
// "not invariant to NULLs"). Anything else — another aggregate, a qualified
// (user-defined) one, an argument of unknown or non-numeric type — is untyped:
// nothing rather than wrong. Every output is nullable: an IN value with no rows
// reads NULL.
func pivotAggregateType(fn string, arg gosmo.CatalogColumn) gosmo.CatalogColumn {
	untyped := gosmo.CatalogColumn{IsNullable: true}
	switch strings.ToUpper(fn) {
	case "COUNT":
		return gosmo.CatalogColumn{DataType: gosmo.DataTypeInt, IsNullable: true}
	case "COUNT_BIG", "APPROX_COUNT_DISTINCT":
		return gosmo.CatalogColumn{DataType: gosmo.DataTypeBigInt, IsNullable: true}
	case "MIN", "MAX":
		if arg.DataType == "" {
			return untyped
		}
		arg.Name, arg.IsNullable = "", true
		return arg
	case "SUM", "AVG":
		col := gosmo.CatalogColumn{IsNullable: true}
		switch dt := gosmo.DataType(strings.ToLower(string(arg.DataType))); dt {
		case gosmo.DataTypeTinyInt, gosmo.DataTypeSmallInt, gosmo.DataTypeInt:
			col.DataType = gosmo.DataTypeInt
		case gosmo.DataTypeBigInt:
			col.DataType = gosmo.DataTypeBigInt
		case gosmo.DataTypeMoney, gosmo.DataTypeSmallMoney:
			col.DataType = gosmo.DataTypeMoney
		case gosmo.DataTypeFloat, gosmo.DataTypeReal:
			col.DataType = gosmo.DataTypeFloat
		case gosmo.DataTypeDecimal, gosmo.DataTypeNumeric:
			col.DataType, col.Precision, col.Scale = dt, 38, arg.Scale
			if strings.EqualFold(fn, "AVG") {
				col.Scale = max(arg.Scale, 6)
			}
		default:
			return untyped
		}
		return col
	case "STDEV", "STDEVP", "VAR", "VARP":
		switch gosmo.DataType(strings.ToLower(string(arg.DataType))) {
		case gosmo.DataTypeTinyInt, gosmo.DataTypeSmallInt, gosmo.DataTypeInt, gosmo.DataTypeBigInt,
			gosmo.DataTypeDecimal, gosmo.DataTypeNumeric, gosmo.DataTypeMoney, gosmo.DataTypeSmallMoney,
			gosmo.DataTypeFloat, gosmo.DataTypeReal:
			return gosmo.CatalogColumn{DataType: gosmo.DataTypeFloat, IsNullable: true}
		}
		return untyped
	}
	return untyped
}

// findColumnIn, findCTE, findRelation and findColumn compare names the way
// the database's collation does: on a case-sensitive one `Orders` and
// `orders` are two tables (or CTEs, aliases, columns), and folding them let
// "FROM dbo.Orders JOIN dbo.orders" resolve `orders.` to whichever came first.
func findColumnIn(collation string, cols []gosmo.CatalogColumn, name string) (gosmo.CatalogColumn, bool) {
	for _, col := range cols {
		if gosmo.SameName(collation, col.Name, name) {
			return col, true
		}
	}
	return gosmo.CatalogColumn{}, false
}

// findBinding matches a sigil-carrying name against the batch's declarations,
// last one winning: a script that drops and recreates #t means the later
// shape, and the earlier declaration is history by the time the cursor is
// below it.
//
// Unlike the finders above it folds case whatever the database's collation:
// a #temp table's name follows tempdb's collation and a @variable's the
// server's, and the panel reads neither, so it keeps the case-insensitive
// default every install ships with.
func findBinding(bindings []sqlparse.Binding, name string) (sqlparse.Binding, bool) {
	for i := len(bindings) - 1; i >= 0; i-- {
		if strings.EqualFold(bindings[i].Name, name) {
			return bindings[i], true
		}
	}
	return sqlparse.Binding{}, false
}

// bindingColumns computes a temp table's or table variable's columns: the
// declared list for a CREATE TABLE / DECLARE ... TABLE, the query's own shape
// for a SELECT ... INTO. The expanding guard is cteColumns' — "SELECT * INTO
// #t FROM #t" is not legal T-SQL, but a half-typed script is not legal T-SQL
// either, and this runs on every keystroke of one.
func bindingColumns(rc resolveCtx, b sqlparse.Binding) []gosmo.CatalogColumn {
	if b.Columns != nil {
		return declaredColumns(b.Columns)
	}
	if b.Query == nil {
		return nil
	}
	key := strings.ToLower(b.Name)
	if rc.expanding[key] {
		return nil
	}
	rc.expanding[key] = true
	defer delete(rc.expanding, key)
	return queryColumns(rc, b.Query)
}

// declaredColumns turns a declared column list — a temp table's, a table
// variable's, a rowset function's WITH list — into catalog columns.
func declaredColumns(decl []sqlparse.BindingColumn) []gosmo.CatalogColumn {
	if decl == nil {
		return nil
	}
	cols := make([]gosmo.CatalogColumn, 0, len(decl))
	for _, c := range decl {
		cols = append(cols, bindingColumn(c))
	}
	return cols
}

// bindingColumn turns one declared column into the catalog shape the rest of
// completion works in, filling the same length/precision/scale fields
// gosmo.TypeString reads back — so a declared "nvarchar(50)" renders exactly
// as the catalog's own nvarchar(50) would. That means doubling the declared
// character count: sys.columns stores an nvarchar's max_length in bytes, and
// gosmo.TypeString halves it again on the way out. Likewise a fractional-
// seconds type declared with no scale gets the 7 the server gives it, since
// gosmo.TypeString renders a zero scale as the (0) it is in the catalog.
func bindingColumn(c sqlparse.BindingColumn) gosmo.CatalogColumn {
	t := strings.ToLower(c.Type)
	col := gosmo.CatalogColumn{Name: c.Name, DataType: gosmo.DataType(t), IsNullable: c.Nullable}
	switch t {
	case "varchar", "char", "varbinary", "binary":
		col.MaxLength, _ = typeArgInt(c.TypeArgs, 0)
	case "nvarchar", "nchar":
		if n, ok := typeArgInt(c.TypeArgs, 0); ok && n > 0 {
			col.MaxLength = n * 2
		} else {
			col.MaxLength = n
		}
	case "decimal", "numeric":
		col.Precision, _ = typeArgInt(c.TypeArgs, 0)
		col.Scale, _ = typeArgInt(c.TypeArgs, 1)
	case "datetime2", "time", "datetimeoffset":
		if n, ok := typeArgInt(c.TypeArgs, 0); ok {
			col.Scale = n
		} else {
			col.Scale = 7
		}
	}
	return col
}

// typeArgInt reads one type argument as a number, with MAX as the -1 the
// catalog stores for it. Anything else leaves the field zero, which
// gosmo.TypeString renders as the bare type name rather than a length read
// out of a fragment.
func typeArgInt(args []string, i int) (int, bool) {
	if i >= len(args) {
		return 0, false
	}
	if strings.EqualFold(args[i], "MAX") {
		return -1, true
	}
	n, err := strconv.Atoi(args[i])
	if err != nil {
		return 0, false
	}
	return n, true
}

func findCTE(collation string, ctes []sqlparse.CTE, name string) (sqlparse.CTE, bool) {
	for _, cte := range ctes {
		if gosmo.SameName(collation, cte.Name, name) {
			return cte, true
		}
	}
	return sqlparse.CTE{}, false
}

// findRelation matches a dot-qualifier against resolved relations: an alias
// first, then a bare table/CTE name, the two-pass order this replaced
// resolveQualifierToObject with — "FROM c, Customers x" must resolve "c" to the
// table named c, but "FROM Orders c, Customers" must resolve it to the alias.
func findRelation(collation string, rels []relation, qualifier string) (relation, bool) {
	for _, wantAlias := range []bool{true, false} {
		for _, r := range rels {
			if r.name != "" && r.aliased == wantAlias && gosmo.SameName(collation, r.name, qualifier) {
				return r, true
			}
		}
	}
	return relation{}, false
}

// cteColumns computes one CTE's column list. An explicit "(a, b, c)" list names
// the columns and the body types them positionally — but only when the counts
// agree, since a mismatch means one side was misparsed and the pairing would be
// arbitrary. The names still stand: they are what the query actually refers to.
func cteColumns(rc resolveCtx, cte sqlparse.CTE) []gosmo.CatalogColumn {
	if cte.Body == nil {
		return nil
	}
	key := foldName(gosmo.CollationIgnoresCase(rc.inv.collation), cte.Name)
	if rc.expanding[key] {
		return nil // recursive CTE, or a name bound to itself
	}
	rc.expanding[key] = true
	defer delete(rc.expanding, key)

	body := queryColumns(rc, cte.Body)
	if cte.Columns == nil {
		return body
	}
	cols := make([]gosmo.CatalogColumn, len(cte.Columns))
	if len(body) == len(cte.Columns) {
		copy(cols, body)
	}
	for i, name := range cte.Columns {
		cols[i].Name = name
	}
	return cols
}

// queryColumns computes the columns one query produces: its select list
// resolved against its own FROM refs, or — for a query with no select list at
// all, which is what a parenthesised join parses as — the union of those refs'
// own columns.
//
// A column the select list names but nothing in scope can type becomes a
// synthetic gosmo.CatalogColumn with an empty DataType; formatColumnType
// renders that as the bare word "column" rather than asserting a type or
// nullability it doesn't know.
func queryColumns(rc resolveCtx, q *sqlparse.Query) []gosmo.CatalogColumn {
	if q == nil || rc.depth >= maxRelationDepth {
		return nil
	}
	rc = rc.withCTEs(q.CTEs)
	rc.depth++
	rels := resolveRefs(rc, q.From)

	var cols []gosmo.CatalogColumn
	seen := newNameSet(rc.inv.collation)
	add := func(col gosmo.CatalogColumn) {
		if col.Name == "" || seen.Has(col.Name) {
			return
		}
		seen.Add(col.Name)
		cols = append(cols, col)
	}

	if len(q.Select) == 0 {
		for _, r := range rels {
			for _, col := range r.columns() {
				add(col)
			}
		}
		return cols
	}

	for _, item := range q.Select {
		switch {
		case item.Star && item.StarQual != "":
			if r, ok := findRelation(rc.inv.collation, rels, item.StarQual); ok {
				for _, col := range r.columns() {
					add(col)
				}
			}
		case item.Star:
			for _, r := range rels {
				for _, col := range r.columns() {
					add(col)
				}
			}
		default:
			col, ok := selectItemColumn(rc.inv.collation, rels, item)
			if ok {
				add(col)
			}
		}
	}
	return cols
}

// selectItemColumn resolves one non-star select item to a column: the real one
// when a relation in scope carries it, an untyped one carrying just the name
// otherwise. An item with neither a name nor an alias — an unaliased expression
// — names nothing and is dropped.
func selectItemColumn(collation string, rels []relation, item sqlparse.SelectItem) (gosmo.CatalogColumn, bool) {
	name := item.Alias
	if name == "" {
		name = item.Name
	}
	if name == "" {
		return gosmo.CatalogColumn{}, false
	}
	if item.Name != "" {
		if col, ok := findColumn(collation, rels, item.Qualifier, item.Name); ok {
			col.Name = name
			return col, true
		}
	}
	return gosmo.CatalogColumn{Name: name}, true
}

// findColumn looks name up in the qualified relation, or — unqualified — in
// every relation in scope, first match winning the way SQL Server's own
// unambiguous-reference rule would.
func findColumn(collation string, rels []relation, qualifier, name string) (gosmo.CatalogColumn, bool) {
	if qualifier != "" {
		r, ok := findRelation(collation, rels, qualifier)
		if !ok {
			return gosmo.CatalogColumn{}, false
		}
		rels = []relation{r}
	}
	for _, r := range rels {
		for _, col := range r.columns() {
			if gosmo.SameName(collation, col.Name, name) {
				return col, true
			}
		}
	}
	return gosmo.CatalogColumn{}, false
}
