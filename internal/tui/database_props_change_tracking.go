package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

var retentionUnits = []string{"DAYS", "HOURS", "MINUTES"}

func pageDatabaseChangeTracking(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Change Tracking",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			ct, err := d.ChangeTrackingContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			tables, err := d.TableChangeTrackingContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			text := make([][]string, len(tables))
			values := make([][]bool, len(tables))
			for i, t := range tables {
				text[i] = []string{t.Schema + "." + t.Name}
				values[i] = []bool{t.Enabled, t.TrackColumnsUpdated}
			}
			tablesRow := propsheet.NewToggleGrid([]string{"Table name", "Enabled", "Track columns updated"}, []int{1, 2}, 10)
			tablesRow.SetRows(text, values)

			enabledRow := propsheet.Select("Change tracking", onOff, boolIdx(ct.Enabled))
			retentionRow := propsheet.Int("Retention period", int64(ct.RetentionPeriod), 1, 100000, "")
			unitRow := propsheet.Select("Retention period units", retentionUnits, indexOf(retentionUnits, orDefault(ct.RetentionUnit, "DAYS")))
			autoCleanupRow := propsheet.Select("Auto cleanup", onOff, boolIdx(ct.AutoCleanup))

			f := propsheet.NewForm(
				propsheet.Section("Change Tracking"),
				enabledRow, retentionRow, unitRow, autoCleanupRow,
				propsheet.Section("Tables using change tracking"),
				tablesRow,
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if enabledRow.Dirty() || retentionRow.Dirty() || unitRow.Dirty() || autoCleanupRow.Dirty() {
					period, err := retentionRow.IntValue()
					if err != nil {
						return err
					}
					info := gosmo.ChangeTrackingInfo{
						Enabled:         enabledRow.Selected() == 1,
						AutoCleanup:     autoCleanupRow.Selected() == 1,
						RetentionPeriod: int(period),
						RetentionUnit:   retentionUnits[unitRow.Selected()],
					}
					if err := d.SetChangeTrackingContext(ctx, info); err != nil {
						return err
					}
				}
				for i, v := range tablesRow.Values() {
					t := tables[i]
					if v[0] == t.Enabled && v[1] == t.TrackColumnsUpdated {
						continue
					}
					if err := d.SetTableChangeTrackingContext(ctx, t.Schema, t.Name, v[0], v[1]); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
