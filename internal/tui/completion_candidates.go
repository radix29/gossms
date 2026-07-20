package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// Candidate resolution against a completionInventory
// ---------------------------------------------------------------------------

// resolveQualifierToObject resolves a dot-qualifier to the CatalogObject it
// names: first an alias or bare table name already in refs (the common
// case), falling back to a direct name match across the whole inventory for
// a table the FROM-scope parse missed or that isn't in scope yet. sysInv is
// consulted too, so an alias/bare-name over a "sys.xxx" reference (e.g.
// "FROM sys.objects o") resolves its columns the same way a user table
// would.
func resolveQualifierToObject(inv, sysInv *completionInventory, refs []fromRef, qualifier string) *gosmo.CatalogObject {
	ql := strings.ToLower(qualifier)
	for _, ref := range refs {
		if ref.alias != "" && strings.ToLower(ref.alias) == ql {
			return findCatalogObject(inv, sysInv, ref.schema, ref.name)
		}
	}
	for _, ref := range refs {
		if ref.alias == "" && strings.ToLower(ref.name) == ql {
			return findCatalogObject(inv, sysInv, ref.schema, ref.name)
		}
	}
	return findCatalogObjectByName(inv, sysInv, qualifier)
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
// a FROM-scope alias/table (-> that object's columns), then as a schema
// name in the connected database (-> every table/view in it), then as a
// schema name in the sys-schema inventory ("sys" being the only one that
// ever matters there). Nothing matching returns nil, closing the popup
// rather than showing something wrong.
func (p *QueryPanel) memberCandidates(inv, sysInv *completionInventory, refs []fromRef, qualifier, prefix string) []controls.CompletionItem {
	if obj := resolveQualifierToObject(inv, sysInv, refs, qualifier); obj != nil {
		return p.columnItemsFor(obj, prefix)
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
// "sys" once its inventory has loaded) and every table/view whose name
// starts with prefix — the FROM/JOIN/INTO/UPDATE/DELETE/TRUNCATE TABLE
// context, and the fallback when a column context has no FROM-scope yet.
// The sys-schema inventory's own objects are deliberately not mixed into
// the unqualified list below (unlike the connected database's own tables) —
// there are hundreds of them, so offering them only once a query actually
// qualifies with "sys." (see memberCandidates) keeps this list from
// drowning in system catalog views nobody typed a prefix for.
func (p *QueryPanel) tableCandidates(inv, sysInv *completionInventory, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, schema := range inv.catalog.Schemas {
		if !strings.HasPrefix(strings.ToLower(schema), pl) {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
		})
	}
	if sysInv != nil && sysInv.catalog != nil {
		for _, schema := range sysInv.catalog.Schemas {
			if !strings.HasPrefix(strings.ToLower(schema), pl) {
				continue
			}
			items = append(items, controls.CompletionItem{
				Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
			})
		}
	}
	for i := range inv.catalog.Objects {
		obj := &inv.catalog.Objects[i]
		if strings.HasPrefix(strings.ToLower(obj.Name), pl) {
			items = append(items, p.objectItem(obj))
		}
	}
	sortCompletionItems(items)
	return items
}

// objectItems offers every table/view in objs whose name starts with
// prefix — a schema's member list ("dbo.").
func (p *QueryPanel) objectItems(objs []*gosmo.CatalogObject, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, obj := range objs {
		if strings.HasPrefix(strings.ToLower(obj.Name), pl) {
			items = append(items, p.objectItem(obj))
		}
	}
	sortCompletionItems(items)
	return items
}

func (p *QueryPanel) objectItem(obj *gosmo.CatalogObject) controls.CompletionItem {
	detail := "table"
	if obj.Type == gosmo.CatalogView {
		detail = "view"
	}
	return controls.CompletionItem{
		Text: bracketIfNeeded(obj.Name), Label: obj.Schema + "." + obj.Name,
		Detail: detail, Icon: p.tableIcon(obj.Type),
	}
}

// columnItemsFor offers every column of obj whose name starts with prefix —
// the "alias." / "table." member-lookup result.
func (p *QueryPanel) columnItemsFor(obj *gosmo.CatalogObject, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, col := range obj.Columns {
		if !strings.HasPrefix(strings.ToLower(col.Name), pl) {
			continue
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(col.Name), Label: col.Name,
			Detail: formatColumnType(col), Icon: p.columnIcon(),
		})
	}
	sortCompletionItems(items)
	return items
}

// scopedColumnCandidates offers the union of every FROM-scope ref's
// columns (deduplicated by name — a column present on more than one joined
// table shows once) plus each ref's alias/table name itself, so typing
// "c." after "c" was just offered still works — the unqualified SELECT/
// WHERE/ON/GROUP BY/ORDER BY/HAVING/SET context.
func (p *QueryPanel) scopedColumnCandidates(inv, sysInv *completionInventory, refs []fromRef, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	seenCol := make(map[string]bool)
	seenRef := make(map[string]bool)
	for _, ref := range refs {
		obj := findCatalogObject(inv, sysInv, ref.schema, ref.name)
		if obj != nil {
			for _, col := range obj.Columns {
				key := strings.ToLower(col.Name)
				if seenCol[key] || !strings.HasPrefix(key, pl) {
					continue
				}
				seenCol[key] = true
				qname := ref.alias
				if qname == "" {
					qname = ref.name
				}
				items = append(items, controls.CompletionItem{
					Text: bracketIfNeeded(col.Name), Label: col.Name,
					Detail: formatColumnType(col) + " — " + qname, Icon: p.columnIcon(),
				})
			}
		}
		qname := ref.alias
		if qname == "" {
			qname = ref.name
		}
		qkey := strings.ToLower(qname)
		if qname == "" || seenRef[qkey] || !strings.HasPrefix(qkey, pl) {
			continue
		}
		seenRef[qkey] = true
		objType := gosmo.CatalogTable
		if obj != nil {
			objType = obj.Type
		}
		items = append(items, controls.CompletionItem{
			Text: bracketIfNeeded(qname), Label: qname, Detail: "table reference", Icon: p.tableIcon(objType),
		})
	}
	sortCompletionItems(items)
	return items
}

func sortCompletionItems(items []controls.CompletionItem) {
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
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

// formatColumnType renders a CatalogColumn's type the way SQL Server itself
// would print it: length for the (n)(var)char/binary family (nvarchar/
// nchar's MaxLength is stored in bytes, so it's halved back to characters),
// precision/scale for decimal/numeric, and scale alone for the fractional-
// seconds types — MAX for a -1 MaxLength either way.
func formatColumnType(col gosmo.CatalogColumn) string {
	t := strings.ToLower(string(col.DataType))
	switch t {
	case "varchar", "char", "varbinary", "binary":
		switch {
		case col.MaxLength == -1:
			t += "(MAX)"
		case col.MaxLength > 0:
			t += fmt.Sprintf("(%d)", col.MaxLength)
		}
	case "nvarchar", "nchar":
		switch {
		case col.MaxLength == -1:
			t += "(MAX)"
		case col.MaxLength > 0:
			t += fmt.Sprintf("(%d)", col.MaxLength/2)
		}
	case "decimal", "numeric":
		if col.Precision > 0 {
			t += fmt.Sprintf("(%d,%d)", col.Precision, col.Scale)
		}
	case "datetime2", "time", "datetimeoffset":
		if col.Scale > 0 {
			t += fmt.Sprintf("(%d)", col.Scale)
		}
	}
	if !col.IsNullable {
		t += ", not null"
	}
	return t
}

var regularIdentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// bracketIfNeeded returns name as-is when it's a plain identifier and not
// one of sqlKeywords, or "[name]" (with any ']' doubled) otherwise — so a
// committed candidate never silently changes what it names by needing
// quoting SQL Server would otherwise require.
func bracketIfNeeded(name string) string {
	if regularIdentPattern.MatchString(name) && !sqlKeywords[strings.ToUpper(name)] {
		return name
	}
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
