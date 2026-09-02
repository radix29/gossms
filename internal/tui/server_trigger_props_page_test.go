package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Server Trigger Properties, driven through fakedb_test.go.
//
// The trigger under test is the second of three and is the disabled one, so a
// page that ignored its selection and read whichever row sorts first cannot
// pass. The by-name read is scoped with arg: and placed before the list read,
// because gosmo's ServerTriggerByNameContext query also contains
// "FROM   sys.server_triggers" and responses match by substring in order.

const serverTriggerUnderTest = "ddl_audit"

func serverTriggerPropResponses(definition driver.Value) []fakeResponse {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return []fakeResponse{
		{
			match: "FROM   sys.server_triggers",
			arg:   serverTriggerUnderTest,
			cols:  6,
			rows: [][]driver.Value{
				{serverTriggerUnderTest, true, when, when, "CREATE_DATABASE,ALTER_DATABASE", definition},
			},
		},
		{
			match: "FROM   sys.server_triggers",
			cols:  6,
			rows: [][]driver.Value{
				{"aaa_first", false, when, when, "LOGON", "CREATE TRIGGER [aaa_first] ON ALL SERVER ..."},
				{serverTriggerUnderTest, true, when, when, "CREATE_DATABASE,ALTER_DATABASE", definition},
				{"zzz_last", false, when, when, "DROP_DATABASE", "CREATE TRIGGER [zzz_last] ON ALL SERVER ..."},
			},
		},
	}
}

// The page must show the trigger it was opened on, not the first row in
// sys.server_triggers — and it must show its state, which is the one thing
// that decides whether the trigger is doing anything at all.
func TestServerTriggerGeneralLoadsTheSelectedTrigger(t *testing.T) {
	sc, inst := newFakeConn(t, serverTriggerPropResponses("CREATE TRIGGER [ddl_audit] ...")...)
	form, apply := loadPage(t, pageServerTriggerGeneral(sc, serverTriggerUnderTest), inst)

	if got := staticValue(t, form, "Name"); got != serverTriggerUnderTest {
		t.Errorf("Name is %q, want the selected trigger's %q", got, serverTriggerUnderTest)
	}
	if got := staticValue(t, form, "Status"); got != "Disabled" {
		t.Errorf("Status is %q, want Disabled — the scripted row has is_disabled set", got)
	}
	if got := staticValue(t, form, "Fires on"); !strings.Contains(got, "ALTER_DATABASE") {
		t.Errorf("Fires on is %q — the page dropped the trigger's events", got)
	}
	if got := staticValue(t, form, "Created"); !strings.Contains(got, "2026-03-04") {
		t.Errorf("Created is %q", got)
	}
	// ENABLE, DISABLE and DROP ... ON ALL SERVER are Object Explorer commands,
	// not an Apply; there is no ALTER a form could build.
	if apply != nil {
		t.Error("the General page has an apply, but a server trigger has no ALTER a form can build")
	}
}

// The Definition page is what the whole dialog is for on a trigger — the body
// has to reach the editor, unedited.
func TestServerTriggerDefinitionShowsTheBody(t *testing.T) {
	const body = "CREATE TRIGGER [ddl_audit] ON ALL SERVER\nFOR CREATE_DATABASE\nAS PRINT 'audited';"
	sc, inst := newFakeConn(t, serverTriggerPropResponses(body)...)
	form, apply := loadPage(t, pageServerTriggerDefinition(sc, serverTriggerUnderTest), inst)

	var ed *propsheet.EditorRow
	for _, r := range form.Rows() {
		if er, ok := r.(*propsheet.EditorRow); ok {
			ed = er
		}
	}
	if ed == nil {
		t.Fatal("the Definition page has no editor row")
	}
	if ed.Value() != body {
		t.Errorf("the editor holds:\n%s\nwant:\n%s", ed.Value(), body)
	}
	if !ed.Editor().ReadOnly() {
		t.Error("the definition editor is writable — an edit here goes nowhere, since the page has no apply")
	}
	if apply != nil {
		t.Error("the Definition page has an apply")
	}
}

// A CLR trigger has no row in sys.server_sql_modules and an encrypted one
// reports NULL, so the page must say so rather than showing an empty editor,
// which reads as a trigger with an empty body.
func TestServerTriggerDefinitionReportsAnUnreadableBody(t *testing.T) {
	sc, inst := newFakeConn(t, serverTriggerPropResponses(nil)...)
	form, _ := loadPage(t, pageServerTriggerDefinition(sc, serverTriggerUnderTest), inst)

	for _, r := range form.Rows() {
		if _, ok := r.(*propsheet.EditorRow); ok {
			t.Error("the page drew an editor for a definition it could not read")
		}
	}
}

// Both pages are read-only by nature, not by omission, and
// prop_page_requires_test.go's pagesThatOnlyRead only permits that for a page
// with no apply at all.
func TestServerTriggerPagesDoNotWrite(t *testing.T) {
	sc, inst := newFakeConn(t, serverTriggerPropResponses("CREATE TRIGGER [ddl_audit] ...")...)

	for _, page := range serverTriggerPropPages(sc, serverTriggerUnderTest) {
		_, apply := loadPage(t, page, inst)
		if apply != nil {
			t.Errorf("page %q has an apply", page.title)
		}
	}
	for _, q := range inst.Statements() {
		if strings.Contains(q, "TRIGGER") && !strings.Contains(q, "SELECT") {
			t.Errorf("loading Server Trigger Properties wrote: %q", q)
		}
	}
}
