package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// xevent capture's tests run on xeServer (xevent_server_test.go): real
// gosmo over canned catalog rows, with a CREATE, START, STOP and DROP
// changing its sessions as the server would. Its steps are the order of the
// writes and the reads a capture is judged by.

const testCaptureSession = "gossms_cli_20261030120000_beef"

// newCapture is an xeServer whose ring buffer holds events, with a login
// that holds ALTER ANY EVENT SESSION.
func newCapture(t *testing.T, events ...gosmo.XEvent) *xeServer {
	s := newXEServer(t)
	s.events = events
	return s
}

// runCaptureCmd runs xevent capture <argv> over s with ctx; sleep replaces
// the wait (nil: return at once, recording the time asked).
func runCaptureCmd(t *testing.T, ctx context.Context, s *xeServer, sleep func(context.Context, time.Duration) error, argv string) (res *output.Result, dials int, waited time.Duration, err error) {
	t.Helper()
	old, oldName := sleepCtx, captureSessionName
	t.Cleanup(func() { sleepCtx, captureSessionName = old, oldName })
	captureSessionName = func() string { return testCaptureSession }
	sleepCtx = func(ctx context.Context, d time.Duration) error {
		waited = d
		s.steps = append(s.steps, "wait")
		if sleep != nil {
			return sleep(ctx, d)
		}
		return nil
	}
	res, dials, err = runXEventCtx(t, ctx, s, append([]string{"capture"}, strings.Fields(argv)...)...)
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
	xs := newCapture(t, batch, errEvt, wait)
	res, dials, waited, err := runCaptureCmd(t, context.Background(), xs, nil,
		"--template waits --duration 10 --allow admin --session-database Sales --login app --min-duration-ms 250 --max-events 50")
	if err != nil {
		t.Fatal(err)
	}
	s := testCaptureSession
	// The lookup is gosmo's read-back after the CREATE.
	if want := []string{"create " + s, "lookup " + s, "start " + s, "wait", "read " + s, "stop " + s, "drop " + s}; dials != 1 || !slices.Equal(xs.steps, want) {
		t.Fatalf("%d dials, steps %q; want %q", dials, xs.steps, want)
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
	if len(xs.creates) != 1 {
		t.Fatalf("%d CREATEs", len(xs.creates))
	}
	stmt := xs.creates[0]
	for _, want := range []string{"CREATE EVENT SESSION [" + s + "] ON SERVER", "MAX_DISPATCH_LATENCY=1 SECONDS",
		"STARTUP_STATE=OFF", "ADD TARGET package0.ring_buffer", "max_events_limit"} {
		if !strings.Contains(stmt, want) {
			t.Errorf("CREATE lacks %q:\n%s", want, stmt)
		}
	}
	if strings.Count(stmt, "ADD TARGET") != 1 || !strings.Contains(stmt, "(50)") {
		t.Errorf("want one ring_buffer keeping 50 events:\n%s", stmt)
	}
	events := strings.Split(stmt, "ADD EVENT ")[1:]
	if len(events) == 0 {
		t.Fatalf("no events:\n%s", stmt)
	}
	for _, e := range events {
		name, _, _ := strings.Cut(strings.TrimPrefix(e, "sqlserver."), "(")
		name = strings.TrimPrefix(strings.TrimPrefix(name, "sqlos."), "sqlserver.")
		for _, want := range []string{"[sqlserver].[client_app_name]<>N'goSSMS - CLI'",
			"[sqlserver].[database_name]=N'Sales'", "[sqlserver].[server_principal_name]=N'app'",
			"[sqlserver].[is_system]"} {
			if !strings.Contains(e, want) {
				t.Errorf("%s: lacks %s", name, want)
			}
		}
		if hasSession := strings.Contains(e, "[sqlserver].[session_id]>(0)"); hasSession != (name == "wait_info") {
			t.Errorf("%s: session filter %v", name, hasSession)
		}
		hasDuration := strings.Contains(e, "[duration]>=(250000)")
		if completed := slices.Contains(captureDurationEvents, name); hasDuration != completed {
			t.Errorf("%s: duration filter %v; want %v", name, hasDuration, completed)
		}
		for _, a := range captureActions {
			if !strings.Contains(e, a) {
				t.Errorf("%s: no action %s", name, a)
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
	xs := newCapture(t, xeAt(1, "error_reported", []gosmo.XEValue{{Name: "severity", Value: "16"}},
		[]gosmo.XEValue{{Name: "session_id", Value: "61"}}, 1))
	res, _, _, err := runCaptureCmd(t, context.Background(), xs, nil, "--template errors --allow admin -o jsonl")
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
	xs := newCapture(t, xeAt(1, "login", nil, nil, 1))
	xs.seen, xs.cut = 1500, true
	res, _, _, err := runCaptureCmd(t, context.Background(), xs, nil, "--allow admin")
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
	full := []string{"create " + s, "lookup " + s, "start " + s, "wait", "read " + s, "stop " + s, "drop " + s}
	for _, tc := range []struct {
		name   string
		setup  func(*xeServer)
		steps  []string
		code   output.Code
		warned bool
	}{
		// The CREATE ran nowhere: the cleanup's look finds nothing.
		{"create refused, nothing made",
			func(x *xeServer) { x.writeErrs["create"] = errors.New("Msg 25602") },
			[]string{"create " + s, "lookup " + s}, output.CodeGeneralError, false},
		// The CREATE ran although it reported an error.
		{"create reported an error but the session exists",
			func(x *xeServer) {
				x.writeErrs["create"], x.createLeaves = errors.New("read-back failed"), true
			},
			[]string{"create " + s, "lookup " + s, "drop " + s}, output.CodeGeneralError, false},
		{"start refused",
			func(x *xeServer) { x.writeErrs["start"] = errors.New("Msg 25704") },
			[]string{"create " + s, "lookup " + s, "start " + s, "drop " + s}, output.CodeGeneralError, false},
		{"read failed",
			func(x *xeServer) { x.noBuffer = true },
			full, output.CodeNotFound, false},
		{"stop failed, drop still made",
			func(x *xeServer) { x.writeErrs["stop"] = errors.New("Msg 25705") },
			full, output.CodeOK, false},
		{"drop failed",
			func(x *xeServer) { x.writeErrs["drop"] = errors.New("Msg 15151") },
			full, output.CodeOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xs := newCapture(t)
			tc.setup(xs)
			res, _, _, err := runCaptureCmd(t, context.Background(), xs, nil, "--allow admin")
			if !slices.Equal(xs.steps, tc.steps) {
				t.Errorf("steps %q; want %q", xs.steps, tc.steps)
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
			// Whatever happened, no session of the capture is left.
			if slices.ContainsFunc(xs.sessions, func(es *gosmo.EventSession) bool { return es.Name == s }) != tc.warned {
				t.Errorf("sessions left %v; want it left only when the drop failed", xs.sessions)
			}
		})
	}
}

// Ctrl+C during the wait: the stop and drop still run, under a context that
// is not cancelled (a cancelled one would not reach the server at all), and
// the command returns the cancellation (exit 130).
func TestXEventCaptureCancelled(t *testing.T) {
	xs := newCapture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, _, err := runCaptureCmd(t, ctx, xs, func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}, "--allow admin --duration 600")
	s := testCaptureSession
	if want := []string{"create " + s, "lookup " + s, "start " + s, "wait", "stop " + s, "drop " + s}; !slices.Equal(xs.steps, want) {
		t.Errorf("steps %q; want %q", xs.steps, want)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v; want the cancellation", err)
	}
	if len(xs.sessions) != 0 {
		t.Errorf("sessions left %v", xs.sessions)
	}
}

// A cancel that lands during the CREATE: the session exists, so it is looked
// up and dropped under a live context.
func TestXEventCaptureCancelledDuringCreate(t *testing.T) {
	xs := newCapture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	xs.hooks["create"] = cancel
	xs.writeErrs["create"], xs.createLeaves = context.Canceled, true
	_, _, _, err := runCaptureCmd(t, ctx, xs, nil, "--allow admin")
	s := testCaptureSession
	if want := []string{"create " + s, "lookup " + s, "drop " + s}; !slices.Equal(xs.steps, want) {
		t.Errorf("steps %q; want %q", xs.steps, want)
	}
	if err == nil {
		t.Error("no error")
	}
	if len(xs.sessions) != 0 {
		t.Errorf("sessions left %v", xs.sessions)
	}
}

func TestXEventCaptureDryRun(t *testing.T) {
	xs := newCapture(t)
	res, _, _, err := runCaptureCmd(t, context.Background(), xs, nil, "--template deadlocks --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(xs.steps) != 0 {
		t.Errorf("steps %q; want none", xs.steps)
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
	denied := func(granted ...string) map[string]gosmo.CapabilityState {
		m := map[string]gosmo.CapabilityState{"ALTER ANY EVENT SESSION": gosmo.CapabilityDenied}
		for _, g := range granted {
			m[g] = gosmo.CapabilityGranted
		}
		return m
	}
	for _, tc := range []struct {
		argv  string
		setup func(*xeServer)
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
		{"--allow admin", func(x *xeServer) { x.Info.EngineEdition = int(gosmo.EngineAzureSQLDatabase) }, output.CodeUnsupported, 1},
		{"--allow admin", func(x *xeServer) { x.noVSS = true }, output.CodePermissionDenied, 1},
		{"--allow admin --dry-run", func(x *xeServer) { x.noVSS = true }, output.CodePermissionDenied, 1},
		{"--allow admin", func(x *xeServer) { x.caps = denied() }, output.CodePermissionDenied, 1},
		// 2022: three of the four granular rights is not enough.
		{"--allow admin", func(x *xeServer) {
			x.caps = denied("CREATE ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE", "DROP ANY EVENT SESSION")
		}, output.CodePermissionDenied, 1},
	} {
		xs := newCapture(t)
		if tc.setup != nil {
			tc.setup(xs)
		}
		_, dials, _, err := runCaptureCmd(t, context.Background(), xs, nil, tc.argv)
		if errCode(err) != tc.want || dials != tc.dials || len(xs.steps) != 0 {
			t.Errorf("%q: %v (%s), %d dials, steps %q; want %s, %d dials, no step", tc.argv, err, errCode(err), dials, xs.steps, tc.want, tc.dials)
		}
	}
	// A name's length is in characters: 128 two-byte ones is a sysname.
	{
		xs := newCapture(t)
		if _, _, _, err := runCaptureCmd(t, context.Background(), xs, nil, "--login "+strings.Repeat("é", 128)+" --allow admin"); err != nil {
			t.Errorf("a 128-character login refused: %v", err)
		}
	}
	// 2022 granular rights in place of the wide one.
	xs := newCapture(t)
	xs.caps = denied(eventSessionRights...)
	if _, _, _, err := runCaptureCmd(t, context.Background(), xs, nil, "--allow admin"); err != nil {
		t.Errorf("granular rights refused: %v", err)
	}
}
