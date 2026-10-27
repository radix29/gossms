package tui

import (
	"context"
	"slices"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// tablesFolderColumns are the Tables folder's detail-grid columns: the name
// first, then row count and size figures filled in once the two aggregate
// queries return (see loadTablesFolderDetails).
var tablesFolderColumns = []string{
	"Name", "Row Count", "Data (MB)", "Index (MB)", "Unused (MB)",
}

// tableKindForNode maps a table folder's node type to the gosmo listing it
// shows. The four sub-folders and the Tables folder itself share this pane;
// only the kind differs.
func tableKindForNode(t NodeType) gosmo.TableKind {
	switch t {
	case NodeSystemTables:
		return gosmo.TableKindSystem
	case NodeFileTables:
		return gosmo.TableKindFileTable
	case NodeExternalTables:
		return gosmo.TableKindExternal
	case NodeGraphTables:
		return gosmo.TableKindGraph
	default:
		return gosmo.TableKindUser
	}
}

// loadTablesFolderDetails shows the Tables folder's Name column as soon as the
// fast table-list query returns, then fills every row's row count and space
// columns from two whole-database aggregate queries.
//
// Two queries for the folder, not two per table. Fanning Table.RowCount and
// Table.SpaceUsed out per row costs 2N round trips, each on its own pooled
// connection, so a database with a few hundred tables visibly trickles in.
// gosmo's TableRowCounts and TableSpaceUsedAll answer the same question for every
// table at once. loadDatabasesFolderDetails still fans out — see the note there
// for why the same collapse isn't available for database sizes.
//
// data is the folder's: node's own, or for a NodeSchemaGroup the folder it
// groups (schemaGroupFolder), with schema keeping only that schema's tables; ""
// keeps every one.
func (db *DetailBrowser) loadTablesFolderDetails(fetchCtx context.Context, app *App, sc *dbconn.ServerConn, node *explorerNode, data nodeData, schema string, seq int) {
	// data, not node.data: this runs on a background goroutine and the UI
	// goroutine writes node.data underneath it (see explorerNode.snapshot). node
	// stays behind as the identity panicRepair and postFinal key off.
	app.safegoRepair("loading table details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(fetchCtx, childFetchTimeout)
		defer cancel()

		dbObj, tables, err := folderTables(ctx, sc, data)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}
		if schema != "" {
			tables = slices.DeleteFunc(tables, func(t *gosmo.Table) bool { return t.Schema != schema })
		}

		rows := make([][]string, len(tables))
		objs := make([]nodeData, len(tables))
		for i, t := range tables {
			rows[i] = []string{tableLabel(t), "…", "…", "…", "…"}
			objs[i] = nodeData{Type: NodeTable, DBName: data.DBName, Schema: t.Schema, Name: t.Name}
		}
		db.postPartialObjects(app, seq, tablesFolderColumns, rows, objs)

		// Either aggregate failing leaves only its own columns as "N/A" —
		// they're independent queries and one answering is better than
		// neither. A table absent from a map has no allocated pages at all,
		// which reads as zero, not as a failure.
		counts, countsErr := dbObj.TableRowCounts(ctx)
		space, spaceErr := dbObj.TableSpaceUsedAll(ctx)

		// rows is written inside the posted closure, so every write lands on
		// the UI goroutine — the same one Draw runs on. cacheOnlyObjects's own post
		// is queued after this one and App.postEvent's queue is FIFO, so the
		// cache never sees a row still showing "…".
		app.postAndWake(func() {
			for i, t := range tables {
				if countsErr != nil {
					rows[i][1] = "N/A"
				} else {
					rows[i][1] = core.FormatThousands(counts[t.ObjectID])
				}
				if spaceErr != nil {
					rows[i][2], rows[i][3], rows[i][4] = "N/A", "N/A", "N/A"
					continue
				}
				s := space[t.ObjectID]
				if s == nil {
					s = &gosmo.TableSpaceInfo{}
				}
				rows[i][2] = formatMB(float64(s.DataKB) / 1024)
				rows[i][3] = formatMB(float64(s.IndexKB) / 1024)
				rows[i][4] = formatMB(float64(s.UnusedKB) / 1024)
			}
			if db.run.Current(seq) {
				db.grid.RefreshColumnWidths()
			}
		})

		db.cacheOnlyObjects(app, node, seq, tablesFolderColumns, rows, objs, nil)
	})
}

// folderTables lists the tables of the folder data describes, filtered as the
// tree filters it. Narrowed at the server where the filter can be expressed,
// and by filterObjects either way — see nodeFilter.pushdown. The kind is what
// makes the pane agree with the tree: the Tables folder lists the plain user
// tables only, and each sub-folder its own family.
func folderTables(ctx context.Context, sc *dbconn.ServerConn, data nodeData) (*gosmo.Database, []*gosmo.Table, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, data.DBName)
	if err != nil {
		return nil, nil, err
	}
	tables, err := dbObj.Tables(ctx, gosmo.TableQuery{Kind: tableKindForNode(data.Type), Filter: serverFilter(data.Filter)})
	if err != nil {
		return nil, nil, err
	}
	tables = filterObjects(data.Filter, tables, func(t *gosmo.Table) nodeData {
		return nodeData{
			Name: t.Name, Schema: t.Schema,
			CreateDate: t.CreateDate, IsMemoryOptimized: t.IsMemoryOptimized,
		}
	})
	return dbObj, tables, nil
}
