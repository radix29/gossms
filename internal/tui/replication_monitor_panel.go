package tui

import (
	"strings"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// replication_monitor_panel.go is SSMS's Replication Monitor as one panel per
// server: the publications the instance distributes, the selected one's
// subscriptions and agents, the selected agent's sessions, and the selected
// session's actions with the error detail behind any that failed — four grids
// stacked, refreshed on a timer. Read-only, like the rest of replication
// (docs/decisions.md § Replication): nothing here starts, stops or
// reinitializes an agent.
//
// This file is the panel's state, construction and layout; reads and the
// refresh timer are in replication_monitor_panel_load.go, the toolbar and
// drawing in replication_monitor_panel_draw.go, input in
// replication_monitor_panel_input.go.
//
// It monitors the distribution databases on the instance it is opened on. A
// publisher whose distributor is elsewhere is monitored there, which the panel
// says instead of showing an empty grid.

// rmReadTimeout bounds one read. The monitor procedures refresh
// MSreplication_monitordata when it is stale, which on a busy distributor is
// not instant, but a panel that never comes back is worse.
const rmReadTimeout = 60 * time.Second

// rmRate is one entry of the Auto refresh selector; 0 is off.
type rmRate struct {
	label string
	every time.Duration
}

// rmRates are the refresh intervals offered, Off first.
var rmRates = []rmRate{
	{"Off", 0},
	{"5 s", 5 * time.Second},
	{"10 s", 10 * time.Second},
	{"30 s", 30 * time.Second},
	{"1 min", time.Minute},
}

// rmDefaultRateIdx is 10 s: often enough to watch an agent fail, and each tick
// is a handful of procedure calls on the distributor.
const rmDefaultRateIdx = 2

// rmWindow is one entry of the History selector: how far back the session
// list reads, 0 meaning every session history retention keeps.
type rmWindow struct {
	label string
	hours int
}

var rmWindows = []rmWindow{
	{"24 h", 24},
	{"2 d", 48},
	{"7 d", 7 * 24},
	{"All", 0},
}

// rmFocus names the grid with the keyboard, in Tab order.
type rmFocus int

const (
	rmFocusPubs rmFocus = iota
	rmFocusAgents
	rmFocusSessions
	rmFocusActions
	rmFocusCount
)

// rmDragZone names the sub-region owning a mouse gesture — see
// QueryPanel.dragZone for why one is needed at all.
type rmDragZone int

const (
	rmZoneNone rmDragZone = iota
	rmZoneSplit
	rmZoneGrid
	rmZoneToolbar
	// rmZoneUnclaimed is a press nothing wanted; it still owns the gesture
	// so the repeats while the button is held land nowhere.
	rmZoneUnclaimed
)

// ReplicationMonitorPanel is one server's Replication Monitor.
//
// Reads run on the server's shared pool: each is a short procedure call,
// bounded by rmReadTimeout, and the timer never starts a refresh while one is
// out (busy).
type ReplicationMonitorPanel struct {
	app  *App
	conn *db.ServerConn

	rect   core.Rect
	active bool

	rateIdx    int
	windowIdx  int
	errorsOnly bool

	// snap is the last publication/subscription/agent read; rows are the
	// publication grid's rows in its order, agentRows the agent grid's for the
	// selected publication, sessions and actions the two lower grids'.
	snap      rmSnapshot
	agentRows []rmAgentRow
	sessions  []rmSessionRow
	actions   []rmActionRow

	// updated is when snap was read, shown on the toolbar row.
	updated time.Time

	// want* are the selection the panel was asked to open on (a publication
	// node's Launch Replication Monitor), applied to the first read and then
	// cleared.
	wantPubDB, wantPub string

	pubsGrid     *controls.DataGrid
	agentsGrid   *controls.DataGrid
	sessionsGrid *controls.DataGrid
	actionsGrid  *controls.DataGrid
	// Three stacked splitters: publications | the rest, agents | the rest,
	// sessions | actions.
	splits [3]*layout.Splitter

	tools    controls.ToolRow
	toolRect core.Rect
	toolEnd  int

	focus rmFocus

	// busy latches Refresh while a snapshot read is out; released by the
	// callback the read posts, so every launch goes through safegoRepair. The
	// session and action reads go through it too: their repair replaces the
	// "Reading…" status a panic would otherwise leave on the pane.
	busy bool
	// One latest per pane: a refresh supersedes the snapshot read it replaces,
	// a cursor move the session or action read it moved past, and none
	// cancels another pane's.
	snapRead    latest
	sessionRead latest
	actionRead  latest
	// ticker is the refresh timer's run; Abandon stops it.
	ticker latest

	dragZone  rmDragZone
	dragSplit int
	dragGrid  *controls.DataGrid
}

// showReplicationMonitorFor opens Replication Monitor on sc — the Tools menu
// and the Replication folder's Launch Replication Monitor. One panel per
// server: asking again raises it. pubDB and pub, when set, select that
// publication (a publication node's menu); on a panel already open they
// select it now.
func (a *App) showReplicationMonitorFor(sc *db.ServerConn, pubDB, pub string) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		rm, ok := p.(*ReplicationMonitorPanel)
		return ok && rm.conn == sc
	})
	if idx < 0 {
		rm := NewReplicationMonitorPanel(a, sc)
		rm.wantPubDB, rm.wantPub = pubDB, pub
		idx = a.panels.AddPanel(rm)
		rm.Refresh()
		rm.startTicker()
	} else if pub != "" {
		a.panels.PanelAt(idx).(*ReplicationMonitorPanel).selectPublication(pubDB, pub)
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showReplicationMonitor is the Tools menu's entry: the active connection.
func (a *App) showReplicationMonitor() {
	if sc := a.connOrFirst(); sc != nil {
		a.showReplicationMonitorFor(sc, "", "")
	}
}

// NewReplicationMonitorPanel creates the panel for one server. Nothing is
// read until Refresh.
func NewReplicationMonitorPanel(app *App, sc *db.ServerConn) *ReplicationMonitorPanel {
	p := new(ReplicationMonitorPanel{
		app:          app,
		conn:         sc,
		rateIdx:      rmDefaultRateIdx,
		pubsGrid:     newQSGrid(app),
		agentsGrid:   newQSGrid(app),
		sessionsGrid: newQSGrid(app),
		actionsGrid:  newQSGrid(app),
	})
	p.splits[0] = layout.NewHorizontalSplitter("─── Subscriptions and agents of the selected publication ───")
	p.splits[1] = layout.NewHorizontalSplitter("─── Sessions of the selected agent ───")
	p.splits[2] = layout.NewHorizontalSplitter("─── Actions of the selected session ───")
	p.splits[0].SetRatio(0.25)
	p.splits[1].SetRatio(0.35)
	p.splits[2].SetRatio(0.45)
	// Each grid's move rebuilds or re-reads only the grids below it, never
	// itself — SetData from inside a grid's own OnSelectRow undoes the move
	// (see the redrawGrid rule).
	p.pubsGrid.OnSelectRow = func(int) { p.showAgents(false) }
	p.agentsGrid.OnSelectRow = func(int) { p.loadSessions(false) }
	p.sessionsGrid.OnSelectRow = func(int) { p.loadActions(false) }
	p.buildTools()
	p.setFocus(rmFocusPubs)
	return p
}

// Title returns the panel's tab title (Panel interface).
func (p *ReplicationMonitorPanel) Title() string {
	if p.conn != nil && p.conn.Opts.Server != "" {
		return "Replication Monitor — " + p.conn.Opts.Server
	}
	return "Replication Monitor"
}

// SetActive marks this panel focused (Activatable interface).
func (p *ReplicationMonitorPanel) SetActive(v bool) {
	p.active = v
	p.applyFocus()
	for _, s := range p.splits {
		s.SetActive(v)
	}
}

// grids lists the four grids in Tab order — rmFocus indexes it.
func (p *ReplicationMonitorPanel) grids() [rmFocusCount]*controls.DataGrid {
	return [rmFocusCount]*controls.DataGrid{p.pubsGrid, p.agentsGrid, p.sessionsGrid, p.actionsGrid}
}

// applyFocus keeps the grids' focus flags in step so only one draws a cursor.
func (p *ReplicationMonitorPanel) applyFocus() {
	for i, g := range p.grids() {
		g.Focus(p.active && rmFocus(i) == p.focus)
	}
}

func (p *ReplicationMonitorPanel) setFocus(f rmFocus) {
	p.focus = f
	p.applyFocus()
}

func (p *ReplicationMonitorPanel) focusedGrid() *controls.DataGrid { return p.grids()[p.focus] }

// Close stops the timer and every read (layout.Disposable).
func (p *ReplicationMonitorPanel) Close() {
	p.ticker.Abandon()
	p.snapRead.Cancel()
	p.sessionRead.Cancel()
	p.actionRead.Cancel()
}

// SetBounds positions the panel: the toolbar row, then the four grids on
// either side of the three splitters.
func (p *ReplicationMonitorPanel) SetBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	p.toolRect = core.Rect{}
	if h >= 1 {
		p.toolRect = core.Rect{X: x, Y: y, W: w, H: 1}
	}
	p.layoutTools()
	p.splits[0].SetBounds(x, y+1, w, h-1)
	p.layoutChildren()
}

// layoutChildren gives each grid its share below the toolbar, on every resize
// and after every splitter drag.
func (p *ReplicationMonitorPanel) layoutChildren() {
	place := func(g *controls.DataGrid, r core.Rect) { g.SetBounds(r.X, r.Y, r.W, r.H) }
	place(p.pubsGrid, p.splits[0].FirstRect())
	for i := 1; i < len(p.splits); i++ {
		r := p.splits[i-1].SecondRect()
		p.splits[i].SetBounds(r.X, r.Y, r.W, r.H)
	}
	place(p.agentsGrid, p.splits[1].FirstRect())
	place(p.sessionsGrid, p.splits[2].FirstRect())
	place(p.actionsGrid, p.splits[2].SecondRect())
}

// rmKey folds a replication name for matching: replication stores one
// server's name in whatever case @@SERVERNAME had when each row was written.
func rmKey(parts ...string) string { return strings.ToUpper(strings.Join(parts, "\x00")) }
