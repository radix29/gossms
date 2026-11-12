package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newSynonymTestDialog builds the New Synonym pages the way show() would,
// minus the connection. The fake server answers the database read the Extended
// Properties page's apply starts with.
func newSynonymTestDialog(t *testing.T, pf *schemaObjectPrefetch) *NewSynonymDialog {
	t.Helper()
	sc, _ := newGConn(t, dbByNameResp("appdb", 5))
	d := &NewSynonymDialog{}
	d.dbName = "appdb"
	d.sc = sc
	d.pages = []string{"General", "Permissions", "Extended Properties"}
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.buildPages(pf)
	return d
}

func newSynonymTestPrefetch(existing ...string) *schemaObjectPrefetch {
	pf := &schemaObjectPrefetch{existing: newNameSet(""), schemas: []string{"app", "dbo", "sales"},
		principals: []permPrincipal{{Name: "app]user", Type: gosmo.PrincipalType("SQL_USER")}}}
	for _, e := range existing {
		schema, name, _ := strings.Cut(e, ".")
		pf.existing.Add(sequenceKey(schema, name))
	}
	return pf
}

// scriptNewSynonym runs the whole pipeline's steps under Script Changes, the
// preflight first, as runPipeline does.
func scriptNewSynonym(t *testing.T, d *NewSynonymDialog) string {
	t.Helper()
	for _, f := range d.forms {
		f.Commit()
	}
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	for i, apply := range d.applyFns {
		if err := apply(ctx); err != nil {
			t.Fatalf("apply page %d: %v", i, err)
		}
	}
	return strings.Join(script.Statements(), "\n")
}

// Left as opened the base object is in this database's dbo; every part typed
// reaches the FOR clause quoted on its own, and blank leading parts are left
// out rather than written empty.
func TestNewSynonymScriptsWhatThePageSays(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		server, database, schema string
		want                     string
	}{
		{"defaults", "", "appdb", "dbo", "CREATE SYNONYM [dbo].[Pat] FOR [appdb].[dbo].[Patient]"},
		{"linked server", "LNK.one", "o]ther", "s", "CREATE SYNONYM [dbo].[Pat] FOR [LNK.one].[o]]ther].[s].[Patient]"},
		{"two-part", "", "  ", "sales", "CREATE SYNONYM [dbo].[Pat] FOR [sales].[Patient]"},
	} {
		d := newSynonymTestDialog(t, newSynonymTestPrefetch())
		f := d.forms[0]
		editText(t, f, "Synonym name", "  Pat  ")
		// editText refuses an edit that leaves a row clean, so only what differs
		// from the defaults is typed.
		for _, e := range []struct{ label, value, opened string }{
			{"Server name", tc.server, ""}, {"Database name", tc.database, "appdb"}, {"Schema", tc.schema, "dbo"},
		} {
			if e.value != e.opened {
				editText(t, f, e.label, e.value)
			}
		}
		editText(t, f, "Object name", "Patient")
		want := "USE [appdb];\nGO\n" + tc.want
		if got := scriptNewSynonym(t, d); got != want {
			t.Errorf("%s scripted\n%q\nwant\n%q", tc.name, got, want)
		}
	}

	d := newSynonymTestDialog(t, newSynonymTestPrefetch())
	editText(t, d.forms[0], "Synonym name", "p]at")
	editSelect(t, d.forms[0], "Synonym schema", "sales")
	editText(t, d.forms[0], "Object name", "Patient")
	if got, want := scriptNewSynonym(t, d), "CREATE SYNONYM [sales].[p]]at] FOR"; !strings.Contains(got, want) {
		t.Errorf("schema and name scripted\n%s\nwant %q", got, want)
	}
}

// The Permissions and Extended Properties pages run after the CREATE and name
// the synonym as typed, in the schema picked: ON [schema].[name] and a property
// at SCHEMA/SYNONYM.
func TestNewSynonymPermissionsAndExtendedPropertiesNameTheNewSynonym(t *testing.T) {
	d := newSynonymTestDialog(t, newSynonymTestPrefetch())
	matrixPartsOf(t, d.forms[1]).perms.OnActivateCell(0, 1) // (none) -> Grant on the first permission
	editText(t, d.forms[2], "Name", "MS_Description")
	editText(t, d.forms[2], "Value", "Patients")
	clickButton(t, d.forms[2], "Add")
	// Typed last: the later pages must read the name when they run, not when built.
	editSelect(t, d.forms[0], "Synonym schema", "sales")
	editText(t, d.forms[0], "Synonym name", "Pat")
	editText(t, d.forms[0], "Object name", "Patient")

	got := scriptNewSynonym(t, d)
	first := gosmo.SecurableSynonym.PermissionNames()[0]
	for _, want := range []string{
		"CREATE SYNONYM [sales].[Pat]",
		"GRANT " + first + " ON [sales].[Pat] TO [app]]user]",
		"@level0type = N'SCHEMA', @level0name = N'sales',",
		"@level1type = N'SYNONYM', @level1name = N'Pat',",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script:\n%s\nwant it to contain: %s", got, want)
		}
	}
	if strings.Index(got, "CREATE SYNONYM") > strings.Index(got, "GRANT") {
		t.Errorf("the GRANT is scripted before the CREATE:\n%s", got)
	}
}

// Everything the server or gosmo would refuse for the request itself is refused
// before the round trip, naming the field.
func TestNewSynonymPreflight(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(f *propsheet.Form)
		want string
	}{
		{"blank name", func(f *propsheet.Form) { editText(t, f, "Synonym name", "  ") }, "name is required"},
		{"taken in that schema, other case", func(f *propsheet.Form) {
			editSelect(t, f, "Synonym schema", "sales")
			editText(t, f, "Synonym name", "PAT")
		}, "already exists"},
		{"no object", func(f *propsheet.Form) { editText(t, f, "Object name", " ") }, "base object's name"},
		{"no schema", func(f *propsheet.Form) { editText(t, f, "Schema", "") }, "base object's schema"},
		{"server without database", func(f *propsheet.Form) {
			editText(t, f, "Server name", "LNK")
			editText(t, f, "Database name", "")
		}, "needs its database name"},
	} {
		d := newSynonymTestDialog(t, newSynonymTestPrefetch("sales.Pat"))
		editText(t, d.forms[0], "Synonym name", "Pat")
		editText(t, d.forms[0], "Object name", "Patient")
		tc.edit(d.forms[0])
		if err := d.preflight(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: preflight = %v, want an error containing %q", tc.name, err, tc.want)
		}
	}

	// The name taken in sales is free in dbo.
	d := newSynonymTestDialog(t, newSynonymTestPrefetch("sales.Pat"))
	editText(t, d.forms[0], "Synonym name", "Pat")
	editText(t, d.forms[0], "Object name", "Patient")
	if err := d.preflight(); err != nil {
		t.Errorf("a name taken only in another schema was refused: %v", err)
	}
}

// New Synonym needs both halves CREATE SYNONYM checks: the database permission,
// and ALTER on some schema. Withheld when either is missing, naming the one
// that is; the dialog then refuses a schema the probe denies ALTER on.
func TestNewSynonymGateNeedsBothHalves(t *testing.T) {
	probed := func(t *testing.T, dbGranted, dbDenied, schemaGranted, schemaDenied []string) *NewSynonymDialog {
		t.Helper()
		sc, _ := newGConn(t, capabilityResponsesWithSchemas(true, nil, nil, dbGranted, dbDenied, schemaGranted, schemaDenied)...)
		sc.ProbeCapabilities(sc.Server.Context())
		sc.DatabaseCapabilities(context.Background(), "appdb")
		d := newSynonymTestDialog(t, newSynonymTestPrefetch())
		d.sc = sc
		d.buildPages(newSynonymTestPrefetch())
		return d
	}
	item := func(t *testing.T, d *NewSynonymDialog) (bool, string) {
		t.Helper()
		folder := &explorerNode{data: nodeData{Type: NodeSynonyms, DBName: "appdb", conn: d.sc}}
		for _, it := range (&App{}).nodeMenuItems(folder) {
			if it.Label == "New Synonym..." {
				return it.Enabled == nil || it.Enabled(), it.Note
			}
		}
		t.Fatal("no New Synonym item")
		return false, ""
	}
	cs := []string{"CREATE SYNONYM"}
	all := []string{"app", "dbo", "sales"}

	if on, _ := item(t, probed(t, cs, nil, []string{"app"}, []string{"dbo", "sales"})); !on {
		t.Error("withheld with CREATE SYNONYM and ALTER on one schema")
	}
	if on, note := item(t, probed(t, nil, cs, all, nil)); on || note != "needs CREATE SYNONYM" {
		t.Errorf("no CREATE SYNONYM: enabled %v, note %q; want withheld, naming it", on, note)
	}
	if on, note := item(t, probed(t, cs, nil, nil, all)); on || note != "needs ALTER on a schema" {
		t.Errorf("no schema ALTER: enabled %v, note %q; want withheld, naming it", on, note)
	}
	if on, _ := item(t, probed(t, nil, nil, nil, nil)); !on {
		t.Error("withheld before any probe answered — the gate must fail open")
	}

	d := probed(t, cs, nil, []string{"app"}, []string{"dbo", "sales"})
	editText(t, d.forms[0], "Synonym name", "s")
	editText(t, d.forms[0], "Object name", "t")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "schema dbo needs ALTER") {
		t.Errorf("dbo, denied: preflight = %v", err)
	}
	editSelect(t, d.forms[0], "Synonym schema", "app")
	if err := d.preflight(); err != nil {
		t.Errorf("app, granted: preflight = %v", err)
	}

	d = probed(t, nil, cs, all, nil)
	editText(t, d.forms[0], "Synonym name", "s")
	editText(t, d.forms[0], "Object name", "t")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "needs CREATE SYNONYM") {
		t.Errorf("no CREATE SYNONYM: preflight = %v", err)
	}
}

// The Synonyms folder offers New Synonym between New Query and Refresh.
func TestSynonymsFolderMenu(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeSynonyms, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Synonym...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
}

// fetchSchemaObjectPrefetch serves New Sequence and New Synonym, each listing
// its own family as schema-qualified keys: a taken name is refused in its own
// schema (case-folded under the database's CI collation) and free in another.
func TestSchemaObjectPrefetchTakesEachDialogsFamily(t *testing.T) {
	sequenceRow := func(schema, name string) []driver.Value {
		return []driver.Value{name, schema, int64(9), "bigint", "", int64(19), int64(0),
			"1", "1", "1", "9223372036854775807", false, true, int64(0), "1", nil, false}
	}
	for _, tc := range []struct {
		name  string
		resp  fakeResponse
		fetch func(*db.ServerConn) (*schemaObjectPrefetch, error)
	}{
		{"New Sequence", fakeResponse{match: "FROM   sys.sequences s", db: "AppDB", cols: 17,
			rows: [][]driver.Value{sequenceRow("sales", "OrderNo")}},
			func(sc *db.ServerConn) (*schemaObjectPrefetch, error) {
				d := NewNewSequenceDialog(newTestApp())
				d.dbName = "AppDB"
				return d.fetch(context.Background(), sc)
			}},
		{"New Synonym", fakeResponse{match: "FROM   sys.synonyms", db: "AppDB", cols: 4,
			rows: [][]driver.Value{{"OrderNo", "sales", int64(9), "[dbo].[Orders]"}}},
			func(sc *db.ServerConn) (*schemaObjectPrefetch, error) {
				d := NewNewSynonymDialog(newTestApp())
				d.dbName = "AppDB"
				return d.fetch(context.Background(), sc)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, _ := newGConn(t, dbByNameResp("AppDB", 5), tc.resp, databasePrincipalsResponse(),
				fakeResponse{match: "FROM   sys.schemas s", db: "AppDB", cols: 3, rows: [][]driver.Value{
					{"dbo", int64(1), "dbo"}, {"sales", int64(5), "dbo"}, {"sys", int64(4), "sys"},
				}})
			pf, err := tc.fetch(sc)
			if err != nil {
				t.Fatal(err)
			}
			if !pf.existing.Has(sequenceKey("SALES", "orderno")) {
				t.Errorf("sales.OrderNo not taken (folded) in %+v", pf.existing)
			}
			if pf.existing.Has(sequenceKey("dbo", "OrderNo")) || pf.existing.Has("OrderNo") {
				t.Error("the name is taken outside its schema")
			}
			if !slices.Equal(pf.schemas, []string{"dbo", "sales"}) {
				t.Errorf("schemas = %v, want [dbo sales]", pf.schemas)
			}
		})
	}
}
