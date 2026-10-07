package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// fulltext_props.go is the read-only Properties for the four full-text
// objects (docs/phase5-plan.md § 27, W15): a catalog, a stoplist, a search
// property list and a table's full-text index. Every page shows and none
// writes yet — W19 makes the catalog, stoplist, property list and index
// pages writable — so each is in prop_page_requires_test.go's
// pagesThatOnlyRead until then.
//
// The finders are the ones the Details pane uses (detail_browser_fulltext.go),
// so the pane and the dialog cannot disagree about which object a node names.

// fullTextReadOnlyNote closes each General page while nothing here writes.
const fullTextReadOnlyNote = "Full-text objects are browsed here; creating and changing them is not built yet."

func findFullTextCatalog(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.FullTextCatalog, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.FullTextCatalogByName(ctx, name)
}

func findFullTextStoplist(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.FullTextStoplist, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.FullTextStoplistByName(ctx, name)
}

func findSearchPropertyList(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.SearchPropertyList, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.SearchPropertyListByName(ctx, name)
}

// findFullTextIndex resolves a table's full-text index. FullTextIndex needs
// the loaded table (its object id), so this is TableByName, never TableRef.
//
// A table without one is an ordinary answer, not a failure: (nil, nil), which
// each page shows as noFullTextIndexForm. Only the index read's not-found is
// that — a table that is itself gone stays an error.
func findFullTextIndex(ctx context.Context, sc *db.ServerConn, dbName, schema, table string) (*gosmo.FullTextIndex, error) {
	t, err := findTable(ctx, sc, dbName, schema, table)
	if err != nil {
		return nil, err
	}
	i, err := t.FullTextIndex(ctx)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, nil
	}
	return i, err
}

// noFullTextIndexForm is an index page for a table that has none.
func noFullTextIndexForm() *propsheet.Form {
	return propsheet.NewForm(propsheet.Note("This table has no full-text index."))
}

// fullTextDate is a catalog or index time as the pages show it: "Never" for
// the zero time, which gosmo reads for "not yet".
func fullTextDate(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	return formatSQLDate(t)
}

// fullTextStoplistText is an index's STOPLIST setting as SSMS's General page
// words it: off, the system stoplist, or the user stoplist's name.
func fullTextStoplistText(i *gosmo.FullTextIndex) string {
	switch i.StoplistKind {
	case gosmo.FullTextStoplistOff:
		return "<off>"
	case gosmo.FullTextStoplistSystem:
		return "<system>"
	}
	return i.Stoplist
}

// fullTextColumnNames is an index's columns, comma-joined, for a grid cell.
func fullTextColumnNames(i *gosmo.FullTextIndex) string {
	names := make([]string, len(i.Columns))
	for k, c := range i.Columns {
		names[k] = c.Name
	}
	return strings.Join(names, ", ")
}

// fullTextColumnRows is an index's columns as the Columns grids show them.
var fullTextColumnHeaders = []string{"Column", "Type Column", "Language", "Statistical Semantics"}

func fullTextColumnRows(i *gosmo.FullTextIndex) [][]string {
	rows := make([][]string, len(i.Columns))
	for k, c := range i.Columns {
		lang := c.Language
		if lang == "" {
			lang = strconv.Itoa(c.LanguageID)
		}
		rows[k] = []string{c.Name, c.TypeColumn, lang, boolStr(c.StatisticalSemantics)}
	}
	return rows
}

// -- Catalog -------------------------------------------------------------------

func fullTextCatalogPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.FullTextCatalog, error) {
		return findFullTextCatalog(ctx, sc, dbName, name)
	}
	return []propPage{pageFullTextCatalogGeneral(find), pageFullTextCatalogTables(find)}
}

func pageFullTextCatalogGeneral(find func(context.Context) (*gosmo.FullTextCatalog, error)) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			accent := "Insensitive"
			if c.AccentSensitive {
				accent = "Sensitive"
			}
			return propsheet.NewForm(
				propsheet.Section("Catalog"),
				propsheet.Static("Name", c.Name),
				propsheet.Static("Owner", c.Owner),
				propsheet.Static("Default catalog", boolStr(c.IsDefault)),
				propsheet.Static("Accent sensitivity", accent),
				propsheet.Static("Importing", boolStr(c.IsImporting)),
				propsheet.Section("Population"),
				propsheet.Static("Population status", c.PopulateStatus.String()),
				propsheet.Static("Last population date", fullTextDate(c.LastPopulated)),
				propsheet.Static("Master merge in progress", boolStr(c.MergeInProgress)),
				propsheet.Section("Size"),
				propsheet.Static("Full-text indexes", strconv.Itoa(c.IndexCount)),
				propsheet.Static("Item count", strconv.Itoa(c.ItemCount)),
				propsheet.Static("Unique key count", strconv.Itoa(c.UniqueKeyCount)),
				propsheet.Static("Catalog size (MB)", strconv.Itoa(c.SizeMB)),
				propsheet.Note(fullTextReadOnlyNote),
			), nil, nil
		},
	}
}

// pageFullTextCatalogTables is SSMS's Tables/Views page: the catalog's
// indexes, and the selected one's columns below.
func pageFullTextCatalogTables(find func(context.Context) (*gosmo.FullTextCatalog, error)) propPage {
	return propPage{
		title: "Tables/Views",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			idx, err := c.Indexes(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(idx))
			for k, i := range idx {
				rows[k] = []string{articleObject(i.Schema, i.Table), i.KeyIndex, boolStr(i.IsEnabled),
					string(i.ChangeTracking), strconv.Itoa(i.ItemCount)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Table/View", "Unique Index", "Enabled", "Change Tracking", "Items"}, rows)
			cols := controls.NewDataGrid()
			cols.SetData(fullTextColumnHeaders, nil)
			// The column grid follows the selection only: a read-only
			// page's grid browses (docs/ui-rules.md).
			sync := func(row int) {
				if row < 0 || row >= len(idx) {
					resetGrid(cols, fullTextColumnHeaders, nil, 0)
					return
				}
				resetGrid(cols, fullTextColumnHeaders, fullTextColumnRows(idx[row]), 0)
			}
			grid.OnSelectRow = sync
			if len(idx) > 0 {
				sync(0)
			}
			f := propsheet.NewForm(
				propsheet.Section("Full-text indexed tables and views"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Section("Columns of the selected table"),
				propsheet.NewGridRow(cols, 6),
			)
			if len(idx) == 0 {
				f.Add(propsheet.Note("No full-text index is in this catalog yet."))
			}
			return f, nil, nil
		},
	}
}

// -- Stoplist ------------------------------------------------------------------

func fullTextStoplistPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.FullTextStoplist, error) {
		return findFullTextStoplist(ctx, sc, dbName, name)
	}
	return []propPage{
		{
			title: "General",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				l, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				return propsheet.NewForm(
					propsheet.Section("Stoplist"),
					propsheet.Static("Name", l.Name),
					propsheet.Static("Owner", l.Owner),
					propsheet.Static("Created", formatSQLDate(l.CreateDate)),
					propsheet.Static("Last modified", formatSQLDate(l.ModifyDate)),
					propsheet.Note(fullTextReadOnlyNote),
				), nil, nil
			},
		},
		{
			title: "Stopwords",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				l, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				words, err := l.Stopwords(ctx)
				if err != nil {
					return nil, nil, err
				}
				grid := controls.NewDataGrid()
				grid.SetData(stopwordHeaders, stopwordRows(words))
				f := propsheet.NewForm(
					propsheet.Section("Stopwords by language"),
					propsheet.NewGridRow(grid, 14),
				)
				if len(words) == 0 {
					f.Add(propsheet.Note("This stoplist is empty: no word is left out of the indexes that use it."))
				}
				return f, nil, nil
			},
		},
	}
}

var stopwordHeaders = []string{"Stopword", "Language", "LCID"}

func stopwordRows(words []gosmo.FullTextStopword) [][]string {
	rows := make([][]string, len(words))
	for k, w := range words {
		rows[k] = []string{w.Word, w.Language, strconv.Itoa(w.LanguageID)}
	}
	return rows
}

// -- Search property list ------------------------------------------------------

func searchPropertyListPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.SearchPropertyList, error) {
		return findSearchPropertyList(ctx, sc, dbName, name)
	}
	return []propPage{
		{
			title: "General",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				p, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				return propsheet.NewForm(
					propsheet.Section("Search property list"),
					propsheet.Static("Name", p.Name),
					propsheet.Static("Owner", p.Owner),
					propsheet.Static("Created", formatSQLDate(p.CreateDate)),
					propsheet.Static("Last modified", formatSQLDate(p.ModifyDate)),
					propsheet.Note(fullTextReadOnlyNote),
				), nil, nil
			},
		},
		{
			title: "Properties",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				p, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				props, err := p.Properties(ctx)
				if err != nil {
					return nil, nil, err
				}
				grid := controls.NewDataGrid()
				grid.SetData(searchPropertyHeaders, searchPropertyRows(props))
				f := propsheet.NewForm(
					propsheet.Section("Registered search properties"),
					propsheet.NewGridRow(grid, 14),
				)
				if len(props) == 0 {
					f.Add(propsheet.Note("No property is registered in this list yet."))
				}
				return f, nil, nil
			},
		},
	}
}

var searchPropertyHeaders = []string{"Property Name", "Property Set GUID", "Int ID", "Internal ID", "Description"}

func searchPropertyRows(props []gosmo.SearchProperty) [][]string {
	rows := make([][]string, len(props))
	for k, p := range props {
		rows[k] = []string{p.Name, p.SetGUID, strconv.Itoa(p.IntID), strconv.Itoa(p.ID), p.Description}
	}
	return rows
}

// -- Full-text index -----------------------------------------------------------

func fullTextIndexPropPages(sc *db.ServerConn, dbName, schema, table string) []propPage {
	find := func(ctx context.Context) (*gosmo.FullTextIndex, error) {
		return findFullTextIndex(ctx, sc, dbName, schema, table)
	}
	return []propPage{pageFullTextIndexGeneral(find), pageFullTextIndexColumns(find)}
}

func pageFullTextIndexGeneral(find func(context.Context) (*gosmo.FullTextIndex, error)) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			i, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			if i == nil {
				return noFullTextIndexForm(), nil, nil
			}
			f := propsheet.NewForm(
				propsheet.Section("Index"),
				propsheet.Static("Table", articleObject(i.Schema, i.Table)),
				propsheet.Static("Unique index", i.KeyIndex),
				propsheet.Static("Full-text catalog", i.Catalog),
				propsheet.Static("Filegroup", orNone(i.FileGroup)),
				propsheet.Static("Enabled", boolStr(i.IsEnabled)),
				propsheet.Static("Change tracking", string(i.ChangeTracking)),
				propsheet.Static("Stoplist", fullTextStoplistText(i)),
				propsheet.Static("Search property list", orNone(i.SearchPropertyList)),
			)
			// index_version exists from 2025 only; 0 below it.
			if i.IndexVersion > 0 {
				f.Add(propsheet.Static("Index version", strconv.Itoa(i.IndexVersion)))
			}
			f.Add(propsheet.Section("Last population"))
			crawl := i.CrawlType
			if crawl == "" {
				crawl = "None"
			}
			f.Add(propsheet.Static("Type", crawl))
			f.Add(propsheet.Static("Completed", boolStr(i.CrawlCompleted)))
			f.Add(propsheet.Static("Started", fullTextDate(i.CrawlStart)))
			f.Add(propsheet.Static("Ended", fullTextDate(i.CrawlEnd)))
			f.Add(propsheet.Section("Counters"))
			f.Add(propsheet.Static("Population status", i.PopulateStatus.String()))
			f.Add(propsheet.Static("Item count", strconv.Itoa(i.ItemCount)))
			f.Add(propsheet.Static("Documents processed", strconv.Itoa(i.DocsProcessed)))
			f.Add(propsheet.Static("Documents failed", strconv.Itoa(i.FailCount)))
			f.Add(propsheet.Static("Pending changes", strconv.Itoa(i.PendingChanges)))
			f.Add(propsheet.Section("Running populations"))
			for _, r := range fullTextPopulationRows(ctx, i) {
				f.Add(propsheet.Static(r[0], r[1]))
			}
			f.Add(propsheet.Note(fullTextReadOnlyNote))
			return f, nil, nil
		},
	}
}

// fullTextPopulationRows is an index's running populations as label/value
// pairs, shared with the Details pane. The DMV needs VIEW SERVER STATE, which
// the index's own read does not, so a refusal costs only these rows and says
// which right is missing.
func fullTextPopulationRows(ctx context.Context, i *gosmo.FullTextIndex) [][]string {
	pops, err := i.Populations(ctx)
	if err != nil {
		if denied := accessDeniedText(err); denied != "" {
			return [][]string{{"Populations", "not visible — " + denied}}
		}
		return [][]string{{"Populations", err.Error()}}
	}
	if len(pops) == 0 {
		return [][]string{{"Populations", "None (idle)"}}
	}
	rows := make([][]string, 0, len(pops))
	for k, p := range pops {
		v := p.Type + ", " + p.Status + ", " + strconv.Itoa(p.RangesDone) + " of " +
			strconv.Itoa(p.RangeCount) + " ranges, started " + formatSQLDate(p.StartTime)
		if p.QueuedType != "" {
			v += ", " + p.QueuedType + " queued"
		}
		rows = append(rows, []string{"Population " + strconv.Itoa(k+1), v})
	}
	return rows
}

func pageFullTextIndexColumns(find func(context.Context) (*gosmo.FullTextIndex, error)) propPage {
	return propPage{
		title: "Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			i, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			if i == nil {
				return noFullTextIndexForm(), nil, nil
			}
			grid := controls.NewDataGrid()
			grid.SetData(fullTextColumnHeaders, fullTextColumnRows(i))
			return propsheet.NewForm(
				propsheet.Section("Full-text indexed columns"),
				propsheet.NewGridRow(grid, 12),
			), nil, nil
		},
	}
}
