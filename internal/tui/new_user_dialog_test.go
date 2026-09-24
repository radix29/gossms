package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newUserTestDialog builds the three pages from a hand-made prefetch, the
// schemas bound to a real *gosmo.Database so Owned Schemas can write. The
// wanted object is never first in its list, so a page that ignored the
// selection would pick the wrong one.
func newUserTestDialog(t *testing.T, pf *nuserPrefetch, responses ...fakeResponse) *NewUserDialog {
	t.Helper()
	responses = append(responses, fakeResponse{match: "FROM   sys.schemas s", cols: 3, rows: [][]driver.Value{
		{"dbo", int64(1), "dbo"}, {"INFORMATION_SCHEMA", int64(3), "INFORMATION_SCHEMA"},
		{"sales", int64(5), "dbo"}, {"sys", int64(4), "sys"},
	}})
	sc, _ := newFakeConn(t, responses...)
	d := &NewUserDialog{dbName: "appdb"}
	d.sc = sc
	d.pages = []string{"General", "Owned Schemas", "Membership"}
	d.forms = make([]*propsheet.Form, 3)
	d.applyFns = make([]propApply, 3)
	if pf.existingNames == nil {
		pf.existingNames = newNameSet("", "alice", "db_owner")
	}
	if pf.logins == nil {
		pf.logins = []string{"applogin", `CONTOSO\bob`, "svclogin"}
		pf.windowsLogins = []string{`CONTOSO\bob`}
	}
	if pf.roles == nil {
		pf.roles = []string{"db_datareader", "db_datawriter", "db_owner"}
	}
	if pf.schemas == nil {
		// Read through gosmo, so each schema is bound to the database the
		// Owned Schemas page writes into, as the prefetch's are.
		var err error
		if pf.schemas, err = sc.Server.DatabaseRef("appdb").Schemas(context.Background()); err != nil {
			t.Fatalf("Schemas: %v", err)
		}
	}
	d.buildPages(pf)
	return d
}

// scriptNewUser runs the whole pipeline under Script Changes.
func scriptNewUser(t *testing.T, d *NewUserDialog) []string {
	t.Helper()
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	for i, fn := range d.applyFns {
		if err := fn(ctx); err != nil {
			t.Fatalf("apply page %d: %v", i, err)
		}
	}
	return script.Statements()
}

const nuserUse = "USE [appdb];\nGO\n"

func TestNewUserForLoginWithSchemaOwnershipAndRoles(t *testing.T) {
	d := newUserTestDialog(t, &nuserPrefetch{})
	f := d.forms[0]
	editText(t, f, "User name", "bob")
	editSelect(t, f, "Login name", "svclogin")
	editSelect(t, f, "Default schema", "sales")
	toggleByName(t, toggleGrid(t, d.forms[1]), "sales", 0)
	toggleByName(t, toggleGrid(t, d.forms[2]), "db_datawriter", 0)

	got := strings.Join(scriptNewUser(t, d), "\n")
	want := strings.Join([]string{
		nuserUse + "CREATE USER [bob] FOR LOGIN [svclogin] WITH DEFAULT_SCHEMA = [sales]",
		nuserUse + "ALTER AUTHORIZATION ON SCHEMA::[sales] TO [bob]",
		nuserUse + "ALTER ROLE [db_datawriter] ADD MEMBER [bob]",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// sys and INFORMATION_SCHEMA cannot change owner, so they are not offered.
func TestNewUserOwnedSchemasLeavesOutTheFixedSchemas(t *testing.T) {
	d := newUserTestDialog(t, &nuserPrefetch{})
	for _, row := range toggleGrid(t, d.forms[1]).Text() {
		if row[0] == "sys" || row[0] == "INFORMATION_SCHEMA" {
			t.Errorf("Owned Schemas offers %s, whose owner cannot change", row[0])
		}
	}
}

func TestNewUserKinds(t *testing.T) {
	for _, c := range []struct {
		name  string
		pf    nuserPrefetch
		edit  func(t *testing.T, f *propsheet.Form)
		want  string
		wantE string // a preflight refusal instead
	}{
		{name: "without login",
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "SQL user without login")
			},
			want: "CREATE USER [newuser] WITHOUT LOGIN"},
		{name: "contained", pf: nuserPrefetch{containment: "PARTIAL"},
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "SQL user with password")
				editText(t, f, "Password", "S3cret!pw")
				editText(t, f, "Confirm password", "S3cret!pw")
			},
			// The typed password never reaches the query window.
			want: "CREATE USER [newuser] WITH PASSWORD = N'<insert password here>'"},
		{name: "contained in a non-contained database", pf: nuserPrefetch{containment: "NONE"},
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "SQL user with password")
				editText(t, f, "Password", "S3cret!pw")
				editText(t, f, "Confirm password", "S3cret!pw")
			},
			wantE: "CONTAINMENT = NONE"},
		{name: "passwords differ", pf: nuserPrefetch{containment: "PARTIAL"},
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "SQL user with password")
				editText(t, f, "Password", "S3cret!pw")
				editText(t, f, "Confirm password", "other")
			},
			wantE: "do not match"},
		{name: "certificate", pf: nuserPrefetch{certNames: []string{"aaa_cert", "signer"}},
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "User mapped to a certificate")
				editSelect(t, f, "Mapped to", "signer")
			},
			want: "CREATE USER [newuser] FROM CERTIFICATE [signer]"},
		{name: "certificate none to pick",
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "User mapped to a certificate")
			},
			wantE: "select the certificate"},
		{name: "asymmetric key", pf: nuserPrefetch{asymKeyNames: []string{"aaa_key", "k2"}},
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "User mapped to an asymmetric key")
				editSelect(t, f, "Mapped to", "k2")
			},
			want: "CREATE USER [newuser] FROM ASYMMETRIC KEY [k2]"},
		{name: "contained Windows user",
			edit: func(t *testing.T, f *propsheet.Form) {
				editText(t, f, "User name", `CONTOSO\newuser`)
				editRadio(t, f, "User type", "Windows user")
			},
			want: `CREATE USER [CONTOSO\newuser]`},
		{name: "Windows user for login",
			edit: func(t *testing.T, f *propsheet.Form) {
				editText(t, f, "User name", `CONTOSO\bob`)
				editRadio(t, f, "User type", "Windows user")
				editSelect(t, f, "Login name", `CONTOSO\bob`)
			},
			want: `CREATE USER [CONTOSO\bob] FOR LOGIN [CONTOSO\bob]`},
		{name: "Entra", pf: nuserPrefetch{entra: true},
			edit: func(t *testing.T, f *propsheet.Form) {
				editText(t, f, "User name", "a@contoso.com")
				editRadio(t, f, "User type", "External user or group")
			},
			want: "CREATE USER [a@contoso.com] FROM EXTERNAL PROVIDER"},
		// A password typed and then abandoned for another kind is not sent.
		{name: "password left behind", pf: nuserPrefetch{containment: "PARTIAL"},
			edit: func(t *testing.T, f *propsheet.Form) {
				editRadio(t, f, "User type", "SQL user with password")
				editText(t, f, "Password", "S3cret!pw")
				editRadio(t, f, "User type", "SQL user without login")
			},
			want: "CREATE USER [newuser] WITHOUT LOGIN"},
		// The default kind with the login left alone: the picker starts on
		// (None), which must not reach FOR LOGIN as an empty name.
		{name: "no login picked",
			edit:  func(t *testing.T, f *propsheet.Form) {},
			wantE: "select the login"},
		{name: "name taken",
			edit: func(t *testing.T, f *propsheet.Form) {
				editText(t, f, "User name", "DB_OWNER")
				editRadio(t, f, "User type", "SQL user without login")
			},
			wantE: "already exists"},
	} {
		t.Run(c.name, func(t *testing.T) {
			pf := c.pf
			responses := []fakeResponse{{match: "containment_desc FROM sys.databases", cols: 1,
				rows: [][]driver.Value{{orDefault(pf.containment, "NONE")}}}}
			d := newUserTestDialog(t, &pf, responses...)
			f := d.forms[0]
			editText(t, f, "User name", "newuser")
			c.edit(t, f)
			if c.wantE != "" {
				err := d.preflight()
				if err == nil || !strings.Contains(err.Error(), c.wantE) {
					t.Fatalf("preflight = %v, want an error containing %q", err, c.wantE)
				}
				return
			}
			stmts := scriptNewUser(t, d)
			if len(stmts) != 1 || stmts[0] != nuserUse+c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), nuserUse+c.want)
			}
		})
	}
}

// Entra is offered only where the prefetch says the server takes it.
func TestNewUserOffersEntraOnlyWhereTheServerTakesIt(t *testing.T) {
	for _, entra := range []bool{false, true} {
		d := newUserTestDialog(t, &nuserPrefetch{entra: entra})
		var kind *propsheet.RadioRow
		for _, r := range d.forms[0].Rows() {
			if rr, ok := r.(*propsheet.RadioRow); ok && rr.Label() == "User type" {
				kind = rr
			}
		}
		if kind == nil {
			t.Fatal("no User type row")
		}
		has := false
		for _, o := range kind.Options() {
			has = has || o == "External user or group"
		}
		if has != entra {
			t.Errorf("entra=%v: Entra offered = %v", entra, has)
		}
	}
}
