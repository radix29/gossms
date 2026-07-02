// Package tui is the gossms application layer. It wires together the
// reusable, embeddable controls from internal/tuikit (theme, core, widgets,
// layout, dialogs, controls) into the SQL-Server-specific Object Explorer,
// query panels, and dialogs that make up goSSMS.
//
// Everything in tuikit is application-agnostic; everything in this package
// knows about gosmo, config.Connection, and SQL Server object types.
package tui

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// App is the root application struct that owns the screen and all UI panels.
// It is the only place in the codebase where tuikit controls are bound to
// SQL-Server-specific behaviour.
type App struct {
	screen tcell.Screen

	explorerSplit *layout.Splitter

	explorer    *ObjectExplorer
	panels      *layout.PanelManager
	menuBar     *controls.MenuBar
	contextMenu *controls.ContextMenu
	statusText  string
	queryPanelCnt int

	connectDialog *ConnectDialog
	connStrDialog *ConnStrDialog
	helpDialog    *HelpDialog
	propsDialog   *PropertiesDialog

	connections []*db.ServerConn
	cfg         *config.Config

	// Focus: "explorer" | "panels"
	focus string

	pendingMu sync.Mutex
	pending   []func()
}

// NewApp constructs the application.
func NewApp() *App {
	return new(App{
		focus:      "explorer",
		statusText: "Ready  |  F1 Help  |  Ctrl+N New Query  |  Ctrl+O Connect  |  Ctrl+Q Quit",
		cfg:        config.Load(),
	})
}

// Run initialises the screen and enters the event loop.
func (a *App) Run() error {
	s, err := core.Init()
	if err != nil {
		return fmt.Errorf("init screen: %w", err)
	}
	a.screen = s
	defer s.Fini()

	a.buildUI()
	a.layoutAll()
	a.draw()

	// tcell v3's Screen interface has no PollEvent/PostEvent methods; events
	// are delivered and posted through a plain channel, EventQ(). The
	// channel is closed by Fini(), so `for ev := range` exits automatically
	// on quit instead of needing a nil-event sentinel check.
	for ev := range s.EventQ() {
		a.drainPending()

		switch e := ev.(type) {
		case *tcell.EventResize:
			s.Sync()
			a.layoutAll()
		case *tcell.EventInterrupt:
			// triggered after background goroutine posts result
		case *tcell.EventKey:
			if a.handleKey(e) {
				return nil
			}
		case *tcell.EventMouse:
			a.handleMouse(e)
		}
		a.draw()
	}
	return nil
}

func (a *App) postEvent(fn func()) {
	a.pendingMu.Lock()
	a.pending = append(a.pending, fn)
	a.pendingMu.Unlock()
}

func (a *App) drainPending() {
	a.pendingMu.Lock()
	fns := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// buildUI creates all UI components from tuikit building blocks.
func (a *App) buildUI() {
	a.explorer = NewObjectExplorer(a)
	a.explorer.SetActive(true)

	a.explorerSplit = layout.NewVerticalSplitter()
	a.explorerSplit.SetRatio(0.3)

	a.panels = layout.NewPanelManager()
	a.panels.AddPanel(NewDetailBrowser("Object Explorer Details"))

	a.menuBar = controls.NewMenuBar()
	a.menuBar.SetMenus(a.buildMenus())

	a.contextMenu = new(controls.ContextMenu{})
	a.connectDialog = NewConnectDialog(a)
	a.connStrDialog = NewConnStrDialog(a)
	a.helpDialog = NewHelpDialog(a)
	a.propsDialog = NewPropertiesDialog(a)
}

func (a *App) buildMenus() []controls.Menu {
	return []controls.Menu{
		{Label: "File", Items: []controls.MenuItem{
			{Label: "Connect...", Shortcut: "Ctrl+O", Action: func() { a.connectDialog.Show() }},
			{Label: "Disconnect", Action: func() { a.disconnectActive() }},
			{Divider: true},
			{Label: "New Query", Shortcut: "Ctrl+N", Action: func() { a.newQueryPanel() }},
			{Label: "Close Query", Shortcut: "Ctrl+W", Action: func() { a.closeActivePanel() }},
			{Divider: true},
			{Label: "Exit", Shortcut: "Ctrl+Q", Action: func() { a.quit() }},
		}},
		{Label: "Edit", Items: []controls.MenuItem{
			{Label: "Undo", Shortcut: "Ctrl+Z"},
			{Label: "Redo", Shortcut: "Ctrl+Y"},
			{Divider: true},
			{Label: "Select All", Shortcut: "Ctrl+A"},
		}},
		{Label: "View", Items: []controls.MenuItem{
			{Label: "Object Explorer", Action: func() { a.focusExplorer() }},
			{Label: "Next Panel", Shortcut: "Ctrl+Tab", Action: func() { a.panels.Next() }},
			{Label: "Prev Panel", Action: func() { a.panels.Prev() }},
			{Divider: true},
			{Label: "Refresh", Shortcut: "F5", Action: func() { a.refreshSelected() }},
		}},
		{Label: "Query", Items: []controls.MenuItem{
			{Label: "Execute", Shortcut: "F5", Action: func() { a.executeActiveQuery() }},
			{Label: "New Query Window", Shortcut: "Ctrl+N", Action: func() { a.newQueryPanel() }},
			{Divider: true},
			{Label: "Connect...", Action: func() { a.connectDialog.Show() }},
		}},
		{Label: "Tools", Items: []controls.MenuItem{
			{Label: "Server Properties...", Action: func() { a.showServerProperties() }},
		}},
		{Label: "Help", Items: []controls.MenuItem{
			{Label: "Help", Shortcut: "F1", Action: func() { a.helpDialog.Show() }},
			{Divider: true},
			{Label: "About goSSMS", Action: func() { a.showAbout() }},
		}},
	}
}

func (a *App) showAbout() {
	a.propsDialog.ShowGenericProperties("About goSSMS", []PropertyRow{
		{Key: "Application", Value: "goSSMS"},
		{Key: "Version", Value: "1.0.0"},
		{Key: "Go Version", Value: "1.26"},
		{Key: "Description", Value: "Go SQL Server Management Studio TUI"},
		{Key: "Author", Value: "radix29"},
		{Key: "Repository", Value: "github.com/radix29/gossms"},
		{Key: "DB Framework", Value: "github.com/radix29/gosmo"},
		{Key: "TUI Library", Value: "internal/tuikit (embedded)"},
		{Key: "TUI Backend", Value: "github.com/gdamore/tcell/v3"},
	})
}

func (a *App) focusExplorer() {
	a.focus = "explorer"
	a.explorer.SetActive(true)
}

func (a *App) focusPanels() {
	a.focus = "panels"
	a.explorer.SetActive(false)
}

// layoutAll recalculates every region from current screen size.
func (a *App) layoutAll() {
	w, h := a.screen.Size()
	if w < 20 || h < 5 {
		return
	}
	const menuH, statusH = 1, 1
	contentH := h - menuH - statusH

	a.menuBar.SetBounds(0, 0, w)
	a.explorerSplit.SetBounds(0, menuH, w, contentH)

	left := a.explorerSplit.FirstRect()
	right := a.explorerSplit.SecondRect()
	a.explorer.SetBounds(left.X, left.Y, left.W, left.H)
	a.panels.SetBounds(right.X, right.Y, right.W, right.H)
}

func (a *App) draw() {
	s := a.screen
	w, h := s.Size()
	s.Clear()

	a.menuBar.Draw(s)
	a.explorerSplit.Draw(s)
	a.explorer.Draw(s)
	a.panels.Draw(s)

	// Status bar
	const statusH = 1
	statusStyle := theme.StyleStatusBar()
	core.FillRect(s, core.Rect{X: 0, Y: h - statusH, W: w, H: statusH}, ' ', statusStyle)
	connInfo := ""
	if len(a.connections) > 0 {
		connInfo = fmt.Sprintf("  |  %d connection(s)", len(a.connections))
	}
	core.DrawTextClipped(s, 1, h-statusH, w-2, statusStyle, a.statusText+connInfo)

	// Overlays — drawn last so they aren't painted over by panels/status bar,
	// which occupy the same rows the menu dropdown and context menu extend into.
	a.menuBar.DrawOverlay(s)
	a.contextMenu.Draw(s)

	// Modal dialogs — highest z-order, one at a time
	switch {
	case a.propsDialog.Visible():
		a.propsDialog.Draw(s)
	case a.helpDialog.Visible():
		a.helpDialog.Draw(s)
	case a.connStrDialog.Visible():
		a.connStrDialog.Draw(s)
	case a.connectDialog.Visible():
		a.connectDialog.Draw(s)
	}

	s.Show()
}

// handleKey processes keyboard events. Returns true to signal quit.
func (a *App) handleKey(ev *tcell.EventKey) (quit bool) {
	switch {
	case a.propsDialog.Visible():
		a.propsDialog.HandleKey(ev)
		return false
	case a.helpDialog.Visible():
		a.helpDialog.HandleKey(ev)
		return false
	case a.connStrDialog.Visible():
		a.connStrDialog.HandleKey(ev)
		return false
	case a.connectDialog.Visible():
		a.connectDialog.HandleKey(ev)
		return false
	}
	if a.contextMenu.Visible() {
		a.contextMenu.HandleKey(ev)
		return false
	}
	if a.menuBar.IsOpen() {
		a.menuBar.HandleKey(ev)
		return false
	}

	switch ev.Key() {
	case tcell.KeyF1:
		a.helpDialog.Show()
		return false
	case tcell.KeyCtrlC, tcell.KeyCtrlQ:
		a.quit()
		return true
	case tcell.KeyCtrlN:
		a.newQueryPanel()
		return false
	case tcell.KeyCtrlW:
		a.closeActivePanel()
		return false
	case tcell.KeyCtrlO:
		a.connectDialog.Show()
		return false
	case tcell.KeyF5:
		if a.focus == "explorer" {
			a.refreshSelected()
		} else {
			a.executeActiveQuery()
		}
		return false
	case tcell.KeyTab:
		// tcell has no distinct KeyCtrlTab constant — Ctrl+Tab is reported
		// as KeyTab with ModCtrl set (on terminals that support a modern
		// keyboard protocol; on legacy terminals it may be indistinguishable
		// from plain Tab, in which case it simply falls through to the
		// explorer/panel focus toggle below).
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			a.panels.Next()
		} else if a.focus == "explorer" {
			a.focusPanels()
		} else {
			a.focusExplorer()
		}
		return false
	}

	// Explorer splitter keyboard resize (Ctrl+Left/Right)
	if a.explorerSplit.HandleKey(ev) {
		a.layoutAll()
		return false
	}

	if a.focus == "explorer" {
		a.explorer.HandleKey(ev)
	} else {
		a.panels.HandleKey(ev)
	}
	return false
}

func (a *App) handleMouse(ev *tcell.EventMouse) {
	mx, my := ev.Position()

	switch {
	case a.propsDialog.Visible():
		a.propsDialog.HandleMouse(ev)
		return
	case a.helpDialog.Visible():
		a.helpDialog.HandleMouse(ev)
		return
	case a.connStrDialog.Visible():
		a.connStrDialog.HandleMouse(ev)
		return
	case a.connectDialog.Visible():
		a.connectDialog.HandleMouse(ev)
		return
	}
	if a.contextMenu.Visible() {
		a.contextMenu.HandleMouse(ev)
		return
	}
	if my == 0 {
		a.menuBar.HandleMouse(ev)
		return
	}
	if a.menuBar.IsOpen() {
		if a.menuBar.HandleMouse(ev) {
			return
		}
		a.menuBar.Close()
	}

	// Explorer/panel splitter drag
	if a.explorerSplit.HandleMouse(ev) {
		a.layoutAll()
		return
	}

	left := a.explorerSplit.FirstRect()
	if mx < left.Right() {
		if a.focus != "explorer" {
			a.focusExplorer()
		}
		a.explorer.HandleMouse(ev)
		return
	}
	if a.focus != "panels" {
		a.focusPanels()
	}
	a.panels.HandleMouse(ev)
}

// ---- Connection management ----

func (a *App) connectServer(opts config.Connection) {
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))
	a.draw()

	go func() {
		sc, err := db.Connect(opts)
		a.postEvent(func() {
			if err != nil {
				if dbErr, ok := errors.AsType[*db.ConnectionError](err); ok {
					a.setStatus(fmt.Sprintf("Connection error [%s]: %s", dbErr.Server, dbErr.Cause))
				} else {
					a.setStatus(fmt.Sprintf("Connection failed: %v", err))
				}
				return
			}
			connIdx := len(a.connections)
			a.connections = append(a.connections, sc)
			a.explorer.AddRoot(sc.Opts.DisplayName(), connIdx)
			info := sc.Server.Info()
			a.setStatus(fmt.Sprintf("Connected to %s  |  %s %s", opts.Server, info.Edition, info.ProductVersion))
			a.screen.EventQ() <- tcell.NewEventInterrupt(nil)
		})
	}()
}

func (a *App) disconnectActive() {
	node := a.explorer.Selected()
	if node == nil {
		return
	}
	root := node
	for root.parent != nil {
		root = root.parent
	}
	connIdx := root.data.connIdx
	if connIdx < 0 || connIdx >= len(a.connections) {
		return
	}
	a.connections[connIdx].Close()
	a.connections = slices.Delete(a.connections, connIdx, connIdx+1)
	a.explorer.RemoveRootByConn(connIdx)
	// Shift connIdx references for all remaining roots above the removed one.
	for _, r := range a.explorer.roots {
		if r.data.connIdx > connIdx {
			r.data.connIdx--
		}
	}
	a.setStatus("Disconnected")
}

// loadChildren loads child nodes for an explorer node in the background.
func (a *App) loadChildren(node *explorerNode) {
	go func() {
		children := a.fetchChildren(node)
		a.postEvent(func() {
			a.explorer.SetChildren(node, children)
			a.screen.EventQ() <- tcell.NewEventInterrupt(nil)
		})
	}()
}

// fetchChildren queries the database for child nodes (runs in a background
// goroutine — must not touch ObjectExplorer's id-allocation or map state;
// node.id is left zero and assigned later by SetChildren on the UI goroutine).
func (a *App) fetchChildren(node *explorerNode) []*explorerNode {
	connIdx := resolveConnIdx(node)
	if connIdx < 0 || connIdx >= len(a.connections) {
		return []*explorerNode{errExplorerNode(fmt.Errorf("not connected"))}
	}
	sc := a.connections[connIdx]

	mk := func(label string, t NodeType, schema, dbName string) *explorerNode {
		return &explorerNode{
			label: label,
			data:  nodeData{Type: t, Schema: schema, DBName: dbName, connIdx: connIdx},
		}
	}
	errNode := func(err error) []*explorerNode {
		log.Printf("fetchChildren [%v]: %v", node.data.Type, err)
		return []*explorerNode{errExplorerNode(err)}
	}

	switch node.data.Type {
	case NodeServer:
		return []*explorerNode{
			mk("Databases", NodeDatabases, "", ""),
			mk("Security", NodeSecurity, "", ""),
			mk("Server Objects", NodeManagement, "", ""),
		}

	case NodeDatabases:
		dbs, err := sc.Server.Databases()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(dbs))
		for _, d := range dbs {
			out = append(out, mk(d.Name(), NodeDatabase, "", d.Name()))
		}
		return out

	case NodeDatabase:
		dbName := node.data.DBName
		return []*explorerNode{
			mk("Tables", NodeTables, "", dbName),
			mk("Views", NodeViews, "", dbName),
			mk("Stored Procedures", NodeStoredProcedures, "", dbName),
			mk("Functions", NodeFunctions, "", dbName),
			mk("Triggers", NodeTriggers, "", dbName),
			mk("Sequences", NodeSequences, "", dbName),
			mk("Synonyms", NodeSynonyms, "", dbName),
			mk("Security", NodeDatabaseSecurity, "", dbName),
		}

	case NodeTables:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		tables, err := dbObj.Tables()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(tables))
		for _, t := range tables {
			out = append(out, mk(t.Schema+"."+t.Name, NodeTable, t.Schema, node.data.DBName))
		}
		return out

	case NodeTable:
		tableName := node.label
		if node.data.Schema != "" && len(tableName) > len(node.data.Schema)+1 {
			tableName = tableName[len(node.data.Schema)+1:]
		}
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		table, err := dbObj.TableByName(node.data.Schema, tableName)
		if err != nil {
			return errNode(err)
		}
		cols, err := table.Columns()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(cols))
		for _, c := range cols {
			nullable := ""
			if c.IsNullable {
				nullable = " NULL"
			}
			out = append(out, mk(fmt.Sprintf("%s (%s%s)", c.Name, c.DataType, nullable),
				NodeColumn, node.data.Schema, node.data.DBName))
		}
		return out

	case NodeViews:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		views, err := dbObj.Views()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(views))
		for _, v := range views {
			out = append(out, mk(v.Schema+"."+v.Name, NodeView, v.Schema, node.data.DBName))
		}
		return out

	case NodeStoredProcedures:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		procs, err := dbObj.StoredProcedures()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(procs))
		for _, p := range procs {
			out = append(out, mk(p.Schema+"."+p.Name, NodeStoredProcedure, p.Schema, node.data.DBName))
		}
		return out

	case NodeFunctions:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		fns, err := dbObj.UserDefinedFunctions()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(fns))
		for _, f := range fns {
			out = append(out, mk(f.Schema+"."+f.Name, NodeFunction, f.Schema, node.data.DBName))
		}
		return out

	case NodeTriggers:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		triggers, err := dbObj.Triggers()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(triggers))
		for _, t := range triggers {
			out = append(out, mk(t.Schema+"."+t.Name, NodeTrigger, t.Schema, node.data.DBName))
		}
		return out

	case NodeSequences:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		seqs, err := dbObj.Sequences()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(seqs))
		for _, seq := range seqs {
			out = append(out, mk(seq.Schema+"."+seq.Name, NodeSequence, seq.Schema, node.data.DBName))
		}
		return out

	case NodeSynonyms:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		syns, err := dbObj.Synonyms()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(syns))
		for _, syn := range syns {
			out = append(out, mk(syn.Schema+"."+syn.Name, NodeSynonym, syn.Schema, node.data.DBName))
		}
		return out

	case NodeSecurity:
		return []*explorerNode{
			mk("Logins", NodeLogins, "", ""),
			mk("Server Roles", NodeServerRoles, "", ""),
		}

	case NodeLogins:
		logins, err := sc.Server.Logins()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(logins))
		for _, l := range logins {
			out = append(out, mk(l.Name, NodeLogin, "", ""))
		}
		return out

	case NodeServerRoles:
		roles, err := sc.Server.ServerRoles()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(roles))
		for _, r := range roles {
			out = append(out, mk(r.Name, NodeServerRole, "", ""))
		}
		return out

	case NodeManagement:
		return []*explorerNode{
			mk("SQL Server Agent", NodeAgentJobs, "", ""),
			mk("Linked Servers", NodeLinkedServers, "", ""),
		}

	case NodeAgentJobs:
		jobs, err := sc.Server.Jobs()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, mk(j.Name, NodeAgentJob, "", ""))
		}
		return out

	case NodeLinkedServers:
		linked, err := sc.Server.LinkedServers()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(linked))
		for _, l := range linked {
			out = append(out, mk(l.Name, NodeLinkedServer, "", ""))
		}
		return out

	case NodeDatabaseSecurity:
		return []*explorerNode{
			mk("Users", NodeUsers, "", node.data.DBName),
			mk("Roles", NodeDatabaseRoles, "", node.data.DBName),
			mk("Schemas", NodeSchemas, "", node.data.DBName),
		}

	case NodeUsers:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		users, err := dbObj.Users()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(users))
		for _, u := range users {
			out = append(out, mk(u.Name, NodeUser, "", node.data.DBName))
		}
		return out

	case NodeDatabaseRoles:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		roles, err := dbObj.DatabaseRoles()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(roles))
		for _, r := range roles {
			out = append(out, mk(r.Name, NodeDatabaseRole, "", node.data.DBName))
		}
		return out

	case NodeSchemas:
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			return errNode(err)
		}
		schemas, err := dbObj.Schemas()
		if err != nil {
			return errNode(err)
		}
		out := make([]*explorerNode, 0, len(schemas))
		for _, s := range schemas {
			out = append(out, mk(s.Name, NodeSchema, s.Name, node.data.DBName))
		}
		return out
	}
	return nil
}

// errExplorerNode builds a placeholder error node. It carries no id — the
// id is assigned later by ObjectExplorer.SetChildren on the UI goroutine.
func errExplorerNode(err error) *explorerNode {
	return &explorerNode{label: err.Error(), data: nodeData{Type: NodeError}}
}

func (a *App) onNodeSelected(node *explorerNode) {
	a.setStatus(FormatNodePath(node))
	if p := a.panels.ActivePanel(); p != nil {
		if browser, ok := p.(*DetailBrowser); ok {
			browser.ShowNodeDetails(a, node)
		}
	}
}

func (a *App) showContextMenu(node *explorerNode, x, y int) {
	a.contextMenu.Show(x, y, a.contextMenuItemsForNode(node))
}

func (a *App) contextMenuItemsForNode(node *explorerNode) []controls.MenuItem {
	connIdx := resolveConnIdx(node)
	refresh := controls.MenuItem{Label: "Refresh", Action: func() {
		node.data.Loaded = false
		node.children = nil
		if node.expanded {
			a.loadChildren(node)
		}
	}}

	switch node.data.Type {
	case NodeServer:
		return []controls.MenuItem{
			{Label: "New Query", Action: func() { a.newQueryPanelForConn(connIdx) }},
			{Divider: true},
			{Label: "Properties...", Action: func() { a.propsDialog.ShowServerProperties(a, connIdx) }},
			{Divider: true},
			{Label: "Disconnect", Action: func() { a.disconnectActive() }},
			refresh,
		}
	case NodeDatabase:
		return []controls.MenuItem{
			{Label: "New Query", Action: func() { a.newQueryPanelForConn(connIdx) }},
			{Divider: true},
			{Label: "Properties...", Action: func() { a.propsDialog.ShowDatabaseProperties(a, connIdx, node.data.DBName) }},
			refresh,
		}
	case NodeTable:
		tableFQN := fmt.Sprintf("[%s].[%s]", node.data.Schema, node.label)
		if node.data.Schema == "" {
			tableFQN = "[" + node.label + "]"
		}
		return []controls.MenuItem{
			{Label: "Select Top 1000 Rows", Action: func() {
				a.openQueryWithText(connIdx, "SELECT TOP 1000 *\nFROM "+tableFQN)
			}},
			{Label: "Edit Top 200 Rows", Action: func() {
				a.openQueryWithText(connIdx, "SELECT TOP 200 *\nFROM "+tableFQN)
			}},
			{Divider: true},
			{Label: "Script Table as CREATE", Action: func() { a.scriptObject(node, "CREATE") }},
			{Label: "Script Table as DROP", Action: func() { a.scriptObject(node, "DROP") }},
			{Divider: true},
			refresh,
		}
	case NodeView:
		viewFQN := fmt.Sprintf("[%s].[%s]", node.data.Schema, node.label)
		return []controls.MenuItem{
			{Label: "Select Top 1000 Rows", Action: func() {
				a.openQueryWithText(connIdx, "SELECT TOP 1000 *\nFROM "+viewFQN)
			}},
			{Label: "Script View as CREATE", Action: func() { a.scriptObject(node, "CREATE") }},
			{Divider: true},
			refresh,
		}
	case NodeStoredProcedure:
		return []controls.MenuItem{
			{Label: "Script Proc as CREATE", Action: func() { a.scriptObject(node, "CREATE") }},
			{Label: "Execute Stored Procedure", Action: func() {
				a.openQueryWithText(connIdx, fmt.Sprintf("EXEC [%s].[%s]", node.data.Schema, node.label))
			}},
			{Divider: true},
			refresh,
		}
	default:
		return []controls.MenuItem{refresh}
	}
}

// ---- Panel actions ----

func (a *App) newQueryPanel() {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	if len(a.connections) > 0 {
		qp.connIdx = 0
	}
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
}

func (a *App) newQueryPanelForConn(connIdx int) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.connIdx = connIdx
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
}

func (a *App) openQueryWithText(connIdx int, text string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.connIdx = connIdx
	qp.editor.SetText(text)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
}

func (a *App) closeActivePanel() {
	if i := a.panels.ActiveIndex(); i >= 0 {
		a.panels.RemovePanel(i)
	}
}

func (a *App) executeActiveQuery() {
	if p := a.panels.ActivePanel(); p != nil {
		if qp, ok := p.(*QueryPanel); ok {
			qp.Execute()
		}
	}
}

func (a *App) refreshSelected() { a.explorer.RefreshSelected() }

func (a *App) showServerProperties() {
	node := a.explorer.Selected()
	connIdx := 0
	if node != nil {
		connIdx = resolveConnIdx(node)
	} else if len(a.connections) == 0 {
		return
	}
	a.propsDialog.ShowServerProperties(a, connIdx)
}

func (a *App) scriptObject(node *explorerNode, action string) {
	connIdx := resolveConnIdx(node)
	if connIdx < 0 || connIdx >= len(a.connections) {
		return
	}
	sc := a.connections[connIdx]
	objName := node.label
	if node.data.Schema != "" && len(objName) > len(node.data.Schema)+1 {
		objName = objName[len(node.data.Schema)+1:]
	}

	go func() {
		dbObj, err := sc.Server.DatabaseByName(node.data.DBName)
		if err != nil {
			a.postEvent(func() { a.setStatus(fmt.Sprintf("Script error: %v", err)) })
			return
		}
		scripter := gosmo.NewScripter(dbObj, gosmo.DefaultScriptOptions())
		var ddl string
		switch node.data.Type {
		case NodeTable:
			ddl, err = scripter.ScriptTable(node.data.Schema, objName)
		case NodeView:
			ddl, err = scripter.ScriptView(node.data.Schema, objName)
		case NodeStoredProcedure:
			ddl, err = scripter.ScriptStoredProcedure(node.data.Schema, objName)
		case NodeFunction:
			ddl, err = scripter.ScriptFunction(node.data.Schema, objName)
		default:
			ddl = fmt.Sprintf("-- Script %s not implemented for this object type\n", action)
		}
		a.postEvent(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Script error: %v", err))
				return
			}
			a.queryPanelCnt++
			qp := NewQueryPanel(a, fmt.Sprintf("Script %d", a.queryPanelCnt))
			qp.connIdx = connIdx
			qp.editor.SetText(ddl)
			a.panels.SetActive(a.panels.AddPanel(qp))
			a.focusPanels()
			a.screen.EventQ() <- tcell.NewEventInterrupt(nil)
		})
	}()
}

func (a *App) quit()                { a.screen.Fini() }
func (a *App) setStatus(msg string) { a.statusText = msg }
