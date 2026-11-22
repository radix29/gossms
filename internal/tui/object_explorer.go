package tui

import (
	"slices"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
)

// explorerItem is the tree bookkeeping controls.TreeModel keeps in every
// explorerNode (Parent, Children, Expanded, Loaded, ID, Retired). An alias, so
// the embedded field, and a literal setting it, read explorerItem.
type explorerItem = controls.TreeItem[*explorerNode]

// explorerNode is one node of Object Explorer's tree. It owns the SQL Server
// data (Type, Schema, DBName, the load guard); the tree structure and its
// lazy loading are the embedded explorerItem's, run by ObjectExplorer.tree.
type explorerNode struct {
	explorerItem
	label string
	data  nodeData

	// flat and groups are a groupedFolderTypes folder's Group by Schema state:
	// flat is its loaded children in the loader's order, the source children is
	// derived from, and groups the schema nodes last built from it, kept once
	// retired so a rebuilt group comes back expanded. See applyGrouping.
	flat   []*explorerNode
	groups []*explorerNode

	// load guards this node's in-flight fetch of its children (App.loadChildren): a
	// result arriving after a newer fetch started drops itself, and the superseded
	// fetch is cancelled outright. See latest.
	load latest
}

// snapshot returns a detached copy of n carrying only what a loader reads: its
// label and its nodeData, by value. Every background fetch takes one instead of
// the live node, because the UI goroutine writes n.data while the fetch runs
// (applyNodeFilter sets data.Filter, and the object ops write it too), so
// reading it from the fetch is a data race, not merely stale. The live node
// still travels alongside as the identity the posted callback keys off, but
// nothing dereferences it off the UI goroutine.
//
// The explorerItem (id, parent, children) is deliberately dropped: no loader
// reads it, and leaving it out stops a snapshot being used as a tree node by
// mistake.
func (n *explorerNode) snapshot() *explorerNode {
	return &explorerNode{label: n.label, data: n.data}
}

// ObjectExplorer wraps a tuikit controls.TreeView, driven by a
// controls.TreeModel over explorerNodes: the model owns the lazy-tree mechanics
// (IDs, the "Loading..." row, retiring a replaced subtree, putting the
// selection back after a rebuild); ObjectExplorer supplies what a node means,
// how its children load, and where a renamed object's selection goes.
type ObjectExplorer struct {
	app  *App
	view *controls.TreeView
	tree *controls.TreeModel[*explorerNode]

	// follow is a rename's or Move to Schema's forwarding address (see Follow),
	// pending until the reload of the folder it names lands.
	follow *followHint

	// drill is an Object Explorer Details double-click waiting for its folder's
	// children to load (DrillInto), cleared when the user selects a node.
	drill *drillHint
}

// NewObjectExplorer creates the object explorer panel.
func NewObjectExplorer(app *App) *ObjectExplorer {
	oe := &ObjectExplorer{app: app, view: controls.NewTreeView()}
	oe.tree = controls.NewTreeModel[*explorerNode](oe.view)
	oe.tree.Row = oe.row
	oe.tree.Placeholder = func(*explorerNode) controls.TreeNode {
		return controls.TreeNode{Label: "Loading...", Icon: nodeIcon(nodeData{Type: NodeLoading}, oe.app.cfg.IconStyle, false)}
	}
	oe.tree.Load = app.loadChildren
	oe.tree.Same = sameObject
	oe.tree.Redirect = oe.redirect
	oe.tree.OnSelect = oe.handleSelect
	oe.tree.OnReselect = oe.handleReselect
	// A retired node's in-flight load is cancelled at once; its Details cache
	// goes when the rebuild that takes its row off releases it. The release
	// cancels again: a load started since from a stale row expanded meanwhile.
	oe.tree.OnRetire = func(n *explorerNode) { n.load.Abandon() }
	oe.tree.OnRelease = func(ns []*explorerNode) {
		for _, n := range ns {
			n.load.Abandon()
		}
		oe.app.detailBrowser.Forget(ns)
	}
	oe.view.SetTitle("Object Explorer")
	// The mouse surface of Group by Schema (Alt+G, View menu): "[⋮]" for the
	// flat list, "[Σ]" while grouped.
	oe.view.SetTitleButton("⋮", "Σ",
		func() bool { return app.groupBySchema },
		func() { app.toggleGroupBySchema() })
	oe.view.SetTitleButtonTip("Group by Schema (Alt+G)")
	oe.view.SetCloseButton(app.hideExplorer)
	oe.view.OnRightClick = oe.handleRightClick
	oe.view.OnActivate = oe.handleActivate
	return oe
}

func (oe *ObjectExplorer) SetBounds(x, y, w, h int)              { oe.view.SetBounds(x, y, w, h) }
func (oe *ObjectExplorer) Focus(v bool)                          { oe.view.Focus(v) }
func (oe *ObjectExplorer) Draw(s tcell.Screen)                   { oe.view.Draw(s) }
func (oe *ObjectExplorer) DrawOverlay(s tcell.Screen)            { oe.view.DrawOverlay(s) }
func (oe *ObjectExplorer) HandleKey(ev *tcell.EventKey) bool     { return oe.view.HandleKey(ev) }
func (oe *ObjectExplorer) HandleMouse(ev *tcell.EventMouse) bool { return oe.view.HandleMouse(ev) }

// AddRoot adds a new server root node and selects it, so Object Explorer Details
// populates immediately after a connect (controls.TreeModel.AddRoot).
func (oe *ObjectExplorer) AddRoot(label string, sc *db.ServerConn) *explorerNode {
	n := &explorerNode{label: label, data: nodeData{Type: NodeServer, conn: sc}}
	oe.tree.AddRoot(n)
	return n
}

// rebuild projects the tree into the TreeView and puts the selection back
// (controls.TreeModel.Rebuild): after anything that changes what a row shows.
func (oe *ObjectExplorer) rebuild() { oe.tree.Rebuild() }

// ExpandNode expands n and fetches its children if not loaded: the path a click
// on the expander glyph takes. connect uses it to open a new server node as SSMS
// does.
func (oe *ObjectExplorer) ExpandNode(n *explorerNode) {
	if n == nil {
		return
	}
	oe.tree.Expand(n)
}

// RemoveRootByConn removes the root belonging to the given connection.
func (oe *ObjectExplorer) RemoveRootByConn(sc *db.ServerConn) {
	for _, r := range oe.tree.Roots() {
		if r.data.conn == sc {
			oe.tree.RemoveRoot(r)
			return
		}
	}
}

// ReloadFolders reloads every node of sc's tree that match accepts, without
// looking below one. It is the one refresh after a write (create, attach,
// restore, delete, rename or state change), and the match is why:
//
//   - A write that finishes later than it started cannot hold on to the node it
//     started from. A Refresh above it meanwhile retires that node, and Reload
//     of a retired node is a no-op, so the write's own refresh was silently
//     lost. A match names the folder by what it is (folderOf, sameNodeAs) and
//     finds whichever node stands for it now.
//   - The folder, not the object's node: the folder's loader builds the label,
//     and a rename leaves the node's name stale, so every later menu action on
//     it would name the old object.
//
// A folder never loaded is not in the tree and needs no action: its next expand
// fetches the current list.
func (oe *ObjectExplorer) ReloadFolders(sc *db.ServerConn, match func(nodeData) bool) {
	for _, r := range oe.tree.Roots() {
		if r.data.conn != sc {
			continue
		}
		if match(r.data) {
			oe.Reload(r)
			return
		}
		var hits []*explorerNode
		var walk func(*explorerNode)
		walk = func(n *explorerNode) {
			for _, c := range n.Children {
				if match(c.data) {
					hits = append(hits, c)
					continue
				}
				walk(c)
			}
		}
		walk(r)
		for _, n := range hits {
			oe.Reload(n)
		}
		return
	}
}

// folderOf matches, for ReloadFolders, the folders of the given types that belong
// to database dbName ("" for the server-scope ones).
func folderOf(dbName string, types ...NodeType) func(nodeData) bool {
	return func(d nodeData) bool {
		return slices.Contains(types, d.Type) && d.DBName == dbName
	}
}

// reloadDatabasesFolder reloads sc's Databases folder: what every write that
// adds, removes or replaces a database ends with (New Database, Attach,
// Detach, New Snapshot, a restore, a revert to a snapshot).
func (a *App) reloadDatabasesFolder(sc *db.ServerConn) {
	a.explorer.ReloadFolders(sc, folderOf("", NodeDatabases))
}

// nodeKey is what names a node: equal keys stand for the same server object or
// folder. TableName, AGName, XESession and RGPool each tell apart folders of one
// type and database with different parents; AgentScheduleID tells apart schedules
// of one name; ReplPublisher and ReplPublisherDB tell apart a database's
// subscriptions to two publications of one name. A node type whose name isn't
// its identity adds its key here, so sameNodeAs and sameObject (the selection's
// survivor) can't disagree; both once missed the schedule id.
type nodeKey struct {
	Type                                                       NodeType
	DBName, Schema, Name, TableName, AGName, XESession, RGPool string
	AgentScheduleID                                            int
	ReplPublisher, ReplPublisherDB                             string
}

func (d nodeData) key() nodeKey {
	return nodeKey{d.Type, d.DBName, d.Schema, d.Name, d.TableName, d.AGName, d.XESession, d.RGPool, d.AgentScheduleID,
		d.ReplPublisher, d.ReplPublisherDB}
}

// sameNodeAs matches, for ReloadFolders, the nodes standing for the same object
// or folder as n (same nodeKey): n itself while live, or the node that replaced
// it after a Refresh above retired it. Nil matches nothing.
func sameNodeAs(n *explorerNode) func(nodeData) bool {
	if n == nil {
		return func(nodeData) bool { return false }
	}
	w := n.data.key()
	return func(d nodeData) bool { return d.key() == w }
}

// findDescendantByType searches n's subtree depth-first for the first node
// of type t, not including n itself.
func findDescendantByType(n *explorerNode, t NodeType) *explorerNode {
	for _, c := range n.Children {
		if c.data.Type == t {
			return c
		}
		if found := findDescendantByType(c, t); found != nil {
			return found
		}
	}
	return nil
}

// Selected returns the application-level node currently highlighted.
func (oe *ObjectExplorer) Selected() *explorerNode {
	n, _ := oe.tree.Selected()
	return n
}

// NodeAt returns the node drawn at screen position (mx, my), or nil when that
// position isn't over one (scrollbar, border and blank space below the last
// node). Unlike Selected, it says what a press landed on, which drag-and-drop
// arming needs.
func (oe *ObjectExplorer) NodeAt(mx, my int) *explorerNode {
	n, _ := oe.tree.NodeAt(mx, my)
	return n
}

// RefreshSelected forces the selected node to reload its children (F5 and Edit >
// Refresh). Everything a Refresh does is Reload's, so this path and the context
// menu's cannot drift apart.
func (oe *ObjectExplorer) RefreshSelected() {
	n := oe.Selected()
	if n == nil {
		oe.app.setStatus("Select an item in Object Explorer first")
		return
	}
	oe.Reload(n)
}

// Reload re-reads n: its children if expanded, and its details. It is the one
// body behind every Refresh (F5, the context menu's, the reload after a write),
// so what a Refresh releases or re-reads is done once, here.
//
// n's children are replaced, not updated: the load makes new nodes. The old ones
// are retired and released by controls.TreeModel.Reload, or every Refresh leaked
// them.
//
// Two connection-level extras ride along:
//   - on the server node, the cached capability answers are re-read. Rights
//     granted to a connected login take effect on its existing sessions, so a
//     Refresh that re-reads the objects must re-read what may be done with them,
//     or the tree comes back current and every gate on it stale. Per-database
//     answers are dropped (and the selection's re-primed); the server-scope set
//     is re-probed off the UI goroutine.
//   - in the Always On subtree, the cached peer connect failures are dropped
//     (forgetPeerFailuresForRefresh).
//
// And two node-level ones: the Resource Governor and Database Mail nodes' labels
// carry their state, which Management's loader reads, so a Refresh of the node
// itself re-reads it in place (refreshResourceGovernorLabel,
// refreshDatabaseMailLabel), or a change from a query window would show only
// after a Refresh of Management.
func (oe *ObjectExplorer) Reload(n *explorerNode) {
	if n != nil && n.data.Type == NodeSchemaGroup {
		// A schema group is a regrouping of its folder's children with no loader
		// of its own: its Refresh (F5 included) is the folder's.
		n = n.Parent
	}
	if n == nil || n.Retired() {
		return
	}
	sc := resolveConn(n)
	if n.data.Type == NodeServer && sc != nil {
		sc.ClearCapabilityCache()
		if sel := oe.Selected(); sel != nil {
			oe.app.primeDatabaseCapabilities(sel)
		}
		if sc.Server != nil {
			oe.app.safego("re-reading server capabilities", func() { sc.ProbeCapabilities(sc.Server.Context()) })
		}
	}
	forgetPeerFailuresForRefresh(sc, n)
	if n.data.Type == NodeResourceGovernor && sc != nil {
		oe.app.refreshResourceGovernorLabel(sc)
	}
	if n.data.Type == NodeDatabaseMail && sc != nil {
		oe.app.refreshDatabaseMailLabel(sc)
	}
	n.flat = nil
	oe.tree.Reload(n)
	oe.app.detailBrowser.Invalidate(oe.app, n)
}

// SetChildren installs the loaded children for a node, from the background-load
// callback on the UI goroutine, and rebuilds the flat view. IDs are allocated
// there (controls.TreeModel.SetChildren), not during the fetch, since the
// allocation mutates shared state. A retired node takes nothing.
func (oe *ObjectExplorer) SetChildren(n *explorerNode, children []*explorerNode) {
	if !oe.tree.SetChildren(n, children) {
		return
	}
	if groupedFolderTypes[n.data.Type] {
		n.flat = children
		oe.applyGrouping(n)
	}
	f := oe.follow
	if f != nil && n.data.key() == f.folder && resolveConn(n) == f.conn {
		f.reveal(children)
		defer func() { oe.follow = nil }() // this was the reload it waited for
	}
	oe.rebuild()
	oe.landDrill(n)
}

// followHint is where a selection on a renamed or moved object goes when the
// folder reload the write started replaces its node: survivor matches by
// nodeKey, which the new name or schema no longer satisfies, so the selection
// fell to the folder (or the schema group) and the next keyboard action went to
// it.
type followHint struct {
	from   *explorerNode // the node the write ran on, retired by the reload
	to     nodeKey       // the object's key after the write
	folder nodeKey       // the listing folder whose reload carries it
	conn   *db.ServerConn
	// parked is where the tree moved the selection (handleReselect) when a
	// rebuild between the Reload and its load landing (the folder shows
	// "Loading...") took it off from: the selection is still from's while it
	// sits there untouched.
	parked *explorerNode
}

// Follow records that n's object now has key to, for the reload of n's listing
// folder that the caller starts next: if the selection is still on n when that
// reload lands, it moves to the node standing for to, not to the folder. It is
// dropped when that folder's load lands, or when the user selects a node.
func (oe *ObjectExplorer) Follow(n *explorerNode, to nodeKey) {
	folder := n.listingParent()
	if folder == nil {
		return
	}
	oe.follow = &followHint{from: n, to: to, folder: folder.data.key(), conn: resolveConn(n)}
}

// reveal expands the schema group holding the followed object among the
// folder's freshly loaded children: Move to Schema lands it in a group that may
// be collapsed, and a hidden node can't take the selection.
func (f *followHint) reveal(children []*explorerNode) {
	for _, c := range children {
		if c.data.key() == f.to {
			if g := c.Parent; g != nil && g.data.Type == NodeSchemaGroup {
				g.Expanded = true
			}
			return
		}
	}
}

// target is the shown node f forwards a selection on prev to, or nil when prev
// isn't the followed selection or the object isn't on screen.
func (f *followHint) target(prev *explorerNode, shown map[*explorerNode]bool) *explorerNode {
	if f == nil || prev != f.from && prev != f.parked {
		return nil
	}
	for n := range shown {
		if n.data.key() == f.to && !n.Retired() && resolveConn(n) == f.conn {
			return n
		}
	}
	return nil
}

// renamedKey is n's key once its object is renamed to newName. A database
// node carries its name in DBName too.
func renamedKey(n nodeData, newName string) nodeKey {
	k := n.key()
	k.Name = newName
	if n.Type == NodeDatabase {
		k.DBName = newName
	}
	return k
}

// redirect is the TreeModel's Redirect hook: a selection on a renamed or moved
// object (Follow) goes to the node now standing for it, once the folder reload
// lands. Until then the model parks it on its survivor (the folder or its
// "Loading..." row), and handleReselect records where.
func (oe *ObjectExplorer) redirect(prev *explorerNode, shown map[*explorerNode]bool) (*explorerNode, bool) {
	t := oe.follow.target(prev, shown)
	if t == nil || t == prev {
		return nil, false
	}
	oe.follow = nil
	return t, true
}

// sameObject reports whether a and b stand for the same server object, how a
// node a reload re-created is recognised (the TreeModel's Same hook, which the
// selection's survivor uses). By nodeKey: by name alone, a reload
// moved the selection from one of two schedules named Daily to the other, and
// Delete then took the one the user hadn't picked. A node with no Name (a
// folder, a log entry) is told apart by its label too; a changed label fails to
// match and the selection goes to the parent, the safe miss.
func sameObject(a, b *explorerNode) bool {
	if a.data.key() != b.data.key() {
		return false
	}
	return a.data.Name != "" || a.label == b.label
}

// profileOf is the profile name opts is saved under, or "" when it has none.
// Display only: the connection itself is never renamed. Nil-safe.
func (a *App) profileOf(opts config.Connection) string {
	if a == nil || a.cfg == nil {
		return ""
	}
	if saved, err := a.cfg.ProfileByName(opts.GeneratedName()); err == nil {
		return saved.Profile
	}
	return ""
}

// row is the TreeModel's Row hook: what n's row shows.
func (oe *ObjectExplorer) row(n *explorerNode, depth int) controls.TreeNode {
	// The suffix is added here rather than written into n.label, so clearing the
	// filter needn't unpick it from a string the loaders own.
	label := n.label
	// A server root saved under a profile name leads with it. Display only:
	// n.label stays the connection label sameObject matches by (the status-bar
	// breadcrumb and the Details title add the profile name themselves).
	if depth == 0 && n.data.Type == NodeServer && n.data.conn != nil {
		if profile := oe.app.profileOf(n.data.conn.Opts); profile != "" {
			label = profile + " (" + label + ")"
		}
	}
	if n.data.Filter.active() {
		label += " (filtered)"
	}
	return controls.TreeNode{
		Label:   label,
		Icon:    nodeIcon(n.data, oe.app.cfg.IconStyle, n.Expanded),
		Dim:     n.data.dimmed(),
		HasKids: hasChildren(n.data.Type),
	}
}

// ---- TreeView callback adapters ----

// handleActivate is a node's default action (Enter, or a double-click). Only
// leaves standing for something openable claim it; everything else answers false
// and keeps Enter's expand.
//
// Deliberately narrow: an object node's menu offers several actions and none is
// obviously "the" one, so guessing would make Enter unpredictable.
func (oe *ObjectExplorer) handleActivate(id controls.TreeNodeID) bool {
	n, ok := oe.tree.ByID(id)
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
	case NodeQueryStoreReport:
		sc := resolveConn(n)
		if sc == nil {
			return false
		}
		oe.app.showQueryStorePanelFor(sc, n.data.DBName, n.data.Name)
		return true
	case NodeEventTarget:
		sc := resolveConn(n)
		if sc == nil {
			return false
		}
		oe.app.showXEventTargetData(sc, n)
		return true
	case NodeXEventProfilerSession:
		sc := resolveConn(n)
		if sc == nil {
			return false
		}
		oe.app.launchXEventProfilerNamed(sc, n.data.Name)
		return true
	case NodeError:
		sc := resolveConn(n)
		if sc == nil || n.data.UnregisteredPeer == "" {
			return false
		}
		// The note's loader is its parent's; re-running it follows the primary
		// through the connection just registered. Matched by key, not pointer:
		// a Refresh while the prompt was open replaces the parent.
		parent := sameNodeAs(n.listingParent())
		return oe.app.offerPeerConnect(sc, &db.PeerNotRegisteredError{Server: n.data.UnregisteredPeer}, func() {
			oe.ReloadFolders(sc, parent)
		})
	}
	return false
}

func (oe *ObjectExplorer) handleSelect(n *explorerNode) {
	oe.follow = nil // the user moved on; the selection is no longer the write's
	oe.drill = nil
	oe.app.onNodeSelected(n)
}

// handleReselect is a selection the tree moved itself, off a node a rebuild no
// longer shows. A followed node's selection parked on its survivor is still
// the write's: Follow's target answers for it there too.
func (oe *ObjectExplorer) handleReselect(from, n *explorerNode) {
	if f := oe.follow; f != nil && from == f.from {
		f.parked = n
	}
	oe.app.onNodeReselected(from, n)
}

func (oe *ObjectExplorer) handleRightClick(id controls.TreeNodeID, x, y int) {
	if n, ok := oe.tree.ByID(id); ok {
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
	for cur := n; cur != nil; cur = cur.Parent {
		parts = append(parts, cur.label)
	}
	slices.Reverse(parts)
	return strings.Join(parts, " > ")
}

// nodePath is FormatNodePath for the status bar: a server root saved under a
// profile is called by the profile name, as in the Details title.
func (a *App) nodePath(n *explorerNode) string {
	path := FormatNodePath(n)
	if n == nil {
		return path
	}
	root := n
	for root.Parent != nil {
		root = root.Parent
	}
	if root.data.Type == NodeServer && root.data.conn != nil {
		if profile := a.profileOf(root.data.conn.Opts); profile != "" {
			return profile + path[len(root.label):]
		}
	}
	return path
}

// resolveConn walks up the explorer tree to find the owning connection. Every
// node fetchChildren creates carries its connection; the walk matters only for
// nodes without one, such as error placeholders.
func resolveConn(n *explorerNode) *db.ServerConn {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.data.conn != nil {
			return cur.data.conn
		}
	}
	return nil
}
