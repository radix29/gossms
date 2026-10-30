package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// captureCatalog serves xevent capture's reads: the ring buffer (or readErr),
// the session by name (exists: the CREATE ran although it reported an
// error), the permission checks and the engine edition.
type captureCatalog struct {
	Catalog
	data    *gosmo.RingBufferData
	readErr error
	exists  bool
	noVSS   bool
	caps    *gosmo.Capabilities
	engine  gosmo.EngineEdition

	log *[]string
}

func (c *captureCatalog) ServerInfo() *gosmo.ServerInfo {
	return &gosmo.ServerInfo{EngineEdition: int(c.engine)}
}

func (c *captureCatalog) HasViewServerState(context.Context) (bool, error) { return !c.noVSS, nil }

func (c *captureCatalog) ServerCapabilities(context.Context) (*gosmo.Capabilities, error) {
	return c.caps, nil
}

func (c *captureCatalog) ReadRingBuffer(ctx context.Context, es *gosmo.EventSession) (*gosmo.RingBufferData, error) {
	*c.log = append(*c.log, "read "+es.Name)
	if c.readErr != nil {
		return nil, c.readErr
	}
	return c.data, nil
}

func (c *captureCatalog) EventSessionByName(_ context.Context, name string) (*gosmo.EventSession, error) {
	*c.log = append(*c.log, "lookup "+name)
	if !c.exists {
		return nil, fmt.Errorf("event session %q: %w", name, gosmo.ErrNotFound)
	}
	return &gosmo.EventSession{Name: name}, nil
}

// captureWriter records each step in log, failing the one errs names; a
// cleanup step records "(cancelled)" when its context is done. Under
// WithScript it is the real serverWriter, so a dry run captures gosmo's T-SQL.
type captureWriter struct {
	Writer
	errs map[string]error
	spec gosmo.EventSessionDefinition
	log  *[]string
}

func (w *captureWriter) step(ctx context.Context, verb string, es *gosmo.EventSession) error {
	entry := verb + " " + es.Name
	if ctx.Err() != nil {
		entry += " (cancelled)"
	}
	*w.log = append(*w.log, entry)
	return w.errs[verb]
}

func (w *captureWriter) CreateEventSession(ctx context.Context, spec gosmo.EventSessionDefinition) (*gosmo.EventSession, error) {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.CreateEventSession(ctx, spec)
	}
	w.spec = spec
	es := &gosmo.EventSession{Name: spec.Name}
	if err := w.step(ctx, "create", es); err != nil {
		return nil, err
	}
	return es, nil
}

func (w *captureWriter) StartEventSession(ctx context.Context, es *gosmo.EventSession) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.StartEventSession(ctx, es)
	}
	return w.step(ctx, "start", es)
}

func (w *captureWriter) StopEventSession(ctx context.Context, es *gosmo.EventSession) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.StopEventSession(ctx, es)
	}
	return w.step(ctx, "stop", es)
}

func (w *captureWriter) DropEventSession(ctx context.Context, es *gosmo.EventSession) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.DropEventSession(ctx, es)
	}
	return w.step(ctx, "drop", es)
}

const testCaptureSession = "gossms_cli_20261030120000_beef"

// newCapture is a capture fake whose ring buffer holds events, with a login
// that holds ALTER ANY EVENT SESSION.
func newCapture(events ...gosmo.XEvent) (*captureCatalog, *captureWriter) {
	log := new([]string)
	cat := &captureCatalog{log: log,
		data: &gosmo.RingBufferData{Events: events, TotalEventsProcessed: int64(len(events)), EventCount: int64(len(events))},
		caps: &gosmo.Capabilities{ServerPermissions: map[string]gosmo.CapabilityState{
			"ALTER ANY EVENT SESSION": gosmo.CapabilityGranted}}}
	return cat, &captureWriter{log: log, errs: map[string]error{}}
}

// runCaptureCmd runs xevent capture <argv> over cat and w with ctx; sleep
// replaces the wait (nil: return at once, recording the time asked).
func runCaptureCmd(t *testing.T, ctx context.Context, cat *captureCatalog, w *captureWriter, sleep func(context.Context, time.Duration) error, argv string) (res *output.Result, dials int, waited time.Duration, err error) {
	t.Helper()
	old, oldName := sleepCtx, captureSessionName
	t.Cleanup(func() { sleepCtx, captureSessionName = old, oldName })
	captureSessionName = func() string { return testCaptureSession }
	sleepCtx = func(ctx context.Context, d time.Duration) error {
		waited = d
		*cat.log = append(*cat.log, "wait")
		if sleep != nil {
			return sleep(ctx, d)
		}
		return nil
	}
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
	}
	tree := &spec.Tree{Commands: XEventCommands(d), Globals: []spec.Option{
		{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: output.Formats, Default: "text"}}}
	inv, perr := args.Parse(tree, append([]string{"xevent", "capture", "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(ctx, inv)
	return res, dials, waited, err
}

func xeAt(sec int, name string, fields, actions []gosmo.XEValue, seq uint64) gosmo.XEvent {
	return gosmo.XEvent{Name: name, Package: "sqlserver", Timestamp: time.Date(2026, 10, 30, 12, 0, sec, 0, time.UTC),
		Seq: seq, Fields: fields, Actions: actions}
}

func TestXEventCapture(t *testing.T) {
	batch := xeAt(5, "sql_batch_completed",
		[]gosmo.XEValue{{Name: "duration", Value: "1500250"}, {Name: "cpu_time", Value: "1000"},
			{Name: "logical_reads", Value: "42"}, {Name: "writes", Value: "0"}, {Name: "row_count", Value: "7"},
			{Name: "result", Value: "0", Text: "OK"}, {Name: "batch_text", Value: "SELECT 1"}},
		[]gosmo.XEValue{{Name: "session_id", Value: "61"}, {Name: "database_name", Value: "Sales"},
			{Name: "server_principal_name", Value: "app"}, {Name: "client_app_name", Value: "app.exe"},
			{Name: "client_hostname", Value: "ws7"}}, 2)
	errEvt := xeAt(5, "error_reported",
		[]gosmo.XEValue{{Name: "error_number", Value: "208"}, {Name: "severity", Value: "16"},
			{Name: "message", Value: "Invalid object name 'x'."}},
		[]gosmo.XEValue{{Name: "session_id", Value: "61"}}, 1)
	wait := xeAt(1, "wait_info", []gosmo.XEValue{{Name: "duration", Value: "12"},
		{Name: "wait_type", Value: "179", Text: "PAGEIOLATCH_SH"}}, nil, 9)
	cat, w := newCapture(batch, errEvt, wait)
	res, dials, waited, err := runCaptureCmd(t, context.Background(), cat, w, nil,
		"--template waits --duration 10 --allow admin --session-database Sales --login app --min-duration-ms 250 --max-events 50")
	if err != nil {
		t.Fatal(err)
	}
	s := testCaptureSession
	if want := []string{"create " + s, "start " + s, "wait", "read " + s, "stop " + s, "drop " + s}; dials != 1 || !slices.Equal(*cat.log, want) {
		t.Fatalf("%d dials, steps %q; want %q", dials, *cat.log, want)
	}
	if waited != 11*time.Second {
		t.Errorf("waited %v; want the duration plus the one-second latency", waited)
	}
	for k, v := range map[string]string{"template": "waits", "session": s, "durationSeconds": "10",
		"dryRun": "false", "eventsSeen": "3", "eventsKept": "3", "sessionDropped": "true"} {
		if got := fieldValue(res, k); got != v {
			t.Errorf("%s = %q; want %q", k, got, v)
		}
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}

	// The session: a ring_buffer keeping --max-events, one second's latency,
	// never at startup; every event keeps its template predicate, leaves out
	// the CLI's own connections and carries the filters; only completed work
	// gets --min-duration-ms, in microseconds.
	sp := w.spec
	if sp.Name != s || sp.MaxDispatchLatency != time.Second || sp.StartupState || len(sp.Targets) != 1 ||
		sp.Targets[0].Name != gosmo.XETargetRingBuffer {
		t.Fatalf("spec %+v", sp)
	}
	if v, _ := sp.Targets[0].Field("max_events_limit"); v != "50" {
		t.Errorf("max_events_limit %q", v)
	}
	for _, e := range sp.Events {
		for _, want := range []string{"[sqlserver].[client_app_name]<>N'goSSMS - CLI'",
			"[sqlserver].[database_name]=N'Sales'", "[sqlserver].[server_principal_name]=N'app'",
			"[sqlserver].[is_system]"} {
			if !strings.Contains(e.Predicate, want) {
				t.Errorf("%s: predicate %s lacks %s", e.Name, e.Predicate, want)
			}
		}
		if hasSession := strings.Contains(e.Predicate, "[sqlserver].[session_id]>(0)"); hasSession != (e.Name == "wait_info") {
			t.Errorf("%s: session filter %v (%s)", e.Name, hasSession, e.Predicate)
		}
		hasDuration := strings.Contains(e.Predicate, "[duration]>=(250000)")
		if completed := slices.Contains(captureDurationEvents, e.Name); hasDuration != completed {
			t.Errorf("%s: duration filter %v; want %v (%s)", e.Name, hasDuration, completed, e.Predicate)
		}
		for _, a := range captureActions {
			if !slices.Contains(e.Actions, a) {
				t.Errorf("%s: no action %s", e.Name, a)
			}
		}
	}

	// Oldest first, ties by event_sequence; wait_info's duration is already
	// milliseconds, the others' microseconds.
	want := [][]string{
		{"2026-10-30 12:00:01.000", "wait_info", "NULL", "NULL", "NULL", "NULL", "NULL", "12", "NULL",
			"NULL", "NULL", "NULL", "NULL", "NULL", "NULL", "PAGEIOLATCH_SH"},
		{"2026-10-30 12:00:05.000", "error_reported", "61", "NULL", "NULL", "NULL", "NULL", "NULL", "NULL",
			"NULL", "NULL", "NULL", "NULL", "208", "16", "Invalid object name 'x'."},
		{"2026-10-30 12:00:05.000", "sql_batch_completed", "61", "Sales", "app", "app.exe", "ws7", "1500.250", "1.000",
			"42", "0", "7", "OK", "NULL", "NULL", "SELECT 1"},
	}
	set := res.Sets[0]
	if got := setRows(set); set.Name != "events" || !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %q rows:\n got %q\nwant %q", set.Name, got, want)
	}
	if slices.ContainsFunc(set.Columns, func(c output.Column) bool { return c.Name == "data" }) {
		t.Error("text output has the data column")
	}
}

// Every template resolves to events, so a gosmo rename is caught here.
func TestXEventCaptureTemplates(t *testing.T) {
	for _, tmpl := range captureTemplates {
		def, ok := tmpl.definition()
		if !ok || len(def.Events) == 0 {
			t.Errorf("%s: no definition", tmpl.name)
		}
		if len(def.Targets) != 0 && tmpl.name != "" {
			// gosmo's templates have no target; the capture sets its own.
			t.Errorf("%s: template has targets %v", tmpl.name, def.Targets)
		}
	}
}

// -o jsonl adds data: every field and action of the event as an object.
func TestXEventCaptureJSONLData(t *testing.T) {
	cat, w := newCapture(xeAt(1, "error_reported", []gosmo.XEValue{{Name: "severity", Value: "16"}},
		[]gosmo.XEValue{{Name: "session_id", Value: "61"}}, 1))
	res, _, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, "--template errors --allow admin -o jsonl")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	col := set.Columns[len(set.Columns)-1]
	if col.Name != "data" || col.Type != output.TypeJSON {
		t.Fatalf("last column %+v", col)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal([]byte(*set.Rows[0][len(set.Columns)-1]), &got); err != nil {
		t.Fatal(err)
	}
	if got["fields"]["severity"] != "16" || got["actions"]["session_id"] != "61" {
		t.Errorf("data %v", got)
	}
}

// The buffer kept fewer events than it saw: fields and a warning say so.
func TestXEventCaptureTruncated(t *testing.T) {
	cat, w := newCapture(xeAt(1, "login", nil, nil, 1))
	cat.data.TotalEventsProcessed, cat.data.Truncated = 1500, true
	res, _, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, "--allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if fieldValue(res, "eventsSeen") != "1500" || fieldValue(res, "eventsKept") != "1" ||
		len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "kept 1 of the 1500") ||
		!strings.Contains(res.Warnings[0], "4 MB") {
		t.Errorf("fields %+v, warnings %q", res.Fields, res.Warnings)
	}
}

// Every exit path after the CREATE stops (once started) and drops the
// session, and the step's own error is what the command returns.
func TestXEventCaptureCleansUpOnFailure(t *testing.T) {
	s := testCaptureSession
	refused := gosmo.ErrNotFound // any error; its mapping is not under test
	for _, tc := range []struct {
		name   string
		setup  func(*captureCatalog, *captureWriter)
		steps  []string
		code   output.Code
		warned bool
	}{
		{"create refused, nothing made",
			func(c *captureCatalog, w *captureWriter) { w.errs["create"] = errors.New("Msg 25602") },
			[]string{"create " + s, "lookup " + s}, output.CodeGeneralError, false},
		{"create reported an error but the session exists",
			func(c *captureCatalog, w *captureWriter) {
				w.errs["create"], c.exists = errors.New("read-back failed"), true
			},
			[]string{"create " + s, "lookup " + s, "drop " + s}, output.CodeGeneralError, false},
		{"start refused",
			func(c *captureCatalog, w *captureWriter) { w.errs["start"] = errors.New("Msg 25704") },
			[]string{"create " + s, "start " + s, "drop " + s}, output.CodeGeneralError, false},
		{"read failed",
			func(c *captureCatalog, w *captureWriter) { c.readErr = fmt.Errorf("gone: %w", refused) },
			[]string{"create " + s, "start " + s, "wait", "read " + s, "stop " + s, "drop " + s}, output.CodeNotFound, false},
		{"stop failed, drop still made",
			func(c *captureCatalog, w *captureWriter) { w.errs["stop"] = errors.New("Msg 25705") },
			[]string{"create " + s, "start " + s, "wait", "read " + s, "stop " + s, "drop " + s}, output.CodeOK, false},
		{"drop failed",
			func(c *captureCatalog, w *captureWriter) { w.errs["drop"] = errors.New("Msg 15151") },
			[]string{"create " + s, "start " + s, "wait", "read " + s, "stop " + s, "drop " + s}, output.CodeOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat, w := newCapture()
			tc.setup(cat, w)
			res, _, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, "--allow admin")
			if !slices.Equal(*cat.log, tc.steps) {
				t.Errorf("steps %q; want %q", *cat.log, tc.steps)
			}
			if code := errCode(err); err != nil && code != tc.code || err == nil && tc.code != output.CodeOK {
				t.Errorf("%v (%s); want %s", err, code, tc.code)
			}
			warned := slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, s) })
			if warned != tc.warned {
				t.Errorf("warnings %q; want one naming the session: %v", res.Warnings, tc.warned)
			}
			if tc.warned && fieldValue(res, "sessionDropped") != "false" {
				t.Errorf("sessionDropped %q", fieldValue(res, "sessionDropped"))
			}
		})
	}
}

// Ctrl+C during the wait: the stop and drop still run, under a context that
// is not cancelled, and the command returns the cancellation (exit 130).
func TestXEventCaptureCancelled(t *testing.T) {
	cat, w := newCapture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, _, err := runCaptureCmd(t, ctx, cat, w, func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}, "--allow admin --duration 600")
	s := testCaptureSession
	if want := []string{"create " + s, "start " + s, "wait", "stop " + s, "drop " + s}; !slices.Equal(*cat.log, want) {
		t.Errorf("steps %q; want %q", *cat.log, want)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v; want the cancellation", err)
	}
}

// A cancel that lands during the CREATE: the session may exist, so it is
// looked up and dropped under a live context.
func TestXEventCaptureCancelledDuringCreate(t *testing.T) {
	cat, w := newCapture()
	cat.exists = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.errs["create"] = context.Canceled
	_, _, _, err := runCaptureCmd(t, ctx, cat, w, nil, "--allow admin")
	s := testCaptureSession
	if want := []string{"create " + s + " (cancelled)", "lookup " + s, "drop " + s}; !slices.Equal(*cat.log, want) {
		t.Errorf("steps %q; want %q", *cat.log, want)
	}
	if err == nil {
		t.Error("no error")
	}
}

func TestXEventCaptureDryRun(t *testing.T) {
	cat, w := newCapture()
	res, _, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, "--template deadlocks --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(*cat.log) != 0 {
		t.Errorf("steps %q; want none", *cat.log)
	}
	tsql := fieldValue(res, "tsql")
	for _, want := range []string{"CREATE EVENT SESSION [" + testCaptureSession + "] ON SERVER",
		"sqlserver.xml_deadlock_report", "package0.ring_buffer", "max_events_limit",
		"STATE = START", "STATE = STOP", "DROP EVENT SESSION [" + testCaptureSession + "] ON SERVER"} {
		if !strings.Contains(tsql, want) {
			t.Errorf("tsql lacks %q:\n%s", want, tsql)
		}
	}
	if fieldValue(res, "dryRun") != "true" || len(res.Sets) != 0 {
		t.Errorf("fields %+v, sets %d", res.Fields, len(res.Sets))
	}
}

// Refused before anything is created: options and the class before
// connecting, the server's edition and the login's rights after.
func TestXEventCaptureRefused(t *testing.T) {
	denied := func(granted ...string) *gosmo.Capabilities {
		m := map[string]gosmo.CapabilityState{"ALTER ANY EVENT SESSION": gosmo.CapabilityDenied}
		for _, g := range granted {
			m[g] = gosmo.CapabilityGranted
		}
		return &gosmo.Capabilities{ServerPermissions: m}
	}
	for _, tc := range []struct {
		argv  string
		setup func(*captureCatalog)
		want  output.Code
		dials int
	}{
		{"--duration 0 --allow admin", nil, output.CodeUsageError, 0},
		{"--duration 601 --allow admin", nil, output.CodeUsageError, 0},
		{"--max-events 0 --allow admin", nil, output.CodeUsageError, 0},
		{"--min-duration-ms -1 --allow admin", nil, output.CodeUsageError, 0},
		{"--program " + strings.Repeat("x", 129) + " --allow admin", nil, output.CodeUsageError, 0},
		{"--login " + strings.Repeat("é", 129) + " --allow admin", nil, output.CodeUsageError, 0},
		{"--session-database " + strings.Repeat("x", 129) + " --allow admin", nil, output.CodeUsageError, 0},
		{"", nil, output.CodeBlocked, 0},
		{"--allow ddl --dry-run", nil, output.CodeBlocked, 0},
		{"--allow admin", func(c *captureCatalog) { c.engine = gosmo.EngineAzureSQLDatabase }, output.CodeUnsupported, 1},
		{"--allow admin", func(c *captureCatalog) { c.noVSS = true }, output.CodePermissionDenied, 1},
		{"--allow admin --dry-run", func(c *captureCatalog) { c.noVSS = true }, output.CodePermissionDenied, 1},
		{"--allow admin", func(c *captureCatalog) { c.caps = denied() }, output.CodePermissionDenied, 1},
		// 2022: three of the four granular rights is not enough.
		{"--allow admin", func(c *captureCatalog) {
			c.caps = denied("CREATE ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE", "DROP ANY EVENT SESSION")
		}, output.CodePermissionDenied, 1},
	} {
		cat, w := newCapture()
		if tc.setup != nil {
			tc.setup(cat)
		}
		_, dials, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, tc.argv)
		if errCode(err) != tc.want || dials != tc.dials || len(*cat.log) != 0 {
			t.Errorf("%q: %v (%s), %d dials, steps %q; want %s, %d dials, no step", tc.argv, err, errCode(err), dials, *cat.log, tc.want, tc.dials)
		}
	}
	// A name's length is in characters: 128 two-byte ones is a sysname.
	{
		cat, w := newCapture()
		if _, _, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, "--login "+strings.Repeat("é", 128)+" --allow admin"); err != nil {
			t.Errorf("a 128-character login refused: %v", err)
		}
	}
	// 2022 granular rights in place of the wide one.
	cat, w := newCapture()
	cat.caps = denied(eventSessionRights...)
	if _, _, _, err := runCaptureCmd(t, context.Background(), cat, w, nil, "--allow admin"); err != nil {
		t.Errorf("granular rights refused: %v", err)
	}
}
