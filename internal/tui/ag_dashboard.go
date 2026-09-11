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

// ag_dashboard.go is the Always On dashboard panel (SSMS "Show Dashboard"), in
// both SSMS forms.
//
// On one group (agName set): health rollup, each replica's role and connection
// state, and per-database queues with estimated data loss and recovery time. On
// the Always On root (agName empty): every group and its replicas
// (ag_dashboard_all.go). Both share refresh, layout and input, so grids are
// named by position.
//
// Reads through the primary (agOnPrimaryFollowed): queues and commit times for
// a secondary's databases are reported by the primary, so a secondary-built
// dashboard would be blank where it matters.

// agDashboardRates are the offered intervals; agDashboardDefaultRate indexes
// the initial one. Groups change over seconds to minutes and each tick is
// several round trips (more when following a primary, and per group in
// all-groups view), so these start where Activity Monitor's end.
var agDashboardRates = []time.Duration{
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var agDashboardRateLabels = []string{"5 s", "10 s", "30 s", "60 s"}

const agDashboardDefaultRate = 1

// agDashboardTimeout bounds one refresh (like childFetchTimeout) so ticks don't
// queue.
const agDashboardTimeout = 30 * time.Second

// AGDashboard is an Always On dashboard hosted by layout.PanelManager, polling
// on its own goroutine until closed or disconnected.
type AGDashboard struct {
	app  *App
	conn *db.ServerConn // the registered connection; owned by App, not by this panel
	// agName is the watched group, empty for the all-groups view.
	agName string

	rect   core.Rect
	active bool

	// topGrid holds replicas (one group) or groups (all groups); bottomGrid
	// databases or replicas.
	topGrid    *controls.DataGrid
	bottomGrid *controls.DataGrid
	// topRect/bottomRect are the grids' bounds, for mouse routing (DataGrid has
	// no bounds accessor).
	topRect    core.Rect
	bottomRect core.Rect
	// topRows/bottomRows back the grids. Same-shape refreshes rewrite them in
	// place instead of SetData, which resets scroll and selection every poll.
	topRows    [][]string
	bottomRows [][]string

	// focusBottom picks the keyboard's grid; Tab flips it.
	focusBottom bool

	// snap is the last reading, err the last refresh failure. A failed refresh
	// keeps the previous reading rather than blanking.
	snap agSnapshot
	err  error

	// paused is atomic: written on the UI goroutine, read by the refresh
	// goroutine.
	paused atomic.Bool
	// kick forces an immediate refresh (F5), even while paused. Buffered, sent
	// non-blocking.
	kick chan struct{}
	// rateIdx indexes agDashboardRates (atomic, like paused). rateCh wakes the
	// goroutine so a rate change applies immediately.
	rateIdx atomic.Int32
	rateCh  chan struct{}
	cancel  context.CancelFunc
}

// agSnapshot is one reading, of one group or all.
type agSnapshot struct {
	group    *gosmo.AvailabilityGroup
	replicas []*gosmo.AvailabilityReplica
	dbs      []agDatabaseMetrics

	// groups is the all-groups reading; ok() tests it in that mode, since zero
	// groups is valid and must not show "Loading..." forever.
	groups   []agGroupRollup
	allGroup bool

	// followed records that the reading came through a peer connection to the
	// primary.
	followed bool
	at       time.Time
}

// ok reports whether the snapshot holds a reading.
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
	// safegoRepair: only run's refreshes replace the "Loading..." placeholders,
	// so a panic would leave them forever.
	app.safegoRepair("refreshing an Always On dashboard", d.refreshPanicked, func() { d.run(ctx) })
	return d
}

// refreshPanicked replaces the placeholders after a refresh-loop panic.
func (d *AGDashboard) refreshPanicked() {
	const msg = "Refresh stopped unexpectedly — see the log for details."
	d.topGrid.SetStatus(msg)
	d.bottomGrid.SetStatus(msg)
}

func (d *AGDashboard) Title() string {
	if d.allGroups() {
		return "Dashboard: Always On"
	}
	return "Dashboard: " + d.agName
}

func (d *AGDashboard) SetActive(v bool) { d.active = v }

// allGroups reports whether this is the all-groups view.
func (d *AGDashboard) allGroups() bool { return d.agName == "" }

// Close stops the refresh loop; the panel's context derives from the
// longer-lived connection's.
func (d *AGDashboard) Close() { cancelIfSet(d.cancel) }

// rate is the interval the panel is currently polling at.
func (d *AGDashboard) rate() time.Duration { return agDashboardRates[d.rateIdx.Load()] }

// setRate selects an interval by index, returning false out of range so the key
// stays unhandled.
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

// run is the refresh goroutine: read, then wait for tick, F5, rate change or
// close. A timer, since the interval can change mid-wait.
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
			// F5 overrides pause.
			d.refreshOnce(ctx)
		case <-d.rateCh:
			// A rate change only re-arms the wait; it takes no reading (none
			// while paused).
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

// read takes one reading through the group's primary.
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
// error but keeps the previous reading and timestamp.
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
	// Re-split now that the top grid's row count is known.
	d.SetBounds(d.rect.X, d.rect.Y, d.rect.W, d.rect.H)
}

// setRows updates a grid, calling SetData (which resets scroll and selection)
// only when the row count changes.
func (d *AGDashboard) setRows(g *controls.DataGrid, held *[][]string, columns []string, rows [][]string) {
	if len(rows) != len(*held) {
		*held = rows
		// resetGrid rather than SetData: fixed columns, so dragged widths
		// survive; the cursor resets since the rows differ.
		resetGrid(g, columns, rows, 0)
		return
	}
	for i := range rows {
		copy((*held)[i], rows[i])
	}
	g.RefreshColumnWidths()
	g.SetStatus(strconv.Itoa(len(rows)) + " rows")
}

// -- derived metrics -----------------------------------------------------------

// agDatabaseMetrics is one (database, replica) row with two derived figures SQL
// Server doesn't report. Both optional: unknown stays blank, since "no data
// loss" and "can't tell" are opposites.
type agDatabaseMetrics struct {
	DB *gosmo.AvailabilityDatabase

	// DataLoss is how far this secondary's last hardened commit trails the
	// primary's: what failing over now would lose.
	DataLoss    time.Duration
	HasDataLoss bool

	// RecoveryTime is how long this secondary's redo queue takes to drain at
	// its current rate: how long a failover takes to come online.
	RecoveryTime    time.Duration
	HasRecoveryTime bool
}

// agComputeDatabaseMetrics derives each secondary's data loss and recovery
// time. Needs the whole set: data loss compares against the primary's row for
// the same database.
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
			// A secondary can't lead its primary; a negative difference is
			// clock skew, not "-3s of data loss".
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
			// Nothing queued is a known zero, not unknown.
			m.HasRecoveryTime = true
		}
		out = append(out, m)
	}
	return out
}

// agReplicaIssues names what's wrong with a replica, worst first (disconnection
// explains the rest). Empty means healthy.
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
	// Only when nothing else is wrong: an old connect error on a connected
	// replica is history.
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

// agReplicaSyncSummary rolls the replica's databases into one sync state,
// listing each distinct state (as agDatabaseLabel does).
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

// agInt renders a queue or rate. A primary's 0 is real and shown.
func agInt(v int64) string { return strconv.FormatInt(v, 10) }

// agDuration renders a derived time, or an em dash when unknown — never "0s".
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

// HandleKey handles panel keys and passes the rest to the focused grid,
// returning its answer so app accelerators aren't swallowed.
func (d *AGDashboard) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyF5:
		d.forceRefresh()
		return true
	case tcell.KeyEnter:
		// Only from the group grid; Enter on the replica grid opens nothing.
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
			// Faster: a shorter interval, earlier in the list.
			return d.setRate(int(d.rateIdx.Load()) - 1)
		case '-', '_':
			return d.setRate(int(d.rateIdx.Load()) + 1)
		}
	}
	return d.grid().HandleKey(ev)
}

// forceRefresh requests an immediate reading, non-blocking.
func (d *AGDashboard) forceRefresh() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// HandleMouse routes to the clicked grid and moves focus with it, so wheel and
// keys act on the same grid.
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
	// A drag or release that started in a grid still belongs to it
	// (ARCHITECTURE.md § The mouseDragging idiom, invariant 5).
	if ev.Buttons() == tcell.ButtonNone {
		d.topGrid.HandleMouse(ev)
		d.bottomGrid.HandleMouse(ev)
	}
	return false
}
