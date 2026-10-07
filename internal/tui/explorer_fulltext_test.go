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
	gosmo "github.com/radix29/gosmo"
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
	ftPropListsRead   = "FROM   sys.registered_search_property_lists"
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
	form, _ := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[2], inst)
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
		ftStoplistsResp("legal_words", "other_words"),
		ftPropListsResp("doc_props"),
		ftTableColumnsResp(ftCol{"ID", "int", false}, ftCol{"Content", "varbinary", true},
			ftCol{"Ext", "nvarchar", true}, ftCol{"Title", "nvarchar", true}),
		{match: ftInstalledRead, cols: 4, rows: [][]driver.Value{{int64(1), int64(0), int64(1), int64(0)}}},
		{match: ftLanguagesRead, cols: 2, rows: [][]driver.Value{
			{int64(1033), "English"}, {int64(1031), "German"}, {int64(0), "Neutral"},
		}},
		{match: ftDocTypesRead, cols: 5},
	}
}

// ftCol is one column ftTableColumnsResp answers with.
type ftCol struct {
	name, typ string
	nullable  bool
}

// ftTableColumnsResp answers Table.Columns in columnSelect's scan order;
// only name, type and nullability matter to the full-text pages.
func ftTableColumnsResp(cols ...ftCol) fakeResponse {
	rows := make([][]driver.Value, len(cols))
	for k, c := range cols {
		rows[k] = []driver.Value{
			c.name, int64(k + 1),
			c.typ, int64(-1), int64(0), int64(0),
			c.nullable, false, false,
			"", "", "",
			false, "",
			int64(0), int64(0),
			false,
			"sys", false,
			false, false,
			false, false,
			"",
			int64(0), false, false,
			int64(0), false,
			"", "", "",
			"", "", false, int64(0), "",
		}
	}
	return fakeResponse{match: "FROM   sys.columns c", db: "AppDB", cols: 37, rows: rows}
}

// ftStoplistsResp answers FullTextStoplists with names, ids from 1.
func ftStoplistsResp(names ...string) fakeResponse {
	rows := make([][]driver.Value, len(names))
	for k, n := range names {
		rows[k] = []driver.Value{int64(k + 1), n, "dbo", time.Time{}, time.Time{}}
	}
	return fakeResponse{match: ftStoplistsRead, db: "AppDB", cols: 5, rows: rows}
}

// ftPropListsResp answers SearchPropertyLists with names, ids from 1.
func ftPropListsResp(names ...string) fakeResponse {
	rows := make([][]driver.Value, len(names))
	for k, n := range names {
		rows[k] = []driver.Value{int64(k + 1), n, "dbo", time.Time{}, time.Time{}}
	}
	return fakeResponse{match: ftPropListsRead, db: "AppDB", cols: 5, rows: rows}
}

// The populations DMV needs VIEW SERVER STATE, which the index read does not:
// a refusal costs only the populations row, and says which right is missing.
func TestFullTextIndexGeneralSurvivesARefusedPopulationRead(t *testing.T) {
	refused := fakeResponse{match: ftPopulationsRead, err: mssql.Error{Number: 300, Class: 14,
		Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}}
	sc, inst := newFakeConn(t, ftIndexResponses(refused)...)
	form, _ := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[0], inst)
	if got := selectRow(t, form, "Stoplist").Value(); got != "legal_words" {
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
		fakeResponse{match: ftPopulationsRead, db: "AppDB", cols: 10},
		ftStoplistsResp(), ftPropListsResp())
	form, _ := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "dbo", "Notes")[0], inst)
	if got := selectRow(t, form, "Stoplist").Value(); got != "<off>" {
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

// fullTextLeaves are the three Storage leaf types W17 wires for Delete and
// Script, with the class word their per-securable CONTROL is keyed by.
// view is the catalog view its script reads first, which tells one family's
// Scripter method from a sibling's.
var fullTextLeaves = []struct {
	nt   NodeType
	kind gosmo.DatabaseSecurableKind
	noun string
	drop string
	view string
}{
	{NodeFullTextCatalog, gosmo.DatabaseSecurableFullTextCatalog, "Full-Text Catalog", "DROP FULLTEXT CATALOG [f1]", "sys.fulltext_catalogs"},
	{NodeFullTextStoplist, gosmo.DatabaseSecurableFullTextStoplist, "Full-Text Stoplist", "DROP FULLTEXT STOPLIST [f1];", "sys.fulltext_stoplists"},
	{NodeSearchPropertyList, gosmo.DatabaseSecurableSearchPropertyList, "Search Property List", "DROP SEARCH PROPERTY LIST [f1];", "sys.registered_search_property_lists"},
}

// Each family's script verbs call its own Scripter method: the wiring takes
// the method as a value, so a sibling's compiles and scripts the wrong family.
// Asserted by the catalog view the script reads, the object itself unscripted.
func TestFullTextScriptsReadTheirOwnFamily(t *testing.T) {
	for _, l := range fullTextLeaves {
		for _, v := range scriptables[l.nt].verbs {
			sc, inst := newFakeConn(t, dbByNameResp("appdb", 5))
			v.gen(t.Context(), sc, nodeData{Type: l.nt, DBName: "appdb", Name: "f1"})
			if len(inst.Reads(l.view)) == 0 {
				t.Errorf("%s %s read nothing from %s", l.noun, v.label, l.view)
			}
			for _, other := range fullTextLeaves {
				if other.nt != l.nt && len(inst.Reads(other.view)) != 0 {
					t.Errorf("%s %s read %s, a sibling family's view", l.noun, v.label, other.view)
				}
			}
		}
	}
}

// Each leaf offers Script as (CREATE and DROP, no ALTER), Delete and
// Properties, and no Rename: none of the three has a statement for one.
func TestFullTextLeavesScriptAndDelete(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for _, l := range fullTextLeaves {
		leaf := &explorerNode{data: nodeData{Type: l.nt, DBName: "appdb", Name: "f1", conn: sc}}
		got := labelsOf(a.contextMenuItemsForNode(leaf))
		for _, want := range []string{"Script " + l.noun + " as", "Delete...", "Properties..."} {
			if !slices.Contains(got, want) {
				t.Errorf("%s: menu %q has no %q", l.noun, got, want)
			}
		}
		if slices.Contains(got, "Rename...") {
			t.Errorf("%s: menu %q offers a rename SQL Server has no statement for", l.noun, got)
		}
		items := (&App{}).scriptMenuItems(opNode(l.nt, "", "f1", "appdb"))
		if len(items) == 0 {
			t.Fatalf("%s offers no Script item", l.noun)
		}
		if got, want := labelsOf(items[0].Sub), []string{"CREATE To", "DROP To", "DROP And CREATE To"}; !slices.Equal(got, want) {
			t.Errorf("%s: script verbs = %v, want %v", l.noun, got, want)
		}
		op := objectOps[l.nt]
		if op.rename != nil || op.warning == "" || op.solo {
			t.Errorf("%s: objectOp = rename %v, warning %q, solo %v; want no rename, a warning, batchable",
				l.noun, op.rename != nil, op.warning, op.solo)
		}
	}
}

// Each drop is its family's one statement through the name-only handle: no
// sys.databases read, nothing in master.
func TestFullTextDropsSendOneStatement(t *testing.T) {
	for _, l := range fullTextLeaves {
		t.Run(l.noun, func(t *testing.T) {
			sc, inst := newFakeConn(t)
			if err := objectOps[l.nt].drop(t.Context(), sc, nodeData{Type: l.nt, DBName: "appdb", Name: "f1"}); err != nil {
				t.Fatalf("drop: %v", err)
			}
			if reads := inst.Reads("sys.databases"); len(reads) != 0 {
				t.Errorf("drop read sys.databases:\n%s", strings.Join(reads, "\n"))
			}
			assertOneStatementIn(t, inst, "appdb", l.drop)
			assertNoStatementsIn(t, inst, "master")
		})
	}
}

// TestFullTextDeleteGateMatchesWhatTheServerAllowed is the probed DROP column
// (2026-10-07, majors 14 and 17, identical for all three families), each row
// one WITHOUT LOGIN user as gosmo's probe reads it back. ALTER or TAKE
// OWNERSHIP on the object read CONTROL 0 and hold nothing wider, and were
// refused; ALTER ANY SCHEMA and CREATE FULLTEXT CATALOG read 0 for every right
// in the set.
func TestFullTextDeleteGateMatchesWhatTheServerAllowed(t *testing.T) {
	for _, l := range fullTextLeaves {
		for _, tc := range []struct {
			name    string
			granted []string
			control bool
			want    bool
		}{
			{"nothing, ALTER ANY SCHEMA, CREATE FULLTEXT CATALOG, or ALTER on it", nil, false, false},
			{"its owner", nil, true, true},
			{"CONTROL on it", nil, true, true},
			{"ALTER ANY FULLTEXT CATALOG, or db_ddladmin", []string{"ALTER ANY FULLTEXT CATALOG"}, false, true},
			{"ALTER on the database", []string{"ALTER", "ALTER ANY FULLTEXT CATALOG"}, false, true},
			{"CONTROL on the database", []string{"ALTER", "CONTROL", "ALTER ANY FULLTEXT CATALOG"}, true, true},
		} {
			var denied []string
			for _, n := range []string{"ALTER", "CONTROL", "ALTER ANY FULLTEXT CATALOG"} {
				if !slices.Contains(tc.granted, n) {
					denied = append(denied, n)
				}
			}
			responses := withSecurableAnswers(capabilityResponses(true, nil, nil, tc.granted, denied),
				map[string]bool{gosmo.DatabaseSecurableKey(l.kind, "", "f1"): tc.control})
			sc, _ := newFakeConn(t, responses...)
			sc.ProbeCapabilities()
			sc.DatabaseCapabilities(context.Background(), "appdb")
			node := &explorerNode{data: nodeData{Type: l.nt, DBName: "appdb", Name: "f1", conn: sc}}
			var found bool
			for _, it := range (&App{}).objectOpsMenuItems(node) {
				if it.Label != "Delete..." {
					continue
				}
				found = true
				if got := it.Enabled == nil || it.Enabled(); got != tc.want {
					t.Errorf("%s, %s: Delete enabled = %v, want %v", l.noun, tc.name, got, tc.want)
				}
				if !tc.want && it.Note != "needs ALTER ANY FULLTEXT CATALOG" {
					t.Errorf("%s, %s: withheld Delete's note = %q", l.noun, tc.name, it.Note)
				}
			}
			if !found {
				t.Fatalf("%s, %s: no Delete item", l.noun, tc.name)
			}
		}
	}
}
