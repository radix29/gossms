package tui

import (
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
// expanded.
//
// It is passed by value — depth is per-branch — but expanding is a shared map,
// deliberately: a recursive CTE (WITH r AS (SELECT * FROM r), legal T-SQL) is
// caught by the name already being on the stack, which only works if every
// branch sees the same set.
type resolveCtx struct {
	inv, sysInv *completionInventory
	ctes        []sqlparse.CTE
	depth       int
	expanding   map[string]bool
}

func newResolveCtx(inv, sysInv *completionInventory, ctes []sqlparse.CTE) resolveCtx {
	return resolveCtx{inv: inv, sysInv: sysInv, ctes: ctes, expanding: map[string]bool{}}
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
// to a catalog object. A schema-qualified ref is never a CTE — "dbo.t1" names a
// real object even when a CTE t1 is in scope.
func resolveRef(rc resolveCtx, ref sqlparse.FromRef) (relation, bool) {
	if ref.Derived != nil {
		cols := queryColumns(rc, ref.Derived)
		return relation{name: ref.Alias, aliased: true, cols: cols}, len(cols) > 0
	}
	name, aliased := ref.Alias, true
	if name == "" {
		name, aliased = ref.Name, false
	}
	if ref.Schema == "" {
		if cte, ok := findCTE(rc.ctes, ref.Name); ok {
			cols := cteColumns(rc, cte)
			return relation{name: name, aliased: aliased, cols: cols}, len(cols) > 0
		}
	}
	if obj := findCatalogObject(rc.inv, rc.sysInv, ref.Schema, ref.Name); obj != nil {
		return relation{name: name, aliased: aliased, obj: obj}, true
	}
	return relation{}, false
}

func findCTE(ctes []sqlparse.CTE, name string) (sqlparse.CTE, bool) {
	for _, cte := range ctes {
		if strings.EqualFold(cte.Name, name) {
			return cte, true
		}
	}
	return sqlparse.CTE{}, false
}

// findRelation matches a dot-qualifier against resolved relations: an alias
// first, then a bare table/CTE name, the two-pass order this replaced
// resolveQualifierToObject with — "FROM c, Customers x" must resolve "c" to the
// table named c, but "FROM Orders c, Customers" must resolve it to the alias.
func findRelation(rels []relation, qualifier string) (relation, bool) {
	for _, wantAlias := range []bool{true, false} {
		for _, r := range rels {
			if r.name != "" && r.aliased == wantAlias && strings.EqualFold(r.name, qualifier) {
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
	key := strings.ToLower(cte.Name)
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
	seen := map[string]bool{}
	add := func(col gosmo.CatalogColumn) {
		key := strings.ToLower(col.Name)
		if col.Name == "" || seen[key] {
			return
		}
		seen[key] = true
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
			if r, ok := findRelation(rels, item.StarQual); ok {
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
			col, ok := selectItemColumn(rels, item)
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
func selectItemColumn(rels []relation, item sqlparse.SelectItem) (gosmo.CatalogColumn, bool) {
	name := item.Alias
	if name == "" {
		name = item.Name
	}
	if name == "" {
		return gosmo.CatalogColumn{}, false
	}
	if item.Name != "" {
		if col, ok := findColumn(rels, item.Qualifier, item.Name); ok {
			col.Name = name
			return col, true
		}
	}
	return gosmo.CatalogColumn{Name: name}, true
}

// findColumn looks name up in the qualified relation, or — unqualified — in
// every relation in scope, first match winning the way SQL Server's own
// unambiguous-reference rule would.
func findColumn(rels []relation, qualifier, name string) (gosmo.CatalogColumn, bool) {
	if qualifier != "" {
		r, ok := findRelation(rels, qualifier)
		if !ok {
			return gosmo.CatalogColumn{}, false
		}
		rels = []relation{r}
	}
	for _, r := range rels {
		for _, col := range r.columns() {
			if strings.EqualFold(col.Name, name) {
				return col, true
			}
		}
	}
	return gosmo.CatalogColumn{}, false
}
