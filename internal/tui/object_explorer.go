package tui

import (
	"context"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorerNode is the application's tree model. It owns the SQL-Server
// specific data (Type, Schema, DBName, child loading state) and is mapped
// into a flat []controls.TreeNode for rendering by the embedded TreeView.
type explorerNode struct {
	id       int
	label    string
	data     nodeData
	expanded bool
	parent   *explorerNode
	children []*explorerNode

	// loadSeq and cancelLoad guard a node's in-flight background fetch
	// (see App.loadChildren): loadSeq is bumped on every fetch request, so
	// a fetch whose result arrives after a newer one was started can tell
	// it's stale and drop itself instead of overwriting fresher children;
	// cancelLoad stops that superseded fetch's context outright rather
	// than just ignoring its result.
	loadSeq    int
	cancelLoad context.CancelFunc
}

// beginLoad cancels whatever fetch is already in flight for this node (a
// fast double-expand, or a Refresh before the initial load returned) and
// starts a new timeout-bound one. The caller must pass seq to endLoad once
// the fetch completes, so a stale result can recognize itself and refuse
// to overwrite fresher children.
func (n *explorerNode) beginLoad(timeout time.Duration) (ctx context.Context, seq int) {
	if n.cancelLoad != nil {
		n.cancelLoad()
	}
	n.loadSeq++
	ctx, n.cancelLoad = context.WithTimeout(context.Background(), timeout)
	return ctx, n.loadSeq
}

// endLoad reports whether seq (as returned by beginLoad) is still current
// — false means a newer beginLoad has since superseded it, and the result
// belonging to seq must be discarded. Clears cancelLoad on success, since
// the fetch it guarded has now finished.
func (n *explorerNode) endLoad(seq int) bool {
	if n.loadSeq != seq {
		return false
	}
	n.cancelLoad = nil
	return true
}

// ObjectExplorer wraps a tuikit controls.TreeView and owns the SQL Server
// object model (roots, expansion state, lazy loading).
type ObjectExplorer struct {
	app  *App
	view *controls.TreeView

	roots  []*explorerNode
	byID   map[int]*explorerNode
	nextID int
}

// NewObjectExplorer creates the object explorer panel.
func NewObjectExplorer(app *App) *ObjectExplorer {
	oe := &ObjectExplorer{
		app:  app,
		view: controls.NewTreeView(),
		byID: make(map[int]*explorerNode),
	}
	oe.view.OnExpand = oe.handleExpand
	oe.view.OnCollapse = oe.handleCollapse
	oe.view.OnSelect = oe.handleSelect
	oe.view.OnRightClick = oe.handleRightClick
	return oe
}

func (oe *ObjectExplorer) SetBounds(x, y, w, h int)              { oe.view.SetBounds(x, y, w, h) }
func (oe *ObjectExplorer) SetActive(v bool)                      { oe.view.SetActive(v) }
func (oe *ObjectExplorer) Draw(s tcell.Screen)                   { oe.view.Draw(s) }
func (oe *ObjectExplorer) HandleKey(ev *tcell.EventKey) bool     { return oe.view.HandleKey(ev) }
func (oe *ObjectExplorer) HandleMouse(ev *tcell.EventMouse) bool { return oe.view.HandleMouse(ev) }

// AddRoot adds a new server root node, selecting it — so Object Explorer
// Details populates immediately after a successful connect, the same as if
// the user had clicked the new node themselves, rather than sitting empty
// until the next manual selection change (SetNodes' tv.sel clamp keeps the
// tree's *previous* selection in bounds, it doesn't mean "select this new
// node" — see controls.TreeView.SelectID).
func (oe *ObjectExplorer) AddRoot(label string, sc *db.ServerConn) *explorerNode {
	n := &explorerNode{
		id:    oe.allocID(),
		label: label,
		data:  nodeData{Type: NodeServer, conn: sc},
	}
	oe.roots = append(oe.roots, n)
	oe.byID[n.id] = n
	oe.rebuild()
	oe.view.SelectID(n.id)
	return n
}

// RemoveRootByConn removes the root belonging to the given connection.
func (oe *ObjectExplorer) RemoveRootByConn(sc *db.ServerConn) {
	for i, r := range oe.roots {
		if r.data.conn == sc {
			oe.removeSubtree(r)
			oe.roots = append(oe.roots[:i], oe.roots[i+1:]...)
			oe.rebuild()
			return
		}
	}
}

// RefreshDatabasesFolder refreshes sc's "Databases" folder node — used
// after an action that changes the database list from outside Object
// Explorer's own expand/refresh flow (e.g. New Database). A folder that's
// never been loaded (the server node hasn't been expanded yet, or the
// Databases folder itself hasn't) needs no action: its next expand fetches
// the current list anyway.
func (oe *ObjectExplorer) RefreshDatabasesFolder(sc *db.ServerConn) {
	for _, r := range oe.roots {
		if r.data.conn != sc {
			continue
		}
		for _, c := range r.children {
			if c.data.Type == NodeDatabases {
				c.data.Loaded = false
				c.children = nil
				if c.expanded {
					oe.app.loadChildren(c)
				}
				oe.app.detailBrowser.Invalidate(oe.app, c)
				return
			}
		}
		return
	}
}

// RefreshLoginsFolder refreshes sc's Security > Logins folder node — used
// after an action that changes the login list from outside Object
// Explorer's own expand/refresh flow (e.g. New Login). Mirrors
// RefreshDatabasesFolder, one level deeper: Databases sits directly under
// the server root, Logins sits under Security.
func (oe *ObjectExplorer) RefreshLoginsFolder(sc *db.ServerConn) {
	for _, r := range oe.roots {
		if r.data.conn != sc {
			continue
		}
		for _, c := range r.children {
			if c.data.Type != NodeSecurity {
				continue
			}
			for _, gc := range c.children {
				if gc.data.Type == NodeLogins {
					gc.data.Loaded = false
					gc.children = nil
					if gc.expanded {
						oe.app.loadChildren(gc)
					}
					oe.app.detailBrowser.Invalidate(oe.app, gc)
					return
				}
			}
			return
		}
		return
	}
}

// RefreshFolderByType refreshes sc's first descendant folder node of type
// t (depth-first) — used after an action that changes a SQL Server Agent
// collection from outside Object Explorer's own expand/refresh flow (e.g.
// New Job/Schedule/Alert/Operator). Unlike RefreshDatabasesFolder/
// RefreshLoginsFolder, which hand-walk one fixed path each, Agent folders
// sit at varying depths under SQL Server Agent (Jobs > User Jobs is three
// levels down, Schedules is two), so this is one generic search instead of
// one hand-written walk per folder.
func (oe *ObjectExplorer) RefreshFolderByType(sc *db.ServerConn, t NodeType) {
	for _, r := range oe.roots {
		if r.data.conn != sc {
			continue
		}
		if n := findDescendantByType(r, t); n != nil {
			n.data.Loaded = false
			n.children = nil
			if n.expanded {
				oe.app.loadChildren(n)
			}
			oe.app.detailBrowser.Invalidate(oe.app, n)
		}
		return
	}
}

// findDescendantByType searches n's subtree depth-first for the first node
// of type t, not including n itself.
func findDescendantByType(n *explorerNode, t NodeType) *explorerNode {
	for _, c := range n.children {
		if c.data.Type == t {
			return c
		}
		if found := findDescendantByType(c, t); found != nil {
			return found
		}
	}
	return nil
}

func (oe *ObjectExplorer) removeSubtree(n *explorerNode) {
	delete(oe.byID, n.id)
	for _, c := range n.children {
		oe.removeSubtree(c)
	}
}

func (oe *ObjectExplorer) allocID() int {
	oe.nextID++
	return oe.nextID
}

// Selected returns the application-level node currently highlighted.
func (oe *ObjectExplorer) Selected() *explorerNode {
	tn := oe.view.SelectedNode()
	if tn == nil {
		return nil
	}
	return oe.byID[tn.ID]
}

// RefreshSelected forces the selected node to reload its children.
func (oe *ObjectExplorer) RefreshSelected() {
	n := oe.Selected()
	if n == nil {
		oe.app.setStatus("Select an item in Object Explorer first")
		return
	}
	n.data.Loaded = false
	n.children = nil
	if n.expanded {
		oe.app.loadChildren(n)
	}
	oe.app.detailBrowser.Invalidate(oe.app, n)
}

// SetChildren installs the loaded children for a node (called from the
// background-load callback, on the main goroutine via postEvent) and
// rebuilds the flat view. IDs are allocated here — not during the
// background fetch — because allocID mutates shared state and must only
// run on the UI goroutine.
func (oe *ObjectExplorer) SetChildren(n *explorerNode, children []*explorerNode) {
	for _, c := range children {
		c.id = oe.allocID()
		c.parent = n
		oe.byID[c.id] = c
	}
	n.children = children
	n.data.Loaded = true
	oe.rebuild()
}

// rebuild flattens the explorer tree into the controls.TreeView's node list.
func (oe *ObjectExplorer) rebuild() {
	flat := make([]controls.TreeNode, 0, 32)
	for _, r := range oe.roots {
		flat = oe.flatten(flat, r, 0)
	}
	oe.view.SetNodes(flat)
}

func (oe *ObjectExplorer) flatten(flat []controls.TreeNode, n *explorerNode, depth int) []controls.TreeNode {
	flat = append(flat, controls.TreeNode{
		ID:       n.id,
		Label:    n.label,
		Icon:     nodeIcon(n.data, oe.app.cfg.IconStyle, n.expanded),
		Depth:    depth,
		Expanded: n.expanded,
		Loaded:   n.data.Loaded,
		HasKids:  hasChildren(n.data.Type),
		Tag:      n,
	})
	if !n.expanded {
		return flat
	}
	if !n.data.Loaded {
		flat = append(flat, controls.TreeNode{
			ID:    oe.allocID(),
			Label: "Loading...",
			Icon:  nodeIcon(nodeData{Type: NodeLoading}, oe.app.cfg.IconStyle, false),
			Depth: depth + 1,
		})
		return flat
	}
	for _, c := range n.children {
		flat = oe.flatten(flat, c, depth+1)
	}
	return flat
}

// ---- TreeView callback adapters ----

func (oe *ObjectExplorer) handleExpand(id controls.TreeNodeID) {
	n, ok := oe.byID[id]
	if !ok {
		return
	}
	n.expanded = true
	if n.data.Loaded {
		// Already fetched from a previous expand — children are still
		// sitting in n.children (collapsing never clears them), so just
		// redisplay them. No loadChildren call, no "Loading...", no
		// round-trip to the server.
		oe.rebuild()
		return
	}
	oe.rebuild() // show "Loading..." immediately
	oe.app.loadChildren(n)
}

func (oe *ObjectExplorer) handleCollapse(id controls.TreeNodeID) {
	if n, ok := oe.byID[id]; ok {
		n.expanded = false
		oe.rebuild()
	}
}

func (oe *ObjectExplorer) handleSelect(id controls.TreeNodeID) {
	if n, ok := oe.byID[id]; ok {
		oe.app.onNodeSelected(n)
	}
}

func (oe *ObjectExplorer) handleRightClick(id controls.TreeNodeID, x, y int) {
	if n, ok := oe.byID[id]; ok {
		oe.app.showContextMenu(n, x, y)
	}
}

// FormatNodePath returns a breadcrumb path for the node ("Server > DB > Tables").
func FormatNodePath(n *explorerNode) string {
	if n == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	for cur := n; cur != nil; cur = cur.parent {
		parts = append(parts, cur.label)
	}
	// reverse
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " > " + p
	}
	return out
}

// resolveConn walks up the explorer tree to find the owning connection.
// Every node created by fetchChildren carries its connection directly; the
// walk only matters for nodes without one (e.g. error placeholders).
func resolveConn(n *explorerNode) *db.ServerConn {
	for cur := n; cur != nil; cur = cur.parent {
		if cur.data.conn != nil {
			return cur.data.conn
		}
	}
	return nil
}
