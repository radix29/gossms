package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ag_dashboard.go is the Always On dashboard panel — SSMS's "Show Dashboard",
// in the two forms SSMS offers it.
//
// Opened on one availability group (agName set) it shows that group's health
// rollup, every replica's role and connection state, and per-database queue
// sizes with the two figures a DBA actually decides on, estimated data loss
// and estimated recovery time. Opened on the Always On root (agName empty) it
// shows every group on the instance over every group's replicas; the
// all-groups reading is in ag_dashboard_all.go. Both share this file's refresh
// loop, layout and input, which is why the two grids are named for their
// position rather than their contents.
//
// Like AG Properties it reads through the primary (agOnPrimaryFollowed): the
// send/redo queues and commit times for a *secondary's* copy of a database are
// reported by the primary, not by that secondary, so a dashboard built from a
// secondary would show blanks for precisely the replicas being watched.

// agDashboardRates are the refresh intervals the panel offers, and
// agDashboardDefaultRate indexes the one it opens at. An
// availability group is a level that moves over seconds to minutes, and each
// tick is four round trips against the primary — so these start where the
// Activity Monitor's list ends rather than at its 2-second cadence. 60 s is
// there for leaving the panel open on a second monitor.
var agDashboardRates = []time.Duration{
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var agDashboardRateLabels = []string{"5 s", "10 s", "30 s", "60 s"}

const agDashboardDefaultRate = 1

// agDashboardTimeout bounds one refresh, mirroring childFetchTimeout: a tick
// that cannot finish before the next is due must not queue up behind it.
const agDashboardTimeout = 30 * time.Second

// AGDashboard is an Always On dashboard, hosted by layout.PanelManager like
// any other panel. It owns a refresh goroutine that polls until the panel is
// closed or its connection goes away.
type AGDashboard struct {
	app  *App
	conn *db.ServerConn // the registered connection; owned by App, not by this panel
	// agName is the group being watched, or empty for the all-groups view.
	agName string

	rect   core.Rect
	active bool

	// topGrid holds replicas in the one-group view and groups in the
	// all-groups view; bottomGrid holds databases and replicas respectively.
	topGrid    *controls.DataGrid
	bottomGrid *controls.DataGrid
	// topRect/bottomRect are where SetBounds put each grid. DataGrid has no
	// bounds accessor, and mouse routing needs to know which grid was hit.
	topRect    core.Rect
	bottomRect core.Rect
	// topRows/bottomRows are the slices the grids read through. Refreshing
	// rewrites them in place when the shape is unchanged rather than calling
	// SetData, which resets scroll and selection — on a 10-second poll that
	// would yank the user back to the top of the grid mid-read.
	topRows    [][]string
	bottomRows [][]string

	// focusBottom picks which grid the keyboard drives; Tab flips it.
	focusBottom bool

	// snap is the last reading, and err whatever the last refresh failed
	// with. A failed refresh keeps the previous reading on screen — a
	// dashboard that blanks itself on one dropped round trip is less useful
	// than one that says the numbers are stale.
	snap agSnapshot
	err  error

	// paused is read by the refresh goroutine and written by the UI
	// goroutine, so it is atomic rather than a plain bool.
	paused atomic.Bool
	// kick forces an immediate refresh (F5) even while paused. Buffered and
	// sent to non-blockingly: a second F5 while one is already pending is
	// the same request.
	kick chan struct{}
	// rateIdx indexes agDashboardRates; like paused it is written by the UI
	// goroutine and read by the refresh goroutine. rateCh wakes that goroutine
	// so a change takes effect now rather than after the interval it is
	// replacing — 60 s down to 5 s would otherwise be a minute of no effect.
	rateIdx atomic.Int32
	rateCh  chan struct{}
	cancel  context.CancelFunc
}

// agSnapshot is one complete reading, of a single group or of every group.
type agSnapshot struct {
	group    *gosmo.AvailabilityGroup
	replicas []*gosmo.AvailabilityReplica
	dbs      []agDatabaseMetrics

	// groups is the all-groups reading, and is what ok() tests in that mode —
	// an instance with no availability groups at all is a valid reading, and
	// must not be shown as "Loading..." forever.
	groups   []agGroupRollup
	allGroup bool

	// followed records that the reading came from a peer connection to the
	// primary rather than from the panel's own connection.
	followed bool
	at       time.Time
}

// ok reports whether the snapshot holds a reading at all.
func (s agSnapshot) ok() bool { return s.group != nil || s.allGroup }

// NewAGDashboard creates the panel and starts its refresh loop.
func NewAGDashboard(app *App, conn *db.ServerConn, agName string) *AGDashboard {
	d := &AGDashboard{
		app: app, conn: conn, agName: agName,
		topGrid:    controls.NewDataGrid(),
		bottomGrid: controls.NewDataGrid(),
		kick:       make(chan struct{}, 1),
		rateCh:     make(chan struct{}, 1),
	}
	d.rateIdx.Store(agDashboardDefaultRate)
	d.topGrid.SetData(d.topColumns(), nil)
	d.topGrid.SetStatus("Loading...")
	d.bottomGrid.SetData(d.bottomColumns(), nil)
	d.bottomGrid.SetStatus("Loading...")

	ctx, cancel := context.WithCancel(conn.Context())
	d.cancel = cancel
	app.safego("refreshing an Always On dashboard", func() { d.run(ctx) })
	return d
}

func (d *AGDashboard) Title() string {
	if d.allGroups() {
		return "Dashboard: Always On"
	}
	return "Dashboard: " + d.agName
}

func (d *AGDashboard) SetActive(v bool) { d.active = v }

// allGroups reports whether this panel watches every group on the instance
// rather than one named group.
func (d *AGDashboard) allGroups() bool { return d.agName == "" }

// Close stops the refresh loop. Called from App.closePanelAt — the panel's
// context is derived from the connection's, which outlives the panel.
func (d *AGDashboard) Close() { cancelIfSet(d.cancel) }

// rate is the interval the panel is currently polling at.
func (d *AGDashboard) rate() time.Duration { return agDashboardRates[d.rateIdx.Load()] }

// setRate selects an interval by index, reporting whether the index existed —
// a false at either end of the list leaves the key unhandled rather than
// swallowing it.
func (d *AGDashboard) setRate(i int) bool {
	if i < 0 || i >= len(agDashboardRates) {
		return false
	}
	d.rateIdx.Store(int32(i))
	select {
	case d.rateCh <- struct{}{}:
	default:
	}
	return true
}

// run is the refresh goroutine: read, then wait for the tick, an F5, a rate
// change, or the panel closing. A timer rather than a ticker, because the
// interval it re-arms with can have changed while it was waiting.
func (d *AGDashboard) run(ctx context.Context) {
	d.refreshOnce(ctx) // the first reading is never skipped, however the panel opened
	t := time.NewTimer(d.rate())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !d.paused.Load() {
				d.refreshOnce(ctx)
			}
		case <-d.kick:
			// F5 overrides the pause, which is what makes it useful there.
			d.refreshOnce(ctx)
		case <-d.rateCh:
			// A rate change re-arms the wait below and takes no reading of its
			// own: it is not a request for data now, and while paused it must
			// not produce one at all.
		}
		t.Stop()
		t.Reset(d.rate())
	}
}

func (d *AGDashboard) refreshOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, agDashboardTimeout)
	defer cancel()
	snap, err := d.read(ctx)
	d.app.postAndWake(func() { d.apply(snap, err) })
}

// read takes one complete reading through the group's primary.
func (d *AGDashboard) read(ctx context.Context) (agSnapshot, error) {
	if d.allGroups() {
		return d.readAllGroups(ctx)
	}
	ag, followed, err := agOnPrimaryFollowed(ctx, d.conn, d.agName)
	if err != nil {
		return agSnapshot{}, err
	}
	replicas, err := ag.ReplicasContext(ctx)
	if err != nil {
		return agSnapshot{}, err
	}
	dbs, err := ag.DatabasesContext(ctx)
	if err != nil {
		return agSnapshot{}, err
	}
	return agSnapshot{
		group: ag, replicas: replicas, dbs: agComputeDatabaseMetrics(dbs),
		followed: followed, at: time.Now(),
	}, nil
}

// apply installs a reading on the UI goroutine. A failed refresh records the
// error but keeps the previous reading and its timestamp, so the header can
// say how stale the numbers on screen are.
func (d *AGDashboard) apply(snap agSnapshot, err error) {
	d.err = err
	if err != nil {
		if !d.snap.ok() {
			d.topGrid.SetStatus("No data")
			d.bottomGrid.SetStatus("No data")
		}
		return
	}
	d.snap = snap
	d.setRows(d.topGrid, &d.topRows, d.topColumns(), d.topRowsFrom(snap))
	d.setRows(d.bottomGrid, &d.bottomRows, d.bottomColumns(), d.bottomRowsFrom(snap))
	// Re-split now that the top grid's row count is known. SetBounds sizes it
	// to its contents, and the only layout it has seen so far was the one
	// before the first reading landed — which showed a two-replica group one
	// replica.
	d.SetBounds(d.rect.X, d.rect.Y, d.rect.W, d.rect.H)
}

// setRows updates a grid without disturbing the user's scroll position where
// it can. SetData resets scroll and selection, which on a polling panel throws
// the reader back to the top every tick, so it is used only when the row count
// actually changes — a replica or database joining or leaving the group.
func (d *AGDashboard) setRows(g *controls.DataGrid, held *[][]string, columns []string, rows [][]string) {
	if len(rows) != len(*held) {
		*held = rows
		g.SetData(columns, rows)
		return
	}
	for i := range rows {
		copy((*held)[i], rows[i])
	}
	g.RefreshColumnWidths()
	g.SetStatus(strconv.Itoa(len(rows)) + " rows")
}

// -- derived metrics -----------------------------------------------------------

// agDatabaseMetrics is one (database, replica) row of the dashboard, carrying
// the two figures SQL Server does not report directly. Both are computed
// rather than read, and both are optional: an unknown number is left blank
// instead of being shown as zero, because "no data loss" and "we cannot tell"
// are the opposite answers to the question this dashboard exists to answer.
type agDatabaseMetrics struct {
	DB *gosmo.AvailabilityDatabase

	// DataLoss is how far this secondary's last hardened commit trails the
	// primary's — what a failover to it would lose right now.
	DataLoss    time.Duration
	HasDataLoss bool

	// RecoveryTime is how long this secondary's redo queue would take to
	// drain at its current redo rate — how long a failover to it would take
	// to come online.
	RecoveryTime    time.Duration
	HasRecoveryTime bool
}

// agComputeDatabaseMetrics derives each secondary row's data loss and recovery
// time. Both need the whole result set, not one row: data loss is measured
// against the *primary's* last commit time for the same database, which is a
// different row.
func agComputeDatabaseMetrics(dbs []*gosmo.AvailabilityDatabase) []agDatabaseMetrics {
	primaryCommit := make(map[string]time.Time, len(dbs))
	for _, d := range dbs {
		if d.IsPrimaryReplica && !d.LastCommitTime.IsZero() {
			primaryCommit[strings.ToLower(d.DatabaseName)] = d.LastCommitTime
		}
	}

	out := make([]agDatabaseMetrics, 0, len(dbs))
	for _, d := range dbs {
		m := agDatabaseMetrics{DB: d}
		if d.IsPrimaryReplica {
			out = append(out, m)
			continue
		}
		if pc, ok := primaryCommit[strings.ToLower(d.DatabaseName)]; ok && !d.LastCommitTime.IsZero() {
			loss := pc.Sub(d.LastCommitTime)
			// A secondary cannot really be ahead of its primary; a negative
			// difference is clock skew between the two rows' sources, and
			// reporting it as "-3s of data loss" would read as a fault.
			if loss < 0 {
				loss = 0
			}
			m.DataLoss, m.HasDataLoss = loss, true
		}
		switch {
		case d.RedoRateKBps > 0:
			m.RecoveryTime = time.Duration(float64(d.RedoQueueKB) / float64(d.RedoRateKBps) * float64(time.Second))
			m.HasRecoveryTime = true
		case d.RedoQueueKB == 0:
			// Nothing queued is a known zero, not an unknown: without this a
			// fully caught-up secondary shows blank, the same as one whose
			// rate we cannot see.
			m.HasRecoveryTime = true
		}
		out = append(out, m)
	}
	return out
}

// agReplicaIssues names what is wrong with a replica, in the order a reader
// would want to hear it: not being connected explains every number below it,
// so it comes first. An empty result means the replica is healthy — this is
// the column that saves reading the other seven.
func agReplicaIssues(r *gosmo.AvailabilityReplica, dbs []agDatabaseMetrics) string {
	var issues []string
	if r.ConnectedState != "" && !strings.EqualFold(r.ConnectedState, "CONNECTED") {
		issues = append(issues, titleWord(r.ConnectedState))
	}
	if r.RecoveryHealth != "" && !strings.EqualFold(r.RecoveryHealth, "ONLINE") {
		issues = append(issues, "Recovery "+strings.ToLower(titleWord(r.RecoveryHealth)))
	}
	if r.SynchronizationHealth != "" && !strings.EqualFold(r.SynchronizationHealth, "HEALTHY") {
		issues = append(issues, titleWord(r.SynchronizationHealth))
	}
	suspended := 0
	for _, m := range dbs {
		if strings.EqualFold(m.DB.ReplicaServerName, r.ReplicaServerName) && m.DB.IsSuspended {
			suspended++
		}
	}
	if suspended > 0 {
		issues = append(issues, fmt.Sprintf("%d database(s) suspended", suspended))
	}
	// Reported last and only when nothing else is: a stale connect error on a
	// replica that is connected now is history, not a problem.
	if len(issues) == 0 && r.LastConnectErrorNumber != 0 {
		issues = append(issues, fmt.Sprintf("Last connect error %d", r.LastConnectErrorNumber))
	}
	return strings.Join(issues, "; ")
}

// -- grid rows -----------------------------------------------------------------

var agReplicaColumns = []string{
	"Server instance", "Role", "Availability mode", "Failover mode",
	"Synchronization", "Connection", "Health", "Issues",
}

func agReplicaRows(replicas []*gosmo.AvailabilityReplica, dbs []agDatabaseMetrics) [][]string {
	rows := make([][]string, len(replicas))
	for i, r := range replicas {
		rows[i] = []string{
			r.ReplicaServerName,
			orDefault(titleWord(r.Role), "—"),
			orDefault(commitModeName(r.AvailabilityMode), "—"),
			orDefault(titleWord(r.FailoverMode), "—"),
			orDefault(agReplicaSyncSummary(r, dbs), "—"),
			orDefault(titleWord(r.ConnectedState), "—"),
			orDefault(titleWord(r.SynchronizationHealth), "—"),
			agReplicaIssues(r, dbs),
		}
	}
	return rows
}

// agReplicaSyncSummary rolls this replica's databases up into one
// synchronization state, listing every distinct one rather than picking a
// winner — the same reason agDatabaseLabel does not collapse them.
func agReplicaSyncSummary(r *gosmo.AvailabilityReplica, dbs []agDatabaseMetrics) string {
	var states []string
	for _, m := range dbs {
		if !strings.EqualFold(m.DB.ReplicaServerName, r.ReplicaServerName) {
			continue
		}
		if s := titleWord(m.DB.SynchronizationState); s != "" && !slicesContains(states, s) {
			states = append(states, s)
		}
	}
	return strings.Join(states, ", ")
}

var agDatabaseColumns = []string{
	"Database", "Replica", "Role", "Synchronization", "Suspended",
	"Send queue (KB)", "Send rate (KB/s)", "Redo queue (KB)", "Redo rate (KB/s)",
	"Est. data loss", "Est. recovery",
}

func agDatabaseGridRows(dbs []agDatabaseMetrics) [][]string {
	rows := make([][]string, len(dbs))
	for i, m := range dbs {
		role := "Secondary"
		if m.DB.IsPrimaryReplica {
			role = "Primary"
		}
		suspended := ""
		if m.DB.IsSuspended {
			suspended = orDefault(titleWord(m.DB.SuspendReason), "Yes")
		}
		rows[i] = []string{
			m.DB.DatabaseName,
			m.DB.ReplicaServerName,
			role,
			orDefault(titleWord(m.DB.SynchronizationState), "—"),
			suspended,
			agInt(m.DB.LogSendQueueKB), agInt(m.DB.LogSendRateKBps),
			agInt(m.DB.RedoQueueKB), agInt(m.DB.RedoRateKBps),
			agDuration(m.DataLoss, m.HasDataLoss),
			agDuration(m.RecoveryTime, m.HasRecoveryTime),
		}
	}
	return rows
}

// agInt renders a queue or rate. A primary's row has no queue of its own, and
// SQL Server reports 0 there; that is a real zero and is shown as one.
func agInt(v int64) string { return strconv.FormatInt(v, 10) }

// agDuration renders a derived time, or an em dash when it could not be
// computed — never "0s", which would claim a fact the numbers do not support.
func agDuration(d time.Duration, known bool) string {
	if !known {
		return "—"
	}
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int64(d/time.Minute), int64(d/time.Second)%60)
	default:
		return fmt.Sprintf("%dh %02dm", int64(d/time.Hour), int64(d/time.Minute)%60)
	}
}

// -- input ---------------------------------------------------------------------

func (d *AGDashboard) grid() *controls.DataGrid {
	if d.focusBottom {
		return d.bottomGrid
	}
	return d.topGrid
}

// HandleKey handles the panel's own keys and hands everything else to the
// focused grid, returning what the grid reports — a blanket true here would
// swallow the application's own accelerators.
func (d *AGDashboard) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyF5:
		d.forceRefresh()
		return true
	case tcell.KeyEnter:
		// Only from the group grid: Enter on the replica grid below has nothing
		// to open, and swallowing it there would be a key that does nothing.
		if !d.focusBottom {
			if name := d.selectedGroup(); name != "" {
				d.app.showAGDashboardFor(d.conn, name)
				return true
			}
		}
	case tcell.KeyTab:
		d.focusBottom = !d.focusBottom
		return true
	case tcell.KeyRune:
		if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			return false
		}
		switch core.EvRune(ev) {
		case 'p', 'P':
			d.paused.Store(!d.paused.Load())
			return true
		case '+', '=':
			// Faster: a shorter interval, i.e. earlier in the rate list.
			return d.setRate(int(d.rateIdx.Load()) - 1)
		case '-', '_':
			return d.setRate(int(d.rateIdx.Load()) + 1)
		}
	}
	return d.grid().HandleKey(ev)
}

// forceRefresh asks the refresh goroutine for an immediate reading. The send
// is non-blocking: a pending kick already means "refresh now".
func (d *AGDashboard) forceRefresh() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// HandleMouse routes to whichever grid was clicked, and makes that click move
// the keyboard focus too — otherwise scrolling one grid with the wheel and
// then pressing Down moves the cursor in the other one.
func (d *AGDashboard) HandleMouse(ev *tcell.EventMouse) bool {
	x, y := ev.Position()
	switch {
	case d.topRect.Contains(x, y):
		d.focusBottom = false
		return d.topGrid.HandleMouse(ev)
	case d.bottomRect.Contains(x, y):
		d.focusBottom = true
		return d.bottomGrid.HandleMouse(ev)
	}
	// A drag or a release that started inside a grid still belongs to it —
	// see ARCHITECTURE.md § The mouseDragging idiom, invariant 5.
	if ev.Buttons() == tcell.ButtonNone {
		d.topGrid.HandleMouse(ev)
		d.bottomGrid.HandleMouse(ev)
	}
	return false
}
