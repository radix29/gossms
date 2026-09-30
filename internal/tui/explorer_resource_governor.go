package tui

import (
	"context"

	"github.com/radix29/gossms/internal/db"
)

// explorer_resource_governor.go is Management ▸ Resource Governor: the node
// itself, whose label carries the governor's state, its Resource Pools and
// External Resource Pools folders, each pool's Workload Groups folder, and
// the leaves under them. The Details pane's grids are in
// detail_browser_resource_governor.go.
//
// Workload groups nest under the pool they use, as SSMS nests them, rather
// than in one flat folder: a group's pool is the first thing anyone asks of
// it. Group names are unique server-wide, so a group leaf needs no pool to be
// addressed; the folder carries its pool in RGPool.

// resourceGovernorRootLabel is the Resource Governor node's base label, before
// resourceGovernorState's suffix.
const resourceGovernorRootLabel = "Resource Governor"

// resourceGovernorNode builds Management's Resource Governor node, its label
// carrying the governor's state the way the Agent node carries "(Stopped)".
func resourceGovernorNode(l loaderCtx) *explorerNode {
	label, enabled := resourceGovernorState(l.ctx, l.sc)
	n := l.node(label, NodeResourceGovernor, "", "", "")
	n.data.IsEnabled = enabled
	return n
}

// resourceGovernorState is the Resource Governor node's label and whether the
// governor is enabled.
//
// "(Reconfiguration pending)" shows only while enabled. The pending flag is
// set by any metadata change even with the governor disabled, and there it
// means only "changed since the last DISABLE" — nothing is waiting to take
// effect, since RECONFIGURE would enable the governor as well as apply it
// (W1). So a disabled governor says "(Disabled)" and nothing else.
//
// Any failed read leaves the label bare: the stored configuration is
// invisible without VIEW ANY DEFINITION and the pending flag without VIEW
// SERVER STATE, and neither is a reason to fail the Management folder. An
// unreadable configuration reports enabled, so nothing is withheld on a guess.
func resourceGovernorState(ctx context.Context, sc *db.ServerConn) (string, bool) {
	if sc == nil || sc.Server == nil || !resourceGovernorSupported(sc.Server.Info()) {
		return resourceGovernorRootLabel, true
	}
	rg, err := sc.Server.ResourceGovernor(ctx)
	if err != nil {
		return resourceGovernorRootLabel, true
	}
	if !rg.IsEnabled {
		return resourceGovernorRootLabel + " (Disabled)", false
	}
	if st, err := sc.Server.ResourceGovernorStatus(ctx); err == nil && st.IsReconfigurationPending {
		return resourceGovernorRootLabel + " (Reconfiguration pending)", true
	}
	return resourceGovernorRootLabel, true
}

// resourceGovernorUnsupportedLabel is the one row an edition without Resource
// Governor expands to.
const resourceGovernorUnsupportedLabel = "Resource Governor is not supported on this edition"

// loadResourceGovernorChildren returns the Resource Governor node's two
// folders. External Resource Pools is listed on every supported instance, as
// SSMS lists it (plan-phase5 D2): gosmo's floor, 2016, is where they arrived,
// and whether Machine Learning Services is installed is not the folder's
// question.
func loadResourceGovernorChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	if l.sc != nil && l.sc.Server != nil && !resourceGovernorSupported(l.sc.Server.Info()) {
		return []*explorerNode{l.node(resourceGovernorUnsupportedLabel, NodeError, "", "", "")}, nil
	}
	return []*explorerNode{
		l.node("Resource Pools", NodeResourcePools, "", "", ""),
		l.node("External Resource Pools", NodeExternalResourcePools, "", "", ""),
	}, nil
}

// resourceGovernorNotVisible is the row a pool folder shows when the catalog
// came back empty. internal and default always exist (and the default
// external pool), so an empty list is never "no pools": it is a login
// without VIEW ANY DEFINITION, which SQL Server answers with zero rows and no
// error (W1). An empty folder would say there is nothing to see.
func resourceGovernorNotVisible(l loaderCtx) []*explorerNode {
	return []*explorerNode{l.node("Not visible — VIEW ANY DEFINITION is needed", NodeError, "", "", "")}
}

// systemSuffix marks a built-in pool or group in its label: internal and
// default are listed beside the user's objects, cannot be dropped, and
// internal cannot be altered either — nothing else in the row says so.
func systemSuffix(name string, system bool) string {
	if system {
		return name + " (system)"
	}
	return name
}

// loadResourcePoolsChildren lists every resource pool, the built-in internal
// and default included.
func loadResourcePoolsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	pools, err := l.sc.Server.ResourcePools(l.ctx)
	if err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return resourceGovernorNotVisible(l), nil
	}
	out := make([]*explorerNode, 0, len(pools))
	for _, p := range pools {
		n := l.node(systemSuffix(p.Name, p.IsSystem()), NodeResourcePool, "", p.Name, "")
		n.data.IsSystem = p.IsSystem()
		out = append(out, n)
	}
	return out, nil
}

// loadResourcePoolChildren is one pool's single folder, Workload Groups.
func loadResourcePoolChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	n := l.node("Workload Groups", NodeWorkloadGroups, "", "", "")
	n.data.RGPool = node.data.Name
	return []*explorerNode{n}, nil
}

// loadWorkloadGroupsChildren lists the groups that use the folder's pool.
//
// One server-wide read filtered here rather than ResourcePoolByName plus
// ResourcePool.WorkloadGroups: the pool's id is all the second read wants
// from the first, and the pool's name, which the folder has, identifies it as
// well. The whole list also tells "not visible" (empty — default and internal
// always exist) from a user pool no group uses yet (nothing for this pool).
func loadWorkloadGroupsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	groups, err := l.sc.Server.WorkloadGroups(l.ctx)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return resourceGovernorNotVisible(l), nil
	}
	pool := node.data.RGPool
	var out []*explorerNode
	for _, g := range groups {
		if g.PoolName != pool {
			continue
		}
		n := l.node(systemSuffix(g.Name, g.IsSystem()), NodeWorkloadGroup, "", g.Name, "")
		n.data.IsSystem = g.IsSystem()
		n.data.RGPool = pool
		out = append(out, n)
	}
	return out, nil
}

// loadExternalResourcePoolsChildren lists every external resource pool. There
// is one built-in, default; unlike resource pools there is no internal one
// (W1).
func loadExternalResourcePoolsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	pools, err := l.sc.Server.ExternalResourcePools(l.ctx)
	if err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return resourceGovernorNotVisible(l), nil
	}
	out := make([]*explorerNode, 0, len(pools))
	for _, p := range pools {
		n := l.node(systemSuffix(p.Name, p.IsSystem()), NodeExternalResourcePool, "", p.Name, "")
		n.data.IsSystem = p.IsSystem()
		out = append(out, n)
	}
	return out, nil
}
