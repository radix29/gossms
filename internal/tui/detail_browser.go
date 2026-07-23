package tui

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v3"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DetailBrowser shows details of the selected object-explorer node.
// It is a thin Panel wrapper around a tuikit controls.DataGrid, adding a
// title bar and the SQL-Server-specific data loading logic.
type DetailBrowser struct {
	rect   core.Rect
	title  string
	grid   *controls.DataGrid
	active bool

	// seq guards against a slow, superseded fetch (see ShowNodeDetails)
	// overwriting the grid with results for a node that's no longer
	// selected — incremented on every call, and any async result (partial
	// or final) is only applied if it's still the most recent one
	// requested.
	seq int

	// currentNode is the node ShowNodeDetails last displayed — tracked so
	// Invalidate can tell whether the node it's invalidating needs an
	// immediate refetch (it's what's on screen right now) or can just be
	// dropped from cache for next time it's selected.
	currentNode *explorerNode

	// cache holds the completed result of the last successful fetch per
	// node, so reselecting a node already shown doesn't re-hit the network —
	// only a Refresh action (see Invalidate) or a fresh node (a folder
	// reload replaces its children with new *explorerNode values, which
	// naturally miss the cache) forces a refetch. Never populated with
	// partial/in-progress results, only the final one.
	cache map[*explorerNode]*detailResult
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
	return new(DetailBrowser{title: title, grid: grid, cache: make(map[*explorerNode]*detailResult)})
}

// Title returns the panel title (Panel interface).
func (db *DetailBrowser) Title() string { return db.title }

// SetBounds positions the panel, reserving the first row for the title bar.
func (db *DetailBrowser) SetBounds(x, y, w, h int) {
	db.rect = core.Rect{X: x, Y: y, W: w, H: h}
	db.grid.SetBounds(x, y+1, w, h-1)
}

// SetActive marks this panel focused (affects title bar colour).
func (db *DetailBrowser) SetActive(v bool) { db.active = v }

// Closable reports false: Object Explorer Details is a fixed, always-
// present panel (there is exactly one, added once in App.buildUI) and
// can't be closed via the tab bar's [x] or Ctrl+W — see layout.Closable.
func (db *DetailBrowser) Closable() bool { return false }

// ShowNodeDetails loads detail data for the given explorer node,
// asynchronously — every fetch here is a real network round trip, and this
// fires on every tree-selection change, so running it inline on the UI
// goroutine would freeze the whole app on each arrow-key press against a
// slow or remote server. A node already shown once is served from cache
// instead of refetching — see Invalidate for how a Refresh action forces a
// fresh copy. Nil-safe like Invalidate, so a minimal test App built without
// a DetailBrowser (see newTestApp in app_connections_test.go) can still
// exercise ObjectExplorer/onNodeSelected paths that now call this on every
// selection change (including AddRoot selecting a newly connected server's
// root) without wiring one up.
func (db *DetailBrowser) ShowNodeDetails(app *App, node *explorerNode) {
	if db == nil {
		return
	}
	db.seq++
	seq := db.seq
	db.currentNode = node

	if node == nil {
		db.title = "Object Explorer Details"
		db.grid.SetFillLastColumn(false)
		db.grid.SetData([]string{"Name", "Type"}, nil)
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

// applyResult renders a completed (cached or freshly finished) result.
func (db *DetailBrowser) applyResult(r *detailResult) {
	if r.err != nil {
		db.grid.SetError(r.err)
		return
	}
	db.grid.SetFillLastColumn(isPropertyValueColumns(r.cols))
	db.grid.SetData(r.cols, r.rows)
}

// isPropertyValueColumns reports whether cols is the Property/Value pair
// shape used for a single-record detail view (Server, Database, and the
// generic default node) rather than a list of many similar rows (the
// Databases/Logins folders, Tables, Views, …) — used to decide whether the
// grid's Value column should stretch to fill the panel instead of just
// fitting its own content.
func isPropertyValueColumns(cols []string) bool {
	return len(cols) == 2 && cols[0] == "Property" && cols[1] == "Value"
}

// Invalidate drops any cached detail data for node — called by every
// Refresh action (F5, right-click Refresh, the Databases/Logins folder
// reload helpers) so a forced refresh reaches the Detail Browser too, not
// just the Object Explorer tree. If node is the one currently on screen,
// it's also refetched immediately rather than waiting for a reselect.
// Nil-safe like dbconn.ServerConn.Close, so call sites (and tests that build an
// App without a DetailBrowser) don't need their own nil check.
func (db *DetailBrowser) Invalidate(app *App, node *explorerNode) {
	if db == nil {
		return
	}
	delete(db.cache, node)
	if db.currentNode == node {
		db.ShowNodeDetails(app, node)
	}
}

// fetch dispatches to a per-node-type loader. Node types with more than one
// round trip worth fetching (NodeServer, NodeDatabases, NodeLogins) show
// their fast fields first and backfill the rest progressively; everything
// else goes through the single-shot fetchNodeDetails.
func (db *DetailBrowser) fetch(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
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
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
			defer cancel()
			cols, rows, err := fetchNodeDetails(ctx, sc, node)
			db.postFinal(app, node, seq, cols, rows, err)
		}()
	}
}

// postPartial displays cols/rows immediately if node/seq is still current,
// without caching — used by progressive loaders for their fast-arriving
// first stage, before slower fields have landed.
func (db *DetailBrowser) postPartial(app *App, seq int, cols []string, rows [][]string) {
	app.postEvent(func() {
		if seq != db.seq {
			return
		}
		db.grid.SetFillLastColumn(isPropertyValueColumns(cols))
		db.grid.SetData(cols, rows)
	})
	app.wakeEventLoop()
}

// postFinal caches the completed result for node and displays it if still
// current. Called exactly once per fetch, whether single-shot or the last
// stage of a progressive loader.
func (db *DetailBrowser) postFinal(app *App, node *explorerNode, seq int, cols []string, rows [][]string, err error) {
	result := &detailResult{cols: cols, rows: rows, err: err}
	app.postEvent(func() {
		db.cache[node] = result
		if seq != db.seq {
			return
		}
		db.applyResult(result)
	})
	app.wakeEventLoop()
}

// cacheOnly caches the completed result for node without touching the
// grid — used by a progressive loader's last stage (see
// loadDatabasesFolderDetails) once every row has already been updated in
// place: calling postFinal there instead would call SetData and reset the
// user's scroll position right after the progressive fill worked to avoid
// exactly that.
func (db *DetailBrowser) cacheOnly(app *App, node *explorerNode, cols []string, rows [][]string, err error) {
	app.postEvent(func() {
		db.cache[node] = &detailResult{cols: cols, rows: rows, err: err}
	})
	app.wakeEventLoop()
}

// fetchNodeDetails runs the gosmo queries for a node's detail grid. Called
// from a background goroutine (see ShowNodeDetails) — it must not touch
// DetailBrowser or any other UI state directly, only return data for the
// caller to apply via postEvent. ctx bounds the whole call (see the
// caller's childFetchTimeout) so a hung server leaves the goroutine and its
// connection to time out instead of blocking forever.
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
	case NodeSystemDatabases:
		dbs, err := sc.Server.DatabasesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
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
		rows := make([][]string, 0, len(procs))
		for _, p := range procs {
			rows = append(rows, []string{p.Schema + "." + p.Name, formatSQLDate(p.CreateDate), formatSQLDate(p.ModifyDate)})
		}
		return []string{"Name", "Created", "Modified"}, rows, nil

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

// fetchChildObjectsDetail is the fallback detail view for any node type with
// children but no richer, purpose-built view above: it just lists the child
// objects, the same way SSMS's Object Explorer Details falls back to a
// folder's contents. Reuses the same childLoaders entry the tree itself
// expands with, so a new NodeType wired into childLoaders picks up a
// reasonable detail view for free rather than defaulting to the leaf-style
// Property/Value grid, which doesn't fit a folder.
func fetchChildObjectsDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	loader, ok := childLoaders[node.data.Type]
	if !ok {
		return []string{"Name"}, nil, nil
	}
	children, err := loader(loaderCtx{ctx: ctx, sc: sc}, node)
	if err != nil {
		return nil, nil, err
	}
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
		titleStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
	}
	core.FillRect(s, core.Rect{X: db.rect.X, Y: db.rect.Y, W: db.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, db.rect.X+1, db.rect.Y, db.rect.W-2, titleStyle, db.title)

	db.grid.Draw(s)
	db.grid.DrawOverlay(s)
}

// HandleKey delegates to the data grid.
func (db *DetailBrowser) HandleKey(ev *tcell.EventKey) bool { return db.grid.HandleKey(ev) }

// HandleMouse delegates to the data grid.
func (db *DetailBrowser) HandleMouse(ev *tcell.EventMouse) bool { return db.grid.HandleMouse(ev) }

// HasSelection, SelectedText, Cut, Paste, and SelectAll implement
// clipboardTarget (see internal/tui/clipboard.go) by forwarding to the
// grid, which is itself only a real clipboard target while its built-in
// "Show Value" content viewer is open (see controls.DataGrid.HasSelection).
func (db *DetailBrowser) HasSelection() bool   { return db.grid.HasSelection() }
func (db *DetailBrowser) SelectedText() string { return db.grid.SelectedText() }
func (db *DetailBrowser) Cut() string          { return db.grid.Cut() }
func (db *DetailBrowser) Paste(text string)    { db.grid.Paste(text) }
func (db *DetailBrowser) SelectAll()           { db.grid.SelectAll() }
