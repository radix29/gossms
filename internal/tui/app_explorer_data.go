package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	ctx, seq := node.beginLoad(resolveConn(node).Context(), childFetchTimeout)
	// safegoRepair, not safego: handleExpand latched the node at "Loading..."
	// before calling this (data.Loaded is still false), and the SetChildren
	// below is the only thing that clears it. A panic unwinds past the posted
	// callback entirely, so without the repair the node keeps spinning until
	// the user happens to collapse and re-expand it — with nothing on screen
	// saying why.
	a.safegoRepair("loading Object Explorer children", func() { a.childFetchPanicked(node, seq) }, func() {
		children := a.fetchChildren(ctx, node)
		a.postAndWake(func() {
			if !node.endLoad(seq) {
				return // superseded by a newer fetch for this node
			}
			a.explorer.SetChildren(node, children)
			if node.data.Type == NodeServer {
				a.refreshAgentRootLabel(node)
			}
		})
	})
}

// errChildFetchPanicked is what an expand shows when its loader panicked. The
// stack is already in the log by the time this is displayed (see reportPanic);
// the tree has room for one line.
var errChildFetchPanicked = errors.New("loading failed unexpectedly — see the log for details")

// childFetchPanicked ends the load a panic abandoned, replacing the
// "Loading..." placeholder with the same kind of error node fetchChildren
// produces for an ordinary loader failure.
//
// Note what that costs, deliberately: SetChildren marks the node Loaded, so
// Refresh is what retries — collapsing and re-expanding now redisplays the
// error instead of refetching, which is what it did before this repair
// existed. That is the same bargain an ordinary loader error already makes,
// and being told the expand failed is worth more than a silent retry on a
// gesture most users won't think to make.
//
// Guarded by seq exactly as the success path is: a newer expand has already
// latched the node for itself, and overwriting its children with this one's
// error is the bug endLoad exists to prevent.
func (a *App) childFetchPanicked(node *explorerNode, seq int) {
	if !node.endLoad(seq) {
		return
	}
	a.explorer.SetChildren(node, []*explorerNode{errExplorerNode(errChildFetchPanicked)})
}

// refreshAgentRootLabel appends " (Stopped)" to the just-shown "SQL Server
// Agent" child's label once a background AgentInfoContext check confirms
// the service isn't running. Split out of loadServerChildren, which stays a
// static no-query loader, so this round trip never blocks the rest of the
// server's top-level folders from appearing. A failed or inconclusive
// check leaves the label alone.
func (a *App) refreshAgentRootLabel(serverNode *explorerNode) {
	var agentNode *explorerNode
	for _, c := range serverNode.children {
		if c.data.Type == NodeAgentJobs {
			agentNode = c
			break
		}
	}
	sc := serverNode.data.conn
	if agentNode == nil || sc == nil || sc.Server == nil {
		return
	}
	a.safego("refreshing the SQL Server Agent node", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		status, err := sc.Server.AgentInfoContext(ctx)
		a.postAndWake(func() {
			if err != nil || status.StatusText == "" || status.StatusText == "Unknown" || status.Running {
				return
			}
			agentNode.label = agentRootLabel + " (Stopped)"
			a.explorer.rebuild()
		})
	})
}

func (a *App) onNodeSelected(node *explorerNode) {
	a.setStatus(FormatNodePath(node))
	a.detailBrowser.ShowNodeDetails(a, node)
}

func (a *App) showContextMenu(node *explorerNode, x, y int) {
	a.contextMenu.Show(x, y, a.contextMenuItemsForNode(node))
}

// contextMenuItemsForNode is the node's own menu plus the two groups every
// node type gets for free: Rename/Delete (explorer_object_ops.go) and, on a
// filterable folder, Filter Settings/Remove Filter (explorer_filter.go).
// Both are spliced in above Refresh, where SSMS puts them, rather than
// repeated in each branch of nodeMenuItems — which node types offer them is
// objectOpFor's and filterProps's answer, not something those branches know.
func (a *App) contextMenuItemsForNode(node *explorerNode) []controls.MenuItem {
	items := a.nodeMenuItems(node)
	items = insertBeforeRefresh(items, a.objectOpsMenuItems(node))
	return insertBeforeRefresh(items, a.filterMenuItems(node))
}

// filterMenuItems is the Filter pair a filterable folder offers, or nil.
func (a *App) filterMenuItems(node *explorerNode) []controls.MenuItem {
	if len(filterProps(node.data.Type)) == 0 {
		return nil
	}
	return []controls.MenuItem{
		{Label: "Filter Settings...", Action: func() { a.showFilterDialog(node) }},
		{
			Label:   "Remove Filter",
			Enabled: func() bool { return node.data.Filter.active() },
			Action:  func() { a.applyNodeFilter(node, nil) },
		},
	}
}

// refreshMenuLabel is the label the Refresh item carries in every node's
// menu, and the anchor insertBeforeRefresh finds it by.
const refreshMenuLabel = "Refresh"

// insertBeforeRefresh splices extra in above the Refresh item as its own
// divided group, leaving Refresh and Properties... last the way SSMS does.
// The dividers are added only where one isn't already there — every node
// menu already has one above Refresh, and two in a row draw as two lines.
// A menu with no Refresh — no node type today, but a leaf that can't be
// reloaded would be one — gets extra appended instead.
func insertBeforeRefresh(items, extra []controls.MenuItem) []controls.MenuItem {
	if len(extra) == 0 {
		return items
	}
	for i, it := range items {
		if it.Label != refreshMenuLabel {
			continue
		}
		group := extra
		if i > 0 && !items[i-1].Divider {
			group = append([]controls.MenuItem{{Divider: true}}, group...)
		}
		group = append(slices.Clone(group), controls.MenuItem{Divider: true})
		out := make([]controls.MenuItem, 0, len(items)+len(group))
		out = append(out, items[:i]...)
		out = append(out, group...)
		return append(out, items[i:]...)
	}
	return append(items, append([]controls.MenuItem{{Divider: true}}, extra...)...)
}

func (a *App) nodeMenuItems(node *explorerNode) []controls.MenuItem {
	sc := resolveConn(node)
	newQuery := controls.MenuItem{Label: "New Query", Action: func() { a.newQueryPanelForConn(sc, node.data.DBName) }}
	refresh := controls.MenuItem{Label: refreshMenuLabel, Action: func() {
		node.data.Loaded = false
		node.children = nil
		if node.expanded {
			a.loadChildren(node)
		}
		a.detailBrowser.Invalidate(a, node)
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
			{Label: "View SQL Server Log", Action: func() {
				a.showLogViewerFor(sc, gosmo.ErrorLogSQLServer, 0)
			}},
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
			{Label: "View Backup History", Action: func() { a.showBackupHistoryFor(sc, node.data.DBName) }},
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
	case NodeServerRole:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() { a.showServerRolePropertiesFor(sc, node.data.Name) }},
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
	case NodeKey:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showKeyPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.TableName, node.data.Name, node.data.IsPrimaryKey)
			}},
		}
	case NodeForeignKey:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showForeignKeyPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.TableName, node.data.Name)
			}},
		}
	case NodeIndex:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showIndexPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.TableName, node.data.Name)
			}},
		}
	case NodeStatistic:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showStatisticPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.TableName, node.data.Name)
			}},
		}
	case NodeAlwaysOn:
		return alwaysOnRootMenuItems(a, sc, node, newQuery, refresh)
	case NodeAvailabilityGroups:
		return agGroupsFolderMenuItems(a, sc, node, newQuery, refresh)
	case NodeAvailabilityGroup:
		return agGroupMenuItems(a, sc, node, newQuery, refresh)
	case NodeAvailabilityReplicas:
		return agReplicasFolderMenuItems(a, sc, node, newQuery, refresh)
	case NodeAvailabilityDatabases:
		return agDatabasesFolderMenuItems(a, sc, node, newQuery, refresh)
	case NodeAvailabilityDatabase:
		return agDatabaseMenuItems(a, sc, node, newQuery, refresh)
	case NodeAvailabilityReplica:
		return agReplicaMenuItems(a, sc, node, newQuery, refresh)
	case NodeAGListeners:
		return agListenersFolderMenuItems(a, sc, node, newQuery, refresh)
	case NodeAGListener:
		return agListenerMenuItems(a, sc, node, refresh)
	case NodeAgentUserJobs:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "New Job...", Action: func() { a.showNewJobDialog(sc) }},
			{Divider: true},
			refresh,
		}
	case NodeAgentSchedules:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "New Schedule...", Action: func() { a.showNewScheduleDialog(sc) }},
			{Divider: true},
			refresh,
		}
	case NodeAgentEventAlerts:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "New Alert...", Action: func() { a.showNewAlertDialog(sc) }},
			{Divider: true},
			refresh,
		}
	case NodeAgentOperators:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "New Operator...", Action: func() { a.showNewOperatorDialog(sc) }},
			{Divider: true},
			refresh,
		}
	case NodeManagement:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "View SQL Server Log", Action: func() {
				a.showLogViewerFor(sc, gosmo.ErrorLogSQLServer, 0)
			}},
			{Divider: true},
			refresh,
		}
	case NodeSQLServerLogs, NodeAgentErrorLogs:
		logType := gosmo.ErrorLogSQLServer
		if node.data.Type == NodeAgentErrorLogs {
			logType = gosmo.ErrorLogAgent
		}
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "View Current Log", Action: func() { a.showLogViewerFor(sc, logType, 0) }},
			{Divider: true},
			refresh,
		}
	case NodeSQLServerLog, NodeAgentErrorLog:
		return []controls.MenuItem{
			{Label: "View Log", Action: func() {
				a.showLogViewerFor(sc, node.data.LogType, node.data.LogNumber)
			}},
			{Divider: true},
			newQuery,
			{Divider: true},
			refresh,
		}
	case NodeAgentJob:
		return agentJobMenuItems(a, sc, node, refresh)
	case NodeAgentSchedule:
		return agentScheduleMenuItems(a, sc, node, refresh)
	case NodeAgentAlert:
		return agentAlertMenuItems(a, sc, node, refresh)
	case NodeAgentOperator:
		return agentOperatorMenuItems(a, sc, node, refresh)
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

	a.safego("scripting an object", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
		if err != nil {
			a.postAndWake(func() { a.setStatus(fmt.Sprintf("Script error: %v", err)) })
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
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Script error: %v", err))
				return
			}
			a.queryPanelCnt++
			qp := NewQueryPanel(a, fmt.Sprintf("Script %d", a.queryPanelCnt))
			qp.editor.SetText(ddl)
			a.panels.SetActive(a.panels.AddPanel(qp))
			a.focusPanels()
			a.connectForQueryPanel(qp, sc, dbName, nil)
		})
	})
}

// toggleDatabaseOffline takes node's database offline, or brings it back
// online if it's already offline — Object Explorer's "Take Database
// Offline"/"Bring Database Online" action. This runs for real immediately,
// so going offline (which rolls back every existing connection to the
// database) is confirmed first; coming back online is not. On success
// node's icon/state updates and its subtree is refreshed via
// refreshExplorerNode: an offline database's expanded children are the
// single "(Database is offline)" placeholder leaf (see
// explorer_databases.go), and an online one's real Tables/Views subtree
// must not linger stale and get re-queried against a now-offline database.
func (a *App) toggleDatabaseOffline(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	dbName := node.data.DBName
	goOffline := !node.data.IsOffline

	run := func() {
		a.safego("changing a database's online state", func() {
			ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
			defer cancel()
			d := sc.Server.Database(dbName)
			var err error
			if goOffline {
				err = d.SetOfflineContext(ctx)
			} else {
				err = d.SetOnlineContext(ctx)
			}
			a.postAndWake(func() {
				if err != nil {
					word := "online"
					if goOffline {
						word = "offline"
					}
					a.setStatus(fmt.Sprintf("Failed to take %q %s: %v", dbName, word, err))
					return
				}
				node.data.IsOffline = goOffline
				refreshExplorerNode(a, node)
				a.explorer.rebuild() // repaint node's own icon immediately even when it's collapsed (refreshExplorerNode only rebuilds once an expanded reload completes)
				word := "online"
				if goOffline {
					word = "offline"
				}
				a.setStatus(fmt.Sprintf("Database %q is now %s", dbName, word))
			})
		})
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
