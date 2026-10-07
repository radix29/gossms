package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// W19: the full-text pages that write, and the four New dialogs. The
// harness shows each page asked the right things and built the right
// statements; the statements' text is gosmo's tests, and acceptance was a
// live run (docs/phase5-plan.md § W19).

// ftLanguagesResps answers FullTextInfo with three languages, English first
// in the list only by name order as the server gives them.
func ftLanguagesResps() []fakeResponse {
	return []fakeResponse{
		{match: ftInstalledRead, cols: 4, rows: [][]driver.Value{{int64(1), int64(0), int64(1), int64(0)}}},
		{match: ftLanguagesRead, cols: 2, rows: [][]driver.Value{
			{int64(1033), "English"}, {int64(1031), "German"}, {int64(0), "Neutral"},
		}},
		{match: ftDocTypesRead, cols: 5},
	}
}

func ftCatalogResps(isDefault bool) []fakeResponse {
	return []fakeResponse{dbByNameResp("AppDB", 5),
		{match: ftCatalogsRead, db: "AppDB", cols: 13, rows: [][]driver.Value{
			ftCatalogRow(5, "docs_cat", !isDefault, 1, 4), ftCatalogRow(6, "main_cat", isDefault, 2, 9),
		}}}
}

func TestFullTextCatalogGeneralSetsTheDefault(t *testing.T) {
	sc, inst := newFakeConn(t, ftCatalogResps(false)...)
	form, apply := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[0], inst)
	checkRow(t, form, "Set as default catalog").Edit(true)
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, []string{"ALTER FULLTEXT CATALOG [main_cat] AS DEFAULT"}) {
		t.Errorf("statements = %q", got)
	}
}

// The default catalog has nothing to unset — no statement does — so its page
// shows the fact and writes nothing.
func TestFullTextCatalogGeneralOfTheDefaultWritesNothing(t *testing.T) {
	sc, inst := newFakeConn(t, ftCatalogResps(true)...)
	form, apply := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[0], inst)
	if apply != nil {
		t.Error("the default catalog's General page has an apply")
	}
	if got := staticValue(t, form, "Default catalog"); got != "Yes" {
		t.Errorf("Default catalog = %q", got)
	}
}

func TestFullTextCatalogMaintenance(t *testing.T) {
	t.Run("optimize", func(t *testing.T) {
		sc, inst := newFakeConn(t, ftCatalogResps(false)...)
		form, apply := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[1], inst)
		editRadio(t, form, "Catalog action", "Optimize (reorganize)")
		if form.ApplyConfirm() != "" {
			t.Error("Optimize asks for confirmation")
		}
		if err := apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := inst.StatementsIn("AppDB"); !slices.Equal(got, []string{"ALTER FULLTEXT CATALOG [main_cat] REORGANIZE"}) {
			t.Errorf("statements = %q", got)
		}
	})
	// Choosing an accent selects Rebuild — the only statement that applies
	// one — and the rebuild is confirmed.
	t.Run("accent rebuilds", func(t *testing.T) {
		sc, inst := newFakeConn(t, ftCatalogResps(false)...)
		form, apply := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[1], inst)
		editSelect(t, form, "Accent sensitivity", "Insensitive")
		if got := radioRow(t, form, "Catalog action").Selected(); got != catalogActionRebuild {
			t.Fatalf("action = %d after choosing an accent, want Rebuild", got)
		}
		if !strings.Contains(form.ApplyConfirm(), "Rebuilding full-text catalog \"main_cat\"") {
			t.Errorf("confirm = %q", form.ApplyConfirm())
		}
		if err := apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := []string{"ALTER FULLTEXT CATALOG [main_cat] REBUILD WITH ACCENT_SENSITIVITY = OFF"}
		if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
			t.Errorf("statements = %q, want %q", got, want)
		}
	})
	// And leaving Rebuild drops the accent, so none sits on the page unsent.
	t.Run("leaving rebuild drops the accent", func(t *testing.T) {
		sc, inst := newFakeConn(t, ftCatalogResps(false)...)
		form, _ := loadPage(t, fullTextCatalogPropPages(sc, "AppDB", "main_cat")[1], inst)
		editSelect(t, form, "Accent sensitivity", "Sensitive")
		radioRow(t, form, "Catalog action").Edit(catalogActionNone) // back to the loaded value
		if got := selectRow(t, form, "Accent sensitivity").Selected(); got != 0 {
			t.Errorf("accent = %d after leaving Rebuild, want Keep current", got)
		}
	})
}

// stoplistResps answers the stoplist legal_words (id 7) with words, and the
// instance's languages.
func stoplistResps(words ...[]driver.Value) []fakeResponse {
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return append([]fakeResponse{dbByNameResp("AppDB", 5),
		{match: ftStoplistsRead, db: "AppDB", cols: 5, rows: [][]driver.Value{
			{int64(5), "a_list", "dbo", created, created},
			{int64(7), "legal_words", "dbo", created, created},
		}},
		{match: ftStopwordsRead, db: "AppDB", arg: "7", cols: 3, rows: words},
	}, ftLanguagesResps()...)
}

var (
	swHereby  = []driver.Value{"hereby", "English", int64(1033)}
	swWhereas = []driver.Value{"whereas", "English", int64(1033)}
	swDer     = []driver.Value{"der", "German", int64(1031)}
	swDie     = []driver.Value{"die", "German", int64(1031)}
)

func loadStopwords(t *testing.T) (*propsheet.Form, propApply, *fakeInstance) {
	t.Helper()
	sc, inst := newFakeConn(t, stoplistResps(swHereby, swWhereas, swDer, swDie)...)
	form, apply := loadPage(t, fullTextStoplistPropPages(sc, "AppDB", "legal_words")[1], inst)
	return form, apply, inst
}

func TestStopwordsRemoveAndAdd(t *testing.T) {
	form, apply, inst := loadStopwords(t)
	selectGridRow(t, firstGrid(t, form), 0, "whereas") // not the first row
	clickButton(t, form, "Remove")
	textRow(t, form, "Stopword").SetValue("thereof")
	editSelectUntracked(t, form, "Language", "German (1031)")
	clickButton(t, form, "Add")
	if !form.Dirty() {
		t.Fatal("page not dirty after Remove and Add")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER FULLTEXT STOPLIST [legal_words] DROP N'whereas' LANGUAGE 1033;",
		"ALTER FULLTEXT STOPLIST [legal_words] ADD N'thereof' LANGUAGE 1031;",
	}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
		t.Errorf("statements:\n%q\nwant\n%q", got, want)
	}
}

// Every word of a language going is one DROP ALL LANGUAGE, not a DROP each.
func TestStopwordsRemoveLanguageIsOneStatement(t *testing.T) {
	form, apply, inst := loadStopwords(t)
	selectGridRow(t, firstGrid(t, form), 0, "die")
	clickButton(t, form, "Remove Language")
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"ALTER FULLTEXT STOPLIST [legal_words] DROP ALL LANGUAGE 1031;"}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
		t.Errorf("statements = %q, want %q", got, want)
	}
}

// Every word going is DROP ALL.
func TestStopwordsRemovingEverythingIsDropAll(t *testing.T) {
	form, apply, inst := loadStopwords(t)
	selectGridRow(t, firstGrid(t, form), 0, "hereby")
	clickButton(t, form, "Remove Language")
	selectGridRow(t, firstGrid(t, form), 0, "der")
	clickButton(t, form, "Remove Language")
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"ALTER FULLTEXT STOPLIST [legal_words] DROP ALL;"}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
		t.Errorf("statements = %q, want %q", got, want)
	}
}

// A word already in the language is refused without a row — case does not
// make it another word — and the same word in another language is not.
func TestStopwordsAddRefusesADuplicate(t *testing.T) {
	form, _, _ := loadStopwords(t)
	textRow(t, form, "Stopword").SetValue("HEREBY")
	editSelectUntracked(t, form, "Language", "English (1033)")
	clickButton(t, form, "Add")
	if form.Dirty() {
		t.Error("a duplicate English word was added")
	}
	textRow(t, form, "Stopword").SetValue("hereby")
	editSelectUntracked(t, form, "Language", "German (1031)")
	clickButton(t, form, "Add")
	if !form.Dirty() {
		t.Error("the same word in German was refused")
	}
}

// editSelectUntracked picks value on a picker the page does not track (an
// Add button's input), which editSelect would call a skipped edit.
func editSelectUntracked(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	row := selectRow(t, f, label)
	i := slices.Index(row.Items(), value)
	if i < 0 {
		t.Fatalf("row %q offers %q, not %q", label, row.Items(), value)
	}
	row.Edit(i)
}

func propListResps() []fakeResponse {
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return []fakeResponse{dbByNameResp("AppDB", 5),
		{match: ftPropListsRead, db: "AppDB", cols: 5, rows: [][]driver.Value{
			{int64(3), "doc_props", "dbo", created, created},
		}},
		{match: "FROM   sys.registered_search_properties", db: "AppDB", cols: 5, rows: [][]driver.Value{
			{"Author", "F29F85E0-4FF9-1068-AB91-08002B27B3D9", int64(4), "", int64(1)},
			{"Title", "F29F85E0-4FF9-1068-AB91-08002B27B3D9", int64(2), "the title", int64(2)},
		}},
	}
}

func TestSearchPropertiesRemoveAndRegister(t *testing.T) {
	sc, inst := newFakeConn(t, propListResps()...)
	form, apply := loadPage(t, searchPropertyListPropPages(sc, "AppDB", "doc_props")[1], inst)
	selectGridRow(t, firstGrid(t, form), 0, "Title")
	clickButton(t, form, "Remove")
	textRow(t, form, "Property name").SetValue("Subject")
	textRow(t, form, "Property set GUID").SetValue("{F29F85E0-4FF9-1068-AB91-08002B27B3D9}")
	textRow(t, form, "Property int ID").SetValue("3")
	clickButton(t, form, "Add")
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER SEARCH PROPERTY LIST [doc_props] DROP N'Title';",
		"ALTER SEARCH PROPERTY LIST [doc_props] ADD N'Subject' WITH (PROPERTY_SET_GUID = N'F29F85E0-4FF9-1068-AB91-08002B27B3D9', PROPERTY_INT_ID = 3);",
	}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
		t.Errorf("statements:\n%q\nwant\n%q", got, want)
	}
}

func TestSearchPropertyInputRefusesWhatTheServerWould(t *testing.T) {
	const guid = "F29F85E0-4FF9-1068-AB91-08002B27B3D9"
	for _, tc := range []struct{ name, guid, id string }{
		{"", guid, "2"},
		{"Title", "F29F85E0-4FF9-1068-AB91", "2"},
		{"Title", guid, "two"},
		{"Title", guid, "-1"},
	} {
		if _, err := searchPropertyInput(tc.name, tc.guid, tc.id, ""); err == nil {
			t.Errorf("%+v accepted", tc)
		}
	}
	p, err := searchPropertyInput(" Title ", "{"+guid+"}", " 2 ", " d ")
	if err != nil || p.Name != "Title" || p.SetGUID != guid || p.IntID != 2 || p.Description != "d" {
		t.Errorf("good input = %+v, %v", p, err)
	}
}

// The index General page: a new stoplist and property list, and tracking
// off with changes pending — confirmed for both reasons.
func TestFullTextIndexGeneralWrites(t *testing.T) {
	sc, inst := newFakeConn(t, ftIndexResponses(fakeResponse{match: ftPopulationsRead, db: "AppDB", cols: 10})...)
	form, apply := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[0], inst)
	editSelect(t, form, "Change tracking", "OFF")
	editSelect(t, form, "Stoplist", "<system>")
	editSelect(t, form, "Search property list", "doc_props")
	if err := form.Validate(); err != nil {
		t.Fatalf("validate on an unprobed connection (fails open): %v", err)
	}
	if c := form.ApplyConfirm(); !strings.Contains(c, "full population") {
		t.Errorf("confirm = %q, want it to name the full population", c)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER FULLTEXT INDEX ON [sales].[Docs] SET CHANGE_TRACKING = OFF",
		"ALTER FULLTEXT INDEX ON [sales].[Docs] SET STOPLIST = SYSTEM",
		"ALTER FULLTEXT INDEX ON [sales].[Docs] SET SEARCH PROPERTY LIST = [doc_props]",
	}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
		t.Errorf("statements:\n%q\nwant\n%q", got, want)
	}
}

// Untouched, the General page sends nothing — the loaded stoplist included.
func TestFullTextIndexGeneralUntouchedWritesNothing(t *testing.T) {
	sc, inst := newFakeConn(t, ftIndexResponses(fakeResponse{match: ftPopulationsRead, db: "AppDB", cols: 10})...)
	_, apply := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[0], inst)
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertNoStatementsIn(t, inst, "AppDB")
}

func TestFullTextIndexColumnsDropThenAdd(t *testing.T) {
	sc, inst := newFakeConn(t, ftIndexResponses(fakeResponse{match: ftPopulationsRead, db: "AppDB", cols: 10})...)
	form, apply := loadPage(t, fullTextIndexPropPages(sc, "AppDB", "sales", "Docs")[1], inst)
	selectGridRow(t, firstGrid(t, form), 0, "Content")
	clickButton(t, form, "Remove")
	editSelectUntracked(t, form, "Column", "Title")
	editSelectUntracked(t, form, "Language", "German (1031)")
	clickButton(t, form, "Add")
	if err := apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER FULLTEXT INDEX ON [sales].[Docs] DROP ([Content])",
		"ALTER FULLTEXT INDEX ON [sales].[Docs] ADD ([Title] LANGUAGE 1031)",
	}
	if got := inst.StatementsIn("AppDB"); !slices.Equal(got, want) {
		t.Errorf("statements:\n%q\nwant\n%q", got, want)
	}
}

// A document column needs its type column; a text one may not have one.
func TestFullTextColumnEditorTypeColumnRules(t *testing.T) {
	cols := []*gosmo.Column{
		{Name: "ID", DataType: "int"}, {Name: "Doc", DataType: "varbinary"},
		{Name: "Ext", DataType: "nvarchar"}, {Name: "Body", DataType: "nvarchar"},
	}
	ed := newFullTextColumnEditor("", cols, []gosmo.FullTextLanguage{{LCID: 1033, Name: "English"}}, nil)
	f := propsheet.NewForm(ed.rows("")...)
	if got := selectRow(t, f, "Column").Items(); !slices.Equal(got, []string{"Doc", "Ext", "Body"}) {
		t.Errorf("eligible columns = %q", got)
	}
	editSelectUntracked(t, f, "Column", "Doc")
	clickButton(t, f, "Add")
	if len(ed.specs()) != 0 {
		t.Error("a varbinary column was added without a type column")
	}
	editSelectUntracked(t, f, "Type column", "Ext")
	clickButton(t, f, "Add")
	editSelectUntracked(t, f, "Column", "Body")
	clickButton(t, f, "Add") // still Ext: refused, Body is text
	editSelectUntracked(t, f, "Type column", propertyListNone)
	clickButton(t, f, "Add")
	got := ed.specs()
	if len(got) != 2 || got[0] != (gosmo.FullTextIndexColumnSpec{Name: "Doc", TypeColumn: "Ext"}) ||
		got[1] != (gosmo.FullTextIndexColumnSpec{Name: "Body"}) {
		t.Errorf("specs = %+v", got)
	}
}

// REFERENCES on a stoplist, list or catalog is asked of the per-securable
// probe: a measured 0 refuses, a 1 or no answer (not probed) does not.
func TestFullTextReferencesRefusal(t *testing.T) {
	responses := withSecurablePermAnswers(capabilityResponses(true, nil, nil, nil, nil), "REFERENCES",
		map[string]bool{
			gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableFullTextStoplist, "", "refd"):  true,
			gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableFullTextStoplist, "", "plain"): false,
		})
	sc, _ := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	if err := fullTextReferencesRefusal(sc, "appdb", gate.ReferencesOnFullTextStoplist, "stoplist", "plain"); err == nil ||
		!strings.Contains(err.Error(), "needs REFERENCES") {
		t.Errorf("REFERENCES 0: %v", err)
	}
	if err := fullTextReferencesRefusal(sc, "appdb", gate.ReferencesOnFullTextStoplist, "stoplist", "refd"); err != nil {
		t.Errorf("REFERENCES 1: %v", err)
	}
}

// -- New dialogs ----------------------------------------------------------------

// buildNew runs a New dialog's build the way show would, minus the
// connection's reads.
func buildNew[P any](t *testing.T, d *newObjectDialog[P], pages int, build func()) {
	t.Helper()
	sc, _ := newFakeConn(t)
	d.sc = sc
	d.forms = make([]*propsheet.Form, pages)
	d.applyFns = make([]propApply, pages)
	build()
}

// scriptRun runs preflight and every step under Script Changes.
func scriptRun[P any](t *testing.T, d *newObjectDialog[P]) []string {
	t.Helper()
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	for _, fn := range d.applyFns {
		if err := fn(ctx); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	// Each statement names its database first; the statement itself is
	// what these tests compare.
	out := script.Statements()
	for i, st := range out {
		out[i] = strings.TrimPrefix(st, "USE [AppDB];\nGO\n")
	}
	return out
}

func TestNewFullTextCatalog(t *testing.T) {
	d := &NewFullTextCatalogDialog{}
	d.dbName = "AppDB"
	buildNew(t, &d.newObjectDialog, 1, func() {
		d.buildPages(&nftCatalogPrefetch{existingNames: newNameSet("", "Docs_Cat"),
			owners: []string{fullTextDefaultOwner, "dbo", "ft_owner"}})
	})
	f := d.forms[0]
	editText(t, f, "Catalog name", "docs_cat")
	if err := d.preflight(); err == nil {
		t.Error("a name taken under the collation was accepted")
	}
	editText(t, f, "Catalog name", "new_cat")
	editSelect(t, f, "Owner", "ft_owner")
	checkRow(t, f, "Set as default catalog").Edit(true)
	editSelect(t, f, "Accent sensitivity", "Sensitive")
	want := []string{"CREATE FULLTEXT CATALOG [new_cat] WITH ACCENT_SENSITIVITY = ON AS DEFAULT AUTHORIZATION [ft_owner]"}
	if got := scriptRun(t, &d.newObjectDialog); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNewFullTextStoplistStartingPoints(t *testing.T) {
	build := func() *NewFullTextStoplistDialog {
		d := &NewFullTextStoplistDialog{}
		d.dbName = "AppDB"
		buildNew(t, &d.newObjectDialog, 1, func() {
			d.buildPages(&nftStoplistPrefetch{existingNames: newNameSet(""),
				stoplists: []string{"a_list", "legal_words"}, owners: []string{fullTextDefaultOwner}})
		})
		editText(t, d.forms[0], "Stoplist name", "s2")
		return d
	}
	for option, want := range map[string]string{
		"The system stoplist":  "CREATE FULLTEXT STOPLIST [s2] FROM SYSTEM STOPLIST;",
		"An empty stoplist":    "CREATE FULLTEXT STOPLIST [s2];",
		"An existing stoplist": "CREATE FULLTEXT STOPLIST [s2] FROM [legal_words];",
	} {
		d := build()
		if option != "The system stoplist" { // the default
			editRadio(t, d.forms[0], "Start from", option)
		}
		if option == "An existing stoplist" {
			editSelect(t, d.forms[0], "Existing stoplist", "legal_words")
		}
		if got := scriptRun(t, &d.newObjectDialog); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: got %q, want %q", option, got, want)
		}
	}
}

func TestNewSearchPropertyListCopy(t *testing.T) {
	d := &NewSearchPropertyListDialog{}
	d.dbName = "AppDB"
	buildNew(t, &d.newObjectDialog, 1, func() {
		d.buildPages(&nftPropertyListPrefetch{existingNames: newNameSet(""), lists: []string{"doc_props"},
			owners: []string{fullTextDefaultOwner}})
	})
	editText(t, d.forms[0], "List name", "p2")
	editRadio(t, d.forms[0], "Start from", "An existing list")
	want := []string{"CREATE SEARCH PROPERTY LIST [p2] FROM [doc_props];"}
	if got := scriptRun(t, &d.newObjectDialog); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func newFullTextIndexTestDialog(t *testing.T, pf *nftIndexPrefetch) *NewFullTextIndexDialog {
	t.Helper()
	d := &NewFullTextIndexDialog{dbName: "AppDB", schema: "sales", table: "Docs"}
	buildNew(t, &d.newObjectDialog, 2, func() { d.buildPages(pf) })
	return d
}

func ftIndexPrefetch() *nftIndexPrefetch {
	return &nftIndexPrefetch{
		keyIndexes: []string{"PK_Docs", "UQ_Docs_Code"},
		catalogs:   []string{"docs_cat", "main_cat"}, defaultCat: 1,
		fileGroups: []string{fullTextDefaultFileGroup, "PRIMARY", "FT"},
		stoplists:  []string{stoplistOffItem, stoplistSystemItem, "legal_words"},
		lists:      []string{propertyListNone, "doc_props"},
		columns: []*gosmo.Column{{Name: "ID", DataType: "int"}, {Name: "Title", DataType: "nvarchar"},
			{Name: "Body", DataType: "nvarchar"}},
		langs: []gosmo.FullTextLanguage{{LCID: 1033, Name: "English"}},
	}
}

func TestNewFullTextIndexStatement(t *testing.T) {
	d := newFullTextIndexTestDialog(t, ftIndexPrefetch())
	g, c := d.forms[0], d.forms[1]
	editSelectUntracked(t, c, "Column", "Body")
	editSelectUntracked(t, c, "Language", "English (1033)")
	clickButton(t, c, "Add")
	editSelect(t, g, "Unique index", "UQ_Docs_Code")
	editSelect(t, g, "Filegroup", "FT")
	editSelect(t, g, "Change tracking", "MANUAL")
	editSelect(t, g, "Stoplist", "legal_words")
	editSelect(t, g, "Search property list", "doc_props")
	want := []string{"CREATE FULLTEXT INDEX ON [sales].[Docs] ([Body] LANGUAGE 1033) KEY INDEX [UQ_Docs_Code] " +
		"ON ([main_cat], FILEGROUP [FT]) WITH (CHANGE_TRACKING = MANUAL, STOPLIST = [legal_words], SEARCH PROPERTY LIST = [doc_props])"}
	if got := scriptRun(t, &d.newObjectDialog); !slices.Equal(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestNewFullTextIndexPreflightRefusals(t *testing.T) {
	withBody := func(d *NewFullTextIndexDialog) {
		editSelectUntracked(t, d.forms[1], "Column", "Body")
		clickButton(t, d.forms[1], "Add")
	}
	for name, tc := range map[string]struct {
		pf   func(*nftIndexPrefetch)
		edit func(*NewFullTextIndexDialog)
		want string
	}{
		"existing index": {func(p *nftIndexPrefetch) { p.existing = &gosmo.FullTextIndex{} }, withBody, "already has"},
		"no key index":   {func(p *nftIndexPrefetch) { p.keyIndexes = nil }, withBody, "no unique"},
		"no catalog":     {func(p *nftIndexPrefetch) { p.catalogs = nil }, withBody, "no full-text catalog is visible"},
		"no column":      {func(*nftIndexPrefetch) {}, func(*NewFullTextIndexDialog) {}, "at least one column"},
		"no population without OFF": {func(*nftIndexPrefetch) {}, func(d *NewFullTextIndexDialog) {
			withBody(d)
			checkRow(t, d.forms[0], "Do not populate now").Edit(true)
		}, "change tracking OFF"},
	} {
		pf := ftIndexPrefetch()
		tc.pf(pf)
		d := newFullTextIndexTestDialog(t, pf)
		tc.edit(d)
		if err := d.preflight(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: preflight = %v, want it to say %q", name, err, tc.want)
		}
	}
}

// The key-index candidates: unique, enabled, unfiltered, rowstore, one
// non-nullable column.
func TestFullTextKeyIndexes(t *testing.T) {
	cols := []*gosmo.Column{{Name: "ID"}, {Name: "Code"}, {Name: "Maybe", IsNullable: true}, {Name: "A"}, {Name: "B"}}
	key := func(c ...string) []gosmo.IndexColumn {
		out := make([]gosmo.IndexColumn, len(c))
		for i, n := range c {
			out[i] = gosmo.IndexColumn{Name: n}
		}
		return out
	}
	idx := []*gosmo.Index{
		{Name: "PK", IsUnique: true, Type: gosmo.IndexTypeClustered, KeyColumns: key("ID")},
		{Name: "UQ_Code", IsUnique: true, Type: gosmo.IndexTypeNonClustered, KeyColumns: key("Code")},
		{Name: "IX_NotUnique", Type: gosmo.IndexTypeNonClustered, KeyColumns: key("Code")},
		{Name: "UQ_Nullable", IsUnique: true, Type: gosmo.IndexTypeNonClustered, KeyColumns: key("Maybe")},
		{Name: "UQ_Two", IsUnique: true, Type: gosmo.IndexTypeNonClustered, KeyColumns: key("A", "B")},
		{Name: "UQ_Filtered", IsUnique: true, Type: gosmo.IndexTypeNonClustered, KeyColumns: key("A"), FilterDefinition: "([A]>(0))"},
		{Name: "UQ_Disabled", IsUnique: true, IsDisabled: true, Type: gosmo.IndexTypeNonClustered, KeyColumns: key("B")},
	}
	if got := fullTextKeyIndexes(idx, cols); !slices.Equal(got, []string{"PK", "UQ_Code"}) {
		t.Errorf("key indexes = %q", got)
	}
}

// The folders' New items are gated on CREATE FULLTEXT CATALOG, the one right
// all three CREATEs check.
func TestFullTextNewItemsAreGatedOnCreateFullTextCatalog(t *testing.T) {
	for _, held := range []bool{true, false} {
		var granted, denied []string
		if held {
			granted = []string{"CREATE FULLTEXT CATALOG"}
		} else {
			denied = []string{"CREATE FULLTEXT CATALOG"}
		}
		sc, _ := newFakeConn(t, capabilityResponses(true, nil, nil, granted, denied)...)
		sc.ProbeCapabilities()
		sc.DatabaseCapabilities(context.Background(), "appdb")
		node := &explorerNode{data: nodeData{DBName: "appdb", conn: sc}}
		var newQuery, refresh controls.MenuItem
		for label, items := range map[string][]controls.MenuItem{
			"New Full-Text Catalog...":    fullTextCatalogsMenuItems(nil, sc, node, newQuery, refresh),
			"New Full-Text Stoplist...":   fullTextStoplistsMenuItems(nil, sc, node, newQuery, refresh),
			"New Search Property List...": searchPropertyListsMenuItems(nil, sc, node, newQuery, refresh),
		} {
			i := slices.IndexFunc(items, func(m controls.MenuItem) bool { return m.Label == label })
			if i < 0 {
				t.Fatalf("no %s", label)
			}
			if got := items[i].Enabled(); got != held {
				t.Errorf("%s enabled = %v with the right held = %v", label, got, held)
			}
		}
	}
}
