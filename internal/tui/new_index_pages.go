package tui

import (
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// new_index_pages.go builds New Index's pages. Which pages exist is
// nidxPagesFor's answer (new_index_dialog.go); this file builds each one, and
// every widget it creates is recorded in nidxRows so request() can read the
// whole form back without threading a dozen values through.

// nidxRows holds the widgets the pages built. A row belonging to a page this
// index type doesn't have is nil, so request() nil-guards every read.
type nidxRows struct {
	name   *propsheet.TextRow
	unique *propsheet.CheckRow

	// commitKeyColumn writes the Sort order dropdown back onto the selected
	// key column. The grid commits on every selection change, but the last
	// edit before OK has had no selection change after it — without this,
	// setting a column to Descending and pressing OK created it ascending.
	commitKeyColumn func()

	// singleColumn is the one column an XML or spatial index is on — those
	// take exactly one, so they get a dropdown instead of the key-column
	// grid. Nil when the table has no column of the matching type.
	singleColumn *propsheet.SelectRow

	// included is the Included Columns toggle grid, and includedNames its
	// rows in grid order.
	included      *propsheet.ToggleGridRow
	includedNames []string

	filter *propsheet.TextRow

	fillFactor       *propsheet.TextRow
	padIndex         *propsheet.CheckRow
	online           *propsheet.CheckRow
	sortInTempDB     *propsheet.CheckRow
	dropExisting     *propsheet.CheckRow
	compression      *propsheet.SelectRow
	compressionDelay *propsheet.TextRow

	fileGroup       *propsheet.SelectRow
	partitionScheme *propsheet.SelectRow
	partitionColumn *propsheet.SelectRow

	xmlPrimary       *propsheet.RadioRow
	xmlParent        *propsheet.SelectRow
	xmlSecondaryType *propsheet.SelectRow

	tessellation   *propsheet.SelectRow
	boundingBox    [4]*propsheet.TextRow
	gridLevels     [4]*propsheet.SelectRow
	cellsPerObject *propsheet.TextRow
}

// includedColumns is the ticked non-key columns. A column that is also a key
// column is dropped rather than sent: CREATE INDEX rejects the duplicate, and
// the two lists are edited on different pages, so the overlap is easy to
// produce and impossible to see.
func (r nidxRows) includedColumns(keys []gosmo.IndexColumnDef) []string {
	var out []string
	for i, v := range r.included.Values() {
		if !v[0] {
			continue
		}
		name := r.includedNames[i]
		if slices.ContainsFunc(keys, func(k gosmo.IndexColumnDef) bool { return k.Name == name }) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// nidxSortOrders is the key column grid's sort order, in the order ASC/DESC
// map onto: index 1 is Descending, which is what request() reads back.
var nidxSortOrders = []string{"Ascending", "Descending"}

// nidxCompressionItems is the Data compression dropdown per index family.
// The leading "(default)" is what request() treats as "say nothing", so the
// server keeps its own default rather than being told NONE.
var (
	nidxRowstoreCompression    = []string{"(default)", "NONE", "ROW", "PAGE"}
	nidxColumnstoreCompression = []string{"(default)", "COLUMNSTORE", "COLUMNSTORE_ARCHIVE"}
)

// nidxTessellations is the spatial USING clause's four schemes, and
// nidxGridDensities one grid level's choices.
var (
	nidxTessellations = []string{
		string(gosmo.SpatialGeometryGrid), string(gosmo.SpatialGeometryAutoGrid),
		string(gosmo.SpatialGeographyGrid), string(gosmo.SpatialGeographyAutoGrid),
	}
	nidxGridDensities     = []string{"(default)", "LOW", "MEDIUM", "HIGH"}
	nidxBoundingBoxLabels = [4]string{"Bounding box X-min", "Bounding box Y-min",
		"Bounding box X-max", "Bounding box Y-max"}
	nidxBoundingBoxDefaults = [4]string{"-180", "-90", "180", "90"}
)

// nidxSecondaryXMLTypes is the FOR clause of a secondary XML index.
var nidxSecondaryXMLTypes = []string{
	string(gosmo.XMLSecondaryPath), string(gosmo.XMLSecondaryValue), string(gosmo.XMLSecondaryProperty),
}

// nidxNeedsOneColumn reports whether typ indexes exactly one column of a
// particular data type, rather than a key column list.
func nidxNeedsOneColumn(typ gosmo.IndexType) bool {
	return typ == gosmo.IndexTypeXML || typ == gosmo.IndexTypeSpatial
}

// nidxColumnKind names the column type such an index needs, for the message
// that explains why the table has none.
func nidxColumnKind(typ gosmo.IndexType) string {
	if typ == gosmo.IndexTypeXML {
		return "xml"
	}
	return "geometry or geography"
}

// nidxEligibleColumns is the columns typ can be created on. XML and spatial
// indexes take their own data types and nothing else; a key column list takes
// anything but those and the deprecated large types, none of which SQL Server
// will index.
func nidxEligibleColumns(cols []*gosmo.Column, typ gosmo.IndexType) []string {
	var out []string
	for _, c := range cols {
		dt := strings.ToLower(string(c.DataType))
		switch {
		case typ == gosmo.IndexTypeXML:
			if dt == "xml" {
				out = append(out, c.Name)
			}
		case typ == gosmo.IndexTypeSpatial:
			if dt == "geometry" || dt == "geography" {
				out = append(out, c.Name)
			}
		default:
			switch dt {
			case "xml", "geometry", "geography", "text", "ntext", "image":
			default:
				out = append(out, c.Name)
			}
		}
	}
	return out
}

// nidxColumnNames is every column of the table, for the pickers that don't
// care about type (the partitioning column).
func nidxColumnNames(cols []*gosmo.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// nidxInt reads an Int row, treating an unparsable value as unset — the row
// has already refused anything but digits, so this is the empty field.
func nidxInt(r *propsheet.TextRow) int {
	n, err := r.IntValue()
	if err != nil {
		return 0
	}
	return int(n)
}

// nidxFloat reads a bounding-box coordinate; the preflight has already
// rejected one that doesn't parse.
func nidxFloat(r *propsheet.TextRow) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(r.Value()), 64)
	return v
}

// nidxDensity reads a grid level, mapping the leading "(default)" onto the
// empty density that omits the level from the GRIDS clause.
func nidxDensity(r *propsheet.SelectRow) gosmo.SpatialGridDensity {
	if r.Selected() == 0 {
		return ""
	}
	return gosmo.SpatialGridDensity(r.Value())
}

// nidxNamePrefixes is the name each type's suggested index name starts with,
// following the convention SSMS's own generated names use.
var nidxNamePrefixes = map[gosmo.IndexType]string{
	gosmo.IndexTypeClustered:            "CIX",
	gosmo.IndexTypeNonClustered:         "IX",
	gosmo.IndexTypeColumnStore:          "NCCI",
	gosmo.IndexTypeClusteredColumnStore: "CCI",
	gosmo.IndexTypeXML:                  "XMLIX",
	gosmo.IndexTypeSpatial:              "SIX",
}

func (d *NewIndexDialog) suggestedName() string {
	prefix, ok := nidxNamePrefixes[d.kind.typ]
	if !ok {
		prefix = "IX"
	}
	return prefix + "_" + d.table
}

// generalForm is the name, the type, and whatever stands for "which columns"
// for this type: a key column list, one dropdown, or nothing at all.
func (d *NewIndexDialog) generalForm(pf *nidxPrefetch) *propsheet.Form {
	d.rows.name = propsheet.Text("Index name", d.suggestedName(), 40)
	rows := []propsheet.Row{
		propsheet.Section("Index"),
		d.rows.name,
		propsheet.Static("Index type", indexTypeName(d.kind.typ)),
		propsheet.Static("Table", fqn(d.schema, d.table)),
	}

	switch {
	case d.kind.typ == gosmo.IndexTypeClusteredColumnStore:
		rows = append(rows, propsheet.Note("A clustered columnstore index covers every column of the table, so it has no key column list. The table can have no other clustered index."))

	case nidxNeedsOneColumn(d.kind.typ):
		cols := nidxEligibleColumns(pf.columns, d.kind.typ)
		rows = append(rows, propsheet.Section("Column"))
		if len(cols) == 0 {
			rows = append(rows, propsheet.Note("This table has no "+nidxColumnKind(d.kind.typ)+" column, so it cannot have an index of this type."))
			break
		}
		d.rows.singleColumn = propsheet.Select("Column", cols, 0)
		rows = append(rows, d.rows.singleColumn,
			propsheet.Note("This index type requires the table to have a clustered primary key."))

	default:
		if d.kind.typ != gosmo.IndexTypeColumnStore {
			d.rows.unique = propsheet.Check("Unique", false)
			rows = append(rows, d.rows.unique)
		}
		rows = append(rows, d.keyColumnRows(pf, d.kind.typ != gosmo.IndexTypeColumnStore)...)
	}
	return propsheet.NewForm(rows...)
}

// keyColumnRows is the key column list: a grid of what has been chosen, in
// index order, plus the controls that add to it, remove from it and reorder
// it. ordered adds the per-column sort order, which only a rowstore index
// has — ASC/DESC in a columnstore column list is a syntax error.
func (d *NewIndexDialog) keyColumnRows(pf *nidxPrefetch, ordered bool) []propsheet.Row {
	headers := []string{"Ord", "Column name"}
	if ordered {
		headers = append(headers, "Sort order")
	}
	rowsFor := func() [][]string {
		out := make([][]string, len(d.keyColumns))
		for i, c := range d.keyColumns {
			out[i] = []string{strconv.Itoa(i + 1), c.Name}
			if ordered {
				out[i] = append(out[i], nidxSortOrders[boolIdx(c.Descending)])
			}
		}
		return out
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	available := nidxEligibleColumns(pf.columns, d.kind.typ)
	addRow := propsheet.Select("Column to add", available, 0)
	sortRow := propsheet.Select("Sort order", nidxSortOrders, 0)
	hint := propsheet.Hint()

	current := -1
	commit := func() {
		if ordered && current >= 0 && current < len(d.keyColumns) {
			d.keyColumns[current].Descending = sortRow.Selected() == 1
		}
	}
	sync := func() {
		current = grid.SelectedRow()
		if ordered && current >= 0 && current < len(d.keyColumns) {
			sortRow.SetSelected(boolIdx(d.keyColumns[current].Descending))
		}
	}
	reload := wireGridEditor(grid, headers, rowsFor, commit, sync)
	d.rows.commitKeyColumn = commit

	// move slides the selected column one place through the list and keeps
	// the selection on it — the column the user is moving, not the position
	// they started from, is what they are still working with.
	move := func(delta int) {
		commit()
		to := current + delta
		if current < 0 || current >= len(d.keyColumns) || to < 0 || to >= len(d.keyColumns) {
			return
		}
		d.keyColumns[current], d.keyColumns[to] = d.keyColumns[to], d.keyColumns[current]
		reload()
		grid.SetSelectedCell(to, 0)
		sync()
	}

	addBtn := widgets.NewButton("Add", func() {
		commit()
		name := addRow.Value()
		if name == "" {
			return
		}
		if slices.ContainsFunc(d.keyColumns, func(k gosmo.IndexColumnDef) bool { return k.Name == name }) {
			hint.SetError(name + " is already a key column of this index.")
			return
		}
		hint.Clear()
		d.keyColumns = append(d.keyColumns, gosmo.IndexColumnDef{Name: name})
		reload()
	})
	removeBtn := widgets.NewButton("Remove", func() {
		commit()
		if current < 0 || current >= len(d.keyColumns) {
			hint.Set("Select a key column to remove.")
			return
		}
		hint.Clear()
		d.keyColumns = slices.Delete(d.keyColumns, current, current+1)
		current = -1
		reload()
	})
	upBtn := widgets.NewButton("Move Up", func() { move(-1) })
	downBtn := widgets.NewButton("Move Down", func() { move(1) })

	gridRow := propsheet.NewGridRow(grid, 6)
	// The grid mirrors a list the buttons own, so its dirty state is that
	// list's, and reverting restores the list rather than the rows.
	gridRow.DirtyFn = func() bool {
		commit()
		return len(d.keyColumns) > 0
	}
	gridRow.RevertFn = func() {
		d.keyColumns = nil
		current = -1
		reload()
	}

	rows := []propsheet.Row{
		propsheet.Section("Key columns"),
		gridRow,
		addRow,
		propsheet.Buttons(addBtn, removeBtn, upBtn, downBtn),
		hint,
	}
	if ordered {
		rows = append(rows, propsheet.Section("Selected key column"), sortRow)
	}
	if len(available) == 0 {
		rows = append(rows, propsheet.Note("This table has no column an index can be created on."))
	}
	return rows
}

// includedForm is the INCLUDE list — every column of the table with a
// toggle, since an included column has no order and no direction.
func (d *NewIndexDialog) includedForm(pf *nidxPrefetch) *propsheet.Form {
	grid := propsheet.NewToggleGrid([]string{"Column name", "Data type", "Included"}, []int{2}, 10)
	names := make([]string, 0, len(pf.columns))
	text := make([][]string, 0, len(pf.columns))
	values := make([][]bool, 0, len(pf.columns))
	for _, c := range pf.columns {
		names = append(names, c.Name)
		text = append(text, []string{c.Name, string(c.DataType)})
		values = append(values, []bool{false})
	}
	grid.SetRows(text, values)
	d.rows.included, d.rows.includedNames = grid, names

	return propsheet.NewForm(
		propsheet.Section("Included (non-key) columns"),
		grid,
		propsheet.Note("An included column is stored in the index's leaf level only: it makes a query covering without widening the key. A column that is already a key column is ignored here."),
	)
}

// optionsForm is the WITH clause. The rows differ per family because the
// options do — a columnstore index has no fill factor and a rowstore one no
// compression delay, and offering either would build a statement the server
// rejects.
func (d *NewIndexDialog) optionsForm() *propsheet.Form {
	rows := []propsheet.Row{propsheet.Section("Index options")}

	if !d.kind.typ.IsColumnStore() {
		d.rows.fillFactor = propsheet.Int("Fill factor", 0, 0, 100, "%")
		d.rows.padIndex = propsheet.Check("Pad index", false)
		rows = append(rows, d.rows.fillFactor, d.rows.padIndex)
	}
	d.rows.online = propsheet.Check("Allow online processing", false)
	d.rows.dropExisting = propsheet.Check("Drop existing index", false)
	rows = append(rows, d.rows.online, d.rows.dropExisting)

	if !d.kind.typ.IsColumnStore() {
		d.rows.sortInTempDB = propsheet.Check("Sort results in tempdb", false)
		rows = append(rows, d.rows.sortInTempDB)
	}

	// An XML index takes no DATA_COMPRESSION at all; the other families take
	// their own keywords, which are not interchangeable.
	if d.kind.typ != gosmo.IndexTypeXML {
		items := nidxRowstoreCompression
		if d.kind.typ.IsColumnStore() {
			items = nidxColumnstoreCompression
		}
		d.rows.compression = propsheet.Select("Data compression", items, 0)
		rows = append(rows, propsheet.Section("Storage options"), d.rows.compression)
	}
	if d.kind.typ.IsColumnStore() {
		d.rows.compressionDelay = propsheet.Int("Compression delay", 0, 0, 10080, "min")
		rows = append(rows, d.rows.compressionDelay,
			propsheet.Note("Compression delay holds a closed rowgroup in the delta store for that many minutes before compressing it, for a table whose rows are updated shortly after insert."))
	}
	note := "Online processing keeps the table available during the build and needs an edition that supports it."
	if d.rows.fillFactor != nil {
		note += " Fill factor 0 means the server's default."
	}
	return propsheet.NewForm(append(rows, propsheet.Note(note))...)
}

// filterForm is the WHERE clause of a filtered index.
func (d *NewIndexDialog) filterForm() *propsheet.Form {
	d.rows.filter = propsheet.Text("Filter predicate", "", 60)
	return propsheet.NewForm(
		propsheet.Section("Filter"),
		d.rows.filter,
		propsheet.Note("The predicate without the WHERE, e.g. [Status] = 'Open'. A filtered index covers only the rows that match, and a query is only served by it when the optimizer can prove its own predicate implies this one. Leave it empty for an unfiltered index."),
	)
}

// storageForm is the ON clause: a filegroup, or a partition scheme and the
// column it partitions by.
func (d *NewIndexDialog) storageForm(pf *nidxPrefetch) *propsheet.Form {
	d.rows.fileGroup = propsheet.Select("Filegroup", append([]string{"(default)"}, pf.fileGroups...), 0)
	d.rows.partitionScheme = propsheet.Select("Partition scheme", append([]string{"(none)"}, pf.partitionSchemes...), 0)
	d.rows.partitionColumn = propsheet.Select("Partitioning column", nidxColumnNames(pf.columns), 0)
	return propsheet.NewForm(
		propsheet.Section("Storage"),
		d.rows.fileGroup,
		propsheet.Section("Partitioning"),
		d.rows.partitionScheme,
		d.rows.partitionColumn,
		propsheet.Note("An index is created on a filegroup or on a partition scheme, never both — choosing a scheme here overrides the filegroup above. Aligning the index with the table's own scheme is what keeps partition switching possible."),
	)
}

// xmlForm chooses between the primary XML index and a secondary one built
// over it.
func (d *NewIndexDialog) xmlForm(pf *nidxPrefetch) *propsheet.Form {
	d.rows.xmlPrimary = propsheet.Radio("Index kind", []string{"Primary", "Secondary"}, 0)
	rows := []propsheet.Row{propsheet.Section("XML index"), d.rows.xmlPrimary}
	if len(pf.primaryXMLNames) > 0 {
		d.rows.xmlParent = propsheet.Select("Primary XML index", pf.primaryXMLNames, 0)
		d.rows.xmlSecondaryType = propsheet.Select("Secondary index type", nidxSecondaryXMLTypes, 0)
		rows = append(rows, d.rows.xmlParent, d.rows.xmlSecondaryType)
	} else {
		rows = append(rows, propsheet.Note("This table has no primary XML index yet, so only a primary one can be created here."))
	}
	return propsheet.NewForm(append(rows,
		propsheet.Note("A primary XML index shreds the column into a node table; PATH, VALUE and PROPERTY secondary indexes are built over it for path predicates, wildcard value lookups and property-bag retrieval respectively."),
	)...)
}

// spatialForm is the USING clause and its tessellation options.
func (d *NewIndexDialog) spatialForm() *propsheet.Form {
	d.rows.tessellation = propsheet.Select("Tessellation scheme", nidxTessellations, 0)
	rows := []propsheet.Row{propsheet.Section("Tessellation"), d.rows.tessellation}

	rows = append(rows, propsheet.Section("Bounding box (geometry only)"))
	for i, label := range nidxBoundingBoxLabels {
		d.rows.boundingBox[i] = propsheet.Text(label, nidxBoundingBoxDefaults[i], 20)
		rows = append(rows, d.rows.boundingBox[i])
	}

	rows = append(rows, propsheet.Section("Grid"))
	for i := range d.rows.gridLevels {
		d.rows.gridLevels[i] = propsheet.Select("Grid level "+strconv.Itoa(i+1), nidxGridDensities, 0)
		rows = append(rows, d.rows.gridLevels[i])
	}
	d.rows.cellsPerObject = propsheet.Int("Cells per object", 0, 0, 8192, "")
	rows = append(rows, d.rows.cellsPerObject,
		propsheet.Note("The bounding box applies to the two GEOMETRY_ schemes and is required there; a geography index tessellates the whole globe. Grid levels apply to GEOMETRY_GRID and GEOGRAPHY_GRID only — the AUTO_GRID schemes choose their own. Cells per object 0 means the server's default."))
	return propsheet.NewForm(rows...)
}
