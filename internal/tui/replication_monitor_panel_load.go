package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// replication_monitor_panel_load.go is Replication Monitor's reads: the
// snapshot of every publication with its subscriptions and agents, the
// selected agent's sessions, the selected session's actions with their error
// detail, and the timer that repeats them. Each pane keeps the row the user
// had across a refresh, by key rather than by index — an agent that failed
// moves up the procedures' worst-first order. The panel is in
// replication_monitor_panel.go.

// rmSnapshot is one read of the instance's distribution databases. note, when
// set, is why there is nothing to list and replaces the publication grid.
type rmSnapshot struct {
	note string
	pubs []rmPub
	// failed are the distribution databases whose read failed while another's
	// succeeded: their publications are missing, and the status line says so.
	failed []rmDBFailure
}

// rmDBFailure is one distribution database whose read failed.
type rmDBFailure struct {
	db  string
	err error
}

// rmPub is one publication with what serves it: its subscriptions, each with
// its Distribution or Merge Agent, and its Snapshot and Log Reader agents.
// An agent the procedures name but sp_MSenum_* does not list is nil.
type rmPub struct {
	dd        *gosmo.DistributionDatabase
	mon       gosmo.MonitorPublication
	subs      []rmSub
	snapshot  *gosmo.ReplicationAgent
	logReader *gosmo.ReplicationAgent
}

type rmSub struct {
	mon   gosmo.MonitorSubscription
	agent *gosmo.ReplicationAgent
}

func (p rmPub) key() string { return rmKey(p.mon.Publisher, p.mon.PublisherDB, p.mon.Publication) }

// rmAgentRow is one row of the agent grid: a subscription with its agent, or
// one of the publication's own agents (sub nil).
type rmAgentRow struct {
	agent *gosmo.ReplicationAgent
	sub   *gosmo.MonitorSubscription
	kind  gosmo.ReplAgentKind
}

func (r rmAgentRow) key() string {
	if r.sub != nil {
		return rmKey("sub", r.sub.Subscriber, r.sub.SubscriberDB)
	}
	return rmKey("agent", r.kind.String())
}

type rmSessionRow struct{ s gosmo.ReplAgentSession }

type rmActionRow struct {
	a gosmo.ReplAgentAction
	// errText is the action's error detail from MSrepl_errors, empty when
	// it has none.
	errText string
}

// The explanations the publication grid shows instead of rows.
const (
	rmNoAccessNote = "Monitoring needs sysadmin, or membership of db_owner or replmonitor in the " +
		"distribution database (%s)."
	rmRemoteNote = "This instance is not a distributor: its publications are distributed by %s. " +
		"Open Replication Monitor on a connection to %s."
	rmNothingNote = "No publications are distributed here."
)

// readReplicationSnapshot reads every distribution database on srv: its
// publications, subscriptions and agents, joined by the agent names the
// monitor procedures report. A distribution database the login may not
// monitor is skipped; if that leaves nothing, the note says why. One whose
// read fails is reported in failed rather than blanking every other's
// publications (as readLogFiles does per file); only when none could be read
// is the failure the read's error.
func readReplicationSnapshot(ctx context.Context, srv *gosmo.Server) (rmSnapshot, error) {
	info, err := srv.ReplicationInfo(ctx)
	if err != nil {
		return rmSnapshot{}, err
	}
	switch {
	case !info.DistributorConfigured && !info.IsDistributor:
		return rmSnapshot{note: replicationNotConfiguredLabel}, nil
	case !info.IsDistributor:
		return rmSnapshot{note: fmt.Sprintf(rmRemoteNote, info.Distributor, info.Distributor)}, nil
	}
	// The names come from sys.databases, which every login reads; msdb's
	// list of distribution databases is sysadmin's alone.
	var snap rmSnapshot
	var denied []string
	read := 0
	for _, d := range info.Databases {
		if !d.Distribution {
			continue
		}
		dd := srv.DistributionDatabaseRef(d.Name)
		ok, err := dd.CanMonitor(ctx)
		if err == nil && !ok {
			denied = append(denied, d.Name)
			continue
		}
		var pubs []rmPub
		if err == nil {
			pubs, err = readDistribution(ctx, dd)
		}
		if err != nil {
			if ctx.Err() != nil {
				return rmSnapshot{}, err
			}
			snap.failed = append(snap.failed, rmDBFailure{db: d.Name, err: err})
			continue
		}
		read++
		snap.pubs = append(snap.pubs, pubs...)
	}
	if read == 0 && len(snap.failed) > 0 {
		if len(snap.failed) == 1 {
			return rmSnapshot{}, snap.failed[0].err
		}
		errs := make([]error, len(snap.failed))
		for i, f := range snap.failed {
			errs[i] = fmt.Errorf("%s: %w", f.db, f.err)
		}
		return rmSnapshot{}, errors.Join(errs...)
	}
	if len(snap.pubs) == 0 && len(snap.failed) == 0 {
		snap.note = rmNothingNote
		if len(denied) > 0 {
			snap.note = fmt.Sprintf(rmNoAccessNote, strings.Join(denied, ", "))
		}
	}
	return snap, nil
}

// readDistribution reads one distribution database and joins its three
// answers into publications.
func readDistribution(ctx context.Context, dd *gosmo.DistributionDatabase) ([]rmPub, error) {
	mpubs, err := dd.MonitorPublications(ctx)
	if err != nil {
		return nil, err
	}
	msubs, err := dd.MonitorSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := dd.Agents(ctx)
	if err != nil {
		return nil, err
	}
	return joinReplication(dd, mpubs, msubs, agents), nil
}

// joinReplication hangs each subscription and agent on its publication. The
// procedures name each publication's agents and each subscription's, so the
// join is by agent name; subscriptions are matched to publications by
// publisher, database and name, compared without regard to case.
func joinReplication(dd *gosmo.DistributionDatabase, mpubs []gosmo.MonitorPublication,
	msubs []gosmo.MonitorSubscription, agents []*gosmo.ReplicationAgent) []rmPub {
	byName := make(map[string]*gosmo.ReplicationAgent, len(agents))
	for _, a := range agents {
		byName[rmKey(a.Name)] = a
	}
	agent := func(name string) *gosmo.ReplicationAgent {
		if name == "" {
			return nil
		}
		return byName[rmKey(name)]
	}
	out := make([]rmPub, 0, len(mpubs))
	idx := make(map[string]int, len(mpubs))
	for _, m := range mpubs {
		p := rmPub{dd: dd, mon: m, snapshot: agent(m.SnapshotAgent), logReader: agent(m.LogReaderAgent)}
		idx[p.key()] = len(out)
		out = append(out, p)
	}
	for _, s := range msubs {
		i, ok := idx[rmKey(s.Publisher, s.PublisherDB, s.Publication)]
		if !ok {
			continue
		}
		name := s.DistributionAgent
		if name == "" {
			name = s.MergeAgent
		}
		out[i].subs = append(out[i].subs, rmSub{mon: s, agent: agent(name)})
	}
	slices.SortStableFunc(out, func(a, b rmPub) int { return strings.Compare(a.key(), b.key()) })
	return out
}

// -- the publication grid -------------------------------------------------------

var rmPubColumns = []string{"Status", "Publication", "Publisher", "Type", "Subscriptions", "Distributing",
	"Worst latency", "Average latency", "Last synchronization", "Warnings"}

func (p rmPub) cells() []string {
	m := p.mon
	return []string{
		m.Status.String(),
		publicationLabel(m.PublisherDB, m.Publication),
		m.Publisher,
		m.Type.String(),
		strconv.Itoa(m.SubscriptionCount),
		strconv.Itoa(m.RunningDistAgentCount),
		rmSeconds(m.WorstLatency),
		rmSeconds(m.AverageLatency),
		formatSQLDate(m.LastDistSync),
		strings.Join(m.Warning.Warnings(), "; "),
	}
}

// rmSeconds renders a latency in seconds, empty where none was measured.
func rmSeconds(v *int) string {
	if v == nil {
		return ""
	}
	return formatHMS(time.Duration(*v) * time.Second)
}

// -- the agent grid -------------------------------------------------------------

var rmAgentColumns = []string{"Agent", "Status", "Subscriber", "Last action", "Last start", "Duration",
	"Delivered", "Latency", "Expires in", "Warnings", "Agent name"}

// agentRowsFor lists a publication's subscriptions, then its Snapshot and Log
// Reader agents — the subscriptions first because they are what Replication
// Monitor's publication view is about, and where an agent failing shows.
func agentRowsFor(p rmPub) []rmAgentRow {
	var rows []rmAgentRow
	for i := range p.subs {
		s := &p.subs[i]
		kind := gosmo.ReplDistributionAgent
		if s.mon.PublicationType == gosmo.PublicationMerge {
			kind = gosmo.ReplMergeAgent
		}
		rows = append(rows, rmAgentRow{agent: s.agent, sub: &s.mon, kind: kind})
	}
	if p.snapshot != nil || p.mon.SnapshotAgent != "" {
		rows = append(rows, rmAgentRow{agent: p.snapshot, kind: gosmo.ReplSnapshotAgent})
	}
	if p.logReader != nil || p.mon.LogReaderAgent != "" {
		rows = append(rows, rmAgentRow{agent: p.logReader, kind: gosmo.ReplLogReaderAgent})
	}
	return rows
}

func (r rmAgentRow) cells() []string {
	c := make([]string, len(rmAgentColumns))
	c[0] = r.kind.String()
	if a := r.agent; a != nil {
		c[1] = a.Status.String()
		c[3] = a.LastAction
		c[4] = formatSQLDate(a.LastStart)
		c[5] = formatHMS(time.Duration(a.Duration) * time.Second)
		c[6] = rmDelivered(a.Kind, a.DeliveredCommands, a.Downloaded, a.Uploaded)
		if a.LatencyMs != nil {
			c[7] = formatHMS(time.Duration(*a.LatencyMs) * time.Millisecond)
		}
		c[10] = a.Name
	} else {
		c[3] = "The distributor lists no such agent"
	}
	if s := r.sub; s != nil {
		// The monitor's own status: it knows an agent that is not running,
		// which the last history row cannot say.
		c[1] = s.Status.String()
		c[2] = "[" + s.Subscriber + "].[" + s.SubscriberDB + "] (" + s.Type.String() + ")"
		if s.Latency != nil {
			c[7] = rmSeconds(s.Latency)
		}
		if s.TimeToExpiration != nil {
			c[8] = strconv.Itoa(*s.TimeToExpiration) + " h"
		}
		c[9] = strings.Join(s.Warning.Warnings(), "; ")
	}
	return c
}

// rmDelivered is what a run delivered: commands, or a merge's rows each way.
func rmDelivered(kind gosmo.ReplAgentKind, commands, down, up int64) string {
	if kind == gosmo.ReplMergeAgent {
		return fmt.Sprintf("↓%d ↑%d", down, up)
	}
	return strconv.FormatInt(commands, 10)
}

// -- the session and action grids ----------------------------------------------

var rmSessionColumns = []string{"Status", "Start", "End", "Duration", "Last action", "Actions", "Delivered", "Latency", "Error"}

func (r rmSessionRow) cells(kind gosmo.ReplAgentKind) []string {
	s := r.s
	c := []string{
		s.Status.String(),
		formatSQLDate(s.Start),
		formatSQLDate(s.End),
		formatHMS(time.Duration(s.Duration) * time.Second),
		s.LastAction,
		strconv.Itoa(s.Actions),
		rmDelivered(kind, s.DeliveredCommands, s.Downloaded, s.Uploaded),
		"",
		"",
	}
	if s.LatencyMs != nil {
		c[7] = formatHMS(time.Duration(*s.LatencyMs) * time.Millisecond)
	}
	if s.ErrorID != 0 {
		c[8] = "Yes"
	}
	return c
}

var rmActionColumns = []string{"Time", "Status", "Message", "Error details"}

func (r rmActionRow) cells() []string {
	return []string{formatSQLDate(r.a.Time), r.a.Status.String(), r.a.Message, r.errText}
}

// rmErrorText joins the rows of one MSrepl_errors entry, oldest first.
func rmErrorText(errs []gosmo.ReplicationError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		t := e.Text
		if e.Code != "" {
			t = e.Code + ": " + t
		}
		if e.SourceName != "" {
			t += " (" + e.SourceName + ")"
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " | ")
}

// -- loads ----------------------------------------------------------------------

// Refresh re-reads everything (F5, the toolbar, the timer), keeping each
// pane's selection.
func (p *ReplicationMonitorPanel) Refresh() {
	if !p.app.isConnected(p.conn) {
		p.applySnapshot(rmSnapshot{note: "Not connected"}, nil)
		return
	}
	ctx, seq := p.snapRead.BeginTimeout(p.conn.Server.Context(), rmReadTimeout)
	p.busy = true
	p.pubsGrid.SetStatus("Reading…")
	srv := p.conn.Server
	// safegoRepair: busy is released in the callback, which a panic skips,
	// and the timer refreshes only while busy is clear.
	p.app.safegoRepair("reading Replication Monitor", func() { p.readPanicked(seq) }, func() {
		snap, err := readReplicationSnapshot(ctx, srv)
		p.app.postAndWake(func() {
			if !p.snapRead.Done(seq) {
				return
			}
			p.busy = false
			p.applySnapshot(snap, err)
		})
	})
}

// readPanicked releases busy after a panic on the read goroutine, unless a
// newer read has taken it.
func (p *ReplicationMonitorPanel) readPanicked(seq int) {
	if !p.snapRead.Done(seq) {
		return
	}
	p.busy = false
	p.pubsGrid.SetStatus(rmReadPanickedText)
}

const rmReadPanickedText = "The read stopped unexpectedly — see the log for details"

// paneReadPanicked replaces the "Reading…" a session or action read left on
// its grid when it panicked, unless a newer read owns the grid. Without it the
// pane said "Reading…" for good.
func paneReadPanicked(l *latest, seq int, g *controls.DataGrid) {
	if l.Done(seq) {
		g.SetStatus(rmReadPanickedText)
	}
}

// applySnapshot puts a read on screen, on the publication the user had (or
// the one the panel was opened for), and cascades to the panes below.
func (p *ReplicationMonitorPanel) applySnapshot(snap rmSnapshot, err error) {
	prev := p.selectedPubKey()
	// A note grid's one column was widened to fit it; carrying that width
	// over to the Status column is what redrawGrid would do.
	wasNote := p.snap.note != ""
	if err != nil {
		p.snap = rmSnapshot{}
		p.pubsGrid.SetError(displayError(err))
		p.showAgents(false)
		return
	}
	p.snap = snap
	p.updated = time.Now()
	if snap.note != "" {
		p.pubsGrid.SetData([]string{"Replication Monitor"}, [][]string{{snap.note}})
		// The note is a sentence, not a value: the default cell cap clips it
		// to its first few words.
		p.pubsGrid.SetColumnWidth(0, core.DisplayWidth(snap.note)+2)
		p.pubsGrid.SetStatus(snap.note)
		p.showAgents(false)
		return
	}
	rows := make([][]string, len(snap.pubs))
	for i, pub := range snap.pubs {
		rows[i] = pub.cells()
	}
	if wasNote {
		p.pubsGrid.SetData(rmPubColumns, rows)
	} else {
		redrawGrid(p.pubsGrid, rmPubColumns, rows)
	}
	keep := true
	if p.wantPub != "" {
		if i := p.pubIndexByName(p.wantPubDB, p.wantPub); i >= 0 {
			p.pubsGrid.SetSelectedRow(i)
			keep = false
		}
		p.wantPubDB, p.wantPub = "", ""
	} else if i := slices.IndexFunc(snap.pubs, func(x rmPub) bool { return x.key() == prev }); i >= 0 {
		p.pubsGrid.SetSelectedRow(i)
	} else {
		keep = false
	}
	p.pubsGrid.SetStatus(p.summary())
	p.showAgents(keep)
}

// summary is the publication grid's status line.
func (p *ReplicationMonitorPanel) summary() string {
	auto := "auto refresh off"
	if r := rmRates[p.rateIdx]; r.every > 0 {
		auto = "refreshed every " + r.label
	}
	s := fmt.Sprintf("%d publications · read %s · %s", len(p.snap.pubs), p.updated.Format("15:04:05"), auto)
	for _, f := range p.snap.failed {
		s += fmt.Sprintf(" · could not read %s: %v", f.db, displayError(f.err))
	}
	return s
}

// pubIndexByName finds a publication by database and name, as an Object
// Explorer node names it.
func (p *ReplicationMonitorPanel) pubIndexByName(dbName, pub string) int {
	want := rmKey(dbName, pub)
	return slices.IndexFunc(p.snap.pubs, func(x rmPub) bool { return rmKey(x.mon.PublisherDB, x.mon.Publication) == want })
}

// selectPublication selects a publication on a panel already open, or on its
// next read if it has none yet.
func (p *ReplicationMonitorPanel) selectPublication(dbName, pub string) {
	if i := p.pubIndexByName(dbName, pub); i >= 0 {
		p.pubsGrid.SetSelectedRow(i)
		p.showAgents(false)
		return
	}
	p.wantPubDB, p.wantPub = dbName, pub
}

// selectedPub is the publication under the cursor, nil when there is none.
func (p *ReplicationMonitorPanel) selectedPub() *rmPub {
	i := p.pubsGrid.SelectedRow()
	if p.snap.note != "" || i < 0 || i >= len(p.snap.pubs) {
		return nil
	}
	return &p.snap.pubs[i]
}

func (p *ReplicationMonitorPanel) selectedPubKey() string {
	if pub := p.selectedPub(); pub != nil {
		return pub.key()
	}
	return ""
}

// showAgents fills the agent grid from the selected publication — no read,
// the snapshot has it — and reads the selected agent's sessions. keep holds
// the agent row the user had, for a refresh; a move to another publication
// starts at its first row.
func (p *ReplicationMonitorPanel) showAgents(keep bool) {
	prev := ""
	if keep {
		prev = p.selectedAgentKey()
	}
	pub := p.selectedPub()
	p.agentRows = nil
	if pub != nil {
		p.agentRows = agentRowsFor(*pub)
	}
	rows := make([][]string, len(p.agentRows))
	for i, r := range p.agentRows {
		rows[i] = r.cells()
	}
	if keep {
		redrawGrid(p.agentsGrid, rmAgentColumns, rows)
	} else {
		p.agentsGrid.SetData(rmAgentColumns, rows)
	}
	if i := slices.IndexFunc(p.agentRows, func(r rmAgentRow) bool { return r.key() == prev }); i >= 0 {
		p.agentsGrid.SetSelectedRow(i)
	} else {
		keep = false
	}
	p.loadSessions(keep)
}

func (p *ReplicationMonitorPanel) selectedAgentRow() *rmAgentRow {
	i := p.agentsGrid.SelectedRow()
	if i < 0 || i >= len(p.agentRows) {
		return nil
	}
	return &p.agentRows[i]
}

func (p *ReplicationMonitorPanel) selectedAgentKey() string {
	if r := p.selectedAgentRow(); r != nil {
		return r.key()
	}
	return ""
}

// loadSessions reads the selected agent's sessions over the History window.
func (p *ReplicationMonitorPanel) loadSessions(keep bool) {
	row := p.selectedAgentRow()
	if row == nil || row.agent == nil || !p.app.isConnected(p.conn) {
		p.sessionRead.Abandon()
		p.sessions = nil
		p.sessionsGrid.SetData(rmSessionColumns, nil)
		if row != nil {
			p.sessionsGrid.SetStatus("No agent to read the history of")
		}
		p.loadActions(false)
		return
	}
	agent := row.agent
	hours, errorsOnly := rmWindows[p.windowIdx].hours, p.errorsOnly
	ctx, seq := p.sessionRead.BeginTimeout(p.conn.Server.Context(), rmReadTimeout)
	p.sessionsGrid.SetStatus("Reading…")
	p.app.safegoRepair("reading a replication agent's sessions", func() { paneReadPanicked(&p.sessionRead, seq, p.sessionsGrid) }, func() {
		sessions, err := agent.Sessions(ctx, hours, errorsOnly)
		p.app.postAndWake(func() {
			if !p.sessionRead.Done(seq) {
				return
			}
			p.applySessions(agent, sessions, err, keep)
		})
	})
}

func (p *ReplicationMonitorPanel) applySessions(agent *gosmo.ReplicationAgent, sessions []gosmo.ReplAgentSession, err error, keep bool) {
	var prev time.Time
	if keep {
		if r := p.selectedSession(); r != nil {
			prev = r.s.Start
		}
	}
	if err != nil {
		p.sessions = nil
		p.sessionsGrid.SetError(displayError(err))
		p.loadActions(false)
		return
	}
	p.sessions = make([]rmSessionRow, len(sessions))
	rows := make([][]string, len(sessions))
	for i, s := range sessions {
		p.sessions[i] = rmSessionRow{s: s}
		rows[i] = p.sessions[i].cells(agent.Kind)
	}
	if keep {
		redrawGrid(p.sessionsGrid, rmSessionColumns, rows)
	} else {
		p.sessionsGrid.SetData(rmSessionColumns, rows)
	}
	if i := slices.IndexFunc(p.sessions, func(r rmSessionRow) bool { return r.s.Start.Equal(prev) }); keep && i >= 0 {
		p.sessionsGrid.SetSelectedRow(i)
	} else {
		p.sessionsGrid.SetSelectedRow(0)
		keep = false
	}
	p.sessionsGrid.SetStatus(p.sessionsSummary(agent, len(sessions)))
	p.loadActions(keep)
}

// sessionsSummary is the session grid's status line: whose sessions, over
// what window.
func (p *ReplicationMonitorPanel) sessionsSummary(agent *gosmo.ReplicationAgent, n int) string {
	window := "the last " + rmWindows[p.windowIdx].label
	if rmWindows[p.windowIdx].hours == 0 {
		window = "all history kept"
	}
	what := "session"
	if p.errorsOnly {
		what = "failed session"
	}
	if n != 1 {
		what += "s"
	}
	return fmt.Sprintf("%s — %d %s, %s", agent.Kind, n, what, window)
}

func (p *ReplicationMonitorPanel) selectedSession() *rmSessionRow {
	i := p.sessionsGrid.SelectedRow()
	if i < 0 || i >= len(p.sessions) {
		return nil
	}
	return &p.sessions[i]
}

// loadActions reads the selected session's actions, and the error detail
// behind each one that names an error.
func (p *ReplicationMonitorPanel) loadActions(keep bool) {
	row, sess := p.selectedAgentRow(), p.selectedSession()
	if row == nil || row.agent == nil || sess == nil || !p.app.isConnected(p.conn) {
		p.actionRead.Abandon()
		p.actions = nil
		p.actionsGrid.SetData(rmActionColumns, nil)
		return
	}
	agent, s := row.agent, sess.s
	ctx, seq := p.actionRead.BeginTimeout(p.conn.Server.Context(), rmReadTimeout)
	p.actionsGrid.SetStatus("Reading…")
	p.app.safegoRepair("reading a replication agent session", func() { paneReadPanicked(&p.actionRead, seq, p.actionsGrid) }, func() {
		rows, err := readActions(ctx, agent, s)
		p.app.postAndWake(func() {
			if !p.actionRead.Done(seq) {
				return
			}
			p.applyActions(rows, err, keep)
		})
	})
}

// readActions reads one session's actions and the error detail behind each
// error id they name, each id once.
func readActions(ctx context.Context, agent *gosmo.ReplicationAgent, s gosmo.ReplAgentSession) ([]rmActionRow, error) {
	acts, err := agent.SessionActions(ctx, s)
	if err != nil {
		return nil, err
	}
	detail := map[int]string{}
	rows := make([]rmActionRow, len(acts))
	for i, a := range acts {
		rows[i].a = a
		if a.ErrorID == 0 {
			continue
		}
		text, ok := detail[a.ErrorID]
		if !ok {
			errs, err := agent.DistributionDatabase().Errors(ctx, a.ErrorID)
			if err != nil {
				return nil, err
			}
			text = rmErrorText(errs)
			if text == "" {
				text = fmt.Sprintf("Error %d (its detail is no longer kept)", a.ErrorID)
			}
			detail[a.ErrorID] = text
		}
		rows[i].errText = text
	}
	return rows, nil
}

func (p *ReplicationMonitorPanel) applyActions(rows []rmActionRow, err error, keep bool) {
	if err != nil {
		p.actions = nil
		p.actionsGrid.SetError(displayError(err))
		return
	}
	p.actions = rows
	cells := make([][]string, len(rows))
	errs := 0
	for i, r := range rows {
		cells[i] = r.cells()
		if r.errText != "" {
			errs++
		}
	}
	if keep {
		redrawGrid(p.actionsGrid, rmActionColumns, cells)
	} else {
		p.actionsGrid.SetData(rmActionColumns, cells)
	}
	status := fmt.Sprintf("%d actions", len(rows))
	if errs > 0 {
		status += fmt.Sprintf(", %d with error details (Show Value on the cell for all of it)", errs)
	}
	p.actionsGrid.SetStatus(status)
}

// -- the refresh timer ----------------------------------------------------------

// startTicker (re)starts the refresh timer at the selected rate, or stops it
// at Off. A tick while a read is out is skipped, not queued — and so is one
// while a grid's value popup or cell menu is open: a refresh rebuilds the
// grids, and rebuilding one closes its popup, so the error a user opened to
// read vanished at the next tick.
func (p *ReplicationMonitorPanel) startTicker() {
	p.ticker.Abandon()
	every := rmRates[p.rateIdx].every
	if every == 0 || p.conn == nil || p.conn.Server == nil {
		return
	}
	ctx, tok := p.ticker.Begin(p.conn.Server.Context())
	p.app.safego("timing Replication Monitor's refresh", func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.app.postAndWake(func() { p.onTick(tok) })
			}
		}
	})
}

// onTick is one tick on the UI goroutine: a refresh, unless the timer was
// replaced, a read is out, or a popup is open (see startTicker).
func (p *ReplicationMonitorPanel) onTick(tok int) {
	if p.ticker.Current(tok) && !p.busy && p.overlayGrid() == nil {
		p.Refresh()
	}
}
