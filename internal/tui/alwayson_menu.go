package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// alwayson_menu.go builds the Always On branch's context menus and runs their
// operations: add/remove databases, suspend/resume data movement, add/remove
// listener, remove replica, delete group, fail over.
//
// # Where an operation runs is part of what it means
//
//   - Membership changes (ADD/REMOVE DATABASE, REMOVE REPLICA, ADD/REMOVE
//     LISTENER, DROP) go to the primary, via agOnPrimary (opening a peer from a
//     secondary).
//   - Suspend and resume act on the copy of the instance they run on: from a
//     secondary that replica, from the primary every secondary. They use the
//     tree's own connection, and the confirmation says which.
//   - Failover runs on the replica being promoted, so it's on the replica leaf,
//     through a connection to it.
//
// No cluster manager is invoked. Under EXTERNAL, SQL Server owns no failover,
// so the app says so and names the tool; see agFailoverRefusal.

// -- Context menus ---------------------------------------------------------

// alwaysOnRootMenuItems builds the Always On root's menu. Its "Show Dashboard"
// lists every group, unlike a group's.
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
		gateOn(controls.MenuItem{Label: "Add Database...",
			Action: func() { a.showAGAddDatabaseDialog(sc, ag, node) }}, sc, "", "", ag, rightAlterAnyAG),
		gateOn(controls.MenuItem{Label: "Add Replica...",
			Action: func() { a.showAGAddReplicaDialog(sc, ag, node) }}, sc, "", "", ag, rightAlterAnyAG),
		gateOn(controls.MenuItem{Label: "Add Listener...",
			Action: func() { a.showAGAddListenerDialog(sc, ag, node) }}, sc, "", "", ag, rightAlterAnyAG),
		{Divider: true},
		refresh,
		gateOn(controls.MenuItem{Label: "Delete Availability Group...",
			Action: func() { a.deleteAvailabilityGroup(sc, node) }}, sc, "", "", ag, rightAlterAnyAG),
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
		gateOn(controls.MenuItem{Label: "Add Replica...",
			Action: func() { a.showAGAddReplicaDialog(sc, node.data.AGName, node) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
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
		gateOn(controls.MenuItem{Label: "Add Database...",
			Action: func() { a.showAGAddDatabaseDialog(sc, node.data.AGName, node) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
		{Divider: true},
		refresh,
	}
}

// agDatabaseMenuItems builds one availability database's menu.
//
// Only one of Suspend/Resume is shown, since the node knows its state and the
// other can only fail. Join/Unjoin follow AGLocalSecondary/AGLocalJoined; they
// act on the local copy, so neither appears on the primary. An unjoined copy
// has no movement to suspend either.
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
	// The only item here writing ALTER AVAILABILITY GROUP, so the only one the
	// group's class-108 DENY withholds; the others are ALTER DATABASE ... SET
	// HADR, which it doesn't affect (measured for Resume and Join).
	return append(items, gateOn(controls.MenuItem{
		Label:  "Remove Database from Group...",
		Action: func() { a.removeAGDatabase(sc, node) },
	}, sc, "", "", node.data.AGName, rightAlterAnyAG))
}

// agReplicaMenuItems builds one replica's menu.
//
// All three writes are gated on the replica not being primary: it can't fail
// over to itself, and REMOVE REPLICA on the primary fails (41190). Whether
// failover is possible depends on the cluster type, which the tree doesn't
// know, so that's checked on selection (see agFailoverRefusal).
func agReplicaMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	secondary := func() bool { return !node.data.AGIsPrimary }
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gateOn(controls.MenuItem{Label: "Fail Over to This Replica...", Enabled: secondary,
			Action: func() { a.failoverToReplica(sc, node, false) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
		gateOn(controls.MenuItem{Label: "Force Failover to This Replica...", Enabled: secondary,
			Action: func() { a.failoverToReplica(sc, node, true) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
		{Divider: true},
		refresh,
		gateOn(controls.MenuItem{Label: "Remove Replica from Group...", Enabled: secondary,
			Action: func() { a.removeAGReplica(sc, node) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
	}
}

// agListenersFolderMenuItems builds the context menu for the Availability Group
// Listeners folder.
func agListenersFolderMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gateOn(controls.MenuItem{Label: "Add Listener...",
			Action: func() { a.showAGAddListenerDialog(sc, node.data.AGName, node) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
		{Divider: true},
		refresh,
	}
}

// agListenerMenuItems builds the context menu for one listener.
func agListenerMenuItems(a *App, sc *db.ServerConn, node *explorerNode, _, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		refresh,
		gateOn(controls.MenuItem{Label: "Remove Listener...",
			Action: func() { a.removeAGListener(sc, node) }}, sc, "", "", node.data.AGName, rightAlterAnyAG),
		{Divider: true},
		{Label: "Properties...", Action: func() {
			a.showAGListenerPropertiesFor(sc, node.data.AGName, node.data.Name)
		}},
	}
}

// -- Running an operation --------------------------------------------------

// agOperation is one Always On operation: what to run, where, and what to
// report and reload.
type agOperation struct {
	// title is the progress dialog's title, normally the confirmation's.
	title string
	// what names the operation in the failure status, e.g. "add database".
	what string
	// done is the status line on success.
	done string
	// refresh is the node reloaded on success, usually the folder above; nil
	// skips it.
	refresh *explorerNode

	// onPrimary runs against the group resolved through its primary. Every
	// membership change is one.
	onPrimary func(context.Context, *gosmo.AvailabilityGroup) error

	// onLocal runs against the group as the tree's connection sees it, for
	// instance-scoped operations.
	onLocal func(context.Context, *gosmo.AvailabilityGroup) error
}

// runAGOperation resolves the group and runs op behind the progress dialog.
// Cancel works: each operation is one ALTER AVAILABILITY GROUP or ALTER
// DATABASE ... SET HADR, rolled back whole. Failover goes through
// confirmFailover instead.
func (a *App) runAGOperation(sc *db.ServerConn, agName string, op agOperation) {
	if !a.requireConn(sc) {
		return
	}
	a.runWithProgress(progressJob{
		title:   op.title,
		message: strings.ToUpper(op.what[:1]) + op.what[1:] + "...",
		what:    "running an Always On operation",
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		run := op.onPrimary
		var ag *gosmo.AvailabilityGroup
		var err error
		if run != nil {
			ag, err = agOnPrimary(ctx, sc, agName)
		} else {
			run = op.onLocal
			ag, err = sc.Server.AvailabilityGroupByNameContext(ctx, agName)
		}
		if err != nil {
			return err
		}
		return run(ctx, ag)
	}, func(err error, cancelled bool) {
		switch {
		case cancelled:
			// Reloaded anyway: the cancel may have arrived after the commit.
			a.setStatus(fmt.Sprintf("Cancelled: %s", op.what))
			a.explorer.Reload(op.refresh)
		case err != nil:
			a.setStatus(fmt.Sprintf("Failed to %s: %v", op.what, err))
		default:
			a.setStatus(op.done)
			a.explorer.Reload(op.refresh)
		}
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
				title:   "Remove Database from Group",
				what:    fmt.Sprintf("remove database %q from %q", dbName, agName),
				done:    fmt.Sprintf("Database %q removed from availability group %q", dbName, agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.RemoveDatabaseContext(ctx, dbName)
				},
			})
		})
}

// joinAGDatabase joins this instance's restored copy to the group — manual
// seeding's counterpart to Add Database, which only adds it on the primary.
//
// Runs on the tree's own connection: SET HADR AVAILABILITY GROUP acts on this
// instance's copy. A copy not in RESTORING is refused (35250 or 1408), reported
// in the status.
func (a *App) joinAGDatabase(sc *db.ServerConn, node *explorerNode) {
	dbName, agName := node.data.Name, node.data.AGName
	a.runAGOperation(sc, agName, agOperation{
		title:   "Join to Availability Group",
		what:    fmt.Sprintf("join database %q to %q", dbName, agName),
		done:    fmt.Sprintf("Database %q on %s joined availability group %q", dbName, sc.Opts.Server, agName),
		refresh: node.parent,
		onLocal: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
			return ag.JoinDatabaseContext(ctx, dbName)
		},
	})
}

// unjoinAGDatabase removes this instance's copy from the group, leaving it
// RESTORING. The confirmation distinguishes it from Remove Database from Group
// (every replica), one item below.
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
				title:   "Remove Secondary Database from Group",
				what:    fmt.Sprintf("remove %s's copy of %q from %q", sc.Opts.Server, dbName, agName),
				done:    fmt.Sprintf("Database %q on %s removed from availability group %q", dbName, sc.Opts.Server, agName),
				refresh: node.parent,
				onLocal: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.UnjoinDatabaseContext(ctx, dbName)
				},
			})
		})
}

// suspendAGDatabase suspends data movement for one database. The confirmation
// states the scope, which depends on the connected instance: one secondary or
// all.
func (a *App) suspendAGDatabase(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	dbName, agName := node.data.Name, node.data.AGName

	// The role must be read first; it changes the question.
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
						title:   "Suspend Data Movement",
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

// agSuspendScope says what suspending from this instance reaches.
func agSuspendScope(server string, isPrimary bool) string {
	if isPrimary {
		return fmt.Sprintf("%s is the primary, so this suspends the database on EVERY secondary.", server)
	}
	return fmt.Sprintf("%s is a secondary, so this suspends only its own copy.", server)
}

func (a *App) resumeAGDatabase(sc *db.ServerConn, node *explorerNode) {
	dbName, agName := node.data.Name, node.data.AGName
	a.runAGOperation(sc, agName, agOperation{
		title:   "Resume Data Movement",
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
				title:   "Remove Replica from Group",
				what:    fmt.Sprintf("remove replica %q from %q", replica, agName),
				done:    fmt.Sprintf("Replica %q removed from availability group %q", replica, agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.RemoveReplicaContext(ctx, replica)
				},
			})
		})
}

// failoverToReplica promotes the menu's replica via a connection to it;
// FAILOVER is issued by the replica being promoted.
//
// The cluster type check happens here, not at menu build: the tree doesn't know
// it, and a vanishing item explains nothing.
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

// agFailoverRefusal explains why the cluster type forbids this failover, or "".
// Mirrors the server: EXTERNAL rejects both forms (47104), NONE rejects the
// lossless form (47122). The server still gates it if this is wrong.
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

// confirmFailover confirms and runs the failover via the promoted replica.
func (a *App) confirmFailover(sc *db.ServerConn, refresh *explorerNode, agName, replica string, force bool) {
	// Uninterruptible: an abandoned failover can leave the group RESOLVING with
	// no primary.
	title := "Fail Over"
	if force {
		title = "Force Failover"
	}
	run := func() {
		a.runWithProgress(progressJob{
			title:           title,
			message:         fmt.Sprintf("Failing availability group %q over to %s...", agName, replica),
			what:            "failing over an availability group",
			sc:              sc,
			uninterruptible: uninterruptibleFailover,
		}, func(ctx context.Context, _ progressReport) error {
			return agFailover(ctx, sc, agName, replica, force)
		}, func(err error, _ bool) {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to fail over %q to %s: %v", agName, replica, err))
				return
			}
			a.setStatus(fmt.Sprintf("Availability group %q failed over to %s", agName, replica))
			a.explorer.Reload(refresh)
		})
	}

	if force {
		a.confirmTypedDialog.ShowTypedConfirm(title,
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
	a.confirmDialog.ShowConfirm(title,
		fmt.Sprintf("Fail availability group %q over to %s?\n\n"+
			"%s becomes the primary. The target has to be a synchronous-commit replica in the SYNCHRONIZED state; SQL Server refuses rather than failing over with loss.",
			agName, replica, replica),
		func(confirmed bool) {
			if confirmed {
				run()
			}
		})
}

// agFailover issues the failover from replica, opening a peer unless the tree
// is there.
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
				title:   "Remove Listener",
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
				title:   "Delete Availability Group",
				what:    fmt.Sprintf("delete availability group %q", agName),
				done:    fmt.Sprintf("Availability group %q deleted", agName),
				refresh: node.parent,
				onPrimary: func(ctx context.Context, ag *gosmo.AvailabilityGroup) error {
					return ag.DropContext(ctx)
				},
			})
		})
}
