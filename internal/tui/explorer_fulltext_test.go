package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The full-text family (W15): Storage's three folders or the not-installed
// row, the leaves' loaders and menus, the table's cascade, and the read-only
// pages and Details views. Statement text is gosmo's to test; these pin what
// the tree and the pages do with the answers.

// Match text for gosmo's full-text reads.
const (
	ftInstalledRead   = "FULLTEXTSERVICEPROPERTY('IsFullTextInstalled')"
	ftLanguagesRead   = "FROM sys.fulltext_languages ORDER BY"
	ftDocTypesRead    = "FROM   sys.fulltext_document_types"
	ftCatalogsRead    = "FROM   sys.fulltext_catalogs c"
	ftIndexesRead     = "FROM   sys.fulltext_indexes fi"
	ftColumnsRead     = "FROM   sys.fulltext_index_columns ic"
	ftPopulationsRead = "FROM   sys.dm_fts_index_population"
	ftStoplistsRead   = "FROM   sys.fulltext_stoplists"
	ftStopwordsRead   = "FROM   sys.fulltext_stopwords"
)

// ftInfo answers FullTextInfo: installed or not, no languages, no filters.
func ftInfo(installed bool) []fakeResponse {
	i := int64(0)
	if installed {
		i = 1
	}
	return []fakeResponse{
		{match: ftInstalledRead, cols: 4, rows: [][]driver.Value{{i, int64(0), int64(1), int64(0)}}},
		{match: ftLanguagesRead, cols: 2},
		{match: ftDocTypesRead, cols: 5},
	}
}

func TestStorageListsFullTextFoldersOrSaysNotInstalled(t *testing.T) {
	all := []string{"Full Text Catalogs", "Partition Functions", "Partition Schemes",
		"Full Text Stoplists", "Search Property Lists"}
	for _, tc := range []struct {
		name      string
		responses []fakeResponse
		want      []string
	}{
		{"installed", ftInfo(true), all},
		{"not installed", ftInfo(false),
			[]string{"Partition Functions", "Partition Schemes", fullTextNotInstalledLabel}},
		// Fail open: a failed read keeps the folders, whose own reads
		// then say what is wrong.
		{"read failed", nil, all},
	} {
		sc, _ := newFakeConn(t, tc.responses...)
		node := &explorerNode{data: nodeData{Type: NodeStorage, DBName: "AppDB", conn: sc}}
		children, err := loadStorageChildren(loaderCtx{ctx: context.Background(), sc: sc}, node)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range children {
			got = append(got, c.label)
			if c.data.DBName != "AppDB" {
				t.Errorf("%s: %q.DBName = %q, want AppDB", tc.name, c.label, c.data.DBName)
			}
			if c.label == fullTextNotInstalledLabel && c.data.Type != NodeError {
				t.Errorf("%s: the not-installed row is %v, want NodeError", tc.name, c.data.Type)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: Storage = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ftCatalogRow is one catalog-read row; the second argument is the default
// flag.
func ftCatalogRow(id int64, name string, isDefault bool, indexes, items int64) []driver.Value {
	return []driver.Value{id, name, isDefault, false, "dbo", false, indexes, items, int64(10), int64(2),
		int64(0), int64(0), int64(3600)}
}

// ftIndexRow is one index-read row on catalog cat: stoplistID nil is OFF, 0
// SYSTEM, else a user stoplist named stoplist.
func ftIndexRow(objectID int64, schema, table, cat string, stoplistID any, stoplist string, items int64) []driver.Value {
	start := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	return []driver.Value{objectID, schema, table, "PK_" + table, cat, "", true,
		"AUTO", stoplistID, stoplist, "", int64(1),
		"FULL_CRAWL", true, start, start.Add(time.Minute),
		items, items, int64(0), int64(0), int64(0)}
}

func TestFullTextCatalogsLoaderListsByName(t *testing.T) {
	sc, _ := newFakeConn(t, dbByNameResp("AppDB", 5),
		fakeResponse{match: ftCatalogsRead, db: "AppDB", cols: 13, rows: [][]driver.Value{
			ftCatalogRow(5, "docs_cat", false, 1, 4), ftCatalogRow(6, "main_cat", true, 2, 9),
		}})
	node := &explorerNode{data: nodeData{Type: NodeFullTextCatalogs, DBName: "AppDB"}}
	children, err := loadFullTextCatalogsChildren(loaderCtx{ctx: context.Background(), sc: sc}, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || children[1].label != "main_cat" {
		t.Fatalf("children = %v, want docs_cat and main_cat", children)
	}
	if d := children[1].data; d.Type != NodeFullTextCatalog || d.Name != "main_cat" || d.DBName != "AppDB" {
		t.Errorf("main_cat node = %+v", d)
	}
}

// The catalog's Tables/Views page: the column grid follows the selected
// index — the second here, so a page showing the first's columns fails.
func TestFullTextCatalogTablesFollowTheSelection(t *testing.T) {
	sc, inst := newFakeConn(t, dbByNameResp("AppDB", 5),
		fakeResponse{match: ftCatalogsRead, db: "AppDB", cols: 13, rows: [][]driver.Value{
			ftCatalogRow(6, "main_cat", true, 2, 9),
		}},
		fakeResponse{match: ftIndexesRead, db: "AppDB", cols: 21, rows: [][]driver.Value{
			ftIndexRow(101, "dbo", "Notes", "main_cat", int64(0), "", 4),
			ftIndexRow(102, "sales", "Docs", "main_cat", int64(7), "legal_words", 5),
		}},
		fakeResponse{match: ftColumnsRead, db: "AppDB", cols: 6, rows: [][]driver.Value{
			{int64(101), "Body", "", int64(1033), "English", false},
			{int64(102), "Content", "Ext", int64(1031), "German", true},
		}})
	form, _ := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[1], inst)
	var grids []*controls.DataGrid
	for _, r := range form.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			grids = append(grids, gr.Grid)
		}
	}
	if len(grids) != 2 {
		t.Fatalf("want the tables grid and the columns grid, got %d", len(grids))
	}
	tables, cols := grids[0], grids[1]
	if cols.Row(0)[0] != "Body" {
		t.Errorf("first table's columns = %v, want Body", cols.Row(0))
	}
	gridKey(t, tables, tcell.KeyDown)
	if r := cols.Row(0); r == nil || r[0] != "Content" || r[1] != "Ext" || r[2] != "German" || r[3] != "True" {
		t.Errorf("second table's columns = %v, want Content/Ext/German/True", r)
	}
	if r := tables.Row(1); r[0] != "sales.Docs" || r[1] != "PK_Docs" {
		t.Errorf("second table row = %v", r)
	}
}

// ftIndexResponses answers findFullTextIndex for sales.Docs, plus pops as
// the populations answer.
func ftIndexResponses(pops fakeResponse) []fakeResponse {
	return []fakeResponse{
		dbByNameResp("AppDB", 5),
		{match: "FROM   sys.tables t", db: "AppDB", cols: 12, rows: [][]driver.Value{
			{int64(102), "sales", "Docs", time.Time{}, time.Time{}, false, false, false, false, false, false, false},
		}},
		{match: ftIndexesRead, db: "AppDB", cols: 21, rows: [][]driver.Value{
			ftIndexRow(102, "sales", "Docs", "main_cat", int64(7), "legal_words", 5),
		}},
		{match: ftColumnsRead, db: "AppDB", cols: 6, rows: [][]driver.Value{
			{int64(102), "Content", "Ext", int64(1031), "German", true},
		}},
		pops,
	}
}

// The populations DMV needs VIEW SERVER STATE, which the index read does not:
// a refusal costs only the populations row, and says which right is missing.
func TestFullTextIndexGeneralSurvivesARefusedPopulationRead(t *testing.T) {
	refused := fakeResponse{match: ftPopulationsRead, err: mssql.Error{Number: 300, Class: 14,
		Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}}
	sc, inst := newFakeConn(t, ftIndexResponses(refused)...)
	form, _ := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[0], inst)
	if got := staticValue(t, form, "Stoplist"); got != "legal_words" {
		t.Errorf("Stoplist = %q, want legal_words", got)
	}
	if got := staticValue(t, form, "Full-text catalog"); got != "main_cat" {
		t.Errorf("catalog = %q", got)
	}
	if got := staticValue(t, form, "Populations"); !strings.Contains(got, "VIEW SERVER STATE") {
		t.Errorf("Populations = %q, want it to name VIEW SERVER STATE", got)
	}
}

func TestFullTextIndexGeneralListsRunningPopulations(t *testing.T) {
	pops := fakeResponse{match: ftPopulationsRead, db: "AppDB", cols: 10, rows: [][]driver.Value{
		{"FULL", "Processing", "NONE", "INCREMENTAL", time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
			int64(4), int64(1), int64(2), int64(3), false},
	}}
	sc, inst := newFakeConn(t, ftIndexResponses(pops)...)
	form, _ := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[0], inst)
	got := staticValue(t, form, "Population 1")
	for _, want := range []string{"FULL", "Processing", "1 of 4 ranges", "INCREMENTAL queued"} {
		if !strings.Contains(got, want) {
			t.Errorf("Population 1 = %q, want it to contain %q", got, want)
		}
	}
	cols := plainGrid(t, mustLoad(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[1], inst))
	if r := cols.Row(0); r == nil || r[0] != "Content" {
		t.Errorf("Columns page = %v, want Content", r)
	}
}

func mustLoad(t *testing.T, p propPage, inst *fakeInstance) *propsheet.Form {
	t.Helper()
	f, _ := loadPage(t, p, inst)
	return f
}

func TestFullTextStoplistText(t *testing.T) {
	sc, inst := newFakeConn(t, dbByNameResp("AppDB", 5),
		fakeResponse{match: "FROM   sys.tables t", db: "AppDB", cols: 12, rows: [][]driver.Value{
			{int64(101), "dbo", "Notes", time.Time{}, time.Time{}, false, false, false, false, false, false, false},
		}},
		fakeResponse{match: ftIndexesRead, db: "AppDB", cols: 21, rows: [][]driver.Value{
			ftIndexRow(101, "dbo", "Notes", "main_cat", nil, "", 4),
		}},
		fakeResponse{match: ftColumnsRead, db: "AppDB", cols: 6},
		fakeResponse{match: ftPopulationsRead, db: "AppDB", cols: 10})
	form, _ := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "dbo", "Notes")[0], inst)
	if got := staticValue(t, form, "Stoplist"); got != "<off>" {
		t.Errorf("STOPLIST OFF reads %q, want <off>", got)
	}
	if got := staticValue(t, form, "Populations"); got != "None (idle)" {
		t.Errorf("Populations = %q, want None (idle)", got)
	}
}

func TestFullTextStoplistDetailListsItsWords(t *testing.T) {
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	sc, _ := newFakeConn(t, dbByNameResp("AppDB", 5),
		fakeResponse{match: ftStoplistsRead, db: "AppDB", cols: 5, rows: [][]driver.Value{
			{int64(5), "a_list", "dbo", created, created},
			{int64(7), "legal_words", "dbo", created, created},
		}},
		fakeResponse{match: ftStopwordsRead, db: "AppDB", arg: "7", cols: 3, rows: [][]driver.Value{
			{"hereby", "English", int64(1033)},
		}})
	node := &explorerNode{data: nodeData{Type: NodeFullTextStoplist, DBName: "AppDB", Name: "legal_words"}}
	_, rows, err := fetchNodeDetails(context.Background(), sc, node, new([]nodeData))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0] != "hereby" || rows[0][2] != "1033" {
		t.Errorf("stopwords = %v, want hereby/English/1033", rows)
	}
}

// The catalogs folder's Details applies the folder's filter, as its tree
// does, and maps each row to its catalog.
func TestFullTextCatalogsFolderDetailFilters(t *testing.T) {
	sc, _ := newFakeConn(t, dbByNameResp("AppDB", 5),
		fakeResponse{match: ftCatalogsRead, db: "AppDB", cols: 13, rows: [][]driver.Value{
			ftCatalogRow(5, "docs_cat", false, 1, 4), ftCatalogRow(6, "main_cat", true, 2, 9),
		}})
	node := &explorerNode{data: nodeData{Type: NodeFullTextCatalogs, DBName: "AppDB",
		Filter: &nodeFilter{criteria: []filterCriterion{{prop: filterProps(NodeFullTextCatalogs)[0], op: opContains, value: "main"}}}}}
	var objs []nodeData
	_, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0] != "main_cat" || rows[0][1] != "True" {
		t.Fatalf("rows = %v, want main_cat alone, default", rows)
	}
	if len(objs) != 1 || objs[0].Type != NodeFullTextCatalog || objs[0].Name != "main_cat" {
		t.Errorf("objs = %+v", objs)
	}
}

func TestFullTextMenusOpenProperties(t *testing.T) {
	var newQuery, refresh controls.MenuItem
	find := func(items []controls.MenuItem, label string) *controls.MenuItem {
		for i := range items {
			if items[i].Label == label {
				return &items[i]
			}
		}
		return nil
	}
	for name, items := range map[string][]controls.MenuItem{
		"catalog":       fullTextCatalogMenuItems(nil, nil, &explorerNode{}, newQuery, refresh),
		"stoplist":      fullTextStoplistMenuItems(nil, nil, &explorerNode{}, newQuery, refresh),
		"property list": searchPropertyListMenuItems(nil, nil, &explorerNode{}, newQuery, refresh),
	} {
		if it := find(items, "Properties..."); it == nil || it.Action == nil {
			t.Errorf("%s: no Properties...", name)
		}
	}
	casc := find(tableMenuItems(nil, nil, &explorerNode{}, newQuery, refresh), "Full-Text index")
	if casc == nil || find(casc.Sub, "Properties...") == nil {
		t.Errorf("table menu: Full-Text index cascade with Properties... missing: %+v", casc)
	}
}

// A table without a full-text index is an answer, not a load failure: each
// page says so instead of offering "Press F5 to retry".
func TestFullTextIndexPagesOnATableWithoutOne(t *testing.T) {
	for k := range fullTextIndexPropPages(nil, "", "", "") {
		sc, inst := newFakeConn(t, dbByNameResp("AppDB", 5),
			fakeResponse{match: "FROM   sys.tables t", db: "AppDB", cols: 12, rows: [][]driver.Value{
				{int64(103), "dbo", "Plain", time.Time{}, time.Time{}, false, false, false, false, false, false, false},
			}},
			fakeResponse{match: ftIndexesRead, db: "AppDB", cols: 21})
		page := fullTextIndexPropPages(sc, "AppDB", "dbo", "Plain")[k]
		form, _ := loadPage(t, page, inst)
		if got := staticValuesJoined(form); got != "" {
			t.Errorf("%s: shows values %q for a table with no index", page.title, got)
		}
		if got := formNotes(form); !strings.Contains(got, "no full-text index") {
			t.Errorf("%s: notes = %q, want one saying there is no full-text index", page.title, got)
		}
	}
}
