package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Database Scoped Credential Properties > General.
//
// The credential scripted here is the second of three, so a page that ignored
// its selection and read whichever row sorts first cannot pass. The by-name
// read is scoped with arg: and placed before the list read, because gosmo's
// DatabaseScopedCredentialByNameContext query also contains
// "FROM   sys.database_scoped_credentials" and responses match by substring in
// order.
//
// Every assertion on the write uses StatementsIn("appdb") rather than
// Statements(): the bare USE is stripped as plumbing, and with it the only
// record that the ALTER landed in the database the page was opened on.

const dbScopedCredUnderTest = "app_cred"

var dbScopedCredCreated = time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)

func dbScopedCredDatabaseRow() fakeResponse {
	return fakeResponse{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
		"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false,
		dbScopedCredCreated,
	}}}
}

func dbScopedCredResponses() []fakeResponse {
	return []fakeResponse{
		dbScopedCredDatabaseRow(),
		{
			match: "FROM   sys.database_scoped_credentials",
			arg:   dbScopedCredUnderTest,
			cols:  5,
			rows: [][]driver.Value{
				{int64(2), dbScopedCredUnderTest, "SHARED ACCESS SIGNATURE",
					dbScopedCredCreated, dbScopedCredCreated},
			},
		},
		{
			match: "FROM   sys.database_scoped_credentials",
			cols:  5,
			rows: [][]driver.Value{
				{int64(1), "aaa_first_cred", "identity_first", dbScopedCredCreated, dbScopedCredCreated},
				{int64(2), dbScopedCredUnderTest, "SHARED ACCESS SIGNATURE",
					dbScopedCredCreated, dbScopedCredCreated},
				{int64(3), "zzz_last_cred", "identity_last", dbScopedCredCreated, dbScopedCredCreated},
			},
		},
	}
}

func loadDBScopedCredGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, dbScopedCredResponses()...)
	name := dbScopedCredUnderTest
	form, apply := loadPage(t, pageDatabaseScopedCredentialGeneral(sc, "appdb", &name), inst)
	return inst, apply, form
}

// The page must load the credential it was opened on, not the first row in
// sys.database_scoped_credentials.
func TestDBScopedCredGeneralLoadsTheSelectedCredential(t *testing.T) {
	_, _, form := loadDBScopedCredGeneralPage(t)

	if got := textRow(t, form, "Identity").Value(); got != "SHARED ACCESS SIGNATURE" {
		t.Errorf("Identity is %q, want the selected credential's %q", got, "SHARED ACCESS SIGNATURE")
	}
	// The database is on the page: a credential's name is only unique within
	// one, so the header alone is not enough to tell two apart.
	var seen bool
	for _, r := range form.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == sheetLabel("Database") {
			seen = true
			if sr.Value() != "appdb" {
				t.Errorf("Database is %q, want appdb", sr.Value())
			}
		}
	}
	if !seen {
		t.Error("the page does not say which database the credential is in")
	}
}

// A page opened and closed writes nothing — and on this page that includes not
// sending an ALTER, which would clear the secret.
func TestDBScopedCredGeneralWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _ := loadDBScopedCredGeneralPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// The rule this page exists to enforce, the same one scope down: ALTER
// DATABASE SCOPED CREDENTIAL resets both halves, and omitting SECRET sets the
// stored secret to NULL. Changing the identity with the password blank must
// therefore be refused, not applied.
func TestDBScopedCredGeneralRefusesAnIdentityChangeWithNoPassword(t *testing.T) {
	inst, apply, form := loadDBScopedCredGeneralPage(t)

	editText(t, form, "Identity", "Managed Identity")

	err := apply(t.Context())
	if err == nil {
		t.Fatal("the identity change was applied with a blank password, which clears the stored secret")
	}
	if !strings.Contains(err.Error(), "clears the stored secret") {
		t.Errorf("the error does not say why it was refused: %v", err)
	}
	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("a statement was sent anyway:\n%s", strings.Join(stmts, "\n"))
	}
}

// With a password typed, both halves go in one ALTER — and it runs in the
// database the page was opened on, not the connection's default.
func TestDBScopedCredGeneralWritesBothHalvesInTheRightDatabase(t *testing.T) {
	inst, apply, form := loadDBScopedCredGeneralPage(t)

	editText(t, form, "Identity", "Managed Identity")
	editText(t, form, "Password", "sv=2019")
	editText(t, form, "Confirm password", "sv=2019")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb",
		`ALTER DATABASE SCOPED CREDENTIAL [app_cred] WITH IDENTITY = N'Managed Identity', SECRET = N'sv=2019'`)
	assertNoStatementsIn(t, inst, "master", "msdb")
}

// A password typed on its own — the ordinary "rotate the key" gesture — still
// has to carry the identity, since ALTER DATABASE SCOPED CREDENTIAL requires
// it and would otherwise be a syntax error.
func TestDBScopedCredGeneralPasswordOnlyChangeKeepsTheIdentity(t *testing.T) {
	inst, apply, form := loadDBScopedCredGeneralPage(t)

	editText(t, form, "Password", "sv=2019")
	editText(t, form, "Confirm password", "sv=2019")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", `IDENTITY = N'SHARED ACCESS SIGNATURE'`)
}

func TestDBScopedCredGeneralRefusesAMismatchedConfirmation(t *testing.T) {
	inst, apply, form := loadDBScopedCredGeneralPage(t)

	editText(t, form, "Password", "sv=2019")
	editText(t, form, "Confirm password", "sv=2020")

	if err := apply(t.Context()); err == nil {
		t.Fatal("a mismatched confirmation was accepted")
	}
	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("a statement was sent anyway:\n%s", strings.Join(stmts, "\n"))
	}
}

// SQL Server stores IDENTITY verbatim, so a pasted value with trailing
// whitespace produces a credential that fails to authenticate for a reason
// nothing on the page shows.
func TestDBScopedCredGeneralTrimsTheIdentity(t *testing.T) {
	inst, apply, form := loadDBScopedCredGeneralPage(t)

	editText(t, form, "Identity", "  Managed Identity  ")
	editText(t, form, "Password", "sv=2019")
	editText(t, form, "Confirm password", "sv=2019")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", `IDENTITY = N'Managed Identity'`)
}

// An identity that is only whitespace is refused rather than sent as an empty
// IDENTITY, which the engine rejects with a message naming nothing useful.
func TestDBScopedCredGeneralRefusesABlankIdentity(t *testing.T) {
	inst, apply, form := loadDBScopedCredGeneralPage(t)

	editText(t, form, "Identity", "   ")
	editText(t, form, "Password", "sv=2019")
	editText(t, form, "Confirm password", "sv=2019")

	if err := apply(t.Context()); err == nil {
		t.Fatal("a blank identity was accepted")
	}
	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("a statement was sent anyway:\n%s", strings.Join(stmts, "\n"))
	}
}

// The page's rights are CONTROL on the database alone. ALTER on the database
// does not permit the write — probed live — so a set that included
// rightAlterDatabase would open the page fully editable for a principal the
// server then refuses.
func TestDBScopedCredPagesRequireControlOnly(t *testing.T) {
	sc, _ := newFakeConn(t)
	pages := databaseScopedCredentialPropPages(sc, "appdb", dbScopedCredUnderTest)
	if len(pages) != 1 {
		t.Fatalf("want one page, got %d", len(pages))
	}
	if pages[0].requiresIn != "appdb" {
		t.Errorf("the page asks about database %q, want appdb", pages[0].requiresIn)
	}
	if len(pages[0].requires) != 1 || pages[0].requires[0].name != "CONTROL" {
		t.Errorf("requires = %v, want exactly [CONTROL]", pages[0].requires)
	}
}
