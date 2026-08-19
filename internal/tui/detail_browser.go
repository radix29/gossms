package tui

import (
	"context"
	"errors"
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DetailBrowser shows details of the selected Object Explorer node — a Panel
// wrapper around controls.DataGrid adding a title bar and the SQL-Server
// specific loading logic.
type DetailBrowser struct {
	rect   core.Rect
	title  string
	grid   *controls.DataGrid
	active bool

	// OnRefresh runs on a click of the title bar's refresh button — the same
	// action as F5 / Edit > Refresh. Nil makes the button a no-op.
	OnRefresh func()

	// refreshRect is the title bar's refresh button, positioned by
	// SetBounds; zero-width when the panel is too narrow to fit it.
	refreshRect core.Rect

	// mouseDragging distinguishes a fresh Button1 press on the refresh
	// button from a continued hold over it, like controls.Toolbar's field
	// of the same name.
	mouseDragging bool

	// seq guards a slow, superseded fetch from overwriting the grid with
	// results for a node no longer selected: incremented on every call, and
	// any async result is applied only if it is still the most recent.
	seq int

	// currentNode is the node ShowNodeDetails last displayed, so Invalidate can
	// tell whether to refetch immediately or just drop the cache entry.
	currentNode *explorerNode

	// cache holds the last successful fetch per node, so reselecting a node
	// already shown doesn't re-hit the network. Only a Refresh action or a
	// fresh node forces a refetch — a folder reload replaces its children with
	// new *explorerNode values, which naturally miss the cache. Never holds
	// partial results, only final ones.
	cache map[*explorerNode]*detailResult

	// pending records per node the seq of the most recent fetch dispatched for
	// it — set by fetch, checked by postFinal/cacheOnly before writing cache.
	// Reselecting a node mid-fetch dispatches a second fetch for the same node
	// pointer; without this whichever finished last wins the cache write.
	pending map[*explorerNode]int
}

// detailResult is a cached or in-flight-result payload for one node.
type detailResult struct {
	cols []string
	rows [][]string
	err  error
}

// NewDetailBrowser creates a detail browser.
func NewDetailBrowser(title string) *DetailBrowser {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	return new(DetailBrowser{
		title:   title,
		grid:    grid,
		cache:   make(map[*explorerNode]*detailResult),
		pending: make(map[*explorerNode]int),
	})
}

// Title returns the panel title (Panel interface).
func (db *DetailBrowser) Title() string { return db.title }

// refreshButtonLabel is drawn at the right end of the title bar and
// clicking it runs OnRefresh.
const refreshButtonLabel = "[⟳]"

// SetBounds positions the panel, reserving the first row for the title bar
// and its right-aligned refresh button.
func (db *DetailBrowser) SetBounds(x, y, w, h int) {
	db.rect = core.Rect{X: x, Y: y, W: w, H: h}
	db.grid.SetBounds(x, y+1, w, h-1)

	bw := core.DisplayWidth(refreshButtonLabel)
	if w >= bw+4 {
		db.refreshRect = core.Rect{X: x + w - bw - 1, Y: y, W: bw, H: 1}
	} else {
		db.refreshRect = core.Rect{}
	}
}

// SetActive marks this panel focused (affects title bar colour).
func (db *DetailBrowser) SetActive(v bool) { db.active = v }

// Closable reports false: Object Explorer Details is a fixed, always-present
// panel, so the tab bar's [x] and Ctrl+W can't close it.
func (db *DetailBrowser) Closable() bool { return false }

// ShowNodeDetails loads detail data for node asynchronously: every fetch is a
// network round trip and this fires on every tree-selection change, so running
// it inline would freeze the app on each arrow key against a slow server. A
// node already shown is served from cache. Nil-safe like Invalidate, so a
// minimal test App without a DetailBrowser can still exercise onNodeSelected.
func (db *DetailBrowser) ShowNodeDetails(app *App, node *explorerNode) {
	if db == nil {
		return
	}
	db.seq++
	seq := db.seq
	db.currentNode = node

	if node == nil {
		db.showEmpty()
		return
	}

	db.title = fmt.Sprintf("Object Explorer Details — %s", node.label)
	sc := resolveConn(node)

	if !app.isConnected(sc) {
		db.grid.SetFillLastColumn(true)
		db.grid.SetData([]string{"Property", "Value"}, [][]string{{"Status", "Not connected"}})
		return
	}

	if cached, ok := db.cache[node]; ok {
		db.applyResult(cached)
		return
	}

	db.grid.SetStatus("Loading...")
	db.fetch(app, sc, node, seq)
}

// showEmpty resets the panel to its nothing-selected state.
func (db *DetailBrowser) showEmpty() {
	db.title = "Object Explorer Details"
	db.grid.SetFillLastColumn(false)
	db.grid.SetData([]string{"Name", "Type"}, nil)
}

// applyResult renders a completed (cached or freshly finished) result.
func (db *DetailBrowser) applyResult(r *detailResult) {
	if r.err != nil {
		db.grid.SetError(r.err)
		return
	}
	db.grid.SetFillLastColumn(isPropertyValueColumns(r.cols))
	db.grid.SetData(r.cols, r.rows)
}

// isPropertyValueColumns reports whether cols is the Property/Value shape of a
// single-record detail view rather than a list of similar rows — which decides
// whether the Value column stretches to fill the panel.
func isPropertyValueColumns(cols []string) bool {
	return len(cols) == 2 && cols[0] == "Property" && cols[1] == "Value"
}

// Invalidate drops any cached detail data for node — called by every Refresh
// action so a forced refresh reaches the Detail Browser, not just the tree. A
// node currently on screen is refetched immediately rather than on reselect.
// Nil-safe, so call sites need no nil check of their own.
func (db *DetailBrowser) Invalidate(app *App, node *explorerNode) {
	if db == nil {
		return
	}
	delete(db.cache, node)
	delete(db.pending, node)
	if db.currentNode == node {
		db.ShowNodeDetails(app, node)
	}
}

// RefreshCurrent re-fetches whatever node the panel is showing, independently
// of the tree's selection — what the title bar's refresh button runs. Keying
// off the panel's own currentNode keeps it correct if the panel ever drills on
// its own. Nil-safe like Invalidate.
func (db *DetailBrowser) RefreshCurrent(app *App) {
	if db == nil || db.currentNode == nil {
		return
	}
	db.Invalidate(app, db.currentNode)
}

// PurgeConn drops every cached and pending entry belonging to sc — called by
// App.disconnect, since the nodes they are keyed by are about to leave the
// tree. Without it both maps grow for the session's life, holding every
// disconnected server's node pointers and result rows alive. Nil-safe.
func (db *DetailBrowser) PurgeConn(sc *dbconn.ServerConn) {
	if db == nil {
		return
	}
	for node := range db.cache {
		if resolveConn(node) == sc {
			delete(db.cache, node)
		}
	}
	for node := range db.pending {
		if resolveConn(node) == sc {
			delete(db.pending, node)
		}
	}
	if db.currentNode != nil && resolveConn(db.currentNode) == sc {
		// Disconnecting the last server empties the tree and fires no
		// OnSelect, so nothing else would repaint the grid and it would keep
		// showing the disconnected server's rows. Bumping seq also drops any
		// fetch still in flight.
		db.currentNode = nil
		db.seq++
		db.showEmpty()
	}
}

// fetch dispatches to a per-node-type loader. Types worth more than one round
// trip (NodeServer, NodeDatabases, NodeLogins) show their fast fields first and
// backfill progressively; the rest go through the single-shot
// fetchNodeDetails.
func (db *DetailBrowser) fetch(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	db.pending[node] = seq
	switch node.data.Type {
	case NodeServer:
		db.loadServerDetails(app, sc, node, seq)
	case NodeDatabases:
		db.loadDatabasesFolderDetails(app, sc, node, seq)
	case NodeLogins:
		db.loadLoginsDetails(app, sc, node, seq)
	case NodeTables:
		db.loadTablesFolderDetails(app, sc, node, seq)
	default:
		// The fetch reads a snapshot, never the live node — see
		// explorerNode.snapshot. node itself stays behind as the identity
		// postFinal and panicRepair key off, both on the UI goroutine.
		snap := node.snapshot()
		app.safegoRepair("loading Object Explorer details", db.panicRepair(node, seq), func() {
			ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
			defer cancel()
			cols, rows, err := fetchNodeDetails(ctx, sc, snap)
			db.postFinal(app, node, seq, cols, rows, err)
		})
	}
}

// errDetailFetchPanicked is what the panel shows when a detail loader
// panicked. The stack is already in the log by then — see reportPanic.
var errDetailFetchPanicked = errors.New("loading failed unexpectedly — see the log for details")

// panicRepair builds the safegoRepair step every loader in fetch shares: the
// panel is latched at "Loading..." (or a progressive loader's placeholder rows)
// and only postFinal clears it, which a panic never reaches.
//
// Nothing is cached. A panic says nothing about this node's details, so
// dropping the pending entry lets the next selection retry rather than serving
// the failure forever — unlike postFinal, which caches an ordinary error
// because the server answered.
//
// Both of postFinal's guards are kept, for the same reasons: pending is cleared
// only if this fetch is still the newest for the node, and the grid touched
// only if its node is still selected.
func (db *DetailBrowser) panicRepair(node *explorerNode, seq int) func() {
	return func() {
		if db.pending[node] == seq {
			delete(db.pending, node)
		}
		if seq != db.seq {
			return
		}
		db.grid.SetError(errDetailFetchPanicked)
	}
}

// postPartial displays cols/rows immediately if node/seq is still current,
// without caching — used by progressive loaders for their fast-arriving
// first stage, before slower fields have landed.
func (db *DetailBrowser) postPartial(app *App, seq int, cols []string, rows [][]string) {
	app.postAndWake(func() {
		if seq != db.seq {
			return
		}
		db.grid.SetFillLastColumn(isPropertyValueColumns(cols))
		db.grid.SetData(cols, rows)
	})
}

// postFinal caches the completed result for node — unless a newer fetch for it
// has been dispatched since, making this one stale — and displays it if still
// current. Called once per fetch, single-shot or last progressive stage.
func (db *DetailBrowser) postFinal(app *App, node *explorerNode, seq int, cols []string, rows [][]string, err error) {
	result := &detailResult{cols: cols, rows: rows, err: err}
	app.postAndWake(func() {
		if db.pending[node] == seq {
			db.cache[node] = result
		}
		if seq != db.seq {
			return
		}
		db.applyResult(result)
	})
}

// cacheOnly caches the completed result without touching the grid — for a
// progressive loader's last stage, once every row has been updated in place and
// postFinal's SetData would reset the user's scroll position. Gated by pending
// the same way postFinal is.
func (db *DetailBrowser) cacheOnly(app *App, node *explorerNode, seq int, cols []string, rows [][]string, err error) {
	app.postAndWake(func() {
		if db.pending[node] == seq {
			db.cache[node] = &detailResult{cols: cols, rows: rows, err: err}
		}
	})
}

// maxRowFetchConcurrency bounds how many per-row backfill goroutines one
// progressive loader runs at once. Unbounded, a folder with hundreds of entries
// opens hundreds of connections against a pool whose MaxOpenConns is 20 and
// queues a redraw for each; capping keeps one slow row from blocking the rest.
//
// The bound is per loader, not per connection: each backfillRows call runs its
// own pool, so two folders loading at once fan out to 16 — still inside the
// pool, but the headroom is two loaders, not more.
const maxRowFetchConcurrency = 8

// fetchNodeDetails runs the gosmo queries for a node's detail grid. Called from
// a background goroutine, so it must not touch DetailBrowser or any other UI
// state — only return data for the caller to apply via postAndWake. ctx bounds
// the whole call, so a hung server times the goroutine out rather than blocking
// forever.
func fetchNodeDetails(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	switch node.data.Type {
	case NodeAgentJobs:
		return agentServerDetail(ctx, sc)
	case NodeAgentJob:
		return agentJobDetail(ctx, sc, node)
	case NodeAgentSchedule:
		return agentScheduleDetail(ctx, sc, node)
	case NodeAgentAlert:
		return agentAlertDetail(ctx, sc, node)
	case NodeAgentOperator:
		return agentOperatorDetail(ctx, sc, node)
	case NodeAgentJobActivity:
		return agentJobActivityDetail(ctx, sc)
	case NodeAgentJobHistory:
		return agentJobHistoryDetail(ctx, sc)
	case NodeAgentJobCategories:
		return agentJobCategoriesDetail(ctx, sc)
	case NodeAgentAlertCategories:
		return agentAlertCategoriesDetail(ctx, sc)
	case NodeAgentReport:
		return agentReportDetail(ctx, sc, node.data.Name)
	case NodeSQLServerLogs:
		return errorLogFilesDetail(ctx, sc, gosmo.ErrorLogSQLServer)
	case NodeAgentErrorLogs:
		return errorLogFilesDetail(ctx, sc, gosmo.ErrorLogAgent)
	case NodeSQLServerLog, NodeAgentErrorLog:
		return errorLogFileDetail(ctx, sc, node)
	case NodeSystemDatabases:
		dbs, err := sc.Server.DatabasesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		dbs = filterObjects(node.data.Filter, dbs, func(d *gosmo.Database) nodeData {
			return nodeData{Name: d.Name(), CreateDate: d.CreateDate()}
		})
		rows := make([][]string, 0, 4)
		for _, d := range dbs {
			if d.IsSystem() {
				rows = append(rows, []string{d.Name(), d.State(), string(d.RecoveryModel())})
			}
		}
		return []string{"Name", "State", "Recovery"}, rows, nil

	case NodeDatabase:
		d, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		sizeStr, dataStr, logStr, availLogStr := "N/A", "N/A", "N/A", "N/A"
		if space, err := d.SpaceUsedContext(ctx); err == nil {
			sizeStr, dataStr, logStr = formatMB(space.TotalMB), formatMB(space.DataMB), formatMB(space.LogMB)
			availLogStr = formatMB(space.AvailLogMB)
		}
		return []string{"Property", "Value"}, [][]string{
			{"Name", d.Name()},
			{"State", d.State()},
			{"Recovery Model", string(d.RecoveryModel())},
			{"Compatibility Level", fmt.Sprintf("%d", d.CompatibilityLevel())},
			{"Collation", d.Collation()},
			{"Create Date", formatSQLDate(d.CreateDate())},
			{"Read Only", fmt.Sprintf("%v", d.IsReadOnly())},
			{"Size (MB)", sizeStr},
			{"Data (MB)", dataStr},
			{"Log (MB)", logStr},
			{"Avail. Log (MB)", availLogStr},
		}, nil

	case NodeViews:
		dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		views, err := dbObj.ViewsContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		views = filterObjects(node.data.Filter, views, func(v *gosmo.View) nodeData {
			return nodeData{Name: v.Name, Schema: v.Schema, CreateDate: v.CreateDate}
		})
		rows := make([][]string, 0, len(views))
		for _, v := range views {
			rows = append(rows, []string{v.Schema + "." + v.Name, formatSQLDate(v.CreateDate)})
		}
		return []string{"Name", "Created"}, rows, nil

	case NodeStoredProcedures:
		dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
		if err != nil {
			return nil, nil, err
		}
		procs, err := dbObj.StoredProceduresContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		procs = filterObjects(node.data.Filter, procs, func(p *gosmo.StoredProcedure) nodeData {
			return nodeData{Name: p.Name, Schema: p.Schema, CreateDate: p.CreateDate}
		})
		rows := make([][]string, 0, len(procs))
		for _, p := range procs {
			rows = append(rows, []string{p.Schema + "." + p.Name, formatSQLDate(p.CreateDate), formatSQLDate(p.ModifyDate)})
		}
		return []string{"Name", "Created", "Modified"}, rows, nil

	case NodePartitionFunction, NodePartitionScheme, NodeSecurityPolicy,
		NodeColumnMasterKey, NodeColumnEncryptionKey:
		return storageSecurityDetail(ctx, sc, node)

	default:
		if hasChildren(node.data.Type) {
			return fetchChildObjectsDetail(ctx, sc, node)
		}
		return []string{"Property", "Value"}, [][]string{
			{"Name", node.label},
			{"Type", nodeTypeName(node.data.Type)},
			{"Database", node.data.DBName},
			{"Schema", node.data.Schema},
		}, nil
	}
}

// fetchChildObjectsDetail is the fallback detail view for a node type with
// children but no purpose-built view: it lists the child objects. Reuses the
// childLoaders entry the tree expands with, so a new NodeType wired in gets a
// folder-shaped detail view rather than the leaf-style Property/Value grid.
// Since it holds *explorerNode it applies the folder's filter through
// filterChildren, not the filterObjects the gosmo-shaped loaders use.
func fetchChildObjectsDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	loader, ok := childLoaders[node.data.Type]
	if !ok {
		return []string{"Name"}, nil, nil
	}
	children, err := loader(loaderCtx{ctx: ctx, sc: sc}, node)
	if err != nil {
		return nil, nil, err
	}
	children = filterChildren(node.data.Filter, children)
	rows := make([][]string, 0, len(children))
	for _, c := range children {
		rows = append(rows, []string{c.label})
	}
	return []string{"Name"}, rows, nil
}

// Draw renders the title bar and the data grid.
func (db *DetailBrowser) Draw(s tcell.Screen) {
	p := theme.Active()
	titleStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	if db.active {
		titleStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(color.White).Bold(true)
	}
	core.FillRect(s, core.Rect{X: db.rect.X, Y: db.rect.Y, W: db.rect.W, H: 1}, ' ', titleStyle)
	titleW := db.rect.W - 2
	if db.refreshRect.W > 0 {
		titleW = db.refreshRect.X - db.rect.X - 2
		core.DrawText(s, db.refreshRect.X, db.refreshRect.Y, titleStyle, refreshButtonLabel)
	}
	core.DrawTextClipped(s, db.rect.X+1, db.rect.Y, titleW, titleStyle, db.title)

	db.grid.Draw(s)
	db.grid.DrawOverlay(s)
}

// HandleKey delegates to the data grid.
func (db *DetailBrowser) HandleKey(ev *tcell.EventKey) bool { return db.grid.HandleKey(ev) }

// HandleMouse fires OnRefresh for a press on the title bar's refresh button and
// delegates everything else to the grid. A release over the button still
// reaches the grid, so its mouseDragging latch can't stick.
func (db *DetailBrowser) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		db.mouseDragging = false
	}
	mx, my := ev.Position()
	if db.refreshRect.Contains(mx, my) {
		if ev.Buttons() == tcell.Button1 && !db.mouseDragging {
			db.mouseDragging = true
			if db.OnRefresh != nil {
				db.OnRefresh()
			}
		}
		db.grid.HandleMouse(ev)
		return true
	}
	return db.grid.HandleMouse(ev)
}

// HasSelection, SelectedText, Cut, Paste and SelectAll implement
// clipboardTarget by forwarding to the grid, which is itself a real clipboard
// target only while its "Show Value" content viewer is open.
func (db *DetailBrowser) HasSelection() bool   { return db.grid.HasSelection() }
func (db *DetailBrowser) SelectedText() string { return db.grid.SelectedText() }
func (db *DetailBrowser) Cut() string          { return db.grid.Cut() }
func (db *DetailBrowser) Paste(text string)    { db.grid.Paste(text) }
func (db *DetailBrowser) SelectAll()           { db.grid.SelectAll() }
