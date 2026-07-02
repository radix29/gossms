package tui

import (
	"github.com/gdamore/tcell/v3"
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

func (oe *ObjectExplorer) SetBounds(x, y, w, h int) { oe.view.SetBounds(x, y, w, h) }
func (oe *ObjectExplorer) SetActive(v bool)         { oe.view.SetActive(v) }
func (oe *ObjectExplorer) Draw(s tcell.Screen)      { oe.view.Draw(s) }
func (oe *ObjectExplorer) HandleKey(ev *tcell.EventKey) bool   { return oe.view.HandleKey(ev) }
func (oe *ObjectExplorer) HandleMouse(ev *tcell.EventMouse) bool { return oe.view.HandleMouse(ev) }

// AddRoot adds a new server root node.
func (oe *ObjectExplorer) AddRoot(label string, connIdx int) *explorerNode {
	n := &explorerNode{
		id:    oe.allocID(),
		label: label,
		data:  nodeData{Type: NodeServer, connIdx: connIdx},
	}
	oe.roots = append(oe.roots, n)
	oe.byID[n.id] = n
	oe.rebuild()
	return n
}

// RemoveRoot removes the root with the given connection index.
func (oe *ObjectExplorer) RemoveRootByConn(connIdx int) {
	for i, r := range oe.roots {
		if r.data.connIdx == connIdx {
			oe.removeSubtree(r)
			oe.roots = append(oe.roots[:i], oe.roots[i+1:]...)
			oe.rebuild()
			return
		}
	}
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
		return
	}
	n.data.Loaded = false
	n.children = nil
	if n.expanded {
		oe.app.loadChildren(n)
	}
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
		Icon:     nodeIcon(n.data.Type),
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
			Icon:  nodeIcon(NodeLoading),
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
	oe.rebuild() // show "Loading..." immediately
	oe.app.loadChildren(n)
}

func (oe *ObjectExplorer) handleCollapse(id controls.TreeNodeID) {
	if n, ok := oe.byID[id]; ok {
		n.expanded = false
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

// resolveConnIdx walks up the explorer tree to find the owning connection.
func resolveConnIdx(n *explorerNode) int {
	for cur := n; cur != nil; cur = cur.parent {
		if cur.data.connIdx > 0 || cur.parent == nil {
			return cur.data.connIdx
		}
	}
	return 0
}
