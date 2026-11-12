package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/tuikit/propsheet"
)

// newSchemaTestDialog builds the New Schema pages the way show() would, minus
// the connection; responses script the fake server.
func newSchemaTestDialog(t *testing.T, pf *nschemaPrefetch, responses ...fakeResponse) *NewSchemaDialog {
	t.Helper()
	sc, _ := newGConn(t, responses...)
	d := &NewSchemaDialog{}
	d.dbName = "appdb"
	d.sc = sc
	d.pages = []string{"General", "Permissions", "Extended Properties"}
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.buildPages(pf)
	return d
}

func scriptNewSchema(t *testing.T, d *NewSchemaDialog) string {
	t.Helper()
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	if err := d.applyFns[0](ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return strings.Join(script.Statements(), "\n")
}

// Left at (Default) the AUTHORIZATION clause is omitted, so the server makes
// the creator the owner; a picked owner is named, quoted.
func TestNewSchemaScriptsTheOwnerOnlyWhenPicked(t *testing.T) {
	pf := &nschemaPrefetch{existingNames: newNameSet(""), owners: []string{"dbo", "app]user"}}

	d := newSchemaTestDialog(t, pf)
	editText(t, d.forms[0], "Schema name", "  sales  ")
	if got, want := scriptNewSchema(t, d), "USE [appdb];\nGO\nCREATE SCHEMA [sales]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	d = newSchemaTestDialog(t, pf)
	editText(t, d.forms[0], "Schema name", "sales")
	owner := selectRow(t, d.forms[0], "Schema owner")
	owner.SetSelected(slices.Index(owner.Items(), "app]user"))
	if got, want := scriptNewSchema(t, d), "USE [appdb];\nGO\nCREATE SCHEMA [sales] AUTHORIZATION [app]]user]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The Permissions and Extended Properties pages run after the CREATE and name
// the schema as typed: a GRANT on SCHEMA::[name] and a property at level0
// SCHEMA. Left untouched, they add nothing to the script.
func TestNewSchemaPermissionsAndExtendedPropertiesNameTheNewSchema(t *testing.T) {
	pf := &nschemaPrefetch{existingNames: newNameSet(""),
		principals: []permPrincipal{{Name: "app]user", Type: gosmo.PrincipalType("SQL_USER")}}}

	runAll := func(d *NewSchemaDialog) string {
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

	d := newSchemaTestDialog(t, pf, dbByNameResp("appdb", 5))
	editText(t, d.forms[0], "Schema name", "sales")
	if got, want := runAll(d), "USE [appdb];\nGO\nCREATE SCHEMA [sales]"; got != want {
		t.Errorf("untouched pages scripted %q, want %q", got, want)
	}

	d = newSchemaTestDialog(t, pf, dbByNameResp("appdb", 5))
	matrixPartsOf(t, d.forms[1]).perms.OnActivateCell(0, 1) // (none) -> Grant on the first permission
	editText(t, d.forms[2], "Name", "MS_Description")
	editText(t, d.forms[2], "Value", "Sales tables")
	clickButton(t, d.forms[2], "Add")
	// Typed last: the later pages must read the name when they run, not when built.
	editText(t, d.forms[0], "Schema name", "sales")

	got := runAll(d)
	first := gosmo.SecurableSchema.PermissionNames()[0]
	for _, want := range []string{
		"CREATE SCHEMA [sales]",
		"GRANT " + first + " ON SCHEMA::[sales] TO [app]]user]",
		"sp_addextendedproperty",
		"@level0type = N'SCHEMA', @level0name = N'sales'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script:\n%s\nwant it to contain: %s", got, want)
		}
	}
	if strings.Index(got, "CREATE SCHEMA") > strings.Index(got, "GRANT") {
		t.Errorf("the GRANT is scripted before the CREATE:\n%s", got)
	}
}

// A blank name and a name already taken (under the database's collation) are
// refused before the round trip.
func TestNewSchemaPreflight(t *testing.T) {
	d := newSchemaTestDialog(t, &nschemaPrefetch{existingNames: newNameSet("", "Sales")})
	editText(t, d.forms[0], "Schema name", "   ")
	if err := d.preflight(); err == nil {
		t.Error("a blank name passed the preflight")
	}
	editText(t, d.forms[0], "Schema name", "Sales")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("an existing name passed the preflight: %v", err)
	}
}

// sys, INFORMATION_SCHEMA and public are refused as AUTHORIZATION by every
// login, so the picker never offers them; guest and fixed roles are accepted.
func TestNewSchemaOwnerPickerLeavesOutUnownablePrincipals(t *testing.T) {
	for _, n := range []string{"sys", "INFORMATION_SCHEMA", "public"} {
		if !slices.Contains(nschemaUnownable, n) {
			t.Errorf("%s is offered as an owner", n)
		}
	}
	for _, n := range []string{"guest", "dbo", "db_datareader"} {
		if slices.Contains(nschemaUnownable, n) {
			t.Errorf("%s, which SQL Server accepts as an owner, is not offered", n)
		}
	}
}

// The Schemas folder offers New Schema between New Query and Refresh.
func TestSchemasFolderMenu(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeSchemas, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Schema...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
}

// TestNewSchemaGateMatchesWhatTheServerAllowed is the probed CREATE SCHEMA
// column (majors 13 and 17): each permitting right reads CREATE SCHEMA 1
// through HAS_PERMS_BY_NAME, and a user holding none reads every one 0.
func TestNewSchemaGateMatchesWhatTheServerAllowed(t *testing.T) {
	all := []string{"CREATE SCHEMA", "ALTER ANY SCHEMA", "ALTER", "CONTROL"}
	for _, tc := range []struct {
		name    string
		granted []string
		want    bool
	}{
		{"nothing", nil, false},
		{"CREATE SCHEMA", []string{"CREATE SCHEMA"}, true},
		{"ALTER ANY SCHEMA, or db_ddladmin", []string{"CREATE SCHEMA", "ALTER ANY SCHEMA"}, true},
		{"ALTER on the database", []string{"CREATE SCHEMA", "ALTER ANY SCHEMA", "ALTER"}, true},
		{"CONTROL on the database", all, true},
	} {
		var denied []string
		for _, n := range all {
			if !slices.Contains(tc.granted, n) {
				denied = append(denied, n)
			}
		}
		sc, _ := newGConn(t, capabilityResponses(true, nil, nil, tc.granted, denied)...)
		sc.ProbeCapabilities(sc.Server.Context())
		sc.DatabaseCapabilities(context.Background(), "appdb")
		folder := &explorerNode{data: nodeData{Type: NodeSchemas, DBName: "appdb", conn: sc}}
		var found bool
		for _, it := range (&App{}).nodeMenuItems(folder) {
			if it.Label != "New Schema..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: New Schema enabled = %v, want %v", tc.name, got, tc.want)
			}
		}
		if !found {
			t.Fatalf("%s: no New Schema item", tc.name)
		}
	}
}
