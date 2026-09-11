package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// ag_dashboard_all.go is the Always On dashboard's all-groups view (SSMS "Show
// Dashboard" on the Always On root). AGDashboard hosts it; this file supplies
// reading, columns and rows.
//
// # One unreachable primary must not empty the page
//
// Each group resolves independently through resolveAGView, degrading to a
// partial local read (Object Explorer's rule) rather than failing (AG
// Properties' rule, where a secondary-loaded page would offer rejected edits).
// This page only reads, and failing because one of five primaries is
// unreachable would be useless when needed most. A locally read group says so
// in Issues.

// agGroupRollup is one group's row, with the replicas and databases it
// summarizes; the replica grid is built from these.
type agGroupRollup struct {
	group    *gosmo.AvailabilityGroup
	replicas []*gosmo.AvailabilityReplica
	dbs      []agDatabaseMetrics

	// unreachable names the primary that couldn't be read through, if following
	// it failed.
	unreachable string
	followed    bool
}

// readAllGroups reads every availability group on the instance.
func (d *AGDashboard) readAllGroups(ctx context.Context) (agSnapshot, error) {
	groups, err := d.conn.Server.AvailabilityGroupsContext(ctx)
	if err != nil {
		return agSnapshot{}, err
	}
	l := loaderCtx{ctx: ctx, sc: d.conn}

	rollups := make([]agGroupRollup, 0, len(groups))
	for _, g := range groups {
		view, err := resolveAGView(l, g.Name)
		if err != nil {
			// Listed a moment ago but unreadable now (dropped, or instance
			// gone); show the rest anyway.
			rollups = append(rollups, agGroupRollup{group: g, unreachable: g.PrimaryReplicaServerName})
			continue
		}
		r := agGroupRollup{group: view.ag, unreachable: view.unreachable, followed: view.followed}
		if replicas, err := view.ag.ReplicasContext(ctx); err == nil {
			r.replicas = replicas
		}
		if dbs, err := view.ag.DatabasesContext(ctx); err == nil {
			r.dbs = agComputeDatabaseMetrics(dbs)
		}
		rollups = append(rollups, r)
	}
	return agSnapshot{groups: rollups, allGroup: true, at: time.Now()}, nil
}

// -- columns and rows ------------------------------------------------------------

func (d *AGDashboard) topColumns() []string {
	if d.allGroups() {
		return agGroupColumns
	}
	return agReplicaColumns
}

func (d *AGDashboard) bottomColumns() []string {
	if d.allGroups() {
		return agAllReplicaColumns
	}
	return agDatabaseColumns
}

func (d *AGDashboard) topRowsFrom(snap agSnapshot) [][]string {
	if d.allGroups() {
		return agGroupRows(snap.groups)
	}
	return agReplicaRows(snap.replicas, snap.dbs)
}

func (d *AGDashboard) bottomRowsFrom(snap agSnapshot) [][]string {
	if d.allGroups() {
		return agAllReplicaRows(snap.groups)
	}
	return agDatabaseGridRows(snap.dbs)
}

var agGroupColumns = []string{
	"Availability group", "Primary replica", "Cluster type", "Health",
	"Replicas", "Databases", "Issues",
}

func agGroupRows(groups []agGroupRollup) [][]string {
	rows := make([][]string, len(groups))
	for i, g := range groups {
		rows[i] = []string{
			g.group.Name,
			orDefault(g.group.PrimaryReplicaServerName, "(none visible)"),
			orDefault(g.group.ClusterType, "WSFC (implied)"),
			orDefault(titleWord(g.group.SynchronizationHealth), "—"),
			strconv.Itoa(len(g.replicas)),
			strconv.Itoa(agDistinctDatabases(g.dbs)),
			g.issues(),
		}
	}
	return rows
}

// issues is the group's one-line verdict. An unreachable primary explains
// everything else, so it comes first. Empty means healthy.
func (g agGroupRollup) issues() string {
	if g.unreachable != "" {
		return "Partial — primary " + g.unreachable + " unreachable"
	}
	var out []string
	if h := g.group.SynchronizationHealth; h != "" && !strings.EqualFold(h, "HEALTHY") {
		out = append(out, titleWord(h))
	}
	disconnected := 0
	for _, r := range g.replicas {
		if r.ConnectedState != "" && !strings.EqualFold(r.ConnectedState, "CONNECTED") {
			disconnected++
		}
	}
	if disconnected > 0 {
		out = append(out, fmt.Sprintf("%d replica(s) disconnected", disconnected))
	}
	suspended := 0
	for _, m := range g.dbs {
		if m.DB.IsSuspended {
			suspended++
		}
	}
	if suspended > 0 {
		out = append(out, fmt.Sprintf("%d database copy/copies suspended", suspended))
	}
	return strings.Join(out, "; ")
}

// agAllReplicaColumns prefixes agReplicaColumns with the group, so a replica
// listed once per group is distinguishable.
var agAllReplicaColumns = append([]string{"Availability group"}, agReplicaColumns...)

func agAllReplicaRows(groups []agGroupRollup) [][]string {
	var rows [][]string
	for _, g := range groups {
		for _, r := range agReplicaRows(g.replicas, g.dbs) {
			rows = append(rows, append([]string{g.group.Name}, r...))
		}
	}
	return rows
}

// selectedGroup is the top grid's cursor group, for Enter to drill in; empty if
// none.
func (d *AGDashboard) selectedGroup() string {
	if !d.allGroups() {
		return ""
	}
	i := d.topGrid.SelectedRow()
	if i < 0 || i >= len(d.snap.groups) {
		return ""
	}
	return d.snap.groups[i].group.Name
}
