package tui

import (
	"slices"
	"strings"
	"unicode/utf8"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

// ---------------------------------------------------------------------------
// Candidate resolution against a completionInventory
// ---------------------------------------------------------------------------

// statementProcedureContext recognizes a statement's first name, optionally
// after EXEC/EXECUTE and a return-status assignment. Qualifier parts precede
// the current replacement span.
func statementProcedureContext(buf []rune, tokens []sqlparse.Token, from, qualifiers int) bool {
	n := 0
	for n < len(tokens) && tokens[n].Start < from {
		n++
	}
	head := tokens[:n]
	if len(head) > 0 && (keywordIs(head[0], "EXEC") || keywordIs(head[0], "EXECUTE")) {
		head = head[1:]
		if len(head) > 0 && !head[0].Quoted && sqlparse.HasSigil(head[0].Text) {
			next := keywordNextToken(buf[:from], head[0].Start+len([]rune(head[0].Text)))
			if next.Kind != sqltext.KindPunct || buf[next.Start] != '=' {
				return false
			}
			head = head[1:]
		}
	}
	if len(head) != qualifiers*2 {
		return false
	}
	for i := 0; i < len(head); i += 2 {
		if head[i].Kind != sqlparse.TokenIdent || head[i+1].Kind != sqlparse.TokenDot {
			return false
		}
	}
	return true
}

func (p *QueryPanel) ddlTargetCandidates(inv *completionInventory, action, objectType string, chain []string, prefix string) ([]editor.CompletionItem, bool) {
	if len(chain) > 2 || len(chain) == 2 && (objectType != "TABLE" || p.conn.IsFabricDW()) {
		return nil, false
	}
	var schemas []string
	if len(chain) == 1 {
		if slices.ContainsFunc(inv.catalog.Schemas, func(schema string) bool { return inv.sameName(schema, chain[0]) }) {
			schemas = []string{chain[0]}
		} else if objectType == "TABLE" && !p.conn.IsFabricDW() {
			other, pending := p.databaseInventory(inv, chain[0])
			if other == nil {
				return nil, pending
			}
			return p.databaseSchemaItems(other, nil, prefix), false
		} else {
			return nil, false
		}
	} else if len(chain) == 2 {
		other, pending := p.databaseInventory(inv, chain[0])
		if other == nil {
			return nil, pending
		}
		inv, schemas = other, other.qualifierSchemas(chain[1])
	}
	var items []editor.CompletionItem
	if len(chain) == 0 {
		items = p.databaseSchemaItems(inv, nil, prefix)
		if objectType == "TABLE" && !p.conn.IsFabricDW() {
			items = append(items, p.databaseItems(strings.ToLower(prefix))...)
		}
	}
	if action != "CREATE" {
		if objectType == "PROCEDURE" {
			items = append(items, p.procedureCandidates(inv, chain, prefix)...)
		} else {
			objects := inv.catalog.Objects
			if objectType == "FUNCTION" {
				objects = inv.catalog.Functions
			}
			seen := newNameSet(inv.collation)
			if len(schemas) == 0 {
				schemas = []string{""}
			}
			for _, schema := range schemas {
				for i := range objects {
					obj := &objects[i]
					if schema != "" && !inv.sameName(obj.Schema, schema) ||
						objectType == "TABLE" && obj.Type != gosmo.CatalogTable ||
						objectType == "VIEW" && obj.Type != gosmo.CatalogView ||
						len(chain) > 0 && seen.Has(obj.Name) {
						continue
					}
					if ok, partial := nameMatch(obj.Name, strings.ToLower(prefix)); ok {
						item := p.objectItem(obj, partial)
						if len(chain) == 0 {
							item.Text = gosmo.QuoteNameIfNeeded(obj.Schema) + "." + item.Text
						}
						if objectType == "FUNCTION" {
							item.Detail = "function"
						}
						item.AppendSpace = true
						items = append(items, item)
						seen.Add(obj.Name)
					}
				}
			}
		}
	}
	sortCompletionItems(items)
	return items, false
}

func (p *QueryPanel) procedureCandidates(inv *completionInventory, chain []string, prefix string) []editor.CompletionItem {
	if len(chain) > 1 {
		return nil
	}
	schema := ""
	if len(chain) == 1 {
		schema = chain[0]
	}
	var items []editor.CompletionItem
	pl := strings.ToLower(prefix)
	for _, proc := range inv.procedures {
		if schema != "" && !inv.sameName(proc.Schema, schema) {
			continue
		}
		label := proc.Schema + "." + proc.Name
		ok, partial := nameMatch(proc.Name, pl)
		if schema == "" {
			if full, mid := nameMatch(label, pl); full {
				partial = mid && (!ok || partial)
				ok = true
			}
		}
		if !ok {
			continue
		}
		text := gosmo.QuoteNameIfNeeded(proc.Name)
		if schema == "" {
			text = gosmo.QuoteNameIfNeeded(proc.Schema) + "." + text
		}
		items = append(items, editor.CompletionItem{
			Text: text, Label: label, Detail: "stored procedure", Partial: partial, AppendSpace: true,
		})
	}
	sortCompletionItemsInContext(items, false, false)
	return items
}

// resolveQualifierToRelation resolves a dot-qualifier to the relation it
// names: first an alias, bare table name, CTE name or derived-table alias
// already resolved out of the cursor's own query (the common case), falling
// back to a direct name match across the whole inventory for a table the
// FROM-scope parse missed or that isn't in scope yet. sysInv is consulted too,
// so an alias/bare-name over a "sys.xxx" reference (e.g. "FROM sys.objects o")
// resolves its columns the same way a user table would.
func resolveQualifierToRelation(inv, sysInv *completionInventory, rels []relation, qualifier string) (relation, bool) {
	if r, ok := findRelation(inv.collation, rels, qualifier); ok {
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
	key := qualifiedKey(schema, name)
	if obj, ok := inv.byQualifiedName.Get(key); ok {
		return obj
	}
	if sysInv != nil {
		if obj, ok := sysInv.byQualifiedName.Get(key); ok {
			return obj
		}
	}
	return nil
}

func findCatalogObjectByName(inv, sysInv *completionInventory, name string) *gosmo.CatalogObject {
	for _, in := range []*completionInventory{inv, sysInv} {
		if in == nil || in.catalog == nil {
			continue
		}
		for i := range in.catalog.Objects {
			if in.sameName(in.catalog.Objects[i].Name, name) {
				return &in.catalog.Objects[i]
			}
		}
	}
	return nil
}

// findCatalogFunction is findCatalogObject over table-valued functions: the
// user database first, then the sys schema's (sys.dm_exec_sql_text and the
// like). An unqualified name matches in any schema, as an unqualified table
// does.
func findCatalogFunction(inv, sysInv *completionInventory, schema, name string) *gosmo.CatalogObject {
	for _, in := range []*completionInventory{inv, sysInv} {
		if in == nil || in.catalog == nil {
			continue
		}
		if schema != "" {
			if fn, ok := in.fnByQualifiedName.Get(qualifiedKey(schema, name)); ok {
				return fn
			}
			continue
		}
		for i := range in.catalog.Functions {
			if in.sameName(in.catalog.Functions[i].Name, name) {
				return &in.catalog.Functions[i]
			}
		}
	}
	return nil
}

// memberCandidates resolves "qualifier.prefix": qualifier is tried first as
// a FROM-scope alias/table/CTE/derived table (-> that relation's columns),
// then as a schema name in the connected database (-> every table/view in
// it), then as a schema name in the sys-schema inventory ("sys" or
// INFORMATION_SCHEMA), then as a database name (-> that
// database's schemas, see databaseInventory), and last as a linked server
// (-> its databases, see linkedChainCandidates). Nothing matching returns nil,
// closing the popup rather than showing something wrong; wait reports that
// the database answer is still loading.
func (p *QueryPanel) memberCandidates(inv, sysInv *completionInventory, rels []relation, qualifier, prefix string) (items []editor.CompletionItem, wait bool) {
	if r, ok := resolveQualifierToRelation(inv, sysInv, rels, qualifier); ok {
		return p.columnItemsFor(r.columns(), prefix), false
	}
	if objs, ok := inv.bySchema.Get(qualifier); ok {
		return p.objectItems(objs, prefix), false
	}
	if sysInv != nil {
		if objs, ok := sysInv.bySchema.Get(qualifier); ok {
			return p.objectItems(objs, prefix), false
		}
		if sysInv.loading && (strings.EqualFold(qualifier, "sys") || strings.EqualFold(qualifier, "INFORMATION_SCHEMA")) {
			return []editor.CompletionItem{loadingCompletionItem}, false
		}
	}
	other, pending := p.databaseInventory(inv, qualifier)
	if pending {
		return nil, true
	}
	if other == nil {
		return p.linkedChainCandidates([]string{qualifier}, prefix)
	}
	return p.databaseSchemaItems(other, sysInv, prefix), false
}

func (p *QueryPanel) bindingItems(bindings []sqlparse.Binding, prefix string) []editor.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []editor.CompletionItem
	seenBinding := map[string]bool{}
	for _, binding := range slices.Backward(bindings) {
		// Backwards and deduplicated, to match findBinding: a redeclared name
		// is one candidate, and it is the later declaration's.
		name := binding.Name
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
		items = append(items, editor.CompletionItem{
			Text: name, Label: name, Detail: detail, Icon: p.tableIcon(gosmo.CatalogTable),
			Partial: partial,
		})
	}
	return items
}

// tableCandidates offers every schema (the connected database's own, plus "sys"
// once its inventory has loaded), every table/view, every visible CTE name, every
// temp table/table variable the batch declares and every ONLINE database on the
// server (the start of a three-part name) and queryable linked server (a
// four-part one) whose name contains prefix — the FROM/JOIN/INTO/UPDATE/DELETE/
// TRUNCATE TABLE context, and the fallback when a column context has no FROM-scope
// yet (which passes no CTEs: a CTE name is a relation, not a column). The
// sys-schema inventory's own objects are not mixed into the unqualified list:
// there are hundreds, so they're offered only once a query qualifies with "sys."
// (see memberCandidates).
func (p *QueryPanel) tableCandidates(inv, sysInv *completionInventory, ctes []sqlparse.CTE, bindings []sqlparse.Binding, prefix string) []editor.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []editor.CompletionItem
	for _, cte := range ctes {
		ok, partial := nameMatch(cte.Name, pl)
		if !ok {
			continue
		}
		items = append(items, editor.CompletionItem{
			Text: gosmo.QuoteNameIfNeeded(cte.Name), Label: cte.Name, Detail: "CTE",
			Icon: p.tableIcon(gosmo.CatalogTable), Partial: partial,
		})
	}
	items = append(items, p.bindingItems(bindings, prefix)...)
	items = append(items, p.schemaItems(inv.catalog.Schemas, pl)...)
	if sysInv != nil && sysInv.catalog != nil {
		items = append(items, p.schemaItems(sysInv.catalog.Schemas, pl)...)
	}
	items = append(items, p.qualifiedObjectItems(inv.catalog.Objects, pl)...)
	items = append(items, p.qualifiedObjectItems(inv.catalog.Functions, pl)...)
	items = append(items, p.databaseItems(pl)...)
	items = append(items, p.linkedServerItems(pl)...)
	sortCompletionItems(items)
	return items
}

// schemaItems offers every schema in schemas whose name contains pl, a
// lower-cased prefix.
func (p *QueryPanel) schemaItems(schemas []string, pl string) []editor.CompletionItem {
	var items []editor.CompletionItem
	for _, schema := range schemas {
		if ok, partial := nameMatch(schema, pl); ok {
			items = append(items, editor.CompletionItem{
				Text: gosmo.QuoteNameIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
				Partial: partial,
			})
		}
	}
	return items
}

// qualifiedObjectItems offers every object in objs whose name contains pl, a
// lower-cased prefix, each committing schema-qualified: the popup shows
// "dbo.Doctors" and commit inserts what it shows (issue #65), as DDL targets
// already do.
func (p *QueryPanel) qualifiedObjectItems(objs []gosmo.CatalogObject, pl string) []editor.CompletionItem {
	var items []editor.CompletionItem
	for i := range objs {
		obj := &objs[i]
		if ok, partial := nameMatch(obj.Name, pl); ok {
			item := p.objectItem(obj, partial)
			item.Text = gosmo.QuoteNameIfNeeded(obj.Schema) + "." + item.Text
			items = append(items, item)
		}
	}
	return items
}

// objectItems offers every table/view/TVF in objs whose name contains
// prefix — a schema's member list ("dbo.").
func (p *QueryPanel) objectItems(objs []*gosmo.CatalogObject, prefix string) []editor.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []editor.CompletionItem
	for _, obj := range objs {
		if ok, partial := nameMatch(obj.Name, pl); ok {
			items = append(items, p.objectItem(obj, partial))
		}
	}
	sortCompletionItems(items)
	return items
}

func (p *QueryPanel) objectItem(obj *gosmo.CatalogObject, partial bool) editor.CompletionItem {
	detail := "table"
	switch obj.Type {
	case gosmo.CatalogView:
		detail = "view"
	case gosmo.CatalogFunction:
		detail = "table-valued function"
	}
	return editor.CompletionItem{
		Text: gosmo.QuoteNameIfNeeded(obj.Name), Label: obj.Schema + "." + obj.Name,
		Detail: detail, Icon: p.tableIcon(obj.Type), Partial: partial,
	}
}

// columnItemsFor offers every column in cols whose name contains prefix —
// the "alias." / "table." member-lookup result.
func (p *QueryPanel) columnItemsFor(cols []gosmo.CatalogColumn, prefix string) []editor.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []editor.CompletionItem
	for _, col := range cols {
		ok, partial := nameMatch(col.Name, pl)
		if !ok {
			continue
		}
		items = append(items, editor.CompletionItem{
			Text: gosmo.QuoteNameIfNeeded(col.Name), Label: col.Name,
			Detail: formatColumnType(col), Icon: p.columnIcon(), Partial: partial,
		})
	}
	sortCompletionItemsInContext(items, false, false)
	return items
}

// scopedColumnCandidates offers the union of every in-scope relation's
// columns (deduplicated by name — a column present on more than one joined
// table shows once) plus each relation's own alias/table/CTE name, so typing
// "c." after "c" was just offered still works — the unqualified SELECT/
// WHERE/ON/GROUP BY/ORDER BY/HAVING/SET context. Whether two columns share a
// name is the panel's database collation's call: under a case-sensitive one
// Id and ID are both offered.
func (p *QueryPanel) scopedColumnCandidates(rels []relation, collation, prefix string) []editor.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []editor.CompletionItem
	seenCol := newNameSet(collation)
	seenRef := make(map[string]bool)
	for _, rel := range rels {
		for _, col := range rel.columns() {
			ok, partial := nameMatch(col.Name, pl)
			if seenCol.Has(col.Name) || !ok {
				continue
			}
			seenCol.Add(col.Name)
			detail := formatColumnType(col)
			if rel.name != "" {
				detail += " — " + rel.name
			}
			items = append(items, editor.CompletionItem{
				Text: gosmo.QuoteNameIfNeeded(col.Name), Label: col.Name,
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
		items = append(items, editor.CompletionItem{
			Text: gosmo.QuoteNameIfNeeded(rel.name), Label: rel.name, Detail: "table reference", Icon: p.tableIcon(objType),
			Partial: partial,
		})
	}
	sortCompletionItemsInContext(items, false, false)
	return items
}

// nameMatch reports whether name contains the lower-cased fragment pl
// anywhere — "ord" offers OrderLines and CustomerOrders alike — and whether
// that match is partial, i.e. not at the start. The editor treats a list of
// nothing but partial matches differently (see editor.CompletionItem.Partial).
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
	for k := range len(name) {
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

func sortCompletionItems(items []editor.CompletionItem) {
	sortCompletionItemsInContext(items, true, false)
}

// Prefix matches stay ahead of partial matches so automatic selection never
// commits a substring match when a prefix match is available.
func sortCompletionItemsInContext(items []editor.CompletionItem, source, aggregatesFirst bool) {
	group := func(item editor.CompletionItem) int {
		if aggregatesFirst && item.Detail == "function" &&
			completionBuiltins[strings.ToUpper(item.Label)].kind&(builtinAggregate|builtinFabricAggregate) != 0 {
			return -1
		}
		return completionGroup(item, source)
	}
	slices.SortStableFunc(items, func(a, b editor.CompletionItem) int {
		if a.Partial != b.Partial {
			if a.Partial {
				return 1
			}
			return -1
		}
		if ag, bg := group(a), group(b); ag != bg {
			return ag - bg
		}
		return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
	})
}

func completionGroup(item editor.CompletionItem, source bool) int {
	if item.Placeholder {
		return 8
	}
	if source {
		switch item.Detail {
		case "schema":
			return 0
		case "table", "view", "CTE", "temp table", "table variable":
			return 1
		case "table-valued function":
			return 2
		case "database":
			return 3
		case "linked server":
			return 4
		}
		return 5
	}
	if item.Label == "*" && item.Detail == "keyword" {
		return 2
	}
	switch item.Detail {
	case "table reference":
		return 0
	case "function", "table-valued function", "expression", "predicate":
		return 3
	case "schema", "table", "view", "CTE", "temp table", "table variable":
		return 4
	case "database", "linked server":
		return 5
	case "keyword":
		return 7
	}
	return 1
}

// ---------------------------------------------------------------------------
// Icons and formatting
// ---------------------------------------------------------------------------

func (p *QueryPanel) tableIcon(t gosmo.CatalogObjectType) rune {
	nt := NodeTable
	switch t {
	case gosmo.CatalogView:
		nt = NodeView
	case gosmo.CatalogFunction:
		nt = NodeFunction
	}
	return nodeIcon(nodeData{Type: nt}, p.app.cfg.IconStyle, false)
}

func (p *QueryPanel) columnIcon() rune {
	return nodeIcon(nodeData{Type: NodeColumn}, p.app.cfg.IconStyle, false)
}

func (p *QueryPanel) schemaIcon() rune {
	return nodeIcon(nodeData{Type: NodeSchema}, p.app.cfg.IconStyle, false)
}

// formatColumnType renders a CatalogColumn's type plus, when it's not nullable, a
// ", not null" suffix — used by IntelliSense's completion detail text, where a
// nullable column's detail stays bare to save popup width.
//
// A column with no DataType is a synthetic one whose type could not be worked out
// (a derived table's or CTE's expression column). It renders as the bare word
// "column": nullability is unknown too, and a zero CatalogColumn has IsNullable
// false, so the suffix would otherwise assert NOT NULL about the columns this code
// knows least about.
func formatColumnType(col gosmo.CatalogColumn) string {
	if col.DataType == "" {
		return "column"
	}
	t := gosmo.TypeString(col.DataType, col.MaxLength, col.Precision, col.Scale)
	if !col.IsNullable {
		t += ", not null"
	}
	return t
}
