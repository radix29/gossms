package tui

import (
	"context"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorerNode is the application's tree model. It owns the SQL Server data
// (Type, Schema, DBName, child loading state) and is flattened into
// []controls.TreeNode for the embedded TreeView to render.
type explorerNode struct {
	id       int
	label    string
	data     nodeData
	expanded bool
	parent   *explorerNode
	children []*explorerNode

	// loadSeq and cancelLoad guard a node's in-flight background fetch (see
	// App.loadChildren): loadSeq is bumped on every request, so a result
	// arriving after a newer fetch started drops itself instead of overwriting
	// fresher children, and cancelLoad stops the superseded fetch outright.
	loadSeq    int
	cancelLoad context.CancelFunc
}

// snapshot returns a detached copy of n carrying only what a loader reads: its
// label and its nodeData, by value. Every background fetch takes one instead of
// the live node, because the UI goroutine writes n.data while the fetch runs —
// applyNodeFilter sets data.Filter, and the object ops write it too — so reading
// it from the fetch is a data race, not merely a stale read. The live node still
// travels alongside as the identity the posted callback keys off, but nothing
// dereferences it off the UI goroutine.
//
// id, parent and children are deliberately dropped: no loader reads them, and
// leaving them out stops a snapshot being usable as a tree node by mistake.
func (n *explorerNode) snapshot() *explorerNode {
	return &explorerNode{label: n.label, data: n.data}
}

// beginLoad cancels whatever fetch is in flight for this node and starts a new
// timeout-bound one derived from parent — the owning connection's Context(), so
// disconnecting cancels this fetch rather than leaving it to idle out. The
// caller passes seq to endLoad on completion, so a stale result refuses to
// overwrite fresher children.
func (n *explorerNode) beginLoad(parent context.Context, timeout time.Duration) (ctx context.Context, seq int) {
	if n.cancelLoad != nil {
		n.cancelLoad()
	}
	n.loadSeq++
	ctx, n.cancelLoad = context.WithTimeout(parent, timeout)
	return ctx, n.loadSeq
}

// endLoad reports whether seq is still current; false means a newer beginLoad
// superseded it and seq's result must be discarded. Clears cancelLoad on
// success.
//
// The cancel is called, not just dropped: the result is already in hand, but the
// timeout context stays registered on the connection's context with its timer
// armed until childFetchTimeout expires, for every node ever expanded.
func (n *explorerNode) endLoad(seq int) bool {
	if n.loadSeq != seq {
		return false
	}
	if n.cancelLoad != nil {
		n.cancelLoad()
		n.cancelLoad = nil
	}
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
	oe.view.OnActivate = oe.handleActivate
	return oe
}

func (oe *ObjectExplorer) SetBounds(x, y, w, h int)              { oe.view.SetBounds(x, y, w, h) }
func (oe *ObjectExplorer) SetActive(v bool)                      { oe.view.SetActive(v) }
func (oe *ObjectExplorer) Draw(s tcell.Screen)                   { oe.view.Draw(s) }
func (oe *ObjectExplorer) HandleKey(ev *tcell.EventKey) bool     { return oe.view.HandleKey(ev) }
func (oe *ObjectExplorer) HandleMouse(ev *tcell.EventMouse) bool { return oe.view.HandleMouse(ev) }

// AddRoot adds a new server root node and selects it, so Object Explorer Details
// populates immediately after a connect rather than sitting empty until the next
// manual selection. SetNodes' tv.sel clamp only keeps the *previous* selection
// in bounds — see controls.TreeView.SelectID.
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

// ExpandNode expands n and fetches its children if they aren't loaded — the path
// a click on the expander glyph takes. connect uses it to open a new server node
// the way SSMS does.
func (oe *ObjectExplorer) ExpandNode(n *explorerNode) {
	if n == nil {
		return
	}
	oe.handleExpand(n.id)
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

// RefreshDatabasesFolder refreshes sc's "Databases" folder node, after an action
// that changes the database list from outside Object Explorer's own
// expand/refresh flow. A folder never loaded needs no action: its next expand
// fetches the current list anyway.
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

// RefreshLoginsFolder refreshes sc's Security > Logins folder node, after an
// action that changes the login list from outside Object Explorer's own
// expand/refresh flow. RefreshDatabasesFolder one level deeper.
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

// RefreshFolderByType refreshes sc's first descendant folder node of type t,
// depth-first, after an action that changes a SQL Server Agent collection from
// outside Object Explorer's own flow. Agent folders sit at varying depths under
// SQL Server Agent, so this is one generic search rather than a hand-written
// walk per folder.
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

// NodeAt returns the node drawn at screen position (mx, my), or nil when that
// position isn't over one — the scrollbar, the border and blank space below the
// last node all report nil. Unlike Selected, it says what a press actually
// landed on, which is what drag-and-drop arming needs.
func (oe *ObjectExplorer) NodeAt(mx, my int) *explorerNode {
	id, ok := oe.view.NodeIDAt(mx, my)
	if !ok {
		return nil
	}
	return oe.byID[id]
}

// RefreshSelected forces the selected node to reload its children.
//
// Refreshing the server root also drops its cached capability answers: rights
// granted to a login while it is connected take effect on its existing
// sessions, so a Refresh that re-reads the objects has to re-read what may be
// done with them, or the tree comes back current and every gate on it stale.
func (oe *ObjectExplorer) RefreshSelected() {
	n := oe.Selected()
	if n == nil {
		oe.app.setStatus("Select an item in Object Explorer first")
		return
	}
	if n.data.Type == NodeServer {
		n.data.conn.ClearCapabilityCache()
	}
	n.data.Loaded = false
	n.children = nil
	if n.expanded {
		oe.app.loadChildren(n)
	}
	oe.app.detailBrowser.Invalidate(oe.app, n)
}

// SetChildren installs the loaded children for a node, from the background-load
// callback on the UI goroutine, and rebuilds the flat view. IDs are allocated
// here rather than during the fetch, since allocID mutates shared state.
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
	// The suffix is added here rather than written into n.label, so clearing the
	// filter needn't unpick it from a string the loaders own.
	label := n.label
	if n.data.Filter.active() {
		label += " (filtered)"
	}
	flat = append(flat, controls.TreeNode{
		ID:       n.id,
		Label:    label,
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
		// Already fetched from a previous expand: collapsing never clears
		// n.children, so redisplay them — no loadChildren, no "Loading...", no
		// round trip.
		oe.rebuild()
		return
	}
	oe.rebuild() // show "Loading..." immediately
	oe.app.loadChildren(n)
}

// handleActivate is a node's default action — Enter, or a double-click on its
// row. Only leaves standing for something openable claim it; everything else
// answers false and keeps Enter's expand.
//
// Deliberately narrow: an object node's menu offers several actions and none is
// obviously "the" one, so guessing would make Enter unpredictable.
func (oe *ObjectExplorer) handleActivate(id controls.TreeNodeID) bool {
	n, ok := oe.byID[id]
	if !ok {
		return false
	}
	switch n.data.Type {
	case NodeSQLServerLog, NodeAgentErrorLog:
		sc := resolveConn(n)
		if sc == nil {
			return false
		}
		oe.app.showLogViewerFor(sc, n.data.LogType, n.data.LogNumber)
		return true
	}
	return false
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

// SelectionAnchor is where a menu about the selected node should open — see
// controls.TreeView.SelectionAnchor.
func (oe *ObjectExplorer) SelectionAnchor() (x, y int, ok bool) {
	return oe.view.SelectionAnchor()
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

// resolveConn walks up the explorer tree to find the owning connection. Every
// node fetchChildren creates carries its connection directly; the walk only
// matters for nodes without one, such as error placeholders.
func resolveConn(n *explorerNode) *db.ServerConn {
	for cur := n; cur != nil; cur = cur.parent {
		if cur.data.conn != nil {
			return cur.data.conn
		}
	}
	return nil
}
