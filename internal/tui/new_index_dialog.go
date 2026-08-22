package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// new_index_dialog.go is the New Index creation dialog — the Indexes folder's
// "New Index ▸" cascade. One dialog builds one gosmo.CreateIndexRequest; the
// cascade item picks the index type, and the type then decides which pages
// the dialog has, because almost nothing is shared between them: a
// columnstore index has no fill factor, a clustered index no INCLUDE list, an
// XML index neither, and a clustered columnstore index no key columns at all.
// A single page set covering all of them would be mostly rows that do not
// apply, and the server rejects each of those combinations rather than
// ignoring it.
//
// The per-type rules live in gosmo's CreateIndexRequest.validate, not here.
// This dialog only offers what a type accepts; the request it builds is
// checked again there, so a page that offers too much fails with a message
// naming the field instead of reaching the server.

// newIndexKind is one entry of the cascade: the menu label, the noun the
// dialog's header names it by, and the type the request carries.
type newIndexKind struct {
	label string
	noun  string
	typ   gosmo.IndexType
}

// newIndexKinds is the cascade, in SSMS's order.
var newIndexKinds = []newIndexKind{
	{"Clustered Index...", "Clustered index", gosmo.IndexTypeClustered},
	{"Non-Clustered Index...", "Nonclustered index", gosmo.IndexTypeNonClustered},
	{"Clustered Columnstore Index...", "Clustered columnstore index", gosmo.IndexTypeClusteredColumnStore},
	{"Non-Clustered Columnstore Index...", "Nonclustered columnstore index", gosmo.IndexTypeColumnStore},
	{"XML Index...", "XML index", gosmo.IndexTypeXML},
	{"Spatial Index...", "Spatial index", gosmo.IndexTypeSpatial},
}

// nidxPrefetch is the one fetch every page of this dialog is built from: the
// table's columns (every column picker), its existing index names (the
// name-uniqueness preflight, and the primary XML indexes a secondary one can
// be built over), and the database's filegroups and partition schemes (the
// Storage page).
type nidxPrefetch struct {
	columns          []*gosmo.Column
	existingNames    map[string]bool
	primaryXMLNames  []string
	fileGroups       []string
	partitionSchemes []string
}

// NewIndexDialog is the New Index creation dialog.
type NewIndexDialog struct {
	newObjectDialog[nidxPrefetch]

	// kind, and the table it creates an index on, are set by show before the
	// embedded dialog's own show runs the prefetch that reads them.
	kind   newIndexKind
	dbName string
	schema string
	table  string
	node   *explorerNode

	// rows are the widgets the pages built, read back by request(). Which of
	// them exist depends on kind, so every read is nil-guarded.
	rows nidxRows
	// keyColumns is the key column list the General page's grid edits — the
	// one piece of the request that is a list the user reorders rather than a
	// widget's value.
	keyColumns []gosmo.IndexColumnDef
}

// NewNewIndexDialog creates the dialog and wires its callbacks.
func NewNewIndexDialog(app *App) *NewIndexDialog {
	d := &NewIndexDialog{}
	d.init(app, newObjectConfig[nidxPrefetch]{
		title:   "New Index",
		noun:    "Index",
		pages:   nidxPagesFor(gosmo.IndexTypeNonClustered),
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	return d
}

// show opens the dialog for one table's Indexes folder. The page set is
// chosen here, before the embedded show calls SetPages with it.
func (d *NewIndexDialog) show(sc *db.ServerConn, node *explorerNode, kind newIndexKind) {
	d.kind = kind
	d.node = node
	d.dbName, d.schema, d.table = node.data.DBName, node.data.Schema, node.data.Name
	// Script Changes opens its query window in the database the statement
	// runs in, not the connection's default.
	d.scriptDatabase = d.dbName
	d.keyColumns = nil
	d.rows = nidxRows{}
	d.pages = nidxPagesFor(kind.typ)
	d.newObjectDialog.show(sc)
	d.SetHeader("Database: "+d.dbName, kind.noun+" on "+fqn(d.schema, d.table))
}

// nidxPagesFor is the page set one index type needs. Every page named here
// is built by buildPages; a type that has no use for a page simply doesn't
// list it.
func nidxPagesFor(typ gosmo.IndexType) []string {
	switch typ {
	case gosmo.IndexTypeXML:
		return []string{"General", "XML", "Options"}
	case gosmo.IndexTypeSpatial:
		return []string{"General", "Spatial", "Options", "Storage"}
	case gosmo.IndexTypeClusteredColumnStore:
		return []string{"General", "Options", "Storage"}
	case gosmo.IndexTypeColumnStore:
		return []string{"General", "Options", "Filter", "Storage"}
	case gosmo.IndexTypeClustered:
		return []string{"General", "Options", "Storage"}
	default:
		return []string{"General", "Included Columns", "Options", "Filter", "Storage"}
	}
}

func (d *NewIndexDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*nidxPrefetch, error) {
	t, err := findTable(ctx, sc, d.dbName, d.schema, d.table)
	if err != nil {
		return nil, err
	}
	cols, err := t.ColumnsContext(ctx)
	if err != nil {
		return nil, err
	}
	indexes, err := t.IndexesContext(ctx)
	if err != nil {
		return nil, err
	}
	pf := &nidxPrefetch{columns: cols, existingNames: make(map[string]bool, len(indexes))}
	for _, idx := range indexes {
		pf.existingNames[strings.ToLower(idx.Name)] = true
	}
	if d.kind.typ == gosmo.IndexTypeXML {
		// A secondary XML index is built over a primary one, which the
		// catalog reports as an XML index with no secondary type.
		xml, err := t.XMLIndexesContext(ctx)
		if err != nil {
			return nil, err
		}
		for _, x := range xml {
			if x.IsPrimary {
				pf.primaryXMLNames = append(pf.primaryXMLNames, x.Name)
			}
		}
	}
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, d.dbName)
	if err != nil {
		return nil, err
	}
	fgs, err := dbObj.FileGroupsContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, fg := range fgs {
		pf.fileGroups = append(pf.fileGroups, fg.Name)
	}
	schemes, err := dbObj.PartitionSchemesContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, ps := range schemes {
		pf.partitionSchemes = append(pf.partitionSchemes, ps.Name)
	}
	return pf, nil
}

func (d *NewIndexDialog) buildPages(pf *nidxPrefetch) {
	for i, page := range d.pages {
		switch page {
		case "General":
			d.forms[i] = d.generalForm(pf)
		case "Included Columns":
			d.forms[i] = d.includedForm(pf)
		case "Options":
			d.forms[i] = d.optionsForm()
		case "Filter":
			d.forms[i] = d.filterForm()
		case "Storage":
			d.forms[i] = d.storageForm(pf)
		case "XML":
			d.forms[i] = d.xmlForm(pf)
		case "Spatial":
			d.forms[i] = d.spatialForm()
		}
	}
	// One statement creates the index, so the whole request is applied by the
	// first page's apply function; the rest only contribute widgets to it.
	d.applyFns[0] = d.createIndex
	d.objectName = func() string { return strings.TrimSpace(d.rows.name.Value()) }
	d.preflight = func() error { return d.checkRequest(pf) }
}

// createIndex is the dialog's whole apply: read the table back, then create.
// The read is a read, so it runs against the real server under Script
// Changes too — only the CREATE is collected.
func (d *NewIndexDialog) createIndex(ctx context.Context) error {
	t, err := findTable(ctx, d.sc, d.dbName, d.schema, d.table)
	if err != nil {
		return err
	}
	return t.CreateIndexContext(ctx, d.request())
}

// request assembles the CreateIndexRequest from whichever rows this type's
// pages built. Every read is nil-guarded: a row that belongs to a page this
// type doesn't have was never created.
func (d *NewIndexDialog) request() gosmo.CreateIndexRequest {
	r := d.rows
	if r.commitKeyColumn != nil {
		r.commitKeyColumn()
	}
	req := gosmo.CreateIndexRequest{
		Name:       strings.TrimSpace(r.name.Value()),
		Type:       d.kind.typ,
		KeyColumns: d.keyColumns,
	}
	if r.singleColumn != nil {
		req.KeyColumns = []gosmo.IndexColumnDef{{Name: r.singleColumn.Value()}}
	}
	if r.unique != nil {
		req.IsUnique = r.unique.Checked()
	}
	if r.included != nil {
		req.IncludedColumns = r.includedColumns(d.keyColumns)
	}
	if r.filter != nil {
		req.FilterDefinition = strings.TrimSpace(r.filter.Value())
	}
	if r.fillFactor != nil {
		req.FillFactor = nidxInt(r.fillFactor)
	}
	if r.padIndex != nil {
		req.PadIndex = r.padIndex.Checked()
	}
	if r.online != nil {
		req.Online = r.online.Checked()
	}
	if r.sortInTempDB != nil {
		req.SortInTempDB = r.sortInTempDB.Checked()
	}
	if r.dropExisting != nil {
		req.DropExisting = r.dropExisting.Checked()
	}
	if r.compression != nil && r.compression.Selected() > 0 {
		req.DataCompression = r.compression.Value()
	}
	if r.compressionDelay != nil {
		req.CompressionDelay = nidxInt(r.compressionDelay)
	}
	if r.fileGroup != nil && r.fileGroup.Selected() > 0 {
		req.FileGroup = r.fileGroup.Value()
	}
	if r.partitionScheme != nil && r.partitionScheme.Selected() > 0 {
		req.PartitionScheme = r.partitionScheme.Value()
		if r.partitionColumn != nil {
			req.PartitionColumns = []string{r.partitionColumn.Value()}
		}
		// An index is on a filegroup or on a partition scheme, never both.
		req.FileGroup = ""
	}
	if r.xmlPrimary != nil {
		req.IsPrimaryXML = r.xmlPrimary.Selected() == 0
		if !req.IsPrimaryXML {
			req.PrimaryXMLIndex = r.xmlParent.Value()
			req.SecondaryXMLType = gosmo.XMLSecondaryIndexType(r.xmlSecondaryType.Value())
		}
	}
	if r.tessellation != nil {
		req.Tessellation = gosmo.SpatialTessellation(r.tessellation.Value())
		if req.Tessellation.IsGeometry() {
			req.BoundingBox = &gosmo.SpatialBoundingBox{
				XMin: nidxFloat(r.boundingBox[0]), YMin: nidxFloat(r.boundingBox[1]),
				XMax: nidxFloat(r.boundingBox[2]), YMax: nidxFloat(r.boundingBox[3]),
			}
		}
		if !req.Tessellation.IsAutoGrid() {
			req.GridLevels = gosmo.SpatialGridLevels{
				Level1: nidxDensity(r.gridLevels[0]), Level2: nidxDensity(r.gridLevels[1]),
				Level3: nidxDensity(r.gridLevels[2]), Level4: nidxDensity(r.gridLevels[3]),
			}
		}
		req.CellsPerObject = nidxInt(r.cellsPerObject)
	}
	return req
}

// checkRequest is the preflight: the things worth catching before a round
// trip, plus the ones the server's own message would not explain. Everything
// else is gosmo's validate and the server's job.
func (d *NewIndexDialog) checkRequest(pf *nidxPrefetch) error {
	name := d.objectName()
	if name == "" {
		return fmt.Errorf("index name is required")
	}
	if pf.existingNames[strings.ToLower(name)] && (d.rows.dropExisting == nil || !d.rows.dropExisting.Checked()) {
		return fmt.Errorf("an index named %q already exists on %s — tick Drop existing index to replace it", name, fqn(d.schema, d.table))
	}
	if nidxNeedsOneColumn(d.kind.typ) && d.rows.singleColumn == nil {
		return fmt.Errorf("%s has no %s column, which a %s needs",
			fqn(d.schema, d.table), nidxColumnKind(d.kind.typ), strings.ToLower(d.kind.noun))
	}
	if d.kind.typ != gosmo.IndexTypeClusteredColumnStore && !nidxNeedsOneColumn(d.kind.typ) && len(d.keyColumns) == 0 {
		return fmt.Errorf("add at least one key column")
	}
	if r := d.rows.xmlPrimary; r != nil && r.Selected() == 1 && d.rows.xmlParent == nil {
		return fmt.Errorf("a secondary XML index is built over a primary XML index, and %s has none yet",
			fqn(d.schema, d.table))
	}
	if r := d.rows.tessellation; r != nil && gosmo.SpatialTessellation(r.Value()).IsGeometry() {
		for i, label := range nidxBoundingBoxLabels {
			if _, err := strconv.ParseFloat(strings.TrimSpace(d.rows.boundingBox[i].Value()), 64); err != nil {
				return fmt.Errorf("%s must be a number", label)
			}
		}
	}
	// Everything else is gosmo's CreateIndexRequest.validate, which the apply
	// runs before it reaches the server — a combination this dialog let
	// through fails there with a message naming the field, without a round
	// trip.
	return nil
}

// newIndexMenuItems is the Indexes folder's "New Index ▸" cascade — one item
// per index type, each opening this dialog preset to it. Every item is
// enabled: whether a type is possible on this table (a second clustered
// index, an XML index with no xml column) needs the table's columns and
// indexes, which the menu has not read, so the dialog says so on its General
// page and its preflight refuses rather than the menu item silently missing.
func (a *App) newIndexMenuItems(sc *db.ServerConn, node *explorerNode) []controls.MenuItem {
	items := make([]controls.MenuItem, 0, len(newIndexKinds))
	for _, kind := range newIndexKinds {
		items = append(items, controls.MenuItem{
			Label:  kind.label,
			Action: func() { a.showNewIndexDialog(sc, node, kind) },
		})
	}
	return items
}

// showNewIndexDialog opens New Index for a table's Indexes folder.
func (a *App) showNewIndexDialog(sc *db.ServerConn, node *explorerNode, kind newIndexKind) {
	if !a.requireConn(sc) {
		return
	}
	a.newIndexDialog.show(sc, node, kind)
}

// showNewStatisticsDialog opens New Statistics for a table's Statistics
// folder.
func (a *App) showNewStatisticsDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newStatisticsDialog.show(sc, node)
}
