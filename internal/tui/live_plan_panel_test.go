package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
)

// A session id is read past Block's tree drawing and sp_WhoIsActive's padding.
func TestSessionIDIn(t *testing.T) {
	for _, tc := range []struct {
		cell string
		want int
		ok   bool
	}{
		{"57", 57, true},
		{"  57", 57, true},
		{"0053", 53, true},
		{"|------  0057", 57, true},
		{"|         |------  112", 112, true},
		{"", 0, false},
		{"NULL", 0, false},
		{"0", 0, false},
	} {
		if got, ok := sessionIDIn(tc.cell); got != tc.want || ok != tc.ok {
			t.Errorf("sessionIDIn(%q) = %d, %v; want %d, %v", tc.cell, got, ok, tc.want, tc.ok)
		}
	}
}

// Both procedure tabs offer the selected row's session; a row naming none
// gets the item disabled, not left off.
func TestProcTabOffersShowLiveExecutionPlan(t *testing.T) {
	am := hostedProcMonitor(t, amTabBlock)
	am.conn = &db.ServerConn{Server: new(gosmo.Server)}
	pt := am.procTab()
	pt.loc = activity.ProcTempDB
	pt.applyResult(procResult())
	pt.grid.SetSelectedRow(1)
	if spid, ok := pt.selectedSession(); !ok || spid != 57 {
		t.Fatalf("Block row 1: session %d, %v; want 57", spid, ok)
	}
	items := pt.menuItems()
	if len(items) != 1 || items[0].Label != "Show Live Execution Plan" || items[0].Action == nil {
		t.Fatalf("menu = %+v, want an enabled Show Live Execution Plan", items)
	}

	am.setTab(amTabSessions)
	pt = am.procTab()
	pt.loc = activity.ProcTempDB
	pt.applyResult(&query.Result{Sets: []query.ResultSet{{
		Columns: []string{"dd hh:mm:ss.mss", "session_id", "sql_text"},
		Rows:    [][]string{{"00 00:00:05.120", "   61", "select 1"}},
	}}})
	if spid, ok := pt.selectedSession(); !ok || spid != 61 {
		t.Fatalf("Sessions row 0: session %d, %v; want 61", spid, ok)
	}

	pt.applyResult(&query.Result{Sets: []query.ResultSet{{
		Columns: []string{"x"}, Rows: [][]string{{"1"}},
	}}})
	items = pt.menuItems()
	if len(items) != 1 || items[0].Enabled == nil || items[0].Enabled() || items[0].Note == "" {
		t.Fatalf("menu without a session column = %+v, want the item disabled with a note", items)
	}
}

// Before SQL Server 2019 an unprofiled query is invisible, and the note says
// what profiles one; from 2019 and on Azure lightweight profiling does.
func TestLiveSessionIdleNoteByVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		info    *gosmo.ServerInfo
		advises bool
	}{
		{"2016", &gosmo.ServerInfo{VersionMajor: 13}, true},
		{"2017", &gosmo.ServerInfo{VersionMajor: 14}, true},
		{"2019", &gosmo.ServerInfo{VersionMajor: 15}, false},
		{"Managed Instance reports 12", &gosmo.ServerInfo{VersionMajor: 12, EngineEdition: 8}, false},
		{"no info", nil, false},
	} {
		note := liveSessionIdleNote(tc.info, 57)
		if got := strings.Contains(note, "7412"); got != tc.advises {
			t.Errorf("%s: note mentions trace flag 7412 = %v, want %v: %q", tc.name, got, tc.advises, note)
		}
		if !strings.Contains(note, "Session 57") {
			t.Errorf("%s: note does not name the session: %q", tc.name, note)
		}
	}
}

// The view follows the session: waiting, running, and — the query over — its
// last reading kept with the title saying so. Nothing lands after Close.
func TestLivePlanPanelFollowsTheSession(t *testing.T) {
	a := newTestApp()
	lp := newLivePlanPanel(a, &db.ServerConn{}, 57)
	_, token := lp.run.Begin(t.Context())
	plan, err := showplan.Parse([]byte(testPlanXML))
	if err != nil {
		t.Fatal(err)
	}

	lp.apply(token, liveUpdate{idle: true})
	if lp.state != "waiting" || lp.planView.Plan() != nil || !lp.planView.Live() {
		t.Fatalf("idle before any plan: state %q, plan %v", lp.state, lp.planView.Plan())
	}
	lp.apply(token, liveUpdate{plan: plan, counters: map[int]showplan.LiveCounters{0: {Rows: 5}}})
	if lp.state != "running" || lp.planView.Plan() != plan || !lp.planView.Live() {
		t.Fatalf("after a reading: state %q", lp.state)
	}
	lp.apply(token, liveUpdate{idle: true})
	if !strings.Contains(lp.state, "last reading") || lp.planView.Plan() != plan {
		t.Fatalf("idle after a plan: state %q, plan kept = %v", lp.state, lp.planView.Plan() == plan)
	}

	lp.Close()
	lp.apply(token, liveUpdate{note: liveSessionRefusedNote})
	if lp.state == "not available" {
		t.Error("an update after Close was applied")
	}
}
