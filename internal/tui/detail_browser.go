package tui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DetailBrowser shows details of the selected Object Explorer node — a Panel
// wrapping controls.DataGrid with a title bar and the SQL Server loading
// logic.
type DetailBrowser struct {
	rect   core.Rect
	title  string
	grid   *controls.DataGrid
	active bool

	// OnRefresh runs on a click of the title bar's refresh button — the same
	// action as F5 / Edit > Refresh. Nil makes the button a no-op.
	OnRefresh func()

	// refreshRect is the title bar's refresh button, positioned by SetBounds;
	// zero-width when the panel is too narrow to fit it.
	refreshRect core.Rect

	// mouseDragging distinguishes a fresh Button1 press on the refresh button
	// from a continued hold, like controls.Toolbar's field of the same name.
	mouseDragging bool

	// currentNode is the node ShowNodeDetails last displayed, so Invalidate can
	// tell whether to refetch immediately or just drop the cache entry.
	currentNode *explorerNode

	// cache holds the last successful fetch per node, so reselecting a node
	// already shown doesn't re-hit the network. Only a Refresh or a fresh node
	// forces a refetch — a folder reload replaces its children with new
	// *explorerNode values, which miss the cache. Final results only.
	cache map[*explorerNode]*detailResult

	// rowObjs is the object each grid row describes, parallel to the rows on
	// screen — see detailResult.objs. Empty for a view whose rows are not
	// objects. Every path that changes the display resets it, so it can never
	// describe the previous node's rows.
	rowObjs []nodeData

	// rowObjsNode is the node rowObjs was installed for. detailMenuItems
	// refuses a Delete when it is not currentNode: a reset missed on some new
	// path then costs the menu item, not a DROP of the previous node's objects
	// on the new node's connection.
	rowObjsNode *explorerNode

	// charts are the composition bars drawn under the grid for the node on
	// screen (detail_browser_charts.go). Reset like rowObjs.
	charts []detailChart

	// tooltip is the chart readout pinned by the last click on the strip,
	// nil when none shows — see detail_browser_charts.go.
	tooltip *detailTooltip

	// run is the fetch lifecycle: which run's result is still wanted, and the
	// cancel that stops a superseded one's reads.
	run detailRuns
}

// detailResult is a cached or in-flight-result payload for one node.
type detailResult struct {
	cols []string
	rows [][]string
	// objs identifies the object each row describes, for the views whose rows
	// are objects — the handle the pane's Delete needs, since a row is
	// [][]string shared with every other node type and its Name cell is only a
	// rendering. nil everywhere else, which withholds Delete.
	objs []nodeData
	// charts are the composition bars drawn under the grid, empty for every
	// view that has none.
	charts []detailChart
	err    error
}

// NewDetailBrowser creates a detail browser.
func NewDetailBrowser(title string) *DetailBrowser {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	return new(DetailBrowser{
		title: title,
		grid:  grid,
		cache: make(map[*explorerNode]*detailResult),
		run:   detailRuns{pending: make(map[*explorerNode]int)},
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
	db.layout()

	bw := core.DisplayWidth(refreshButtonLabel)
	if w >= bw+4 {
		db.refreshRect = core.Rect{X: x + w - bw - 1, Y: y, W: bw, H: 1}
	} else {
		db.refreshRect = core.Rect{}
	}
}

// SetActive marks this panel focused: the title bar's colour, and the grid's
// own, so selected rows draw as a selection rather than in the alternating-row
// grey an inactive grid falls back to — the selection is what Delete acts on.
func (db *DetailBrowser) SetActive(v bool) {
	db.active = v
	db.grid.Focus(v)
}

// Closable reports false: Object Explorer Details is a fixed, always-present
// panel, so the tab bar's [x] and Ctrl+W can't close it.
func (db *DetailBrowser) Closable() bool { return false }

// ShowNodeDetails loads detail data for node asynchronously: every fetch is a
// network round trip and this fires on every tree-selection change, so running
// it inline would freeze the app on each arrow key against a slow server. A node
// already shown is served from cache. Nil-safe like Invalidate.
//
// Whatever was in flight is cancelled first: this call supersedes it, same node
// or not, so nothing it could still produce would be shown or cached.
func (db *DetailBrowser) ShowNodeDetails(app *App, node *explorerNode) {
	if db == nil {
		return
	}
	db.run.supersede()
	prev := db.currentNode
	db.currentNode = node

	if node == nil {
		db.showEmpty()
		return
	}

	db.title = fmt.Sprintf("Object Explorer Details — %s", node.label)
	sc := resolveConn(node)

	if !app.isConnected(sc) {
		// Not showEmpty: that rewrites the title, and this pane is showing a
		// node. Everything else it drops still has to go, or the status row
		// sits under the previous node's charts with its row objects live,
		// and the context menu offers Delete on one of them.
		//
		// Reached when a node outlives its connection's close: a peer
		// connection closed under a node still in the tree, or a selection
		// change queued behind one. File > Disconnect goes to showEmpty.
		db.resetForNewNode()
		db.grid.SetFillLastColumn(true)
		db.grid.SetData([]string{"Property", "Value"}, [][]string{{"Status", "Not connected"}})
		return
	}

	if cached, ok := db.cache[node]; ok {
		db.applyResult(cached)
		return
	}

	// A different node's rows must not stay on screen under "Loading...": the
	// fetch can take childFetchTimeout, and until it lands the context menu
	// and Show Value would pair those rows with this node's connection — a
	// Delete of the previous node's objects on this node's server. A Refresh
	// of the node on screen keeps its rows (they are still this node's), but
	// not their objects, as nothing pins which of them the reload removes.
	db.resetForNewNode()
	if prev != node {
		db.grid.SetFillLastColumn(false)
		db.grid.SetData(nil, nil)
	}
	db.grid.SetStatus("Loading...")
	ctx, seq := db.run.begin(sc.Server.Context(), node)
	db.fetch(ctx, app, sc, node, seq)
}

// showEmpty resets the panel to its nothing-selected state.
func (db *DetailBrowser) showEmpty() {
	db.title = "Object Explorer Details"
	db.grid.SetFillLastColumn(false)
	db.grid.SetData([]string{"Name", "Type"}, nil)
	db.resetForNewNode()
}

// resetForNewNode drops everything that described the node leaving the screen.
// applyResult and postPartial reset the same fields by installing new ones;
// every path that installs none — showEmpty, and the "Not connected" and
// loading branches of ShowNodeDetails — calls this instead.
//
// setCharts, not `db.charts = nil`: it also re-splits the panel, giving the
// grid the rows the strip held, and drops the pinned tooltip, which nothing
// else clears.
func (db *DetailBrowser) resetForNewNode() {
	db.rowObjs, db.rowObjsNode = nil, nil
	db.setCharts(nil)
}

// applyResult renders a completed (cached or freshly finished) result.
func (db *DetailBrowser) applyResult(r *detailResult) {
	if r.err != nil {
		db.setRowObjects(nil, nil)
		db.setCharts(nil)
		db.grid.SetError(displayError(r.err))
		return
	}
	db.grid.SetFillLastColumn(isPropertyValueColumns(r.cols))
	db.setCharts(r.charts)
	db.grid.SetData(r.cols, r.rows)
	db.setRowObjects(r.rows, r.objs)
}

// setCharts installs the chart strip for what is now on screen and re-splits
// the panel: whether there is a strip at all decides the grid's height, and
// the two are set together so a repaint can never draw a grid over it.
func (db *DetailBrowser) setCharts(c []detailChart) {
	db.charts = c
	db.layout()
}

// setRowObjects installs the row-to-object mapping for what is now on screen,
// and refuses one that does not line up with the rows: the pane deletes by row
// index, so a mapping one short would delete the object the *next* row
// describes. Losing Delete is the safe failure; the wrong DROP is not.
func (db *DetailBrowser) setRowObjects(rows [][]string, objs []nodeData) {
	if len(objs) != len(rows) {
		db.rowObjs, db.rowObjsNode = nil, nil
		return
	}
	db.rowObjs, db.rowObjsNode = objs, db.currentNode
}

// isPropertyValueColumns reports whether cols is the Property/Value shape of a
// single-record detail view rather than a list of rows, which decides whether
// the Value column stretches to fill the panel.
func isPropertyValueColumns(cols []string) bool {
	return len(cols) == 2 && cols[0] == "Property" && cols[1] == "Value"
}

// Retitle re-reads the title from node's label when node is the one shown.
// A label re-read in place (the Resource Governor and Database Mail nodes'
// state) lands after the Refresh that re-fetched the grid, which titled the
// pane with the old label. Nil-safe like Invalidate.
func (db *DetailBrowser) Retitle(node *explorerNode) {
	if db == nil || node == nil || db.currentNode != node {
		return
	}
	db.title = fmt.Sprintf("Object Explorer Details — %s", node.label)
}

// Invalidate drops any cached detail data for node, so every Refresh action
// reaches the Detail Browser and not just the tree. A node on screen is
// refetched at once rather than on reselect. Nil-safe.
func (db *DetailBrowser) Invalidate(app *App, node *explorerNode) {
	if db == nil {
		return
	}
	delete(db.cache, node)
	db.run.forget(node)
	if db.currentNode == node {
		db.ShowNodeDetails(app, node)
	}
}

// InvalidateWhere drops every cached and pending entry whose node matches, and
// refetches the one on screen — Invalidate for a change that stales a class of
// nodes rather than one known pointer.
//
// The predicate runs over cached nodes and over currentNode separately: a node
// whose fetch failed, or is still in flight, has no cache entry and is still
// the one the user is looking at.
func (db *DetailBrowser) InvalidateWhere(app *App, match func(*explorerNode) bool) {
	if db == nil {
		return
	}
	for node := range db.cache {
		if match(node) {
			delete(db.cache, node)
			db.run.forget(node)
		}
	}
	for node := range db.run.pending {
		if match(node) {
			db.run.forget(node)
		}
	}
	if db.currentNode != nil && match(db.currentNode) {
		db.Invalidate(app, db.currentNode)
	}
}

// Forget drops every cached and pending entry for nodes that have left the
// tree — ObjectExplorer.releaseRetired calls it for the nodes a Reload
// replaced, so neither map holds them alive until the connection closes.
// Nothing is refetched: a node on screen that is among them is about to be
// reselected away from. Nil-safe.
func (db *DetailBrowser) Forget(nodes []*explorerNode) {
	if db == nil {
		return
	}
	for _, n := range nodes {
		if db.run.node == n {
			db.run.stop()
		}
		delete(db.cache, n)
		db.run.forget(n)
	}
}

// RefreshCurrent re-fetches whatever node the panel is showing, independently of
// the tree's selection — what the title bar's refresh button runs. Nil-safe.
func (db *DetailBrowser) RefreshCurrent(app *App) {
	if db == nil || db.currentNode == nil {
		return
	}
	db.Invalidate(app, db.currentNode)
}

// PurgeConn drops every cached and pending entry belonging to sc, whose nodes
// are about to leave the tree, so neither map holds a disconnected server's
// nodes and rows for the session's life. Nil-safe.
func (db *DetailBrowser) PurgeConn(sc *dbconn.ServerConn) {
	if db == nil {
		return
	}
	// supersede, not stop: the run belongs to a connection being torn down, so
	// its result has nowhere to go and must not stay current. The currentNode
	// branch below is not enough — it covers this only while ShowNodeDetails
	// assigns currentNode in the same call that begins the run, an invariant
	// nothing pins. See TestDetailBrowserPurgeConnSupersedes.
	if db.run.node != nil && resolveConn(db.run.node) == sc {
		db.run.supersede()
	}
	for node := range db.cache {
		if resolveConn(node) == sc {
			delete(db.cache, node)
		}
	}
	for node := range db.run.pending {
		if resolveConn(node) == sc {
			db.run.forget(node)
		}
	}
	if db.currentNode != nil && resolveConn(db.currentNode) == sc {
		// Disconnecting the last server empties the tree and fires no OnSelect,
		// so nothing else repaints the grid and it keeps showing the
		// disconnected server's rows. Superseding also drops the result of the
		// fetch stopped above.
		db.currentNode = nil
		db.run.supersede()
		db.showEmpty()
	}
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
	db.drawCharts(s)
	db.drawChartTooltip(s)
	// After the strip: the grid's "Show Value" viewer and cell menu open over
	// the whole panel, chart rows included.
	db.grid.DrawOverlay(s)
}

// HandleKey closes a pinned chart readout on Escape and otherwise delegates
// to the data grid. Escape is claimed only while a box is showing: with none
// it belongs to whatever the panel is inside.
func (db *DetailBrowser) HandleKey(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyEscape && db.tooltip != nil {
		db.tooltip = nil
		return true
	}
	return db.grid.HandleKey(ev)
}

// HandleMouse fires OnRefresh for a press on the title bar's refresh button and
// delegates the rest to the grid. A release over the button still reaches the
// grid, so its mouseDragging latch can't stick.
func (db *DetailBrowser) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		db.mouseDragging = false
	}
	mx, my := ev.Position()
	if strip := db.chartsRect(); strip.Contains(mx, my) {
		if ev.Buttons() == tcell.Button1 && !db.mouseDragging {
			db.mouseDragging = true
			// A showing box is dismissed by the next click wherever it lands,
			// so one click never both closes a box and opens another — the
			// user would see only the second and think the first never closed.
			if db.tooltip != nil {
				db.tooltip = nil
			} else {
				db.tooltip = db.pinChartTooltip(mx, my)
			}
		}
		// Claimed either way: the strip is not the grid, and a press on it
		// must not scroll or select behind the charts.
		return true
	}
	if db.tooltip != nil && ev.Buttons() == tcell.Button1 && !db.mouseDragging {
		db.mouseDragging = true
		db.tooltip = nil
		return true
	}
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

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it
// implement clipboardTarget by forwarding to the grid, which is a real
// clipboard target only while its "Show Value" viewer is open.
func (db *DetailBrowser) HasSelection() bool   { return db.grid.HasSelection() }
func (db *DetailBrowser) SelectedText() string { return db.grid.SelectedText() }
func (db *DetailBrowser) Cut() string          { return db.grid.Cut() }
func (db *DetailBrowser) Paste(text string)    { db.grid.Paste(text) }
func (db *DetailBrowser) SelectAll()           { db.grid.SelectAll() }

// newDetailBrowser builds the Object Explorer Details panel wired to this
// app: the title bar's refresh button, the Query Store reports' "Query" column
// opening its full statement in a query panel, and the cell menu's Delete over
// the selected rows (detail_browser_ops.go).
func (a *App) newDetailBrowser() *DetailBrowser {
	db := NewDetailBrowser("Object Explorer Details")
	db.OnRefresh = func() { db.RefreshCurrent(a) }
	db.grid.OnShowValue = func(col int, column, value string) bool {
		return db.showQueryStoreValue(a, col, column, value)
	}
	db.grid.OnMenuItems = func() []controls.MenuItem { return a.detailMenuItems(db) }
	return db
}

// showQueryStoreValue is the grid's "Show Value" hook. On a Query Store
// report's Query column it re-reads the statement from the server by the row's
// query id, rather than opening the cell.
//
// The cell cannot be opened: queryStoreOneLine collapses the statement onto one
// line for the grid, so a `-- comment` anywhere in it swallows every line that
// follows and what opens is SQL with most of the query commented out. This grid
// holds only [][]string, shared with every other node type, so the id is the
// only handle back to the statement (QueryStorePanel keeps the real text and
// needs no round trip).
//
// Every path that cannot produce an id — another column, another node type, a
// row whose id cell is a dash — falls through to the flattened cell.
func (db *DetailBrowser) showQueryStoreValue(a *App, col int, column, value string) bool {
	if column != qsQueryColumn || db.currentNode == nil ||
		db.currentNode.data.Type != NodeQueryStoreReport {
		return a.showSQLCellValue(col, column, value)
	}
	node := db.currentNode
	sc := resolveConn(node)
	idCol := db.grid.ColumnIndex(qsQueryIDColumn)
	// By name, never by position: these grids are built from whatever the
	// loader returned, and the reports do not share one column list.
	cells := db.grid.Row(db.grid.SelectedRow())
	if sc == nil || idCol < 0 || idCol >= len(cells) {
		return a.showSQLCellValue(col, column, value)
	}
	queryID, err := strconv.ParseInt(cells[idCol], 10, 64)
	if err != nil {
		return a.showSQLCellValue(col, column, value)
	}
	dbName := node.data.DBName
	a.setStatus(fmt.Sprintf("Reading the text of query %d...", queryID))
	// safego, not safegoRepair: nothing is latched here. The status line is the
	// only thing written before the read, and the next action overwrites it.
	a.safego("reading a Query Store query's text", func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		text, err := queryStoreQueryText(ctx, sc, dbName, queryID)
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Query %d: %v", queryID, displayError(err)))
				return
			}
			if text == "" {
				// Query Store no longer holds the statement; the flattened
				// cell is all that is left of it.
				text = value
			}
			a.openValuePanel(column, ".sql", controls.SQLHighlighter(theme.Active()), text)
		})
	})
	return true
}
