package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/planview"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// live_plan_panel.go is Activity Monitor's Show Live Execution Plan: the live
// plan of another session's running query, read-only. It is the Live Query
// Statistics view (query_panel_live.go) pointed at a session goSSMS does not
// run, so there is no actual plan to hand over to at the end, and the
// session is profiled only if something else made it so — lightweight
// profiling, on by default from SQL Server 2019, or the session's own
// STATISTICS XML/PROFILE (another goSSMS with Live on, SSMS's actual plan).

// LivePlanPanel watches one session's running query in a live plan view until
// it is closed. It polls whatever the session runs next too: a batch moving
// to its next statement, or a new query after an idle spell.
type LivePlanPanel struct {
	app       *App
	sc        *db.ServerConn
	sessionID int
	info      *gosmo.ServerInfo // what the idle note's advice depends on
	rect      core.Rect
	planView  *planview.PlanView
	active    bool

	// run is the poller, cancelled by Close or by disconnecting.
	run latest
	// seen is set once a plan has been shown; state is the title bar's word
	// on what the view shows.
	seen  bool
	state string
}

// liveSessionRefusedNote is the view's note when the server refuses the DMV.
const liveSessionRefusedNote = "No live plan: reading sys.dm_exec_query_profiles needs VIEW SERVER STATE " +
	"(VIEW DATABASE STATE on Azure SQL Database)."

// lightweightProfilingByDefault reports whether every query on the server is
// profiled without being asked: lightweight profiling v3, on by default from
// SQL Server 2019 (major 15) and on Azure. No server info answers yes, per
// gate.AllowsOn's fail-open rule.
func lightweightProfilingByDefault(info *gosmo.ServerInfo) bool {
	return info == nil || info.IsAzure() || info.VersionMajor >= 15
}

// liveSessionIdleNote says why session spid shows no plan, and keeps waiting:
// it may simply be idle, but on 2016/2017 a query is only in the DMV when
// something turned profiling on for it.
func liveSessionIdleNote(info *gosmo.ServerInfo, spid int) string {
	if lightweightProfilingByDefault(info) {
		return fmt.Sprintf("Session %d is not running a query right now, or its database has "+
			"LIGHTWEIGHT_QUERY_PROFILING turned off. Waiting for one…", spid)
	}
	return fmt.Sprintf("Session %d is not running a profiled query. Before SQL Server 2019 a query "+
		"shows here only when profiling is on for it: run with an actual plan (SET STATISTICS XML "+
		"or PROFILE ON, as Live Query Statistics does), trace flag 7412, or an Extended Events "+
		"session on query_thread_profile. Waiting for one…", spid)
}

// openLivePlanPanel shows session spid's live plan, bringing forward a panel
// already watching it on sc rather than opening a second poller.
func (a *App) openLivePlanPanel(sc *db.ServerConn, spid int) {
	for i := range a.panels.Count() {
		if lp, ok := a.panels.PanelAt(i).(*LivePlanPanel); ok && lp.sc == sc && lp.sessionID == spid {
			a.panels.SetActive(i)
			a.focusPanels()
			return
		}
	}
	lp := newLivePlanPanel(a, sc, spid)
	a.panels.SetActive(a.panels.AddPanel(lp))
	a.focusPanels()
	lp.start()
}

func newLivePlanPanel(app *App, sc *db.ServerConn, spid int) *LivePlanPanel {
	v := planview.New()
	v.OnCopyRequest = app.copyWithStatus
	v.SetLive(nil, nil)
	lp := new(LivePlanPanel{app: app, sc: sc, sessionID: spid, planView: v, state: "waiting"})
	if sc != nil && sc.Server != nil {
		lp.info = sc.Server.Info()
	}
	return lp
}

// start polls the session on sc's pool until Close or disconnect.
func (lp *LivePlanPanel) start() {
	srv := lp.sc.Server
	ctx, token := lp.run.Begin(srv.Context())
	spid := lp.sessionID
	lp.app.safego("polling a live execution plan", func() {
		pollLiveStats(ctx, srv, spid, nil, livePollTiming, func(u liveUpdate) {
			lp.app.postAndWake(func() { lp.apply(token, u) })
		})
	})
}

// apply puts one poll's result on the view. A query that ends leaves its last
// reading up: unlike a query panel's run there is no actual plan to follow.
func (lp *LivePlanPanel) apply(token int, u liveUpdate) {
	if !lp.run.Current(token) {
		return
	}
	switch {
	case u.note != "":
		lp.planView.SetLiveNote(u.note)
		lp.state = "not available"
		lp.app.setStatus(u.note)
	case u.idle && !lp.seen:
		lp.planView.SetLiveNote(liveSessionIdleNote(lp.info, lp.sessionID))
	case u.idle:
		lp.state = "no query running — last reading shown"
	default:
		lp.seen = true
		lp.state = "running"
		lp.planView.SetLiveNote("")
		lp.planView.SetLive(u.plan, u.counters)
	}
}

// Close stops the poller (layout.Disposable).
func (lp *LivePlanPanel) Close() { lp.run.Abandon() }

// Title returns the panel's tab/window title (Panel interface).
func (lp *LivePlanPanel) Title() string {
	return fmt.Sprintf("Live Execution Plan — session %d", lp.sessionID)
}

// SetBounds positions the panel, reserving the first row for the title bar.
func (lp *LivePlanPanel) SetBounds(x, y, w, h int) {
	lp.rect = core.Rect{X: x, Y: y, W: w, H: h}
	lp.planView.SetBounds(x, y+1, w, h-1)
}

// SetActive marks this panel focused (affects title bar colour).
func (lp *LivePlanPanel) SetActive(v bool) {
	lp.active = v
	lp.planView.SetActive(v)
}

// Draw renders the title bar, with the view's state after the title, and the
// wrapped PlanView.
func (lp *LivePlanPanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	if lp.active {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
	}
	core.FillRect(s, core.Rect{X: lp.rect.X, Y: lp.rect.Y, W: lp.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, lp.rect.X+1, lp.rect.Y, lp.rect.W-2, titleStyle, lp.Title()+" · "+lp.state)
	lp.planView.Draw(s)
	// Drawn last — see the "overlays drawn last" rule in tuikit/README.md.
	lp.planView.DrawOverlay(s)
}

// HandleKey delegates to the wrapped PlanView.
func (lp *LivePlanPanel) HandleKey(ev *tcell.EventKey) bool { return lp.planView.HandleKey(ev) }

// HandleMouse delegates to the wrapped PlanView.
func (lp *LivePlanPanel) HandleMouse(ev *tcell.EventMouse) bool { return lp.planView.HandleMouse(ev) }

// HasSelection and the rest of clipboardTarget forward to the PlanView, as
// PlanPanel's do.
func (lp *LivePlanPanel) HasSelection() bool   { return lp.planView.HasSelection() }
func (lp *LivePlanPanel) SelectedText() string { return lp.planView.SelectedText() }
func (lp *LivePlanPanel) Cut() string          { return lp.planView.Cut() }
func (lp *LivePlanPanel) Paste(text string)    { lp.planView.Paste(text) }
func (lp *LivePlanPanel) SelectAll()           { lp.planView.SelectAll() }
