package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The Detail Browser's view of the Phase 3 tree families.
//
// Two things are worth pinning here and nowhere else. A folder's rows must
// carry the objs mapping — the pane's Delete is withheld entirely without it,
// silently, since a nil objs is also what every Property/Value view has. And
// a leaf must reach its family's own arm rather than fetchNodeDetails'
// default, which answers any unhandled type with a four-row Name/Type/
// Database/Schema grid built from the node itself: it never errors, never
// queries, and looks like a page until you notice it says nothing.

// detailOf runs fetchNodeDetails for one node against a scripted instance.
func detailOf(t *testing.T, sc *db.ServerConn, typ NodeType, schema, name string) ([]string, [][]string, []nodeData) {
	t.Helper()
	var objs []nodeData
	node := &explorerNode{label: name, data: nodeData{
		Type: typ, DBName: propTypeDB, Schema: schema, Name: name, conn: sc}}
	cols, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("fetchNodeDetails for %v: %v", typ, err)
	}
	return cols, rows, objs
}

// fallbackColumns are what fetchNodeDetails' default arm returns. A family
// that reached it shows these instead of its own.
var fallbackRowLabels = []string{"Name", "Type", "Database", "Schema"}

func isFallback(cols []string, rows [][]string) bool {
	if len(cols) != 2 || cols[0] != "Property" || len(rows) != len(fallbackRowLabels) {
		return false
	}
	for i, want := range fallbackRowLabels {
		if rows[i][0] != want {
			return false
		}
	}
	return true
}

// A Rules folder's rows must be the tree's rows, with the mapping that lets
// the pane act on one. Rules stands in for the seven folders sharing
// programmabilityFolderDetail's switch; the arms differ only in which listing
// they call.
func TestRulesFolderDetailCarriesItsObjects(t *testing.T) {
	sc, _ := newTypePropConn(t, fakeResponse{match: "o.type = 'R'", cols: 6, rows: [][]driver.Value{
		{"PhoneRule", "dbo", int64(1001), "AS @v LIKE '[0-9]%'", propTypeDate, propTypeDate},
		{"ZipRule", "sales", int64(1002), "AS @v LIKE '[0-9][0-9][0-9][0-9][0-9]'", propTypeDate, propTypeDate},
	}})
	cols, rows, objs := detailOf(t, sc, NodeRules, "", "")

	if !slices.Contains(cols, "Definition") {
		t.Errorf("the Rules folder's columns are %v, want the definition among them", cols)
	}
	if len(rows) != 2 {
		t.Fatalf("the folder shows %d rows, want 2", len(rows))
	}
	// The tree labels a rule "dbo.PhoneRule"; the pane must not render the
	// same object as "[dbo].[PhoneRule]" beside it.
	if rows[0][0] != "dbo.PhoneRule" {
		t.Errorf("row 1's name is %q, want the tree's dbo.PhoneRule", rows[0][0])
	}
	if len(objs) != len(rows) {
		t.Fatalf("the folder mapped %d objects to %d rows — Delete is withheld unless they match", len(objs), len(rows))
	}
	if objs[1].Type != NodeRule || objs[1].Schema != "sales" || objs[1].Name != "ZipRule" {
		t.Errorf("row 2 maps to %+v, want sales.ZipRule as a NodeRule", objs[1])
	}
}

// Plan guides have their own folder arm because their state matters as much
// as their name.
func TestPlanGuidesFolderDetailShowsState(t *testing.T) {
	sc, _ := newTypePropConn(t, planGuideResponse(true, "OBJECT", "[dbo].[GetClaims]", "", ""))
	cols, rows, objs := detailOf(t, sc, NodePlanGuides, "", "")

	if !slices.Contains(cols, "Status") {
		t.Errorf("the Plan Guides folder's columns are %v, want a Status among them", cols)
	}
	if len(rows) != 1 || rows[0][1] != "Disabled" {
		t.Fatalf("the folder shows %v, want the guide reported as Disabled", rows)
	}
	if len(objs) != 1 || objs[0].IsEnabled {
		t.Errorf("the mapped object is %+v, want IsEnabled false", objs[0])
	}
}

// External Libraries is the one folder gosmo refuses outright below major 14.
// The pane must surface that as an error rather than as an empty folder,
// which reads as a database with no libraries.
func TestExternalLibrariesFolderDetailReportsTheVersionRefusal(t *testing.T) {
	sc, _ := newFakeConnAtVersion(t, "13.0.6300.2",
		fakeResponse{match: "compatibility_level, collation_name", cols: 9, rows: [][]driver.Value{{
			propTypeDB, int64(7), "ONLINE", "FULL", int64(130), "SQL_Latin1_General_CP1_CI_AS",
			false, propTypeDate, int64(0)}}})

	var objs []nodeData
	node := &explorerNode{data: nodeData{Type: NodeExternalLibraries, DBName: propTypeDB, conn: sc}}
	if _, _, err := fetchNodeDetails(context.Background(), sc, node, &objs); err == nil {
		t.Error("the folder answered on major 13, where sys.external_libraries does not exist")
	}
}

// Every leaf the Phase 3 families added has to reach its own arm. The default
// arm never errors and never queries, so a family missed in the dispatch
// looks like a working pane that happens to say very little.
func TestEveryNewLeafHasItsOwnDetailView(t *testing.T) {
	cases := []struct {
		typ       NodeType
		schema    string
		name      string
		responses []fakeResponse
	}{
		{NodeSystemDataType, "", "int", []fakeResponse{{
			match: "t.is_user_defined = 0", cols: 6,
			rows: [][]driver.Value{{"int", int64(56), int64(4), int64(10), int64(0), true}}}}},
		{NodeUserDefinedDataType, "dbo", "Phone", []fakeResponse{aliasTypeResponse("", "")}},
		{NodeUserDefinedTableType, "dbo", "IdList", tableTypeResponses()},
		{NodeUserDefinedType, "dbo", "Geo", []fakeResponse{{
			match: "sys.assembly_types", cols: 9,
			rows: [][]driver.Value{{"Geo", "dbo", int64(259), int64(-1), int64(0), int64(0),
				true, "GeoUtils", "GeoUtils.Point"}}}}},
		{NodeXmlSchemaCollection, "dbo", "Claims", xmlSchemaCollectionResponses("<xsd:schema/>")},
		{NodeAssembly, "", propAssembly, assemblyPropResponses()},
		{NodeRule, "dbo", "PhoneRule", []fakeResponse{ruleResponse("AS @v > 0")}},
		{NodeDefault, "dbo", "TodayDefault", []fakeResponse{defaultResponse("AS GETDATE()")}},
		{NodePlanGuide, "", propPlanGuide, []fakeResponse{planGuideResponse(false, "SQL", "", "", "")}},
		{NodeExternalDataSource, "", "HadoopCluster", []fakeResponse{externalDataSourceResponse("", false)}},
		{NodeExternalFileFormat, "", "CsvFormat", []fakeResponse{externalFileFormatResponse(0, "")}},
		{NodeExternalLibrary, "", "ggplot2", []fakeResponse{{
			match: "FROM   sys.external_libraries l", cols: 5,
			rows: [][]driver.Value{{int64(1), "ggplot2", "dbo", "R", "PUBLIC"}}}}},
	}

	for _, c := range cases {
		sc, _ := newTypePropConn(t, c.responses...)
		cols, rows, objs := detailOf(t, sc, c.typ, c.schema, c.name)
		if isFallback(cols, rows) {
			t.Errorf("%v falls to fetchNodeDetails' default arm — it has no detail view of its own", c.typ)
			continue
		}
		if len(rows) == 0 {
			t.Errorf("%v's detail view is empty", c.typ)
		}
		// A leaf is one object, not a listing: objs is what offers Delete on
		// a *row*, and a Property/Value view has no rows to delete.
		if objs != nil {
			t.Errorf("%v's leaf view mapped %d row objects, want none", c.typ, len(objs))
		}
	}
}

// A Properties page nothing opens is a page that does not exist. Every leaf
// with a props file must offer the item on its Object Explorer menu — the
// only entry point these dialogs have.
func TestEveryNewLeafOffersProperties(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")

	withProperties := []NodeType{
		NodeUserDefinedDataType, NodeUserDefinedTableType, NodeUserDefinedType,
		NodeXmlSchemaCollection, NodeAssembly, NodeRule, NodeDefault,
		NodePlanGuide, NodeExternalDataSource, NodeExternalFileFormat,
		NodeExternalLibrary,
	}
	for _, typ := range withProperties {
		node := &explorerNode{label: "x", data: nodeData{
			Type: typ, DBName: propTypeDB, Schema: "dbo", Name: "x", conn: sc}}
		if !hasMenuItem(a.nodeMenuItems(node), "Properties...") {
			t.Errorf("%v offers no Properties item — its props pages are unreachable", typ)
		}
	}

	// SSMS offers no Properties on a built-in type, and there is nothing to
	// show about `int` that its name does not already say. Absent on purpose,
	// so it is pinned rather than left to look like an omission.
	sysType := &explorerNode{label: "int", data: nodeData{
		Type: NodeSystemDataType, DBName: propTypeDB, Name: "int", conn: sc}}
	if hasMenuItem(a.nodeMenuItems(sysType), "Properties...") {
		t.Error("a system data type offers Properties, which has nothing to show")
	}
}

func hasMenuItem(items []controls.MenuItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}
