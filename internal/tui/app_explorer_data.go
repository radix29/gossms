package tui

import (
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
			refresh,
			{Divider: true},
			{Label: "Properties...", Action: func() { a.showServerPropertiesFor(sc) }},
		}
	case NodeDatabase:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Back Up Database...", Action: func() { a.backupDatabase(sc, node.data.DBName) }},
			{Divider: true},
			refresh,
			{Divider: true},
			{Label: "Properties...", Action: func() { a.showDatabasePropertiesFor(sc, node.data.DBName) }},
		}
	case NodeLogin:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Divider: true},
			{Label: "Properties...", Action: func() { a.showLoginProperties(sc, node.data.Name) }},
		}
	case NodeTable:
		tableFQN := fqn(node.data.Schema, node.data.Name)
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Select Top 1000 Rows", Action: func() {
				a.openQueryWithText(sc, node.data.DBName, "SELECT TOP 1000 *\nFROM "+tableFQN)
			}},
			{Label: "Edit Top 200 Rows", Action: func() {
				a.openQueryWithText(sc, node.data.DBName, "SELECT TOP 200 *\nFROM "+tableFQN)
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
		dbObj, err := sc.Server.DatabaseByName(dbName)
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
			ddl, err = scripter.ScriptTable(schema, name)
		case NodeView:
			ddl, err = scripter.ScriptView(schema, name)
		case NodeStoredProcedure:
			ddl, err = scripter.ScriptStoredProcedure(schema, name)
		case NodeFunction:
			ddl, err = scripter.ScriptFunction(schema, name)
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

// backupDatabase prompts for a backup device path, generates the BACKUP
// DATABASE statement for it, and opens that in a new query — the same
// "generate a script, let the user review and run it" pattern as Execute
// Stored Procedure and Rebuild All Indexes, rather than running the backup
// itself.
func (a *App) backupDatabase(sc *db.ServerConn, dbName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.pathPrompt.Prompt("Back Up Database", dbName+".bak", func(path string) {
		stmt, err := gosmo.BuildBackupStatement(gosmo.BackupOptions{
			Database: dbName,
			Devices:  []string{path},
			Init:     true,
		})
		if err != nil {
			a.setStatus(fmt.Sprintf("Backup script error: %v", err))
			return
		}
		a.openQueryWithText(sc, dbName, stmt)
	})
}
