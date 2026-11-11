package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newSequenceTestDialog builds the New Sequence pages the way show() would,
// minus the connection. The fake server answers the database read the Extended
// Properties page's apply starts with.
func newSequenceTestDialog(t *testing.T, pf *schemaObjectPrefetch) *NewSequenceDialog {
	t.Helper()
	sc, _ := newGConn(t, dbByNameResp("appdb", 5))
	d := &NewSequenceDialog{}
	d.dbName = "appdb"
	d.sc = sc
	d.pages = []string{"General", "Permissions", "Extended Properties"}
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.buildPages(pf)
	return d
}

func newSequenceTestPrefetch(existing ...string) *schemaObjectPrefetch {
	pf := &schemaObjectPrefetch{existing: newNameSet(""), schemas: []string{"app", "dbo", "sales"},
		principals: []permPrincipal{{Name: "app]user", Type: gosmo.PrincipalType("SQL_USER")}}}
	for _, e := range existing {
		schema, name, _ := strings.Cut(e, ".")
		pf.existing.Add(sequenceKey(schema, name))
	}
	return pf
}

// scriptNewSequence runs the whole pipeline's steps under Script Changes, the
// preflight first, as runPipeline does.
func scriptNewSequence(t *testing.T, d *NewSequenceDialog) string {
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

// Left as opened the dialog creates a bigint sequence in dbo starting at 1, with
// the server's default cache; every option the page offers reaches the
// statement, a decimal's precision included.
func TestNewSequenceScriptsWhatThePageSays(t *testing.T) {
	d := newSequenceTestDialog(t, newSequenceTestPrefetch())
	editText(t, d.forms[0], "Sequence name", "  OrderNo  ")
	want := "USE [appdb];\nGO\nCREATE SEQUENCE [dbo].[OrderNo] AS bigint START WITH 1 INCREMENT BY 1 NO MINVALUE NO MAXVALUE NO CYCLE CACHE"
	if got := scriptNewSequence(t, d); got != want {
		t.Errorf("defaults scripted\n%q\nwant\n%q", got, want)
	}

	d = newSequenceTestDialog(t, newSequenceTestPrefetch())
	f := d.forms[0]
	editText(t, f, "Sequence name", "o]rd")
	editSelect(t, f, "Sequence schema", "sales")
	editSelect(t, f, "Data type", "decimal")
	editText(t, f, "Precision", "30")
	editText(t, f, "Start value", "100")
	editText(t, f, "Increment by", "-5")
	editCheck(t, f, "No minimum (the type's)", false)
	editText(t, f, "Minimum value", "-50")
	editCheck(t, f, "No maximum (the type's)", false)
	editText(t, f, "Maximum value", "1000")
	editCheck(t, f, "Cycle", true)
	editSelect(t, f, "Cache", nseqCacheSize)
	editText(t, f, "Cache size", "20")
	want = "USE [appdb];\nGO\nCREATE SEQUENCE [sales].[o]]rd] AS decimal(30, 0) START WITH 100 INCREMENT BY -5 MINVALUE -50 MAXVALUE 1000 CYCLE CACHE 20"
	if got := scriptNewSequence(t, d); got != want {
		t.Errorf("every option scripted\n%q\nwant\n%q", got, want)
	}

	// A decimal(38, 0) sequence starting past bigint, with the type's own
	// widest bounds.
	d = newSequenceTestDialog(t, newSequenceTestPrefetch())
	f = d.forms[0]
	editText(t, f, "Sequence name", "wide")
	editSelect(t, f, "Data type", "decimal")
	editText(t, f, "Precision", "38")
	editText(t, f, "Start value", "9223372036854775808")
	editCheck(t, f, "No minimum (the type's)", false)
	editText(t, f, "Minimum value", "-99999999999999999999999999999999999999")
	editCheck(t, f, "No maximum (the type's)", false)
	editText(t, f, "Maximum value", "10000000000000000000000000000000000000")
	want = "USE [appdb];\nGO\nCREATE SEQUENCE [dbo].[wide] AS decimal(38, 0) START WITH 9223372036854775808 INCREMENT BY 1 " +
		"MINVALUE -99999999999999999999999999999999999999 MAXVALUE 10000000000000000000000000000000000000 NO CYCLE CACHE"
	if got := scriptNewSequence(t, d); got != want {
		t.Errorf("past bigint scripted\n%q\nwant\n%q", got, want)
	}

	// No cache, and a precision typed for decimal and then left behind by
	// switching to int: the row is off, so nothing of it reaches the statement.
	d = newSequenceTestDialog(t, newSequenceTestPrefetch())
	f = d.forms[0]
	editText(t, f, "Sequence name", "s")
	editSelect(t, f, "Data type", "decimal")
	editText(t, f, "Precision", "30")
	editSelect(t, f, "Data type", "int")
	editSelect(t, f, "Cache", nseqCacheNone)
	want = "USE [appdb];\nGO\nCREATE SEQUENCE [dbo].[s] AS int START WITH 1 INCREMENT BY 1 NO MINVALUE NO MAXVALUE NO CYCLE NO CACHE"
	if got := scriptNewSequence(t, d); got != want {
		t.Errorf("no cache scripted\n%q\nwant\n%q", got, want)
	}
}

// The Permissions and Extended Properties pages run after the CREATE and name
// the sequence as typed, in the schema picked: ON [schema].[name] and a
// property at SCHEMA/SEQUENCE.
func TestNewSequencePermissionsAndExtendedPropertiesNameTheNewSequence(t *testing.T) {
	d := newSequenceTestDialog(t, newSequenceTestPrefetch())
	matrixPartsOf(t, d.forms[1]).perms.OnActivateCell(0, 1) // (none) -> Grant on the first permission
	editText(t, d.forms[2], "Name", "MS_Description")
	editText(t, d.forms[2], "Value", "Order numbers")
	clickButton(t, d.forms[2], "Add")
	// Typed last: the later pages must read the name when they run, not when built.
	editSelect(t, d.forms[0], "Sequence schema", "sales")
	editText(t, d.forms[0], "Sequence name", "OrderNo")

	got := scriptNewSequence(t, d)
	first := gosmo.SecurableSequence.PermissionNames()[0]
	for _, want := range []string{
		"CREATE SEQUENCE [sales].[OrderNo]",
		"GRANT " + first + " ON [sales].[OrderNo] TO [app]]user]",
		"@level0type = N'SCHEMA', @level0name = N'sales',",
		"@level1type = N'SEQUENCE', @level1name = N'OrderNo',",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script:\n%s\nwant it to contain: %s", got, want)
		}
	}
	if strings.Index(got, "CREATE SEQUENCE") > strings.Index(got, "GRANT") {
		t.Errorf("the GRANT is scripted before the CREATE:\n%s", got)
	}
}

// Everything the server would refuse for the request itself is refused before
// the round trip, naming the field.
func TestNewSequencePreflight(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(f *propsheet.Form)
		want string
	}{
		{"blank name", func(f *propsheet.Form) { editText(t, f, "Sequence name", "  ") }, "name is required"},
		{"taken in that schema, other case", func(f *propsheet.Form) {
			editSelect(t, f, "Sequence schema", "sales")
			editText(t, f, "Sequence name", "ORDERNO")
		}, "already exists"},
		{"zero increment", func(f *propsheet.Form) { editText(t, f, "Increment by", "0") }, "cannot be 0"},
		{"not a number", func(f *propsheet.Form) { editText(t, f, "Start value", "1e3") }, "start value"},
		{"minimum not below maximum", func(f *propsheet.Form) {
			editCheck(t, f, "No minimum (the type's)", false)
			editText(t, f, "Minimum value", "10")
			editCheck(t, f, "No maximum (the type's)", false)
			editText(t, f, "Maximum value", "10")
		}, "below the maximum"},
		{"start outside the range", func(f *propsheet.Form) {
			editCheck(t, f, "No minimum (the type's)", false)
			editText(t, f, "Minimum value", "5")
		}, "start value must lie"},
		{"past bigint for bigint", func(f *propsheet.Form) {
			editText(t, f, "Start value", "9223372036854775808")
		}, "start value must be a whole number from -9223372036854775808 to 9223372036854775807"},
		{"past the precision picked", func(f *propsheet.Form) {
			editSelect(t, f, "Data type", "decimal")
			editText(t, f, "Precision", "5")
			editText(t, f, "Start value", "100000")
		}, "from -99999 to 99999"},
		{"minimum not below maximum past bigint", func(f *propsheet.Form) {
			editSelect(t, f, "Data type", "numeric")
			editText(t, f, "Precision", "38")
			editCheck(t, f, "No minimum (the type's)", false)
			editText(t, f, "Minimum value", "10000000000000000001")
			editCheck(t, f, "No maximum (the type's)", false)
			editText(t, f, "Maximum value", "10000000000000000000")
		}, "below the maximum"},
		{"precision out of range", func(f *propsheet.Form) {
			editSelect(t, f, "Data type", "numeric")
			editText(t, f, "Precision", "39")
		}, "precision"},
	} {
		d := newSequenceTestDialog(t, newSequenceTestPrefetch("sales.OrderNo"))
		editText(t, d.forms[0], "Sequence name", "OrderNo")
		tc.edit(d.forms[0])
		if err := d.preflight(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: preflight = %v, want an error containing %q", tc.name, err, tc.want)
		}
	}

	// The name taken in sales is free in dbo.
	d := newSequenceTestDialog(t, newSequenceTestPrefetch("sales.OrderNo"))
	editText(t, d.forms[0], "Sequence name", "OrderNo")
	if err := d.preflight(); err != nil {
		t.Errorf("a name taken only in another schema was refused: %v", err)
	}
}

// sequenceCapabilityResponses answers the database probe with CREATE SEQUENCE
// held on the schemas in granted and not on those in denied, as gosmo's
// per-schema block tags them.
func sequenceCapabilityResponses(granted, denied []string) []fakeResponse {
	out := capabilityResponses(true, nil, nil, nil, nil)
	for i, r := range out {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, s := range granted {
			r.rows = append(r.rows, []driver.Value{"S:CREATE SEQUENCE", s, int64(1)})
		}
		for _, s := range denied {
			r.rows = append(r.rows, []driver.Value{"S:CREATE SEQUENCE", s, int64(0)})
		}
		out[i] = r
	}
	return out
}

// New Sequence is offered while any schema permits CREATE SEQUENCE and withheld
// once the probe says none does, and the dialog refuses a schema the probe
// denies, naming it: the right is per schema, so a principal granted it on one
// schema creates there and nowhere else.
func TestNewSequenceGateIsPerSchema(t *testing.T) {
	item := func(t *testing.T, granted, denied []string) (bool, string) {
		t.Helper()
		sc, _ := newGConn(t, sequenceCapabilityResponses(granted, denied)...)
		sc.ProbeCapabilities(sc.Server.Context())
		sc.DatabaseCapabilities(context.Background(), "appdb")
		folder := &explorerNode{data: nodeData{Type: NodeSequences, DBName: "appdb", conn: sc}}
		for _, it := range (&App{}).nodeMenuItems(folder) {
			if it.Label == "New Sequence..." {
				return it.Enabled == nil || it.Enabled(), it.Note
			}
		}
		t.Fatal("no New Sequence item")
		return false, ""
	}
	if on, _ := item(t, []string{"app"}, []string{"dbo", "sales"}); !on {
		t.Error("withheld with CREATE SEQUENCE held on one schema")
	}
	if on, note := item(t, nil, []string{"app", "dbo", "sales"}); on || note != "needs CREATE SEQUENCE on a schema" {
		t.Errorf("no schema permits: enabled %v, note %q; want withheld, naming the right", on, note)
	}
	if on, _ := item(t, nil, nil); !on {
		t.Error("withheld before any probe answered — the gate must fail open")
	}

	sc, _ := newGConn(t, sequenceCapabilityResponses([]string{"app"}, []string{"dbo", "sales"})...)
	sc.ProbeCapabilities(sc.Server.Context())
	sc.DatabaseCapabilities(context.Background(), "appdb")
	d := newSequenceTestDialog(t, newSequenceTestPrefetch())
	d.sc = sc
	d.buildPages(newSequenceTestPrefetch())
	editText(t, d.forms[0], "Sequence name", "s")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "schema dbo needs CREATE SEQUENCE") {
		t.Errorf("dbo, denied: preflight = %v", err)
	}
	editSelect(t, d.forms[0], "Sequence schema", "app")
	if err := d.preflight(); err != nil {
		t.Errorf("app, granted: preflight = %v", err)
	}
}

// The Sequences folder offers New Sequence between New Query and Refresh, and a
// sequence offers Properties.
func TestSequenceMenus(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeSequences, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Sequence...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
	leaf := &explorerNode{data: nodeData{Type: NodeSequence, DBName: "appdb", Schema: "dbo", Name: "s", conn: sc}}
	if !slices.Contains(labelsOf(a.nodeMenuItems(leaf)), "Properties...") {
		t.Errorf("sequence menu = %q, want Properties...", labelsOf(a.nodeMenuItems(leaf)))
	}
}

// The picker offers every schema a sequence can be created in: not sys or
// INFORMATION_SCHEMA (Msg 2760 even for sa), whatever their case.
func TestNewSequenceSchemaPickerLeavesOutSysAndInformationSchema(t *testing.T) {
	var in []*gosmo.Schema
	for _, n := range []string{"db_owner", "dbo", "information_schema", "s", "SYS"} {
		in = append(in, &gosmo.Schema{Name: n})
	}
	if got, want := sequenceSchemas("SQL_Latin1_General_CP1_CI_AS", in), []string{"db_owner", "dbo", "s"}; !slices.Equal(got, want) {
		t.Errorf("picker = %q, want %q", got, want)
	}
}
