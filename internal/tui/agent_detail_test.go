package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"
)

// agentCountsResponse answers gosmo's AgentCounts aggregate: jobs, schedules,
// all alerts, event alerts, operators.
func agentCountsResponse(jobs, schedules, alerts, eventAlerts, operators int64) fakeResponse {
	return fakeResponse{match: "FROM msdb.dbo.sysoperators)", cols: 5,
		rows: [][]driver.Value{{jobs, schedules, alerts, eventAlerts, operators}}}
}

// The Agent root's census comes from one aggregate read, and each number lands
// on its own row. Every count is distinct, so a row wired to the wrong field —
// Event alerts showing all alerts, say — shows the wrong number.
func TestAgentServerDetailCountsFromOneRead(t *testing.T) {
	sc, fake := newFakeConn(t,
		fakeResponse{match: "sys.dm_server_services", cols: 2,
			rows: [][]driver.Value{{"Running", nil}}},
		agentCountsResponse(7, 5, 4, 3, 2),
	)
	_, rows, err := agentServerDetail(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Status", "Running"},
		{"Last startup", ""},
		{"Jobs", "7"},
		{"Schedules", "5"},
		{"Event alerts", "3"},
		{"Operators", "2"},
	}
	if !slices.EqualFunc(rows, want, slices.Equal) {
		t.Errorf("rows = %q, want %q", rows, want)
	}
	// The listings it replaced: none of them may run.
	for _, list := range []string{"xp_sqlagent_enum_jobs", "FROM   msdb.dbo.sysjobs j", "FROM   msdb.dbo.sysalerts a"} {
		if got := fake.Reads(list); len(got) > 0 {
			t.Errorf("the detail listed %q to count it: %q", list, got)
		}
	}
}

// A failed count read dashes the counts and still shows the Agent's status.
func TestAgentServerDetailFailedCountsDash(t *testing.T) {
	failed := agentCountsResponse(0, 0, 0, 0, 0)
	failed.err = errors.New("SELECT permission denied on sysjobs")
	sc, _ := newFakeConn(t,
		fakeResponse{match: "sys.dm_server_services", cols: 2,
			rows: [][]driver.Value{{"Stopped", nil}}},
		failed,
	)
	_, rows, err := agentServerDetail(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Status", "Stopped"},
		{"Last startup", ""},
		{"Jobs", "—"},
		{"Schedules", "—"},
		{"Event alerts", "—"},
		{"Operators", "—"},
	}
	if !slices.EqualFunc(rows, want, slices.Equal) {
		t.Errorf("rows = %q, want %q", rows, want)
	}
}
