package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// Candidate resolution against a completionInventory
// ---------------------------------------------------------------------------

// resolveQualifierToRelation resolves a dot-qualifier to the relation it
// names: first an alias, bare table name, CTE name or derived-table alias
// already resolved out of the cursor's own query (the common case), falling
// back to a direct name match across the whole inventory for a table the
// FROM-scope parse missed or that isn't in scope yet. sysInv is consulted too,
// so an alias/bare-name over a "sys.xxx" reference (e.g. "FROM sys.objects o")
// resolves its columns the same way a user table would.
func resolveQualifierToRelation(inv, sysInv *completionInventory, rels []relation, qualifier string) (relation, bool) {
	if r, ok := findRelation(rels, qualifier); ok {
		return r, true
	}
	if obj := findCatalogObjectByName(inv, sysInv, qualifier); obj != nil {
		return relation{name: qualifier, obj: obj}, true
	}
	return relation{}, false
}

func findCatalogObject(inv, sysInv *completionInventory, schema, name string) *gosmo.CatalogObject {
	if schema == "" {
		return findCatalogObjectByName(inv, sysInv, name)
	}
	key := strings.ToLower(schema) + "." + strings.ToLower(name)
	if obj, ok := inv.byQualifiedName[key]; ok {
		return obj
	}
	if sysInv != nil {
		if obj, ok := sysInv.byQualifiedName[key]; ok {
			return obj
		}
	}
	return nil
}

func findCatalogObjectByName(inv, sysInv *completionInventory, name string) *gosmo.CatalogObject {
	nl := strings.ToLower(name)
	for i := range inv.catalog.Objects {
		if strings.ToLower(inv.catalog.Objects[i].Name) == nl {
			return &inv.catalog.Objects[i]
		}
	}
	if sysInv != nil && sysInv.catalog != nil {
		for i := range sysInv.catalog.Objects {
			if strings.ToLower(sysInv.catalog.Objects[i].Name) == nl {
				return &sysInv.catalog.Objects[i]
			}
		}
	}
	return nil
}

// memberCandidates resolves "qualifier.prefix": qualifier is tried first as
// a FROM-scope alias/table/CTE/derived table (-> that relation's columns),
// then as a schema name in the connected database (-> every table/view in
// it), then as a schema name in the sys-schema inventory ("sys" being the
// only one that ever matters there). Nothing matching returns nil, closing
// the popup rather than showing something wrong.
func (p *QueryPanel) memberCandidates(inv, sysInv *completionInventory, rels []relation, qualifier, prefix string) []controls.CompletionItem {
	if r, ok := resolveQualifierToRelation(inv, sysInv, rels, qualifier); ok {
		return p.columnItemsFor(r.columns(), prefix)
	}
	if objs, ok := inv.bySchema[strings.ToLower(qualifier)]; ok {
		return p.objectItems(objs, prefix)
	}
	if sysInv != nil {
		if objs, ok := sysInv.bySchema[strings.ToLower(qualifier)]; ok {
			return p.objectItems(objs, prefix)
		}
		if sysInv.loading && strings.EqualFold(qualifier, "sys") {
			return []controls.CompletionItem{loadingCompletionItem}
		}
	}
	return nil
}

// tableCandidates offers every schema (the connected database's own, plus
// "sys" once its inventory has loaded), every table/view, every visible CTE
// name and every temp table/table variable the batch declares whose name
// contains prefix — the FROM/JOIN/INTO/UPDATE/DELETE/TRUNCATE TABLE
// context, and the fallback when a column context has no FROM-scope yet
// (which passes no CTEs: a CTE name is a relation, not a column).
// The sys-schema inventory's own objects are not mixed into the unqualified
// list below: there are hundreds of them, so they're offered only once a
// query qualifies with "sys." (see memberCandidates).
func (p *QueryPanel) tableCandidates(inv, sysInv *completionInventory, ctes []sqlparse.CTE, bindings []sqlparse.Binding, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, cte := range ctes {
		ok, partial := nameMatch(cte.Name, pl)
		if !ok {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(cte.Name), Label: cte.Name, Detail: "CTE",
			Icon: p.tableIcon(gosmo.CatalogTable), Partial: partial,
		})
	}
	seenBinding := map[string]bool{}
	for i := len(bindings) - 1; i >= 0; i-- {
		// Backwards and deduplicated, to match findBinding: a redeclared name
		// is one candidate, and it is the later declaration's.
		name := bindings[i].Name
		key := strings.ToLower(name)
		ok, partial := nameMatch(key, pl)
		if seenBinding[key] || !ok {
			continue
		}
		seenBinding[key] = true
		detail := "temp table"
		if strings.HasPrefix(name, "@") {
			detail = "table variable"
		}
		// Never bracketed: "[@t]" names a column or object, not the variable,
		// and "[#t]" is legal but not what the user typed.
		items = append(items, controls.CompletionItem{
			Text: name, Label: name, Detail: detail, Icon: p.tableIcon(gosmo.CatalogTable),
			Partial: partial,
		})
	}
	for _, schema := range inv.catalog.Schemas {
		ok, partial := nameMatch(schema, pl)
		if !ok {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
			Partial: partial,
		})
	}
	if sysInv != nil && sysInv.catalog != nil {
		for _, schema := range sysInv.catalog.Schemas {
			ok, partial := nameMatch(schema, pl)
			if !ok {
				continue
			}
			items = append(items, controls.CompletionItem{
				Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
				Partial: partial,
			})
		}
	}
	for i := range inv.catalog.Objects {
		obj := &inv.catalog.Objects[i]
		if ok, partial := nameMatch(obj.Name, pl); ok {
			items = append(items, p.objectItem(obj, partial))
		}
	}
	sortCompletionItems(items)
	return items
}

// objectItems offers every table/view in objs whose name contains
// prefix — a schema's member list ("dbo.").
func (p *QueryPanel) objectItems(objs []*gosmo.CatalogObject, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, obj := range objs {
		if ok, partial := nameMatch(obj.Name, pl); ok {
			items = append(items, p.objectItem(obj, partial))
		}
	}
	sortCompletionItems(items)
	return items
}

func (p *QueryPanel) objectItem(obj *gosmo.CatalogObject, partial bool) controls.CompletionItem {
	detail := "table"
	if obj.Type == gosmo.CatalogView {
		detail = "view"
	}
	return controls.CompletionItem{
		Text: bracketIfNeeded(obj.Name), Label: obj.Schema + "." + obj.Name,
		Detail: detail, Icon: p.tableIcon(obj.Type), Partial: partial,
	}
}

// columnItemsFor offers every column in cols whose name contains prefix —
// the "alias." / "table." member-lookup result.
func (p *QueryPanel) columnItemsFor(cols []gosmo.CatalogColumn, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, col := range cols {
		ok, partial := nameMatch(col.Name, pl)
		if !ok {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(col.Name), Label: col.Name,
			Detail: formatColumnType(col), Icon: p.columnIcon(), Partial: partial,
		})
	}
	sortCompletionItems(items)
	return items
}

// scopedColumnCandidates offers the union of every in-scope relation's
// columns (deduplicated by name — a column present on more than one joined
// table shows once) plus each relation's own alias/table/CTE name, so typing
// "c." after "c" was just offered still works — the unqualified SELECT/
// WHERE/ON/GROUP BY/ORDER BY/HAVING/SET context.
func (p *QueryPanel) scopedColumnCandidates(rels []relation, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	seenCol := make(map[string]bool)
	seenRef := make(map[string]bool)
	for _, rel := range rels {
		for _, col := range rel.columns() {
			key := strings.ToLower(col.Name)
			ok, partial := nameMatch(key, pl)
			if seenCol[key] || !ok {
				continue
			}
			seenCol[key] = true
			detail := formatColumnType(col)
			if rel.name != "" {
				detail += " — " + rel.name
			}
			items = append(items, controls.CompletionItem{
				Text: bracketIfNeeded(col.Name), Label: col.Name,
				Detail: detail, Icon: p.columnIcon(), Partial: partial,
			})
		}
		qkey := strings.ToLower(rel.name)
		ok, partial := nameMatch(qkey, pl)
		if rel.name == "" || seenRef[qkey] || !ok {
			continue
		}
		seenRef[qkey] = true
		objType := gosmo.CatalogTable
		if rel.obj != nil {
			objType = rel.obj.Type
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(rel.name), Label: rel.name, Detail: "table reference", Icon: p.tableIcon(objType),
			Partial: partial,
		})
	}
	sortCompletionItems(items)
	return items
}

// nameMatch reports whether name contains the lower-cased fragment pl
// anywhere — "ord" offers OrderLines and CustomerOrders alike — and whether
// that match is partial, i.e. not at the start. The editor treats a list of
// nothing but partial matches differently (see controls.CompletionItem.Partial).
//
// It runs over every catalog name on every keystroke, so an ASCII name — nearly
// all of them — is searched in place: lower-casing each one first cost 15 ms
// and 50,000 allocations per keystroke against a 50k-object catalog.
func nameMatch(name, pl string) (ok, partial bool) {
	i, ascii := indexLowerASCII(name, pl)
	if !ascii {
		i = strings.Index(strings.ToLower(name), pl)
	}
	return i >= 0, i > 0
}

// indexLowerASCII is strings.Index(strings.ToLower(name), pl) for an ASCII
// name, without building the lowered copy. It reports ascii false, and no
// index, for a name holding any other byte, whose lowering can change its
// length.
func indexLowerASCII(name, pl string) (i int, ascii bool) {
	for k := 0; k < len(name); k++ {
		if name[k] >= utf8.RuneSelf {
			return -1, false
		}
	}
	n := len(pl)
	for i = 0; i+n <= len(name); i++ {
		j := 0
		for ; j < n; j++ {
			c := name[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != pl[j] {
				break
			}
		}
		if j == n {
			return i, true
		}
	}
	return -1, true
}

// sortCompletionItems lists prefix matches before partial ones, each group
// alphabetically, so what the user typed the start of stays on top.
func sortCompletionItems(items []controls.CompletionItem) {
	slices.SortStableFunc(items, func(a, b controls.CompletionItem) int {
		if a.Partial != b.Partial {
			if a.Partial {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
	})
}

// ---------------------------------------------------------------------------
// Icons and formatting
// ---------------------------------------------------------------------------

func (p *QueryPanel) tableIcon(t gosmo.CatalogObjectType) rune {
	nt := NodeTable
	if t == gosmo.CatalogView {
		nt = NodeView
	}
	return nodeIcon(nodeData{Type: nt}, p.app.cfg.IconStyle, false)
}

func (p *QueryPanel) columnIcon() rune {
	return nodeIcon(nodeData{Type: NodeColumn}, p.app.cfg.IconStyle, false)
}

func (p *QueryPanel) schemaIcon() rune {
	return nodeIcon(nodeData{Type: NodeSchema}, p.app.cfg.IconStyle, false)
}

// formatDataTypeLen renders a data type the way SQL Server itself would
// print it: length for the (n)(var)char/binary family (nvarchar/nchar's
// maxLength is stored in bytes, so it's halved back to characters),
// precision/scale for decimal/numeric, and scale alone for the fractional-
// seconds types — MAX for a -1 maxLength either way.
func formatDataTypeLen(dataType string, maxLength, precision, scale int) string {
	t := strings.ToLower(dataType)
	switch t {
	case "varchar", "char", "varbinary", "binary":
		switch {
		case maxLength == -1:
			t += "(MAX)"
		case maxLength > 0:
			t += fmt.Sprintf("(%d)", maxLength)
		}
	case "nvarchar", "nchar":
		switch {
		case maxLength == -1:
			t += "(MAX)"
		case maxLength > 0:
			t += fmt.Sprintf("(%d)", maxLength/2)
		}
	case "decimal", "numeric":
		if precision > 0 {
			t += fmt.Sprintf("(%d,%d)", precision, scale)
		}
	case "datetime2", "time", "datetimeoffset":
		if scale > 0 {
			t += fmt.Sprintf("(%d)", scale)
		}
	}
	return t
}

// formatColumnType renders a CatalogColumn's type plus, when it's not
// nullable, a ", not null" suffix — used by IntelliSense's completion detail
// text, where a nullable column's detail stays bare to save popup width.
//
// A column with no DataType is a synthetic one whose type could not be worked
// out — a derived table's or CTE's expression column. It renders as the bare
// word "column": nullability is unknown too, and a zero CatalogColumn has
// IsNullable false, so the suffix would otherwise assert NOT NULL about every
// column this code knows least about.
func formatColumnType(col gosmo.CatalogColumn) string {
	if col.DataType == "" {
		return "column"
	}
	t := formatDataTypeLen(string(col.DataType), col.MaxLength, col.Precision, col.Scale)
	if !col.IsNullable {
		t += ", not null"
	}
	return t
}

var regularIdentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// bracketIfNeeded returns name as-is when it's a plain identifier and not
// one of sqlKeywordList, or "[name]" (with any ']' doubled) otherwise — so a
// committed candidate never silently changes what it names by needing
// quoting SQL Server would otherwise require.
func bracketIfNeeded(name string) string {
	if regularIdentPattern.MatchString(name) && !sqlparse.IsKeyword(strings.ToUpper(name)) {
		return name
	}
	return gosmo.QuoteName(name)
}
