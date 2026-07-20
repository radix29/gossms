package tui

import (
	"context"
	"sync"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// tablesFolderColumns are the Tables folder's detail-grid columns: the name
// first, then row count and size figures backfilled progressively (see
// loadTablesFolderDetails) since each table's counts need their own round
// trips.
var tablesFolderColumns = []string{
	"Name", "Row Count", "Data (MB)", "Index (MB)", "Unused (MB)",
}

// loadTablesFolderDetails shows the Tables folder's Name column as soon as
// the single, fast table-list query returns, then backfills each row's row
// count and space columns — one table at a time, concurrently — as its own
// RowCountContext/SpaceUsedContext round trips complete, mirroring
// loadDatabasesFolderDetails' per-row backfill pattern.
func (db *DetailBrowser) loadTablesFolderDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()

		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}
		tables, err := dbObj.TablesContext(ctx)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}

		rows := make([][]string, len(tables))
		for i, t := range tables {
			rows[i] = []string{t.Schema + "." + t.Name, "…", "…", "…", "…"}
		}
		db.postPartial(app, seq, tablesFolderColumns, rows)

		var wg sync.WaitGroup
		for i, t := range tables {
			wg.Add(1)
			go func(i int, t *gosmo.Table) {
				defer wg.Done()
				tCtx, tCancel := context.WithTimeout(context.Background(), childFetchTimeout)
				defer tCancel()
				rowCount, rcErr := t.RowCountContext(tCtx)
				space, spErr := t.SpaceUsedContext(tCtx)
				if rcErr == nil {
					rows[i][1] = core.FormatThousands(rowCount)
				} else {
					rows[i][1] = "N/A"
				}
				if spErr == nil {
					rows[i][2] = formatMB(float64(space.DataKB) / 1024)
					rows[i][3] = formatMB(float64(space.IndexKB) / 1024)
					rows[i][4] = formatMB(float64(space.UnusedKB) / 1024)
				} else {
					rows[i][2], rows[i][3], rows[i][4] = "N/A", "N/A", "N/A"
				}
				app.postEvent(func() {
					if seq != db.seq {
						return
					}
					db.grid.RefreshColumnWidths()
				})
				app.wakeEventLoop()
			}(i, t)
		}
		wg.Wait()
		db.cacheOnly(app, node, tablesFolderColumns, rows, nil)
	}()
}
