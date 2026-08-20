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
)

// indexTypeNames renders a gosmo.IndexType the way SSMS's Index Properties
// General page does.
var indexTypeNames = map[gosmo.IndexType]string{
	gosmo.IndexTypeClustered:            "Clustered",
	gosmo.IndexTypeNonClustered:         "Nonclustered",
	gosmo.IndexTypeXML:                  "XML",
	gosmo.IndexTypeSpatial:              "Spatial",
	gosmo.IndexTypeColumnStore:          "Nonclustered columnstore",
	gosmo.IndexTypeClusteredColumnStore: "Clustered columnstore",
}

// indexTypeName renders t for display, falling back to the server's own
// type_desc for a type gosmo carries through verbatim — NONCLUSTERED HASH on
// a memory-optimized table, or a type a newer SQL Server adds. The bare map
// lookup drew an empty row for those.
func indexTypeName(t gosmo.IndexType) string {
	if s, ok := indexTypeNames[t]; ok {
		return s
	}
	return string(t)
}

// indexDataCompressionOptions is the Options page's Data compression
// dropdown — NONE/ROW/PAGE, the three values every SQL Server edition
// supports on a rowstore index. A columnstore index reports COLUMNSTORE or
// COLUMNSTORE_ARCHIVE, which stay out of the editable set; see
// dataCompressionRow for how such a value is shown instead.
var indexDataCompressionOptions = []string{"NONE", "ROW", "PAGE"}

// dataCompressionRow builds the Data compression row for an index Options
// page from the server's data_compression_desc. It returns the row to lay out
// plus the editable Select, which is nil when current isn't one of the three
// editable values.
//
// Selecting into the dropdown with indexOf's not-found 0 rendered a
// columnstore index's COLUMNSTORE as "NONE" — itself a real compression
// setting, so it read as fact rather than as a missing value. Showing the
// server's own text read-only is the honest form.
func dataCompressionRow(current string) (propsheet.Row, *propsheet.SelectRow) {
	if i, ok := indexOfOK(indexDataCompressionOptions, current); ok {
		row := propsheet.Select("Data compression", indexDataCompressionOptions, i)
		return row, row
	}
	return propsheet.Static("Data compression", current+" (not editable here)"), nil
}

// indexPropPages builds the page set for Index Properties. There's no
// Permissions page: an index isn't a SQL Server securable class, so
// GRANT/DENY/REVOKE against one isn't valid T-SQL. Sort in tempdb, Online
// index operation, Resumable, and Max duration are left out too — they're
// REBUILD-time-only clauses with no persisted state to show afterward,
// unlike fill factor, pad index, and the SET-able options this page has.
func indexPropPages(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) []propPage {
	return []propPage{
		pageIndexGeneral(sc, dbName, schema, table, name),
		pageIndexOptions(sc, dbName, schema, table, name),
		pageIndexStorage(sc, dbName, schema, table, &name),
		pageIndexIncludedColumns(sc, dbName, schema, table, name),
		pageIndexFilter(d, sc, dbName, schema, table, name),
		pageIndexFragmentation(d, sc, dbName, schema, table, &name),
		pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{
				Level0Type: "SCHEMA", Level0Name: schema,
				Level1Type: "TABLE", Level1Name: table,
				Level2Type: "INDEX", Level2Name: name,
			}
		}),
	}
}

// findIndex resolves dbName/schema/table/name to the owning *gosmo.Table
// and its *gosmo.Index. Both are needed: the pages script and alter through
// the table, not the index.
func findIndex(ctx context.Context, sc *db.ServerConn, dbName, schema, table, name string) (*gosmo.Table, *gosmo.Index, error) {
	t, err := findTable(ctx, sc, dbName, schema, table)
	if err != nil {
		return nil, nil, err
	}
	idx, err := t.IndexByNameContext(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	return t, idx, nil
}

func pageIndexGeneral(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(idx.KeyColumns))
			for i, c := range idx.KeyColumns {
				order := "Ascending"
				if c.Descending {
					order = "Descending"
				}
				rows[i] = []string{strconv.Itoa(i + 1), c.Name, order}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Ord", "Column name", "Sort order"}, rows)
			grid.SetCellCursor(true)

			included := "(none)"
			if len(idx.IncludedColumns) > 0 {
				names := make([]string, len(idx.IncludedColumns))
				for i, c := range idx.IncludedColumns {
					names[i] = c.Name
				}
				included = strings.Join(names, ", ")
			}

			f := propsheet.NewForm(
				propsheet.Section("Index identity"),
				propsheet.Static("Index name", idx.Name),
				propsheet.Static("Index type", indexTypeName(idx.Type)),
				propsheet.Static("Unique", boolStr(idx.IsUnique)),
				propsheet.Static("Disabled", boolStr(idx.IsDisabled)),
				propsheet.Section("Table or view"),
				propsheet.Static("Schema", t.Schema),
				propsheet.Static("Object", t.Name),
				propsheet.Static("Object type", "Table"),
				propsheet.Section("Key columns"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Section("Included columns"),
				propsheet.Static("Columns", included),
			)
			return f, nil, nil
		},
	}
}

func pageIndexOptions(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Options",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			_, idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}

			// A PK/unique-constraint-backing index rejects IGNORE_DUP_KEY
			// outright, even to re-set its current value ("Cannot use index
			// option ignore_dup_key to alter index '...' as it enforces a
			// primary or unique constraint") — the same restriction
			// Index.SetLockOptions's doc comment covers, and why Key
			// Properties' pageKeyGeneral uses SetLockOptions, not
			// SetOptions.
			constrained := idx.IsPrimaryKey || idx.IsUniqueConstraint

			fillFactorRow := propsheet.Int("Fill factor", int64(idx.FillFactor), 0, 100, "%")
			padRow := propsheet.Check("Pad index", idx.IsPadded)
			rowLocksRow := propsheet.Check("Allow row locks", idx.AllowRowLocks)
			pageLocksRow := propsheet.Check("Allow page locks", idx.AllowPageLocks)
			compressionLayoutRow, compressionRow := dataCompressionRow(idx.DataCompression)

			rows := []propsheet.Row{propsheet.Section("Index options"), fillFactorRow, padRow}
			var ignoreDupRow *propsheet.CheckRow
			if constrained {
				rows = append(rows, propsheet.Static("Ignore duplicate keys", "n/a (enforces a primary or unique constraint)"))
			} else {
				ignoreDupRow = propsheet.Check("Ignore duplicate keys", idx.IgnoreDupKey)
				rows = append(rows, ignoreDupRow)
			}
			rows = append(rows, rowLocksRow, pageLocksRow,
				propsheet.Section("Compression"),
				compressionLayoutRow,
				propsheet.Note("Fill factor, pad index, and data compression only take effect after a rebuild — Apply issues one automatically when any of these three change."),
			)
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				t, idx, err := findIndex(ctx, sc, dbName, schema, table, name)
				if err != nil {
					return err
				}
				if ignoreDupRow != nil && (ignoreDupRow.Dirty() || rowLocksRow.Dirty() || pageLocksRow.Dirty()) {
					if err := idx.SetOptionsContext(ctx, t, ignoreDupRow.Checked(), rowLocksRow.Checked(), pageLocksRow.Checked()); err != nil {
						return err
					}
				} else if ignoreDupRow == nil && (rowLocksRow.Dirty() || pageLocksRow.Dirty()) {
					if err := idx.SetLockOptionsContext(ctx, t, rowLocksRow.Checked(), pageLocksRow.Checked()); err != nil {
						return err
					}
				}
				// compressionRow is nil when the server reports a compression
				// outside the editable set, so there is nothing to rebuild
				// from — the rest of the page still applies.
				compressionDirty := compressionRow != nil && compressionRow.Dirty()
				if fillFactorRow.Dirty() || padRow.Dirty() || compressionDirty {
					fillFactor, err := fillFactorRow.IntValue()
					if err != nil {
						return err
					}
					compression := ""
					if compressionDirty {
						compression = compressionRow.Value()
					}
					if err := idx.RebuildWithOptionsContext(ctx, t, int(fillFactor), padRow.Checked(), compression); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// name is a *string (rather than the plain string every other Index
// Properties page takes) so Key Properties, which shares this page, can
// point it at a name that pageKeyGeneral's rename apply updates in place —
// otherwise a rename-then-Apply would leave this page's next reload
// looking up an index name that no longer exists.
func pageIndexStorage(sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title: "Storage",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}
			info, err := idx.StorageInfoContext(ctx, t)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(info.Allocations))
			for i, a := range info.Allocations {
				rows[i] = []string{a.Type, strconv.FormatInt(a.Pages, 10), strconv.FormatInt(a.UsedKB/1024, 10)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Type", "Pages", "Used MB"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Filegroup and partitioning"),
				propsheet.Static("Filegroup", orDefault(info.FileGroup, "<none>")),
				propsheet.Static("Partition scheme", orDefault(info.PartitionScheme, "<none>")),
				propsheet.Static("Partition column", orDefault(info.PartitionColumn, "<not set>")),
				propsheet.Section("Space allocation"),
				propsheet.Static("Used space (MB)", strconv.FormatInt(info.UsedKB/1024, 10)),
				propsheet.Static("Reserved space (MB)", strconv.FormatInt(info.ReservedKB/1024, 10)),
				propsheet.Static("Row count", strconv.FormatInt(info.RowCount, 10)),
				propsheet.Static("Average record size", fmt.Sprintf("%.1f bytes", info.AvgRecordSize)),
				propsheet.Section("Allocation units"),
				propsheet.NewGridRow(grid, 6),
			)
			return f, nil, nil
		},
	}
}

// pageIndexIncludedColumns lists every non-key table column with a toggle
// for whether it's part of this index's INCLUDE list — SSMS's Index
// Properties > Included Columns page. Applying a change reissues the whole
// index via DROP_EXISTING (see Index.SetIncludedColumns): included columns
// aren't a plain ALTER, so this is the only correct way to change them on
// an existing index.
func pageIndexIncludedColumns(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Included Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			cols, err := t.ColumnsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			keySet := make(map[string]bool, len(idx.KeyColumns))
			for _, c := range idx.KeyColumns {
				keySet[c.Name] = true
			}
			includedSet := make(map[string]bool, len(idx.IncludedColumns))
			for _, c := range idx.IncludedColumns {
				includedSet[c.Name] = true
			}

			var eligible []*gosmo.Column
			for _, c := range cols {
				if !keySet[c.Name] {
					eligible = append(eligible, c)
				}
			}

			text := make([][]string, len(eligible))
			values := make([][]bool, len(eligible))
			for i, c := range eligible {
				text[i] = []string{c.Name, gosmo.ColumnTypeString(c)}
				values[i] = []bool{includedSet[c.Name]}
			}
			grid := propsheet.NewToggleGrid([]string{"Inc", "Column name", "Data type"}, []int{0}, 10)
			grid.SetRows(text, values)

			f := propsheet.NewForm(
				propsheet.Section("Non-key columns included in the index"),
				grid,
				propsheet.Note("Space/Enter (or click) toggles whether a column is included. Key columns aren't shown here — they're fixed when the index is created."),
			)

			apply := func(ctx context.Context) error {
				if !grid.Dirty() {
					return nil
				}
				t, idx, err := findIndex(ctx, sc, dbName, schema, table, name)
				if err != nil {
					return err
				}
				var newIncluded []string
				vals := grid.Values()
				for i, c := range eligible {
					if vals[i][0] {
						newIncluded = append(newIncluded, c.Name)
					}
				}
				return idx.SetIncludedColumnsContext(ctx, t, newIncluded)
			}
			return f, apply, nil
		},
	}
}

// pageIndexFilter is Index Properties' Filter page — read-only, since SQL
// Server only accepts a filtered index's predicate at CREATE time; changing
// one on an existing index means dropping and recreating it. Shared with
// Statistics Properties' own Filter page via buildFilterInfoForm.
func pageIndexFilter(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Filter",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			f := buildFilterInfoForm(d, t, idx.FilterDefinition != "", idx.FilterDefinition)
			return f, nil, nil
		},
	}
}

// pageIndexFragmentation is Index Properties' Fragmentation page: current
// fragmentation/page-density (sys.dm_db_index_physical_stats, SAMPLED), a
// recommendation using Microsoft's own documented thresholds, and
// Refresh/Reorganize/Rebuild/Update Statistics actions that run
// immediately, independent of OK/Cancel/Apply.
// name is *string — see pageIndexStorage's doc comment.
func pageIndexFragmentation(d *PropDialog, sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title: "Fragmentation",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}
			frag, err := idx.FragmentationContext(ctx, t, "SAMPLED")
			if err != nil {
				return nil, nil, err
			}

			recommendation, reason := "None", "Fragmentation is low"
			switch {
			case frag.AvgFragmentationPct > 30:
				recommendation, reason = "Rebuild", "Fragmentation > 30%"
			case frag.AvgFragmentationPct > 10:
				recommendation, reason = "Reorganize", "Fragmentation > 10%"
			}

			statusRow := propsheet.Static("Last action", "")
			rebuildBtn := d.asyncStatusButton("Rebuild", statusRow, "Rebuilding...", func(ctx context.Context) (string, error) {
				if err := idx.RebuildContext(ctx, t, 0); err != nil {
					return "", err
				}
				return "Rebuild complete", nil
			})
			reorgBtn := d.asyncStatusButton("Reorganize", statusRow, "Reorganizing...", func(ctx context.Context) (string, error) {
				if err := idx.ReorganizeContext(ctx, t); err != nil {
					return "", err
				}
				return "Reorganize complete", nil
			})
			updateStatsBtn := d.asyncStatusButton("Update Statistics", statusRow, "Updating statistics...", func(ctx context.Context) (string, error) {
				if err := idx.UpdateStatisticsContext(ctx, t); err != nil {
					return "", err
				}
				return "Statistics updated", nil
			})

			f := propsheet.NewForm(
				propsheet.Section("Current fragmentation"),
				propsheet.Static("Avg fragmentation", fmt.Sprintf("%.1f %%", frag.AvgFragmentationPct)),
				propsheet.Static("Avg page density", fmt.Sprintf("%.1f %%", frag.AvgPageSpaceUsedPct)),
				propsheet.Static("Fragment count", strconv.FormatInt(frag.FragmentCount, 10)),
				propsheet.Static("Page count", strconv.FormatInt(frag.PageCount, 10)),
				propsheet.Section("Maintenance recommendation"),
				propsheet.Static("Recommendation", recommendation),
				propsheet.Static("Reason", reason),
				propsheet.Section("Actions"),
				propsheet.Buttons(rebuildBtn, reorgBtn, updateStatsBtn),
				statusRow,
				propsheet.Note("These actions run immediately, independent of OK/Cancel/Apply. Press F5 to refresh this page's numbers afterward."),
			)
			return f, nil, nil
		},
	}
}
