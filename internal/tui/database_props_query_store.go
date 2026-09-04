package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// pageDatabaseQueryStore exposes Query Store's configuration (operation
// mode, capture mode, storage/cleanup, capture policy) plus its two
// maintenance actions as Apply-gated checkboxes — Flush and Clear are
// plain writes like everything else on this page, so they go through the
// same dirty-tracking/Apply/Script Changes pipeline as every other
// change here rather than firing immediately off a button click.
func pageDatabaseQueryStore(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Query Store",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			qs, err := d.QueryStoreContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			// Two groups of controls exist only on new enough servers, and
			// offering one the instance lacks leaves a row that can only fail
			// on Apply: CUSTOM capture mode and the QUERY_CAPTURE_POLICY it
			// writes are SQL Server 2019 and later, WAIT_STATS_CAPTURE_MODE is
			// 2017 and later. An unread version (0) is treated as newest.
			major := serverMajor(sc)
			hasCustomPolicy := major == 0 || major >= int(gosmo.SQLServer2019)
			hasWaitStats := major == 0 || major >= int(gosmo.SQLServer2017)

			stateItems := []string{"OFF", "READ_ONLY", "READ_WRITE"}
			captureItems := []string{"NONE", "AUTO", "ALL", "CUSTOM"}
			if !hasCustomPolicy {
				captureItems = captureItems[:len(captureItems)-1]
			}
			cleanupItems := []string{"AUTO", "OFF"}

			stateRow := propsheet.Select("Requested state", stateItems, indexOf(stateItems, qs.DesiredState))
			// The server's own capture mode still has to display even when it
			// is one this list no longer offers — a 2019 database attached to
			// a 2017 instance cannot happen, but indexOf's not-found 0 would
			// silently rename it NONE if it did.
			captureItems, captureIdx := preservingItems(captureItems, qs.CaptureMode)
			captureRow := propsheet.Select("Query capture mode", captureItems, captureIdx)
			maxSizeRow := propsheet.Int("Max size", qs.MaxStorageMB, 10, 2147483647, "MB")
			cleanupRow := propsheet.Select("Size based cleanup mode", cleanupItems, indexOf(cleanupItems, qs.SizeCleanupMode))
			staleRow := propsheet.Int("Stale query threshold", int64(qs.StaleThresholdDays), 0, 999999, "days")
			flushIntervalRow := propsheet.Int("Data flush interval", int64(qs.FlushIntervalSec), 1, 86400, "sec")
			intervalRow := propsheet.Int("Statistics interval", int64(qs.IntervalMinutes), 1, 1440, "min")
			maxPlansRow := propsheet.Int("Max plans per query", int64(qs.MaxPlansPerQuery), 0, 999999, "")
			// indexOfOK, not indexOf: below 2017 the read has no column to
			// report and returns "", which indexOf resolves to onOff[0] — a
			// value the server never reported, written back on Apply as though
			// it had.
			var waitStatsRow *propsheet.SelectRow
			if waitIdx, ok := indexOfOK(onOff, qs.WaitStatsCaptureMode); ok && hasWaitStats {
				waitStatsRow = propsheet.Select("Wait stats capture", onOff, waitIdx)
			}

			var execCountRow, compileCPURow, execCPURow, staleHoursRow *propsheet.TextRow
			if hasCustomPolicy {
				execCountRow = propsheet.Int("Custom: execution count", int64(qs.CapturePolicyExecCount), 0, 999999, "")
				compileCPURow = propsheet.Int("Custom: total compile CPU", qs.CapturePolicyCompileCPUMs, 0, 999999999, "ms")
				execCPURow = propsheet.Int("Custom: total execution CPU", qs.CapturePolicyExecCPUMs, 0, 999999999, "ms")
				staleHoursRow = propsheet.Int("Custom: stale threshold", int64(qs.CapturePolicyStaleHours), 0, 999999, "hours")
			}

			flushCheck := propsheet.Check("Flush data to disk on Apply", false)
			clearCheck := propsheet.Check("Clear Query Store on Apply", false)

			rows := []propsheet.Row{
				propsheet.Section("Operation mode"),
				propsheet.Static("Actual state", qs.ActualState),
				stateRow,
				captureRow,
				propsheet.Section("Storage"),
				propsheet.Static("Current size", strconv.FormatInt(qs.CurrentStorageMB, 10)+" MB"),
				maxSizeRow,
				cleanupRow,
				staleRow,
				propsheet.Section("Capture policy"),
				flushIntervalRow,
				intervalRow,
				maxPlansRow,
			}
			if waitStatsRow != nil {
				rows = append(rows, waitStatsRow)
			}
			if hasCustomPolicy {
				rows = append(rows, execCountRow, compileCPURow, execCPURow, staleHoursRow)
			}
			rows = append(rows,
				propsheet.Section("Actions"),
				flushCheck, clearCheck,
				propsheet.Note("Flush and Clear take effect the next time you Apply or OK, same as every other change on this page."),
			)
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				dirty := stateRow.Dirty() || captureRow.Dirty() || maxSizeRow.Dirty() || cleanupRow.Dirty() ||
					staleRow.Dirty() || flushIntervalRow.Dirty() || intervalRow.Dirty() || maxPlansRow.Dirty() ||
					(waitStatsRow != nil && waitStatsRow.Dirty()) ||
					(hasCustomPolicy && (execCountRow.Dirty() || compileCPURow.Dirty() || execCPURow.Dirty() || staleHoursRow.Dirty()))
				if dirty {
					maxSize, err := maxSizeRow.IntValue()
					if err != nil {
						return err
					}
					stale, err := staleRow.IntValue()
					if err != nil {
						return err
					}
					flushInterval, err := flushIntervalRow.IntValue()
					if err != nil {
						return err
					}
					interval, err := intervalRow.IntValue()
					if err != nil {
						return err
					}
					maxPlans, err := maxPlansRow.IntValue()
					if err != nil {
						return err
					}
					opts := gosmo.QueryStoreOptions{
						DesiredState: stateItems[stateRow.Selected()], MaxStorageMB: maxSize,
						CaptureMode: captureItems[captureRow.Selected()], SizeCleanupMode: cleanupItems[cleanupRow.Selected()],
						StaleThresholdDays: int(stale), FlushIntervalSec: int(flushInterval), IntervalMinutes: int(interval),
						MaxPlansPerQuery: int(maxPlans),
					}
					// A row the server has no setting for leaves its field at
					// the zero value, which is what gosmo omits the clause on.
					if waitStatsRow != nil {
						opts.WaitStatsCaptureMode = onOff[waitStatsRow.Selected()]
					}
					if hasCustomPolicy {
						execCount, err := execCountRow.IntValue()
						if err != nil {
							return err
						}
						compileCPU, err := compileCPURow.IntValue()
						if err != nil {
							return err
						}
						execCPU, err := execCPURow.IntValue()
						if err != nil {
							return err
						}
						staleHours, err := staleHoursRow.IntValue()
						if err != nil {
							return err
						}
						opts.CapturePolicyExecCount = int(execCount)
						opts.CapturePolicyCompileCPUMs = compileCPU
						opts.CapturePolicyExecCPUMs = execCPU
						opts.CapturePolicyStaleHours = int(staleHours)
					}
					if err := d.SetQueryStoreOptionsContext(ctx, opts); err != nil {
						return err
					}
				}
				if flushCheck.Checked() {
					if err := d.FlushQueryStoreContext(ctx); err != nil {
						return err
					}
				}
				if clearCheck.Checked() {
					if err := d.ClearQueryStoreContext(ctx); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
