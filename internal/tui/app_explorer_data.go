package tui

import (
	"context"
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// childFetchTimeout bounds a single Object Explorer expand/refresh — long
// enough for a slow or remote server, short enough that a dead connection
// doesn't leave a node stuck showing "Loading..." forever.
const childFetchTimeout = 30 * time.Second

// loadChildren loads child nodes for an explorer node in the background.
// If node already has a fetch in flight (a fast double-expand, or a
// Refresh while the initial load hasn't returned yet), beginLoad cancels
// it and its result — even if it arrives late — is discarded by endLoad,
// so it can never clobber the newer one.
func (a *App) loadChildren(node *explorerNode) {
	ctx, seq := node.beginLoad(childFetchTimeout)
	go func() {
		children := a.fetchChildren(ctx, node)
		a.postEvent(func() {
			if !node.endLoad(seq) {
				return // superseded by a newer fetch for this node
			}
			a.explorer.SetChildren(node, children)
		})
		a.wakeEventLoop()
	}()
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
	sc := resolveConn(node)
	newQuery := controls.MenuItem{Label: "New Query", Action: func() { a.newQueryPanelForConn(sc, node.data.DBName) }}
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
			newQuery,
			{Divider: true},
			{Label: "Disconnect", Action: func() { a.disconnectActive() }},
			{Divider: true},
			{Label: "New Database...", Action: func() { a.showNewDatabaseDialog(sc) }},
			{Divider: true},
			{Label: "Activity Monitor", Action: func() { a.showActivityMonitorFor(sc) }},
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() { a.showServerPropertiesFor(sc) }},
		}
	case NodeDatabases:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "New Database...", Action: func() { a.showNewDatabaseDialog(sc) }},
			{Divider: true},
			{Label: "Back Up Database...", Action: func() { a.showBackupDialog(sc, "") }},
			{Label: "Restore Database...", Action: func() { a.showRestoreDialog(sc, "") }},
			{Divider: true},
			refresh,
		}
	case NodeDatabase:
		offlineLabel := "Take Database Offline"
		if node.data.IsOffline {
			offlineLabel = "Bring Database Online"
		}
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Back Up Database...", Action: func() { a.showBackupDialog(sc, node.data.DBName) }},
			{Label: "Restore Database...", Action: func() { a.showRestoreDialog(sc, node.data.DBName) }},
			{Label: offlineLabel, Action: func() { a.toggleDatabaseOffline(sc, node) }},
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() { a.showDatabasePropertiesFor(sc, node.data.DBName) }},
		}
	case NodeLogins:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "New Login...", Action: func() { a.showNewLoginDialog(sc) }},
			{Divider: true},
			refresh,
		}
	case NodeLogin:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() { a.showLoginProperties(sc, node.data.Name) }},
		}
	case NodeUser:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showUserPropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodeDatabaseRole:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showRolePropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodeSchema:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showSchemaPropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodeTable:
		tableFQN := fqn(node.data.Schema, node.data.Name)
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Select Top 1000 Rows", Action: func() {
				a.openQueryWithText(sc, node.data.DBName, "SELECT TOP 1000 *\nFROM "+tableFQN)
			}},
			{Divider: true},
			{Label: "Script Table as CREATE", Action: func() { a.scriptObject(node, "CREATE") }},
			{Label: "Script Table as DROP", Action: func() { a.scriptObject(node, "DROP") }},
			{Divider: true},
			{Label: "Rebuild All Indexes", Action: func() {
				a.openQueryWithText(sc, node.data.DBName, "ALTER INDEX ALL ON "+tableFQN+" REBUILD")
			}},
			{Label: "View Dependencies", Action: func() { a.showDependencies(node) }},
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showTablePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
			}},
		}
	case NodeView:
		viewFQN := fqn(node.data.Schema, node.data.Name)
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Select Top 1000 Rows", Action: func() {
				a.openQueryWithText(sc, node.data.DBName, "SELECT TOP 1000 *\nFROM "+viewFQN)
			}},
			{Label: "Script View as CREATE", Action: func() { a.scriptObject(node, "CREATE") }},
			{Divider: true},
			{Label: "View Dependencies", Action: func() { a.showDependencies(node) }},
			{Divider: true},
			refresh,
		}
	case NodeStoredProcedure:
		procFQN := fqn(node.data.Schema, node.data.Name)
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Script Proc as CREATE", Action: func() { a.scriptObject(node, "CREATE") }},
			{Label: "Execute Stored Procedure", Action: func() {
				a.openQueryWithText(sc, node.data.DBName, "EXEC "+procFQN)
			}},
			{Divider: true},
			{Label: "View Dependencies", Action: func() { a.showDependencies(node) }},
			{Divider: true},
			refresh,
		}
	default:
		return []controls.MenuItem{newQuery, {Divider: true}, refresh}
	}
}

// showDependencies displays what node's object depends on and what depends
// on it (Object Explorer > View Dependencies), backed by gosmo's
// Dependencies/Dependents.
func (a *App) showDependencies(node *explorerNode) {
	sc := resolveConn(node)
	if sc == nil {
		return
	}
	a.propsDialog.ShowDependencies(a, sc, node.data.DBName, node.data.Schema, node.data.Name)
}

func (a *App) scriptObject(node *explorerNode, action string) {
	sc := resolveConn(node)
	if sc == nil {
		return
	}
	schema, name, dbName := node.data.Schema, node.data.Name, node.data.DBName

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
		if err != nil {
			a.postEvent(func() { a.setStatus(fmt.Sprintf("Script error: %v", err)) })
			a.wakeEventLoop()
			return
		}
		opts := gosmo.DefaultScriptOptions()
		opts.ScriptDrops = action == "DROP"
		scripter := gosmo.NewScripter(dbObj, opts)
		var ddl string
		switch node.data.Type {
		case NodeTable:
			ddl, err = scripter.ScriptTableContext(ctx, schema, name)
		case NodeView:
			ddl, err = scripter.ScriptViewContext(ctx, schema, name)
		case NodeStoredProcedure:
			ddl, err = scripter.ScriptStoredProcedureContext(ctx, schema, name)
		case NodeFunction:
			ddl, err = scripter.ScriptFunctionContext(ctx, schema, name)
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
			qp.editor.SetText(ddl)
			a.panels.SetActive(a.panels.AddPanel(qp))
			a.focusPanels()
			a.connectForQueryPanel(qp, sc, dbName)
		})
		a.wakeEventLoop()
	}()
}

// toggleDatabaseOffline takes node's database offline, or brings it back
// online if it's already offline — Object Explorer's "Take Database
// Offline"/"Bring Database Online" action. Unlike backupDatabase, this
// runs for real immediately rather than only generating a script, so
// going offline (which rolls back every existing connection to the
// database) is confirmed first; coming back online is not, since it's
// non-destructive. On success, only this one node's icon needs updating —
// no Databases-folder reload, since nothing was added, removed, or
// renamed.
func (a *App) toggleDatabaseOffline(sc *db.ServerConn, node *explorerNode) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	dbName := node.data.DBName
	goOffline := !node.data.IsOffline

	run := func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
			defer cancel()
			d := sc.Server.Database(dbName)
			var err error
			if goOffline {
				err = d.SetOfflineContext(ctx)
			} else {
				err = d.SetOnlineContext(ctx)
			}
			a.postEvent(func() {
				if err != nil {
					word := "online"
					if goOffline {
						word = "offline"
					}
					a.setStatus(fmt.Sprintf("Failed to take %q %s: %v", dbName, word, err))
					return
				}
				node.data.IsOffline = goOffline
				a.explorer.rebuild()
				word := "online"
				if goOffline {
					word = "offline"
				}
				a.setStatus(fmt.Sprintf("Database %q is now %s", dbName, word))
			})
			a.wakeEventLoop()
		}()
	}

	if goOffline {
		a.confirmDialog.ShowConfirm("Take Database Offline",
			fmt.Sprintf("Take %q offline? Existing connections to it will be rolled back immediately.", dbName),
			func(confirmed bool) {
				if confirmed {
					run()
				}
			})
		return
	}
	run()
}
