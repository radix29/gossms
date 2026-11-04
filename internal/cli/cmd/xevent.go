package cmd

import (
	"context"
	"errors"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// XEventCommands is the xevent group: its node and its commands. Its summary
// names only what exists.
func XEventCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"xevent"},
			Summary: "List the server's Extended Events sessions, or capture events for a while",
			Class:   spec.ClassRead,
		},
		xeventSessionsCommand(d),
		xeventCaptureCommand(d),
	}
}

// --- xevent sessions --------------------------------------------------------

func xeventSessionsCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"xevent", "sessions"},
		Summary: "List the Extended Events sessions: running or not, started with the server, their targets and events",
		Description: "One row per event session defined on the server (sys.server_event_sessions), by name; on " +
			"Azure SQL Database the database's own sessions (sys.database_event_sessions), scope database. " +
			"The engine's private sessions that have no definition (hkenginexesession, sp_server_diagnostics " +
			"session) are not listed. running is whether the session is started now, startup whether it starts " +
			"with the server. started (server local time) and dropped_events — the events it could not buffer " +
			"since it started — are a running session's, NULL for a stopped one. targets and events are the " +
			"session's target and event names. file is the event_file target's file: the .xel being written " +
			"when the session runs, else the filename the target was given (a relative one lands in the " +
			"server's log directory); NULL without an event_file target. Needs VIEW SERVER STATE (VIEW SERVER " +
			"PERFORMANCE STATE from SQL Server 2022): without it, exit 9.",
		Options: ConnectOptions(),
		Class:   spec.ClassRead,
		Run:     d.xeventSessions,
		Examples: []string{
			"gossms xevent sessions -p prod-ro",
			"gossms xevent sessions -p dev -o json",
		},
	}
}

// xeSession is one row of xevent sessions: the session's definition and, for
// a running one, its runtime state.
type xeSession struct {
	*gosmo.EventSession
	Status *gosmo.EventSessionStatus // nil: not running
}

func (d Deps) xeventSessions(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	// Azure SQL Database's sessions are the database's, and VIEW SERVER
	// STATE does not exist there: its views need VIEW DATABASE STATE, which a
	// refused read reports.
	var scopeDB *gosmo.Database
	scope := "server"
	if info := cat.ServerInfo(); info != nil && gosmo.EngineEdition(info.EngineEdition) == gosmo.EngineAzureSQLDatabase {
		if scopeDB, err = currentDatabase(ctx, cat, res); err != nil {
			return res, err
		}
		scope = "database"
	} else {
		// Without it the session catalog is refused or reads empty, which
		// would say the server has no sessions.
		ok, err := cat.HasViewServerState(ctx)
		if err == nil && !ok {
			err = viewServerStateDenied("xevent sessions")
		}
		if err != nil {
			return res, readError(err)
		}
	}
	list, err := cat.EventSessions(ctx, scopeDB)
	if err != nil {
		return res, readError(err)
	}
	rows := make([]xeSession, 0, len(list))
	for _, es := range list {
		row := xeSession{EventSession: es}
		if es.IsRunning {
			st, err := cat.EventSessionStatus(ctx, es)
			switch {
			case errors.Is(err, gosmo.ErrNotFound):
				// Stopped between the two reads.
				es.IsRunning = false
			case err != nil:
				return res, readError(err)
			default:
				row.Status = st
			}
		}
		rows = append(rows, row)
	}
	res.Sets = []output.Set{xeSessionsSet(rows, scope)}
	return res, nil
}

// xeSessionsSet is xevent sessions' sessions set.
func xeSessionsSet(rows []xeSession, scope string) output.Set {
	set := output.Set{Name: "sessions", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(128)"}, {Name: "scope", Type: "varchar(8)"},
		{Name: "running", Type: "bit"}, {Name: "startup", Type: "bit"},
		{Name: "started", Type: "datetime", Nullable: true}, {Name: "dropped_events", Type: "bigint", Nullable: true},
		{Name: "targets", Type: "nvarchar(max)", Nullable: true}, {Name: "events", Type: "nvarchar(max)", Nullable: true},
		{Name: "file", Type: "nvarchar(4000)", Nullable: true},
	}}
	for _, r := range rows {
		var targets, events []string
		for _, t := range r.Targets {
			targets = append(targets, t.Name)
		}
		for _, e := range r.Events {
			events = append(events, e.Name)
		}
		var started, dropped *string
		if r.Status != nil {
			started, dropped = ptr(gridTime(r.Status.CreateTime)), int64Cell(r.Status.DroppedEvents)
		}
		set.Rows = append(set.Rows, []*string{
			ptr(r.Name), ptr(scope), bitCell(r.IsRunning), bitCell(r.StartupState), started, dropped,
			listCell(targets), listCell(events), textOrNull(xeSessionFile(r)),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// xeSessionFile is the session's event_file: the file a running session
// writes, else its target's filename parameter; empty without one.
func xeSessionFile(r xeSession) string {
	if r.Status != nil {
		for _, t := range r.Status.Targets {
			if t.Name == gosmo.XETargetEventFile && t.CurrentFile != "" {
				return t.CurrentFile
			}
		}
	}
	if t, ok := r.Target(gosmo.XETargetEventFile); ok {
		name, _ := t.Field("filename")
		return name
	}
	return ""
}
