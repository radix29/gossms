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

			stateItems := []string{"OFF", "READ_ONLY", "READ_WRITE"}
			captureItems := []string{"NONE", "AUTO", "ALL", "CUSTOM"}
			cleanupItems := []string{"AUTO", "OFF"}

			stateRow := propsheet.Select("Requested state", stateItems, indexOf(stateItems, qs.DesiredState))
			captureRow := propsheet.Select("Query capture mode", captureItems, indexOf(captureItems, qs.CaptureMode))
			maxSizeRow := propsheet.Int("Max size", qs.MaxStorageMB, 10, 2147483647, "MB")
			cleanupRow := propsheet.Select("Size based cleanup mode", cleanupItems, indexOf(cleanupItems, qs.SizeCleanupMode))
			staleRow := propsheet.Int("Stale query threshold", int64(qs.StaleThresholdDays), 0, 999999, "days")
			flushIntervalRow := propsheet.Int("Data flush interval", int64(qs.FlushIntervalSec), 1, 86400, "sec")
			intervalRow := propsheet.Int("Statistics interval", int64(qs.IntervalMinutes), 1, 1440, "min")
			maxPlansRow := propsheet.Int("Max plans per query", int64(qs.MaxPlansPerQuery), 0, 999999, "")
			waitStatsRow := propsheet.Select("Wait stats capture", onOff, indexOf(onOff, qs.WaitStatsCaptureMode))
			execCountRow := propsheet.Int("Custom: execution count", int64(qs.CapturePolicyExecCount), 0, 999999, "")
			compileCPURow := propsheet.Int("Custom: total compile CPU", qs.CapturePolicyCompileCPUMs, 0, 999999999, "ms")
			execCPURow := propsheet.Int("Custom: total execution CPU", qs.CapturePolicyExecCPUMs, 0, 999999999, "ms")
			staleHoursRow := propsheet.Int("Custom: stale capture threshold", int64(qs.CapturePolicyStaleHours), 0, 999999, "hours")

			flushCheck := propsheet.Check("Flush data to disk on Apply", false)
			clearCheck := propsheet.Check("Clear Query Store on Apply", false)

			f := propsheet.NewForm(
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
				waitStatsRow,
				execCountRow, compileCPURow, execCPURow, staleHoursRow,
				propsheet.Section("Actions"),
				flushCheck, clearCheck,
				propsheet.Note("Flush and Clear take effect the next time you Apply or OK, same as every other change on this page."),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if stateRow.Dirty() || captureRow.Dirty() || maxSizeRow.Dirty() || cleanupRow.Dirty() ||
					staleRow.Dirty() || flushIntervalRow.Dirty() || intervalRow.Dirty() || maxPlansRow.Dirty() ||
					waitStatsRow.Dirty() || execCountRow.Dirty() || compileCPURow.Dirty() || execCPURow.Dirty() || staleHoursRow.Dirty() {
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
					opts := gosmo.QueryStoreOptions{
						DesiredState: stateItems[stateRow.Selected()], MaxStorageMB: maxSize,
						CaptureMode: captureItems[captureRow.Selected()], SizeCleanupMode: cleanupItems[cleanupRow.Selected()],
						StaleThresholdDays: int(stale), FlushIntervalSec: int(flushInterval), IntervalMinutes: int(interval),
						MaxPlansPerQuery: int(maxPlans), WaitStatsCaptureMode: onOff[waitStatsRow.Selected()],
						CapturePolicyExecCount: int(execCount), CapturePolicyCompileCPUMs: compileCPU,
						CapturePolicyExecCPUMs: execCPU, CapturePolicyStaleHours: int(staleHours),
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
