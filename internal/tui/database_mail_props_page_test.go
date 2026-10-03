package tui

import (
	"context"
	"database/sql/driver"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Database Mail Properties, driven through the fake driver. Statement text is
// gosmo's to test; these pin what the dialog asks for and in what order — the
// plan's phases across pages, names shared between pages before Apply, and
// the credential kept, replaced or refused.

const (
	mailGrantRead  = "FROM   msdb.dbo.sysmail_principalprofile pp"
	mailParamRead  = "FROM msdb.dbo.sysmail_configuration"
	mailUsersRead  = "FROM   sys.database_principals"
	mailConfigRead = "FROM   sys.configurations\nWHERE  name = @p1"
	mailSecret     = "s3cret-Pa55"
)

// mailPageReads is every read the five pages make: mailConfig's profiles
// (ops, the public default, with relay2 then relay1; alerts, empty) and
// accounts (relay1 Basic as "mailer", relay2 anonymous), the public grant of
// ops, two msdb users, the parameters and XPs at 0. A profile the test
// creates reads no links, so SetAccounts adds every account.
func mailPageReads(created ...string) []fakeResponse {
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	var r []fakeResponse
	for _, p := range created {
		r = append(r, fakeResponse{match: mailProfAcctRead, arg: p, cols: 4})
	}
	r = append(r, mailConfig()...)
	return append(r,
		fakeResponse{match: mailGrantRead, cols: 5, rows: [][]driver.Value{
			{int64(1), "ops", []byte{0}, "public", true},
		}},
		fakeResponse{match: mailUsersRead, db: "msdb", cols: 7, rows: [][]driver.Value{
			{"##MS_PolicyEventProcessingLogin##", int64(4), "SQL_USER", "dbo", at, at, "INSTANCE"},
			{"app_sender", int64(5), "SQL_USER", "dbo", at, at, "INSTANCE"},
			{"guest", int64(2), "SQL_USER", nil, at, at, "NONE"},
			{"report_user", int64(6), "SQL_USER", "dbo", at, at, "INSTANCE"},
		}},
		fakeResponse{match: mailParamRead, cols: 2, rows: [][]driver.Value{
			{"AccountRetryAttempts", "1"}, {"AccountRetryDelay", "60"},
			{"DatabaseMailExeMinimumLifeTime", "600"}, {"DefaultAttachmentEncoding", "MIME"},
			{"LoggingLevel", "2"}, {"MaxFileSize", "1000000"}, {"ProhibitedExtensions", "exe,dll,vbs,js"},
		}},
		fakeResponse{match: mailConfigRead, cols: 9, rows: [][]driver.Value{
			{int64(16386), "Database Mail XPs", int64(0), int64(0), int64(0), int64(1), true, true, "Enable or disable Database Mail XPs"},
		}},
		mailStatus("DISABLED"),
	)
}

// mailPages loads the five pages of one showing — one model, as the dialog
// builds them — in page order.
func mailPages(t *testing.T, sc *db.ServerConn, inst *fakeInstance) ([]*propsheet.Form, []propApply) {
	t.Helper()
	pages := databaseMailPropPages(nil, sc)
	forms := make([]*propsheet.Form, len(pages))
	applies := make([]propApply, len(pages))
	for i, p := range pages {
		forms[i], applies[i] = loadPage(t, p, inst)
	}
	return forms, applies
}

// runMailApply runs the dirty pages' applies as the dialog's Apply does.
func runMailApply(ctx context.Context, forms []*propsheet.Form, applies []propApply) error {
	var fns []propApply
	for i, f := range forms {
		if f.Dirty() {
			fns = append(fns, applies[i])
		}
	}
	return plannedApply(fns, func(ctx context.Context, plan *applyPlan) error { return plan.run(ctx) })(ctx)
}

// mailGrid is the page's grid at index i, top to bottom — the Profiles and
// Profile Security pages have two.
func mailGrid(t *testing.T, f *propsheet.Form, i int) *controls.DataGrid {
	t.Helper()
	var grids []*controls.DataGrid
	for _, r := range f.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			grids = append(grids, gr.Grid)
		}
	}
	if i >= len(grids) {
		t.Fatalf("the page has %d grids, not %d", len(grids), i+1)
	}
	return grids[i]
}

// procCalls is every sysmail procedure (and the XPs sp_configure) the
// statements call, in the order they call them — a batch such as SetAccounts'
// contributes each of its calls.
func procCalls(stmts []string) []string {
	var out []string
	for _, s := range stmts {
		out = append(out, mailCallPattern.FindAllString(s, -1)...)
	}
	return out
}

var mailCallPattern = regexp.MustCompile(`sysmail_[a-z_]+_sp\b|sp_configure N'Database Mail XPs'`)

// TestMailFromScratchIsOneApply is the reason the pages share names: an
// account, a profile using it and its grant, all new, in one Apply — the
// account and the profile created before anything names them.
func TestMailFromScratchIsOneApply(t *testing.T) {
	sc, inst := newFakeConn(t, mailPageReads("gossms_ops")...)
	forms, applies := mailPages(t, sc, inst)
	accounts, profiles, security, params := forms[mailPageAccounts], forms[mailPageProfiles], forms[mailPageSecurity], forms[mailPageParameters]

	editText(t, accounts, "New account name", "gossms_relay")
	clickButton(t, accounts, "Add")
	editText(t, accounts, "E-mail address", "dba@example.com")
	editText(t, accounts, "SMTP server", "192.0.2.1")
	editRadio(t, accounts, "Authentication", "Basic")
	editText(t, accounts, "User name", "relayuser")
	editText(t, accounts, "Password", mailSecret)
	editText(t, accounts, "Confirm password", mailSecret)

	editText(t, profiles, "New profile name", "gossms_ops")
	clickButton(t, profiles, "Add")
	// Offered before any Apply: the Accounts page published it.
	if items := selectRow(t, profiles, "Account to add").Items(); !slices.Contains(items, "gossms_relay") {
		t.Fatalf("Account to add offers %q — the new account is missing", items)
	}
	addRow := selectRow(t, profiles, "Account to add")
	addRow.Edit(slices.Index(addRow.Items(), "gossms_relay"))
	clickButton(t, profiles, "Add Account")

	activateGridCell(t, mailGrid(t, security, 0), 0, "gossms_ops", 2)
	editText(t, params, "Account retry attempts", "3")

	if err := runMailApply(context.Background(), forms, applies); err != nil {
		t.Fatal(err)
	}
	stmts := inst.Statements()
	want := []string{
		"sysmail_add_account_sp", "sysmail_add_profile_sp", "sysmail_add_profileaccount_sp",
		"sysmail_update_principalprofile_sp", "sysmail_add_principalprofile_sp", "sysmail_configure_sp",
	}
	if got := procCalls(stmts); !slices.Equal(got, want) {
		t.Fatalf("calls = %q\nwant    %q\n%s", got, want, strings.Join(stmts, "\n"))
	}
	all := strings.Join(stmts, "\n")
	for _, s := range []string{"@account_name = N'gossms_relay'", "@profile_name = N'gossms_ops'", "@username = N'relayuser'",
		"@password = N'" + mailSecret + "'", "@principal_name = N'public'", "N'AccountRetryAttempts'"} {
		if !strings.Contains(all, s) {
			t.Errorf("no statement carries %s:\n%s", s, all)
		}
	}
	// The new public default replaces ops: cleared first, set second.
	if !strings.Contains(all, "@profile_name = N'ops'") {
		t.Errorf("ops kept its public default:\n%s", all)
	}
}

// TestMailScriptChangesHidesThePassword: Script Changes is the same plan
// under WithScript, and the typed password never reaches the script.
func TestMailScriptChangesHidesThePassword(t *testing.T) {
	sc, inst := newFakeConn(t, mailPageReads()...)
	forms, applies := mailPages(t, sc, inst)
	accounts := forms[mailPageAccounts]
	selectGridRow(t, plainGrid(t, accounts), 0, "relay1")
	editText(t, accounts, "Password", mailSecret)
	editText(t, accounts, "Confirm password", mailSecret)

	ctx, script := gosmo.WithScript(context.Background())
	if err := runMailApply(ctx, forms, applies); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(script.Statements(), "\n")
	if strings.Contains(text, mailSecret) {
		t.Fatalf("the script carries the typed password:\n%s", text)
	}
	if !strings.Contains(text, "@password = N'"+gosmo.PasswordPlaceholder+"'") || !strings.Contains(text, "@username = N'mailer'") {
		t.Fatalf("the script does not replace relay1's credential:\n%s", text)
	}
	if n := len(inst.Statements()); n != 0 {
		t.Fatalf("Script Changes ran %d statements", n)
	}
}

// TestMailAccountKeepsOrReplacesItsCredential pins W8's rule: blank password
// and unchanged user name keep the stored credential; a changed user name
// needs the password again; switching to anonymous drops it.
func TestMailAccountKeepsOrReplacesItsCredential(t *testing.T) {
	ctx := context.Background()
	t.Run("keep", func(t *testing.T) {
		sc, inst := newFakeConn(t, mailPageReads()...)
		forms, applies := mailPages(t, sc, inst)
		a := forms[mailPageAccounts]
		selectGridRow(t, plainGrid(t, a), 0, "relay1")
		editText(t, a, "Display name", "Ops mailer")
		if err := runMailApply(ctx, forms, applies); err != nil {
			t.Fatal(err)
		}
		all := strings.Join(inst.Statements(), "\n")
		if !strings.Contains(all, "@no_credential_change = 1") || strings.Contains(all, "@username") {
			t.Fatalf("a display-name edit touched the credential:\n%s", all)
		}
		if !strings.Contains(all, "@account_name = N'relay1'") {
			t.Fatalf("the edit went to another account:\n%s", all)
		}
	})
	t.Run("user without password", func(t *testing.T) {
		sc, inst := newFakeConn(t, mailPageReads()...)
		forms, applies := mailPages(t, sc, inst)
		a := forms[mailPageAccounts]
		selectGridRow(t, plainGrid(t, a), 0, "relay1")
		editText(t, a, "User name", "other")
		err := runMailApply(ctx, forms, applies)
		if err == nil || !strings.Contains(err.Error(), "typed again") {
			t.Fatalf("err = %v, want the password asked for again", err)
		}
		if n := len(inst.Statements()); n != 0 {
			t.Fatalf("%d statements ran before the refusal", n)
		}
	})
	t.Run("to anonymous", func(t *testing.T) {
		sc, inst := newFakeConn(t, mailPageReads()...)
		forms, applies := mailPages(t, sc, inst)
		a := forms[mailPageAccounts]
		selectGridRow(t, plainGrid(t, a), 0, "relay1")
		editRadio(t, a, "Authentication", "Anonymous")
		if err := runMailApply(ctx, forms, applies); err != nil {
			t.Fatal(err)
		}
		all := strings.Join(inst.Statements(), "\n")
		if strings.Contains(all, "@no_credential_change") || strings.Contains(all, "@username") ||
			!strings.Contains(all, "@use_default_credentials = 0") {
			t.Fatalf("switching to anonymous did not drop the credential:\n%s", all)
		}
	})
}

// TestMailCredentialFieldsNeedAlterAnyCredential: msdb db_owner configures
// accounts, but a Basic credential is a server credential (W8, Msg 15247).
func TestMailCredentialFieldsNeedAlterAnyCredential(t *testing.T) {
	responses := append(capabilityResponses(true, nil, []string{"ALTER ANY CREDENTIAL", "CONTROL SERVER"}, nil, nil),
		mailPageReads()...)
	sc, inst := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	forms, applies := mailPages(t, sc, inst)
	a := forms[mailPageAccounts]
	selectGridRow(t, plainGrid(t, a), 0, "relay2")
	editRadio(t, a, "Authentication", "Basic")
	if !textRow(t, a, "User name").ReadOnly() || !textRow(t, a, "Password").ReadOnly() {
		t.Fatal("user name and password are editable without ALTER ANY CREDENTIAL")
	}
	err := runMailApply(context.Background(), forms, applies)
	if err == nil || !strings.Contains(err.Error(), "ALTER ANY CREDENTIAL") {
		t.Fatalf("err = %v, want the right named", err)
	}
	// Another account's settings still apply.
	sc2, inst2 := newFakeConn(t, responses...)
	sc2.ProbeCapabilities()
	forms, applies = mailPages(t, sc2, inst2)
	a = forms[mailPageAccounts]
	selectGridRow(t, plainGrid(t, a), 0, "relay2")
	editText(t, a, "Port", "2525")
	if err := runMailApply(context.Background(), forms, applies); err != nil {
		t.Fatal(err)
	}
	if all := strings.Join(inst2.Statements(), "\n"); !strings.Contains(all, "@port = 2525") || !strings.Contains(all, "@account_name = N'relay2'") {
		t.Fatalf("relay2's port change not sent:\n%s", all)
	}
}

// TestMailBasicAccountUntouchableWithoutAlterAnyCredential: without ALTER ANY
// CREDENTIAL the sysmail procedures see no credential, so deleting a Basic
// account orphans it and any update — even with @no_credential_change = 1 —
// unlinks it (W14, live on 17). Neither may reach the server.
func TestMailBasicAccountUntouchableWithoutAlterAnyCredential(t *testing.T) {
	responses := append(capabilityResponses(true, nil, []string{"ALTER ANY CREDENTIAL", "CONTROL SERVER"}, nil, nil),
		mailPageReads()...)
	t.Run("other setting", func(t *testing.T) {
		sc, inst := newFakeConn(t, responses...)
		sc.ProbeCapabilities()
		forms, applies := mailPages(t, sc, inst)
		a := forms[mailPageAccounts]
		selectGridRow(t, plainGrid(t, a), 0, "relay1")
		editText(t, a, "Display name", "Relay one")
		err := runMailApply(context.Background(), forms, applies)
		if err == nil || !strings.Contains(err.Error(), "unlinks its password") {
			t.Fatalf("err = %v, want the edit refused", err)
		}
		assertNoMailWrites(t, inst)
	})
	t.Run("remove", func(t *testing.T) {
		sc, inst := newFakeConn(t, responses...)
		sc.ProbeCapabilities()
		forms, applies := mailPages(t, sc, inst)
		a := forms[mailPageAccounts]
		selectGridRow(t, plainGrid(t, a), 0, "relay1")
		clickButton(t, a, "Remove")
		gridRowIndex(t, plainGrid(t, a), 0, "relay1") // still listed, not "(removed)"
		if err := runMailApply(context.Background(), forms, applies); err != nil {
			t.Fatal(err)
		}
		assertNoMailWrites(t, inst)
	})
}

// assertNoMailWrites fails when anything but a read reached the server.
func assertNoMailWrites(t *testing.T, inst *fakeInstance) {
	t.Helper()
	for _, s := range inst.Statements() {
		if strings.Contains(s, "sysmail_update_") || strings.Contains(s, "sysmail_delete_") || strings.Contains(s, "sysmail_add_") {
			t.Fatalf("a write ran:\n%s", s)
		}
	}
}

// TestMailRenamesRunAfterWhatNamesTheOldName: the Profiles page writes a
// renamed profile's accounts under the name the server has, so the rename
// comes after.
func TestMailRenamesRunAfterWhatNamesTheOldName(t *testing.T) {
	sc, inst := newFakeConn(t, mailPageReads()...)
	forms, applies := mailPages(t, sc, inst)
	p := forms[mailPageProfiles]
	selectGridRow(t, mailGrid(t, p, 0), 0, "ops")
	editText(t, p, "Profile name", "operations")
	// relay1 is second; move it first.
	selectGridRow(t, mailGrid(t, p, 1), 1, "relay1")
	clickButton(t, p, "Move Up")
	if err := runMailApply(context.Background(), forms, applies); err != nil {
		t.Fatal(err)
	}
	stmts := inst.Statements()
	got := procCalls(stmts)
	want := []string{"sysmail_update_profileaccount_sp", "sysmail_update_profileaccount_sp", "sysmail_update_profile_sp"}
	if !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q\n%s", got, want, strings.Join(stmts, "\n"))
	}
	// SetAccounts is one batch; it must address the profile as ops.
	if len(stmts) != 2 || strings.Contains(stmts[0], "operations") || !strings.Contains(stmts[0], "@profile_name = N'ops'") {
		t.Errorf("the account order is not written under the stored name, before the rename:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestMailRemovedAccountLeavesTheProfilesPage: an account removed on the
// Accounts page is marked on the Profiles page at once and left out of the
// profile's account list, which the drop would empty anyway.
func TestMailRemovedAccountLeavesTheProfilesPage(t *testing.T) {
	sc, inst := newFakeConn(t, mailPageReads()...)
	forms, applies := mailPages(t, sc, inst)
	a, p := forms[mailPageAccounts], forms[mailPageProfiles]
	selectGridRow(t, plainGrid(t, a), 0, "relay2")
	clickButton(t, a, "Remove")
	selectGridRow(t, mailGrid(t, p, 0), 0, "ops")
	gridRowIndex(t, mailGrid(t, p, 1), 1, "relay2 (removed)")
	editText(t, p, "Description", "on call")
	if err := runMailApply(context.Background(), forms, applies); err != nil {
		t.Fatal(err)
	}
	got := procCalls(inst.Statements())
	want := []string{"sysmail_delete_profileaccount_sp", "sysmail_update_profileaccount_sp", "sysmail_update_profile_sp", "sysmail_delete_account_sp"}
	if !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

// TestMailPrivateGrantsFollowThePrincipal: the private grid is the chosen
// principal's, and a grant goes to that principal — not the first listed —
// and neither guest, public's SID, nor a ##…## server user is offered.
func TestMailPrivateGrantsFollowThePrincipal(t *testing.T) {
	sc, inst := newFakeConn(t, mailPageReads()...)
	forms, applies := mailPages(t, sc, inst)
	s := forms[mailPageSecurity]
	principal := selectRow(t, s, "Principal (msdb user)")
	if items := principal.Items(); slices.Contains(items, "guest") || !slices.Equal(items, []string{"app_sender", "report_user"}) {
		t.Fatalf("principals = %q", items)
	}
	principal.Edit(slices.Index(principal.Items(), "report_user"))
	if principal.Dirty() {
		t.Fatal("choosing a principal made the page dirty")
	}
	activateGridCell(t, mailGrid(t, s, 1), 0, "alerts", 1)
	if err := runMailApply(context.Background(), forms, applies); err != nil {
		t.Fatal(err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 || !strings.Contains(stmts[0], "sysmail_add_principalprofile_sp") ||
		!strings.Contains(stmts[0], "@principal_name = N'report_user'") || !strings.Contains(stmts[0], "@profile_name = N'alerts'") ||
		!strings.Contains(stmts[0], "@is_default = 0") {
		t.Fatalf("statements = %q", stmts)
	}
}

// TestMailGeneralTurnsOnXPs: General writes the option through
// ApplyConfiguration, first in the plan, and System Parameters writes the
// logging level as its number.
func TestMailGeneralTurnsOnXPs(t *testing.T) {
	sc, inst := newFakeConn(t, mailPageReads()...)
	forms, applies := mailPages(t, sc, inst)
	editSelect(t, forms[mailPageParameters], "Logging level", "Verbose")
	var xps *propsheet.CheckRow
	for _, r := range forms[mailPageGeneral].Rows() {
		if c, ok := r.(*propsheet.CheckRow); ok && c.Label() == mailXPsOption {
			xps = c
		}
	}
	if xps == nil || xps.Checked() {
		t.Fatal("General has no unticked Database Mail XPs box")
	}
	xps.Edit(true)
	if err := runMailApply(context.Background(), forms, applies); err != nil {
		t.Fatal(err)
	}
	stmts := inst.Statements()
	if got, want := procCalls(stmts), []string{"sp_configure N'Database Mail XPs'", "sysmail_configure_sp"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if !strings.Contains(stmts[0], "N'Database Mail XPs', 1") || !strings.Contains(stmts[1], "@parameter_value = N'3'") {
		t.Fatalf("statements = %q", stmts)
	}
}

// TestMailRemovingTheLastAccountReachesTheProfilesPage: an empty published
// list is still published. Stored as nil, it read as "Accounts not loaded",
// and the Profiles page went back to its own read — found live, removing the
// only account left the profile listing it as if it stayed.
func TestMailRemovingTheLastAccountReachesTheProfilesPage(t *testing.T) {
	m := &mailModel{}
	m.setAccounts([]string{"relay1"})
	m.publishAccounts(nil)
	if got := m.accountNames([]string{"relay1"}); len(got) != 0 {
		t.Fatalf("accountNames = %q after the last account was removed", got)
	}
	m.setProfiles([]string{"ops"})
	m.publishProfiles(nil)
	if got := m.profileNames([]string{"ops"}); len(got) != 0 {
		t.Fatalf("profileNames = %q after the last profile was removed", got)
	}
}
