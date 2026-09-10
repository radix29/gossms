package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The Object Explorer wiring for the eight families Phase 3 added — Types
// (five sub-folders), Assemblies, Rules, Defaults, Plan Guides and External
// Resources (three sub-folders). Same per-family checklist
// explorer_database_scoped_credentials_test.go runs, applied to a batch:
// a family added to the tree but missed in one of the five touch points
// reaches the user as a folder with no icon, no children, or a Filter menu
// that offers nothing.
//
// Every assertion here is about the tree. What each family's catalog read
// actually returns is gosmo's own live tests; the fake answers by substring
// and proves only that the loader asked and mapped the answer.

var treeFamilyCreated = time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)

func treeFamilyDatabaseRow() fakeResponse {
	return fakeResponse{match: "compatibility_level, collation_name", cols: 9, rows: [][]driver.Value{{
		"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false,
		treeFamilyCreated, int64(0)}}}
}

// newFamilyConn is newFakeConn with the database row every one of these
// loaders resolves through DatabaseByNameContext before its own read.
func newFamilyConn(t *testing.T, responses ...fakeResponse) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, append([]fakeResponse{treeFamilyDatabaseRow()}, responses...)...)
	return sc
}

// loadFamily expands one folder node in appdb and returns its children.
func loadFamily(t *testing.T, sc *db.ServerConn, folder NodeType) []*explorerNode {
	t.Helper()
	loader, ok := childLoaders[folder]
	if !ok {
		t.Fatalf("%v has no childLoaders entry", folder)
	}
	children, err := loader(loaderCtx{ctx: context.Background(), sc: sc},
		&explorerNode{data: nodeData{Type: folder, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loader for %v: %v", folder, err)
	}
	return children
}

// assertCarriesDatabase fails if any child lost appdb — without it every read
// under the node runs against the connection's default database.
func assertCarriesDatabase(t *testing.T, children []*explorerNode, want NodeType) {
	t.Helper()
	for _, n := range children {
		if n.data.Type != want {
			t.Errorf("%q is typed %v, want %v", n.label, n.data.Type, want)
		}
		if n.data.DBName != "appdb" {
			t.Errorf("%q carries DBName %q, want appdb", n.label, n.data.DBName)
		}
	}
}

// ============================================================
// Placement
// ============================================================

// The new folders have to appear where SSMS puts them, since that is the only
// thing a user navigating from SSMS has to go on.
func TestNewFamiliesSitWhereSSMSPutsThem(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	prog, err := childLoaders[NodeProgrammability](l,
		&explorerNode{data: nodeData{Type: NodeProgrammability, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadProgrammabilityChildren: %v", err)
	}
	wantProg := []struct {
		label string
		typ   NodeType
	}{
		{"Stored Procedures", NodeStoredProcedures},
		{"Functions", NodeFunctions},
		{"Database Triggers", NodeDatabaseTriggers},
		{"Assemblies", NodeAssemblies},
		{"Types", NodeTypes},
		{"Rules", NodeRules},
		{"Defaults", NodeDefaults},
		{"Plan Guides", NodePlanGuides},
		{"Sequences", NodeSequences},
		{"Synonyms", NodeSynonyms},
	}
	if len(prog) != len(wantProg) {
		t.Fatalf("Programmability = %v, want %d folders", labelsOfNodes(prog), len(wantProg))
	}
	for i, w := range wantProg {
		if prog[i].label != w.label || prog[i].data.Type != w.typ {
			t.Errorf("Programmability[%d] = %q/%v, want %q/%v", i, prog[i].label, prog[i].data.Type, w.label, w.typ)
		}
	}

	// External Resources is a sibling of Views, not something under
	// Programmability — SSMS's own placement.
	dbChildren, err := childLoaders[NodeDatabase](l,
		&explorerNode{data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseChildren: %v", err)
	}
	labels := labelsOfNodes(dbChildren)
	ext := slices.Index(labels, "External Resources")
	if ext < 0 {
		t.Fatalf("a database's folders = %v, with no External Resources", labels)
	}
	if got, want := ext, slices.Index(labels, "Views")+1; got != want {
		t.Errorf("External Resources is at %d, want %d — directly after Views", got, want)
	}
	if dbChildren[ext].data.Type != NodeExternalResources || dbChildren[ext].data.DBName != "appdb" {
		t.Errorf("the External Resources folder = %+v", dbChildren[ext].data)
	}
}

// Types is a folder of folders, and each has to carry the database or its own
// read runs somewhere else.
func TestTypesFolderChildren(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	children := loadFamily(t, sc, NodeTypes)

	want := []struct {
		label string
		typ   NodeType
	}{
		{"System Data Types", NodeSystemDataTypes},
		{"User-Defined Data Types", NodeUserDefinedDataTypes},
		{"User-Defined Table Types", NodeUserDefinedTableTypes},
		{"User-Defined Types", NodeUserDefinedTypes},
		{"XML Schema Collections", NodeXmlSchemaCollections},
	}
	if len(children) != len(want) {
		t.Fatalf("Types = %v, want %d folders", labelsOfNodes(children), len(want))
	}
	for i, w := range want {
		if children[i].label != w.label || children[i].data.Type != w.typ {
			t.Errorf("Types[%d] = %q/%v, want %q/%v", i, children[i].label, children[i].data.Type, w.label, w.typ)
		}
		if children[i].data.DBName != "appdb" {
			t.Errorf("Types[%d] lost the database name", i)
		}
	}
}

// External Libraries is 2017 and later. gosmo refuses the read outright on an
// older instance (sys.external_libraries does not exist there at all), so the
// folder must be absent rather than present-and-broken — an absent folder and
// an empty one are different bugs.
func TestExternalLibrariesFolderIsAbsentBefore2017(t *testing.T) {
	old, _ := newFakeConnAtVersion(t, "13.0.6300.2")
	labels := labelsOfNodes(loadFamily(t, old, NodeExternalResources))
	if slices.Contains(labels, "External Libraries") {
		t.Errorf("External Resources on major 13 = %v — the folder can only error", labels)
	}
	want := []string{"External Data Sources", "External File Formats"}
	if !slices.Equal(labels, want) {
		t.Errorf("External Resources on major 13 = %v, want %v", labels, want)
	}

	newer, _ := newFakeConnAtVersion(t, "14.0.3465.1")
	labels = labelsOfNodes(loadFamily(t, newer, NodeExternalResources))
	if !slices.Contains(labels, "External Libraries") {
		t.Errorf("External Resources on major 14 = %v, with no External Libraries", labels)
	}
}

// ============================================================
// The loaders
// ============================================================

func TestSystemDataTypesLoader(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "WHERE  t.is_user_defined = 0", cols: 6,
		rows: [][]driver.Value{
			{"int", int64(56), int64(4), int64(10), int64(0), true},
			{"varchar", int64(167), int64(8000), int64(0), int64(0), true},
		}})
	children := loadFamily(t, sc, NodeSystemDataTypes)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"int", "varchar"}) {
		t.Fatalf("children = %v", got)
	}
	assertCarriesDatabase(t, children, NodeSystemDataType)
	// A built-in type can be neither dropped nor renamed; IsSystem is what
	// keeps both off its menu (see objectOpsMenuItems).
	for _, n := range children {
		if !n.data.IsSystem {
			t.Errorf("%q is not marked IsSystem", n.label)
		}
	}
}

// An alias type is its base type plus a name, so the label carries the base —
// the name alone says nothing about it.
func TestUserDefinedDataTypesLoaderLabelsTheBaseType(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "t.is_table_type = 0 AND t.is_assembly_type = 0", cols: 11,
		rows: [][]driver.Value{
			{"Phone", "dbo", int64(257), "varchar", int64(25), int64(0), int64(0),
				"SQL_Latin1_General_CP1_CI_AS", true, "", ""},
			{"Money", "sales", int64(258), "decimal", int64(9), int64(19), int64(4), "", false, "", ""},
		}})
	children := loadFamily(t, sc, NodeUserDefinedDataTypes)
	want := []string{"dbo.Phone (varchar(25), null)", "sales.Money (decimal(19,4), not null)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeUserDefinedDataType)
	// Schema and Name must be the type's own, not sliced back out of the
	// label — everything downstream builds T-SQL from them.
	if children[0].data.Schema != "dbo" || children[0].data.Name != "Phone" {
		t.Errorf("first child = %+v, want Schema=dbo Name=Phone", children[0].data)
	}
}

func TestUserDefinedTableTypesLoader(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.table_types", cols: 5,
		rows: [][]driver.Value{
			{"OrderList", "dbo", int64(260), int64(9001), false},
			{"IdList", "dbo", int64(261), int64(9002), true},
		}})
	children := loadFamily(t, sc, NodeUserDefinedTableTypes)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"dbo.OrderList", "dbo.IdList"}) {
		t.Fatalf("children = %v", got)
	}
	assertCarriesDatabase(t, children, NodeUserDefinedTableType)
	if !children[1].data.IsMemoryOptimized {
		t.Error("the memory-optimized table type did not carry the flag")
	}
}

func TestClrTypesLoader(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "sys.assembly_types", cols: 9,
		rows: [][]driver.Value{
			{"Point", "dbo", int64(270), int64(16), int64(0), int64(0), true, "GeoLib", "Geo.Point"},
		}})
	children := loadFamily(t, sc, NodeUserDefinedTypes)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"dbo.Point"}) {
		t.Fatalf("children = %v", got)
	}
	assertCarriesDatabase(t, children, NodeUserDefinedType)
}

func TestXmlSchemaCollectionsLoader(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.xml_schema_collections", cols: 5,
		rows: [][]driver.Value{
			{"InvoiceSchema", "dbo", int64(65536), treeFamilyCreated, treeFamilyCreated},
		}})
	children := loadFamily(t, sc, NodeXmlSchemaCollections)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"dbo.InvoiceSchema"}) {
		t.Fatalf("children = %v", got)
	}
	assertCarriesDatabase(t, children, NodeXmlSchemaCollection)
	// The folder offers a Creation Date filter, which matches on this field —
	// a zero here would reject every row.
	if !children[0].data.CreateDate.Equal(treeFamilyCreated) {
		t.Errorf("CreateDate = %v, want %v", children[0].data.CreateDate, treeFamilyCreated)
	}
}

// SQL Server's own assemblies list here as SSMS lists them, but marked
// IsSystem so Delete and Rename stay off their menu.
func TestAssembliesLoaderMarksTheShippedOnesSystem(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.assemblies", cols: 9,
		rows: [][]driver.Value{
			{"Microsoft.SqlServer.Types", int64(1), "sys", "Microsoft.SqlServer.Types, version=14.0.0.0",
				"UNSAFE", true, false, treeFamilyCreated, treeFamilyCreated},
			{"GeoLib", int64(65536), "dbo", "GeoLib, version=1.0.0.0",
				"SAFE", true, true, treeFamilyCreated, treeFamilyCreated},
		}})
	children := loadFamily(t, sc, NodeAssemblies)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"Microsoft.SqlServer.Types", "GeoLib"}) {
		t.Fatalf("children = %v", got)
	}
	assertCarriesDatabase(t, children, NodeAssembly)
	if !children[0].data.IsSystem {
		t.Error("a shipped assembly is not marked IsSystem — Delete would be offered on it")
	}
	if children[1].data.IsSystem {
		t.Error("a user assembly is marked IsSystem — Delete would be withheld from it")
	}
}

func TestRulesAndDefaultsLoaders(t *testing.T) {
	sc := newFamilyConn(t,
		fakeResponse{
			match: "WHERE  o.type = 'R'", cols: 6,
			rows: [][]driver.Value{
				{"PhoneRule", "dbo", int64(100), "CREATE RULE PhoneRule AS @v LIKE '[0-9]%'",
					treeFamilyCreated, treeFamilyCreated},
			}},
		fakeResponse{
			match: "WHERE  o.type = 'D'", cols: 6,
			rows: [][]driver.Value{
				{"ZeroDefault", "dbo", int64(101), "CREATE DEFAULT ZeroDefault AS 0",
					treeFamilyCreated, treeFamilyCreated},
			}})

	rules := loadFamily(t, sc, NodeRules)
	if got := labelsOfNodes(rules); !slices.Equal(got, []string{"dbo.PhoneRule"}) {
		t.Fatalf("rules = %v", got)
	}
	assertCarriesDatabase(t, rules, NodeRule)

	defaults := loadFamily(t, sc, NodeDefaults)
	if got := labelsOfNodes(defaults); !slices.Equal(got, []string{"dbo.ZeroDefault"}) {
		t.Fatalf("defaults = %v", got)
	}
	assertCarriesDatabase(t, defaults, NodeDefault)
}

// A disabled plan guide shapes no plan, and nothing else in the row says so —
// the same "(Disabled)" suffix the trigger and policy folders carry.
func TestPlanGuidesLoaderLabelsTheDisabledOnes(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.plan_guides", cols: 13,
		rows: [][]driver.Value{
			{int64(1), "Guide_Active", false, "SELECT 1", "SQL", "", "", "", "", "", "OPTION (MAXDOP 1)",
				treeFamilyCreated, treeFamilyCreated},
			{int64(2), "Guide_Off", true, "SELECT 2", "SQL", "", "", "", "", "", "OPTION (RECOMPILE)",
				treeFamilyCreated, treeFamilyCreated},
		}})
	children := loadFamily(t, sc, NodePlanGuides)
	want := []string{"Guide_Active", "Guide_Off (Disabled)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodePlanGuide)
	// Name is the guide's own, without the suffix — the suffix is
	// presentation, and sp_control_plan_guide takes the name.
	if children[1].data.Name != "Guide_Off" {
		t.Errorf("Name = %q, want Guide_Off", children[1].data.Name)
	}
	if children[0].data.IsEnabled != true || children[1].data.IsEnabled != false {
		t.Error("IsEnabled does not mirror is_disabled — the Enable/Disable toggle reads it")
	}
}

func TestExternalResourceLoaders(t *testing.T) {
	sc := newFamilyConn(t,
		fakeResponse{
			match: "FROM   sys.external_data_sources", cols: 10,
			rows: [][]driver.Value{
				{"HadoopSrc", int64(1), "hdfs://nn:8020", "HADOOP", "nn:8050", "hdfs_cred",
					"", "", "", false},
			}},
		fakeResponse{
			match: "FROM   sys.external_file_formats", cols: 13,
			rows: [][]driver.Value{
				{int64(1), "CsvFormat", "DELIMITEDTEXT", ",", "\"", "", false, "", "\n",
					"UTF8", "", int64(0), ""},
			}},
		fakeResponse{
			match: "FROM   sys.external_libraries", cols: 5,
			rows: [][]driver.Value{
				{int64(1), "ggplot2", "dbo", "R", "PUBLIC"},
				{int64(2), "numpy", "dbo", "Python", "PUBLIC"},
			}})

	sources := loadFamily(t, sc, NodeExternalDataSources)
	if got := labelsOfNodes(sources); !slices.Equal(got, []string{"HadoopSrc"}) {
		t.Fatalf("data sources = %v", got)
	}
	assertCarriesDatabase(t, sources, NodeExternalDataSource)

	formats := loadFamily(t, sc, NodeExternalFileFormats)
	if got := labelsOfNodes(formats); !slices.Equal(got, []string{"CsvFormat"}) {
		t.Fatalf("file formats = %v", got)
	}
	assertCarriesDatabase(t, formats, NodeExternalFileFormat)

	// The language is in the label because it is the only thing separating an
	// R package from a Python one of the same name.
	libs := loadFamily(t, sc, NodeExternalLibraries)
	if got := labelsOfNodes(libs); !slices.Equal(got, []string{"ggplot2 (R)", "numpy (Python)"}) {
		t.Fatalf("libraries = %v", got)
	}
	assertCarriesDatabase(t, libs, NodeExternalLibrary)
	if libs[0].data.Name != "ggplot2" {
		t.Errorf("Name = %q, want ggplot2 — the label's suffix is presentation", libs[0].data.Name)
	}
}

// ============================================================
// The five touch points, as a table
// ============================================================

// newFamilyFolders is every folder type the phase added. A folder missing
// from childLoaders expands to nothing, which reads as an empty database
// rather than as missing wiring; one missing from isContainerNode draws an
// object glyph and refuses to open.
var newFamilyFolders = []NodeType{
	NodeTypes, NodeSystemDataTypes, NodeUserDefinedDataTypes,
	NodeUserDefinedTableTypes, NodeUserDefinedTypes, NodeXmlSchemaCollections,
	NodeAssemblies, NodeRules, NodeDefaults, NodePlanGuides,
	NodeExternalResources, NodeExternalDataSources, NodeExternalFileFormats,
	NodeExternalLibraries,
}

// newFamilyLeaves is every leaf type the phase added.
var newFamilyLeaves = []NodeType{
	NodeSystemDataType, NodeUserDefinedDataType, NodeUserDefinedTableType,
	NodeUserDefinedType, NodeXmlSchemaCollection,
	NodeAssembly, NodeRule, NodeDefault, NodePlanGuide,
	NodeExternalDataSource, NodeExternalFileFormat, NodeExternalLibrary,
}

func TestNewFamilyFoldersAreWired(t *testing.T) {
	for _, nt := range newFamilyFolders {
		if _, ok := childLoaders[nt]; !ok {
			t.Errorf("%v has no childLoaders entry — the folder would expand to nothing", nt)
		}
		if !isContainerNode(nt) {
			t.Errorf("%v is not a container node — it would draw an object icon and refuse to expand", nt)
		}
		if !hasChildren(nt) {
			t.Errorf("hasChildren(%v) = false — the folder would have no expand arrow", nt)
		}
	}
}

func TestNewFamilyLeavesAreLeaves(t *testing.T) {
	for _, nt := range newFamilyLeaves {
		if hasChildren(nt) {
			t.Errorf("hasChildren(%v) = true — the leaf would draw an arrow that leads nowhere", nt)
		}
		if _, ok := childLoaders[nt]; ok {
			t.Errorf("childLoaders has an entry for %v, but hasChildren says it's a leaf — inconsistent", nt)
		}
		if isContainerNode(nt) {
			t.Errorf("%v is a container node — it would draw a folder glyph", nt)
		}
	}
}

// Both icon sets are separate switches, and a family added to one only draws
// blank — or falls through to the shared bullet — in the other.
func TestNewFamilyLeavesHaveAnIconInEveryStyle(t *testing.T) {
	styles := []struct {
		name string
		s    config.IconStyle
	}{
		{"Emoji", config.IconStyleEmoji},
		{"Symbols", config.IconStyleSymbols},
		{"Portable", config.IconStylePortable},
	}
	for _, nt := range newFamilyLeaves {
		for _, style := range styles {
			got := objectIcon(nt, style.s)
			if got == 0 {
				t.Errorf("%s: %v has no glyph", style.name, nt)
			}
			if got == '•' {
				t.Errorf("%s: %v fell through to the default bullet", style.name, nt)
			}
		}
	}
}

// nodeTypeName's "Object" default is what the Script and Delete menus would
// say instead of the family's name.
func TestNewFamilyLeavesAreNamed(t *testing.T) {
	want := map[NodeType]string{
		NodeSystemDataType:       "System Data Type",
		NodeUserDefinedDataType:  "User-Defined Data Type",
		NodeUserDefinedTableType: "User-Defined Table Type",
		NodeUserDefinedType:      "User-Defined Type",
		NodeXmlSchemaCollection:  "XML Schema Collection",
		NodeAssembly:             "Assembly",
		NodeRule:                 "Rule",
		NodeDefault:              "Default",
		NodePlanGuide:            "Plan Guide",
		NodeExternalDataSource:   "External Data Source",
		NodeExternalFileFormat:   "External File Format",
		NodeExternalLibrary:      "External Library",
	}
	for _, nt := range newFamilyLeaves {
		if got := nodeTypeName(nt); got != want[nt] {
			t.Errorf("nodeTypeName(%v) = %q, want %q", nt, got, want[nt])
		}
	}
}

// Every folder that lists objects needs filter properties, or its Filter menu
// item silently offers nothing. Types and External Resources are folders of
// folders and are deliberately not on this list.
func TestNewFamilyFoldersAreFilterable(t *testing.T) {
	for _, nt := range newFamilyFolders {
		if nt == NodeTypes || nt == NodeExternalResources {
			if len(filterProps(nt)) != 0 {
				t.Errorf("%v is a folder of folders and declares filter properties", nt)
			}
			continue
		}
		props := filterProps(nt)
		if len(props) == 0 {
			t.Errorf("%v declares no filter properties — its Filter menu offers nothing", nt)
			continue
		}
		var names []string
		for _, p := range props {
			names = append(names, p.name)
		}
		if !slices.Contains(names, "Name") {
			t.Errorf("%v filter properties = %v, with no Name", nt, names)
		}
	}
}

// A Creation Date criterion matches against nodeData.CreateDate, which only
// the loaders that populate it can satisfy — offering the property on a
// family whose catalog records no date rejects every row instead of
// filtering. sys.types has none, so the three type folders backed by it must
// not offer it.
func TestTypeFoldersDoNotOfferACreationDateTheyCannotFill(t *testing.T) {
	for _, nt := range []NodeType{NodeSystemDataTypes, NodeUserDefinedDataTypes,
		NodeUserDefinedTableTypes, NodeUserDefinedTypes,
		NodeExternalDataSources, NodeExternalFileFormats, NodeExternalLibraries} {
		for _, p := range filterProps(nt) {
			if p.id == fpCreationDate {
				t.Errorf("%v offers Creation Date, but its loader sets no CreateDate — every row would be rejected", nt)
			}
		}
	}
}
