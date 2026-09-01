package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The four SQL Server Agent create dialogs — New Operator, New Alert, New
// Schedule and New Job — driven through their real prefetch, preflight and
// apply closures against the scripted fake instance.
//
// What is worth pinning here is not the statement text (gosmo owns that) but
// the mapping between what the form shows and what the request carries. Every
// one of these pages builds a dropdown or a toggle grid over a list it fetched
// and reads it back by index, so the failure they actually produce is a write
// aimed one row over: an alert that e-mails the wrong operator, a job step
// created against the wrong database, a schedule attached to the wrong job.
// Each list below therefore holds more than one entry and every test acts on
// an entry that is not first.
//
// Two sentinels get their own tests for the same reason. "(None)" at the head
// of a category list and "(default)" at the head of a step's database list
// mean *omit the argument*, not "the first real value" — mapping either one
// through as a value creates an object the user did not ask for and the page
// reports success.
//
// Everything msdb is addressed three-part, so these writes are read back with
// Statements(), never StatementsIn("msdb") — see agent_fakedb_test.go.

// onlyStatementWith returns the single recorded statement containing want, and
// fails unless exactly one does. These dialogs write several statements per OK
// (sp_add_job is followed by sp_add_jobserver), so assertOneStatement is too
// strict and a bare Contains over the whole set would pass on a page that
// wrote the same procedure twice with different arguments.
func onlyStatementWith(t *testing.T, inst *fakeInstance, want string) string {
	t.Helper()
	var found []string
	for _, s := range inst.Statements() {
		if strings.Contains(s, want) {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one statement containing %q, got %d:\n%s",
			want, len(found), strings.Join(inst.Statements(), "\n"))
	}
	return found[0]
}

// assertStatementHas checks every fragment is present, reporting the statement
// once rather than per miss.
func assertStatementHas(t *testing.T, stmt string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(stmt, w) {
			t.Errorf("statement is missing %q:\n%s", w, stmt)
		}
	}
}

// chooseRadio is editRadio without the dirty assertion, for a group whose
// default option is one of the cases under test: "SQL Server error number" is
// selected already, and editRadio would fail on the very case that has to
// still work.
func chooseRadio(t *testing.T, f *propsheet.Form, label, option string) {
	t.Helper()
	row := radioRow(t, f, label)
	for i, o := range row.Options() {
		if o == option {
			row.Edit(i)
			return
		}
	}
	t.Fatalf("row %q offers %q, not %q", label, row.Options(), option)
}

func assertStatementLacks(t *testing.T, stmt string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(stmt, w) {
			t.Errorf("statement should not contain %q:\n%s", w, stmt)
		}
	}
}

// -- New Operator ------------------------------------------------------------

func newOperatorDialog(t *testing.T, responses ...fakeResponse) (*NewOperatorDialog, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	d := NewNewOperatorDialog(a)
	sc, inst := newFakeConn(t, responses...)
	d.show(sc)
	waitAndDrain(t, a)
	if d.forms[0] == nil {
		t.Fatal("the prefetch did not build the General page")
	}
	return d, inst
}

func newOperatorResponses() []fakeResponse {
	return append(agentOperatorResponses(), agentCategoryResponse())
}

func TestNewOperatorCreatesTheOperatorWithTheCategoryPicked(t *testing.T) {
	d, inst := newOperatorDialog(t, newOperatorResponses()...)
	form := d.forms[0]

	editText(t, form, "Name", "night-shift")
	editText(t, form, "E-mail address", "night@example.com")
	// Not the first category: a page that ignored the selection would still
	// pass on "Database Maintenance".
	editSelect(t, form, "Category", "Replication")

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmt := onlyStatementWith(t, inst, "sp_add_operator")
	assertStatementHas(t, stmt,
		"@name = N'night-shift'",
		"@enabled = 1",
		"@email_address = N'night@example.com'",
		"@category_name = N'Replication'",
	)
}

// The "(None)" head of the category list means no category at all. Mapping it
// through as a name would create the operator under a category called
// "(None)", which does not exist — and sp_add_operator fails on it, so the
// dialog reports an error for a field the user never touched.
func TestNewOperatorOmitsTheCategoryWhenNoneIsChosen(t *testing.T) {
	d, inst := newOperatorDialog(t, newOperatorResponses()...)
	form := d.forms[0]

	editText(t, form, "Name", "night-shift")

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertStatementLacks(t, onlyStatementWith(t, inst, "sp_add_operator"), "@category_name")
}

func TestNewOperatorPreflightRejectsANameThatExists(t *testing.T) {
	d, inst := newOperatorDialog(t, newOperatorResponses()...)

	// The check is case-insensitive, and the existing operator acted on is
	// neither first nor last in the fetched list.
	editText(t, d.forms[0], "Name", strings.ToUpper(agentOperatorName))

	if err := d.preflight(); err == nil {
		t.Fatal("preflight accepted a duplicate operator name")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("nothing should have been written:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- New Alert ---------------------------------------------------------------

const newAlertName = "Disk latency"

// newAlertResponses scripts the alert reads with a by-name answer for the
// alert this dialog creates, ahead of the fixture's own. Without it the
// Response page's read-back resolves to whichever alert the list answer holds
// first, and every notification would be written against that one while the
// test still passed.
func newAlertResponses() []fakeResponse {
	rs := []fakeResponse{
		{match: "WHERE a.name = @p1", arg: newAlertName, cols: 19, rows: [][]driver.Value{
			alertRow(20, newAlertName, 20, "", "MSSQLSERVER"),
		}},
	}
	rs = append(rs, agentAlertResponses()...)
	rs = append(rs, agentDatabaseListResponse(), agentCategoryResponse())
	return append(rs, agentOperatorResponses()...)
}

func newAlertDialog(t *testing.T) (*NewAlertDialog, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	d := NewNewAlertDialog(a)
	sc, inst := newFakeConn(t, newAlertResponses()...)
	d.show(sc)
	waitAndDrain(t, a)
	if d.forms[0] == nil || d.forms[1] == nil {
		t.Fatal("the prefetch did not build both pages")
	}
	return d, inst
}

// The two trigger fields are both on the page at once and only the one the
// radio names is sent. A page that sent both would create an alert that fires
// on a severity the user never asked for — sp_add_alert accepts the pair.
func TestNewAlertSendsOnlyTheTriggerFieldTheRadioNames(t *testing.T) {
	for _, tc := range []struct {
		trigger    string
		want, lack string
	}{
		{"SQL Server error number", "@message_id = 50001", "@severity = 20"},
		{"Severity level", "@severity = 20", "@message_id = 50001"},
	} {
		t.Run(tc.trigger, func(t *testing.T) {
			d, inst := newAlertDialog(t)
			form := d.forms[0]

			editText(t, form, "Name", newAlertName)
			chooseRadio(t, form, "Trigger", tc.trigger)
			// Both fields are filled in, so the statement can only be right
			// for the reason the radio says.
			editText(t, form, "Error number", "50001")
			editText(t, form, "Severity", "20")

			if err := d.preflight(); err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if err := d.applyFns[0](context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmt := onlyStatementWith(t, inst, "sp_add_alert")
			assertStatementHas(t, stmt, tc.want)
			assertStatementLacks(t, stmt, tc.lack)
		})
	}
}

func TestNewAlertScopesToTheDatabasePicked(t *testing.T) {
	d, inst := newAlertDialog(t)
	form := d.forms[0]

	editText(t, form, "Name", newAlertName)
	chooseRadio(t, form, "Trigger", "Severity level")
	editText(t, form, "Severity", "20")
	// Not the first database in the list, and not the connection's own.
	editSelect(t, form, "Database", "salesdb")

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertStatementHas(t, onlyStatementWith(t, inst, "sp_add_alert"), "@database_name = N'salesdb'")
}

// "<All databases>" is the head of the same list and means server-wide. Sent
// as a name it would scope the alert to a database of that name, which cannot
// exist — the alert would then never fire, silently.
func TestNewAlertOmitsTheDatabaseWhenAllDatabasesIsChosen(t *testing.T) {
	d, inst := newAlertDialog(t)
	form := d.forms[0]

	editText(t, form, "Name", newAlertName)
	chooseRadio(t, form, "Trigger", "Severity level")
	editText(t, form, "Severity", "20")

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertStatementLacks(t, onlyStatementWith(t, inst, "sp_add_alert"), "@database_name")
}

// The Response page's grid is indexed against the fetched operator list, so
// the operator ticked must be the operator notified. Ticking by name is what
// makes that catchable — by index the test would agree with a page whose grid
// and list had drifted apart.
func TestNewAlertNotifiesOnlyTheOperatorTicked(t *testing.T) {
	d, inst := newAlertDialog(t)

	editText(t, d.forms[0], "Name", newAlertName)
	chooseRadio(t, d.forms[0], "Trigger", "Severity level")
	editText(t, d.forms[0], "Severity", "20")
	toggleByName(t, toggleGrid(t, d.forms[1]), agentOperatorName, 0)

	if err := d.applyFns[1](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := onlyStatementWith(t, inst, "sp_add_notification")
	assertStatementHas(t, stmt,
		"@alert_name = N'"+newAlertName+"'",
		"@operator_name = N'"+agentOperatorName+"'",
	)
}

func TestNewAlertPreflightRejectsATriggerWithNoValue(t *testing.T) {
	d, inst := newAlertDialog(t)
	editText(t, d.forms[0], "Name", newAlertName)

	// Trigger defaults to "SQL Server error number" with the field at 0.
	if err := d.preflight(); err == nil {
		t.Fatal("preflight accepted an error-number alert with no error number")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("nothing should have been written:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- New Schedule ------------------------------------------------------------

const newScheduleName = "Nightly 02:00"

func newScheduleResponses() []fakeResponse {
	rs := []fakeResponse{
		// As in newAlertResponses: the Jobs page resolves each job it attaches
		// to by name, and behind the list answer every one of them would come
		// back as whichever job sorts first.
		{match: "WHERE  j.name = @p1", arg: agentJobName, cols: 17, rows: [][]driver.Value{
			jobRow(agentJobName, "Database Maintenance", "sa", true, 0, 0, ""),
		}},
	}
	rs = append(rs, agentScheduleResponses()...)
	return append(rs, agentJobResponses(jobRow(agentJobName, "Database Maintenance", "sa", true, 0, 0, ""))...)
}

func newScheduleDialog(t *testing.T) (*NewScheduleDialog, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	d := NewNewScheduleDialog(a)
	sc, inst := newFakeConn(t, newScheduleResponses()...)
	d.show(sc)
	waitAndDrain(t, a)
	if d.forms[0] == nil || d.forms[1] == nil {
		t.Fatal("the prefetch did not build both pages")
	}
	return d, inst
}

// A weekly schedule's @freq_interval is a weekday bitmask, and the label a day
// is ticked by and the bit it sets come from two parallel tables
// (weekdayNames/weekdayBits). Ticking by name and asserting the bit is what a
// round trip through the same pair cannot see — see CLAUDE.md.
func TestNewScheduleWeeklyTicksTheDayItIsLabelled(t *testing.T) {
	d, inst := newScheduleDialog(t)
	form := d.forms[0]

	editText(t, form, "Name", newScheduleName)
	editSelect(t, form, "Occurs", "Weekly")
	grid := toggleGrid(t, form)
	// The form starts Mon-Fri; clear them and leave Wednesday alone, so the
	// mask is one named day and not a default that happens to contain it.
	for _, day := range []string{"Monday", "Tuesday", "Thursday", "Friday"} {
		toggleByName(t, grid, day, 0)
	}

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := onlyStatementWith(t, inst, "sp_add_schedule")
	assertStatementHas(t, stmt,
		"@schedule_name = N'"+newScheduleName+"'",
		"@freq_type = 8",     // FreqWeekly
		"@freq_interval = 8", // Wednesday
	)
}

// The Jobs page attaches by index into the fetched job list. The job ticked is
// deliberately not the first: a page that ignored the selection would attach
// "Backup log" and still report success.
func TestNewScheduleAttachesOnlyTheJobTicked(t *testing.T) {
	d, inst := newScheduleDialog(t)

	editText(t, d.forms[0], "Name", newScheduleName)
	toggleByName(t, toggleGrid(t, d.forms[1]), agentJobName, 0)

	if err := d.applyFns[1](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := onlyStatementWith(t, inst, "sp_attach_schedule")
	assertStatementHas(t, stmt,
		"@job_name = N'"+agentJobName+"'",
		"@schedule_name = N'"+newScheduleName+"'",
	)
}

func TestNewScheduleAttachesNothingWhenNoJobIsTicked(t *testing.T) {
	d, inst := newScheduleDialog(t)
	editText(t, d.forms[0], "Name", newScheduleName)

	if err := d.applyFns[1](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("no job was ticked, but the page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- New Job -----------------------------------------------------------------

const newJobName = "Rebuild statistics"

func newJobResponses() []fakeResponse {
	newJob := jobRow(newJobName, "Replication", "otheruser", true, 0, 0, "")
	rs := []fakeResponse{
		{match: "WHERE  j.name = @p1", arg: newJobName, cols: 17, rows: [][]driver.Value{newJob}},
	}
	rs = append(rs, agentJobResponses(jobRow(agentJobName, "Database Maintenance", "sa", true, 0, 0, ""))...)
	rs = append(rs, loginListResponse(), agentCategoryResponse(), agentDatabaseListResponse())
	rs = append(rs, agentScheduleResponses()...)
	return append(rs, agentOperatorResponses()...)
}

func newJobDialog(t *testing.T) (*NewJobDialog, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	d := NewNewJobDialog(a)
	sc, inst := newFakeConn(t, newJobResponses()...)
	d.show(sc)
	waitAndDrain(t, a)
	for i, f := range d.forms {
		if f == nil {
			t.Fatalf("the prefetch did not build page %q", d.pages[i])
		}
	}
	return d, inst
}

// jobPage addresses a page by name rather than by index, so the test does not
// follow a reordered page list.
func (d *NewJobDialog) page(t *testing.T, name string) (*propsheet.Form, propApply) {
	t.Helper()
	for i, p := range d.pages {
		if p == name {
			return d.forms[i], d.applyFns[i]
		}
	}
	t.Fatalf("this dialog has pages %v, not %q", d.pages, name)
	return nil, nil
}

func TestNewJobGeneralCreatesTheJobWithTheOwnerAndCategoryPicked(t *testing.T) {
	d, inst := newJobDialog(t)
	form, apply := d.page(t, "General")

	editText(t, form, "Name", newJobName)
	editText(t, form, "Description", "Nightly UPDATE STATISTICS")
	// Neither is the first item in its list.
	editSelect(t, form, "Owner", "otheruser")
	editSelect(t, form, "Category", "Replication")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := onlyStatementWith(t, inst, "sp_add_job ")
	assertStatementHas(t, stmt,
		"@job_name = N'"+newJobName+"'",
		"@category_name = N'Replication'",
		"@owner_login_name = N'otheruser'",
		"@enabled = 1",
	)
	// The job is useless until it is enlisted on the local server, and that
	// second statement is easy to lose in a refactor of the first.
	assertStatementHas(t, onlyStatementWith(t, inst, "sp_add_jobserver"), "@job_name = N'"+newJobName+"'")
}

// Each step carries its own database, and "(default)" means omit the argument
// so the server picks. A step created against the wrong database runs the
// right T-SQL in the wrong place, which is the one failure here that does
// damage rather than erroring.
func TestNewJobStepsCarryTheDatabaseEachStepWasGiven(t *testing.T) {
	d, inst := newJobDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Name", newJobName)

	form, apply := d.page(t, "Steps")
	editText(t, form, "Step name", "load staging")
	chooseSelect(t, form, "Database", "salesdb")
	editEditor(t, form, "Command", "EXEC dbo.load_staging")
	clickButton(t, form, "New")

	editText(t, form, "Step name", "notify")
	chooseSelect(t, form, "Database", defaultDatabaseItem)
	editEditor(t, form, "Command", "EXEC dbo.notify")
	clickButton(t, form, "New")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	load := onlyStatementWith(t, inst, "N'load staging'")
	assertStatementHas(t, load, "sp_add_jobstep", "@database_name = N'salesdb'")
	notify := onlyStatementWith(t, inst, "N'notify'")
	assertStatementHas(t, notify, "sp_add_jobstep")
	assertStatementLacks(t, notify, "@database_name")
}

func TestNewJobAttachesOnlyTheScheduleTicked(t *testing.T) {
	d, inst := newJobDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Name", newJobName)

	form, apply := d.page(t, "Schedules")
	toggleByName(t, toggleGrid(t, form), agentScheduleName, 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := onlyStatementWith(t, inst, "sp_attach_schedule")
	assertStatementHas(t, stmt,
		"@job_name = N'"+newJobName+"'",
		"@schedule_name = N'"+agentScheduleName+"'",
	)
}

func TestNewJobNotificationsEmailTheOperatorPicked(t *testing.T) {
	d, inst := newJobDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Name", newJobName)

	form, apply := d.page(t, "Notifications")
	editCheck(t, form, "E-mail", true)
	editSelect(t, form, "Operator", agentOperatorName)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := onlyStatementWith(t, inst, "@notify_level_email")
	assertStatementHas(t, stmt,
		"@job_name = N'"+newJobName+"'",
		"@notify_email_operator_name = N'"+agentOperatorName+"'",
	)
}

// With neither checkbox ticked the Notifications page does not reach the
// server at all — not even the by-name read its two writes would need. The
// early return is what makes that true, and it matters because this apply runs
// on every OK: without it a new job costs a round trip to look itself up and
// discover there is nothing to say.
//
// Asserting only "nothing was written" does not pin it. Both writes are
// individually gated as well, so removing the early return still writes
// nothing — the read is the only visible difference.
func TestNewJobNotificationsDoNotReachTheServerWhenNeitherBoxIsTicked(t *testing.T) {
	d, inst := newJobDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Name", newJobName)

	form, apply := d.page(t, "Notifications")
	// The operator is picked but E-mail is left clear: choosing one is not
	// asking for one.
	editSelect(t, form, "Operator", agentOperatorName)

	before := inst.QueryCount()
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := inst.QueryCount() - before; got != 0 {
		t.Errorf("neither box was ticked, but the page ran %d queries", got)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("neither box was ticked, but the page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// An enabled job with no steps is refused by the Agent, so the dialog refuses
// it first — the preflight is the only thing standing between the user and a
// job created, enlisted, and then rejected halfway through the pipeline.
func TestNewJobPreflightRejectsAnEnabledJobWithNoSteps(t *testing.T) {
	d, inst := newJobDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Name", newJobName)

	if err := d.preflight(); err == nil {
		t.Fatal("preflight accepted an enabled job with no steps")
	}

	// Clearing Enabled is the documented way out, and it must actually work.
	editCheck(t, general, "Enabled", false)
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight rejected a disabled job with no steps: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("preflight should not write:\n%s", strings.Join(stmts, "\n"))
	}
}
