package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// SQL Server Agent Properties > Alert System. Profiles come from mailConfig:
// "alerts" first, "ops" second.

// agentMailRead is AgentMailSettings' registry read — named down to the value,
// since connect-time loadInfo runs xp_instance_regread too.
const agentMailRead = `SQLServerAgent', N'UseDatabaseMail'`

const agentMailWrite = "EXEC msdb.dbo.sp_set_sqlagent_properties "

func agentMailAnswer(use any, profile any) fakeResponse {
	return fakeResponse{match: agentMailRead, cols: 2, rows: [][]driver.Value{{use, profile}}}
}

func loadAgentAlertSystemPage(t *testing.T, mail fakeResponse, more ...fakeResponse) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, append([]fakeResponse{mail, mailConfig()[1], mailConfig()[2]}, more...)...)
	form, apply := loadPage(t, pageAgentAlertSystem(sc), inst)
	return inst, apply, form
}

func formNotes(f *propsheet.Form) string {
	var b strings.Builder
	for _, r := range f.Rows() {
		if n, ok := r.(*propsheet.NoteRow); ok {
			b.WriteString(n.Text() + "\n")
		}
	}
	return b.String()
}

// Never-written values: off, no profile. Enabling and picking the second
// profile is one call carrying both, by the picked name.
func TestAgentAlertSystemEnablesThePickedProfileInOneCall(t *testing.T) {
	inst, apply, form := loadAgentAlertSystemPage(t, agentMailAnswer(nil, nil))
	if got := selectRow(t, form, "Mail profile").Value(); got != noneItem {
		t.Fatalf("profile shows %q for a never-written value, want %q", got, noneItem)
	}

	editCheck(t, form, "Enable mail profile", true)
	editSelect(t, form, "Mail profile", "ops")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, agentMailWrite+"@use_databasemail = 1, @databasemail_profile = N'ops'")
}

// Unchecking sends only the switch: the profile stays configured for the
// next time mail is turned on, as in SSMS.
func TestAgentAlertSystemDisablingKeepsTheProfile(t *testing.T) {
	inst, apply, form := loadAgentAlertSystemPage(t, agentMailAnswer(int64(1), "ops"))
	if got := selectRow(t, form, "Mail profile").Value(); got != "ops" {
		t.Fatalf("profile shows %q, want the configured ops", got)
	}

	editCheck(t, form, "Enable mail profile", false)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, agentMailWrite+"@use_databasemail = 0")
	if s := inst.Statements()[0]; strings.Contains(s, "@databasemail_profile") {
		t.Errorf("disabling also wrote the profile: %s", s)
	}
}

// Enabled with no profile would leave Agent sending through nothing; the page
// refuses rather than writing it.
func TestAgentAlertSystemRefusesEnablingWithNoProfile(t *testing.T) {
	inst, apply, form := loadAgentAlertSystemPage(t, agentMailAnswer(int64(0), nil))

	editCheck(t, form, "Enable mail profile", true)

	if err := apply(t.Context()); err == nil || !strings.Contains(err.Error(), "choose a mail profile") {
		t.Errorf("apply = %v, want the no-profile refusal", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a refused apply wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

func TestAgentAlertSystemUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _ := loadAgentAlertSystemPage(t, agentMailAnswer(int64(1), "ops"))
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// A profile deleted since it was configured is still what Agent tries: the
// page shows it, says so, and does not rewrite it on an unrelated apply.
func TestAgentAlertSystemShowsADeletedProfile(t *testing.T) {
	inst, apply, form := loadAgentAlertSystemPage(t, agentMailAnswer(int64(1), "gone"))
	if got := selectRow(t, form, "Mail profile").Value(); got != "gone" {
		t.Errorf("profile shows %q, want the configured gone", got)
	}
	if !strings.Contains(formNotes(form), "no longer exists") {
		t.Errorf("no note that the profile is gone; notes:\n%s", formNotes(form))
	}
	editCheck(t, form, "Enable mail profile", false)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, agentMailWrite+"@use_databasemail = 0")
}

// A login refused msdb's profile list (Msg 229) still sees the configured
// profile — and no claim that it is gone, which the page cannot know.
func TestAgentAlertSystemWithProfilesRefused(t *testing.T) {
	sc, inst := newFakeConn(t, agentMailAnswer(int64(1), "ops"), mailRefused(mailProfileRead, "sysmail_profile"))
	form, _ := loadPage(t, pageAgentAlertSystem(sc), inst)
	if got := selectRow(t, form, "Mail profile").Value(); got != "ops" {
		t.Errorf("profile shows %q, want the configured ops", got)
	}
	if strings.Contains(formNotes(form), "no longer exists") {
		t.Error("claims the profile is gone though the list was never read")
	}
}

// On Linux the settings live in mssql.conf: the page says where, offers
// nothing to apply, and reads nothing from the registry.
func TestAgentAlertSystemOnLinuxPointsAtMssqlConf(t *testing.T) {
	sc, inst := newFakeConnOnLinux(t)
	form, apply := loadPage(t, pageAgentAlertSystem(sc), inst)
	if apply != nil {
		t.Error("the Linux page has an apply")
	}
	if !strings.Contains(formNotes(form), "mssql-conf set sqlagent.databasemailprofile") {
		t.Errorf("no mssql-conf note; notes:\n%s", formNotes(form))
	}
	// No registry answer is scripted: loadPage fails on an unanswered read.
}
