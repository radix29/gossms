package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// scopedConfigRow pairs an editable Int row with the database scoped
// configuration option name it edits, so a page's apply closure can write
// back only the ones that changed.
type scopedConfigRow struct {
	name string
	row  *propsheet.TextRow
}

// scopedConfigBoolRow is scopedConfigRow's Select-row counterpart, for
// ON/OFF-style options — SetDatabaseScopedConfigContext takes the keyword
// ALTER DATABASE SCOPED CONFIGURATION expects ("ON"/"OFF"), not the "0"/"1"
// sys.database_scoped_configurations reports a boolean option's value as.
type scopedConfigBoolRow struct {
	name string
	row  *propsheet.SelectRow
}

func findScopedConfig(configs []*gosmo.DatabaseScopedConfig, name string) *gosmo.DatabaseScopedConfig {
	for _, c := range configs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// newScopedConfigIntEditor returns a builder that creates an editable Int
// row for a named scoped configuration option, appending it to *tracked.
// An option missing on this server/edition renders as a disabled "N/A" row.
func newScopedConfigIntEditor(configs []*gosmo.DatabaseScopedConfig, tracked *[]scopedConfigRow) func(name, label, unit string) *propsheet.TextRow {
	return func(name, label, unit string) *propsheet.TextRow {
		c := findScopedConfig(configs, name)
		if c == nil {
			row := propsheet.Text(label, "N/A", 12)
			row.SetEnabled(false)
			return row
		}
		v, _ := strconv.ParseInt(c.Value, 10, 64)
		row := propsheet.Int(label, v, 0, 2147483647, unit)
		*tracked = append(*tracked, scopedConfigRow{name: name, row: row})
		return row
	}
}

// newScopedConfigBoolEditor is newScopedConfigIntEditor's counterpart for
// options whose value is conventionally "0"/"1".
func newScopedConfigBoolEditor(configs []*gosmo.DatabaseScopedConfig, tracked *[]scopedConfigBoolRow) func(name, label string) *propsheet.SelectRow {
	return func(name, label string) *propsheet.SelectRow {
		c := findScopedConfig(configs, name)
		idx := 0
		if c != nil && c.Value == "1" {
			idx = 1
		}
		row := propsheet.Select(label, onOff, idx)
		if c == nil {
			return row
		}
		*tracked = append(*tracked, scopedConfigBoolRow{name: name, row: row})
		return row
	}
}

// applyScopedConfigRows writes back every dirty row in intRows/boolRows
// via Database.SetDatabaseScopedConfigContext.
func applyScopedConfigRows(ctx context.Context, d *gosmo.Database, intRows []scopedConfigRow, boolRows []scopedConfigBoolRow) error {
	for _, r := range intRows {
		if !r.row.Dirty() {
			continue
		}
		v, err := r.row.IntValue()
		if err != nil {
			return err
		}
		if err := d.SetDatabaseScopedConfigContext(ctx, r.name, strconv.FormatInt(v, 10), false); err != nil {
			return err
		}
	}
	for _, r := range boolRows {
		if !r.row.Dirty() {
			continue
		}
		value := onOff[r.row.Selected()]
		if err := d.SetDatabaseScopedConfigContext(ctx, r.name, value, false); err != nil {
			return err
		}
	}
	return nil
}

// pageDatabaseScopedConfig groups the well-known
// sys.database_scoped_configurations options into editable rows, then
// lists every option — including ones this build doesn't expose
// individually — in a read-only grid underneath, the same pattern Server
// Properties' Advanced page uses for sys.configurations.
func pageDatabaseScopedConfig(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Database Scoped Configurations",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			configs, err := d.DatabaseScopedConfigsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []scopedConfigRow
			var boolRows []scopedConfigBoolRow
			cfgInt := newScopedConfigIntEditor(configs, &intRows)
			cfgBool := newScopedConfigBoolEditor(configs, &boolRows)

			rows := make([][]string, len(configs))
			for i, c := range configs {
				rows[i] = []string{c.Name, c.Value, c.ValueForSecondary}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Configuration", "Value", "Value for secondary"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Query optimizer"),
				cfgInt("MAXDOP", "Max DOP", ""),
				cfgBool("LEGACY_CARDINALITY_ESTIMATION", "Legacy cardinality estimation"),
				cfgBool("PARAMETER_SNIFFING", "Parameter sniffing"),
				cfgBool("QUERY_OPTIMIZER_HOTFIXES", "Query optimizer hotfixes"),
				cfgBool("INTERLEAVED_EXECUTION_TVF", "Interleaved execution for TVFs"),
				cfgBool("BATCH_MODE_MEMORY_GRANT_FEEDBACK", "Batch mode memory grant feedback"),
				cfgBool("BATCH_MODE_ADAPTIVE_JOINS", "Batch mode adaptive joins"),
				cfgBool("TSQL_SCALAR_UDF_INLINING", "TSQL scalar UDF inlining"),
				cfgBool("ACCELERATED_PLAN_FORCING", "Accelerated plan forcing"),
				cfgBool("OPTIMIZED_PLAN_FORCING", "Optimized plan forcing"),
				propsheet.Section("Miscellaneous"),
				cfgBool("GLOBAL_TEMPORARY_TABLE_AUTO_DROP", "Global temporary table auto drop"),
				propsheet.Section("All database scoped configurations (sys.database_scoped_configurations)"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("The grid above is read-only — edit an option from its group above if it has one. \"Value for secondary\" (Always On readable secondaries) isn't editable in this build."),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				return applyScopedConfigRows(ctx, d, intRows, boolRows)
			}
			return f, apply, nil
		},
	}
}
