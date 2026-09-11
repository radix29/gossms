package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// childFetchTimeout bounds one Object Explorer expand/refresh: enough for a
// slow server, short enough that a dead connection doesn't leave "Loading..."
// forever.
const childFetchTimeout = 30 * time.Second

// serverWriteTimeout bounds a menu-action write, far longer than
// childFetchTimeout, which is sized for folder listings.
//
// A drop, rename, offline or failover can wait minutes on another session's
// lock, or for WITH ROLLBACK IMMEDIATE to roll back killed transactions. At 30s
// the statement would be abandoned mid-flight, leaving gosmo's repair pass
// (e.g. back to MULTI_USER) on an expired context.
//
// Bounded because a dead connection must still report; nothing on screen is
// blocked meanwhile. Writes the user waits for in a dialog take no deadline
// (PropDialog.runPipeline uses the dialog's context; Escape stops it).
const serverWriteTimeout = 5 * time.Minute

// serverWriteContext bounds a menu-driven write. Shared so no site reaches for
// childFetchTimeout, which every read here uses.
func serverWriteContext(sc *db.ServerConn) (context.Context, context.CancelFunc) {
	return context.WithTimeout(sc.Context(), serverWriteTimeout)
}

// loadChildren loads an explorer node's children in the background. A load
// already in flight (double expand, Refresh during load) is cancelled by
// beginLoad, and endLoad discards its late result.
//
// A retired node (replaced by a Reload, expanded from its stale row) isn't
// loaded; SetChildren would refuse it.
func (a *App) loadChildren(node *explorerNode) {
	if node.retired {
		return
	}
	ctx, seq := node.beginLoad(resolveConn(node).Context(), childFetchTimeout)
	// The fetch reads a snapshot: applyNodeFilter writes node.data.Filter on
	// the UI goroutine meanwhile. node itself is used only by the posted
	// callback on the UI goroutine.
	snap := node.snapshot()
	// safegoRepair, not safego: the node is latched at "Loading..." and only
	// SetChildren below clears it. A panic skips the posted callback, so
	// without repair the node spins silently until re-expanded.
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

// errChildFetchPanicked is shown when a loader panicked; the stack is already
// logged (see reportPanic).
var errChildFetchPanicked = errors.New("loading failed unexpectedly — see the log for details")

// childFetchPanicked ends a panicked load, replacing "Loading..." with an error
// node like an ordinary loader failure.
//
// SetChildren marks the node Loaded, so Refresh retries (re-expanding
// redisplays the error) — the same trade as ordinary errors; a visible failure
// beats a silent retry.
//
// Guarded by seq like the success path, so a newer expand's children aren't
// overwritten.
func (a *App) childFetchPanicked(node *explorerNode, seq int) {
	if !node.endLoad(seq) {
		return
	}
	a.explorer.SetChildren(node, []*explorerNode{errExplorerNode(errChildFetchPanicked)})
}

// refreshAgentRootLabel appends " (Stopped)" to the "SQL Server Agent" child
// once a background AgentInfoContext check says it isn't running. Separate from
// loadServerChildren so that static loader never waits on the round trip. A
// failed check leaves the label alone.
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

// onNodeReselected is onNodeSelected for a selection Object Explorer moved
// itself (off a node a rebuild no longer shows; ObjectExplorer.reselect).
// Details follows; the status bar only while it still shows from's path, so a
// write's "deleted"/"renamed" message isn't replaced by the reload that
// follows.
func (a *App) onNodeReselected(from, node *explorerNode) {
	if a.statusText == FormatNodePath(from) {
		a.setStatus(FormatNodePath(node))
	}
	a.primeDatabaseCapabilities(node)
	a.detailBrowser.ShowNodeDetails(a, node)
}

// primeDatabaseCapabilities warms the capability cache for the newly selected
// node, off the UI goroutine. Enabled predicates only read the cache
// (CachedDatabaseCapabilities), so without this database gates fail open.
// Selection precedes opening a menu; the probe is two round trips on a
// database's first touch.
func (a *App) primeDatabaseCapabilities(node *explorerNode) {
	sc, dbName := resolveConn(node), node.data.DBName
	// Agent nodes have no DBName, but their New-X actions need msdb roles, so
	// their menu asks about msdb; otherwise the Agent gates read an unprobed
	// msdb and fail open.
	if isAgentNode(node.data.Type) {
		dbName = "msdb"
	}
	// Already cached (the common case): no goroutine.
	if sc == nil || dbName == "" || sc.HasDatabaseCapabilities(dbName) {
		return
	}
	a.safego("priming database capabilities", func() {
		sc.DatabaseCapabilities(sc.Context(), dbName)
	})
}

func (a *App) showContextMenu(node *explorerNode, x, y int) {
	a.contextMenu.Show(x, y, a.contextMenuItemsForNode(node))
}

// contextMenuItemsForNode is the node's menu plus three shared groups: Script
// <Noun> as (scripting.go), Rename/Delete (explorer_object_ops.go) and, on
// filterable folders, Filter Settings/Remove Filter (explorer_filter.go).
// Spliced above Refresh as SSMS does; which types get them is decided by
// scriptables, objectOpFor and filterProps, not the nodeMenus builders.
func (a *App) contextMenuItemsForNode(node *explorerNode) []controls.MenuItem {
	items := a.nodeMenuItems(node)
	items = insertBeforeRefresh(items, a.scriptMenuItems(node))
	items = insertBeforeRefresh(items, a.objectOpsMenuItems(node))
	return insertBeforeRefresh(items, a.filterMenuItems(node))
}

// filterMenuItems is a filterable folder's Filter pair, or nil.
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

// refreshMenuLabel is every menu's Refresh label, which insertBeforeRefresh
// anchors on.
const refreshMenuLabel = "Refresh"

// insertBeforeRefresh splices extra above Refresh as its own divided group,
// leaving Refresh and Properties... last as SSMS does. Dividers are added only
// where missing. A menu without Refresh gets extra appended.
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

// nodeMenuItems is node's own menu from its nodeMenus builder.
func (a *App) nodeMenuItems(node *explorerNode) []controls.MenuItem {
	sc := resolveConn(node)
	newQuery := controls.MenuItem{Label: "New Query", Action: func() { a.newQueryPanelForConn(sc, node.data.DBName) }}
	refresh := controls.MenuItem{Label: refreshMenuLabel, Action: func() { a.explorer.Reload(node) }}

	if build, ok := nodeMenus[node.data.Type]; ok {
		return build(a, sc, node, newQuery, refresh)
	}
	return []controls.MenuItem{newQuery, {Divider: true}, refresh}
}

// showDependencies shows what node's object depends on and what depends on it
// (View Dependencies), via gosmo's Dependencies/Dependents.
func (a *App) showDependencies(node *explorerNode) {
	sc := resolveConn(node)
	if sc == nil {
		return
	}
	a.propsDialog.ShowDependencies(a, sc, node.data.DBName, node.data.Schema, node.data.Name)
}

// toggleSecurityPolicy enables or disables node's row-level security policy.
// Disabling makes the whole table visible to every user. The state is in the
// node's label (see loadSecurityPoliciesChildren), so the parent folder is
// refreshed.
func (a *App) toggleSecurityPolicy(sc *db.ServerConn, node *explorerNode) {
	dbName, schema := node.data.DBName, node.data.Schema
	display := fqn(schema, node.data.Name)
	a.toggleEnabledState(sc, node, "security policy", display,
		"Disable Security Policy",
		fmt.Sprintf("Disable %s? Its filter and block predicates stop applying, and every row of the tables it protects becomes visible.", display),
		func(ctx context.Context, name string, on bool) error {
			p, err := findSecurityPolicy(ctx, sc, dbName, schema, name)
			if err != nil {
				return err
			}
			if on {
				return p.EnableContext(ctx)
			}
			return p.DisableContext(ctx)
		})
}

// toggleServerTrigger enables or disables a server-scope DDL or logon trigger.
// Disabling (confirmed) stops its policy instance-wide. State is in the label
// (loadServerTriggersChildren), so the parent is refreshed.
func (a *App) toggleServerTrigger(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.toggleEnabledState(sc, node, "server trigger", name,
		"Disable Server Trigger",
		fmt.Sprintf("Disable %s? The DDL or logon policy it enforces stops applying server-wide.", name),
		func(ctx context.Context, name string, on bool) error {
			t := sc.Server.ServerTrigger(name)
			if on {
				return t.EnableContext(ctx)
			}
			return t.DisableContext(ctx)
		})
}

// toggleDatabaseTrigger enables or disables a database DDL trigger. Disabling
// (confirmed) stops its policy database-wide. State is in the label
// (loadDatabaseTriggersChildren), so the parent is refreshed.
func (a *App) toggleDatabaseTrigger(sc *db.ServerConn, node *explorerNode) {
	name, dbName := node.data.Name, node.data.DBName
	a.toggleEnabledState(sc, node, "database trigger", name,
		"Disable Database Trigger",
		fmt.Sprintf("Disable %s? The DDL policy it enforces stops applying in %s.", name, dbName),
		func(ctx context.Context, name string, on bool) error {
			// Database, not DatabaseByName: addressing a trigger needs no
			// sys.databases read.
			t := sc.Server.Database(dbName).DatabaseTrigger(name)
			if on {
				return t.EnableContext(ctx)
			}
			return t.DisableContext(ctx)
		})
}

// auditToggleLabel is the Enable/Disable wording for an audit or server audit
// specification, from cached state.
func auditToggleLabel(node *explorerNode) string {
	if node.data.IsEnabled {
		return "Disable"
	}
	return "Enable"
}

// toggleAudit enables or disables a server audit. Disabling (confirmed) stops
// recording. State is in the label (loadAuditsChildren), so the parent is
// refreshed.
func (a *App) toggleAudit(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.toggleEnabledState(sc, node, "audit", name,
		"Disable Audit",
		fmt.Sprintf("Disable %s? The instance stops recording anything through it.", name),
		func(ctx context.Context, name string, on bool) error {
			return sc.Server.ServerAudit(name).SetStateContext(ctx, on)
		})
}

// toggleServerAuditSpecification enables or disables node's specification.
func (a *App) toggleServerAuditSpecification(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.toggleEnabledState(sc, node, "server audit specification", name,
		"Disable Server Audit Specification",
		fmt.Sprintf("Disable %s? The action groups it names stop being recorded.", name),
		func(ctx context.Context, name string, on bool) error {
			return sc.Server.ServerAuditSpecification(name).SetStateContext(ctx, on)
		})
}

// toggleDatabaseAuditSpecification enables or disables node's specification,
// via the name-only database handle.
func (a *App) toggleDatabaseAuditSpecification(sc *db.ServerConn, node *explorerNode) {
	name, dbName := node.data.Name, node.data.DBName
	a.toggleEnabledState(sc, node, "database audit specification", name,
		"Disable Database Audit Specification",
		fmt.Sprintf("Disable %s? The action groups and actions it names stop being recorded.", name),
		func(ctx context.Context, name string, on bool) error {
			return sc.Server.Database(dbName).DatabaseAuditSpecification(name).SetStateContext(ctx, on)
		})
}

// toggleEnabledState is the shared Enable/Disable toggle for security policies,
// triggers (both scopes), audits, audit specifications and plan guides, which
// differ only in wording and gosmo call. display is the name as shown
// (schema-qualified for policies); prompt is the formatted confirmation.
// Disabling is confirmed, enabling isn't; the parent folder is refreshed
// because state lives in child labels.
func (a *App) toggleEnabledState(sc *db.ServerConn, node *explorerNode, noun, display, title, prompt string,
	set func(ctx context.Context, name string, on bool) error) {
	if !a.requireConn(sc) {
		return
	}
	enable := !node.data.IsEnabled
	name := node.data.Name

	run := func() {
		article := "a "
		if strings.ContainsRune("aeiou", rune(noun[0])) {
			article = "an "
		}
		word, doing, jobTitle := "disable", "Disabling", title
		if enable {
			word, doing, jobTitle = "enable", "Enabling", "Enable"+strings.TrimPrefix(title, "Disable")
		}
		a.runWithProgress(progressJob{
			title:   jobTitle,
			message: fmt.Sprintf("%s %s %q...", doing, noun, display),
			what:    "enabling/disabling " + article + noun,
			sc:      sc,
		}, func(ctx context.Context, _ progressReport) error {
			return set(ctx, name, enable)
		}, func(err error, cancelled bool) {
			switch {
			case cancelled:
				// Re-read rather than assumed: the cancel may have arrived
				// after the commit.
				a.setStatus(fmt.Sprintf("%s %q cancelled", doing, display))
				if parent := node.parent; parent != nil {
					a.explorer.Reload(parent)
				}
			case err != nil:
				a.setStatus(fmt.Sprintf("Failed to %s %q: %v", word, display, err))
			default:
				node.data.IsEnabled = enable
				if parent := node.parent; parent != nil {
					a.explorer.Reload(parent)
				}
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("%s %q is now %sd", strings.ToUpper(noun[:1])+noun[1:], display, word))
			}
		})
	}

	if !enable {
		a.confirmDialog.ShowConfirm(title, prompt, func(confirmed bool) {
			if confirmed {
				run()
			}
		})
		return
	}
	run()
}

// togglePlanGuide enables or disables a plan guide (sp_control_plan_guide, as
// Plan Guide Properties' General page).
//
// Disabling is confirmed: a disabled guide shows up only as a regressed query.
// State is in the label (loadPlanGuidesChildren), so the parent is refreshed.
// Server.Database, not DatabaseByName: nothing is read off sys.databases.
func (a *App) togglePlanGuide(sc *db.ServerConn, node *explorerNode) {
	name, dbName := node.data.Name, node.data.DBName
	a.toggleEnabledState(sc, node, "plan guide", name,
		"Disable Plan Guide",
		fmt.Sprintf("Disable %s? The queries it applies hints to go back to the plans the optimizer picks on its own.", name),
		func(ctx context.Context, name string, on bool) error {
			g := sc.Server.Database(dbName).PlanGuide(name)
			if on {
				return g.EnableContext(ctx)
			}
			return g.DisableContext(ctx)
		})
}

// setEndpointState starts, stops or disables an endpoint. Stopping or disabling
// (confirmed) cuts off everything using it.
//
// Built-in endpoints are refused with a message rather than a greyed item
// (greyed implies the login lacks permission). gosmo refuses too; this is the
// explanation.
func (a *App) setEndpointState(sc *db.ServerConn, node *explorerNode, state gosmo.EndpointState) {
	if !a.requireConn(sc) {
		return
	}
	if node.data.IsSystem {
		a.setStatus(fmt.Sprintf("%q is a built-in endpoint — SQL Server does not allow its state to be changed", node.data.Name))
		return
	}
	name := node.data.Name

	run := func() {
		a.runWithProgress(progressJob{
			title:   "Change Endpoint State",
			message: fmt.Sprintf("Setting endpoint %q to %s...", name, state),
			what:    "changing an endpoint's state",
			sc:      sc,
		}, func(ctx context.Context, _ progressReport) error {
			e, err := sc.Server.EndpointByNameContext(ctx, name)
			if err != nil {
				return err
			}
			return e.SetStateContext(ctx, state)
		}, func(err error, cancelled bool) {
			switch {
			case cancelled:
				a.setStatus(fmt.Sprintf("Setting %q to %s cancelled", name, state))
				if parent := node.parent; parent != nil {
					a.explorer.Reload(parent)
				}
			case err != nil:
				a.setStatus(fmt.Sprintf("Failed to set %q to %s: %v", name, state, err))
			default:
				node.data.IsEnabled = state == gosmo.EndpointStarted
				if parent := node.parent; parent != nil {
					a.explorer.Reload(parent)
				}
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("Endpoint %q is now %s", name, endpointStateLabel(string(state))))
			}
		})
	}

	if state != gosmo.EndpointStarted {
		a.confirmDialog.ShowConfirm("Change Endpoint State",
			fmt.Sprintf("Set %s to %s? Availability replicas, mirroring partners and Service Broker routes connecting through it stop being able to.",
				name, state),
			func(confirmed bool) {
				if confirmed {
					run()
				}
			})
		return
	}
	run()
}

// toggleDatabaseOffline takes node's database offline, or back online (Take
// Offline / Bring Online). Offline rolls back every connection, so it's
// confirmed. On success the node's state updates and its subtree reloads: an
// offline database shows a single "(Database is offline)" placeholder
// (explorer_databases.go), and a stale online subtree mustn't be re-queried
// against it.
func (a *App) toggleDatabaseOffline(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	dbName := node.data.DBName
	goOffline := !node.data.IsOffline

	run := func() {
		word, title, message := "online", "Bring Database Online", fmt.Sprintf("Bringing %q online...", dbName)
		if goOffline {
			word, title, message = "offline", "Take Database Offline", fmt.Sprintf("Taking %q offline...", dbName)
		}
		a.runWithProgress(progressJob{
			title:   title,
			message: message,
			what:    "changing a database's online state",
			sc:      sc,
		}, func(ctx context.Context, _ progressReport) error {
			d := sc.Server.Database(dbName)
			if goOffline {
				return d.SetOfflineContext(ctx)
			}
			return d.SetOnlineContext(ctx)
		}, func(err error, cancelled bool) {
			switch {
			case cancelled:
				// Refresh the databases folder: after a cancel the node's state
				// is unknown until re-read.
				a.setStatus(fmt.Sprintf("Taking %q %s cancelled", dbName, word))
				a.explorer.RefreshDatabasesFolder(sc)
			case err != nil:
				a.setStatus(fmt.Sprintf("Failed to take %q %s: %v", dbName, word, err))
			default:
				node.data.IsOffline = goOffline
				a.explorer.Reload(node)
				a.explorer.rebuild() // repaint node's own icon immediately even when it's collapsed (Reload only rebuilds once an expanded reload completes)
				a.setStatus(fmt.Sprintf("Database %q is now %s", dbName, word))
			}
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

// restoreFromSnapshot reverts the snapshot's source database to it (RESTORE
// DATABASE … FROM DATABASE_SNAPSHOT).
//
// The typed confirmation is the source's name, since what's destroyed is every
// change to the source since the snapshot.
//
// The server checks its own preconditions (the source has exactly one snapshot;
// no connections to either database); both can change between a check here and
// the statement, and the server names the failure.
func (a *App) restoreFromSnapshot(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	snapshot, source := node.data.Name, node.data.SourceDatabase
	if source == "" {
		a.setStatus(fmt.Sprintf("Snapshot %q cannot be restored from: its source database has been dropped", snapshot))
		return
	}
	msg := fmt.Sprintf(
		"Revert %q to snapshot %q? Every change made to %q since the snapshot was taken is lost, and connections to either database are closed. Type the database name to confirm.",
		source, snapshot, source)
	a.confirmTypedDialog.ShowTypedConfirm("Restore Database from Snapshot", msg, source, func(confirmed bool) {
		if !confirmed {
			return
		}
		// Uninterruptible: a half-done revert leaves the source RESTORING and
		// unusable.
		a.runWithProgress(progressJob{
			title:           "Restore Database from Snapshot",
			message:         fmt.Sprintf("Reverting %q to snapshot %q...", source, snapshot),
			what:            "restoring a database from a snapshot",
			sc:              sc,
			uninterruptible: uninterruptibleRestore,
		}, func(ctx context.Context, _ progressReport) error {
			return sc.Server.RestoreFromSnapshotContext(ctx, source, snapshot)
		}, func(err error, _ bool) {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to restore %q from %q: %v", source, snapshot, displayError(err)))
				return
			}
			a.setStatus(fmt.Sprintf("Database %q reverted to snapshot %q", source, snapshot))
			a.explorer.RefreshDatabasesFolder(sc)
		})
	})
}

// forgetPeerFailuresForRefresh drops sc's cached peer connect failures when
// refreshing an Always On node — the only tree the peer cache serves, and where
// a user who fixed the network needs "try again" rather than waiting out
// peerFailureTTL.
func forgetPeerFailuresForRefresh(sc *db.ServerConn, node *explorerNode) {
	if sc == nil || node == nil {
		return
	}
	if isAlwaysOnNode(node.data.Type) {
		sc.ForgetPeerFailures()
	}
}

// isAlwaysOnNode reports whether t is in the Always On subtree.
func isAlwaysOnNode(t NodeType) bool {
	switch t {
	case NodeAlwaysOn, NodeAvailabilityGroups, NodeAvailabilityGroup,
		NodeAvailabilityReplicas, NodeAvailabilityReplica,
		NodeAvailabilityDatabases, NodeAvailabilityDatabase,
		NodeAGListeners, NodeAGListener:
		return true
	}
	return false
}
