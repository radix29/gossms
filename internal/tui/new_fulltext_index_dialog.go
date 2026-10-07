package tui

import (
	"context"
	"fmt"
	"slices"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_fulltext_index_dialog.go is New Full-Text Index — the table menu's
// Full-Text index ▸ Define Full-Text Index…, SSMS's wizard as one dialog of two
// pages: General (key index, catalog, filegroup, change tracking, stoplist,
// property list) and Columns (fulltext_index_props.go's column editor).
//
// The rights are two halves (2026-10-08 probe, majors 14 and 17): ALTER on the
// table, which gates the menu item like the rest of the cascade, and
// REFERENCES on the catalog and on a user stoplist or property list named,
// which preflight asks once they are chosen. ALTER on the database and ALTER
// ANY FULLTEXT CATALOG read 0 for REFERENCES and are refused (Msg 7666), so the
// menu offering Define to them is not enough.

// nftIndexPrefetch is what the dialog reads before it opens.
type nftIndexPrefetch struct {
	// existing is the table's full-text index, nil when it has none — a
	// table has at most one.
	existing   *gosmo.FullTextIndex
	keyIndexes []string
	catalogs   []string
	defaultCat int
	fileGroups []string
	stoplists  []string
	lists      []string
	columns    []*gosmo.Column
	langs      []gosmo.FullTextLanguage
	collation  string
}

// NewFullTextIndexDialog is the New Full-Text Index dialog.
type NewFullTextIndexDialog struct {
	newObjectDialog[nftIndexPrefetch]

	dbName, schema, table string
	node                  *explorerNode
}

// NewNewFullTextIndexDialog creates the dialog and wires its callbacks.
func NewNewFullTextIndexDialog(app *App) *NewFullTextIndexDialog {
	d := &NewFullTextIndexDialog{}
	d.init(app, newObjectConfig[nftIndexPrefetch]{
		title: "New Full-Text Index",
		noun:  "Full-text index on",
		pages: []string{"General", "Columns"},
		fetch: d.fetchPrefetch,
		build: d.buildPages,
		// The index has no node of its own: what shows it is the catalogs'
		// Details, as after the cascade's writes.
		refresh: func(sc *db.ServerConn) { d.app.refreshFullTextDetails(sc, d.dbName) },
	})
	return d
}

// show opens the dialog for a table node.
func (d *NewFullTextIndexDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.node = node
	d.dbName, d.schema, d.table = node.data.DBName, node.data.Schema, node.data.Name
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Database: "+d.dbName, "Table: "+fqn(d.schema, d.table))
}

// fullTextKeyIndexes are the indexes CREATE FULLTEXT INDEX accepts as its
// KEY INDEX: unique, enabled, unfiltered, rowstore, on one non-nullable
// column. The server refuses any other (Msg 7653, 7655), so they are not
// offered.
func fullTextKeyIndexes(idx []*gosmo.Index, cols []*gosmo.Column) []string {
	nullable := map[string]bool{}
	for _, c := range cols {
		nullable[c.Name] = c.IsNullable
	}
	var out []string
	for _, i := range idx {
		if !i.IsUnique || i.IsDisabled || i.FilterDefinition != "" || len(i.KeyColumns) != 1 {
			continue
		}
		if i.Type != gosmo.IndexTypeClustered && i.Type != gosmo.IndexTypeNonClustered {
			continue
		}
		if nullable[i.KeyColumns[0].Name] {
			continue
		}
		out = append(out, i.Name)
	}
	return out
}

func (d *NewFullTextIndexDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*nftIndexPrefetch, error) {
	t, err := findTable(ctx, sc, d.dbName, d.schema, d.table)
	if err != nil {
		return nil, err
	}
	pf := &nftIndexPrefetch{collation: databaseCollation(t.Database())}
	if pf.existing, err = findFullTextIndex(ctx, sc, d.dbName, d.schema, d.table); err != nil {
		return nil, err
	}
	if pf.columns, err = t.Columns(ctx); err != nil {
		return nil, err
	}
	idx, err := t.Indexes(ctx)
	if err != nil {
		return nil, err
	}
	pf.keyIndexes = fullTextKeyIndexes(idx, pf.columns)
	// The primary key first: it is what SSMS proposes, and nearly always the
	// right answer.
	for _, i := range idx {
		if k := slices.Index(pf.keyIndexes, i.Name); i.IsPrimaryKey && k > 0 {
			pf.keyIndexes[0], pf.keyIndexes[k] = pf.keyIndexes[k], pf.keyIndexes[0]
		}
	}
	d0 := t.Database()
	cats, err := d0.FullTextCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	for k, c := range cats {
		pf.catalogs = append(pf.catalogs, c.Name)
		if c.IsDefault {
			pf.defaultCat = k
		}
	}
	fgs, err := d0.FileGroups(ctx)
	if err != nil {
		return nil, err
	}
	pf.fileGroups = []string{fullTextDefaultFileGroup}
	for _, g := range fgs {
		if g.Type == "ROWS_FILEGROUP" {
			pf.fileGroups = append(pf.fileGroups, g.Name)
		}
	}
	if pf.stoplists, pf.lists, err = fullTextPickers(ctx, d0); err != nil {
		return nil, err
	}
	if pf.langs, err = readFullTextLanguages(ctx, sc); err != nil {
		return nil, err
	}
	return pf, nil
}

const fullTextDefaultFileGroup = "(default)"

func (d *NewFullTextIndexDialog) buildPages(pf *nftIndexPrefetch) {
	sc := d.sc
	dbName, schema, table := d.dbName, d.schema, d.table

	keyIndex := propsheet.Select("Unique index", pf.keyIndexes, 0)
	keyIndex.SetFitItems(true)
	catalog := propsheet.Select("Full-text catalog", pf.catalogs, pf.defaultCat)
	catalog.SetFitItems(true)
	fileGroup := propsheet.Select("Filegroup", pf.fileGroups, 0)
	fileGroup.SetFitItems(true)
	tracking := propsheet.Select("Change tracking", fullTextChangeTrackingItems, 0)
	noPopulation := propsheet.Check("Do not populate now", false)
	stoplist := propsheet.Select("Stoplist", pf.stoplists, 1) // <system>, the server's default
	stoplist.SetFitItems(true)
	list := propsheet.Select("Search property list", pf.lists, 0)
	list.SetFitItems(true)

	general := []propsheet.Row{
		propsheet.Section("Index"),
		propsheet.Static("Table", fqn(schema, table)),
		keyIndex,
		catalog,
		fileGroup,
		propsheet.Section("Maintenance"),
		tracking,
		noPopulation,
		propsheet.Section("Search"),
		stoplist,
		list,
	}
	switch {
	case pf.existing != nil:
		general = append(general, propsheet.Note("This table already has a full-text index — "+
			"use Full-Text index ▸ Properties to change it, or Delete it first."))
	case len(pf.keyIndexes) == 0:
		general = append(general, propsheet.Note("This table has no index a full-text index can key on: "+
			"it needs a unique, single-column, non-nullable index, such as a primary key on one column."))
	case len(pf.catalogs) == 0:
		// The catalog view lists only what the login holds a permission on,
		// so "none" can mean "none visible to you".
		general = append(general, propsheet.Note("No full-text catalog in this database is visible to this login — "+
			"create one under Storage > Full Text Catalogs, or ask for REFERENCES on an existing one."))
	}
	general = append(general, propsheet.Note("Change tracking AUTO keeps the index current as rows change; MANUAL "+
		"collects the changes for Apply Tracked Changes; OFF needs a population to pick anything up. "+
		"Do not populate now applies only with change tracking OFF."))
	d.forms[0] = propsheet.NewForm(general...)

	cols := newFullTextColumnEditor(pf.collation, pf.columns, pf.langs, nil)
	d.forms[1] = propsheet.NewForm(cols.rows("The index holds the columns listed here. A varbinary or image column " +
		"holds documents and needs a type column naming each row's file type.")...)

	var req gosmo.CreateFullTextIndexRequest
	d.objectName = func() string { return fqn(schema, table) }
	d.preflight = func() error {
		switch {
		case pf.existing != nil:
			return fmt.Errorf("%s already has a full-text index", fqn(schema, table))
		case len(pf.keyIndexes) == 0:
			return fmt.Errorf("%s has no unique, single-column, non-nullable index to key on", fqn(schema, table))
		case len(pf.catalogs) == 0:
			return fmt.Errorf("no full-text catalog is visible to this login — create one, or ask for REFERENCES on one")
		}
		specs := cols.specs()
		if len(specs) == 0 {
			return fmt.Errorf("add at least one column on the Columns page")
		}
		ct := gosmo.FullTextChangeTracking(tracking.Value())
		if noPopulation.Checked() && ct != gosmo.FullTextChangeTrackingOff {
			return fmt.Errorf("\"do not populate now\" needs change tracking OFF — the server refuses it otherwise")
		}
		if err := fullTextReferencesRefusal(sc, dbName, gate.ReferencesOnFullTextCatalog, "catalog", catalog.Value()); err != nil {
			return err
		}
		kind, slName := fullTextStoplistChoice(stoplist.Value())
		if kind == gosmo.FullTextStoplistUser {
			if err := fullTextReferencesRefusal(sc, dbName, gate.ReferencesOnFullTextStoplist, "stoplist", slName); err != nil {
				return err
			}
		}
		plName := ""
		if v := list.Value(); v != propertyListNone {
			plName = v
			if err := fullTextReferencesRefusal(sc, dbName, gate.ReferencesOnSearchPropertyList, "search property list", v); err != nil {
				return err
			}
		}
		req = gosmo.CreateFullTextIndexRequest{
			Columns:            specs,
			KeyIndex:           keyIndex.Value(),
			Catalog:            catalog.Value(),
			ChangeTracking:     ct,
			NoPopulation:       noPopulation.Checked(),
			StoplistOff:        kind == gosmo.FullTextStoplistOff,
			Stoplist:           slName,
			SearchPropertyList: plName,
		}
		if fg := fileGroup.Value(); fg != fullTextDefaultFileGroup {
			req.FileGroup = fg
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// A TableRef: every write addresses the table by name, and the
		// by-name read would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).TableRef(schema, table).CreateFullTextIndex(ctx, req)
		return err
	}
	// The Columns page has nothing of its own to send: its columns are in the
	// CREATE General's step sends.
	d.applyFns[1] = func(context.Context) error { return nil }
}
