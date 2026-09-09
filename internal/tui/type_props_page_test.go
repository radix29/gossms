package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Types Properties pages, driven through fakedb_test.go.
//
// Every page here reads through a by-name finder, and the fake answers by
// substring, so each response below is matched on the predicate that tells
// one sys.types family from another — the four share a view and differ only
// in flag columns, which is exactly the confusion gosmo's own header warns
// about. A match on "sys.types" alone would let the alias-type answer serve
// the CLR-type page.

const propTypeDB = "appdb"

var propTypeDate = time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)

// newTypePropConn is newFakeConn with the sys.databases row every
// database-scoped finder in this file resolves through DatabaseByNameContext
// before its own read. Shared by the other tree-family props tests.
func newTypePropConn(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	dbRow := fakeResponse{match: "compatibility_level, collation_name", cols: 9, rows: [][]driver.Value{{
		propTypeDB, int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS",
		false, propTypeDate, int64(0)}}}
	return newFakeConn(t, append([]fakeResponse{dbRow}, responses...)...)
}

// ============================================================
// Alias types
// ============================================================

func aliasTypeResponse(rule, def driver.Value) fakeResponse {
	return fakeResponse{
		match: "t.is_user_defined = 1 AND t.is_table_type = 0",
		cols:  11,
		rows: [][]driver.Value{{
			"Phone", "dbo", int64(257), "varchar", int64(25), int64(0), int64(0),
			"SQL_Latin1_General_CP1_CI_AS", true, rule, def,
		}},
	}
}

// The base type is the whole of what an alias type is — the name alone says
// nothing — so it has to reach the page rendered the way a column declaration
// would show it, not as a bare "varchar".
func TestAliasTypeGeneralShowsTheBaseType(t *testing.T) {
	sc, inst := newTypePropConn(t, aliasTypeResponse("PhoneRule", ""))
	form, apply := loadPage(t, userDefinedDataTypePropPages(sc, propTypeDB, "dbo", "Phone")[0], inst)

	if got := staticValue(t, form, "Name"); got != "Phone" {
		t.Errorf("Name is %q", got)
	}
	if got := staticValue(t, form, "Base type"); got != "varchar(25)" {
		t.Errorf("Base type is %q, want varchar(25) — the length has to come through", got)
	}
	if got := staticValue(t, form, "Allow nulls"); got != "True" {
		t.Errorf("Allow nulls is %q", got)
	}
	if got := staticValue(t, form, "Bound rule"); got != "PhoneRule" {
		t.Errorf("Bound rule is %q", got)
	}
	if apply != nil {
		t.Error("the page has an apply, but CREATE TYPE has no ALTER for one to call")
	}
}

// An unbound rule or default is the common case, and an empty value there
// reads as a field the page failed to load rather than as nothing bound.
func TestAliasTypeGeneralNamesAnUnboundRule(t *testing.T) {
	sc, inst := newTypePropConn(t, aliasTypeResponse("", ""))
	form, _ := loadPage(t, userDefinedDataTypePropPages(sc, propTypeDB, "dbo", "Phone")[0], inst)

	if got := staticValue(t, form, "Bound rule"); got != "(none)" {
		t.Errorf("Bound rule is %q, want (none)", got)
	}
	if got := staticValue(t, form, "Bound default"); got != "(none)" {
		t.Errorf("Bound default is %q, want (none)", got)
	}
}

// ============================================================
// Table types
// ============================================================

func tableTypeResponses() []fakeResponse {
	return []fakeResponse{
		{match: "FROM   sys.table_types tt", cols: 5, rows: [][]driver.Value{
			{"IdList", "dbo", int64(258), int64(9999), false},
		}},
		{match: "FROM   sys.columns c", cols: 17, rows: [][]driver.Value{
			{"Id", int64(1), "int", int64(4), int64(10), int64(0),
				false, false, false, nil, nil, nil, false, nil, nil, nil, false},
			{"Label", int64(2), "nvarchar", int64(100), int64(0), int64(0),
				true, false, false, nil, nil, nil, false, "SQL_Latin1_General_CP1_CI_AS", nil, nil, false},
		}},
	}
}

// The columns are read through type_table_object_id, the internal table
// sys.table_types points at — the one lookup in this family that has no
// obvious form. A page reaching for the type's own id returns an empty grid,
// which reads as a table type with no columns.
func TestTableTypeColumnsPageListsTheTypesShape(t *testing.T) {
	sc, inst := newTypePropConn(t, tableTypeResponses()...)
	form, apply := loadPage(t, pageTableTypeColumns(sc, propTypeDB, "dbo", "IdList"), inst)

	rows := gridRowsOf(t, form)
	if len(rows) != 2 {
		t.Fatalf("the Columns page shows %d rows, want 2", len(rows))
	}
	if rows[0][1] != "Id" || rows[0][2] != "int" {
		t.Errorf("row 1 is %v, want the Id int column", rows[0])
	}
	if rows[1][2] != "nvarchar(50)" {
		t.Errorf("row 2's data type is %q — max_length is bytes, and nvarchar halves it", rows[1][2])
	}
	if rows[1][3] != "True" {
		t.Errorf("row 2's Allow Nulls is %q, want True", rows[1][3])
	}
	if apply != nil {
		t.Error("the Columns page has an apply, but a table type cannot be altered")
	}
}

func TestTableTypeGeneralShowsMemoryOptimized(t *testing.T) {
	sc, inst := newTypePropConn(t, tableTypeResponses()...)
	form, _ := loadPage(t, pageTableTypeGeneral(sc, propTypeDB, "dbo", "IdList"), inst)

	if got := staticValue(t, form, "Memory optimized"); got != "False" {
		t.Errorf("Memory optimized is %q, want False", got)
	}
}

// ============================================================
// CLR types
// ============================================================

// A CLR type's assembly and class are the only thing separating it from an
// opaque byte string, and they come from a join the alias-type read does not
// make.
func TestClrTypeGeneralNamesItsAssembly(t *testing.T) {
	sc, inst := newTypePropConn(t, fakeResponse{
		match: "sys.assembly_types", cols: 9,
		rows: [][]driver.Value{{"Geo", "dbo", int64(259), int64(-1), int64(0), int64(0), true,
			"GeoUtils", "GeoUtils.Point"}},
	})
	form, apply := loadPage(t, clrTypePropPages(sc, propTypeDB, "dbo", "Geo")[0], inst)

	if got := staticValue(t, form, "Assembly"); got != "GeoUtils" {
		t.Errorf("Assembly is %q", got)
	}
	if got := staticValue(t, form, "Assembly class"); got != "GeoUtils.Point" {
		t.Errorf("Assembly class is %q", got)
	}
	// max_length -1 is MAX, not a length of minus one byte.
	if got := staticValue(t, form, "Length (bytes)"); got != "max" {
		t.Errorf("Length is %q, want max", got)
	}
	if apply != nil {
		t.Error("the page has an apply")
	}
}

// ============================================================
// XML schema collections
// ============================================================

func xmlSchemaCollectionResponses(definition driver.Value) []fakeResponse {
	return []fakeResponse{
		{match: "XML_SCHEMA_NAMESPACE", cols: 1, rows: [][]driver.Value{{definition}}},
		{match: "sys.xml_schema_collections", cols: 5, rows: [][]driver.Value{
			{"Claims", "dbo", int64(65536), propTypeDate, propTypeDate},
		}},
	}
}

func TestXmlSchemaCollectionSchemaPageShowsTheDocuments(t *testing.T) {
	const doc = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"/>`
	sc, inst := newTypePropConn(t, xmlSchemaCollectionResponses(doc)...)
	form, apply := loadPage(t, pageXmlSchemaCollectionSchema(sc, propTypeDB, "dbo", "Claims"), inst)

	var ed *propsheet.EditorRow
	for _, r := range form.Rows() {
		if er, ok := r.(*propsheet.EditorRow); ok {
			ed = er
		}
	}
	if ed == nil {
		t.Fatal("the Schema page has no editor row")
	}
	if ed.Value() != doc {
		t.Errorf("the editor holds %q, want the collection's documents", ed.Value())
	}
	if !ed.Editor().ReadOnly() {
		t.Error("the schema editor is writable — an edit here goes nowhere, since the page has no apply")
	}
	if apply != nil {
		t.Error("the Schema page has an apply")
	}
}

// XML_SCHEMA_NAMESPACE returns nothing to a login without VIEW DEFINITION.
// An empty editor there reads as a collection holding no documents, which is
// not a state the catalog can be in.
func TestXmlSchemaCollectionSchemaPageReportsAnUnreadableSchema(t *testing.T) {
	sc, inst := newTypePropConn(t, xmlSchemaCollectionResponses(nil)...)
	form, _ := loadPage(t, pageXmlSchemaCollectionSchema(sc, propTypeDB, "dbo", "Claims"), inst)

	for _, r := range form.Rows() {
		if _, ok := r.(*propsheet.EditorRow); ok {
			t.Error("the page drew an editor for a schema it could not read")
		}
	}
}

func TestXmlSchemaCollectionGeneralShowsItsDates(t *testing.T) {
	sc, inst := newTypePropConn(t, xmlSchemaCollectionResponses("<xsd:schema/>")...)
	form, _ := loadPage(t, pageXmlSchemaCollectionGeneral(sc, propTypeDB, "dbo", "Claims"), inst)

	if got := staticValue(t, form, "Created"); !strings.Contains(got, "2026-05-06") {
		t.Errorf("Created is %q", got)
	}
}

// ============================================================
// The whole family
// ============================================================

// None of the four type dialogs writes, and prop_page_requires_test.go's
// pagesThatOnlyRead only permits that for a page with no apply at all. This
// asserts the fact directly rather than through the exemption list, so a page
// that grows an apply fails here as well as there.
func TestTypePropertiesPagesDoNotWrite(t *testing.T) {
	responses := append(tableTypeResponses(), xmlSchemaCollectionResponses("<xsd:schema/>")...)
	responses = append(responses,
		aliasTypeResponse("", ""),
		fakeResponse{match: "sys.assembly_types", cols: 9, rows: [][]driver.Value{
			{"Geo", "dbo", int64(259), int64(-1), int64(0), int64(0), true, "GeoUtils", "GeoUtils.Point"}}},
	)
	sc, inst := newTypePropConn(t, responses...)

	sets := map[string][]propPage{
		"User-Defined Data Type":  userDefinedDataTypePropPages(sc, propTypeDB, "dbo", "Phone"),
		"User-Defined Table Type": userDefinedTableTypePropPages(sc, propTypeDB, "dbo", "IdList"),
		"User-Defined Type":       clrTypePropPages(sc, propTypeDB, "dbo", "Geo"),
		"XML Schema Collection":   xmlSchemaCollectionPropPages(sc, propTypeDB, "dbo", "Claims"),
	}
	for name, pages := range sets {
		for _, page := range pages {
			if _, apply := loadPage(t, page, inst); apply != nil {
				t.Errorf("%s/%s has an apply — none of these objects has an ALTER", name, page.title)
			}
		}
	}
}

// hasStatic reports whether the page shows a Static row with this label. The
// tree families' pages hide a field the instance's catalog cannot express
// rather than showing it empty, and "absent" is the assertion that needs
// making — staticValue fails the test when the row is missing.
func hasStatic(f *propsheet.Form, label string) bool {
	for _, r := range f.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == label {
			return true
		}
	}
	return false
}

// gridRowsOf returns the rows of the page's single grid. Shared by the
// tree-family props tests, all of which have at most one grid per page.
func gridRowsOf(t *testing.T, f *propsheet.Form) [][]string {
	t.Helper()
	var gr *propsheet.GridRow
	for _, r := range f.Rows() {
		if g, ok := r.(*propsheet.GridRow); ok {
			if gr != nil {
				t.Fatal("this page has more than one grid — address them by index instead")
			}
			gr = g
		}
	}
	if gr == nil {
		t.Fatal("this page has no grid row")
	}
	var rows [][]string
	for i := 0; ; i++ {
		row := gr.Grid.Row(i)
		if row == nil {
			return rows
		}
		rows = append(rows, row)
	}
}
