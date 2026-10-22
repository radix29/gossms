package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
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
// type_desc for a type gosmo carries through verbatim (NONCLUSTERED HASH on a
// memory-optimized table, or a type a newer SQL Server adds); a bare map lookup
// drew an empty row for those.
func indexTypeName(t gosmo.IndexType) string {
	if s, ok := indexTypeNames[t]; ok {
		return s
	}
	return string(t)
}

// indexDataCompressionOptions is the Options page's Data compression dropdown:
// NONE/ROW/PAGE, the three values every edition supports on a rowstore index.
// A columnstore index reports COLUMNSTORE or COLUMNSTORE_ARCHIVE, kept out of
// the editable set; see dataCompressionRow.
var indexDataCompressionOptions = []string{"NONE", "ROW", "PAGE"}

// dataCompressionRow builds the Data compression row for an index Options page
// from the server's data_compression_desc. It returns the row to lay out plus
// the editable Select, nil when current isn't one of the three editable values.
//
// indexOf's not-found 0 would render a columnstore index as "NONE", itself a
// real compression setting read as fact. Showing the server's own text
// read-only is the honest form.
func dataCompressionRow(current string) (propsheet.Row, *propsheet.SelectRow) {
	if i, ok := indexOfOK(indexDataCompressionOptions, current); ok {
		row := propsheet.Select("Data compression", indexDataCompressionOptions, i)
		return row, row
	}
	return propsheet.Static("Data compression", current+" (not editable here)"), nil
}

// rebuildOptions groups the three index options SQL Server only honours after a
// rebuild — fill factor, pad index and data compression — for the two pages
// that offer them, Index Properties > Options and Key Properties > Options.
type rebuildOptions struct {
	fillFactor *propsheet.TextRow
	pad        *propsheet.CheckRow

	// compression is nil when the server reports a compression outside the
	// editable set; layout is then a Static saying so and there is nothing to
	// rebuild from, but the rest of the page still applies.
	compression *propsheet.SelectRow
	layout      propsheet.Row
}

func newRebuildOptions(idx *gosmo.Index) rebuildOptions {
	layout, compression := dataCompressionRow(string(idx.DataCompression()))
	return rebuildOptions{
		fillFactor:  propsheet.Int("Fill factor", int64(idx.FillFactor), 0, 100, "%"),
		pad:         propsheet.Check("Pad index", idx.IsPadded),
		compression: compression,
		layout:      layout,
	}
}

// compressionRows are the trailing rows both pages draw, including the note
// saying why Apply issues a rebuild of its own.
func (r rebuildOptions) compressionRows() []propsheet.Row {
	return []propsheet.Row{
		propsheet.Section("Compression"),
		r.layout,
		propsheet.Note("Fill factor, pad index, and data compression only take effect after a rebuild — Apply issues one automatically when any of these three change."),
	}
}

// apply rebuilds the index if any of the three changed, and issues nothing
// otherwise.
func (r rebuildOptions) apply(ctx context.Context, idx *gosmo.Index) error {
	compressionDirty := r.compression != nil && r.compression.Dirty()
	if !r.fillFactor.Dirty() && !r.pad.Dirty() && !compressionDirty {
		return nil
	}
	fillFactor, err := r.fillFactor.IntValue()
	if err != nil {
		return err
	}
	var compression gosmo.DataCompression
	if compressionDirty {
		compression = gosmo.DataCompression(r.compression.Value())
	}
	opts := gosmo.IndexRebuildOptions{FillFactor: int(fillFactor), DataCompression: compression}
	// Only when edited: a rebuild for a fill-factor or compression edit has no
	// reason to restate it.
	if r.pad.Dirty() {
		opts.PadIndex = new(r.pad.Checked())
	}
	return idx.Rebuild(ctx, opts)
}

// rowstoreOptionsRefusal names why idx takes none of the Options pages' rows,
// "" when it takes them all. SQL Server refuses every one on a columnstore
// index (Msgs 35327, 35328; NONE/ROW/PAGE compression 10799), and any ALTER
// INDEX at all on a memory-optimized table's index (a hash index always is
// one). gosmo refuses the same before sending; the page says so before the
// user edits anything.
func rowstoreOptionsRefusal(idx *gosmo.Index) string {
	switch {
	case idx.Type == gosmo.IndexTypeNonClusteredHash || (idx.Table() != nil && idx.Table().IsMemoryOptimized):
		return "memory-optimized table"
	case idx.Type.IsColumnStore():
		return "columnstore"
	}
	return ""
}

// refusedOptionsForm is an Options page for an index rowstoreOptionsRefusal
// refuses: each row the page would offer, shown "n/a", with the reason. withIgnoreDup
// is false for Key Properties, whose page has no such row.
func refusedOptionsForm(idx *gosmo.Index, section, refusal string, withIgnoreDup bool) *propsheet.Form {
	na := "n/a (" + refusal + ")"
	labels := []string{"Fill factor", "Pad index"}
	if withIgnoreDup {
		labels = append(labels, "Ignore duplicate keys")
	}
	labels = append(labels, "Allow row locks", "Allow page locks")
	rows := []propsheet.Row{propsheet.Section(section)}
	for _, l := range labels {
		rows = append(rows, propsheet.Static(l, na))
	}
	why := "SQL Server refuses fill factor, pad index, ignore duplicate keys, the lock options and NONE/ROW/PAGE compression on a columnstore index."
	if refusal != "columnstore" {
		why = "SQL Server supports no ALTER INDEX on a memory-optimized table: these options are fixed when the table is created."
	}
	return propsheet.NewForm(append(rows,
		propsheet.Section("Compression"),
		propsheet.Static("Data compression", orDefault(string(idx.DataCompression()), "NONE")),
		propsheet.Note(why),
	)...)
}

// applySetOptions issues one ALTER INDEX ... SET when any row changed,
// restating every row at its current value. A nil ignoreDup (the
// constraint-backing index, whose page has no such row) leaves IGNORE_DUP_KEY
// off the statement, since SQL Server refuses the option there even unchanged.
func applySetOptions(ctx context.Context, idx *gosmo.Index, ignoreDup, rowLocks, pageLocks *propsheet.CheckRow) error {
	dirty := rowLocks.Dirty() || pageLocks.Dirty() || (ignoreDup != nil && ignoreDup.Dirty())
	if !dirty {
		return nil
	}
	opts := gosmo.IndexSetOptions{
		AllowRowLocks:  new(rowLocks.Checked()),
		AllowPageLocks: new(pageLocks.Checked()),
	}
	if ignoreDup != nil {
		opts.IgnoreDupKey = new(ignoreDup.Checked())
	}
	return idx.SetOptions(ctx, opts)
}

// indexPropPages builds the page set for Index Properties. No Permissions page:
// an index isn't a securable class, so GRANT/DENY/REVOKE against one isn't
// valid T-SQL. Sort in tempdb, Online index operation, Resumable and Max
// duration are left out too: REBUILD-time-only clauses with no persisted state
// to show, unlike fill factor, pad index and the SET-able options here.
func indexPropPages(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string, onView bool) []propPage {
	w := gate.ObjectWriteRights()
	return []propPage{
		pageIndexGeneral(sc, dbName, schema, table, name),
		withRequiresOn(pageIndexOptions(sc, dbName, schema, table, name), dbName, schema, table, w...),
		pageIndexStorage(sc, dbName, schema, table, &name),
		withRequiresOn(pageIndexIncludedColumns(sc, dbName, schema, table, name), dbName, schema, table, w...),
		pageIndexFilter(d, sc, dbName, schema, table, name),
		pageIndexFragmentation(d, sc, dbName, schema, table, &name),
		withRequiresOn(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{
				Level0Type: "SCHEMA", Level0Name: schema,
				Level1Type: extPropObjectLevel(onView), Level1Name: table,
				Level2Type: "INDEX", Level2Name: name,
			}
		}), dbName, schema, table, w...),
	}
}

// indexedObject is what an index or a statistic is on: a *gosmo.Table, or an
// indexed *gosmo.View. Both answer every read and write the index and
// statistics pages and dialogs make; the rest (XML indexes, a filter
// predicate's checks) is a table's alone.
type indexedObject interface {
	Database() *gosmo.Database
	FullName() string
	Columns(context.Context) ([]*gosmo.Column, error)
	Indexes(context.Context) ([]*gosmo.Index, error)
	IndexByName(context.Context, string) (*gosmo.Index, error)
	CreateIndex(context.Context, gosmo.CreateIndexRequest) (*gosmo.Index, error)
	Statistics(context.Context) ([]*gosmo.Statistic, error)
	StatisticByName(context.Context, string) (*gosmo.Statistic, error)
	CreateStatistic(context.Context, gosmo.CreateStatisticRequest) (*gosmo.Statistic, error)
}

// findIndexed resolves dbName/schema/name to the table or view an index or a
// statistic is on. Tables and views share one namespace, so a name that is
// no table is read as a view: one read for a table, two for a view. The
// pages and dialogs are handed names, not a node, which is why they resolve
// this way rather than by nodeData.OnView; the tree's loaders, which have the
// node, use indexedFor.
func findIndexed(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (indexedObject, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	t, err := d.TableByName(ctx, schema, name)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, gosmo.ErrNotFound) {
		return nil, err
	}
	v, err := d.ViewByName(ctx, schema, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, fmt.Errorf("no table or view %s in %q: %w", gosmo.QualifiedName(schema, name), dbName, err)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// indexedOwner is what idx or st is on, as an indexedObject: its view, or its
// table.
func indexedOwner(view *gosmo.View, table *gosmo.Table) indexedObject {
	if view != nil {
		return view
	}
	return table
}

// indexedIdentity is the schema, name and kind ("Table" or "View") of o, for
// the "Table or view" rows of Index and Statistics Properties.
func indexedIdentity(o indexedObject) (schema, name, kind string) {
	if v, ok := o.(*gosmo.View); ok {
		return v.Schema, v.Name, "View"
	}
	t := o.(*gosmo.Table)
	return t.Schema, t.Name, "Table"
}

// extPropObjectLevel is the level-1 type an index's or statistic's extended
// properties are addressed under: the object it is on.
func extPropObjectLevel(onView bool) string {
	if onView {
		return "VIEW"
	}
	return "TABLE"
}

// findIndex resolves dbName/schema/table/name to the *gosmo.Index, read in
// full; table may name a view (findIndexed). The index carries its object
// (Index.Table or Index.View), which every write and read on it names
// itself.
func findIndex(ctx context.Context, sc *db.ServerConn, dbName, schema, table, name string) (*gosmo.Index, error) {
	o, err := findIndexed(ctx, sc, dbName, schema, table)
	if err != nil {
		return nil, err
	}
	return o.IndexByName(ctx, name)
}

func pageIndexGeneral(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}

			grid := indexKeyColumnGrid(idx)
			ownerSchema, ownerName, ownerKind := indexedIdentity(indexedOwner(idx.View(), idx.Table()))

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
				propsheet.Static("Hypothetical", boolStr(idx.IsHypothetical)),
				propsheet.Section("Table or view"),
				propsheet.Static("Schema", ownerSchema),
				propsheet.Static("Object", ownerName),
				propsheet.Static("Object type", ownerKind),
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
			idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}

			// A PK/unique-constraint-backing index rejects IGNORE_DUP_KEY outright, even to
			// re-set its current value ("Cannot use index option ignore_dup_key to alter
			// index '...' as it enforces a primary or unique constraint"; see
			// gosmo.IndexSetOptions). Such an index gets no row, and applySetOptions leaves
			// IgnoreDupKey nil so the option is never sent; Key Properties' Options page
			// does the same.
			constrained := idx.IsPrimaryKey || idx.IsUniqueConstraint
			if refusal := rowstoreOptionsRefusal(idx); refusal != "" {
				return refusedOptionsForm(idx, "Index options", refusal, !constrained), nil, nil
			}

			rebuild := newRebuildOptions(idx)
			rowLocksRow := propsheet.Check("Allow row locks", idx.AllowRowLocks)
			pageLocksRow := propsheet.Check("Allow page locks", idx.AllowPageLocks)

			rows := []propsheet.Row{propsheet.Section("Index options"), rebuild.fillFactor, rebuild.pad}
			var ignoreDupRow *propsheet.CheckRow
			if constrained {
				rows = append(rows, propsheet.Static("Ignore duplicate keys", "n/a (enforces a primary or unique constraint)"))
			} else {
				ignoreDupRow = propsheet.Check("Ignore duplicate keys", idx.IgnoreDupKey)
				rows = append(rows, ignoreDupRow)
			}
			rows = append(rows, rowLocksRow, pageLocksRow,
				propsheet.Static("Incremental statistics", boolStr(idx.StatisticsIncremental)))
			rows = append(rows, rebuild.compressionRows()...)
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				idx, err := findIndex(ctx, sc, dbName, schema, table, name)
				if err != nil {
					return err
				}
				if err := applySetOptions(ctx, idx, ignoreDupRow, rowLocksRow, pageLocksRow); err != nil {
					return err
				}
				return rebuild.apply(ctx, idx)
			}
			return f, apply, nil
		},
	}
}

// name is a *string (not the plain string other Index Properties pages take) so
// Key Properties, which shares this page, can point it at a name that
// pageKeyGeneral's rename apply updates in place; otherwise rename-then-Apply
// would leave this page's next reload looking up a name that no longer exists.
func pageIndexStorage(sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title: "Storage",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}
			info, err := idx.StorageInfo(ctx)
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

// pageIndexIncludedColumns lists every non-key table column with a toggle for
// whether it's in this index's INCLUDE list (SSMS's Index Properties > Included
// Columns). Applying reissues the whole index via DROP_EXISTING (see
// Index.SetIncludedColumns): included columns aren't a plain ALTER.
//
// Only a rowstore nonclustered index backing no constraint has a changeable
// INCLUDE list; for any other the page says why and offers nothing (SSMS greys it
// out). The page is still listed, since the index type isn't known until the load
// reads it.
func pageIndexIncludedColumns(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Included Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			if err := idx.IncludedColumnsSupported(); err != nil {
				return propsheet.NewForm(
					propsheet.Section("Non-key columns included in the index"),
					propsheet.Note("Included columns can't be changed on this index: "+err.Error()+"."),
				), nil, nil
			}
			cols, err := indexedOwner(idx.View(), idx.Table()).Columns(ctx)
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
				text[i] = []string{c.Name, c.TypeString()}
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
				idx, err := findIndex(ctx, sc, dbName, schema, table, name)
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
				return idx.SetIncludedColumns(ctx, newIncluded)
			}
			return f, apply, nil
		},
	}
}

// pageIndexFilter is Index Properties' Filter page, read-only since SQL Server
// accepts a filtered index's predicate only at CREATE time; changing one means
// dropping and recreating. Shared with Statistics Properties' Filter page via
// buildFilterInfoForm.
func pageIndexFilter(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "Filter",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}
			f := buildFilterInfoForm(d, whereTargetOf(idx.Table()), idx.FilterDefinition != "", idx.FilterDefinition)
			return f, nil, nil
		},
	}
}

// pageIndexFragmentation is Index Properties' Fragmentation page: current
// fragmentation/page-density (sys.dm_db_index_physical_stats, SAMPLED), a
// recommendation using Microsoft's documented thresholds, and
// Rebuild/Reorganize/Update Statistics actions that run immediately,
// independent of OK/Cancel/Apply. F5 re-reads the numbers. name is *string; see
// pageIndexStorage.
func pageIndexFragmentation(d *PropDialog, sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title: "Fragmentation",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}
			frag, err := idx.Fragmentation(ctx, gosmo.FragmentationSampled)
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
				if err := idx.Rebuild(ctx, gosmo.IndexRebuildOptions{}); err != nil {
					return "", err
				}
				return "Rebuild complete", nil
			})
			reorgBtn := d.asyncStatusButton("Reorganize", statusRow, "Reorganizing...", func(ctx context.Context) (string, error) {
				if err := idx.Reorganize(ctx); err != nil {
					return "", err
				}
				return "Reorganize complete", nil
			})
			updateStatsBtn := d.asyncStatusButton("Update Statistics", statusRow, "Updating statistics...", func(ctx context.Context) (string, error) {
				if err := idx.UpdateStatistics(ctx, 0); err != nil {
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
