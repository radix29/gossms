package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// xevent_profiler.go is SSMS's XEvent Profiler: one command from nothing to a
// live trace. Launch Session creates the Standard or TSQL session if the
// server does not have it, starts it if it is stopped, and opens Watch Live
// Data on it. The entry points are the XEvent Profiler folder under
// Management ▸ Extended Events (its leaves' menu and Enter), Tools ▸ XEvent
// Profiler, and Alt+P.
//
// The sessions are gossms's own copies of SSMS's (docs/decisions.md
// § Extended Events): named gossms_QuickSession*, not QuickSession*, and
// with a target. SSMS creates its copies with no target — it reads the live
// stream, which goSSMS does not — so reusing them would leave nothing to
// read on any server where SSMS's Profiler has run, and altering them would
// change a session that is not ours. An existing gossms copy is reused as it
// is: whatever the user changed in it stays changed.

// xeProfilerKind is one XEvent Profiler template.
type xeProfilerKind struct {
	label    string // the leaf's label and the Tools submenu's item
	session  string // the session gossms creates for it
	template func() gosmo.EventSessionSpec
}

var xeProfilerKinds = []xeProfilerKind{
	{"Standard", "gossms_QuickSessionStandard", gosmo.XEProfilerStandard},
	{"TSQL", "gossms_QuickSessionTSQL", gosmo.XEProfilerTSQL},
}

// xeProfilerKindNamed returns the kind a leaf is labelled with.
func xeProfilerKindNamed(label string) (xeProfilerKind, bool) {
	i := slices.IndexFunc(xeProfilerKinds, func(k xeProfilerKind) bool { return k.label == label })
	if i < 0 {
		return xeProfilerKind{}, false
	}
	return xeProfilerKinds[i], true
}

const (
	// xeProfilerLatency is the sessions' dispatch latency. SSMS uses 5 s, for
	// a stream; with the viewer's one-second poll on top, 3 s keeps a query
	// run in another window arriving within a few seconds.
	xeProfilerLatency = 3 * time.Second

	// The event_file target's rollover: a Profiler trace is a look at what is
	// happening now, not an archive, so four 20 MB files.
	xeProfilerFileMB    = 20
	xeProfilerFileCount = 4
)

// spec is the session to create for k: the template under gossms's name, with
// a target the viewer reads. A Managed Instance refuses an event_file without
// a blob URL (Msg 40538), so there it is a ring_buffer — Phase G's default,
// pulled forward.
func (k xeProfilerKind) spec(azure bool) gosmo.EventSessionSpec {
	spec := k.template()
	spec.Name = k.session
	spec.MaxDispatchLatency = xeProfilerLatency
	// The ring_buffer is deduped on event_sequence (D1); SSMS's templates
	// already collect it, and a copy that did not would dedupe by content.
	//
	// The viewer's own connection is left out of every event: its poll is an
	// RPC a second, and SSMS's templates, written for a stream that makes
	// none, would fill the trace with the viewer watching itself.
	notViewer := "[sqlserver].[client_app_name]<>" + gosmo.QuoteLiteral(db.RoleXEventProfiler.ApplicationName())
	for i := range spec.Events {
		e := &spec.Events[i]
		if !slices.Contains(e.Actions, "package0.event_sequence") {
			e.Actions = append(e.Actions, "package0.event_sequence")
		}
		if e.Predicate == "" {
			e.Predicate = "(" + notViewer + ")"
		} else {
			e.Predicate = "(" + e.Predicate + " AND " + notViewer + ")"
		}
	}
	if azure {
		spec.Targets = []gosmo.SessionTarget{gosmo.RingBufferTarget(0)}
	} else {
		spec.Targets = []gosmo.SessionTarget{gosmo.EventFileTarget(k.session, xeProfilerFileMB, xeProfilerFileCount)}
	}
	return spec
}

// loadXEventProfilerChildren lists the two templates.
func loadXEventProfilerChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	out := make([]*explorerNode, len(xeProfilerKinds))
	for i, k := range xeProfilerKinds {
		out[i] = l.node(k.label, NodeXEventProfilerSession, "", k.label, "")
	}
	return out, nil
}

// xeventProfilerMenuItems is a template leaf's menu: Launch Session.
func xeventProfilerMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		gate.ItemOnAll(controls.MenuItem{Label: "Launch Session",
			Action: func() { a.launchXEventProfilerNamed(sc, node.data.Name) }},
			sc, "", "", "", xeProfilerRights...),
	}
}

// xeProfilerRights are what Launch Session needs: to create the session and
// to start it — both, since a launch does both. On 2016–2019 ALTER ANY EVENT
// SESSION is both; 2022 splits them into CREATE ANY EVENT SESSION and ALTER
// ANY EVENT SESSION ENABLE, and a login holding only the first created the
// session and was then refused the start (found live on major 17), leaving a
// stopped session behind.
//
// Asked even of a session that already exists and runs, which needs neither:
// which case applies is not known until the server is asked, and a login
// that can only watch has Watch Live Data on the session itself.
var xeProfilerRights = [][]gate.Right{{gate.EventSessionCreate}, {gate.EventSessionStart}}

// xeProfilerAllowed reports whether sc's login may launch a Profiler session.
// Never on Azure SQL Database, which has no server-scoped sessions to create.
func xeProfilerAllowed(sc *db.ServerConn) bool {
	return !databaseScopedXEvents(sc) && gate.AllowsAllOn(sc, "", "", "", xeProfilerRights...)
}

// xeProfilerNeedsRight is the status line when the gate refuses.
const xeProfilerNeedsRight = "XEvent Profiler needs ALTER ANY EVENT SESSION"

// xeProfilerNoServerScope is the status line on Azure SQL Database.
const xeProfilerNoServerScope = "XEvent Profiler creates a server-scoped session, which Azure SQL Database does not have — create one under the database's Extended Events folder instead"

// launchXEventProfilerNamed is a leaf's Enter and Launch Session.
func (a *App) launchXEventProfilerNamed(sc *db.ServerConn, label string) {
	k, ok := xeProfilerKindNamed(label)
	if !ok {
		return
	}
	a.launchXEventProfiler(sc, k)
}

// launchXEventProfilerActive is Tools ▸ XEvent Profiler and Alt+P: the
// template on the connection Object Explorer has selected.
func (a *App) launchXEventProfilerActive(k xeProfilerKind) {
	if sc := a.connOrFirst(); sc != nil {
		a.launchXEventProfiler(sc, k)
	}
}

// launchXEventProfiler creates k's session on sc if it is missing, starts it
// if it is stopped, and opens Watch Live Data on it. The session is left
// running when the viewer closes unless the user says otherwise there, as
// SSMS leaves it.
func (a *App) launchXEventProfiler(sc *db.ServerConn, k xeProfilerKind) {
	if !a.requireConn(sc) {
		return
	}
	if databaseScopedXEvents(sc) {
		a.setStatus(xeProfilerNoServerScope)
		return
	}
	if !xeProfilerAllowed(sc) {
		a.setStatus(xeProfilerNeedsRight)
		return
	}
	azure := sc.Server.Info().IsAzure()
	var changed bool // created or started: the Sessions folder is stale
	a.runWithProgress(progressJob{
		title:   "XEvent Profiler",
		message: fmt.Sprintf("Starting event session %q...", k.session),
		what:    "launching the XEvent Profiler",
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		es, err := sc.Server.EventSessionByName(ctx, k.session)
		if errors.Is(err, gosmo.ErrNotFound) {
			es, err = sc.Server.CreateEventSession(ctx, k.spec(azure))
			changed = err == nil
		}
		if err != nil {
			return err
		}
		if es.IsRunning {
			return nil
		}
		changed = true
		return es.Start(ctx)
	}, func(err error, cancelled bool) {
		if changed || cancelled {
			a.explorer.ReloadFolders(sc, folderOf("", NodeEventSessions))
		}
		switch {
		case cancelled:
			a.setStatus("XEvent Profiler cancelled")
		case err != nil:
			a.setStatus(fmt.Sprintf("Failed to launch %s: %v", k.session, withPermissionAdvice(err)))
		default:
			a.openXEventViewer(sc, xeScope{}, k.session, "", true, true)
		}
	})
}

// xeProfilerMenu is Tools ▸ XEvent Profiler.
func (a *App) xeProfilerMenu() controls.MenuItem {
	var sub []controls.MenuItem
	for i, k := range xeProfilerKinds {
		item := controls.MenuItem{Label: k.label, Action: func() { a.launchXEventProfilerActive(k) },
			Enabled: func() bool { return len(a.connections) > 0 && xeProfilerAllowed(a.activeServerConn()) },
			Note:    "needs ALTER ANY EVENT SESSION",
			NoteWhen: func() bool {
				sc := a.activeServerConn()
				return len(a.connections) > 0 && !databaseScopedXEvents(sc) && !xeProfilerAllowed(sc)
			}}
		if i == 0 {
			item.Shortcut = "Alt+P"
		}
		sub = append(sub, item)
	}
	return controls.MenuItem{Label: "XEvent Profiler", Sub: sub}
}

// requestCloseXEventViewer closes an Extended Events viewer. One the XEvent
// Profiler opened first asks whether to stop the session too — No by default
// and on Escape, since SSMS leaves the session running and the next launch
// picks it up again. Asked only where the question means something: the
// viewer got as far as connecting, and the login may stop the session.
func (a *App) requestCloseXEventViewer(v *XEventViewer) {
	host := v.host
	if !v.profiler || v.conn == nil || !a.isConnected(host) || !gate.Allows(host, "", gate.EventSessionStop) {
		a.closePanelByPointer(v)
		return
	}
	a.confirmDialog.ShowConfirmDefaultNo("Stop Event Session",
		fmt.Sprintf("Stop the event session %s too? The XEvent Profiler started it; left running, it goes on collecting until it is stopped.", v.session),
		func(stop bool) {
			a.closePanelByPointer(v)
			if stop {
				a.stopXEventProfilerSession(host, v.session)
			}
		})
}

// stopXEventProfilerSession stops the session a closed Profiler viewer was
// reading.
func (a *App) stopXEventProfilerSession(sc *db.ServerConn, session string) {
	if !a.requireConn(sc) {
		return
	}
	a.runWithProgress(progressJob{
		title:   "Stop Event Session",
		message: fmt.Sprintf("Stopping event session %q...", session),
		what:    "stopping an XEvent Profiler session",
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		return sc.Server.EventSessionRef(session).Stop(ctx)
	}, func(err error, cancelled bool) {
		a.explorer.ReloadFolders(sc, folderOf("", NodeEventSessions))
		switch {
		case cancelled:
			a.setStatus(fmt.Sprintf("Stop of %q cancelled", session))
		case err != nil:
			a.setStatus(fmt.Sprintf("Failed to stop %q: %v", session, withPermissionAdvice(err)))
		default:
			a.setStatus(fmt.Sprintf("Event session %q stopped", session))
		}
	})
}
