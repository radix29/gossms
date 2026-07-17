package tui

import (
	"fmt"

	gosmo "github.com/radix29/gosmo"
)

func loadTablesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Table, error) { return dbObj.TablesContext(l.ctx) },
		func(t *gosmo.Table) *explorerNode {
			return l.node(t.Schema+"."+t.Name, NodeTable, t.Schema, t.Name, node.data.DBName)
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
// nodeData.IsPrimaryKey) with the primary-key glyph from todo/icons.md.
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
		nullable := ""
		if c.IsNullable {
			nullable = " NULL"
		}
		label := fmt.Sprintf("%s (%s%s)", c.Name, c.DataType, nullable)
		n := l.node(label, NodeColumn, node.data.Schema, c.Name, node.data.DBName)
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
			out = append(out, l.node(idx.Name, NodeKey, node.data.Schema, idx.Name, node.data.DBName))
		}
	}
	fks, err := table.ForeignKeysContext(l.ctx)
	if err != nil {
		return nil, err
	}
	for _, fk := range fks {
		out = append(out, l.node(fk.Name, NodeForeignKey, node.data.Schema, fk.Name, node.data.DBName))
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
			return l.node(cc.Name, NodeCheck, node.data.Schema, cc.Name, node.data.DBName)
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
		kind := "Nonclustered"
		if idx.IsClustered {
			kind = "Clustered"
		}
		unique := ""
		if idx.IsUnique {
			unique = ", Unique"
		}
		label := fmt.Sprintf("%s (%s%s)", idx.Name, kind, unique)
		out = append(out, l.node(label, NodeIndex, node.data.Schema, idx.Name, node.data.DBName))
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
			return l.node(st.Name, NodeStatistic, node.data.Schema, st.Name, node.data.DBName)
		})
}

// loadViewsChildren returns a database's user views, plus a "System Views"
// folder listed first — matching the "System Databases" precedent in
// loadDatabasesChildren. The System Views folder's own contents (the
// sys-schema catalog views, identical in every database on the server) are
// only fetched once it's actually expanded — see loadSystemViewsChildren.
func loadViewsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	views, err := listChildren(func() ([]*gosmo.View, error) { return dbObj.ViewsContext(l.ctx) },
		func(v *gosmo.View) *explorerNode {
			return l.node(v.Schema+"."+v.Name, NodeView, v.Schema, v.Name, node.data.DBName)
		})
	if err != nil {
		return nil, err
	}
	sysFolder := l.node("System Views", NodeSystemViews, "", "", node.data.DBName)
	return append([]*explorerNode{sysFolder}, views...), nil
}

// loadSystemViewsChildren returns the "sys" schema's own catalog views —
// see loadViewsChildren.
func loadSystemViewsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.View, error) { return dbObj.SystemViewsContext(l.ctx) },
		func(v *gosmo.View) *explorerNode {
			return l.node(v.Schema+"."+v.Name, NodeView, v.Schema, v.Name, node.data.DBName)
		})
}

// loadStoredProceduresChildren returns a database's user stored procedures,
// plus a "System Procedures" folder listed first — see loadViewsChildren.
func loadStoredProceduresChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	procs, err := listChildren(func() ([]*gosmo.StoredProcedure, error) { return dbObj.StoredProceduresContext(l.ctx) },
		func(p *gosmo.StoredProcedure) *explorerNode {
			return l.node(p.Schema+"."+p.Name, NodeStoredProcedure, p.Schema, p.Name, node.data.DBName)
		})
	if err != nil {
		return nil, err
	}
	sysFolder := l.node("System Procedures", NodeSystemProcedures, "", "", node.data.DBName)
	return append([]*explorerNode{sysFolder}, procs...), nil
}

// loadSystemProceduresChildren returns the "sys" schema's own stored
// procedures — see loadViewsChildren.
func loadSystemProceduresChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.StoredProcedure, error) { return dbObj.SystemStoredProceduresContext(l.ctx) },
		func(p *gosmo.StoredProcedure) *explorerNode {
			return l.node(p.Schema+"."+p.Name, NodeStoredProcedure, p.Schema, p.Name, node.data.DBName)
		})
}

// loadFunctionsChildren returns a database's user functions, plus a
// "System Functions" folder listed first — see loadViewsChildren.
func loadFunctionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	funcs, err := listChildren(func() ([]*gosmo.UserDefinedFunction, error) { return dbObj.UserDefinedFunctionsContext(l.ctx) },
		func(f *gosmo.UserDefinedFunction) *explorerNode {
			return l.node(f.Schema+"."+f.Name, NodeFunction, f.Schema, f.Name, node.data.DBName)
		})
	if err != nil {
		return nil, err
	}
	sysFolder := l.node("System Functions", NodeSystemFunctions, "", "", node.data.DBName)
	return append([]*explorerNode{sysFolder}, funcs...), nil
}

// loadSystemFunctionsChildren returns the "sys" schema's own functions —
// see loadViewsChildren.
func loadSystemFunctionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.UserDefinedFunction, error) { return dbObj.SystemFunctionsContext(l.ctx) },
		func(f *gosmo.UserDefinedFunction) *explorerNode {
			return l.node(f.Schema+"."+f.Name, NodeFunction, f.Schema, f.Name, node.data.DBName)
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
