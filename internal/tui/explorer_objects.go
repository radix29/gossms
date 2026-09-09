package tui

import (
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// loadTablesChildren returns the Tables folder: SSMS's four table-family
// sub-folders first, then the plain user tables — the same shape the System
// Databases folder gives the Databases node.
//
// The user list is TableKindUser, which excludes the three families that have
// a folder of their own. Without that a FileTable is listed twice, once here
// and once under FileTables, and the two entries are the same object.
//
// Which folders appear follows SSMS: System Tables and FileTables always,
// External Tables only where the database actually has one (the folder is
// PolyBase-specific and empty everywhere else), Graph Tables only on an
// instance whose sys.tables has is_node/is_edge at all — 2017 and later. An
// absent folder and an empty one are different answers, and only the absent
// one is honest about a server that cannot have the objects.
func loadTablesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	folders, err := tableSubFolders(l, node, dbObj)
	if err != nil {
		return nil, err
	}
	tables, err := loadTablesOfKind(l, node, dbObj, gosmo.TableKindUser, false)
	if err != nil {
		return nil, err
	}
	return append(folders, tables...), nil
}

// tableSubFolders builds the folders listed above the user tables. The
// presence read is one aggregate over sys.tables, not a listing per folder —
// the folder's own loader is what lists it, once it is expanded.
func tableSubFolders(l loaderCtx, node *explorerNode, dbObj *gosmo.Database) ([]*explorerNode, error) {
	dbName := node.data.DBName
	folders := []*explorerNode{
		l.node("System Tables", NodeSystemTables, "", "", dbName),
		l.node("FileTables", NodeFileTables, "", "", dbName),
	}
	present, err := dbObj.TableKindsPresentContext(l.ctx)
	if err != nil {
		return nil, err
	}
	if present.External {
		folders = append(folders, l.node("External Tables", NodeExternalTables, "", "", dbName))
	}
	// The same call External Libraries gets (see loadExternalResourcesChildren):
	// gosmo refuses the graph listing below 2017, so a folder there could only
	// ever show that refusal. An unread major (0, which includes Azure) is
	// treated as newest.
	if major := serverMajor(l.sc); major == 0 || major >= int(gosmo.SQLServer2017) {
		folders = append(folders, l.node("Graph Tables", NodeGraphTables, "", "", dbName))
	}
	return folders, nil
}

// loadTablesOfKind lists one table family as NodeTable leaves. Every leaf is
// a NodeTable whatever folder it came from: a FileTable, an external table
// and a graph table are all tables, with the same Columns/Keys/Indexes
// children, the same Properties dialog and the same scripts — the folder is
// where they differ, not the object. system marks the System Tables folder's
// leaves, which is what keeps Delete and Rename off their menu.
func loadTablesOfKind(l loaderCtx, node *explorerNode, dbObj *gosmo.Database,
	kind gosmo.TableKind, system bool,
) ([]*explorerNode, error) {
	// The folder's filter goes to the server where it can be expressed; what
	// comes back is filtered again by fetchChildren, which stays the authority
	// on what the filter means (see nodeFilter.pushdown). Each sub-folder has
	// its own filter, the way System Views does — not the parent's.
	filter := serverFilter(node.data.Filter)
	return listChildren(
		func() ([]*gosmo.Table, error) { return dbObj.TablesOfKindFilteredContext(l.ctx, kind, filter) },
		func(t *gosmo.Table) *explorerNode {
			n := l.node(tableLabel(t), NodeTable, t.Schema, t.Name, node.data.DBName)
			n.data.CreateDate = t.CreateDate
			n.data.IsMemoryOptimized = t.IsMemoryOptimized
			n.data.IsSystem = system
			return n
		})
}

// tableLabel names a table in the tree. A graph table says which half of the
// graph it is: node and edge tables sit in one folder, nothing else in the
// row distinguishes them, and the two are not interchangeable in a MATCH.
func tableLabel(t *gosmo.Table) string {
	label := t.Schema + "." + t.Name
	switch {
	case t.IsNode:
		label += " (node)"
	case t.IsEdge:
		label += " (edge)"
	}
	return label
}

// tablesOfKindLoader is the childLoader for one of the four sub-folders.
func tablesOfKindLoader(kind gosmo.TableKind, system bool) childLoader {
	return func(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
		dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
		if err != nil {
			return nil, err
		}
		return loadTablesOfKind(l, node, dbObj, kind, system)
	}
}

// loadTableChildren returns one table's object-family folders, matching
// SSMS: Columns, Keys, Constraints, Triggers, Indexes, Statistics. Each
// folder node carries the owning table's Schema/Name so its own loader
// knows which table to query.
func loadTableChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	schema, name, dbName := node.data.Schema, node.data.Name, node.data.DBName
	return []*explorerNode{
		l.node("Columns", NodeColumns, schema, name, dbName),
		l.node("Keys", NodeKeys, schema, name, dbName),
		l.node("Constraints", NodeChecks, schema, name, dbName),
		l.node("Triggers", NodeTriggers, schema, name, dbName),
		l.node("Indexes", NodeIndexes, schema, name, dbName),
		l.node("Statistics", NodeStatistics, schema, name, dbName),
	}, nil
}

// loadViewChildren returns one view's object-family folders. A view has
// exactly one: its INSTEAD OF triggers, which SSMS also files under the view
// rather than under the database. They were previously reachable only through
// the database-wide Triggers roll-up, and that folder is gone.
func loadViewChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Triggers", NodeTriggers, node.data.Schema, node.data.Name, node.data.DBName),
	}, nil
}

// tableFor resolves node's owning table — node.data.Schema/Name are the
// table's own, propagated onto it by loadTableChildren above.
func tableFor(l loaderCtx, node *explorerNode) (*gosmo.Table, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return dbObj.TableByNameContext(l.ctx, node.data.Schema, node.data.Name)
}

// loadColumnsChildren returns one table's columns. Columns that are part of
// the primary key get NodeColumn's icon overridden by nodeIcon (via
// nodeData.IsPrimaryKey) with the primary-key glyph.
func loadColumnsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	table, err := tableFor(l, node)
	if err != nil {
		return nil, err
	}
	cols, err := table.ColumnsContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(cols))
	for _, c := range cols {
		nullWord := "null"
		if !c.IsNullable {
			nullWord = "not null"
		}
		typ := formatDataTypeLen(string(c.DataType), c.MaxLength, c.Precision, c.Scale)
		label := fmt.Sprintf("%s (%s, %s)", c.Name, typ, nullWord)
		n := l.node(label, NodeColumn, node.data.Schema, c.Name, node.data.DBName)
		// The owning table, the way the Keys and Indexes loaders carry it:
		// Name here is the column's own, so without this nothing downstream
		// can say which table an ALTER TABLE ... DROP COLUMN belongs to.
		n.data.TableName = node.data.Name
		n.data.IsPrimaryKey = c.IsPrimaryKey
		out = append(out, n)
	}
	return out, nil
}

// loadKeysChildren returns one table's Keys folder: its primary key and
// unique-constraint indexes (NodeKey), plus its foreign keys (NodeForeignKey)
// — a flat list, matching SSMS's Keys folder rather than nested subfolders.
func loadKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	table, err := tableFor(l, node)
	if err != nil {
		return nil, err
	}
	indexes, err := table.IndexesContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(indexes))
	for _, idx := range indexes {
		if idx.IsPrimaryKey || idx.IsUniqueConstraint {
			n := l.node(idx.Name, NodeKey, node.data.Schema, idx.Name, node.data.DBName)
			n.data.TableName = node.data.Name
			n.data.IsPrimaryKey = idx.IsPrimaryKey
			out = append(out, n)
		}
	}
	fks, err := table.ForeignKeysContext(l.ctx)
	if err != nil {
		return nil, err
	}
	for _, fk := range fks {
		n := l.node(fk.Name, NodeForeignKey, node.data.Schema, fk.Name, node.data.DBName)
		n.data.TableName = node.data.Name
		out = append(out, n)
	}
	return out, nil
}

// loadConstraintsChildren returns one table's Constraints folder: its CHECK
// constraints.
func loadConstraintsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	table, err := tableFor(l, node)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.CheckConstraint, error) { return table.CheckConstraintsContext(l.ctx) },
		func(cc *gosmo.CheckConstraint) *explorerNode {
			n := l.node(cc.Name, NodeCheck, node.data.Schema, cc.Name, node.data.DBName)
			n.data.TableName = node.data.Name
			return n
		})
}

// loadIndexesChildren returns one table's Indexes folder.
func loadIndexesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	table, err := tableFor(l, node)
	if err != nil {
		return nil, err
	}
	indexes, err := table.IndexesContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(indexes))
	for _, idx := range indexes {
		// The index's own type, not just clustered-vs-not: a columnstore,
		// XML or spatial index read as "Nonclustered" under the old
		// IsClustered test, and a clustered columnstore index — which gosmo
		// does report as clustered — read as "Nonclustered" too.
		kind := indexTypeName(idx.Type)
		unique := ""
		if idx.IsUnique {
			unique = ", Unique"
		}
		label := fmt.Sprintf("%s (%s%s)", idx.Name, kind, unique)
		n := l.node(label, NodeIndex, node.data.Schema, idx.Name, node.data.DBName)
		n.data.TableName = node.data.Name
		out = append(out, n)
	}
	return out, nil
}

// loadStatisticsChildren returns one table's Statistics folder.
func loadStatisticsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	table, err := tableFor(l, node)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Statistic, error) { return table.StatisticsContext(l.ctx) },
		func(st *gosmo.Statistic) *explorerNode {
			n := l.node(st.Name, NodeStatistic, node.data.Schema, st.Name, node.data.DBName)
			n.data.TableName = node.data.Name
			return n
		})
}

// Views, stored procedures and functions are the same folder three times
// over: each has a user list carrying a "System …" folder in front of it and
// a system list behind that folder, and each builds "schema.name" nodes with
// a CreateDate. The six loaders below are the registry entries
// explorer_loaders.go binds to a NodeType; loadSchemaScoped is all the
// behaviour.

// schemaScoped names the three fields a gosmo object needs for a tree node.
// gosmo.View, gosmo.StoredProcedure and gosmo.UserDefinedFunction each have
// them as plain struct fields, which a type parameter can't reach, so each
// kind supplies a two-line accessor instead.
type schemaScoped[T any] func(T) (schema, name string, created time.Time)

func viewFields(v *gosmo.View) (string, string, time.Time) { return v.Schema, v.Name, v.CreateDate }

func procFields(p *gosmo.StoredProcedure) (string, string, time.Time) {
	return p.Schema, p.Name, p.CreateDate
}

// loadSchemaScoped lists one kind of schema-scoped object from node's
// database as "schema.name" nodes of type nt, marking them system or not.
func loadSchemaScoped[T any](l loaderCtx, node *explorerNode, nt NodeType, system bool,
	fetch func(*gosmo.Database) ([]T, error), fields schemaScoped[T],
) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]T, error) { return fetch(dbObj) },
		func(it T) *explorerNode {
			schema, name, created := fields(it)
			n := l.node(schema+"."+name, nt, schema, name, node.data.DBName)
			n.data.CreateDate = created
			n.data.IsSystem = system
			return n
		})
}

// withSystemFolder puts the "System …" folder ahead of the user objects,
// matching the "System Databases" precedent in loadDatabasesChildren. The
// folder's own contents — identical in every database on the server — are
// only fetched once it is actually expanded, by its own loader below.
func withSystemFolder(l loaderCtx, node *explorerNode, label string, nt NodeType, objs []*explorerNode) []*explorerNode {
	folder := l.node(label, nt, "", "", node.data.DBName)
	return append([]*explorerNode{folder}, objs...)
}

// loadViewsChildren returns a database's user views, plus a "System Views"
// folder listed first.
func loadViewsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	views, err := loadSchemaScoped(l, node, NodeView, false,
		func(d *gosmo.Database) ([]*gosmo.View, error) {
			return d.ViewsFilteredContext(l.ctx, serverFilter(node.data.Filter))
		}, viewFields)
	if err != nil {
		return nil, err
	}
	return withSystemFolder(l, node, "System Views", NodeSystemViews, views), nil
}

// loadSystemViewsChildren returns the "sys" schema's own catalog views.
func loadSystemViewsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return loadSchemaScoped(l, node, NodeView, true,
		func(d *gosmo.Database) ([]*gosmo.View, error) {
			return d.SystemViewsFilteredContext(l.ctx, serverFilter(node.data.Filter))
		}, viewFields)
}

// loadStoredProceduresChildren returns a database's user stored procedures,
// plus a "System Procedures" folder listed first.
func loadStoredProceduresChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	procs, err := loadSchemaScoped(l, node, NodeStoredProcedure, false,
		func(d *gosmo.Database) ([]*gosmo.StoredProcedure, error) {
			return d.StoredProceduresFilteredContext(l.ctx, serverFilter(node.data.Filter))
		}, procFields)
	if err != nil {
		return nil, err
	}
	return withSystemFolder(l, node, "System Procedures", NodeSystemProcedures, procs), nil
}

// loadSystemProceduresChildren returns the "sys" schema's own stored
// procedures.
func loadSystemProceduresChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return loadSchemaScoped(l, node, NodeStoredProcedure, true,
		func(d *gosmo.Database) ([]*gosmo.StoredProcedure, error) {
			return d.SystemStoredProceduresFilteredContext(l.ctx, serverFilter(node.data.Filter))
		}, procFields)
}

// loadFunctionsChildren returns a database's user functions, plus a
// "System Functions" folder listed first.
func loadFunctionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	funcs, err := loadFunctionNodes(l, node, false,
		func(d *gosmo.Database) ([]*gosmo.UserDefinedFunction, error) {
			return d.UserDefinedFunctionsFilteredContext(l.ctx, serverFilter(node.data.Filter))
		})
	if err != nil {
		return nil, err
	}
	return withSystemFolder(l, node, "System Functions", NodeSystemFunctions, funcs), nil
}

// loadSystemFunctionsChildren returns the "sys" schema's own functions.
func loadSystemFunctionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return loadFunctionNodes(l, node, true,
		func(d *gosmo.Database) ([]*gosmo.UserDefinedFunction, error) {
			return d.SystemFunctionsFilteredContext(l.ctx, serverFilter(node.data.Filter))
		})
}

// loadFunctionNodes is loadSchemaScoped for functions, which need one field
// beyond the three that generic reads: FuncType decides which call template
// "Script Function as SELECT" produces.
func loadFunctionNodes(l loaderCtx, node *explorerNode, system bool,
	fetch func(*gosmo.Database) ([]*gosmo.UserDefinedFunction, error),
) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.UserDefinedFunction, error) { return fetch(dbObj) },
		func(f *gosmo.UserDefinedFunction) *explorerNode {
			n := l.node(f.Schema+"."+f.Name, NodeFunction, f.Schema, f.Name, node.data.DBName)
			n.data.CreateDate = f.CreateDate
			n.data.IsSystem = system
			n.data.FuncType = f.FuncType
			return n
		})
}

// loadTriggersChildren backs the NodeTriggers folder, which hangs under a
// table or a view — node.data.Schema/Name are the owning object's, put there
// by loadTableChildren or loadViewChildren.
//
// It reads by name through gosmo's Database.ObjectTriggers rather than
// resolving the parent to a *Table first, because the parent is not always a
// table: a view's INSTEAD OF triggers list here too, and gosmo's View is a
// plain row struct with no Triggers method to call. OBJECT_ID does not care
// which of the two it is, and it is one round trip instead of two.
//
// There is no database-wide arm any more. The flat Triggers folder that used
// to sit under a database listed every DML trigger a second time and would
// have read as the sibling of Programmability > Database Triggers, which is a
// different family entirely — see loadProgrammabilityChildren.
func loadTriggersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.Trigger, error) {
			return dbObj.ObjectTriggersContext(l.ctx, node.data.Schema, node.data.Name)
		},
		func(t *gosmo.Trigger) *explorerNode {
			return l.node(t.Name, NodeTrigger, node.data.Schema, t.Name, node.data.DBName)
		})
}

func loadSequencesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Sequence, error) { return dbObj.SequencesContext(l.ctx) },
		func(seq *gosmo.Sequence) *explorerNode {
			return l.node(seq.Schema+"."+seq.Name, NodeSequence, seq.Schema, seq.Name, node.data.DBName)
		})
}

func loadSynonymsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Synonym, error) { return dbObj.SynonymsContext(l.ctx) },
		func(syn *gosmo.Synonym) *explorerNode {
			return l.node(syn.Schema+"."+syn.Name, NodeSynonym, syn.Schema, syn.Name, node.data.DBName)
		})
}
