package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// ag_dashboard_all.go is the Always On dashboard's all-groups view — SSMS's
// "Show Dashboard" on the Always On High Availability root, which lists every
// group on the instance rather than drilling into one. AGDashboard hosts it;
// this file supplies the reading, the columns and the rows.
//
// # One unreachable primary must not empty the page
//
// Each group is resolved independently through resolveAGView, the Object
// Explorer's degrade-to-partial rule rather than AG Properties' treat-it-as-an-
// error rule. The two differ on purpose: a Properties page that loaded from a
// secondary would offer edits the server rejects, while this page only reads,
// and a root dashboard that fails outright because one group of five has an
// unreachable primary would be useless exactly when it is needed. A group read
// locally says so in its Issues column.

// agGroupRollup is one group's row of the all-groups view, with the replicas
// and databases it was summarized from — the replica grid below is built from
// these rather than re-read.
type agGroupRollup struct {
	group    *gosmo.AvailabilityGroup
	replicas []*gosmo.AvailabilityReplica
	dbs      []agDatabaseMetrics

	// unreachable names the primary this group could not be read through, if
	// following it was attempted and failed.
	unreachable string
	followed    bool
}

// readAllGroups takes one reading of every availability group on the instance.
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
			// The group was listed a moment ago and cannot be read now — it was
			// dropped between the two round trips, or the instance went away.
			// Either way the remaining groups are still worth showing.
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

// issues is the group's one-line verdict, in the order a reader wants it: a
// reading that could not reach the primary explains every other column, so it
// comes first. Empty means the group is healthy.
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

// agAllReplicaColumns is agReplicaColumns with the group each replica belongs
// to in front — without it the lower grid is a list of instance names appearing
// once per group they are in, with no way to tell which row is which.
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

// selectedGroup is the group the top grid's cursor is on in the all-groups
// view, for drilling in with Enter. Empty when there is no such row.
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
