package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// xeFake is the event sessions xevent sessions reads: sessions, and each
// running one's status by name (missing: stopped since the list was read).
// engine is ServerInfo's EngineEdition; each read records what it was asked.
type xeFake struct {
	sessions  []*gosmo.EventSession
	status    map[string]*gosmo.EventSessionStatus
	statusErr error
	engine    gosmo.EngineEdition

	listReads   int
	gotDB       *gosmo.Database
	statusReads []string
}

func (c *monitorCatalog) ServerInfo() *gosmo.ServerInfo {
	return &gosmo.ServerInfo{EngineEdition: int(c.xe.engine)}
}

func (c *monitorCatalog) EventSessions(_ context.Context, d *gosmo.Database) ([]*gosmo.EventSession, error) {
	c.xe.listReads++
	c.xe.gotDB = d
	return c.xe.sessions, nil
}

func (c *monitorCatalog) EventSessionStatus(_ context.Context, es *gosmo.EventSession) (*gosmo.EventSessionStatus, error) {
	c.xe.statusReads = append(c.xe.statusReads, es.Name)
	if c.xe.statusErr != nil {
		return nil, c.xe.statusErr
	}
	st, ok := c.xe.status[es.Name]
	if !ok {
		return nil, fmt.Errorf("event session %q is not running: %w", es.Name, gosmo.ErrNotFound)
	}
	return st, nil
}

func xeCatalog() *monitorCatalog {
	started := time.Date(2026, 10, 14, 16, 33, 15, 110e6, time.UTC)
	return &monitorCatalog{xe: &xeFake{
		sessions: []*gosmo.EventSession{
			{Name: "AlwaysOn_health", Targets: []gosmo.SessionTarget{{Package: "package0", Name: gosmo.XETargetEventFile,
				Fields: []gosmo.SessionField{{Name: "filename", Value: "AlwaysOn_health.xel"}}}},
				Events: []gosmo.SessionEvent{{Package: "sqlserver", Name: "alwayson_ddl_executed"}}},
			{Name: "gossms_QuickSessionTSQL", IsRunning: true, Targets: []gosmo.SessionTarget{{Package: "package0", Name: gosmo.XETargetRingBuffer}}},
			{Name: "system_health", IsRunning: true, StartupState: true, DroppedEvents: 9,
				Targets: []gosmo.SessionTarget{
					{Package: "package0", Name: gosmo.XETargetEventFile, Fields: []gosmo.SessionField{{Name: "filename", Value: "system_health.xel"}}},
					{Package: "package0", Name: gosmo.XETargetRingBuffer},
				},
				Events: []gosmo.SessionEvent{{Package: "sqlserver", Name: "error_reported"}, {Package: "sqlserver", Name: "xml_deadlock_report"}}},
		},
		status: map[string]*gosmo.EventSessionStatus{
			"system_health": {CreateTime: started, DroppedEvents: 3, Targets: []gosmo.EventTargetStatus{
				{Package: "package0", Name: gosmo.XETargetEventFile, CurrentFile: `C:\Log\system_health_0_1.xel`},
				{Package: "package0", Name: gosmo.XETargetRingBuffer, EventCount: 10},
			}},
		},
	}}
}

func TestXEventSessions(t *testing.T) {
	cat := xeCatalog()
	res, err := runGroup(t, cat, "xevent", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if cat.xe.listReads != 1 || cat.xe.gotDB != nil {
		t.Errorf("%d list reads, database %v; want one server-scoped read", cat.xe.listReads, cat.xe.gotDB)
	}
	// Only the running sessions' status is read.
	if want := []string{"gossms_QuickSessionTSQL", "system_health"}; !slices.Equal(cat.xe.statusReads, want) {
		t.Errorf("status read for %v; want %v", cat.xe.statusReads, want)
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
	cat := xeCatalog()
	cat.xe.engine = gosmo.EngineAzureSQLDatabase
	cat.noVSS = true // VIEW SERVER STATE does not exist there: not asked
	res, err := runGroup(t, cat, "xevent", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if cat.xe.gotDB == nil || cat.xe.gotDB.Name != "Sales" {
		t.Errorf("read database %v; want the connection's, Sales", cat.xe.gotDB)
	}
	if got := column(res.Sets[0], "scope"); !slices.Equal(got, []string{"database", "database", "database"}) {
		t.Errorf("scope %q", got)
	}
}

func TestXEventSessionsWithoutViewServerState(t *testing.T) {
	cat := xeCatalog()
	cat.noVSS = true
	_, err := runGroup(t, cat, "xevent", "sessions")
	if errCode(err) != output.CodePermissionDenied || cat.xe.listReads != 0 {
		t.Errorf("%v (%s), %d list reads; want %s and no read", err, errCode(err), cat.xe.listReads, output.CodePermissionDenied)
	}
}

func TestXEventSessionsStatusError(t *testing.T) {
	cat := xeCatalog()
	cat.xe.statusErr = errors.New("connection reset")
	if _, err := runGroup(t, cat, "xevent", "sessions"); err == nil {
		t.Error("a failed status read was not reported")
	}
}
