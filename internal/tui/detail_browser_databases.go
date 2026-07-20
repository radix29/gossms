package tui

import (
	"context"
	"sync"

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
// returns, then backfills each row's size columns as its own
// SpaceUsedContext round trip completes. Sizes can't be answered from that
// first query — each database needs its own USE-scoped query — and firing
// them all concurrently means one slow database doesn't hold up the rest.
func (db *DetailBrowser) loadDatabasesFolderDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()

		all, err := sc.Server.DatabasesContext(ctx)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}

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

		var wg sync.WaitGroup
		for i, d := range dbs {
			wg.Add(1)
			go func(i int, d *gosmo.Database) {
				defer wg.Done()
				dCtx, dCancel := context.WithTimeout(context.Background(), childFetchTimeout)
				defer dCancel()
				space, spaceErr := d.SpaceUsedContext(dCtx)
				// rows[i] is only ever written here, inside the postEvent
				// closure, so every write lands on the UI goroutine — the
				// same one Draw() runs on — rather than racing a redraw
				// triggered by some other row's own completion or an
				// unrelated event arriving mid-fetch.
				app.postEvent(func() {
					if seq != db.seq {
						return
					}
					if spaceErr == nil {
						rows[i][3] = formatMB(space.TotalMB)
						rows[i][4] = formatMB(space.DataMB)
						rows[i][5] = formatMB(space.LogMB)
						rows[i][6] = formatMB(space.UnallocatedMB)
						rows[i][7] = formatMB(space.AvailLogMB)
					} else {
						for c := 3; c <= 7; c++ {
							rows[i][c] = "N/A"
						}
					}
					db.grid.RefreshColumnWidths()
				})
				app.wakeEventLoop()
			}(i, d)
		}
		wg.Wait()
		db.cacheOnly(app, node, databasesFolderColumns, rows, nil)
	}()
}
