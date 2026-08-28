package tui

import (
	"context"

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

// loadTablesFolderDetails shows the Tables folder's Name column as soon as
// the fast table-list query returns, then fills every row's row count and
// space columns from two whole-database aggregate queries.
//
// Two queries for the folder, not two per table. This used to fan out
// Table.RowCount and Table.SpaceUsed per row, bounded to
// maxRowFetchConcurrency at a time — 2N round trips, each on its own pooled
// connection, so a database with a few hundred tables cost a few hundred
// queries and visibly trickled in. gosmo's TableRowCounts and
// TableSpaceUsedAll answer the same question for every table at once (same
// aggregates, grouped by object_id instead of filtered to one), so the
// fan-out is gone. loadDatabasesFolderDetails still needs its own — see the
// note there for why the same collapse isn't available for database sizes.
func (db *DetailBrowser) loadTablesFolderDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	// data, not node: this runs on a background goroutine and the UI goroutine
	// writes node.data underneath it (see explorerNode.snapshot). node stays
	// behind as the identity panicRepair and postFinal key off.
	data := node.data
	app.safegoRepair("loading table details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()

		dbObj, err := sc.Server.DatabaseByNameContext(ctx, data.DBName)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}
		// Narrowed at the server where the filter can be expressed, and by
		// filterObjects below either way — see nodeFilter.pushdown.
		tables, err := dbObj.TablesFilteredContext(ctx, serverFilter(data.Filter))
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}
		tables = filterObjects(data.Filter, tables, func(t *gosmo.Table) nodeData {
			return nodeData{
				Name: t.Name, Schema: t.Schema,
				CreateDate: t.CreateDate, IsMemoryOptimized: t.IsMemoryOptimized,
			}
		})

		rows := make([][]string, len(tables))
		objs := make([]nodeData, len(tables))
		for i, t := range tables {
			rows[i] = []string{t.Schema + "." + t.Name, "…", "…", "…", "…"}
			objs[i] = nodeData{Type: NodeTable, DBName: data.DBName, Schema: t.Schema, Name: t.Name}
		}
		db.postPartialObjects(app, seq, tablesFolderColumns, rows, objs)

		// Either aggregate failing leaves only its own columns as "N/A" —
		// they're independent queries and one answering is better than
		// neither. A table absent from a map has no allocated pages at all,
		// which reads as zero, not as a failure.
		counts, countsErr := dbObj.TableRowCountsContext(ctx)
		space, spaceErr := dbObj.TableSpaceUsedAllContext(ctx)

		// rows is written inside the posted closure, so every write lands on
		// the UI goroutine — the same one Draw runs on. cacheOnly's own post
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
			if seq == db.seq {
				db.grid.RefreshColumnWidths()
			}
		})

		db.cacheOnlyObjects(app, node, seq, tablesFolderColumns, rows, objs, nil)
	})
}
