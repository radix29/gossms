package tui

import (
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer.go is SSMS's Extended Events viewer: Watch Live Data on a
// session, or View Target Data on one of its targets, as a grid of events
// over a details pane. This file is the panel's state, construction and
// layout; the toolbar is in xevent_viewer_toolbar.go, the reads in
// xevent_viewer_feed.go, the grid's rows and the filter in
// xevent_viewer_rows.go, Grouping and Aggregation in xevent_viewer_group.go,
// Find and bookmarks in xevent_viewer_find.go, Export and the saved display
// settings in xevent_viewer_export.go, drawing in xevent_viewer_draw.go and
// input in xevent_viewer_input.go. The event store itself is internal/xevent.
//
// Live data is polled from the session's event_file (or ring_buffer) target
// rather than streamed — D1 in docs/xevents-plan.md, recorded in
// docs/decisions.md § Extended Events.

// XEventViewer is one Extended Events viewer panel.
//
// It reads on a connection of its own (db.RoleXEventProfiler), opened when
// the panel is and closed with it: a live feed polls every second, and a
// first read of a big event_file set runs for seconds, neither of which may
// queue ahead of Object Explorer on the host connection.
type XEventViewer struct {
	app *App
	// host is the Object Explorer connection the panel was opened from: the
	// server it reads, the connection a query window it opens attaches to,
	// and what reuse-if-open compares.
	host *db.ServerConn
	// conn is the panel's own connection, nil until it has connected and
	// after Close.
	conn *db.ServerConn

	session string
	// scope is where the session lives: the server, or an Azure SQL
	// Database's database (xeScope).
	scope xeScope
	// target is the target View Target Data reads (event_file, ring_buffer);
	// empty for Watch Live Data, which picks one itself.
	target string
	live   bool
	// files is the pattern of a Merge Extended Event Files viewer, which reads
	// those files and no session (session is empty); see xevent_merge.go.
	files string
	// profiler marks a viewer the XEvent Profiler opened: closing it offers
	// to stop the session, which the Profiler started (D2).
	profiler bool

	rect   core.Rect
	active bool

	store *xevent.Store
	// filter is the client-side filter in force, nil for none.
	filter *xevent.Filter
	// hiddenCols is the Choose Columns state: column keys (xeColumnKey) not shown.
	hiddenCols map[string]bool
	// columns are the grid's columns right now — the store's minus hiddenCols —
	// and shown the events the grid is built from, oldest first: the store's
	// events that pass the filter. The grid reads both through xeRowSource,
	// so appending to shown grows the grid without rebuilding it.
	columns []xevent.Column
	shown   []*xevent.Event

	// groupBy are the Grouping columns, outermost first, and aggs the
	// Aggregation shown on each group row; groups are shown grouped, and
	// display the grid's rows while grouped — group rows and the events of
	// expanded groups — nil otherwise, when the grid's rows are shown itself.
	// expanded holds the Keys of the expanded groups. See
	// xevent_viewer_group.go.
	groupBy  []xevent.Column
	aggs     []xevent.Aggregate
	groups   []*xevent.Group
	display  []xeDisplayRow
	expanded map[string]bool

	// bookmarks are the bookmarked events' IDs; find the text Find last
	// looked for. See xevent_viewer_find.go.
	bookmarks map[uint64]bool
	find      string

	grid     *controls.DataGrid
	splitter *layout.Splitter

	toolRect   core.Rect
	gridRect   core.Rect
	detailRect core.Rect
	tools      []toolButton
	toolIDs    []xeTool // what each of tools is; the cells differ by mode
	more       toolButton
	overflow   []int // toolbar cells folded into "More ▾"

	detailScroll     int
	detailCache      []string
	detailCacheEvent *xevent.Event
	detailCacheWidth int

	feed xeFeedState

	dragZone xeDragZone
}

// xeDragZone names the sub-region owning the mouse gesture in progress — see
// QueryPanel.dragZone.
type xeDragZone int

const (
	xZoneNone xeDragZone = iota
	xZoneSplitter
	xZoneGrid
	xZoneToolbar
	xZoneUnclaimed
)

// NewXEventViewer makes the panel. live picks Watch Live Data; otherwise it
// is View Target Data on target. Nothing is read until connectXEventViewer
// has opened the panel's connection.
func NewXEventViewer(app *App, host *db.ServerConn, session, target string, live bool) *XEventViewer {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	grid.SetStatusStyle(resultsStatusStyle)
	grid.OnCopyRequest = app.copyWithStatus
	grid.SetMaxCellWidth(app.cfg.MaxCellLength + 2)
	v := new(XEventViewer{
		app:        app,
		host:       host,
		session:    session,
		target:     target,
		live:       live,
		store:      xevent.NewStore(config.ClampXEventStoreCapacity(app.cfg.XEventStoreCapacity)),
		hiddenCols: loadXEHiddenColumns(app, session),
		expanded:   map[string]bool{},
		bookmarks:  map[uint64]bool{},
		grid:       grid,
		splitter:   layout.NewHorizontalSplitter("─── Event details ─── (drag or Ctrl+Up/Down to resize)"),
	})
	v.feed.autoScroll = live
	grid.OnShowValue = v.showValue
	grid.OnMenuItems = v.cellMenuItems
	v.splitter.SetRatio(0.65)
	v.buildTools()
	v.rebuildRows()
	return v
}

// Title returns the panel's tab title (Panel interface), SSMS's own: the
// session and "Live Data", or the session and the target.
func (v *XEventViewer) Title() string {
	if v.files != "" {
		return "Merged: " + v.files
	}
	session := v.session
	if v.scope.db != "" {
		session = v.scope.db + "." + session
	}
	if v.live {
		return session + ": Live Data"
	}
	return session + ": " + v.target
}

// SetActive marks this panel focused (Activatable interface).
func (v *XEventViewer) SetActive(on bool) {
	v.active = on
	v.grid.Focus(on)
	v.splitter.SetActive(on)
}

// Close stops the feed and closes the panel's connection. Called from
// App.closePanelAt.
func (v *XEventViewer) Close() {
	v.stopFeed()
	if v.conn != nil {
		v.conn.Close()
		v.conn = nil
	}
}

// SetBounds positions the panel: the toolbar row, then the grid and the
// details pane either side of the splitter.
func (v *XEventViewer) SetBounds(x, y, w, h int) {
	v.rect = core.Rect{X: x, Y: y, W: w, H: h}
	if h >= 1 {
		v.toolRect = core.Rect{X: x, Y: y, W: w, H: 1}
	} else {
		v.toolRect = core.Rect{}
	}
	v.layoutTools()
	v.splitter.SetBounds(x, y+1, w, h-1)
	v.layoutChildren()
}

// layoutChildren gives the grid and the details pane their halves, on every
// resize and after every splitter drag.
func (v *XEventViewer) layoutChildren() {
	v.gridRect = v.splitter.FirstRect()
	v.detailRect = v.splitter.SecondRect()
	v.grid.SetBounds(v.gridRect.X, v.gridRect.Y, v.gridRect.W, v.gridRect.H)
}

// layoutTools places the toolbar cells, folding whatever doesn't fit into
// "More ▾".
func (v *XEventViewer) layoutTools() {
	v.overflow, _ = layoutToolButtonsOverflow(v.tools, v.toolRect, "", &v.more)
}
