package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_group_schema.go is Object Explorer Details under Group by
// Schema (explorer_group_schema.go): a grouped folder lists its schemas, and a
// NodeSchemaGroup lists its objects with the columns of the folder it groups.
//
// Both run the folder's own Details query and narrow or count its rows, never
// the tree's loaded children: the pane answers for a folder the tree has not
// expanded, and a reload of the folder (which Invalidates it) is reflected
// without the pane waiting on the tree. The toggle invalidates every grouped
// folder and group (App.toggleGroupBySchema), since a cached entry is keyed by
// node and was built in the other mode.

// groupedFolderColumns are a grouped folder's Details columns: one row per
// schema holding at least one of its objects, counted.
var groupedFolderColumns = []string{"Name", "Objects"}

// schemaGroupFolder is the folder a NodeSchemaGroup stands in for, as a
// snapshot the fetch goroutine can read: the folder's type and filter, the
// group's database and connection, and no Schema, so the folder's loader lists
// exactly what it does for the folder itself. The filter is the folder's, as
// the group has none of its own: the filter stays on the folder. UI goroutine
// only.
func schemaGroupFolder(group *explorerNode) *explorerNode {
	snap := group.snapshot()
	snap.data.Type = group.data.GroupOf
	snap.data.Schema = ""
	snap.data.Filter = nil
	if group.parent != nil {
		snap.label = group.parent.label
		snap.data.Filter = group.parent.data.Filter
	}
	return snap
}

// loadSchemaGroupDetails shows a NodeSchemaGroup as its folder would, narrowed
// to the group's schema. Tables keep their progressive loader; the other
// families go through fetchNodeDetails and drop the other schemas' rows.
func (db *DetailBrowser) loadSchemaGroupDetails(fetchCtx context.Context, app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	folder, schema := schemaGroupFolder(node), node.data.Schema
	if folder.data.Type == NodeTables {
		db.loadTablesFolderDetails(fetchCtx, app, sc, node, folder.data, schema, seq)
		return
	}
	app.safegoRepair("loading Object Explorer details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(fetchCtx, childFetchTimeout)
		defer cancel()
		var objs []nodeData
		cols, rows, err := fetchNodeDetails(ctx, sc, folder, &objs)
		rows, objs = rowsInSchema(rows, objs, schema)
		db.postFinalResult(app, node, seq, &detailResult{cols: cols, rows: rows, objs: objs, err: err})
	})
}

// rowsInSchema keeps the rows whose object is in schema, with their objects.
// The objects are what say which schema a row is in: a Name cell is only a
// rendering ("dbo.t", but a Functions row may be decorated). Rows without a
// parallel object list cannot be narrowed and are dropped rather than shown
// under every schema; every groupedFolderTypes loader returns one
// (viewsFolderDetail, storedProceduresFolderDetail, fetchChildObjectsDetail).
func rowsInSchema(rows [][]string, objs []nodeData, schema string) ([][]string, []nodeData) {
	if len(objs) != len(rows) {
		return nil, nil
	}
	var keptRows [][]string
	var keptObjs []nodeData
	for i, o := range objs {
		if o.Schema == schema {
			keptRows = append(keptRows, rows[i])
			keptObjs = append(keptObjs, o)
		}
	}
	return keptRows, keptObjs
}

// loadGroupedFolderDetails shows a grouped folder as its schemas, each with its
// object count, in the tree's order. The rows are not objects: a schema row
// offers no Delete (Security > Schemas owns the schema itself).
func (db *DetailBrowser) loadGroupedFolderDetails(fetchCtx context.Context, app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	snap := node.snapshot()
	app.safegoRepair("loading Object Explorer details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(fetchCtx, childFetchTimeout)
		defer cancel()
		objs, err := groupedFolderObjects(ctx, sc, snap)
		var rows [][]string
		if err == nil {
			rows = schemaCountRows(objs)
		}
		db.postFinalResult(app, node, seq, &detailResult{cols: groupedFolderColumns, rows: rows, err: err})
	})
}

// groupedFolderObjects lists folder's objects as its ungrouped Details would.
// Tables through folderTables, not loadTablesFolderDetails: the counts need
// none of its row-count and space aggregates.
func groupedFolderObjects(ctx context.Context, sc *dbconn.ServerConn, folder *explorerNode) ([]nodeData, error) {
	if folder.data.Type == NodeTables {
		_, tables, err := folderTables(ctx, sc, folder.data)
		if err != nil {
			return nil, err
		}
		objs := make([]nodeData, len(tables))
		for i, t := range tables {
			objs[i] = nodeData{Type: NodeTable, DBName: folder.data.DBName, Schema: t.Schema, Name: t.Name}
		}
		return objs, nil
	}
	var objs []nodeData
	_, _, err := fetchNodeDetails(ctx, sc, folder, &objs)
	return objs, err
}

// schemaCountRows is one {schema, count} row per schema among objs, ordered as
// applyGrouping orders the tree's groups: case-insensitively, first seen
// breaking ties. Objects without a schema (a Functions folder's System
// Functions sub-folder) are not counted, as the tree leaves them in the folder.
func schemaCountRows(objs []nodeData) [][]string {
	var schemas []string
	counts := map[string]int{}
	for _, o := range objs {
		if o.Schema == "" {
			continue
		}
		if counts[o.Schema] == 0 {
			schemas = append(schemas, o.Schema)
		}
		counts[o.Schema]++
	}
	slices.SortStableFunc(schemas, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	rows := make([][]string, len(schemas))
	for i, s := range schemas {
		rows[i] = []string{s, strconv.Itoa(counts[s])}
	}
	return rows
}
