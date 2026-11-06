package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// What a login below SQLAgentOperatorRole sees of SQL Agent's alerts: msdb
// refuses it sysalerts and sysnotifications (gosmo.Server.Alerts), but not
// sp_get_job_alerts or sysjobs_view.

// readerResponses probes as a SQLAgentReaderRole member — in SQLAgentUserRole
// through the nesting, not in SQLAgentOperatorRole or msdb db_owner, CONTROL
// SERVER denied. extra goes first: the agent reads' batch carries
// IS_SRVROLEMEMBER too, which the probe's answer would otherwise serve.
func readerResponses(operatorRole bool, extra ...fakeResponse) []fakeResponse {
	in := []string{"SQLAgentUserRole", "SQLAgentReaderRole"}
	notIn := []string{"db_owner"}
	if operatorRole {
		in = append(in, "SQLAgentOperatorRole")
	} else {
		notIn = append(notIn, "SQLAgentOperatorRole")
	}
	return append(extra, capabilityResponsesWithRoles(true, nil, []string{"CONTROL SERVER"}, in, notIn)...)
}

func probedReaderConn(t *testing.T, operatorRole bool, extra ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	sc, inst := newGConn(t, readerResponses(operatorRole, extra...)...)
	sc.ProbeCapabilities(sc.Server.Context())
	sc.DatabaseCapabilities(context.Background(), "msdb")
	return sc, inst
}

func agentRootLabels(t *testing.T, sc *db.ServerConn) []string {
	t.Helper()
	nodes, err := loadAgentRootChildren(loaderCtx{ctx: context.Background(), sc: sc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range nodes {
		out = append(out, n.label)
	}
	return out
}

// SSMS leaves the folder out rather than letting it answer Access denied; an
// unprobed msdb keeps it (the gate fails open).
func TestAgentAlertsFolderNeedsSQLAgentOperatorRole(t *testing.T) {
	reader, _ := probedReaderConn(t, false)
	if got := agentRootLabels(t, reader); slices.Contains(got, "Alerts") || !slices.Contains(got, "Operators") {
		t.Errorf("reader's Agent folders = %v; want Operators and no Alerts", got)
	}
	operator, _ := probedReaderConn(t, true)
	if got := agentRootLabels(t, operator); !slices.Contains(got, "Alerts") {
		t.Errorf("SQLAgentOperatorRole member's Agent folders = %v; want Alerts", got)
	}
	unprobed, _ := newGConn(t)
	if got := agentRootLabels(t, unprobed); !slices.Contains(got, "Alerts") {
		t.Errorf("unprobed connection's Agent folders = %v; want Alerts", got)
	}
}

// jobAlertsResponses answers Job.Alerts: the id lookup, then
// sp_get_job_alerts (id, name, enabled, type), unordered.
func jobAlertsResponses() []fakeResponse {
	return []fakeResponse{
		{match: "FROM msdb.dbo.sysjobs_view WHERE name = @p1", cols: 1, rows: [][]driver.Value{
			{"0B1D2C3E-0000-0000-0000-000000000001"}}},
		{match: "sp_get_job_alerts", cols: 4, rows: [][]driver.Value{
			{int64(14), "Zeta perf", true, int64(2)},
			{int64(12), agentAlertName, false, int64(1)},
		}},
	}
}

func assertJobAlertsSummary(t *testing.T, form *propsheet.Form, apply propApply) {
	t.Helper()
	if apply != nil {
		t.Error("the summary page offers an Apply it has nothing to write with")
	}
	grid := plainGrid(t, form)
	want := [][]string{
		{agentAlertName, "False", "SQL Server event or WMI"},
		{"Zeta perf", "True", "Performance condition"},
	}
	for i, w := range want {
		if got := grid.Row(i); !slices.Equal(got, w) {
			t.Errorf("row %d = %v; want %v", i, got, w)
		}
	}
	if grid.Row(len(want)) != nil {
		t.Error("the summary lists more alerts than the job has")
	}
}

// A reader cannot list alerts, so the page shows, read-only, the ones that
// run this job — and never asks for the list: nothing answers it, so a read
// would fail the load.
func TestJobAlertsPageForAReaderListsThisJobsAlerts(t *testing.T) {
	sc, inst := probedReaderConn(t, false, jobAlertsResponses()...)
	name := agentJobName
	form, apply := loadPageG(t, pageJobAlerts(sc, &name), inst)
	assertJobAlertsSummary(t, form, apply)
}

// The gate fails open, so an unprobed login reaches the list read; its 229
// lands on the same summary, not an error page.
func TestJobAlertsPageFallsBackOnARefusal(t *testing.T) {
	refused := fakeResponse{match: "FROM   msdb.dbo.sysalerts a", err: mssql.Error{Number: 229, Class: 14,
		Message: "The SELECT permission was denied on the object 'sysalerts'"}}
	sc, inst := newGConn(t, append(jobAlertsResponses(), refused)...)
	name := agentJobName
	form, apply := loadPageG(t, pageJobAlerts(sc, &name), inst)
	assertJobAlertsSummary(t, form, apply)
}

// Below SQLAgentOperatorRole the alert half is a note; the job half, read
// through sysjobs_view, still lists.
func TestOperatorNotificationsForAReader(t *testing.T) {
	jobs := fakeResponse{match: "FROM   msdb.dbo.sysjobs_view", cols: 2, rows: [][]driver.Value{
		{agentJobName, int64(2)}}}
	notif := fakeResponse{match: "FROM   msdb.dbo.sysnotifications n", err: mssql.Error{Number: 229, Class: 14,
		Message: "The SELECT permission was denied on the object 'sysnotifications'"}}
	sc, inst := probedReaderConn(t, false, append(agentOperatorResponses(), jobs, notif)...)
	name := agentOperatorName
	form, _ := loadPageG(t, pageOperatorNotifications(sc, &name), inst)

	if !strings.Contains(formNotes(form), operatorAlertsWithheldNote) {
		t.Errorf("notes = %v; want the withheld-alerts note", formNotes(form))
	}
	grid := plainGrid(t, form)
	if got := grid.Row(0); len(got) == 0 || got[0] != agentJobName {
		t.Errorf("job grid row 0 = %v; want %s", got, agentJobName)
	}
}
