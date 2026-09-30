package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_xevents.go is the Details pane for Management ▸ Extended
// Events: the Sessions folder, one session's events and targets, and one
// target's settings beside its runtime counters.

// eventSessionsFolderDetail lists every event session of the folder's scope
// (the server's, or an Azure SQL Database's own — see xeScope). It reads
// gosmo independently of the tree, so the folder's filter is applied here too —
// over the gosmo objects, before the rows are built.
func eventSessionsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	scope := xeScopeOf(node.data)
	sessions, err := scope.list(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	sessions = filterObjects(node.data.Filter, sessions, func(es *gosmo.EventSession) nodeData {
		return nodeData{Name: es.Name}
	})

	rows := make([][]string, 0, len(sessions))
	out := make([]nodeData, 0, len(sessions))
	for _, es := range sessions {
		targets := make([]string, 0, len(es.Targets))
		for _, t := range es.Targets {
			targets = append(targets, t.Name)
		}
		// Blank for a stopped session rather than "0": it has no counter at
		// all, and a zero reads as "running and losing nothing".
		dropped := ""
		if es.IsRunning {
			dropped = strconv.FormatInt(es.DroppedEvents, 10)
		}
		rows = append(rows, []string{
			es.Name, eventSessionStateText(es.IsRunning), yesNo(es.StartupState),
			strconv.Itoa(len(es.Events)), strings.Join(targets, ", "), dropped,
		})
		out = append(out, nodeData{Type: NodeEventSession, Name: es.Name, DBName: scope.db, IsEnabled: es.IsRunning})
	}
	*objs = out
	return []string{"Name", "State", "Startup", "Events", "Targets", "Dropped"}, rows, nil
}

// eventSessionDetail is one session's events and targets, one row each. The
// session's own options are the Properties dialog's; this grid is what the
// session collects and where it goes.
func eventSessionDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	es, err := xeScopeOf(node.data).byName(ctx, sc, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(es.Events)+len(es.Targets))
	for _, e := range es.Events {
		rows = append(rows, []string{
			"Event", e.QualifiedName(), strings.Join(e.Actions, ", "),
			// A predicate is often written across lines; the grid holds one.
			strings.Join(strings.Fields(e.Predicate), " "), sessionFieldsText(e.Fields),
		})
	}
	for _, t := range es.Targets {
		rows = append(rows, []string{"Target", t.QualifiedName(), "", "", sessionFieldsText(t.Fields)})
	}
	return []string{"Type", "Name", "Actions", "Predicate", "Settings"}, rows, nil
}

// eventTargetDetail is one target's settings, then — while its session runs —
// what sys.dm_xe_session_targets says about it.
func eventTargetDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	es, err := xeScopeOf(node.data).byName(ctx, sc, node.data.XESession)
	if err != nil {
		return nil, nil, err
	}
	t, ok := es.Target(node.data.Name)
	if !ok {
		return nil, nil, errors.New("target " + node.data.Name + " is no longer on session " + es.Name + " — refresh the session")
	}
	pairs := []string{
		"Session", es.Name,
		"Target", t.QualifiedName(),
		"Session state", eventSessionStateText(es.IsRunning),
	}
	for _, f := range t.Fields {
		pairs = append(pairs, f.Name, f.Value)
	}
	if es.IsRunning {
		st, err := es.Status(ctx)
		// Not found is a session stopped between the two reads; the settings
		// above still stand, so the counters are left off rather than the
		// view failed.
		if err != nil && !errors.Is(err, gosmo.ErrNotFound) {
			return nil, nil, err
		}
		if st != nil {
			pairs = append(pairs, eventTargetStatusPairs(st, t)...)
		}
	}
	return propertyRows(pairs...)
}

// eventTargetStatusPairs is the runtime half of eventTargetDetail: the
// counters the target reports, for the target named t. A counter the target
// kind doesn't report (a ring_buffer's event count on an event_file) is left
// out rather than shown as 0.
func eventTargetStatusPairs(st *gosmo.EventSessionStatus, t gosmo.SessionTarget) []string {
	for _, ts := range st.Targets {
		if ts.Name != t.Name {
			continue
		}
		pairs := []string{"Execution count", strconv.FormatInt(ts.ExecutionCount, 10)}
		switch t.Name {
		case gosmo.XETargetEventFile:
			pairs = append(pairs, "Current file", ts.CurrentFile)
		case gosmo.XETargetRingBuffer:
			pairs = append(pairs,
				"Events processed", strconv.FormatInt(ts.TotalEventsProcessed, 10),
				"Events in buffer", strconv.FormatInt(ts.EventCount, 10),
				"Events dropped", strconv.FormatInt(ts.DroppedCount, 10),
				"Output truncated", yesNo(ts.Truncated))
		}
		return pairs
	}
	return nil
}

// sessionFieldsText renders an event's or target's SET list the way the
// CREATE statement spells it, name=value.
func sessionFieldsText(fields []gosmo.SessionField) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f.Name+"="+f.Value)
	}
	return strings.Join(parts, ", ")
}

// eventSessionStateText names a session's running state.
func eventSessionStateText(running bool) string {
	if running {
		return "Running"
	}
	return "Stopped"
}
