package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/charts"
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
		objs := make([]nodeData, 0, len(all))
		for _, d := range all {
			if d.IsSystem() {
				continue
			}
			dbs = append(dbs, d)
			rows = append(rows, []string{d.Name(), d.State(), string(d.RecoveryModel()), "…", "…", "…", "…", "…"})
			objs = append(objs, nodeData{Type: NodeDatabase, DBName: d.Name(), Name: d.Name()})
		}
		db.postPartialObjects(app, seq, databasesFolderColumns, rows, objs)

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

		db.cacheOnlyObjects(app, node, seq, databasesFolderColumns, rows, objs, nil)
	})
}

// loadDatabaseDetails is one database's Property/Value view plus the disk
// usage strip under it. It has its own loader rather than an arm of
// fetchNodeDetails because it is the only detail view with charts, and
// gosmo's DiskUsage answers both halves in a single round trip — the file
// sizes the properties show and the allocation breakdown the bars split
// them by.
func (db *DetailBrowser) loadDatabaseDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	// data, not node: the fetch runs on a background goroutine while the UI
	// goroutine may write node.data — see explorerNode.snapshot.
	name := node.data.DBName
	app.safegoRepair("loading database details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()

		d, err := sc.Server.DatabaseByNameContext(ctx, name)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}
		// A database that is offline, restoring or otherwise unreadable
		// answers its sys.databases metadata and nothing else, so the space
		// figures are reported as N/A and the strip is left off rather than
		// drawn empty — the properties are still worth showing.
		usage, usageErr := d.DiskUsageContext(ctx)

		sizeStr, dataStr, logStr, availDataStr, availLogStr := "N/A", "N/A", "N/A", "N/A", "N/A"
		var cs []detailChart
		if usageErr == nil {
			sizeStr = formatMB(usage.DataFilesMB + usage.LogFilesMB)
			dataStr, logStr = formatMB(usage.DataFilesMB), formatMB(usage.LogFilesMB)
			availDataStr, availLogStr = formatMB(usage.UnallocatedMB), formatMB(usage.LogUnusedMB)
			cs = diskUsageCharts(usage)
		}
		db.postFinalCharts(app, node, seq, []string{"Property", "Value"}, [][]string{
			{"Name", d.Name()},
			{"State", d.State()},
			{"Recovery Model", string(d.RecoveryModel())},
			{"Compatibility Level", fmt.Sprintf("%d", d.CompatibilityLevel())},
			{"Collation", d.Collation()},
			{"Create Date", formatSQLDate(d.CreateDate())},
			{"Read Only", fmt.Sprintf("%v", d.IsReadOnly())},
			{"Size (MB)", sizeStr},
			{"Data (MB)", dataStr},
			{"Log (MB)", logStr},
			{"Avail. Data (MB)", availDataStr},
			{"Avail. Log (MB)", availLogStr},
		}, cs, nil)
	})
}

// diskUsageCharts are the two composition bars of the Disk Usage strip, in
// SSMS's Disk Usage report order and split the same way: the data files
// against what their pages hold, the log files against how much of them is
// live.
//
// The segments are read against each other, not against the file total —
// StackedBar's zero Scale fills the width — which is why the data-file bar
// carries the file size as its total and the parts, which sum to slightly
// less (see gosmo.DiskUsage), still fill it. Format is what puts megabytes
// on the tooltip a click pins: the legend under the bar has room for the
// names only.
func diskUsageCharts(u gosmo.DiskUsage) []detailChart {
	cyan, green, _, blue, _, purple, _ := chartColors()
	return []detailChart{
		{Title: "DATA FILES SPACE USAGE", Format: formatMB, Series: []charts.Series{
			{Label: "Data", Short: "Dat", Color: blue, Values: []float64{u.DataMB}},
			{Label: "Index", Short: "Idx", Color: purple, Values: []float64{u.IndexMB}},
			{Label: "Unused", Short: "Unu", Color: cyan, Values: []float64{u.UnusedMB}},
			{Label: "Unallocated", Short: "Unall", Color: green, Values: []float64{u.UnallocatedMB}},
		}},
		{Title: "TRANSACTION LOG SPACE USAGE", Format: formatMB, Series: []charts.Series{
			{Label: "Used", Short: "Use", Color: blue, Values: []float64{u.LogUsedMB}},
			{Label: "Unused", Short: "Unu", Color: green, Values: []float64{u.LogUnusedMB}},
		}},
	}
}

// databaseTriggersFolderDetail lists a database's DDL triggers. It reads
// gosmo independently of the tree, so the folder's filter is applied here too
// — over the gosmo objects, before the rows are built.
func databaseTriggersFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	triggers, err := dbObj.DatabaseTriggersContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	triggers = filterObjects(node.data.Filter, triggers, func(t *gosmo.DatabaseTrigger) nodeData {
		return nodeData{Name: t.Name, CreateDate: t.CreateDate}
	})

	rows := make([][]string, 0, len(triggers))
	out := make([]nodeData, 0, len(triggers))
	for _, t := range triggers {
		rows = append(rows, []string{
			t.Name, enabledText(t.IsEnabled), strings.Join(t.Events, ", "),
			formatSQLDate(t.CreateDate), formatSQLDate(t.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeDatabaseTrigger, DBName: node.data.DBName, Name: t.Name})
	}
	*objs = out
	return []string{"Name", "Status", "Events", "Created", "Modified"}, rows, nil
}

// databaseTriggerDetail is one DDL trigger's Property/Value view. The
// definition is not shown here — it is multi-line, which a grid row flattens;
// the Properties dialog's Definition page is where it belongs.
func databaseTriggerDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	t, err := dbObj.DatabaseTriggerByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", t.Name,
		"Status", enabledText(t.IsEnabled),
		"Scope", "Database",
		"Events", strings.Join(t.Events, ", "),
		"Created", formatSQLDate(t.CreateDate),
		"Modified", formatSQLDate(t.ModifyDate),
	)
}
