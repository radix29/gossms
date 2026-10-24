package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

var retentionUnits = []string{"DAYS", "HOURS", "MINUTES"}

func pageDatabaseChangeTracking(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Change Tracking",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			ct, err := d.ChangeTracking(ctx)
			if err != nil {
				return nil, nil, err
			}
			tables, err := d.TableChangeTracking(ctx)
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

			// An untracked database reads retention 0 and auto cleanup OFF. Shown
			// as read, switching tracking on was refused for retention 0 (minimum
			// 1) and then wrote AUTO_CLEANUP = OFF, leaving the side tables to grow
			// unbounded. Seeded with SQL Server's own defaults for SET
			// CHANGE_TRACKING = ON, as SSMS does; the rows load clean either way.
			if !ct.Enabled {
				ct.RetentionPeriod, ct.RetentionUnit, ct.AutoCleanup = 2, gosmo.ChangeTrackingDays, true
			}
			enabledRow := propsheet.Select("Change tracking", onOff, boolIdx(ct.Enabled))
			retentionRow := propsheet.Int("Retention period", int64(ct.RetentionPeriod), 1, 100000, "")
			unitRow := propsheet.Select("Retention period units", retentionUnits, indexOf(retentionUnits, orDefault(string(ct.RetentionUnit), "DAYS")))
			autoCleanupRow := propsheet.Select("Auto cleanup", onOff, boolIdx(ct.AutoCleanup))

			// stillTracked names the tables the grid leaves tracked. Switching the
			// database off while any is listed is refused by the server (Msg 22115,
			// "Disable change tracking on each table before disabling it for the
			// database"), and by then earlier writes could have landed, so it is
			// refused here first, saying which tables to untick.
			stillTracked := func() []string {
				var names []string
				for i, v := range tablesRow.Values() {
					if v[0] {
						names = append(names, text[i][0])
					}
				}
				return names
			}
			enabledRow.SetValidate(func(v string) error {
				if v != "OFF" {
					return nil
				}
				if names := stillTracked(); len(names) > 0 {
					return fmt.Errorf("untick Enabled for %s before switching change tracking off for the database", strings.Join(names, ", "))
				}
				return nil
			})

			f := propsheet.NewForm(
				propsheet.Section("Change Tracking"),
				enabledRow, retentionRow, unitRow, autoCleanupRow,
				propsheet.Section("Tables using change tracking"),
				tablesRow,
			)
			f.SetApplyConfirm(func() string {
				var names []string
				for i, v := range tablesRow.Values() {
					if t := tables[i]; changeTrackingReset(t.Enabled, t.TrackColumnsUpdated, v[0], v[1]) {
						names = append(names, text[i][0])
					}
				}
				return changeTrackingResetWarning(names)
			})

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByName(ctx, dbName)
				if err != nil {
					return err
				}
				setDatabase := func() error {
					if !(enabledRow.Dirty() || retentionRow.Dirty() || unitRow.Dirty() || autoCleanupRow.Dirty()) {
						return nil
					}
					// Retention edited on a database left off has nothing to set:
					// SET CHANGE_TRACKING = OFF on an untracked one is Msg 5089.
					if !ct.Enabled && enabledRow.Selected() == 0 {
						return nil
					}
					period, err := retentionRow.IntValue()
					if err != nil {
						return err
					}
					return d.SetChangeTracking(ctx, gosmo.ChangeTrackingInfo{
						Enabled:         enabledRow.Selected() == 1,
						AutoCleanup:     autoCleanupRow.Selected() == 1,
						RetentionPeriod: int(period),
						RetentionUnit:   gosmo.ChangeTrackingUnit(retentionUnits[unitRow.Selected()]),
					})
				}
				setTables := func() error {
					for i, v := range tablesRow.Values() {
						t := tables[i]
						// Track columns ticked on a table left untracked is nothing to
						// write: a DISABLE there fails (Msg 4997, not enabled).
						if v[0] == t.Enabled && (v[1] == t.TrackColumnsUpdated || !v[0]) {
							continue
						}
						if err := d.TableRef(t.Schema, t.Name).SetChangeTracking(ctx, gosmo.TableChangeTrackingOptions{Enabled: v[0], TrackColumnsUpdated: v[1]}); err != nil {
							return err
						}
					}
					return nil
				}
				// The database is the outer switch: tables can be tracked only while it is
				// on, so it goes on before them and off after them.
				first, second := setDatabase, setTables
				if ct.Enabled && enabledRow.Selected() == 0 {
					first, second = setTables, setDatabase
				}
				if err := first(); err != nil {
					return err
				}
				return second()
			}
			return f, apply, nil
		},
	}
}

// changeTrackingReset reports whether moving a table's change tracking from
// (wasOn, wasCols) to (on, cols) resets it: Track columns updated changed on a
// table tracked before and after. There is no ALTER for that, so gosmo's
// Table.SetChangeTracking disables and re-enables tracking (review plan M4).
func changeTrackingReset(wasOn, wasCols, on, cols bool) bool {
	return wasOn && on && wasCols != cols
}

// changeTrackingResetWarning is the apply question for the tables
// changeTrackingReset names, "" for none — both Change Tracking pages ask it.
func changeTrackingResetWarning(tables []string) string {
	if len(tables) == 0 {
		return ""
	}
	return "Changing Track columns updated on " + strings.Join(tables, ", ") +
		" disables and re-enables its change tracking. Its change history is discarded and " +
		"CHANGE_TRACKING_MIN_VALID_VERSION moves, so every client syncing it must reinitialise."
}
