package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ptr boxes a login name the way the Login Properties pages share one: a
// rename changes the identity every other page's lookup depends on.
func ptr(s string) *string { return &s }

// Login Properties > General is the page with the credentials on it. Four
// things it does are worth pinning, each of which fails silently in a
// direction that looks like success:
//
//   - Blank password fields mean "keep the current password", not "set it to
//     empty". A page that sent ALTER LOGIN ... WITH PASSWORD on every apply
//     would reset the password of any login whose Properties were opened.
//   - The rename is the *last* write, so every other write is addressed by
//     the name the server still has.
//   - Changing the mapped credential unmaps the old one first; ALTER LOGIN
//     takes one credential, so skipping the unmap leaves the login mapped to
//     something the page no longer shows.
//   - A Windows login has no password to change, and a built-in ## login has
//     no name to change — ALTER LOGIN ... WITH NAME succeeds on those and
//     orphans the matching users.

// loginGeneralResponses answers the five reads the page makes: the login,
// its details, the database list, the language list and the credentials.
func loginGeneralResponses(name, loginType string) []fakeResponse {
	return []fakeResponse{
		{match: "FROM sys.server_principals", arg: name, cols: 7, rows: [][]driver.Value{
			{name, []byte{0x01, 0x02}, loginType, false, "master", time.Now(), time.Now()},
		}},
		{match: "LOGINPROPERTY", cols: 12, rows: [][]driver.Value{{
			int64(0), int64(0), int64(0), true, true,
			time.Now(), time.Now(), int64(0), time.Now(),
			"us_english", "cred_old", "GRANT",
		}}},
		{match: "FROM sys.databases", cols: 8, rows: [][]driver.Value{
			{"master", int64(1), "ONLINE", "SIMPLE", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
			{"reporting", int64(6), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		{match: "FROM sys.syslanguages", cols: 3, rows: [][]driver.Value{
			{int64(0), "us_english", "English"},
			{int64(6), "Français", "French"},
		}},
		{match: "FROM   sys.credentials", cols: 4, rows: [][]driver.Value{
			{"cred_old", "DOMAIN\\svc_old", time.Now(), time.Now()},
			{"cred_new", "DOMAIN\\svc_new", time.Now(), time.Now()},
		}},
	}
}

// A page opened and closed must write nothing at all — and on this page that
// includes not sending a password.
func TestLoginGeneralWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
	_, apply := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("an untouched login page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// The default database and language are written under their own names, and
// they are the pair on this page most easily crossed: two adjacent Selects
// over two lists of names.
func TestLoginGeneralWritesTheDefaultsChosen(t *testing.T) {
	for _, tc := range []struct {
		label, value, want string
	}{
		// Not the first item in either list: a page that ignored the
		// selection would still pass on index 0.
		{"Default database", "reporting", "DEFAULT_DATABASE = [reporting]"},
		{"Default language", "Français", "DEFAULT_LANGUAGE"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
			form, apply := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

			editSelect(t, form, tc.label, tc.value)
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertOneStatement(t, inst, tc.want)
			if !strings.Contains(inst.Statements()[0], "[appuser]") {
				t.Errorf("statement does not name the login:\n%s", inst.Statements()[0])
			}
		})
	}
}

// A password is sent only when one was typed. This is the assertion the page
// exists to keep true: every other write here is recoverable.
func TestLoginGeneralSendsNoPasswordUnlessOneWasTyped(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
	form, apply := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

	// Something else changed, so apply has work to do and cannot pass by
	// doing nothing at all.
	editSelect(t, form, "Default database", "reporting")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, s := range inst.Statements() {
		if strings.Contains(strings.ToUpper(s), "PASSWORD") {
			t.Errorf("a page with both password fields blank wrote:\n%s", s)
		}
	}
}

func TestLoginGeneralChangesThePasswordWhenTyped(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
	form, apply := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

	editText(t, form, "Password", "n3wSecret!")
	editText(t, form, "Confirm password", "n3wSecret!")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "PASSWORD")
	if !strings.Contains(inst.Statements()[0], "n3wSecret!") {
		t.Errorf("the typed password did not reach the statement:\n%s", inst.Statements()[0])
	}
}

// Mismatched fields are caught before anything is written. The check lives on
// the password row rather than the confirmation, because Form.Validate only
// runs a dirty row's validator and a user who never touches Confirm would
// otherwise skip it entirely.
func TestLoginGeneralRefusesAMismatchedConfirmation(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
	form, _ := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

	editText(t, form, "Password", "n3wSecret!")
	editText(t, form, "Confirm password", "typo")

	if err := form.Validate(); err == nil {
		t.Fatal("a mismatched confirmation validated")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("validation wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// Changing the credential unmaps the old one before mapping the new: ALTER
// LOGIN carries one credential, so a page that only added would leave the
// login mapped to a credential it no longer displays.
func TestLoginGeneralUnmapsTheOldCredentialBeforeMappingTheNew(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
	form, apply := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

	editSelect(t, form, "Map to credential", "cred_new")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want the unmap then the map, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "cred_old") {
		t.Errorf("first statement is not the unmap of cred_old:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "cred_new") {
		t.Errorf("second statement is not the map to cred_new:\n%s", stmts[1])
	}
}

// The rename goes last. Every other write on this page addresses the login by
// name, so a rename in the middle leaves the ones after it aimed at a name
// the server no longer has.
func TestLoginGeneralRenamesLast(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("appuser", "SQL_LOGIN")...)
	name := ptr("appuser")
	form, apply := loadPage(t, pageLoginGeneral(sc, name), inst)

	editSelect(t, form, "Default database", "reporting")
	editText(t, form, "Login name", "appuser2")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want the default-database write then the rename, got %d:\n%s",
			len(stmts), strings.Join(stmts, "\n"))
	}
	if strings.Contains(stmts[0], "NAME = ") {
		t.Errorf("the rename ran first; the write after it addresses a login that no longer exists:\n%s",
			strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[1], "appuser2") {
		t.Errorf("last statement is not the rename:\n%s", stmts[1])
	}
	// The boxed name follows the rename, or every other page in the dialog
	// reloads under a name the server no longer has.
	if *name != "appuser2" {
		t.Errorf("the shared login name is still %q after the rename", *name)
	}
}

// A Windows login has no password for this page to change and no policy to
// set: SQL Server rejects both, and offering live controls would report a
// change that never happened.
func TestLoginGeneralOffersNoPasswordForAWindowsLogin(t *testing.T) {
	sc, inst := newFakeConn(t, loginGeneralResponses("DOMAIN\\alice", "WINDOWS_LOGIN")...)
	form, apply := loadPage(t, pageLoginGeneral(sc, ptr("DOMAIN\\alice")), inst)

	// Asserted by typing into them: a disabled row's Edit is a no-op, which
	// is the behaviour a user meets, and the enabled flag is not readable
	// from outside the package.
	for _, label := range []string{"Password", "Confirm password"} {
		row := textRow(t, form, label)
		row.Edit("whatever")
		if row.Value() != "" {
			t.Errorf("%q accepted an edit for a Windows login", label)
		}
	}
	// Ticked, not unticked: the page clears the policy boxes for a Windows
	// login, so unticking would be no edit at all and apply would have
	// nothing to skip.
	editCheck(t, form, "Enforce password policy", true)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a Windows login's password policy was written:\n%s", strings.Join(stmts, "\n"))
	}
}

// A built-in ## login cannot be renamed. The server allows it — and silently
// orphans the users in master and msdb that match it — so the page is what
// has to refuse.
func TestLoginGeneralWillNotRenameABuiltInLogin(t *testing.T) {
	const name = "##MS_PolicyEventProcessingLogin##"
	sc, inst := newFakeConn(t, loginGeneralResponses(name, "SQL_LOGIN")...)
	form, _ := loadPage(t, pageLoginGeneral(sc, ptr(name)), inst)

	for _, r := range form.Rows() {
		if tr, ok := r.(*propsheet.TextRow); ok && tr.Label() == sheetLabel("Login name") {
			t.Fatal("a built-in login's name is editable; renaming it orphans its users")
		}
	}
	if v, ok := findStatic(form, "Login name"); !ok || v != name {
		t.Errorf("the built-in login's name is shown as %q, want the static %q", v, name)
	}
}
