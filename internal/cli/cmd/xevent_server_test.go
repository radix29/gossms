package cmd

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// xeServer is real gosmo over canned rows for xevent sessions and xevent
// capture: a catalogServer (the connection's database Sales) with whether
// the login holds VIEW SERVER STATE, the server's event sessions, a running
// session's status, its ring_buffer and the login's event-session rights.
// A CREATE, ALTER … STATE and DROP change the sessions as the server would
// (applyWrite), and the reads after one see that.
//
// steps is what the server was asked, in order, that a capture is judged by:
// each write ("create s", "start s", "stop s", "drop s"), a session looked up
// by name ("lookup s": gosmo's read-back after a CREATE, and the cleanup's
// check for a session a failed CREATE may have left), the ring_buffer read
// ("read s") and the capture's wait ("wait").
type xeServer struct {
	*catalogServer
	noVSS bool // HAS_PERMS_BY_NAME answers 0

	// sessions is the server's (or Azure SQL Database's) event sessions;
	// the reads answer them in name order, as the server's ORDER BY does.
	sessions []*gosmo.EventSession
	// status is each running session's runtime state by name; one missing
	// is a session that stopped since the list was read. statusErr fails
	// every status read.
	status    map[string]*gosmo.EventSessionStatus
	statusErr error

	// events is what a running session's ring_buffer holds. seen and cut,
	// when set, are the totalEventsProcessed and truncated the buffer
	// reports; noBuffer answers the read with no row, as the server does
	// for a session without a running ring_buffer.
	events   []gosmo.XEvent
	seen     int64
	cut      bool
	noBuffer bool

	// caps is the login's server permissions as HAS_PERMS_BY_NAME answers.
	caps map[string]gosmo.CapabilityState

	// steps is documented above; creates is each CREATE's statement.
	steps   []string
	creates []string
	// writeErrs fails the write named by its verb (create, start, stop,
	// drop) after it is recorded; a failed CREATE still leaves its session
	// when createLeaves, as one whose read-back failed would. hooks run as
	// the verb's write arrives.
	writeErrs     map[string]error
	createLeaves  bool
	hooks         map[string]func()
	listDB        []string // the database each session list was read in
	statusReadsOf []string
}

func newXEServer(t *testing.T) *xeServer {
	s := &xeServer{
		status:    map[string]*gosmo.EventSessionStatus{},
		writeErrs: map[string]error{},
		hooks:     map[string]func(){},
		caps:      map[string]gosmo.CapabilityState{"ALTER ANY EVENT SESSION": gosmo.CapabilityGranted},
	}
	newCatalogServerWith(t, "Sales", func(c *catalogServer) { s.catalogServer = c; s.rules() })
	return s
}

// reads is how many queries s was sent containing every one of subs.
func (s *xeServer) reads(subs ...string) int {
	n := 0
	for _, q := range s.Queries() {
		if has(q, subs...) {
			n++
		}
	}
	return n
}

var xeWrite = regexp.MustCompile(`^(CREATE|DROP) EVENT SESSION \[(.+?)\] ON (?:SERVER|DATABASE)|^ALTER EVENT SESSION \[(.+?)\] ON SERVER STATE = (START|STOP)$`)

// applyWrite is the server taking q: recorded, then failed with the verb's
// writeErr, else applied to the sessions.
func (s *xeServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	m := xeWrite.FindStringSubmatch(q.Text)
	if m == nil {
		s.t.Errorf("xeServer: a write it does not know: %s", q)
		return gosmotest.Result{}
	}
	var verb, name string
	switch {
	case m[1] != "":
		verb, name = strings.ToLower(m[1]), m[2]
	default:
		verb, name = strings.ToLower(m[4]), m[3]
	}
	name = strings.ReplaceAll(name, "]]", "]")
	s.steps = append(s.steps, verb+" "+name)
	if verb == "create" {
		s.creates = append(s.creates, q.Text)
	}
	if hook := s.hooks[verb]; hook != nil {
		hook()
	}
	err := s.writeErrs[verb]
	if err != nil && !(verb == "create" && s.createLeaves) {
		return gosmotest.Result{Err: err}
	}
	i := slices.IndexFunc(s.sessions, func(es *gosmo.EventSession) bool { return es.Name == name })
	switch verb {
	case "create":
		s.sessions = append(s.sessions, &gosmo.EventSession{Name: name})
	case "start":
		s.sessions[i].IsRunning = true
	case "stop":
		s.sessions[i].IsRunning = false
	case "drop":
		s.sessions = slices.Delete(s.sessions, i, i+1)
	}
	return gosmotest.Result{Err: err}
}

// sorted is the sessions in name order, each numbered from 1 in that order.
func (s *xeServer) sorted() []*gosmo.EventSession {
	out := slices.Clone(s.sessions)
	slices.SortFunc(out, func(a, b *gosmo.EventSession) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// ringBuffer is the running session's target_data.
func (s *xeServer) ringBuffer() string {
	x := clitest.RingBufferXML(s.events)
	if s.seen > 0 {
		x = strings.Replace(x, fmt.Sprintf(`totalEventsProcessed="%d"`, len(s.events)), fmt.Sprintf(`totalEventsProcessed="%d"`, s.seen), 1)
	}
	if s.cut {
		x = strings.Replace(x, `truncated="0"`, `truncated="1"`, 1)
	}
	return x
}

func (s *xeServer) rules() {
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, s.applyWrite)
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "HAS_PERMS_BY_NAME(NULL, NULL, 'VIEW SERVER STATE')") },
		func(gosmotest.Query) gosmotest.Result {
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: [][]any{{map[bool]int{true: 0, false: 1}[s.noVSS]}}}}}
		})
	// The login's server permissions: 'P' rows of the capability probe.
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for name, st := range s.caps {
			rows = append(rows, []any{"P", name, map[bool]int{true: 1, false: 0}[st == gosmo.CapabilityGranted]})
		}
		return rows
	}, "IS_SRVROLEMEMBER")

	// Sessions and their children, in either scope: all of them, or the
	// one a WHERE s.name = @p1 names. A named read is a lookup.
	for _, scope := range []string{"server", "database"} {
		dmSessions := map[string]string{"server": "sys.dm_xe_sessions", "database": "sys.dm_xe_database_sessions"}[scope]
		picked := func(q gosmotest.Query) []*gosmo.EventSession {
			var out []*gosmo.EventSession
			for _, es := range s.sorted() {
				if !has(q, "WHERE  s.name = @p1") || es.Name == argString(q, 0) {
					out = append(out, es)
				}
			}
			return out
		}
		id := func(es *gosmo.EventSession) int { return slices.Index(s.sorted(), es) + 1 }
		child := func(read string, pick func(events, actions, targets, fields [][]any) [][]any) {
			s.on(func(q gosmotest.Query) [][]any {
				var rows [][]any
				for _, es := range picked(q) {
					_, e, a, t, f := clitest.EventSessionRows(es, id(es))
					rows = append(rows, pick(e, a, t, f)...)
				}
				return rows
			}, "FROM   sys."+scope+"_event_session_"+read)
		}
		s.on(func(q gosmotest.Query) [][]any {
			if has(q, "WHERE  s.name = @p1") {
				s.steps = append(s.steps, "lookup "+argString(q, 0))
			} else {
				s.listDB = append(s.listDB, q.Database)
			}
			var rows [][]any
			for _, es := range picked(q) {
				row, _, _, _, _ := clitest.EventSessionRows(es, id(es))
				rows = append(rows, row)
			}
			return rows
		}, "FROM   sys."+scope+"_event_sessions s\nLEFT   JOIN "+dmSessions+" r")
		child("events e", func(e, _, _, _ [][]any) [][]any { return e })
		child("actions a", func(_, a, _, _ [][]any) [][]any { return a })
		child("targets t", func(_, _, t, _ [][]any) [][]any { return t })
		child("fields f", func(_, _, _, f [][]any) [][]any { return f })
	}

	// A running session's status: one row per target, or one with none.
	statusRead := func(q gosmotest.Query) [][]any {
		name := argString(q, 0)
		s.statusReadsOf = append(s.statusReadsOf, name)
		st, ok := s.status[name]
		if !ok {
			return nil
		}
		row := func(t *gosmo.EventTargetStatus) []any {
			if t == nil {
				return []any{st.CreateTime, st.DroppedEvents, st.DroppedBuffers, nil, nil, nil, nil, nil, nil, nil, nil, nil}
			}
			return []any{st.CreateTime, st.DroppedEvents, st.DroppedBuffers, t.Package, t.Name, t.ExecutionCount,
				t.ExecutionDurationMs, clitest.Nullable(t.CurrentFile), t.TotalEventsProcessed, t.EventCount,
				t.DroppedCount, t.Truncated}
		}
		if len(st.Targets) == 0 {
			return [][]any{row(nil)}
		}
		var rows [][]any
		for i := range st.Targets {
			rows = append(rows, row(&st.Targets[i]))
		}
		return rows
	}
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "x.d.value('(/EventFileTarget/File/@name)[1]'") },
		func(q gosmotest.Query) gosmotest.Result {
			if s.statusErr != nil {
				return gosmotest.Result{Err: s.statusErr}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: statusRead(q)}}}
		})

	// The capture's ring_buffer: only a running session has one.
	s.on(func(q gosmotest.Query) [][]any {
		name := argString(q, 0)
		s.steps = append(s.steps, "read "+name)
		i := slices.IndexFunc(s.sessions, func(es *gosmo.EventSession) bool { return es.Name == name })
		if s.noBuffer || i < 0 || !s.sessions[i].IsRunning {
			return nil
		}
		return [][]any{{s.ringBuffer()}}
	}, "WHERE  s.name = @p1 AND t.target_name = N'ring_buffer'")
}

// xeSessionsFixture is three sessions: a stopped one with an event_file, one
// running in the list but gone by the time its status is read, and
// system_health, running with a status.
func xeSessionsFixture(s *xeServer) {
	started := time.Date(2026, 10, 14, 16, 33, 15, 110e6, time.UTC)
	s.sessions = []*gosmo.EventSession{
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
	}
	s.status["system_health"] = &gosmo.EventSessionStatus{CreateTime: started, DroppedEvents: 3, Targets: []gosmo.EventTargetStatus{
		{Package: "package0", Name: gosmo.XETargetEventFile, CurrentFile: `C:\Log\system_health_0_1.xel`},
		{Package: "package0", Name: gosmo.XETargetRingBuffer, EventCount: 10},
	}}
}

// runXEvent runs xevent <argv> over s, connecting with a fixed server and
// Windows auth, with ctx; dials is how many connections it made.
func runXEventCtx(t *testing.T, ctx context.Context, s *xeServer, argv ...string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			dials++
			return s.conn(ctx)
		},
	}
	tree := &spec.Tree{Commands: XEventCommands(d), Globals: []spec.Option{
		{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: output.Formats, Default: "text"}}}
	inv, perr := args.Parse(tree, append([]string{"xevent", argv[0], "--server", "srv", "--auth", "windows"}, argv[1:]...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(ctx, inv)
	return res, dials, err
}

func runXEvent(t *testing.T, s *xeServer, argv ...string) (*output.Result, error) {
	t.Helper()
	res, _, err := runXEventCtx(t, context.Background(), s, argv...)
	return res, err
}
