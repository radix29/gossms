package tui

import (
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The six dropdowns whose value is a *name the server supplied* against a list
// read separately — a credential, a category, a login, a database, a job. Each
// list can be missing the value: the object was dropped between the two reads,
// or the caller cannot see it (a login needs VIEW ANY DEFINITION to be listed,
// but a user mapped to it is visible either way). indexOf answers 0 for a miss,
// and 0 on all six of these pages is a sentinel — "(None)", "<All databases>" —
// so the page states as fact that nothing is configured, on exactly the object
// an admin opened Properties to investigate. selectPreserving widens the list
// with the value instead.
//
// Each case is asserted twice: the row shows the vanished value, and an
// untouched page still writes nothing — a stand-in that leaked into a write
// would send "(None)" to the server as a name.

func assertShowsVanishedValue(t *testing.T, form *propsheet.Form, label, want string) {
	t.Helper()
	row := selectRow(t, form, label)
	if got := row.Value(); got != want {
		t.Errorf("%q shows %q, want the value the server reported, %q", label, got, want)
	}
	if !slices.Contains(row.Items(), want) {
		t.Errorf("%q does not offer %q at all: %v", label, want, row.Items())
	}
	if row.Dirty() {
		t.Errorf("%q is dirty on load — apply would write the stand-in back", label)
	}
}

func assertUntouchedApplyWritesNothing(t *testing.T, inst *fakeInstance, apply propApply) {
	t.Helper()
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// A login mapped to a credential the credentials list doesn't carry. Reading
// "(None)" here says the login maps to nothing, which is the opposite of the
// truth and the reason someone opened the page.
func TestLoginGeneralShowsACredentialMissingFromTheList(t *testing.T) {
	responses := loginGeneralResponses("appuser", "SQL_LOGIN")
	responses[1].rows[0][10] = "cred_gone" // LOGINPROPERTY's credential_name
	sc, inst := newFakeConn(t, responses...)
	form, apply := loadPage(t, pageLoginGeneral(sc, ptr("appuser")), inst)

	assertShowsVanishedValue(t, form, "Map to credential", "cred_gone")
	assertUntouchedApplyWritesNothing(t, inst, apply)
}

// A user mapped to a login this connection cannot list. "(None)" reads as an
// orphaned user, which is what an admin fixes by remapping — against a login
// that is in fact already mapped.
func TestUserGeneralShowsALoginMissingFromTheList(t *testing.T) {
	responses := []fakeResponse{
		dbByNameResp(principalDatabase, 5),
		userByNameResponse(principalUser, "dbo", "vanished_login"),
		userByNameResponse("dbo", "dbo", ""),
	}
	responses = append(responses, databaseSchemaResponses()...)
	responses = append(responses, databaseRoleResponses()...)
	responses = append(responses, principalPermissionResponses()...)
	responses = append(responses, loginListResponse())

	sc, inst := newFakeConn(t, responses...)
	name := principalUser
	form, apply := loadPage(t, pageUserGeneral(sc, principalDatabase, &name), inst)

	assertShowsVanishedValue(t, form, "Login name", "vanished_login")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// An operator in a category that has since been deleted — sysoperators keeps
// the name, syscategories no longer lists it.
func TestOperatorGeneralShowsACategoryMissingFromTheList(t *testing.T) {
	rows := [][]driver.Value{
		operatorRow(1, "dba-oncall", "dba@example.com", "Database Maintenance", true),
		operatorRow(2, agentOperatorName, "reports@example.com", "Ghost category", true),
		operatorRow(3, "weekend-cover", "cover@example.com", "", false),
	}
	responses := []fakeResponse{
		{match: "WHERE o.name = @p1", cols: 13, rows: [][]driver.Value{rows[1]}},
		{match: "FROM   msdb.dbo.sysoperators o", cols: 13, rows: rows},
		agentCategoryResponse(),
	}
	sc, inst := newFakeConn(t, responses...)
	name := agentOperatorName
	form, apply := loadPage(t, pageOperatorGeneral(sc, &name), inst)

	assertShowsVanishedValue(t, form, "Category", "Ghost category")
	assertUntouchedApplyWritesNothing(t, inst, apply)
}

// vanishedAlertResponses scripts the alert with a database, a category and a
// response job that none of the three lists carries — the alert row keeps all
// three as plain names, so all three dropdowns are exposed at once.
func vanishedAlertResponses() []fakeResponse {
	row := alertRow(12, agentAlertName, 20, "Ghost job", "MSSQLSERVER")
	row[6] = "ghostdb"         // database_name
	row[11] = "Ghost category" // category
	rows := [][]driver.Value{
		alertRow(11, "Sev 17 errors", 17, "Backup log", "MSSQLSERVER"),
		row,
		alertRow(13, "WMI deadlock", 0, "", "WMI"),
	}
	return []fakeResponse{
		{match: "WHERE a.name = @p1", cols: 19, rows: [][]driver.Value{row}},
		{match: "FROM   msdb.dbo.sysalerts a", cols: 19, rows: rows},
		{match: "FROM   msdb.dbo.sysnotifications n", cols: 2, rows: [][]driver.Value{
			{"dba-oncall", int64(1)},
		}},
	}
}

// An alert scoped to a database that has been dropped, in a deleted category.
// "<All databases>" is the worst of the six stand-ins to show wrongly: it says
// the alert fires server-wide when it fires nowhere.
func TestAlertGeneralShowsADatabaseAndCategoryMissingFromTheLists(t *testing.T) {
	responses := append(vanishedAlertResponses(), alertGeneralResponses()...)
	sc, inst := newFakeConn(t, responses...)
	name := agentAlertName
	form, apply := loadPage(t, pageAlertGeneral(sc, &name), inst)

	assertShowsVanishedValue(t, form, "Database", "ghostdb")
	assertShowsVanishedValue(t, form, "Category", "Ghost category")
	assertUntouchedApplyWritesNothing(t, inst, apply)
}

// An alert whose response job is not in the job list.
func TestAlertResponseShowsAJobMissingFromTheList(t *testing.T) {
	responses := append(vanishedAlertResponses(), alertResponseResponses()...)
	sc, inst := newFakeConn(t, responses...)
	name := agentAlertName
	form, apply := loadPage(t, pageAlertResponse(sc, &name), inst)

	assertShowsVanishedValue(t, form, "Response job", "Ghost job")
	assertUntouchedApplyWritesNothing(t, inst, apply)
}

// The other half of the change: the stand-in must still write as "", and a
// real pick must still write its own name. Each of the six is read back with
// preservedValue now rather than by comparing the selected index against 0,
// and an index comparison is what a widened list breaks.
func TestPreservedDropdownsStillWriteTheValuePicked(t *testing.T) {
	t.Run("alert scope back to all databases", func(t *testing.T) {
		responses := append(vanishedAlertResponses(), alertGeneralResponses()...)
		sc, inst := newFakeConn(t, responses...)
		name := agentAlertName
		form, apply := loadPage(t, pageAlertGeneral(sc, &name), inst)

		editSelect(t, form, "Database", allDatabasesItem)

		if err := apply(t.Context()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertOneStatement(t, inst, "@database_name = N''")
	})

	t.Run("alert category cleared", func(t *testing.T) {
		responses := append(vanishedAlertResponses(), alertGeneralResponses()...)
		sc, inst := newFakeConn(t, responses...)
		name := agentAlertName
		form, apply := loadPage(t, pageAlertGeneral(sc, &name), inst)

		editSelect(t, form, "Category", noneItem)

		// gosmo maps the empty category to Agent's own default name; what
		// matters here is that the stand-in is not sent as one.
		if err := apply(t.Context()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertOneStatement(t, inst, "@category_name = N'[Uncategorized]'")
	})

	t.Run("alert response job picked", func(t *testing.T) {
		responses := append(vanishedAlertResponses(), alertResponseResponses()...)
		sc, inst := newFakeConn(t, responses...)
		name := agentAlertName
		form, apply := loadPage(t, pageAlertResponse(sc, &name), inst)

		editSelect(t, form, "Response job", agentJobName)

		if err := apply(t.Context()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertOneStatement(t, inst, "@job_name = N'Nightly reindex'")
	})

	t.Run("operator category cleared", func(t *testing.T) {
		responses := append(agentOperatorResponses(), agentCategoryResponse())
		sc, inst := newFakeConn(t, responses...)
		name := agentOperatorName
		form, apply := loadPage(t, pageOperatorGeneral(sc, &name), inst)

		editSelect(t, form, "Category", noneItem)

		if err := apply(t.Context()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertOneStatement(t, inst, "@category_name = N'[Uncategorized]'")
	})
}
