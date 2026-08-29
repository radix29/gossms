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

// serverWriteTimeout bounds one write statement issued from a menu action.
// Deliberately far longer than childFetchTimeout: that budget is sized for a
// folder listing, and a write is not a read.
//
// A drop, a rename, an offline, a failover waits — for a lock another session
// holds, and, on a database, for WITH ROLLBACK IMMEDIATE to roll back every
// transaction it just killed. Minutes is a normal duration for that; on a 30s
// budget the statement is abandoned mid-flight, leaving gosmo's repair pass to
// put the database back to MULTI_USER on an expired context.
//
// Bounded, not unlimited: nothing on screen is blocked while this runs, so a
// generous bound costs only a late message, but a dead connection still has to
// report rather than leaving the status line pending forever. A write the user
// waits *in* a dialog for is a different case and takes no deadline at all —
// see PropDialog.runPipeline, which runs against the dialog's own context so
// Escape is what stops it.
const serverWriteTimeout = 5 * time.Minute

// serverWriteContext bounds one such write. Every menu-driven write shares it
// so there is no per-site timeout to reach for the wrong one of — the mistake
// being that childFetchTimeout is what every *read* here uses, and the writes
// sit among them.
func serverWriteContext(sc *db.ServerConn) (context.Context, context.CancelFunc) {
	return context.WithTimeout(sc.Context(), serverWriteTimeout)
}

// loadChildren loads child nodes for an explorer node in the background.
// If node already has a fetch in flight (a fast double-expand, or a
// Refresh while the initial load hasn't returned yet), beginLoad cancels
// it and its result — even if it arrives late — is discarded by endLoad,
// so it can never clobber the newer one.
func (a *App) loadChildren(node *explorerNode) {
	ctx, seq := node.beginLoad(resolveConn(node).Context(), childFetchTimeout)
	// The fetch reads a snapshot, never the live node: applyNodeFilter writes
	// node.data.Filter on the UI goroutine while this is in flight. node itself
	// stays behind for the posted callback, which runs on the UI goroutine.
	snap := node.snapshot()
	// safegoRepair, not safego: handleExpand latched the node at "Loading..."
	// before calling this (data.Loaded is still false), and the SetChildren
	// below is the only thing that clears it. A panic unwinds past the posted
	// callback entirely, so without the repair the node keeps spinning until
	// the user happens to collapse and re-expand it — with nothing on screen
	// saying why.
	a.safegoRepair("loading Object Explorer children", func() { a.childFetchPanicked(node, seq) }, func() {
		children := a.fetchChildren(ctx, snap)
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
// Refresh is what retries — collapsing and re-expanding redisplays the error
// instead of refetching. That is the same bargain an ordinary loader error
// makes, and being told the expand failed is worth more than a silent retry on
// a gesture most users won't think to make.
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
	a.primeDatabaseCapabilities(node)
	a.detailBrowser.ShowNodeDetails(a, node)
}

// primeDatabaseCapabilities warms the per-database capability cache for the
// node the user has just moved to, off the UI goroutine.
//
// A menu item's Enabled predicate runs while the menu is being drawn and can
// only read the cache (CachedDatabaseCapabilities), so without this every
// database-scope gate would fail open until something else happened to probe.
// Selecting a node is the move that precedes opening its menu, and the probe
// is two round trips on the first touch of a database and nothing afterwards.
func (a *App) primeDatabaseCapabilities(node *explorerNode) {
	sc, dbName := resolveConn(node), node.data.DBName
	// A SQL Agent node carries no DBName — it hangs off the server, not a
	// database — but what permits its New-X actions is membership of an msdb
	// role, so msdb is the database its menu asks about. Without this the
	// Agent gates read an unprobed msdb and fail open for the whole session.
	if isAgentNode(node.data.Type) {
		dbName = "msdb"
	}
	if sc == nil || dbName == "" {
		return
	}
	a.safego("priming database capabilities", func() {
		sc.DatabaseCapabilities(sc.Context(), dbName)
	})
}

func (a *App) showContextMenu(node *explorerNode, x, y int) {
	a.contextMenu.Show(x, y, a.contextMenuItemsForNode(node))
}

// contextMenuItemsForNode is the node's own menu plus the three groups every
// node type gets for free: Script <Noun> as (scripting.go), Rename/Delete
// (explorer_object_ops.go) and, on a filterable folder, Filter
// Settings/Remove Filter (explorer_filter.go). All three are spliced in above
// Refresh, where SSMS puts them, rather than repeated in each branch of
// nodeMenuItems — which node types offer them is scriptables', objectOpFor's
// and filterProps's answer, not something those branches know.
func (a *App) contextMenuItemsForNode(node *explorerNode) []controls.MenuItem {
	items := a.nodeMenuItems(node)
	items = insertBeforeRefresh(items, a.scriptMenuItems(node))
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
			gate(controls.MenuItem{Label: "New Database...", Action: func() { a.showNewDatabaseDialog(sc) }},
				sc, "", rightCreateAnyDatabase, rightAlterAnyDatabase),
			{Divider: true},
			gate(controls.MenuItem{Label: "Activity Monitor", Action: func() { a.showActivityMonitorFor(sc) }},
				sc, "", rightViewServerState),
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
			gate(controls.MenuItem{Label: "New Database...", Action: func() { a.showNewDatabaseDialog(sc) }},
				sc, "", rightCreateAnyDatabase, rightAlterAnyDatabase),
			gate(controls.MenuItem{Label: "Attach Database...", Action: func() { a.showAttachDatabaseDialog(sc) }},
				sc, "", rightCreateAnyDatabase, rightAlterAnyDatabase),
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
		items := []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "Back Up Database...", Action: func() { a.showBackupDialog(sc, node.data.DBName) }},
				sc, node.data.DBName, rightBackupDatabase, rightAlterAnyDatabase),
			gate(controls.MenuItem{Label: "Restore Database...", Action: func() { a.showRestoreDialog(sc, node.data.DBName) }},
				sc, node.data.DBName, rightControlDB, rightAlterAnyDatabase, rightCreateAnyDatabase),
			{Label: "View Backup History", Action: func() { a.showBackupHistoryFor(sc, node.data.DBName) }},
			gate(controls.MenuItem{Label: offlineLabel, Action: func() { a.toggleDatabaseOffline(sc, node) }},
				sc, node.data.DBName, rightAlterDatabase, rightAlterAnyDatabase),
		}
		// Detach is offered on user databases only: sp_detach_db refuses a
		// system database outright, and a permanently grey item explains
		// nothing the name doesn't already say.
		if !node.data.IsSystem {
			items = append(items,
				gate(controls.MenuItem{Label: "Detach Database...", Action: func() {
					a.showDetachDatabaseDialog(sc, node.data.DBName)
				}}, sc, node.data.DBName, rightControlDB, rightAlterAnyDatabase))
		}
		return append(items,
			controls.MenuItem{Divider: true},
			refresh,
			controls.MenuItem{Label: "Properties...", Action: func() { a.showDatabasePropertiesFor(sc, node.data.DBName) }},
		)
	case NodeQueryStore:
		// The folder's own settings are a Database Properties page, not a
		// dialog of its own — the same page SSMS puts them on.
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "Open Query Store...", Action: func() {
				a.showQueryStorePanelFor(sc, node.data.DBName, "")
			}}, sc, node.data.DBName, rightViewDBState),
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() { a.showDatabasePropertiesFor(sc, node.data.DBName) }},
		}
	case NodeQueryStoreReport:
		// The leaf's own Detail Browser grid is the report; this opens the
		// same view in the panel, where the metric, the statistic and the
		// window can be changed and a plan can be forced.
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "Open in Query Store Panel", Action: func() {
				a.showQueryStorePanelFor(sc, node.data.DBName, node.data.Name)
			}}, sc, node.data.DBName, rightViewDBState),
			{Divider: true},
			refresh,
		}
	case NodeLogins:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "New Login...", Action: func() { a.showNewLoginDialog(sc) }},
				sc, "", rightAlterAnyLogin),
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
	case NodePartitionFunction:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showPartitionFunctionPropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodePartitionScheme:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showPartitionSchemePropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodeSecurityPolicy:
		toggleLabel := "Disable"
		if !node.data.IsEnabled {
			toggleLabel = "Enable"
		}
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: toggleLabel, Action: func() { a.toggleSecurityPolicy(sc, node) }},
				sc, node.data.DBName, rightAlterAnySecPolicy),
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showSecurityPolicyPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
			}},
		}
	case NodeColumnMasterKeys:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "New Column Master Key...",
				Action: func() { a.showNewColumnMasterKeyDialog(sc, node) }},
				sc, node.data.DBName, rightAlterAnyCMK),
			{Divider: true},
			refresh,
		}
	case NodeColumnEncryptionKeys:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "New Column Encryption Key...",
				Action: func() { a.showNewColumnEncryptionKeyDialog(sc, node) }},
				sc, node.data.DBName, rightAlterAnyCEK),
			{Divider: true},
			refresh,
		}
	case NodeColumnMasterKey:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showColumnMasterKeyPropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodeColumnEncryptionKey:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			refresh,
			{Label: "Properties...", Action: func() {
				a.showColumnEncryptionKeyPropertiesFor(sc, node.data.DBName, node.data.Name)
			}},
		}
	case NodeIndexes:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gateOn(controls.MenuItem{Label: "New Index", Sub: a.newIndexMenuItems(sc, node)},
				sc, node.data.DBName, node.data.Schema, node.data.Name, objectWriteRights()...),
			{Divider: true},
			{Label: "Rebuild All Indexes", Action: func() {
				a.openQueryWithText(sc, node.data.DBName,
					"ALTER INDEX ALL ON "+fqn(node.data.Schema, node.data.Name)+" REBUILD")
			}},
			{Divider: true},
			refresh,
		}
	case NodeStatistics:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gateOn(controls.MenuItem{Label: "New Statistics...",
				Action: func() { a.showNewStatisticsDialog(sc, node) }},
				sc, node.data.DBName, node.data.Schema, node.data.Name, objectWriteRights()...),
			{Divider: true},
			refresh,
		}
	case NodeStatistic:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			{Label: "Update Statistics", Action: func() {
				a.generateScript(sc, node.data, statisticUpdateVerb, func(text string) {
					a.openQueryWithText(sc, node.data.DBName, text)
				})
			}},
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
			gate(controls.MenuItem{Label: "New Job...", Action: func() { a.showNewJobDialog(sc) }},
				sc, "msdb", agentWriteRights()...),
			{Divider: true},
			refresh,
		}
	case NodeAgentSchedules:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "New Schedule...", Action: func() { a.showNewScheduleDialog(sc) }},
				sc, "msdb", agentWriteRights()...),
			{Divider: true},
			refresh,
		}
	case NodeAgentEventAlerts:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "New Alert...", Action: func() { a.showNewAlertDialog(sc) }},
				sc, "msdb", agentWriteRights()...),
			{Divider: true},
			refresh,
		}
	case NodeAgentOperators:
		return []controls.MenuItem{
			newQuery,
			{Divider: true},
			gate(controls.MenuItem{Label: "New Operator...", Action: func() { a.showNewOperatorDialog(sc) }},
				sc, "msdb", agentWriteRights()...),
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
			gate(controls.MenuItem{Label: "Recycle", Action: func() { a.recycleLogFrom(sc, logType, node) }},
				sc, "", rightControlServer),
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

// toggleSecurityPolicy enables or disables node's row-level security policy
// — SSMS's Enable/Disable on the policy. Disabling one stops it filtering
// and blocking anything, so the whole table becomes visible to every user;
// that is the state change, not a cosmetic flag, and the node's label
// carries it (see loadSecurityPoliciesChildren), which is why the parent
// folder is refreshed rather than just the icon repainted.
func (a *App) toggleSecurityPolicy(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	enable := !node.data.IsEnabled
	dbName, schema, name := node.data.DBName, node.data.Schema, node.data.Name

	run := func() {
		a.safego("enabling/disabling a security policy", func() {
			ctx, cancel := serverWriteContext(sc)
			defer cancel()
			p, err := findSecurityPolicy(ctx, sc, dbName, schema, name)
			if err == nil {
				if enable {
					err = p.EnableContext(ctx)
				} else {
					err = p.DisableContext(ctx)
				}
			}
			a.postAndWake(func() {
				word := "disable"
				if enable {
					word = "enable"
				}
				if err != nil {
					a.setStatus(fmt.Sprintf("Failed to %s %q: %v", word, fqn(schema, name), err))
					return
				}
				node.data.IsEnabled = enable
				if parent := node.parent; parent != nil {
					refreshExplorerNode(a, parent)
				}
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("Security policy %q is now %sd", fqn(schema, name), word))
			})
		})
	}

	if !enable {
		a.confirmDialog.ShowConfirm("Disable Security Policy",
			fmt.Sprintf("Disable %s? Its filter and block predicates stop applying, and every row of the tables it protects becomes visible.", fqn(schema, name)),
			func(confirmed bool) {
				if confirmed {
					run()
				}
			})
		return
	}
	run()
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
			ctx, cancel := serverWriteContext(sc)
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
