package tui

import (
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// New Index's own risk is not the statement — gosmo pins that, and refuses
// the combinations SQL Server rejects. What only this layer decides is which
// rows each index type is offered, and that the request built from them says
// what the form says: the key column *order*, the sort direction on the
// column the user set it on, and the storage clause that overrides the other.

func nidxTestPrefetch() *nidxPrefetch {
	return &nidxPrefetch{
		columns: []*gosmo.Column{
			{Name: "OrderID", DataType: gosmo.DataType("int")},
			{Name: "Customer", DataType: gosmo.DataType("nvarchar")},
			{Name: "Placed", DataType: gosmo.DataType("datetime2")},
			{Name: "Doc", DataType: gosmo.DataType("xml")},
			{Name: "Where", DataType: gosmo.DataType("geometry")},
			{Name: "Notes", DataType: gosmo.DataType("ntext")},
		},
		existingNames:    map[string]bool{"ix_orders_existing": true},
		primaryXMLNames:  []string{"PXML_Orders"},
		fileGroups:       []string{"PRIMARY", "FG_Archive"},
		partitionSchemes: []string{"ps_year"},
	}
}

// nidxDialog builds the dialog's real pages for one index type, the way show
// plus the prefetch callback would.
func nidxDialog(t *testing.T, typ gosmo.IndexType, pf *nidxPrefetch) *NewIndexDialog {
	t.Helper()
	i := slices.IndexFunc(newIndexKinds, func(k newIndexKind) bool { return k.typ == typ })
	if i < 0 {
		t.Fatalf("no cascade entry creates a %s index", typ)
	}
	d := &NewIndexDialog{kind: newIndexKinds[i], dbName: "AppDB", schema: "dbo", table: "Orders"}
	d.pages = nidxPagesFor(typ)
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.buildPages(pf)
	return d
}

// page returns one of the dialog's built forms by name, so a test never
// depends on the page order.
func (d *NewIndexDialog) page(t *testing.T, name string) *propsheet.Form {
	t.Helper()
	i := slices.Index(d.pages, name)
	if i < 0 {
		t.Fatalf("this dialog has pages %v, not %q", d.pages, name)
	}
	return d.forms[i]
}

// addKeyColumn picks a column in the "Column to add" dropdown and presses
// Add, the two-step the page really requires.
func addKeyColumn(t *testing.T, f *propsheet.Form, column string) {
	t.Helper()
	chooseSelect(t, f, "Column to add", column)
	clickButton(t, f, "Add")
}

func TestNewIndexRequestNonClustered(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeNonClustered, nidxTestPrefetch())
	general := d.page(t, "General")

	editText(t, general, "Index name", "IX_Orders_Customer")
	editCheck(t, general, "Unique", true)
	// Two columns, added in an order that is not the table's — the order the
	// user chose is the index's key order, and sorting it would silently
	// build a different index.
	addKeyColumn(t, general, "Placed")
	addKeyColumn(t, general, "Customer")

	// Sort order applies to the *selected* row, so select the second one the
	// way the user does — with the keyboard, through OnSelectRow.
	selectGridRow(t, plainGrid(t, general), 1, "Customer")
	editSelect(t, general, "Sort order", "Descending")

	included := d.page(t, "Included Columns")
	toggleByName(t, included.Rows()[1].(*propsheet.ToggleGridRow), "OrderID", 0)
	// A column that is also a key column must not reach INCLUDE: CREATE INDEX
	// rejects the duplicate, and the two lists are edited on different pages.
	toggleByName(t, included.Rows()[1].(*propsheet.ToggleGridRow), "Placed", 0)

	options := d.page(t, "Options")
	editText(t, options, "Fill factor", "80")
	editCheck(t, options, "Pad index", true)
	editCheck(t, options, "Allow online processing", true)
	editSelect(t, options, "Data compression", "PAGE")

	editText(t, d.page(t, "Filter"), "Filter predicate", "[Customer] IS NOT NULL")

	storage := d.page(t, "Storage")
	editSelect(t, storage, "Filegroup", "FG_Archive")

	req := d.request()
	if req.Name != "IX_Orders_Customer" || req.Type != gosmo.IndexTypeNonClustered || !req.IsUnique {
		t.Errorf("request = %+v, want a unique nonclustered index by that name", req)
	}
	if got := nidxNames(req.KeyColumns); !slices.Equal(got, []string{"Placed", "Customer"}) {
		t.Errorf("key columns = %v, want them in the order they were added", got)
	}
	if req.KeyColumns[0].Descending || !req.KeyColumns[1].Descending {
		t.Errorf("key columns = %+v, want DESC on Customer only", req.KeyColumns)
	}
	if !slices.Equal(req.IncludedColumns, []string{"OrderID"}) {
		t.Errorf("included columns = %v, want only OrderID — Placed is a key column", req.IncludedColumns)
	}
	if req.FillFactor != 80 || !req.PadIndex || !req.Online || req.DataCompression != "PAGE" {
		t.Errorf("options = %+v, want the page's fill factor, pad, online and compression", req)
	}
	if req.FilterDefinition != "[Customer] IS NOT NULL" {
		t.Errorf("filter = %q, want the page's predicate", req.FilterDefinition)
	}
	if req.FileGroup != "FG_Archive" || req.PartitionScheme != "" {
		t.Errorf("storage = %q/%q, want the chosen filegroup", req.FileGroup, req.PartitionScheme)
	}
}

// A partition scheme and a filegroup are alternatives, and the ON clause
// takes one. The scheme is the one that wins, because choosing it is the
// deliberate act — leaving the filegroup at its default would otherwise send
// both and gosmo would refuse the request.
func TestNewIndexPartitionSchemeOverridesTheFilegroup(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeNonClustered, nidxTestPrefetch())
	addKeyColumn(t, d.page(t, "General"), "Customer")
	editText(t, d.page(t, "General"), "Index name", "IX_Orders_Customer")

	storage := d.page(t, "Storage")
	editSelect(t, storage, "Filegroup", "FG_Archive")
	editSelect(t, storage, "Partition scheme", "ps_year")
	editSelect(t, storage, "Partitioning column", "Placed")

	req := d.request()
	if req.FileGroup != "" {
		t.Errorf("filegroup = %q, want it dropped in favour of the partition scheme", req.FileGroup)
	}
	if req.PartitionScheme != "ps_year" || !slices.Equal(req.PartitionColumns, []string{"Placed"}) {
		t.Errorf("partitioning = %q %v, want ps_year(Placed)", req.PartitionScheme, req.PartitionColumns)
	}
}

// The key column list is reorderable, and Move Up has to move the column, not
// the cursor: the user is still working with the column they picked.
func TestNewIndexMoveUpReordersTheKeyColumns(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeNonClustered, nidxTestPrefetch())
	general := d.page(t, "General")
	addKeyColumn(t, general, "Placed")
	addKeyColumn(t, general, "Customer")

	selectGridRow(t, plainGrid(t, general), 1, "Customer")
	clickButton(t, general, "Move Up")

	if got := nidxNames(d.request().KeyColumns); !slices.Equal(got, []string{"Customer", "Placed"}) {
		t.Fatalf("key columns = %v, want Customer moved ahead of Placed", got)
	}
	// The selection follows the column, so a second Move Up is a no-op rather
	// than dragging whatever landed under the cursor.
	clickButton(t, general, "Move Up")
	if got := nidxNames(d.request().KeyColumns); !slices.Equal(got, []string{"Customer", "Placed"}) {
		t.Errorf("key columns = %v after moving the first column up, want them unchanged", got)
	}
}

func TestNewIndexRemoveDropsTheSelectedKeyColumn(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeNonClustered, nidxTestPrefetch())
	general := d.page(t, "General")
	addKeyColumn(t, general, "OrderID")
	addKeyColumn(t, general, "Placed")
	addKeyColumn(t, general, "Customer")

	// Not the first row: a page that ignores the selection still passes when
	// the test removes row 0.
	selectGridRow(t, plainGrid(t, general), 1, "Placed")
	clickButton(t, general, "Remove")

	if got := nidxNames(d.request().KeyColumns); !slices.Equal(got, []string{"OrderID", "Customer"}) {
		t.Errorf("key columns = %v, want the selected Placed removed", got)
	}
}

// A column can only be a key column once; the second Add says so instead of
// building a statement the server rejects.
func TestNewIndexRefusesADuplicateKeyColumn(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeNonClustered, nidxTestPrefetch())
	general := d.page(t, "General")
	addKeyColumn(t, general, "Customer")
	addKeyColumn(t, general, "Customer")

	if got := nidxNames(d.request().KeyColumns); len(got) != 1 {
		t.Errorf("key columns = %v, want the duplicate refused", got)
	}
	if hint := formHint(t, general); !strings.Contains(hint, "already a key column") {
		t.Errorf("hint = %q, want it to say why nothing was added", hint)
	}
}

func TestNewIndexRequestColumnstore(t *testing.T) {
	pf := nidxTestPrefetch()

	cci := nidxDialog(t, gosmo.IndexTypeClusteredColumnStore, pf)
	editText(t, cci.page(t, "General"), "Index name", "CCI_Orders_Archive")
	editSelect(t, cci.page(t, "Options"), "Data compression", "COLUMNSTORE_ARCHIVE")
	editText(t, cci.page(t, "Options"), "Compression delay", "60")
	req := cci.request()
	if len(req.KeyColumns) != 0 {
		t.Errorf("key columns = %v, want none on a clustered columnstore index", req.KeyColumns)
	}
	if req.DataCompression != "COLUMNSTORE_ARCHIVE" || req.CompressionDelay != 60 {
		t.Errorf("request = %+v, want the columnstore compression options", req)
	}
	// A clustered columnstore index has no fill factor and no INCLUDE list,
	// so those pages must not exist to offer them.
	if slices.Contains(cci.pages, "Included Columns") || slices.Contains(cci.pages, "Filter") {
		t.Errorf("pages = %v, want no Included Columns or Filter page", cci.pages)
	}

	ncci := nidxDialog(t, gosmo.IndexTypeColumnStore, pf)
	addKeyColumn(t, ncci.page(t, "General"), "Customer")
	editText(t, ncci.page(t, "Filter"), "Filter predicate", "[OrderID] > 0")
	nreq := ncci.request()
	if nreq.FilterDefinition != "[OrderID] > 0" {
		t.Errorf("filter = %q, want the filtered-NCCI predicate", nreq.FilterDefinition)
	}
	// Nothing on a columnstore page may set an option the type rejects: no
	// Unique row, no sort order, no fill factor.
	if nreq.IsUnique || nreq.FillFactor != 0 || nreq.KeyColumns[0].Descending {
		t.Errorf("request = %+v, want no rowstore-only option set", nreq)
	}
	if compression := selectRow(t, ncci.page(t, "Options"), "Data compression"); slices.Contains(compression.Items(), "PAGE") {
		t.Errorf("columnstore compression offers %v, want only the columnstore keywords", compression.Items())
	}
}

func TestNewIndexRequestXML(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeXML, nidxTestPrefetch())
	general := d.page(t, "General")
	editText(t, general, "Index name", "SXML_Orders_Path")
	// The column picker offers the xml column and nothing else: an XML index
	// on an int column is not a thing the server will do.
	if items := selectRow(t, general, "Column").Items(); !slices.Equal(items, []string{"Doc"}) {
		t.Fatalf("column picker offers %v, want only the xml column", items)
	}

	xml := d.page(t, "XML")
	editRadio(t, xml, "Index kind", "Secondary")
	editSelect(t, xml, "Secondary index type", "VALUE")

	req := d.request()
	if req.IsPrimaryXML {
		t.Error("request is a primary XML index, want the secondary form the page selected")
	}
	if req.PrimaryXMLIndex != "PXML_Orders" || req.SecondaryXMLType != gosmo.XMLSecondaryValue {
		t.Errorf("request = %+v, want it built over PXML_Orders FOR VALUE", req)
	}
	if !slices.Equal(nidxNames(req.KeyColumns), []string{"Doc"}) {
		t.Errorf("key columns = %v, want the xml column", nidxNames(req.KeyColumns))
	}
	// An XML index takes no DATA_COMPRESSION at all.
	for _, r := range d.page(t, "Options").Rows() {
		if sr, ok := r.(*propsheet.SelectRow); ok && strings.HasPrefix(sr.Label(), "Data compression") {
			t.Error("the XML Options page offers data compression, which an XML index rejects")
		}
	}
}

func TestNewIndexRequestSpatial(t *testing.T) {
	d := nidxDialog(t, gosmo.IndexTypeSpatial, nidxTestPrefetch())
	editText(t, d.page(t, "General"), "Index name", "SIX_Orders_Where")

	spatial := d.page(t, "Spatial")
	// GEOMETRY_GRID is already the first item, so this is chooseSelect's case:
	// picking what is already selected is a legitimate choice, and asserting
	// dirtiness would only pin the order the list was built in.
	chooseSelect(t, spatial, "Tessellation scheme", "GEOMETRY_GRID")
	editText(t, spatial, "Bounding box X-min", "-100.5")
	editText(t, spatial, "Bounding box Y-max", "45")
	editSelect(t, spatial, "Grid level 1", "LOW")
	editText(t, spatial, "Cells per object", "16")

	req := d.request()
	if req.Tessellation != gosmo.SpatialGeometryGrid {
		t.Errorf("tessellation = %q, want GEOMETRY_GRID", req.Tessellation)
	}
	if req.BoundingBox == nil || req.BoundingBox.XMin != -100.5 || req.BoundingBox.YMax != 45 {
		t.Errorf("bounding box = %+v, want the page's coordinates", req.BoundingBox)
	}
	if req.GridLevels.Level1 != gosmo.SpatialGridLow || req.GridLevels.Level2 != "" {
		t.Errorf("grid levels = %+v, want level 1 only", req.GridLevels)
	}
	if req.CellsPerObject != 16 {
		t.Errorf("cells per object = %d, want 16", req.CellsPerObject)
	}

	// A geography index tessellates the whole globe, so the bounding box the
	// form still shows must not reach the request.
	editSelect(t, spatial, "Tessellation scheme", "GEOGRAPHY_AUTO_GRID")
	geo := d.request()
	if geo.BoundingBox != nil {
		t.Errorf("bounding box = %+v, want none for a geography index", geo.BoundingBox)
	}
	if geo.GridLevels.Level1 != "" {
		t.Errorf("grid levels = %+v, want none for an auto grid", geo.GridLevels)
	}
}

func TestNewIndexPreflight(t *testing.T) {
	pf := nidxTestPrefetch()

	t.Run("no name", func(t *testing.T) {
		d := nidxDialog(t, gosmo.IndexTypeNonClustered, pf)
		editText(t, d.page(t, "General"), "Index name", "")
		assertPreflight(t, d.preflight(), "name is required")
	})
	t.Run("duplicate name", func(t *testing.T) {
		d := nidxDialog(t, gosmo.IndexTypeNonClustered, pf)
		general := d.page(t, "General")
		// Matched case-insensitively: SQL Server names are, and letting a
		// differently-cased duplicate through just moves the error to CREATE.
		editText(t, general, "Index name", "IX_Orders_EXISTING")
		addKeyColumn(t, general, "Customer")
		assertPreflight(t, d.preflight(), "already exists")
	})
	t.Run("duplicate name with drop existing", func(t *testing.T) {
		d := nidxDialog(t, gosmo.IndexTypeNonClustered, pf)
		general := d.page(t, "General")
		editText(t, general, "Index name", "IX_Orders_existing")
		addKeyColumn(t, general, "Customer")
		editCheck(t, d.page(t, "Options"), "Drop existing index", true)
		if err := d.preflight(); err != nil {
			t.Errorf("preflight = %v, want the name accepted once it is being replaced", err)
		}
	})
	t.Run("no key columns", func(t *testing.T) {
		d := nidxDialog(t, gosmo.IndexTypeNonClustered, pf)
		editText(t, d.page(t, "General"), "Index name", "IX_New")
		assertPreflight(t, d.preflight(), "at least one key column")
	})
	t.Run("no column of the type", func(t *testing.T) {
		bare := nidxTestPrefetch()
		bare.columns = []*gosmo.Column{{Name: "OrderID", DataType: gosmo.DataType("int")}}
		d := nidxDialog(t, gosmo.IndexTypeSpatial, bare)
		editText(t, d.page(t, "General"), "Index name", "SIX_New")
		assertPreflight(t, d.preflight(), "geometry or geography")
	})
	t.Run("secondary XML with no primary", func(t *testing.T) {
		bare := nidxTestPrefetch()
		bare.primaryXMLNames = nil
		d := nidxDialog(t, gosmo.IndexTypeXML, bare)
		editText(t, d.page(t, "General"), "Index name", "SXML_New")
		editRadio(t, d.page(t, "XML"), "Index kind", "Secondary")
		assertPreflight(t, d.preflight(), "has none yet")
	})
	t.Run("unparsable bounding box", func(t *testing.T) {
		d := nidxDialog(t, gosmo.IndexTypeSpatial, pf)
		editText(t, d.page(t, "General"), "Index name", "SIX_New")
		editText(t, d.page(t, "Spatial"), "Bounding box X-max", "east")
		assertPreflight(t, d.preflight(), "must be a number")
	})
	t.Run("a clustered columnstore index needs nothing else", func(t *testing.T) {
		d := nidxDialog(t, gosmo.IndexTypeClusteredColumnStore, pf)
		editText(t, d.page(t, "General"), "Index name", "CCI_New")
		if err := d.preflight(); err != nil {
			t.Errorf("preflight = %v, want a name to be enough", err)
		}
	})
}

// The Indexes folder is the only way into New Index, and every index type has
// its own item there.
func TestIndexesFolderOffersEveryIndexType(t *testing.T) {
	node := &explorerNode{}
	node.data.Type = NodeIndexes
	a := &App{}
	var cascade []string
	for _, item := range a.nodeMenuItems(node) {
		if item.Label == "New Index" {
			cascade = labelsOf(item.Sub)
		}
	}
	if len(cascade) == 0 {
		t.Fatalf("Indexes folder menu = %v, want a New Index cascade", menuLabels(t, node))
	}
	for _, kind := range newIndexKinds {
		if !slices.Contains(cascade, kind.label) {
			t.Errorf("New Index cascade = %v, want an entry for %s", cascade, kind.label)
		}
	}
}

func TestStatisticsFolderOffersNewStatistics(t *testing.T) {
	node := &explorerNode{}
	node.data.Type = NodeStatistics
	if labels := menuLabels(t, node); !slices.Contains(labels, "New Statistics...") {
		t.Errorf("Statistics folder menu = %v, want a New Statistics... item", labels)
	}
}

func TestStatisticOffersUpdateStatistics(t *testing.T) {
	node := &explorerNode{}
	node.data.Type = NodeStatistic
	if labels := menuLabels(t, node); !slices.Contains(labels, "Update Statistics") {
		t.Errorf("statistic menu = %v, want an Update Statistics item", labels)
	}
}

// nidxNames reduces a key column list to its names, in order.
func nidxNames(cols []gosmo.IndexColumnDef) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// formHint returns the page's HintRow text.
func formHint(t *testing.T, f *propsheet.Form) string {
	t.Helper()
	for _, r := range f.Rows() {
		if hr, ok := r.(*propsheet.HintRow); ok {
			return hr.Text()
		}
	}
	t.Fatal("this page has no HintRow — a handler that declines has nowhere to say why")
	return ""
}

func assertPreflight(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("preflight accepted the form, want it refused with %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("preflight = %v, want it to mention %q", err, want)
	}
}
