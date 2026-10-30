package cmd

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/db"
)

// --- xevent capture ---------------------------------------------------------

// The xevent capture options (D9). --session-database, --login and --program
// are monitor sessions' names: -d/--database is the connection's.
const (
	optTemplate      = "template"
	optDuration      = "duration"
	optMaxEvents     = "max-events"
	optMinDurationMs = "min-duration-ms"
)

const (
	defaultCaptureSeconds = 30
	maxCaptureSeconds     = 600
	defaultCaptureEvents  = 1000
	maxCaptureEvents      = 100000

	// maxFilterNameChars is a sysname's length: --session-database, --login
	// and --program name a database, login or client program.
	maxFilterNameChars = 128

	// captureLatency is the session's MAX_DISPATCH_LATENCY: the capture
	// waits it out after --duration, so the last second's events reach the
	// ring buffer before it is read.
	captureLatency = time.Second

	// captureCleanupTimeout bounds the stop and drop, made under a context
	// of their own so a Ctrl+C that ended the wait still removes the session.
	captureCleanupTimeout = 15 * time.Second

	// captureSessionPrefix names every session xevent capture creates.
	captureSessionPrefix = "gossms_cli_"
)

// captureTemplate is one --template value: gosmo's template of that name, or
// def for one the CLI defines itself.
type captureTemplate struct {
	name   string
	gosmo  string // XESessionTemplates' Name; empty when def is set
	def    func() gosmo.EventSessionDefinition
	events string // what it collects, for the description
}

// captureTemplates is --template's enum, in help order. gosmo's Count Query
// Locks is left out: it writes to a histogram, not events.
var captureTemplates = []captureTemplate{
	{name: "standard", gosmo: "Standard", events: "logins, logouts, attentions and every completed batch and RPC"},
	{name: "tsql", gosmo: "TSQL", events: "every batch and RPC as it starts, with logins and logouts"},
	{name: "batches", gosmo: "Query Batch Tracking", events: "completed batches and RPCs, attentions and errors"},
	{name: "statements", gosmo: "Query Detail Tracking", events: "batches and RPCs with every statement and module they ran"},
	{name: "waits", gosmo: "Query Wait Statistics", events: "every finished wait, with the batches and RPCs around it"},
	{name: "connections", gosmo: "Connection Tracking", events: "logins, logouts and failed logins"},
	{name: "deadlocks", gosmo: "Deadlocks", events: "every deadlock graph"},
	{name: "errors", def: errorsTemplate, events: "every error of severity 11 or higher"},
}

// errorsTemplate is --template errors: error_reported at severity 11 and up,
// an error a client sees. It is a gossms opinion, so it is not in gosmo's
// SSMS-derived list.
func errorsTemplate() gosmo.EventSessionDefinition {
	return gosmo.EventSessionDefinition{
		Events: []gosmo.SessionEvent{{Package: "sqlserver", Name: "error_reported",
			Actions:   []string{"package0.event_sequence", "sqlserver.sql_text"},
			Predicate: "([package0].[greater_than_equal_int64]([severity],(11)))"}},
		RetentionMode:       gosmo.XERetentionAllowSingleEventLoss,
		MaxMemory:           4096,
		MemoryPartitionMode: gosmo.XEPartitionNone,
	}
}

func captureTemplateNames() []string {
	out := make([]string, len(captureTemplates))
	for i, t := range captureTemplates {
		out[i] = t.name
	}
	return out
}

// definition is the template's events and options; ok is false when gosmo
// no longer has a template of that name (a test guards it).
func (t captureTemplate) definition() (gosmo.EventSessionDefinition, bool) {
	if t.def != nil {
		return t.def(), true
	}
	for _, g := range gosmo.XESessionTemplates() {
		if g.Name == t.gosmo {
			return g.Definition(), true
		}
	}
	return gosmo.EventSessionDefinition{}, false
}

// captureActions are collected with every event, beside the template's own,
// so the events set's columns are filled whatever the template collects.
var captureActions = []string{"package0.event_sequence", "sqlserver.session_id", "sqlserver.database_name",
	"sqlserver.server_principal_name", "sqlserver.client_app_name", "sqlserver.client_hostname"}

// captureDurationEvents are the events --min-duration-ms filters: those that
// end a unit of work, whose duration field is in microseconds.
var captureDurationEvents = []string{"sql_batch_completed", "rpc_completed", "sql_statement_completed",
	"sp_statement_completed", "module_end"}

func xeventCaptureCommand(d Deps) *spec.Command {
	var tmpl []string
	for _, t := range captureTemplates {
		tmpl = append(tmpl, t.name+" ("+t.events+")")
	}
	return &spec.Command{
		Path:    []string{"xevent", "capture"},
		Summary: "Capture events for a while with a short-lived Extended Events session, then drop it",
		Description: "Creates an event session " + captureSessionPrefix + "<yyyymmddhhmmss>_<4 hex> (UTC) from " +
			"--template, with a ring_buffer target, starts it, waits --duration seconds (1 to 600, default 30) " +
			"plus its one-second dispatch latency, reads the buffer, then stops and drops the session — on " +
			"every exit path, a failed step or Ctrl+C included (exit 130 after the drop). A drop that fails is " +
			"a warning naming the session; xevent sessions lists any " + captureSessionPrefix + " session left " +
			"behind (a killed process cannot drop it), and DROP EVENT SESSION removes it. Templates: " +
			strings.Join(tmpl, "; ") + ". Events of this command's own connections are left out. " +
			"--session-database, --login and --program keep only the events of that database, login or " +
			"client program (exact names, filtered by the server); --min-duration-ms keeps only completed " +
			"batches, RPCs, statements and modules that ran at least that long, leaving the template's other " +
			"events alone. Set events, oldest first: time_utc, event, then what the event carries — " +
			"session_id database login program host, duration_ms and cpu_ms (a wait_info's duration is its " +
			"wait), logical_reads writes row_count result, error and severity (error_reported), and text: " +
			"the batch, statement or error message, a deadlock's graph, a wait's type. With -o jsonl each row " +
			"adds data, an object of every field and action of the event. The buffer keeps the newest " +
			"--max-events (default 1000) and at most about 4 MB; fields eventsSeen and eventsKept say when it " +
			"kept fewer than were seen, with a warning. Needs --allow admin (it creates a server object), " +
			"ALTER ANY EVENT SESSION (from SQL Server 2022 CREATE, ENABLE, DISABLE and DROP ANY EVENT " +
			"SESSION do) and VIEW SERVER STATE: without them, exit 9 and nothing is created. --dry-run prints " +
			"the CREATE, START, STOP and DROP without running them. Not on Azure SQL Database (exit 11), " +
			"which has no server-scoped sessions.",
		Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optTemplate, Type: spec.TypeEnum, Enum: captureTemplateNames(), Default: "standard",
				Help: "What to collect"},
			{Name: optDuration, Type: spec.TypeInt, Default: strconv.Itoa(defaultCaptureSeconds),
				Help: "Seconds to capture, 1 to 600"},
			{Name: optMaxEvents, Type: spec.TypeInt, Default: strconv.Itoa(defaultCaptureEvents),
				Help: "Events the buffer keeps, the newest; 1 to 100000"},
			{Name: optSessionDatabase, Type: spec.TypeString, Help: "Keep only the events in this database"},
			{Name: optLogin, Type: spec.TypeString, Help: "Keep only the events of this login"},
			{Name: optProgram, Type: spec.TypeString, Help: "Keep only the events of this client program (application name)"},
			{Name: optMinDurationMs, Type: spec.TypeInt, Default: "0",
				Help: "Keep only completed work that ran at least this many milliseconds; 0 keeps all"},
		}),
		Class: spec.ClassAdmin,
		Run:   d.xeventCapture,
		Examples: []string{
			"gossms xevent capture --template batches --duration 60 --min-duration-ms 500 -p dev --allow admin",
			"gossms xevent capture --template errors --session-database Sales -p dev --allow admin -o jsonl",
			"gossms xevent capture --template deadlocks --duration 300 -p dev --allow admin --dry-run",
		},
	}
}

// captureRequest is xevent capture's options, checked before connecting.
type captureRequest struct {
	template              captureTemplate
	duration              time.Duration
	maxEvents             int
	database, login, prog string
	minDurationMs         int
	data                  bool // -o jsonl: the events set gains data
}

func parseCapture(inv *spec.Invocation) (captureRequest, error) {
	var c captureRequest
	name := inv.Values[optTemplate].(string)
	i := slices.IndexFunc(captureTemplates, func(t captureTemplate) bool { return t.name == name })
	if i < 0 {
		return c, usageError(fmt.Sprintf("--template: %q is not a template", name), "")
	}
	c.template = captureTemplates[i]
	secs := inv.Values[optDuration].(int)
	if secs < 1 || secs > maxCaptureSeconds {
		return c, usageError(fmt.Sprintf("--duration: %d is not between 1 and %d", secs, maxCaptureSeconds),
			"Give the seconds to capture for.")
	}
	c.duration = time.Duration(secs) * time.Second
	c.maxEvents = inv.Values[optMaxEvents].(int)
	if c.maxEvents < 1 || c.maxEvents > maxCaptureEvents {
		return c, usageError(fmt.Sprintf("--max-events: %d is not between 1 and %d", c.maxEvents, maxCaptureEvents), "")
	}
	c.minDurationMs = inv.Values[optMinDurationMs].(int)
	if c.minDurationMs < 0 {
		return c, usageError(fmt.Sprintf("--min-duration-ms: %d is negative", c.minDurationMs), "")
	}
	c.database, _ = inv.Values[optSessionDatabase].(string)
	c.login, _ = inv.Values[optLogin].(string)
	c.prog, _ = inv.Values[optProgram].(string)
	// Each name is a sysname, so a longer one matches nothing; past about
	// 2000 characters the server also refuses the CREATE's predicate with a
	// misleading syntax error (found live), so refuse it here.
	for _, f := range []struct{ opt, v string }{
		{optSessionDatabase, c.database}, {optLogin, c.login}, {optProgram, c.prog},
	} {
		if n := utf8.RuneCountInString(f.v); n > maxFilterNameChars {
			return c, usageError(fmt.Sprintf("--%s: %d characters, more than a name's %d", f.opt, n, maxFilterNameChars), "")
		}
	}
	format, _ := inv.Values["output"].(string)
	c.data = output.Format(format) == output.FormatJSONL
	return c, nil
}

// definition is the session to create: the template under name, a
// ring_buffer keeping the newest maxEvents, a one-second latency, never
// started with the server, and every event narrowed by the filters and
// kept off this command's own connections.
func (c captureRequest) definition(name string) (gosmo.EventSessionDefinition, error) {
	spec, ok := c.template.definition()
	if !ok {
		return spec, fmt.Errorf("gosmo has no %q event session template", c.template.gosmo)
	}
	spec.Name = name
	spec.MaxDispatchLatency = captureLatency
	spec.StartupState = false
	spec.MaxDuration = 0
	target := gosmo.RingBufferTarget(0)
	target.Fields = append(target.Fields, gosmo.SessionField{Name: "max_events_limit", Value: strconv.Itoa(c.maxEvents)})
	spec.Targets = []gosmo.SessionTarget{target}

	common := []string{"[sqlserver].[client_app_name]<>" + gosmo.QuoteLiteral(db.RoleCLI.ApplicationName())}
	if c.database != "" {
		common = append(common, "[sqlserver].[database_name]="+gosmo.QuoteLiteral(c.database))
	}
	if c.login != "" {
		common = append(common, "[sqlserver].[server_principal_name]="+gosmo.QuoteLiteral(c.login))
	}
	if c.prog != "" {
		common = append(common, "[sqlserver].[client_app_name]="+gosmo.QuoteLiteral(c.prog))
	}
	for i := range spec.Events {
		e := &spec.Events[i]
		e.Actions = slices.Clone(e.Actions)
		for _, a := range captureActions {
			if !slices.Contains(e.Actions, a) {
				e.Actions = append(e.Actions, a)
			}
		}
		terms := slices.Clone(common)
		if e.Name == "wait_info" {
			// Background tasks have no session, and is_system does not keep
			// them out: live on 17, the waits template was all session 0's
			// SOS_WORK_DISPATCHER.
			terms = append(terms, "[sqlserver].[session_id]>(0)")
		}
		if c.minDurationMs > 0 && slices.Contains(captureDurationEvents, e.Name) {
			terms = append(terms, fmt.Sprintf("[duration]>=(%d)", int64(c.minDurationMs)*1000))
		}
		if e.Predicate != "" {
			terms = slices.Insert(terms, 0, "("+e.Predicate+")")
		}
		e.Predicate = "(" + strings.Join(terms, " AND ") + ")"
	}
	return spec, nil
}

// captureSessionName is a fresh session name; a test replaces it.
var captureSessionName = func() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return captureSessionPrefix + time.Now().UTC().Format("20060102150405") + "_" + hex.EncodeToString(b)
}

// eventSessionRights are the server permissions a capture needs, one per
// verb: ALTER ANY EVENT SESSION covers all four, and from SQL Server 2022
// each also has its own, any of which permits that verb.
var eventSessionRights = []string{"CREATE ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE",
	"ALTER ANY EVENT SESSION DISABLE", "DROP ANY EVENT SESSION"}

// eventSessionDenied names the first verb caps knows the login may not use:
// ALTER ANY EVENT SESSION denied and the verb's own right not granted. A
// probe that failed knows nothing, and the server then decides.
func eventSessionDenied(caps *gosmo.Capabilities) string {
	if caps == nil || caps.Permission("ALTER ANY EVENT SESSION") != gosmo.CapabilityDenied {
		return ""
	}
	for _, r := range eventSessionRights {
		if caps.Permission(r) != gosmo.CapabilityGranted {
			return r
		}
	}
	return ""
}

const eventSessionHint = "Creating, starting, stopping and dropping an event session needs ALTER ANY EVENT " +
	"SESSION (from SQL Server 2022, CREATE, ALTER … ENABLE, ALTER … DISABLE and DROP ANY EVENT SESSION " +
	"do too). Use a login that has it, or have it granted."

func (d Deps) xeventCapture(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	c, err := parseCapture(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	if info := cat.ServerInfo(); info != nil && gosmo.EngineEdition(info.EngineEdition) == gosmo.EngineAzureSQLDatabase {
		return res, output.NewError(output.CodeUnsupported,
			"xevent capture creates a server-scoped event session, which Azure SQL Database does not have", "")
	}
	// The ring buffer is read from the DMVs, so a capture that could not
	// read it is refused before anything is created.
	ok, err := cat.HasViewServerState(ctx)
	if err == nil && !ok {
		err = viewServerStateDenied("xevent capture")
	}
	if err != nil {
		return res, readError(err)
	}
	caps, err := cat.ServerCapabilities(ctx)
	if err != nil {
		return res, readError(err)
	}
	if right := eventSessionDenied(caps); right != "" {
		return res, output.NewError(output.CodePermissionDenied,
			"xevent capture: the login lacks ALTER ANY EVENT SESSION and "+right, eventSessionHint)
	}

	name := captureSessionName()
	def, err := c.definition(name)
	if err != nil {
		return res, err
	}
	res.Fields = append(res.Fields, output.Field{Key: "template", Value: c.template.name},
		output.Field{Key: "session", Value: name},
		output.Field{Key: "durationSeconds", Value: strconv.Itoa(int(c.duration / time.Second))})

	if dryRun(inv) {
		return res, applyWrite(ctx, inv, res, func(ctx context.Context) error {
			es, err := w.CreateEventSession(ctx, def)
			if err != nil {
				return err
			}
			return errors.Join(w.StartEventSession(ctx, es), w.StopEventSession(ctx, es), w.DropEventSession(ctx, es))
		})
	}
	res.Fields = append(res.Fields, output.Field{Key: "dryRun", Value: "false"})
	data, err := runCapture(ctx, cat, w, def, c.duration+captureLatency, res)
	if err != nil {
		return res, err
	}
	events := slices.Clone(data.Events)
	slices.SortStableFunc(events, func(a, b gosmo.XEvent) int {
		return cmp.Or(a.Timestamp.Compare(b.Timestamp), cmp.Compare(a.Seq, b.Seq))
	})
	seen := max(data.TotalEventsProcessed, int64(len(events)))
	res.Fields = append(res.Fields, output.Field{Key: "eventsSeen", Value: strconv.FormatInt(seen, 10)},
		output.Field{Key: "eventsKept", Value: strconv.Itoa(len(events))})
	if seen > int64(len(events)) || data.Truncated {
		why := fmt.Sprintf("the ring buffer kept %d of the %d events seen, the newest", len(events), seen)
		if data.Truncated {
			why += ", its contents cut to fit about 4 MB"
		}
		res.Warnings = append(res.Warnings, why+": capture for a shorter --duration, narrow it with "+
			"--session-database, --login, --program or --min-duration-ms, or raise --max-events.")
	}
	res.Sets = []output.Set{captureEventsSet(events, c.data)}
	return res, nil
}

// runCapture creates the session, starts it, waits, and reads its ring
// buffer — then stops and drops it whatever happened, under a context of its
// own: a cancelled ctx (Ctrl+C) still removes the session. A drop that fails
// is a warning on res naming the session; a step's own error is returned
// after the cleanup.
func runCapture(ctx context.Context, cat Catalog, w Writer, def gosmo.EventSessionDefinition,
	wait time.Duration, res *output.Result) (*gosmo.RingBufferData, error) {
	es, err := w.CreateEventSession(ctx, def)
	if err != nil {
		// The CREATE may have run before the error (the read-back after
		// it, or a cancel that raced it), so look before giving up on it.
		cleanupCapture(ctx, cat, w, nil, def.Name, false, res)
		return nil, writeError(err)
	}
	started := false
	defer func() { cleanupCapture(ctx, cat, w, es, def.Name, started, res) }()
	if err := w.StartEventSession(ctx, es); err != nil {
		return nil, writeError(err)
	}
	started = true
	if err := sleepCtx(ctx, wait); err != nil {
		return nil, err
	}
	data, err := cat.ReadRingBuffer(ctx, es)
	if err != nil {
		return nil, readError(err)
	}
	return data, nil
}

// cleanupCapture stops (when it was started) and drops the capture's
// session. es nil is a CREATE that failed: the session is dropped only if
// the catalog shows it exists. The outcome is field sessionDropped, and a
// warning when the drop failed.
func cleanupCapture(parent context.Context, cat Catalog, w Writer, es *gosmo.EventSession, name string, started bool, res *output.Result) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), captureCleanupTimeout)
	defer cancel()
	if es == nil {
		found, err := cat.EventSessionByName(ctx, name)
		if errors.Is(err, gosmo.ErrNotFound) {
			return
		}
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("could not check whether event session %s was "+
				"created: %v. If xevent sessions lists it, drop it with DROP EVENT SESSION [%s] ON SERVER.", name, err, name))
			return
		}
		es, started = found, found.IsRunning
	}
	if started {
		// A drop needs no stop; the stop first just ends the collection
		// before the session goes.
		_ = w.StopEventSession(ctx, es)
	}
	err := w.DropEventSession(ctx, es)
	res.Fields = append(res.Fields, output.Field{Key: "sessionDropped", Value: strconv.FormatBool(err == nil)})
	if err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("event session %s was not dropped: %v. Drop it with "+
			"DROP EVENT SESSION [%s] ON SERVER.", name, err, name))
	}
}

// captureEventsSet is xevent capture's events set; data adds the column of
// every field and action as a JSON object.
func captureEventsSet(events []gosmo.XEvent, data bool) output.Set {
	set := output.Set{Name: "events", Columns: []output.Column{
		{Name: "time_utc", Type: "datetime"}, {Name: "event", Type: "nvarchar(128)"},
		{Name: "session_id", Type: "int", Nullable: true}, {Name: "database", Type: "nvarchar(128)", Nullable: true},
		{Name: "login", Type: "nvarchar(128)", Nullable: true}, {Name: "program", Type: "nvarchar(128)", Nullable: true},
		{Name: "host", Type: "nvarchar(128)", Nullable: true},
		{Name: "duration_ms", Type: "decimal(19,3)", Nullable: true}, {Name: "cpu_ms", Type: "decimal(19,3)", Nullable: true},
		{Name: "logical_reads", Type: "bigint", Nullable: true}, {Name: "writes", Type: "bigint", Nullable: true},
		{Name: "row_count", Type: "bigint", Nullable: true}, {Name: "result", Type: "nvarchar(32)", Nullable: true},
		{Name: "error", Type: "int", Nullable: true}, {Name: "severity", Type: "int", Nullable: true},
		{Name: "text", Type: "nvarchar(max)", Nullable: true},
	}}
	if data {
		set.Columns = append(set.Columns, output.Column{Name: "data", Type: output.TypeJSON})
	}
	for _, e := range events {
		duration := microsCell(e, "duration")
		if e.Name == "wait_info" {
			// wait_info's duration is milliseconds.
			duration = numberCell(e, "duration")
		}
		row := []*string{
			ptr(gridTime(e.Timestamp.UTC())), ptr(e.Name),
			actionOrField(e, "session_id"), textOrNull(eventText(e, "database_name")),
			textOrNull(cmp.Or(eventText(e, "server_principal_name"), eventText(e, "username"), eventText(e, "nt_username"))),
			textOrNull(eventText(e, "client_app_name")), textOrNull(eventText(e, "client_hostname")),
			duration, microsCell(e, "cpu_time"),
			numberCell(e, "logical_reads"), numberCell(e, "writes"), numberCell(e, "row_count"),
			textOrNull(fieldText(e, "result")), numberCell(e, "error_number"), numberCell(e, "severity"),
			textOrNull(captureText(e)),
		}
		if data {
			row = append(row, ptr(eventData(e)))
		}
		set.Rows = append(set.Rows, row)
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// fieldText is a field's text: a map value's text, else its value.
func fieldText(e gosmo.XEvent, name string) string {
	v, ok := e.Field(name)
	if !ok {
		return ""
	}
	return cmp.Or(v.Text, v.Value)
}

// eventText is an action's value, else a field's of the same name (a login
// event carries database_name as a field).
func eventText(e gosmo.XEvent, name string) string {
	if v, ok := e.Action(name); ok && v.Value != "" {
		return cmp.Or(v.Text, v.Value)
	}
	return fieldText(e, name)
}

// actionOrField is eventText as a number cell: NULL when absent or not a
// whole number.
func actionOrField(e gosmo.XEvent, name string) *string {
	s := eventText(e, name)
	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return nil
	}
	return ptr(s)
}

// numberCell is a whole-number field: NULL when absent or not one.
func numberCell(e gosmo.XEvent, name string) *string {
	v, ok := e.Field(name)
	if !ok {
		return nil
	}
	if _, err := strconv.ParseInt(v.Value, 10, 64); err != nil {
		if _, err := strconv.ParseUint(v.Value, 10, 64); err != nil {
			return nil
		}
	}
	return ptr(v.Value)
}

// microsCell is a microseconds field as milliseconds with three decimals.
func microsCell(e gosmo.XEvent, name string) *string {
	v, ok := e.Field(name)
	if !ok {
		return nil
	}
	us, err := strconv.ParseUint(v.Value, 10, 64)
	if err != nil {
		return nil
	}
	return microsMs(float64(us))
}

// captureText is the events set's text: the batch or statement, an error's
// message, the sql_text action, a deadlock's graph, a wait's type.
func captureText(e gosmo.XEvent) string {
	for _, f := range []string{"batch_text", "statement", "message", "xml_report"} {
		if s := fieldText(e, f); s != "" {
			return s
		}
	}
	if v, ok := e.Action("sql_text"); ok && v.Value != "" {
		return v.Value
	}
	return fieldText(e, "wait_type")
}

// eventData is data's object: {"fields": {…}, "actions": {…}}, each value
// its text (a map value's text) or, for an XML field, its markup.
func eventData(e gosmo.XEvent) string {
	obj := func(vs []gosmo.XEValue) map[string]string {
		m := make(map[string]string, len(vs))
		for _, v := range vs {
			m[v.Name] = cmp.Or(v.Text, v.Value)
		}
		return m
	}
	b, _ := json.Marshal(struct {
		Fields  map[string]string `json:"fields"`
		Actions map[string]string `json:"actions"`
	}{obj(e.Fields), obj(e.Actions)})
	return string(b)
}
