package cmd

import (
	"errors"
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// xevent sessions' tests run on xeServer (xevent_server_test.go): real gosmo
// over canned catalog rows.

func TestXEventSessions(t *testing.T) {
	s := newXEServer(t)
	xeSessionsFixture(s)
	res, err := runXEvent(t, s, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{""}; !slices.Equal(s.listDB, want) {
		t.Errorf("session lists read in %q; want one, server-scoped", s.listDB)
	}
	// Only the running sessions' status is read.
	if want := []string{"gossms_QuickSessionTSQL", "system_health"}; !slices.Equal(s.statusReadsOf, want) {
		t.Errorf("status read for %v; want %v", s.statusReadsOf, want)
	}
	set := res.Sets[0]
	if set.Name != "sessions" || set.TotalRowCount != 3 {
		t.Fatalf("set %q with %d rows", set.Name, set.TotalRowCount)
	}
	want := [][]string{
		// A stopped session: its target's filename, no runtime state.
		{"AlwaysOn_health", "server", "0", "0", "NULL", "NULL", "event_file", "alwayson_ddl_executed", "AlwaysOn_health.xel"},
		// Running in the list but stopped before its status was read.
		{"gossms_QuickSessionTSQL", "server", "0", "0", "NULL", "NULL", "ring_buffer", "NULL", "NULL"},
		// Running: the started time and dropped events from its status, the
		// file it writes now.
		{"system_health", "server", "1", "1", "2026-10-14 16:33:15.110", "3", "event_file, ring_buffer",
			"error_reported, xml_deadlock_report", `C:\Log\system_health_0_1.xel`},
	}
	if got := setRows(set); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows:\n got %q\nwant %q", got, want)
	}
}

func TestXEventSessionsAzureSQLDatabase(t *testing.T) {
	s := newXEServer(t)
	xeSessionsFixture(s)
	s.Info.EngineEdition = int(gosmo.EngineAzureSQLDatabase)
	s.noVSS = true // VIEW SERVER STATE does not exist there: not asked
	res, err := runXEvent(t, s, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Sales"}; !slices.Equal(s.listDB, want) {
		t.Errorf("session lists read in %q; want the connection's database, Sales", s.listDB)
	}
	if n := s.reads("VIEW SERVER STATE"); n != 0 {
		t.Errorf("VIEW SERVER STATE asked %d times", n)
	}
	if got := column(res.Sets[0], "scope"); !slices.Equal(got, []string{"database", "database", "database"}) {
		t.Errorf("scope %q", got)
	}
}

func TestXEventSessionsWithoutViewServerState(t *testing.T) {
	s := newXEServer(t)
	xeSessionsFixture(s)
	s.noVSS = true
	_, err := runXEvent(t, s, "sessions")
	if errCode(err) != output.CodePermissionDenied || len(s.listDB) != 0 {
		t.Errorf("%v (%s), %d list reads; want %s and no read", err, errCode(err), len(s.listDB), output.CodePermissionDenied)
	}
}

func TestXEventSessionsStatusError(t *testing.T) {
	s := newXEServer(t)
	xeSessionsFixture(s)
	s.statusErr = errors.New("connection reset")
	if _, err := runXEvent(t, s, "sessions"); err == nil {
		t.Error("a failed status read was not reported")
	}
}
