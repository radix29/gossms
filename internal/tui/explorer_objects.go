package tui

import (
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
)

func loadTablesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	// The folder's filter goes to the server where it can be expressed; what
	// comes back is filtered again by fetchChildren, which stays the authority
	// on what the filter means (see nodeFilter.pushdown).
	filter := serverFilter(node.data.Filter)
	return listChildren(func() ([]*gosmo.Table, error) { return dbObj.TablesFilteredContext(l.ctx, filter) },
		func(t *gosmo.Table) *explorerNode {
			n := l.node(t.Schema+"."+t.Name, NodeTable, t.Schema, t.Name, node.data.DBName)
			n.data.CreateDate = t.CreateDate
			n.data.IsMemoryOptimized = t.IsMemoryOptimized
			return n
		})
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

// loadTriggersChildren backs the NodeTriggers folder, which appears in two
// places: directly under a Database (all DML triggers, schema-qualified
// labels) and under a Table's own Triggers folder (that table's triggers
// only — node.data.Name is set to the table's name by loadTableChildren in
// that case, empty in the database-wide one).
func loadTriggersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	if node.data.Name != "" {
		table, err := dbObj.TableByNameContext(l.ctx, node.data.Schema, node.data.Name)
		if err != nil {
			return nil, err
		}
		return listChildren(func() ([]*gosmo.Trigger, error) { return table.TriggersContext(l.ctx) },
			func(t *gosmo.Trigger) *explorerNode {
				return l.node(t.Name, NodeTrigger, node.data.Schema, t.Name, node.data.DBName)
			})
	}
	return listChildren(func() ([]*gosmo.Trigger, error) { return dbObj.TriggersContext(l.ctx) },
		func(t *gosmo.Trigger) *explorerNode {
			return l.node(t.Schema+"."+t.Name, NodeTrigger, t.Schema, t.Name, node.data.DBName)
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
