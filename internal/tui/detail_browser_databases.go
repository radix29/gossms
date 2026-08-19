package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// databasesFolderColumns are the Databases folder's detail-grid columns:
// identity fields first, then size figures backfilled progressively (see
// loadDatabasesFolderDetails) since each database's size needs its own
// round trip via gosmo's SpaceUsedContext.
var databasesFolderColumns = []string{
	"Name", "State", "Recovery",
	"Total (MB)", "Data (MB)", "Log (MB)", "Avail. Data (MB)", "Avail. Log (MB)",
}

// formatMB renders a database size in MB, rounded to the nearest whole MB
// with a thousands separator, e.g. 123456.7 -> "123,457 MB".
func formatMB(mb float64) string {
	return core.FormatThousands(int64(mb+0.5)) + " MB"
}

// loadDatabasesFolderDetails shows the Databases folder's Name/State/
// Recovery columns as soon as the single, fast database-list query
// returns, then backfills each row's size columns — up to
// maxRowFetchConcurrency databases at a time — as its own
// SpaceUsedContext round trip completes. Sizes can't be answered from that
// first query — each database needs its own USE-scoped query — and running
// them concurrently (bounded by maxRowFetchConcurrency) means one slow
// database doesn't hold up the rest.
func (db *DetailBrowser) loadDatabasesFolderDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	// data, not node: this runs on a background goroutine and the UI goroutine
	// writes node.data underneath it (see explorerNode.snapshot). node stays
	// behind as the identity panicRepair and postFinal key off.
	data := node.data
	app.safegoRepair("loading database details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()

		all, err := sc.Server.DatabasesContext(ctx)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}

		all = filterObjects(data.Filter, all, func(d *gosmo.Database) nodeData {
			return nodeData{Name: d.Name(), CreateDate: d.CreateDate()}
		})

		dbs := make([]*gosmo.Database, 0, len(all))
		rows := make([][]string, 0, len(all))
		for _, d := range all {
			if d.IsSystem() {
				continue
			}
			dbs = append(dbs, d)
			rows = append(rows, []string{d.Name(), d.State(), string(d.RecoveryModel()), "…", "…", "…", "…", "…"})
		}
		db.postPartial(app, seq, databasesFolderColumns, rows)

		// Unlike the Tables folder, these sizes genuinely need one round trip
		// per database: every figure but the total comes from FILEPROPERTY,
		// which reports on the current database only, so there is no
		// server-wide query to replace the fan-out with.
		markFailed := func(i int) {
			for c := 3; c <= 7; c++ {
				rows[i][c] = "N/A"
			}
		}
		db.backfillRows(app, sc, seq, len(dbs), "loading database size",
			func(ctx context.Context, i int) func() {
				space, err := dbs[i].SpaceUsedContext(ctx)
				return func() {
					if err != nil {
						markFailed(i)
						return
					}
					rows[i][3] = formatMB(space.TotalMB)
					rows[i][4] = formatMB(space.DataMB)
					rows[i][5] = formatMB(space.LogMB)
					rows[i][6] = formatMB(space.UnallocatedMB)
					rows[i][7] = formatMB(space.AvailLogMB)
				}
			}, markFailed)

		db.cacheOnly(app, node, seq, databasesFolderColumns, rows, nil)
	})
}
