package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// alwayson_menu.go builds the Object Explorer context menus for the Always On
// branch and runs the operations behind them: adding and removing databases,
// suspending and resuming data movement, adding and removing the listener,
// removing a replica, deleting a group, and failing one over.
//
// # Where an operation runs is part of what it means
//
// Three connections are in play, and picking the wrong one either fails on the
// server or quietly does something else:
//
//   - Membership changes (ADD/REMOVE DATABASE, REMOVE REPLICA, ADD/REMOVE
//     LISTENER, DROP) must go to the *primary*. agOnPrimary resolves it, opening
//     a peer connection when the tree sits on a secondary.
//   - Suspend and resume act on the copy held by the instance they run on: from
//     a secondary that one replica, from the primary every secondary. These run
//     on the tree's *own* connection, and the confirmation says which.
//   - Failover runs on the replica being promoted, never on the current primary,
//     so it is offered on the replica leaf and executed through a connection to
//     that replica.
//
// Nothing here shells out to a cluster manager. Under an EXTERNAL cluster type
// SQL Server owns none of failover, so the answer is to say so and name the tool
// that does; see agFailoverRefusal.

// -- Context menus ---------------------------------------------------------

// alwaysOnRootMenuItems builds the context menu for the Always On High
// Availability node. SSMS hangs "Show Dashboard" here as well as on each group,
// and the two are different views: this one lists every group.
func alwaysOnRootMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		{Label: "Show Dashboard", Action: func() { a.showAGDashboardFor(sc, "") }},
		{Divider: true},
		gate(controls.MenuItem{Label: "New Database Mirroring Endpoint...",
			Action: func() { a.showNewEndpointDialog(sc, node) }}, sc, "", rightAlterAnyEndpoint),
		{Divider: true},
		refresh,
	}
}

// agGroupsFolderMenuItems builds the context menu for the Availability Groups
// folder.
func agGroupsFolderMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "New Availability Group...",
			Action: func() { a.showNewAGDialog(sc, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		refresh,
	}
}

// agGroupMenuItems builds the context menu for a NodeAvailabilityGroup.
func agGroupMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	ag := node.data.AGName
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		{Label: "Show Dashboard", Action: func() { a.showAGDashboardFor(sc, ag) }},
		{Divider: true},
		gate(controls.MenuItem{Label: "Add Database...",
			Action: func() { a.showAGAddDatabaseDialog(sc, ag, node) }}, sc, "", rightAlterAnyAG),
		gate(controls.MenuItem{Label: "Add Replica...",
			Action: func() { a.showAGAddReplicaDialog(sc, ag, node) }}, sc, "", rightAlterAnyAG),
		gate(controls.MenuItem{Label: "Add Listener...",
			Action: func() { a.showAGAddListenerDialog(sc, ag, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		refresh,
		gate(controls.MenuItem{Label: "Delete Availability Group...",
			Action: func() { a.deleteAvailabilityGroup(sc, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showAGPropertiesFor(sc, ag) }},
	}
}

// agReplicasFolderMenuItems builds the context menu for the Availability
// Replicas folder.
func agReplicasFolderMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "Add Replica...",
			Action: func() { a.showAGAddReplicaDialog(sc, node.data.AGName, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		refresh,
	}
}

// agDatabasesFolderMenuItems builds the context menu for the Availability
// Databases folder.
func agDatabasesFolderMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "Add Database...",
			Action: func() { a.showAGAddDatabaseDialog(sc, node.data.AGName, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		refresh,
	}
}

// agDatabaseMenuItems builds the context menu for one availability database.
//
// Suspend and Resume are shown one or the other rather than both greyed against
// each other: the node knows which state it is in, and offering "Resume" on a
// running database invites a click that can only error. Join and Unjoin follow
// the same rule against AGLocalSecondary/AGLocalJoined — they act on the local
// copy, so on the primary neither appears.
//
// A copy that has not joined has no data movement to suspend either, so the
// movement item goes with it.
func agDatabaseMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	items := []controls.MenuItem{newQuery, {Divider: true}}

	if node.data.AGLocalSecondary && !node.data.AGLocalJoined {
		items = append(items, gate(controls.MenuItem{
			Label:  "Join to Availability Group",
			Action: func() { a.joinAGDatabase(sc, node) },
		}, sc, "", rightAlterAnyAG))
	} else {
		movement := controls.MenuItem{
			Label:  "Suspend Data Movement...",
			Action: func() { a.suspendAGDatabase(sc, node) },
		}
		if node.data.AGSuspended {
			movement = controls.MenuItem{
				Label:  "Resume Data Movement",
				Action: func() { a.resumeAGDatabase(sc, node) },
			}
		}
		items = append(items, gate(movement, sc, "", rightAlterAnyAG))
	}

	items = append(items, controls.MenuItem{Divider: true}, refresh)
	if node.data.AGLocalSecondary && node.data.AGLocalJoined {
		items = append(items, gate(controls.MenuItem{
			Label:  "Remove Secondary Database from Group...",
			Action: func() { a.unjoinAGDatabase(sc, node) },
		}, sc, "", rightAlterAnyAG))
	}
	return append(items, gate(controls.MenuItem{
		Label:  "Remove Database from Group...",
		Action: func() { a.removeAGDatabase(sc, node) },
	}, sc, "", rightAlterAnyAG))
}

// agReplicaMenuItems builds the context menu for one availability replica.
//
// Both failover items are gated on the replica not already being the primary: a
// replica cannot fail over to itself, and REMOVE REPLICA against the primary is
// refused by the server (41190). Whether failover is possible *at all* depends
// on the group's cluster type, which the tree doesn't know, so that check
// happens when the item is chosen (see agFailoverRefusal).
func agReplicaMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	secondary := func() bool { return !node.data.AGIsPrimary }
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "Fail Over to This Replica...", Enabled: secondary,
			Action: func() { a.failoverToReplica(sc, node, false) }}, sc, "", rightAlterAnyAG),
		gate(controls.MenuItem{Label: "Force Failover to This Replica...", Enabled: secondary,
			Action: func() { a.failoverToReplica(sc, node, true) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		refresh,
		gate(controls.MenuItem{Label: "Remove Replica from Group...", Enabled: secondary,
			Action: func() { a.removeAGReplica(sc, node) }}, sc, "", rightAlterAnyAG),
	}
}

// agListenersFolderMenuItems builds the context menu for the Availability Group
// Listeners folder.
func agListenersFolderMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "Add Listener...",
			Action: func() { a.showAGAddListenerDialog(sc, node.data.AGName, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		refresh,
	}
}

// agListenerMenuItems builds the context menu for one listener.
func agListenerMenuItems(a *App, sc *db.ServerConn, node *explorerNode, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		refresh,
		gate(controls.MenuItem{Label: "Remove Listener...",
			Action: func() { a.removeAGListener(sc, node) }}, sc, "", rightAlterAnyAG),
		{Divider: true},
		{Label: "Properties...", Action: func() {
			a.showAGListenerPropertiesFor(sc, node.data.AGName, node.data.Name)
		}},
	}
}

// -- Running an operation --------------------------------------------------

// agOperation is one Always On operation: what to run, where to run it, and
// what to say and reload afterwards.
type agOperation struct {
	// what names the operation in the failure status, e.g. "add database".
	what string
	// done is the status line on success.
	done string
	// refresh is the tree node reloaded once the operation succeeds — usually the
	// folder above whatever changed; nil skips the reload.
	refresh *explorerNode

	// onPrimary runs against the group resolved through its primary replica,
	// following it from a secondary if need be. Every membership change is one.
	onPrimary func(context.Context, *gosmo.AvailabilityGroup) error

	// onLocal runs against the group as the tree's own connection sees it, with
	// no following — only for operations scoped to the instance they run on.
	onLocal func(context.Context, *gosmo.AvailabilityGroup) error
}

// runAGOperation resolves the group and runs op on a background goroutine.
func (a *App) runAGOperation(sc *db.ServerConn, agName string, op agOperation) {
	if !a.requireConn(sc) {
		return
	}
	a.safego("running an Always On operation", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()

		var err error
		var ag *gosmo.AvailabilityGroup
		run := op.onPrimary
		if run != nil {
			ag, err = agOnPrimary(ctx, sc, agName)
		} else {
			run = op.onLocal
			ag, err = sc.Server.AvailabilityGroupByNameContext(ctx, agName)
		}
		if err == nil {
			err = run(ctx, ag)
		}

		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to %s: %v", op.what, err))
				return
			}
			a.setStatus(op.done)
			refreshExplorerNode(a, op.refresh)
		})
	})
}

// -- Databases -------------------------------------------------------------

func (a *App) removeAGDatabase(sc *db.ServerConn, node *explorerNode) {
	dbName, agName := node.data.Name, node.data.AGName
	a.confirmDialog.ShowConfirm("Remove Database from Group",
		fmt.Sprintf("Remove database %q from availability group %q?\n\n"+
			"The primary's copy stays online and read-write. Every secondary is left holding a copy that is in no role — unreachable until it is dropped or restored WITH RECOVERY.",
			dbName, agName),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			a.runAGOperation(sc, agName, agOperation{
				what:    fmt.Sprintf("remove database %q from %q", dbName, agName),
				done:    fmt.Sprintf("Database %q removed from availability group %q", dbName, agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.RemoveDatabaseContext(ctx, dbName)
				},
			})
		})
}

// joinAGDatabase joins this instance's restored copy of a database to the group
// — the manual-seeding counterpart of Add Database, which only puts the database
// in the group on the primary.
//
// Runs against the tree's own connection, never the primary: ALTER DATABASE …
// SET HADR AVAILABILITY GROUP acts on the copy the instance it runs on holds.
// The prerequisite it can't check is the restore — a copy not in RESTORING is
// rejected by the server (35250 or 1408), which the failure status carries.
func (a *App) joinAGDatabase(sc *db.ServerConn, node *explorerNode) {
	dbName, agName := node.data.Name, node.data.AGName
	a.runAGOperation(sc, agName, agOperation{
		what:    fmt.Sprintf("join database %q to %q", dbName, agName),
		done:    fmt.Sprintf("Database %q on %s joined availability group %q", dbName, sc.Opts.Server, agName),
		refresh: node.parent,
		onLocal: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
			return ag.JoinDatabaseContext(ctx, dbName)
		},
	})
}

// unjoinAGDatabase takes this instance's copy of a database back out of the
// group, leaving it RESTORING.
//
// The confirmation spells out that this is the one-replica form: Remove Database
// from Group, one item below, removes it on every replica, and the two are easy
// to mistake.
func (a *App) unjoinAGDatabase(sc *db.ServerConn, node *explorerNode) {
	dbName, agName := node.data.Name, node.data.AGName
	a.confirmDialog.ShowConfirm("Remove Secondary Database from Group",
		fmt.Sprintf("Remove %s's copy of %q from availability group %q?\n\n"+
			"Only this replica's copy leaves the group; the database stays in it on the primary and on every other secondary. The copy here is left in the RESTORING state, and rejoining it needs a restore of the primary's log to catch up.",
			sc.Opts.Server, dbName, agName),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			a.runAGOperation(sc, agName, agOperation{
				what:    fmt.Sprintf("remove %s's copy of %q from %q", sc.Opts.Server, dbName, agName),
				done:    fmt.Sprintf("Database %q on %s removed from availability group %q", dbName, sc.Opts.Server, agName),
				refresh: node.parent,
				onLocal: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.UnjoinDatabaseContext(ctx, dbName)
				},
			})
		})
}

// suspendAGDatabase suspends data movement for one database.
//
// The confirmation spells out the scope rather than the action, because the
// scope is what changes: the same item suspends one secondary or all of them,
// depending which instance the tree is connected to.
func (a *App) suspendAGDatabase(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	dbName, agName := node.data.Name, node.data.AGName

	// The role has to be read before the question is asked, since it is what
	// changes the answer.
	a.safego("reading an availability group's local role", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		ag, err := sc.Server.AvailabilityGroupByNameContext(ctx, agName)
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to read availability group %q: %v", agName, err))
				return
			}
			a.confirmDialog.ShowConfirm("Suspend Data Movement",
				fmt.Sprintf("Suspend data movement for %q?\n\n%s\n\n"+
					"The primary keeps accepting writes while movement is suspended, and its log cannot be truncated — a long suspension fills the log drive.",
					dbName, agSuspendScope(sc.Opts.Server, ag.IsLocalPrimary())),
				func(confirmed bool) {
					if !confirmed {
						return
					}
					a.runAGOperation(sc, agName, agOperation{
						what:    fmt.Sprintf("suspend data movement for %q", dbName),
						done:    fmt.Sprintf("Data movement suspended for %q on %s", dbName, sc.Opts.Server),
						refresh: node.parent,
						onLocal: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
							return ag.SuspendDatabaseContext(ctx, dbName)
						},
					})
				})
		})
	})
}

// agSuspendScope is the sentence telling the user what suspending from this
// instance actually reaches.
func agSuspendScope(server string, isPrimary bool) string {
	if isPrimary {
		return fmt.Sprintf("%s is the primary, so this suspends the database on EVERY secondary.", server)
	}
	return fmt.Sprintf("%s is a secondary, so this suspends only its own copy.", server)
}

func (a *App) resumeAGDatabase(sc *db.ServerConn, node *explorerNode) {
	dbName, agName := node.data.Name, node.data.AGName
	a.runAGOperation(sc, agName, agOperation{
		what:    fmt.Sprintf("resume data movement for %q", dbName),
		done:    fmt.Sprintf("Data movement resumed for %q on %s", dbName, sc.Opts.Server),
		refresh: node.parent,
		onLocal: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
			return ag.ResumeDatabaseContext(ctx, dbName)
		},
	})
}

// -- Replicas --------------------------------------------------------------

func (a *App) removeAGReplica(sc *db.ServerConn, node *explorerNode) {
	replica, agName := node.data.Name, node.data.AGName
	a.confirmTypedDialog.ShowTypedConfirm("Remove Replica from Group",
		fmt.Sprintf("Remove replica %q from availability group %q?\n\n"+
			"%s keeps its copies of the databases and a stale entry for the group, which only deleting the group there clears.",
			replica, agName, replica),
		replica,
		func(confirmed bool) {
			if !confirmed {
				return
			}
			a.runAGOperation(sc, agName, agOperation{
				what:    fmt.Sprintf("remove replica %q from %q", replica, agName),
				done:    fmt.Sprintf("Replica %q removed from availability group %q", replica, agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.RemoveReplicaContext(ctx, replica)
				},
			})
		})
}

// failoverToReplica promotes the replica the menu was opened on, connecting to
// it to do so — FAILOVER is issued by the replica being promoted, not by the
// current primary.
//
// Whether the statement is allowed at all is the group's cluster type's call,
// checked here rather than at menu-build time: the tree doesn't carry the
// cluster type, and a menu item that silently disappears explains nothing.
func (a *App) failoverToReplica(sc *db.ServerConn, node *explorerNode, force bool) {
	if !a.requireConn(sc) {
		return
	}
	replica, agName := node.data.Name, node.data.AGName

	a.safego("checking whether an availability group can be failed over", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		ag, err := sc.Server.AvailabilityGroupByNameContext(ctx, agName)
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to read availability group %q: %v", agName, err))
				return
			}
			if reason := agFailoverRefusal(ag.ClusterType, force); reason != "" {
				a.alertDialog.ShowAlert("Failover", reason)
				return
			}
			a.confirmFailover(sc, node.parent, agName, replica, force)
		})
	})
}

// agFailoverRefusal explains why the group's cluster type forbids this failover,
// or returns "" when it allows it.
//
// Both refusals are the server's own: EXTERNAL rejects both forms with error
// 47104, NONE rejects the lossless form with 47122 and allows only the forced
// one. Catching them here turns an error message into an explanation, and the
// server still gates the statement if this gets it wrong.
func agFailoverRefusal(clusterType string, force bool) string {
	switch strings.ToUpper(clusterType) {
	case "EXTERNAL":
		return "This availability group has cluster type EXTERNAL, so failover belongs to the external cluster manager, not to SQL Server.\n\n" +
			"On Linux that is Pacemaker: use `crm resource move` (or `pcs resource move`) against the group's resource, and clear the constraint afterwards. " +
			"SQL Server rejects ALTER AVAILABILITY GROUP ... FAILOVER here with error 47104."
	case "NONE":
		if !force {
			return "This availability group has cluster type NONE — a read-scale group with no cluster manager to arbitrate, so SQL Server supports only forced failover on it (error 47122).\n\n" +
				"Use Force Failover to This Replica, which can lose transactions the target had not hardened."
		}
	}
	return ""
}

// confirmFailover asks for confirmation and runs the failover through a
// connection to the replica being promoted.
func (a *App) confirmFailover(sc *db.ServerConn, refresh *explorerNode, agName, replica string, force bool) {
	run := func() {
		a.safego("failing over an availability group", func() {
			ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
			defer cancel()
			err := agFailover(ctx, sc, agName, replica, force)
			a.postAndWake(func() {
				if err != nil {
					a.setStatus(fmt.Sprintf("Failed to fail over %q to %s: %v", agName, replica, err))
					return
				}
				a.setStatus(fmt.Sprintf("Availability group %q failed over to %s", agName, replica))
				refreshExplorerNode(a, refresh)
			})
		})
	}

	if force {
		a.confirmTypedDialog.ShowTypedConfirm("Force Failover",
			fmt.Sprintf("Force availability group %q over to %s, allowing data loss?\n\n"+
				"Every transaction %s had not hardened is lost, and the remaining secondaries have to be resumed — and may need reseeding — afterwards.",
				agName, replica, replica),
			replica,
			func(confirmed bool) {
				if confirmed {
					run()
				}
			})
		return
	}
	a.confirmDialog.ShowConfirm("Fail Over",
		fmt.Sprintf("Fail availability group %q over to %s?\n\n"+
			"%s becomes the primary. The target has to be a synchronous-commit replica in the SYNCHRONIZED state; SQL Server refuses rather than failing over with loss.",
			agName, replica, replica),
		func(confirmed bool) {
			if confirmed {
				run()
			}
		})
}

// agFailover issues the failover from replica itself, opening a peer connection
// unless the tree is already there.
func agFailover(ctx context.Context, sc *db.ServerConn, agName, replica string, force bool) error {
	target := sc
	if !strings.EqualFold(sc.Server.Name(), replica) {
		peer, err := sc.Peer(ctx, replica)
		if err != nil {
			return fmt.Errorf("connect to replica %s: %w", replica, err)
		}
		target = peer
	}
	ag, err := target.Server.AvailabilityGroupByNameContext(ctx, agName)
	if err != nil {
		return err
	}
	if force {
		return ag.ForceFailoverAllowDataLossContext(ctx)
	}
	return ag.FailoverContext(ctx)
}

// -- Listeners -------------------------------------------------------------

func (a *App) removeAGListener(sc *db.ServerConn, node *explorerNode) {
	dnsName, agName := node.data.Name, node.data.AGName
	a.confirmDialog.ShowConfirm("Remove Listener",
		fmt.Sprintf("Remove listener %q from availability group %q?\n\n"+
			"Clients configured to reach the group through this name will no longer resolve it. Existing connections are not dropped.",
			dnsName, agName),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			a.runAGOperation(sc, agName, agOperation{
				what:    fmt.Sprintf("remove listener %q from %q", dnsName, agName),
				done:    fmt.Sprintf("Listener %q removed from availability group %q", dnsName, agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.RemoveListenerContext(ctx, dnsName)
				},
			})
		})
}

// -- The group itself ------------------------------------------------------

func (a *App) deleteAvailabilityGroup(sc *db.ServerConn, node *explorerNode) {
	agName := node.data.AGName
	a.confirmTypedDialog.ShowTypedConfirm("Delete Availability Group",
		fmt.Sprintf("Delete availability group %q?\n\n"+
			"The databases survive: the primary's copies stay online and read-write, and each secondary is left holding a copy that is in no role. "+
			"Under an externally managed cluster the cluster resource is not removed by this and has to be cleaned up separately.",
			agName),
		agName,
		func(confirmed bool) {
			if !confirmed {
				return
			}
			a.runAGOperation(sc, agName, agOperation{
				what:    fmt.Sprintf("delete availability group %q", agName),
				done:    fmt.Sprintf("Availability group %q deleted", agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.DropContext(ctx)
				},
			})
		})
}
