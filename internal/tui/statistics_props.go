package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// statisticPropPages builds the page set for Statistics Properties.
// Permissions — the mockup's final page — is dropped for the same reason
// Index Properties drops it: statistics aren't a SQL Server securable
// class either. No recompute/Incremental are shown read-only (General and
// Details) rather than as editable toggles: SQL Server has no ALTER
// STATISTICS to flip them in isolation, only UPDATE STATISTICS ... WITH
// NORECOMPUTE — so changing them is folded into the Details page's Update
// Statistics action rather than the OK/Cancel/Apply pipeline.
func statisticPropPages(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) []propPage {
	return []propPage{
		pageStatisticGeneral(sc, dbName, schema, table, name),
		pageStatisticColumns(sc, dbName, schema, table, name),
		pageStatisticFilter(d, sc, dbName, schema, table, name),
		pageStatisticDetails(d, sc, dbName, schema, table, name),
		pageStatisticHistogram(sc, dbName, schema, table, name),
		pageStatisticDensityVector(sc, dbName, schema, table, name),
		pageStatisticExtendedProperties(sc, dbName, schema, table, name),
	}
}

// findStatistic resolves dbName/schema/table/name to the owning *gosmo.Table
// and its *gosmo.Statistic — there's no StatisticByNameContext (gosmo only
// exposes the bulk StatisticsContext listing), mirroring findIndex.
func findStatistic(ctx context.Context, sc *db.ServerConn, dbName, schema, table, name string) (*gosmo.Table, *gosmo.Statistic, error) {
	t, err := findTable(ctx, sc, dbName, schema, table)
	if err != nil {
		return nil, nil, err
	}
	stats, err := t.StatisticsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, st := range stats {
		if st.Name == name {
			return t, st, nil
		}
	}
	return nil, nil, fmt.Errorf("statistic %q not found on %s", name, fqn(schema, table))
}

func pageStatisticGeneral(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, st, err := findStatistic(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}

			createdBy := "System"
			if st.IsUserCreated {
				createdBy = "User"
			}
			samplingRate := "n/a"
			if st.TotalRows > 0 {
				samplingRate = fmt.Sprintf("%.1f %%", float64(st.RowsSampled)/float64(st.TotalRows)*100)
			}

			f := propsheet.NewForm(
				propsheet.Section("Statistics identity"),
				propsheet.Static("Statistics name", st.Name),
				propsheet.Static("Created by", createdBy),
				propsheet.Static("Auto-created", boolStr(st.IsAutoCreated)),
				propsheet.Static("User-created", boolStr(st.IsUserCreated)),
				propsheet.Static("No recompute", boolStr(st.NoRecompute)),
				propsheet.Static("Incremental", boolStr(st.IsIncremental)),
				propsheet.Section("Parent object"),
				propsheet.Static("Schema", t.Schema),
				propsheet.Static("Object", t.Name),
				propsheet.Static("Object type", "Table"),
				propsheet.Section("Current statistics metadata"),
				propsheet.Static("Last updated", formatSQLDate(st.LastUpdated)),
				propsheet.Static("Rows", strconv.FormatInt(st.TotalRows, 10)),
				propsheet.Static("Rows sampled", strconv.FormatInt(st.RowsSampled, 10)),
				propsheet.Static("Sampling rate", samplingRate),
				propsheet.Static("Steps", strconv.Itoa(st.Steps)),
				propsheet.Static("Unfiltered rows", strconv.FormatInt(st.UnfilteredRows, 10)),
				propsheet.Static("Modification counter", strconv.FormatInt(st.ModificationCounter, 10)),
			)
			return f, nil, nil
		},
	}
}

// pageStatisticColumns is a read-only grid of this statistic's columns plus
// a "selected column" detail section — the same read-only-listing shape
// Table Properties' own Columns page uses. Sort order is always "Ascending"
// — SQL Server statistics have no per-column sort direction of their own
// (unlike an index's key columns), so nothing is queried for it.
func pageStatisticColumns(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, st, err := findStatistic(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			statCols, err := st.ColumnsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			tableCols, err := t.ColumnsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			colByName := make(map[string]*gosmo.Column, len(tableCols))
			for _, c := range tableCols {
				colByName[c.Name] = c
			}

			rows := make([][]string, len(statCols))
			for i, name := range statCols {
				rows[i] = []string{strconv.Itoa(i + 1), name, "Ascending"}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Ord", "Column name", "Sort order"}, rows)
			grid.SetCellCursor(true)

			nameStatic := propsheet.Static("Selected column", "")
			dataTypeStatic := propsheet.Static("Data type", "")
			nullableStatic := propsheet.Static("Nullable", "")
			computedStatic := propsheet.Static("Computed", "")
			collationStatic := propsheet.Static("Collation", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(statCols) {
					return
				}
				nameStatic.SetValue(statCols[row])
				c, ok := colByName[statCols[row]]
				if !ok {
					return
				}
				dataTypeStatic.SetValue(gosmo.ColumnTypeString(c))
				nullableStatic.SetValue(boolStr(c.IsNullable))
				computedStatic.SetValue(boolStr(c.IsComputed))
				collationStatic.SetValue(orDefault(c.Collation, "n/a"))
			}
			grid.OnSelectRow = syncFromSelection
			if len(statCols) > 0 {
				syncFromSelection(0)
			}

			f := propsheet.NewForm(
				propsheet.Section("Statistics columns"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Section("Column details"),
				nameStatic, dataTypeStatic, nullableStatic, computedStatic, collationStatic,
				propsheet.Note("The histogram is built on the leading column only. Additional columns contribute density information used by the optimizer."),
			)
			return f, nil, nil
		},
	}
}

// pageStatisticFilter is Statistics Properties' Filter page — shares
// buildFilterInfoForm with Index Properties' own Filter page.
func pageStatisticFilter(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Filter",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, st, err := findStatistic(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			f := buildFilterInfoForm(d, t, st.HasFilter, st.FilterDef)
			return f, nil, nil
		},
	}
}

// pageStatisticDetails is Statistics Properties' Details page:
// DBCC SHOW_STATISTICS ... WITH STAT_HEADER's fields, plus Update
// Statistics/Script actions that run immediately, independent of
// OK/Cancel/Apply.
func pageStatisticDetails(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Details",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			_, st, err := findStatistic(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			hdr, err := st.HeaderContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			samplingMethod := "Not yet computed"
			if hdr.Updated != "" {
				samplingMethod = fmt.Sprintf("Sample %.1f %%", hdr.PersistedSamplePercent)
				if hdr.Rows > 0 && hdr.RowsSampled >= hdr.Rows {
					samplingMethod = "Full scan"
				}
			}

			statusRow := propsheet.Static("Last action", "")
			updateBtn := d.asyncStatusButton("Update Statistics", statusRow, "Updating...", func(ctx context.Context) (string, error) {
				if err := st.UpdateContext(ctx, 0); err != nil {
					return "", err
				}
				return "Statistics updated (full scan)", nil
			})
			scriptCreateBtn := widgets.NewButton("Script as CREATE", func() {
				statusRow.SetValue("Scripting...")
				var ddl string
				d.runPageAction(func(ctx context.Context) error {
					cols, err := st.ColumnsContext(ctx)
					if err != nil {
						return err
					}
					quoted := make([]string, len(cols))
					for i, c := range cols {
						quoted[i] = fqn("", c)
					}
					ddl = fmt.Sprintf("CREATE STATISTICS %s ON %s (%s)", fqn("", st.Name), fqn(schema, table), strings.Join(quoted, ", "))
					return nil
				}, func(err error) {
					if err != nil {
						statusRow.SetValue("Error: " + err.Error())
						return
					}
					statusRow.SetValue("")
					d.app.openQueryWithText(sc, dbName, ddl)
				})
			})
			scriptUpdateBtn := widgets.NewButton("Script as UPDATE", func() {
				ddl := fmt.Sprintf("UPDATE STATISTICS %s (%s) WITH FULLSCAN", fqn(schema, table), fqn("", st.Name))
				d.app.openQueryWithText(sc, dbName, ddl)
			})

			f := propsheet.NewForm(
				propsheet.Section("Statistics details"),
				propsheet.Static("Last updated", orDefault(hdr.Updated, "Never")),
				propsheet.Static("Rows", strconv.FormatInt(hdr.Rows, 10)),
				propsheet.Static("Rows sampled", strconv.FormatInt(hdr.RowsSampled, 10)),
				propsheet.Static("Sampling method", samplingMethod),
				propsheet.Static("Steps", strconv.Itoa(hdr.Steps)),
				propsheet.Static("Density", fmt.Sprintf("%.8f", hdr.Density)),
				propsheet.Static("Average key length", fmt.Sprintf("%.1f", hdr.AverageKeyLength)),
				propsheet.Static("String index", orDefault(hdr.StringIndex, "n/a")),
				propsheet.Static("Unfiltered rows", strconv.FormatInt(hdr.UnfilteredRows, 10)),
				propsheet.Section("Update options"),
				propsheet.Static("No recompute", boolStr(st.NoRecompute)),
				propsheet.Static("Incremental", boolStr(st.IsIncremental)),
				propsheet.Section("Actions"),
				propsheet.Buttons(updateBtn, scriptCreateBtn, scriptUpdateBtn),
				statusRow,
				propsheet.Note("Update Statistics runs immediately (full scan), independent of OK/Cancel/Apply. Press F5 to refresh this page's numbers afterward."),
			)
			return f, nil, nil
		},
	}
}

func pageStatisticHistogram(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Histogram",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			_, st, err := findStatistic(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			cols, err := st.ColumnsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			leadCol := "?"
			if len(cols) > 0 {
				leadCol = cols[0]
			}
			steps, err := st.HistogramContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(steps))
			for i, s := range steps {
				rows[i] = []string{strconv.Itoa(i + 1), s.RangeHighKey,
					fmt.Sprintf("%.0f", s.EqRows), fmt.Sprintf("%.0f", s.RangeRows)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Step", "Range high key", "Eq rows", "Range rows"}, rows)
			grid.SetCellCursor(true)

			rangeHiStatic := propsheet.Static("Range high key", "")
			eqStatic := propsheet.Static("Equal rows", "")
			rangeStatic := propsheet.Static("Range rows", "")
			distinctStatic := propsheet.Static("Distinct range rows", "")
			avgStatic := propsheet.Static("Average range rows", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(steps) {
					return
				}
				s := steps[row]
				rangeHiStatic.SetValue(s.RangeHighKey)
				eqStatic.SetValue(fmt.Sprintf("%.0f", s.EqRows))
				rangeStatic.SetValue(fmt.Sprintf("%.0f", s.RangeRows))
				distinctStatic.SetValue(strconv.FormatInt(s.DistinctRangeRows, 10))
				avgStatic.SetValue(fmt.Sprintf("%.4f", s.AvgRangeRows))
			}
			grid.OnSelectRow = syncFromSelection
			if len(steps) > 0 {
				syncFromSelection(0)
			}

			f := propsheet.NewForm(
				propsheet.Section("Histogram on leading key column: "+leadCol),
				propsheet.NewGridRow(grid, 10),
				propsheet.Section("Selected step"),
				rangeHiStatic, eqStatic, rangeStatic, distinctStatic, avgStatic,
				propsheet.Note("The histogram exists only for the statistic's leading (first) column."),
			)
			return f, nil, nil
		},
	}
}

func pageStatisticDensityVector(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Density Vector",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			_, st, err := findStatistic(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			densities, err := st.DensityVectorContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(densities))
			for i, dv := range densities {
				rows[i] = []string{dv.Columns, fmt.Sprintf("%.8f", dv.AllDensity), fmt.Sprintf("%.1f", dv.AverageLength)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Columns", "All density", "Average length"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("All density and average length"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Note("Density values are used for predicates that can't be estimated directly from the histogram — one row per leading-column prefix of the statistic's columns."),
			)
			return f, nil, nil
		},
	}
}

func pageStatisticExtendedProperties(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			level := gosmo.ExtendedPropertyLevel{
				Level0Type: "SCHEMA", Level0Name: schema,
				Level1Type: "TABLE", Level1Name: table,
				Level2Type: "STATISTICS", Level2Name: name,
			}
			props, err := d.ExtendedPropertiesContext(ctx, level)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildExtendedPropertiesForm(sc, dbName, level, props)
			return f, apply, nil
		},
	}
}
