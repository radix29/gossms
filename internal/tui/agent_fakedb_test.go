package tui

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Shared fixtures for the SQL Server Agent Properties pages — Job, Alert,
// Operator and Schedule.
//
// Everything msdb is addressed three-part (EXEC msdb.dbo.sp_update_job), never
// through a USE, so every write here lands on a connection pinned to no
// database and is read back with Statements(), not StatementsIn("msdb"). A
// test that reached for the latter would assert on an empty slice.
//
// Every list is scripted with more than one entry and every test acts on an
// entry that is neither first nor last, because the failure these pages
// actually produce is a write addressed to the neighbouring object: an alert
// pointed at the wrong job, a notification added for the wrong operator, a
// step deleted at the wrong step_id.

const (
	agentJobName      = "Nightly reindex"
	agentAlertName    = "Sev 20 errors"
	agentOperatorName = "reporting"
	agentScheduleName = "Hourly"
	// agentScheduleID is deliberately not 1: sp_update_schedule addresses a
	// schedule by id, so a page that lost the one it loaded and fell back to a
	// zero value would still produce a statement that looks right.
	agentScheduleID = 7
)

var agentEpoch = time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)

// jobRow is one row of the 17-column job SELECT that both JobsContext and
// JobByNameContext scan.
func jobRow(name, category, owner string, enabled bool, deleteLevel, notifyLevel int64, operator string) []driver.Value {
	return []driver.Value{
		"job-" + name, name, "Rebuilds every index",
		enabled, category, owner,
		agentEpoch, agentEpoch, int64(1),
		deleteLevel, notifyLevel, operator,
		agentEpoch, int64(1), int64(1500), agentEpoch, int64(1),
	}
}

// agentJobResponses answers the job reads. The by-name read is scripted ahead
// of the list read on purpose: the two queries differ only by a WHERE clause,
// so behind the list answer every by-name lookup would resolve to whichever
// job sorts first — the same trap DatabaseByName has (see fakeResponse.arg).
func agentJobResponses(job []driver.Value) []fakeResponse {
	return []fakeResponse{
		{match: "WHERE  j.name = @p1", cols: 17, rows: [][]driver.Value{job}},
		{match: "FROM   msdb.dbo.sysjobs j", cols: 17, rows: [][]driver.Value{
			jobRow("Backup log", "Database Maintenance", "sa", true, 0, 0, ""),
			job,
		}},
	}
}

// agentCategoryResponse answers CategoriesContext for every class — the class
// is an int parameter, and fakeResponse.arg only discriminates strings, so one
// answer serves all three. No page loads two classes at once.
func agentCategoryResponse() fakeResponse {
	return fakeResponse{match: "FROM   msdb.dbo.syscategories", cols: 2, rows: [][]driver.Value{
		{int64(1), "Database Maintenance"},
		{int64(2), "Replication"},
		{int64(3), "[Uncategorized (Local)]"},
	}}
}

func agentOperatorResponses() []fakeResponse {
	rows := [][]driver.Value{
		operatorRow(1, "dba-oncall", "dba@example.com", "Database Maintenance", true),
		operatorRow(2, agentOperatorName, "reports@example.com", "Replication", true),
		operatorRow(3, "weekend-cover", "cover@example.com", "", false),
	}
	return []fakeResponse{
		{match: "WHERE o.name = @p1", cols: 13, rows: [][]driver.Value{rows[1]}},
		{match: "FROM   msdb.dbo.sysoperators o", cols: 13, rows: rows},
	}
}

func operatorRow(id int64, name, email, category string, enabled bool) []driver.Value {
	return []driver.Value{
		id, name, enabled, email, "", "", category,
		int64(0), int64(0), int64(0), int64(0), int64(0), int64(0),
	}
}

// alertRow is one row of the 19-column alert SELECT.
func alertRow(id int64, name string, severity int64, jobName, source string) []driver.Value {
	return []driver.Value{
		id, name, true, source,
		int64(0), severity,
		"appdb", int64(60),
		"", int64(0),
		"", "Replication",
		jobName, "",
		int64(4),
		int64(0), int64(0), int64(0), int64(0),
	}
}

// agentAlertResponses scripts three alerts, one of which is a WMI alert that
// EventAlerts must filter out — the Job Alerts page indexes its edit slice
// against the filtered list, so a page reading back against the unfiltered one
// writes to the alert one row over.
func agentAlertResponses() []fakeResponse {
	rows := [][]driver.Value{
		alertRow(11, "Sev 17 errors", 17, "Backup log", "MSSQLSERVER"),
		alertRow(12, agentAlertName, 20, agentJobName, "MSSQLSERVER"),
		alertRow(13, "WMI deadlock", 0, "", "WMI"),
	}
	return []fakeResponse{
		{match: "WHERE a.name = @p1", cols: 19, rows: [][]driver.Value{rows[1]}},
		{match: "FROM   msdb.dbo.sysalerts a", cols: 19, rows: rows},
		{match: "FROM   msdb.dbo.sysnotifications n", cols: 2, rows: [][]driver.Value{
			{"dba-oncall", int64(1)},
		}},
	}
}

// scheduleRow is one row of the 16-column schedule SELECT.
func scheduleRow(id int64, name string, freqType, freqInterval, recurrence int64, owner string) []driver.Value {
	return []driver.Value{
		id, name, true, freqType, freqInterval,
		int64(1), int64(1), int64(0), recurrence,
		int64(20260101), int64(99991231),
		int64(10000), int64(235959),
		agentEpoch, agentEpoch, owner,
	}
}

// agentScheduleResponses scripts three shared schedules, of which the job is
// attached to the first only.
func agentScheduleResponses() []fakeResponse {
	all := [][]driver.Value{
		scheduleRow(3, "Daily 01:00", 4, 1, 0, "appuser"),
		scheduleRow(agentScheduleID, agentScheduleName, 4, 1, 0, "appuser"),
		scheduleRow(9, "Weekly Sunday", 8, 1, 1, "appuser"),
	}
	return []fakeResponse{
		{match: "WHERE sch.name = @p1", cols: 16, rows: [][]driver.Value{all[1]}},
		{match: "sysjobschedules js ON js.schedule_id", cols: 16, rows: all[:1]},
		{match: "FROM   msdb.dbo.sysschedules sch", cols: 16, rows: all},
	}
}

// agentDatabaseListResponse is the database dropdown behind a job step and an
// alert's scope.
func agentDatabaseListResponse() fakeResponse {
	return fakeResponse{match: "FROM sys.databases", cols: 8, rows: [][]driver.Value{
		{"master", int64(1), "ONLINE", "SIMPLE", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, agentEpoch},
		{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, agentEpoch},
		{"salesdb", int64(6), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, agentEpoch},
	}}
}

// loadAgentPage is loadPage plus the fake instance, for the pages whose whole
// fixture set is one of the groups above.
func loadAgentPage(t *testing.T, responses []fakeResponse, page func(sc *db.ServerConn) propPage) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, responses...)
	form, apply := loadPage(t, page(sc), inst)
	return inst, apply, form
}
