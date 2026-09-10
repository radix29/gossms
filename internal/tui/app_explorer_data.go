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
// Refresh, where SSMS puts them, rather than repeated in each nodeMenus
// builder — which node types offer them is scriptables', objectOpFor's and
// filterProps's answer, not something those builders know.
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

// nodeMenuItems is node's own context menu, from its nodeMenus builder.
func (a *App) nodeMenuItems(node *explorerNode) []controls.MenuItem {
	sc := resolveConn(node)
	newQuery := controls.MenuItem{Label: "New Query", Action: func() { a.newQueryPanelForConn(sc, node.data.DBName) }}
	refresh := controls.MenuItem{Label: refreshMenuLabel, Action: func() {
		forgetPeerFailuresForRefresh(sc, node)
		node.data.Loaded = false
		node.children = nil
		if node.expanded {
			a.loadChildren(node)
		}
		a.detailBrowser.Invalidate(a, node)
	}}

	if build, ok := nodeMenus[node.data.Type]; ok {
		return build(a, sc, node, newQuery, refresh)
	}
	return []controls.MenuItem{newQuery, {Divider: true}, refresh}
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

// toggleServerTrigger enables or disables node's server-scope DDL or logon
// trigger — SSMS's Enable/Disable on one. Disabling is what stops the policy
// it enforces from applying anywhere on the instance, so it is confirmed;
// enabling is not. The node's label carries the state (see
// loadServerTriggersChildren), which is why the parent folder is refreshed
// rather than the icon repainted.
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

// toggleDatabaseTrigger enables or disables node's database-scope DDL
// trigger — SSMS's Enable/Disable on one. Disabling is what stops the policy
// it enforces from applying anywhere in the database, so it is confirmed;
// enabling is not. The node's label carries the state (see
// loadDatabaseTriggersChildren), which is why the parent folder is refreshed
// rather than the icon repainted.
func (a *App) toggleDatabaseTrigger(sc *db.ServerConn, node *explorerNode) {
	name, dbName := node.data.Name, node.data.DBName
	a.toggleEnabledState(sc, node, "database trigger", name,
		"Disable Database Trigger",
		fmt.Sprintf("Disable %s? The DDL policy it enforces stops applying in %s.", name, dbName),
		func(ctx context.Context, name string, on bool) error {
			// Database, not DatabaseByName: the handle needs no read of
			// sys.databases to address a trigger by name.
			t := sc.Server.Database(dbName).DatabaseTrigger(name)
			if on {
				return t.EnableContext(ctx)
			}
			return t.DisableContext(ctx)
		})
}

// auditToggleLabel is the Enable/Disable item's wording for an audit or a
// server audit specification, read from the node's cached state.
func auditToggleLabel(node *explorerNode) string {
	if node.data.IsEnabled {
		return "Disable"
	}
	return "Enable"
}

// toggleAudit enables or disables node's server audit — SSMS's Enable/Disable
// Audit. Disabling stops the instance recording anything through it, so it is
// confirmed; enabling is not. The node's label carries the state (see
// loadAuditsChildren), which is why the parent folder is refreshed rather than
// the icon repainted.
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

// toggleDatabaseAuditSpecification enables or disables node's specification.
// The database handle is the name-only one: the state toggle needs nothing off
// sys.databases.
func (a *App) toggleDatabaseAuditSpecification(sc *db.ServerConn, node *explorerNode) {
	name, dbName := node.data.Name, node.data.DBName
	a.toggleEnabledState(sc, node, "database audit specification", name,
		"Disable Database Audit Specification",
		fmt.Sprintf("Disable %s? The action groups and actions it names stop being recorded.", name),
		func(ctx context.Context, name string, on bool) error {
			return sc.Server.Database(dbName).DatabaseAuditSpecification(name).SetStateContext(ctx, on)
		})
}

// toggleEnabledState is the shared half of every Enable/Disable toggle in
// Object Explorer: a security policy, the two trigger scopes, an audit, the two
// audit specifications and a plan guide differ only in the wording and the
// gosmo call. display is the name as the status line and prompt show it (a
// policy's is schema-qualified); prompt is the confirmation text, already
// formatted. Disabling is confirmed, enabling is not; the parent folder is
// refreshed afterwards because each family carries its state in the child
// label rather than in the icon.
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
		a.safego("enabling/disabling "+article+noun, func() {
			ctx, cancel := serverWriteContext(sc)
			defer cancel()
			err := set(ctx, name, enable)
			a.postAndWake(func() {
				word := "disable"
				if enable {
					word = "enable"
				}
				if err != nil {
					a.setStatus(fmt.Sprintf("Failed to %s %q: %v", word, display, err))
					return
				}
				node.data.IsEnabled = enable
				if parent := node.parent; parent != nil {
					refreshExplorerNode(a, parent)
				}
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("%s %q is now %sd", strings.ToUpper(noun[:1])+noun[1:], display, word))
			})
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

// togglePlanGuide enables or disables node's plan guide — SSMS's
// Enable/Disable on one, and the same sp_control_plan_guide call Plan Guide
// Properties' General page applies.
//
// Disabling is confirmed and enabling is not, the way the audit toggles are:
// a disabled guide stops shaping plans, which shows up as a regressed query
// rather than as anything on screen. The node's label carries the state (see
// loadPlanGuidesChildren), so the parent folder is refreshed rather than the
// icon repainted.
//
// Server.Database, not DatabaseByName: the guide is addressed by name and the
// toggle reads nothing off sys.databases.
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

// setEndpointState starts, stops or disables node's endpoint — SSMS's
// Start/Stop/Disable on one. Stopping or disabling takes the listener away
// from everything connecting through it, so both are confirmed; starting is
// not.
//
// A built-in endpoint is refused here with a message rather than by leaving
// the item greyed: greyed-out says the login may not do this, and the reason
// is the endpoint, not the login. gosmo refuses it a second time — this is the
// explanation, not the guard.
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
		a.safego("changing an endpoint's state", func() {
			ctx, cancel := serverWriteContext(sc)
			defer cancel()
			e, err := sc.Server.EndpointByNameContext(ctx, name)
			if err == nil {
				err = e.SetStateContext(ctx, state)
			}
			a.postAndWake(func() {
				if err != nil {
					a.setStatus(fmt.Sprintf("Failed to set %q to %s: %v", name, state, err))
					return
				}
				node.data.IsEnabled = state == gosmo.EndpointStarted
				if parent := node.parent; parent != nil {
					refreshExplorerNode(a, parent)
				}
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("Endpoint %q is now %s", name, endpointStateLabel(string(state))))
			})
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

// restoreFromSnapshot reverts a snapshot's source database to it —
// RESTORE DATABASE … FROM DATABASE_SNAPSHOT.
//
// The confirmation is typed, and the word asked for is the *source*
// database's name, not the snapshot's: what this destroys is every change
// made to the source since the snapshot was taken, and the source is the
// object the user has to have in mind to answer.
//
// Two of the server's own preconditions are left to the server: the source
// must have exactly one snapshot, and nobody may be connected to either
// database. Both can change between a check here and the statement, and the
// server names which one failed.
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
		a.safego("restoring a database from a snapshot", func() {
			ctx, cancel := serverWriteContext(sc)
			defer cancel()
			err := sc.Server.RestoreFromSnapshotContext(ctx, source, snapshot)
			a.postAndWake(func() {
				if err != nil {
					a.setStatus(fmt.Sprintf("Failed to restore %q from %q: %v", source, snapshot, displayError(err)))
					return
				}
				a.setStatus(fmt.Sprintf("Database %q reverted to snapshot %q", source, snapshot))
				a.explorer.RefreshDatabasesFolder(sc)
			})
		})
	})
}

// forgetPeerFailuresForRefresh drops sc's cached peer connect failures when the
// node being refreshed is part of the Always On subtree — the only tree the
// peer cache answers for, and the one place a user who has just fixed the
// network has to be able to say "try again" rather than wait out
// peerFailureTTL.
func forgetPeerFailuresForRefresh(sc *db.ServerConn, node *explorerNode) {
	if sc == nil || node == nil {
		return
	}
	if isAlwaysOnNode(node.data.Type) {
		sc.ForgetPeerFailures()
	}
}

// isAlwaysOnNode reports whether t is in the Always On subtree — the only tree
// a peer read serves, so the only Refresh the peer cache should answer to.
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
