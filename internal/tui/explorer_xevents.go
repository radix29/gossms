package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_xevents.go is Management ▸ Extended Events: the Sessions folder, one
// node per server-scoped event session, one leaf per target, and the state
// verbs on a session; New Session and Session Properties are
// xevent_session_dialog.go. Script Session as and Delete come from scripting.go's
// and explorer_object_ops.go's tables; the Details pane's grids are in
// detail_browser_xevents.go; the XEvent Profiler folder beside Sessions is
// xevent_profiler.go.
//
// Azure SQL Database has no server-scoped sessions: its sessions belong to a
// database (ON DATABASE), and the folder hangs off each database node there
// instead of Management, as SSMS hangs it. The same node types serve both;
// a node's DBName is the scope — empty for the server — and xeScope turns it
// into the gosmo handle and the right every verb asks for.

// xeScope is where an event session lives: the server (db empty), or one
// database — Azure SQL Database's database-scoped sessions.
type xeScope struct{ db string }

// xeScopeOf is the scope of an Extended Events node.
func xeScopeOf(n nodeData) xeScope { return xeScope{db: n.DBName} }

// ref is a lookup-free handle on the session named name in this scope.
func (s xeScope) ref(sc *db.ServerConn, name string) *gosmo.EventSession {
	if s.db != "" {
		return sc.Server.DatabaseRef(s.db).EventSessionRef(name)
	}
	return sc.Server.EventSessionRef(name)
}

// byName reads the session named name in this scope.
func (s xeScope) byName(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.EventSession, error) {
	if s.db != "" {
		return sc.Server.DatabaseRef(s.db).EventSessionByName(ctx, name)
	}
	return sc.Server.EventSessionByName(ctx, name)
}

// list reads every session of this scope.
func (s xeScope) list(ctx context.Context, sc *db.ServerConn) ([]*gosmo.EventSession, error) {
	if s.db != "" {
		return sc.Server.DatabaseRef(s.db).EventSessions(ctx)
	}
	return sc.Server.EventSessions(ctx)
}

// create writes CREATE EVENT SESSION in this scope.
func (s xeScope) create(ctx context.Context, sc *db.ServerConn, spec gosmo.EventSessionSpec) (*gosmo.EventSession, error) {
	if s.db != "" {
		return sc.Server.DatabaseRef(s.db).CreateEventSession(ctx, spec)
	}
	return sc.Server.CreateEventSession(ctx, spec)
}

// right is the right a verb needs in this scope: r itself on the server, and
// ALTER ANY DATABASE EVENT SESSION in a database, which Azure SQL Database
// has not split per verb.
func (s xeScope) right(r gate.Right) gate.Right {
	if s.db != "" {
		return gate.DatabaseEventSession
	}
	return r
}

// label names the scope in a dialog's subtitle.
func (s xeScope) label(sc *db.ServerConn) string {
	if s.db != "" {
		return "Database: " + s.db
	}
	return "Server: " + sc.Opts.Server
}

// databaseScopedXEvents reports whether sc's sessions are database-scoped —
// an Azure SQL Database, where sys.server_event_sessions does not exist and
// each database has an Extended Events folder of its own.
func databaseScopedXEvents(sc *db.ServerConn) bool {
	return sc != nil && sc.Server != nil && sc.Server.Info() != nil &&
		gosmo.EngineEdition(sc.Server.Info().EngineEdition) == gosmo.EngineAzureSQLDatabase
}

// refreshXESessions reloads scope's Sessions folder on sc, where the tree has
// it loaded. The database is part of the match: on Azure SQL Database every
// database has a Sessions folder of its own.
func (a *App) refreshXESessions(sc *db.ServerConn, scope xeScope) {
	a.explorer.ReloadFolders(sc, folderOf(scope.db, NodeEventSessions))
}

// refreshXESession reloads the node of scope's session name on sc — its
// targets, and its Details view (events then targets) — where the tree lists
// it. Reload fetches only an expanded node, so a collapsed one is just marked
// for a fresh read when it opens.
func (a *App) refreshXESession(sc *db.ServerConn, scope xeScope, name string) {
	a.explorer.ReloadFolders(sc, func(d nodeData) bool {
		return d.Type == NodeEventSession && d.Name == name && d.DBName == scope.db
	})
}

// loadExtendedEventsChildren returns the Extended Events folder's children:
// Sessions, and XEvent Profiler (xevent_profiler.go). SSMS hangs XEvent
// Profiler off the server node instead; here it sits with the sessions it
// creates.
//
// A database's folder (Azure SQL Database) holds Sessions alone: the Profiler
// creates server-scoped sessions.
func loadExtendedEventsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	if dbName := node.data.DBName; dbName != "" {
		return []*explorerNode{l.node("Sessions", NodeEventSessions, "", "", dbName)}, nil
	}
	return []*explorerNode{
		l.node("Sessions", NodeEventSessions, "", "", ""),
		l.node("XEvent Profiler", NodeXEventProfiler, "", "", ""),
	}, nil
}

// loadEventSessionsChildren lists every event session of the folder's scope,
// the ones SQL Server creates for itself (system_health and its siblings)
// included, as SSMS lists them. Running or stopped is IsEnabled, which
// nodeIcon draws.
func loadEventSessionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	scope := xeScopeOf(node.data)
	return listChildren(func() ([]*gosmo.EventSession, error) { return scope.list(l.ctx, l.sc) },
		func(es *gosmo.EventSession) *explorerNode {
			n := l.node(es.Name, NodeEventSession, "", es.Name, scope.db)
			n.data.IsEnabled = es.IsRunning
			return n
		})
}

// loadEventSessionChildren lists one session's targets, labelled by their
// qualified name (package0.event_file) as SSMS labels them. The session is
// read by name rather than carried from the folder's listing: a Refresh on
// the session node has to see a target added since.
func loadEventSessionChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	es, err := xeScopeOf(node.data).byName(l.ctx, l.sc, node.data.Name)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]gosmo.SessionTarget, error) { return es.Targets, nil },
		func(t gosmo.SessionTarget) *explorerNode {
			n := l.node(t.QualifiedName(), NodeEventTarget, "", t.Name, node.data.DBName)
			n.data.XESession = es.Name
			return n
		})
}

// builtInEventSessions are the sessions SQL Server (Setup, the Always On
// wizard, the telemetry service) creates for itself. They are listed, started,
// stopped and scripted like any other — SSMS does the same — but Delete asks
// for the name to be typed: system_health is what most post-mortems are read
// from, and nothing in goSSMS can put it back.
var builtInEventSessions = []string{"system_health", "AlwaysOn_health", "telemetry_xevents"}

// isBuiltInEventSession reports whether name is one of builtInEventSessions.
// Case-insensitive, as the catalog's collation usually is.
func isBuiltInEventSession(name string) bool {
	return slices.ContainsFunc(builtInEventSessions, func(b string) bool { return strings.EqualFold(b, name) })
}

// eventSessionsMenuItems is the Sessions folder's menu: New Session.
func eventSessionsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Session...", Action: func() { a.showNewXESessionDialog(sc, xeScopeOf(node.data)) }},
			sc, node.data.DBName, xeScopeOf(node.data).right(gate.EventSessionCreate)),
	)
}

// eventSessionMenuItems is a session's own menu. Script Session as and Delete
// are spliced in by contextMenuItemsForNode.
//
// Start and Stop are both listed, each enabled in the one state it applies
// to, rather than one toggling item: SSMS lists both, and an item whose label
// flips with the icon is one the user has to read twice.
func eventSessionMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	scope := xeScopeOf(node.data)
	return []controls.MenuItem{
		// SSMS enables Watch Live Data only on a running session: a stopped
		// one has nothing arriving to watch. Its targets' View Target Data
		// reads what an event_file kept.
		{Label: "Watch Live Data",
			Enabled: func() bool { return node.data.IsEnabled },
			Note:    "the session is stopped",
			Action:  func() { a.showXEventViewerFor(sc, scope, node.data.Name, "", true) }},
		{Divider: true},
		newQuery,
		{Divider: true},
		gate.Item(controls.MenuItem{Label: "Start Session",
			Enabled: func() bool { return !node.data.IsEnabled },
			Action:  func() { a.setEventSessionState(sc, node, true) }},
			sc, scope.db, scope.right(gate.EventSessionStart)),
		gate.Item(controls.MenuItem{Label: "Stop Session",
			Enabled: func() bool { return node.data.IsEnabled },
			Action:  func() { a.setEventSessionState(sc, node, false) }},
			sc, scope.db, scope.right(gate.EventSessionStop)),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() { a.showEventSessionPropertiesFor(sc, scope, node.data.Name) }},
	}
}

// eventTargetMenuItems is a target leaf's menu: View Target Data, for the two
// targets that hold events. The others (histogram, pair_matching,
// event_counter) keep aggregates in a shape of their own, which the viewer
// does not read yet.
func eventTargetMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		{Label: "View Target Data",
			Enabled: func() bool { return isViewableEventTarget(node.data.Name) },
			Note:    "only event_file and ring_buffer",
			Action:  func() { a.showXEventTargetData(sc, node) }},
		{Divider: true},
		newQuery,
		{Divider: true},
		refresh,
	}
}

// isViewableEventTarget reports whether View Target Data can read a target.
func isViewableEventTarget(name string) bool {
	return name == gosmo.XETargetEventFile || name == gosmo.XETargetRingBuffer
}

// showXEventTargetData opens View Target Data on a target leaf — its menu and
// its Enter.
func (a *App) showXEventTargetData(sc *db.ServerConn, node *explorerNode) {
	if !isViewableEventTarget(node.data.Name) {
		a.setStatus("View Target Data reads only event_file and ring_buffer targets")
		return
	}
	a.showXEventViewerFor(sc, xeScopeOf(node.data), node.data.XESession, node.data.Name, false)
}

// setEventSessionState starts or stops node's session. Stopping asks first: a
// ring_buffer, histogram or pair_matching target keeps its data in memory, and
// the stop discards it — the one part of the session that cannot be got back
// by starting it again.
func (a *App) setEventSessionState(sc *db.ServerConn, node *explorerNode, start bool) {
	if !a.requireConn(sc) {
		return
	}
	name := node.data.Name
	verb, doing, done := "Stop", "Stopping", "stopped"
	if start {
		verb, doing, done = "Start", "Starting", "started"
	}

	run := func() {
		a.runWithProgress(progressJob{
			title:   verb + " Event Session",
			message: fmt.Sprintf("%s event session %q...", doing, name),
			what:    strings.ToLower(doing) + " an event session",
			sc:      sc,
		}, func(ctx context.Context, _ progressReport) error {
			es := xeScopeOf(node.data).ref(sc, name)
			if start {
				return es.Start(ctx)
			}
			return es.Stop(ctx)
		}, func(err error, cancelled bool) {
			// The folder, not the node: the icon is drawn from IsEnabled, which
			// the folder's listing sets.
			reload := func() { a.explorer.ReloadFolders(sc, sameNodeAs(node.parent)) }
			switch {
			case cancelled:
				a.setStatus(fmt.Sprintf("%s of %q cancelled", verb, name))
				reload()
			case err != nil:
				a.setStatus(fmt.Sprintf("Failed to %s %q: %v", strings.ToLower(verb), name, withPermissionAdvice(err)))
			default:
				node.data.IsEnabled = start
				reload()
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("Event session %q %s", name, done))
			}
		})
	}

	if start {
		run()
		return
	}
	a.confirmDialog.ShowConfirm("Stop Event Session",
		fmt.Sprintf("Stop %s? It stops collecting, and what a ring_buffer, histogram or pair_matching target holds in memory is discarded. event_file targets keep their files.", name),
		func(confirmed bool) {
			if confirmed {
				run()
			}
		})
}
