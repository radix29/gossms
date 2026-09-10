package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_programmability.go is the Detail Browser's view of the
// Programmability families Phase 3 added: the four Types folders and their
// members, Assemblies, Rules, Defaults and Plan Guides.
//
// Each leaf reuses the finder its Properties page uses — the rule
// detail_browser_storage.go's header states — so the pane and the dialog can
// never disagree about which object a node names. Each folder reads the same
// gosmo listing its explorer_programmability.go loader reads, and applies the
// folder's own filter through filterObjects, so the pane shows exactly the
// rows the tree shows.

// programmabilityFolderDetail lists one Programmability folder. The caller
// has already narrowed node.data.Type to one this switch handles.
func programmabilityFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	n := node.data
	d, err := sc.Server.DatabaseByNameContext(ctx, n.DBName)
	if err != nil {
		return nil, nil, err
	}

	switch n.Type {
	case NodeSystemDataTypes:
		types, err := d.SystemDataTypesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		types = filterObjects(n.Filter, types, func(t *gosmo.SystemDataType) nodeData {
			return nodeData{Name: t.Name}
		})
		rows := make([][]string, 0, len(types))
		for _, t := range types {
			rows = append(rows, []string{t.Name, typeLengthText(t.MaxLength),
				strconv.Itoa(t.Precision), strconv.Itoa(t.Scale), boolStr(t.IsNullable)})
			// A built-in type is not droppable, and objs is what offers the
			// pane's Delete — see detailResult.objs. IsSystem carries the
			// same withholding the tree's node does.
			*objs = append(*objs, nodeData{Type: NodeSystemDataType, DBName: n.DBName, Name: t.Name, IsSystem: true})
		}
		return []string{"Name", "Length", "Precision", "Scale", "Nullable"}, rows, nil

	case NodeUserDefinedDataTypes:
		types, err := d.UserDefinedDataTypesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		types = filterObjects(n.Filter, types, func(t *gosmo.UserDefinedDataType) nodeData {
			return nodeData{Name: t.Name, Schema: t.Schema}
		})
		rows := make([][]string, 0, len(types))
		for _, t := range types {
			rows = append(rows, []string{dottedName(t.Schema, t.Name),
				formatDataTypeLen(t.BaseType, t.MaxLength, t.Precision, t.Scale),
				boolStr(t.IsNullable), t.Rule, t.Default})
			*objs = append(*objs, nodeData{Type: NodeUserDefinedDataType, DBName: n.DBName, Schema: t.Schema, Name: t.Name})
		}
		return []string{"Name", "Base Type", "Nullable", "Rule", "Default"}, rows, nil

	case NodeUserDefinedTableTypes:
		types, err := d.UserDefinedTableTypesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		types = filterObjects(n.Filter, types, func(t *gosmo.UserDefinedTableType) nodeData {
			return nodeData{Name: t.Name, Schema: t.Schema}
		})
		rows := make([][]string, 0, len(types))
		for _, t := range types {
			rows = append(rows, []string{dottedName(t.Schema, t.Name), boolStr(t.IsMemoryOptimized)})
			*objs = append(*objs, nodeData{Type: NodeUserDefinedTableType, DBName: n.DBName, Schema: t.Schema, Name: t.Name})
		}
		return []string{"Name", "Memory Optimized"}, rows, nil

	case NodeUserDefinedTypes:
		types, err := d.ClrTypesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		types = filterObjects(n.Filter, types, func(t *gosmo.ClrType) nodeData {
			return nodeData{Name: t.Name, Schema: t.Schema}
		})
		rows := make([][]string, 0, len(types))
		for _, t := range types {
			rows = append(rows, []string{dottedName(t.Schema, t.Name), t.Assembly, t.AssemblyClass, boolStr(t.IsNullable)})
			*objs = append(*objs, nodeData{Type: NodeUserDefinedType, DBName: n.DBName, Schema: t.Schema, Name: t.Name})
		}
		return []string{"Name", "Assembly", "Class", "Nullable"}, rows, nil

	case NodeXmlSchemaCollections:
		cols, err := d.XmlSchemaCollectionsContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		cols = filterObjects(n.Filter, cols, func(c *gosmo.XmlSchemaCollection) nodeData {
			return nodeData{Name: c.Name, Schema: c.Schema, CreateDate: c.CreateDate}
		})
		rows := make([][]string, 0, len(cols))
		for _, c := range cols {
			rows = append(rows, []string{dottedName(c.Schema, c.Name), formatSQLDate(c.CreateDate), formatSQLDate(c.ModifyDate)})
			*objs = append(*objs, nodeData{Type: NodeXmlSchemaCollection, DBName: n.DBName, Schema: c.Schema, Name: c.Name})
		}
		return []string{"Name", "Created", "Modified"}, rows, nil

	case NodeAssemblies:
		asms, err := d.AssembliesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		asms = filterObjects(n.Filter, asms, func(a *gosmo.Assembly) nodeData {
			return nodeData{Name: a.Name, CreateDate: a.CreateDate}
		})
		rows := make([][]string, 0, len(asms))
		for _, a := range asms {
			rows = append(rows, []string{a.Name, string(a.PermissionSet), a.Owner,
				boolStr(a.IsVisible), formatSQLDate(a.CreateDate)})
			*objs = append(*objs, nodeData{Type: NodeAssembly, DBName: n.DBName, Name: a.Name,
				IsSystem: !a.IsUserDefined})
		}
		return []string{"Name", "Permission Set", "Owner", "Visible", "Created"}, rows, nil

	case NodeRules:
		rules, err := d.RulesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		rules = filterObjects(n.Filter, rules, func(r *gosmo.Rule) nodeData {
			return nodeData{Name: r.Name, Schema: r.Schema, CreateDate: r.CreateDate}
		})
		rows := make([][]string, 0, len(rules))
		for _, r := range rules {
			rows = append(rows, []string{dottedName(r.Schema, r.Name), formatSQLDate(r.CreateDate), r.Definition})
			*objs = append(*objs, nodeData{Type: NodeRule, DBName: n.DBName, Schema: r.Schema, Name: r.Name})
		}
		return []string{"Name", "Created", "Definition"}, rows, nil

	default: // NodeDefaults
		defs, err := d.DefaultsContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		defs = filterObjects(n.Filter, defs, func(df *gosmo.Default) nodeData {
			return nodeData{Name: df.Name, Schema: df.Schema, CreateDate: df.CreateDate}
		})
		rows := make([][]string, 0, len(defs))
		for _, df := range defs {
			rows = append(rows, []string{dottedName(df.Schema, df.Name), formatSQLDate(df.CreateDate), df.Definition})
			*objs = append(*objs, nodeData{Type: NodeDefault, DBName: n.DBName, Schema: df.Schema, Name: df.Name})
		}
		return []string{"Name", "Created", "Definition"}, rows, nil
	}
}

// planGuidesFolderDetail lists the Plan Guides folder. Separate from the
// switch above because it is the one family here whose state matters as much
// as its name: a disabled guide shapes no plan, and nothing else in the row
// says so.
func planGuidesFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	n := node.data
	d, err := sc.Server.DatabaseByNameContext(ctx, n.DBName)
	if err != nil {
		return nil, nil, err
	}
	guides, err := d.PlanGuidesContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	guides = filterObjects(n.Filter, guides, func(g *gosmo.PlanGuide) nodeData {
		return nodeData{Name: g.Name, CreateDate: g.CreateDate}
	})
	rows := make([][]string, 0, len(guides))
	for _, g := range guides {
		rows = append(rows, []string{g.Name, enabledText(!g.IsDisabled), string(g.Scope),
			g.ScopeObject, formatSQLDate(g.CreateDate)})
		*objs = append(*objs, nodeData{Type: NodePlanGuide, DBName: n.DBName, Name: g.Name,
			IsEnabled: !g.IsDisabled, ScopeSchema: g.ScopeSchema, ScopeName: g.ScopeName})
	}
	return []string{"Name", "Status", "Scope", "Scope Object", "Created"}, rows, nil
}

// programmabilityDetail is the Property/Value view of one leaf. Every arm
// reuses the finder in this package that the object's Properties page uses.
func programmabilityDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	n := node.data
	switch n.Type {
	case NodeSystemDataType:
		// The built-in types have no by-name finder — sys.types names them
		// unqualified and gosmo lists them without one — so this arm picks
		// its row out of the listing every other System Data Types read uses.
		t, err := findSystemDataType(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", t.Name,
			"Length", typeLengthText(t.MaxLength),
			"Precision", strconv.Itoa(t.Precision),
			"Scale", strconv.Itoa(t.Scale),
			"Nullable", boolStr(t.IsNullable),
		)

	case NodeUserDefinedDataType:
		t, err := findUserDefinedDataType(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", dottedName(t.Schema, t.Name),
			"Base type", formatDataTypeLen(t.BaseType, t.MaxLength, t.Precision, t.Scale),
			"Nullable", boolStr(t.IsNullable),
			"Collation", t.Collation,
			"Bound rule", t.Rule,
			"Bound default", t.Default,
		)

	case NodeUserDefinedTableType:
		t, err := findUserDefinedTableType(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", dottedName(t.Schema, t.Name)},
			{"Memory optimized", boolStr(t.IsMemoryOptimized)},
		}
		// The columns are the type — a table type with none is not a thing
		// the catalog can hold — so they are listed here rather than left to
		// the Properties dialog.
		cols, err := t.ColumnsContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		for i, c := range cols {
			rows = append(rows, []string{"Column " + strconv.Itoa(i+1), tableTypeColumnText(c)})
		}
		return propertyValueColumns, rows, nil

	case NodeUserDefinedType:
		t, err := findClrType(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", dottedName(t.Schema, t.Name),
			"Assembly", t.Assembly,
			"Assembly class", t.AssemblyClass,
			"Length", typeLengthText(t.MaxLength),
			"Nullable", boolStr(t.IsNullable),
		)

	case NodeXmlSchemaCollection:
		c, err := findXmlSchemaCollection(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", dottedName(c.Schema, c.Name),
			"Created", formatSQLDate(c.CreateDate),
			"Modified", formatSQLDate(c.ModifyDate),
		)

	case NodeAssembly:
		a, err := findAssembly(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", a.Name},
			{"Owner", a.Owner},
			{"Permission set", string(a.PermissionSet)},
			{"Visible", boolStr(a.IsVisible)},
			{"System", boolStr(!a.IsUserDefined)},
			{"Strong name", a.ClrName},
			{"Created", formatSQLDate(a.CreateDate)},
			{"Modified", formatSQLDate(a.ModifyDate)},
		}
		// Files, not FileContent: the payload is a binary the pane has
		// nothing to do with, and reading it costs the whole assembly over
		// the wire for a row that would show a hex preview of it.
		files, err := a.FilesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			rows = append(rows, []string{"File " + strconv.Itoa(f.FileID),
				f.Name + " (" + formatBytes(f.ContentLength) + ")"})
		}
		return propertyValueColumns, rows, nil

	case NodeRule:
		r, err := findRule(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", dottedName(r.Schema, r.Name),
			"Created", formatSQLDate(r.CreateDate),
			"Modified", formatSQLDate(r.ModifyDate),
			"Definition", r.Definition,
		)

	case NodeDefault:
		df, err := findDefault(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", dottedName(df.Schema, df.Name),
			"Created", formatSQLDate(df.CreateDate),
			"Modified", formatSQLDate(df.ModifyDate),
			"Definition", df.Definition,
		)

	default: // NodePlanGuide
		g, err := findPlanGuide(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", g.Name,
			"Status", enabledText(!g.IsDisabled),
			"Scope", string(g.Scope),
			"Scope object", g.ScopeObject,
			"Scope batch", g.ScopeBatch,
			"Parameters", g.Parameters,
			"Query text", g.QueryText,
			"Hints", g.Hints,
			"Created", formatSQLDate(g.CreateDate),
			"Modified", formatSQLDate(g.ModifyDate),
		)
	}
}

// dottedName is a schema-qualified name for display — the same "dbo.Phone"
// the tree's labels carry. fqn is the bracket-quoted form and belongs in
// generated T-SQL, not in a grid cell or a property row the user reads.
func dottedName(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// typeLengthText renders sys.types.max_length, whose -1 means MAX rather than
// a length of minus one byte.
func typeLengthText(maxLength int) string {
	if maxLength < 0 {
		return "max"
	}
	return strconv.Itoa(maxLength)
}

// tableTypeColumnText renders one table-type column the way a CREATE TYPE ...
// AS TABLE body declares it.
func tableTypeColumnText(c *gosmo.Column) string {
	s := c.Name + " " + formatDataTypeLen(string(c.DataType), c.MaxLength, c.Precision, c.Scale)
	if c.IsNullable {
		return s + ", null"
	}
	return s + ", not null"
}
