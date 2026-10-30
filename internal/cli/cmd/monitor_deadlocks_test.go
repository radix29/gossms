package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// dlFake is system_health as monitor deadlocks reads it: session (nil: none
// exists), and the events its event_file or ring_buffer serves. Each read
// records what it was asked.
type dlFake struct {
	session *gosmo.EventSession
	events  []gosmo.XEvent
	dbs     []*gosmo.Database

	gotPattern string
	gotEvents  []string
	fileReads  int
	ringReads  int
	dbReads    int
}

func (c *monitorCatalog) EventSessionByName(_ context.Context, name string) (*gosmo.EventSession, error) {
	if c.dl.session == nil || name != systemHealth {
		return nil, fmt.Errorf("event session %q: %w", name, gosmo.ErrNotFound)
	}
	return c.dl.session, nil
}

func (c *monitorCatalog) ReadFileEvents(_ context.Context, pattern string, cursor gosmo.EventFileCursor, max int, events []string) ([]gosmo.XEvent, gosmo.EventFileCursor, error) {
	c.dl.fileReads++
	c.dl.gotPattern, c.dl.gotEvents = pattern, events
	if !cursor.IsZero() || max != 0 {
		return nil, cursor, errors.New("monitor deadlocks reads every file from the start")
	}
	var out []gosmo.XEvent
	for _, e := range c.dl.events {
		if slices.Contains(events, e.Name) {
			out = append(out, e)
		}
	}
	return out, cursor, nil
}

func (c *monitorCatalog) ReadRingBuffer(context.Context, *gosmo.EventSession) (*gosmo.RingBufferData, error) {
	c.dl.ringReads++
	return &gosmo.RingBufferData{Events: c.dl.events}, nil
}

func (c *monitorCatalog) Databases(context.Context) ([]*gosmo.Database, error) {
	c.dl.dbReads++
	return c.dl.dbs, nil
}

// systemHealthSession is a running system_health with the given targets.
func systemHealthSession(targets ...string) *gosmo.EventSession {
	es := &gosmo.EventSession{Name: systemHealth, IsRunning: true}
	for _, t := range targets {
		tg := gosmo.SessionTarget{Package: "package0", Name: t}
		if t == gosmo.XETargetEventFile {
			tg.Fields = []gosmo.SessionField{{Name: "filename", Value: "system_health.xel"}}
		}
		es.Targets = append(es.Targets, tg)
	}
	return es
}

// deadlockGraph is a two-process graph: spid a the victim, ecid 0 both, the
// rows of dbo.t in Sales. dbName empty writes no currentdbname, as 2017 RTM.
func deadlockGraph(a, b int, dbName string) string {
	proc := func(spid int, mode, stmt string) string {
		name := ""
		if dbName != "" {
			name = fmt.Sprintf(` currentdbname="%s"`, dbName)
		}
		return fmt.Sprintf(`<process id="p%d" spid="%d" ecid="0" loginname="app" hostname="web1" clientapp="api" `+
			`currentdb="5"%s isolationlevel="read committed (2)" lockMode="%s" waitresource="KEY: 5:72057594043367424 (8194443284a0)" `+
			`transactionname="user_transaction"><executionStack><frame procname="adhoc" line="1">%s</frame></executionStack>`+
			`<inputbuf>batch %d</inputbuf></process>`, spid, spid, name, mode, stmt, spid)
	}
	return fmt.Sprintf(`<deadlock><victim-list><victimProcess id="p%d"/></victim-list><process-list>%s%s</process-list>`+
		`<resource-list><keylock dbid="5" objectname="Sales.dbo.t" indexname="PK_t" mode="X">`+
		`<owner-list><owner id="p%d" mode="X"/></owner-list><waiter-list><waiter id="p%d" mode="U" requestType="wait"/></waiter-list>`+
		`</keylock></resource-list></deadlock>`,
		a, proc(a, "U", "UPDATE t SET v = 1 WHERE id = 2"), proc(b, "X", ""), b, a)
}

func deadlockEvent(at time.Time, graph string) gosmo.XEvent {
	return gosmo.XEvent{Name: xmlDeadlockReport, Package: "sqlserver", Timestamp: at,
		Fields: []gosmo.XEValue{{Name: "xml_report", Value: graph, IsXML: true}}}
}

// The server's now, 10:00 UTC, in a zone that is not UTC: the default window
// is the 24 h to it, converted.
var dlNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.FixedZone("CEST", 2*3600))

func dlCatalog(events ...gosmo.XEvent) *monitorCatalog {
	return &monitorCatalog{qs: &qsFake{now: dlNow},
		dl: &dlFake{session: systemHealthSession(gosmo.XETargetRingBuffer, gosmo.XETargetEventFile), events: events,
			dbs: []*gosmo.Database{{Name: "master", ID: 1}, {Name: "Sales", ID: 5}}}}
}

// The event_file is read whole, the server keeping only the deadlock
// reports; the window is the 24 h to the server's now in UTC, newest first;
// a process with no currentdbname is named from the catalog.
func TestMonitorDeadlocks(t *testing.T) {
	at := time.Date(2026, 10, 30, 9, 15, 0, 250_000_000, time.UTC)
	cat := dlCatalog(
		deadlockEvent(at.Add(-30*time.Hour), deadlockGraph(70, 71, "Sales")), // before the window
		deadlockEvent(at.Add(-time.Hour), deadlockGraph(51, 52, "Sales")),
		gosmo.XEvent{Name: "sp_server_diagnostics_component_result", Timestamp: at},
		deadlockEvent(at, deadlockGraph(61, 62, "")),
		deadlockEvent(dlNow, deadlockGraph(80, 81, "Sales")), // --until is exclusive
	)
	res, err := runMonitor(t, cat, "deadlocks")
	if err != nil {
		t.Fatal(err)
	}
	if cat.dl.gotPattern != "system_health*.xel" || !slices.Equal(cat.dl.gotEvents, []string{xmlDeadlockReport}) || cat.dl.ringReads != 0 {
		t.Errorf("read %q %q, ring reads %d", cat.dl.gotPattern, cat.dl.gotEvents, cat.dl.ringReads)
	}
	if field(res, "source") != "event_file" || field(res, "from") != "2026-10-29 10:00:00.000" ||
		field(res, "to") != "2026-10-30 10:00:00.000" || field(res, "deadlocksInWindow") != "2" {
		t.Errorf("fields = %+v", res.Fields)
	}
	want := []string{
		"1 2026-10-30 09:15:00.250 61 2 Sales Sales.dbo.t",
		"2 2026-10-30 08:15:00.250 51 2 Sales Sales.dbo.t",
	}
	if res.Sets[0].Name != "deadlocks" || !slices.Equal(cells(res.Sets[0]), want) {
		t.Errorf("deadlocks =\n%q\nwant\n%q", cells(res.Sets[0]), want)
	}
	want = []string{
		"1 61 0 1 app web1 api Sales read committed (2) U KEY: 5:72057594043367424 (8194443284a0) user_transaction UPDATE t SET v = 1 WHERE id = 2",
		"1 62 0 0 app web1 api Sales read committed (2) X KEY: 5:72057594043367424 (8194443284a0) user_transaction batch 62",
		"2 51 0 1 app web1 api Sales read committed (2) U KEY: 5:72057594043367424 (8194443284a0) user_transaction UPDATE t SET v = 1 WHERE id = 2",
		"2 52 0 0 app web1 api Sales read committed (2) X KEY: 5:72057594043367424 (8194443284a0) user_transaction batch 52",
	}
	if len(res.Sets) != 2 || res.Sets[1].Name != "processes" || !slices.Equal(cells(res.Sets[1]), want) {
		t.Fatalf("processes =\n%q\nwant\n%q", cells(res.Sets[1]), want)
	}
	for _, s := range res.Sets {
		if len(s.Columns) != len(s.Rows[0]) {
			t.Errorf("%s: %d columns, %d cells", s.Name, len(s.Columns), len(s.Rows[0]))
		}
	}
	if cat.dl.dbReads != 1 {
		t.Errorf("%d catalog reads for the missing database name, want 1", cat.dl.dbReads)
	}
}

// Every report naming its database reads no catalog.
func TestMonitorDeadlocksNamedDatabases(t *testing.T) {
	cat := dlCatalog(deadlockEvent(dlNow.Add(-time.Hour), deadlockGraph(51, 52, "Sales")))
	if _, err := runMonitor(t, cat, "deadlocks"); err != nil {
		t.Fatal(err)
	}
	if cat.dl.dbReads != 0 {
		t.Errorf("%d catalog reads, want none", cat.dl.dbReads)
	}
}

// --since and --until: no zone is UTC, a zone is converted; --top keeps the
// newest and deadlocksInWindow still counts the rest.
func TestMonitorDeadlocksWindow(t *testing.T) {
	base := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	var events []gosmo.XEvent
	for h := range 6 {
		events = append(events, deadlockEvent(base.Add(time.Duration(h)*time.Hour), deadlockGraph(51+h, 60, "Sales")))
	}
	for _, tc := range []struct {
		argv     []string
		from, to string
		victims  []string
	}{
		{[]string{"--since", "2026-10-27T01:00", "--until", "2026-10-27T04:00:00"},
			"2026-10-27 01:00:00.000", "2026-10-27 04:00:00.000", []string{"54", "53", "52"}},
		{[]string{"--since", "2026-10-27T03:00:00+02:00", "--until", "2026-10-27T03:00Z"},
			"2026-10-27 01:00:00.000", "2026-10-27 03:00:00.000", []string{"53", "52"}},
		{[]string{"--since", "2026-10-27", "--until", "2026-10-28", "--top", "2"},
			"2026-10-27 00:00:00.000", "2026-10-28 00:00:00.000", []string{"56", "55"}},
	} {
		res, err := runMonitor(t, dlCatalog(events...), append([]string{"deadlocks"}, tc.argv...)...)
		if err != nil {
			t.Fatalf("%q: %v", tc.argv, err)
		}
		var victims []string
		for _, row := range res.Sets[0].Rows {
			victims = append(victims, *row[2])
		}
		if field(res, "from") != tc.from || field(res, "to") != tc.to || !slices.Equal(victims, tc.victims) {
			t.Errorf("%q: %s to %s, victims %q; want %s to %s, %q", tc.argv, field(res, "from"), field(res, "to"), victims, tc.from, tc.to, tc.victims)
		}
	}
	res, _ := runMonitor(t, dlCatalog(events...), "deadlocks", "--since", "2026-10-27", "--top", "2")
	if field(res, "deadlocksInWindow") != "6" || len(res.Sets[0].Rows) != 2 {
		t.Errorf("--top 2: %s in window, %d rows", field(res, "deadlocksInWindow"), len(res.Sets[0].Rows))
	}
}

// --deadlock-format xml gives each graph whole as a Verbatim field, in the
// deadlocks set's order, and no processes set.
func TestMonitorDeadlocksXML(t *testing.T) {
	older, newer := deadlockGraph(51, 52, "Sales"), deadlockGraph(61, 62, "Sales")
	cat := dlCatalog(deadlockEvent(dlNow.Add(-2*time.Hour), older), deadlockEvent(dlNow.Add(-time.Hour), newer))
	res, err := runMonitor(t, cat, "deadlocks", "--deadlock-format", "xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sets) != 1 || res.Sets[0].Name != "deadlocks" {
		t.Fatalf("sets = %+v", res.Sets)
	}
	for i, want := range []string{newer, older} {
		key := fmt.Sprint("deadlock", i+1)
		f := res.Fields[slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == key })]
		if f.Value != want || !f.Verbatim {
			t.Errorf("%s = %+v", key, f)
		}
	}
}

// With no event_file target the ring_buffer is read, and its other events
// left out client-side.
func TestMonitorDeadlocksRingBuffer(t *testing.T) {
	cat := dlCatalog(deadlockEvent(dlNow.Add(-time.Hour), deadlockGraph(51, 52, "Sales")),
		gosmo.XEvent{Name: "error_reported", Timestamp: dlNow.Add(-time.Hour)})
	cat.dl.session = systemHealthSession(gosmo.XETargetRingBuffer)
	res, err := runMonitor(t, cat, "deadlocks")
	if err != nil {
		t.Fatal(err)
	}
	if cat.dl.fileReads != 0 || cat.dl.ringReads != 1 || field(res, "source") != "ring_buffer" || len(res.Sets[0].Rows) != 1 {
		t.Errorf("file reads %d, ring reads %d, fields %+v, rows %q", cat.dl.fileReads, cat.dl.ringReads, res.Fields, cells(res.Sets[0]))
	}
}

// A report that does not decode is a warning; the others still answer.
func TestMonitorDeadlocksBadReport(t *testing.T) {
	cat := dlCatalog(deadlockEvent(dlNow.Add(-time.Hour), "<deadlock><process-list>"),
		deadlockEvent(dlNow.Add(-2*time.Hour), deadlockGraph(51, 52, "Sales")))
	res, err := runMonitor(t, cat, "deadlocks")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sets[0].Rows) != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "09:00:00.000") {
		t.Errorf("rows %q, warnings %q", cells(res.Sets[0]), res.Warnings)
	}
}

// system_health missing, stopped or with neither target is exit 8, and
// nothing is read.
func TestMonitorDeadlocksNoSystemHealth(t *testing.T) {
	stopped := systemHealthSession(gosmo.XETargetEventFile)
	stopped.IsRunning = false
	for name, es := range map[string]*gosmo.EventSession{
		"missing": nil, "stopped": stopped, "no target": systemHealthSession("histogram"),
	} {
		cat := dlCatalog(deadlockEvent(dlNow.Add(-time.Hour), deadlockGraph(51, 52, "Sales")))
		cat.dl.session = es
		_, err := runMonitor(t, cat, "deadlocks")
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound || oe.Hint == "" {
			t.Errorf("%s: err = %v, want NOT_FOUND with a hint", name, err)
		}
		if cat.dl.fileReads+cat.dl.ringReads != 0 {
			t.Errorf("%s: read the events", name)
		}
	}
}

func TestMonitorDeadlocksNeedsViewServerState(t *testing.T) {
	cat := dlCatalog()
	cat.noVSS = true
	_, err := runMonitor(t, cat, "deadlocks")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied {
		t.Errorf("err = %v, want PERMISSION_DENIED", err)
	}
	if cat.dl.fileReads+cat.dl.ringReads != 0 {
		t.Error("read the events without the permission")
	}
}

func TestMonitorDeadlocksUsage(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"--top", "0"}, {"--since", "yesterday"}, {"--until", "2026-10-30 10:00"},
		{"--since", "2026-10-28", "--until", "2026-10-28"}, {"--since", "2026-10-28T02:00+02:00", "--until", "2026-10-28T00:00Z"},
		{"--deadlock-file", file}, {"--deadlock-file", ""},
	} {
		cat := dlCatalog()
		_, err := runMonitor(t, cat, append([]string{"deadlocks"}, argv...)...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if cat.dl.fileReads != 0 {
			t.Errorf("%q: read before refusing", argv)
		}
	}
	// A --since after the server's now, with no --until, is refused once
	// the clock is read.
	_, err := runMonitor(t, dlCatalog(), "deadlocks", "--since", "2026-11-01")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
		t.Errorf("--since in the future: err = %v, want a usage error", err)
	}
}

// --deadlock-file writes each graph as deadlock_<UTC time>.xdl, creating the
// directory; two in one millisecond get _2. A second run refuses without
// --force and writes nothing, and replaces them with it.
func TestMonitorDeadlocksFiles(t *testing.T) {
	at := time.Date(2026, 10, 30, 9, 15, 0, 250_000_000, time.UTC)
	a, b, c := deadlockGraph(51, 52, "Sales"), deadlockGraph(61, 62, "Sales"), deadlockGraph(71, 72, "Sales")
	cat := dlCatalog(deadlockEvent(at.Add(-time.Hour), a), deadlockEvent(at, b), deadlockEvent(at, c))
	dir := filepath.Join(t.TempDir(), "dl")
	res, err := runMonitor(t, cat, "deadlocks", "--deadlock-file", dir)
	if err != nil || len(res.Errors) > 0 {
		t.Fatal(err, res.Errors)
	}
	if field(res, "deadlockFile") != dir {
		t.Errorf("fields = %+v", res.Fields)
	}
	for name, want := range map[string]string{
		"deadlock_20261030_081500_250.xdl": a, "deadlock_20261030_091500_250.xdl": c, "deadlock_20261030_091500_250_2.xdl": b,
	} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Errorf("%s: %v, %q", name, err, got)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Errorf("%d files, want 3", len(entries))
	}

	newer := deadlockEvent(at.Add(time.Minute), a)
	cat = dlCatalog(newer, deadlockEvent(at.Add(-time.Hour), b))
	res, _ = runMonitor(t, cat, "deadlocks", "--deadlock-file", dir)
	if len(res.Errors) != 1 || res.Errors[0].Code != output.CodeUsageError {
		t.Fatalf("without --force: errors %+v", res.Errors)
	}
	if _, err := os.Stat(filepath.Join(dir, "deadlock_20261030_091600_250.xdl")); err == nil {
		t.Error("wrote a file before refusing")
	}
	cat = dlCatalog(newer, deadlockEvent(at.Add(-time.Hour), b))
	res, _ = runMonitor(t, cat, "deadlocks", "--deadlock-file", dir, "--force")
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "deadlock_20261030_081500_250.xdl")); string(got) != b {
		t.Errorf("--force did not replace the file: %q", got)
	}
}
