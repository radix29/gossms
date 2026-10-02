package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
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
	st := resourceGovernorState(l.ctx, l.sc)
	n := l.node(st.label, NodeResourceGovernor, "", "", "")
	st.applyTo(n)
	return n
}

// rgState is what the Resource Governor node shows of the governor: its
// label, and the two flags its menu reads.
type rgState struct {
	label            string
	enabled, pending bool
}

func (st rgState) applyTo(n *explorerNode) {
	n.label, n.data.IsEnabled, n.data.RGPending = st.label, st.enabled, st.pending
}

// resourceGovernorState is the Resource Governor node's label, whether the
// governor is enabled, and whether changes wait for RECONFIGURE.
//
// "(Reconfiguration pending)" shows only while enabled. The pending flag is
// set by any metadata change even with the governor disabled, and there it
// means only "changed since the last DISABLE" — nothing is waiting to take
// effect, since RECONFIGURE would enable the governor as well as apply it
// (W1). So a disabled governor says "(Disabled)" and nothing else.
//
// A failed read is not a reason to fail the Management folder: the stored
// configuration is invisible without VIEW ANY DEFINITION and the pending flag
// without VIEW SERVER STATE. An unreadable configuration says "(unknown)" —
// a bare label would read as enabled — and reports enabled, so nothing is
// withheld on a guess. An unreadable pending flag leaves the label bare: the
// governor is known to be enabled, and pending is only a qualifier.
func resourceGovernorState(ctx context.Context, sc *db.ServerConn) rgState {
	bare := rgState{label: resourceGovernorRootLabel, enabled: true}
	if sc == nil || sc.Server == nil || !resourceGovernorSupported(sc.Server.Info()) {
		return bare
	}
	rg, err := sc.Server.ResourceGovernor(ctx)
	if err != nil {
		return rgState{label: resourceGovernorRootLabel + " (unknown)", enabled: true}
	}
	if !rg.IsEnabled {
		return rgState{label: resourceGovernorRootLabel + " (Disabled)"}
	}
	if st, err := sc.Server.ResourceGovernorStatus(ctx); err == nil && st.IsReconfigurationPending {
		return rgState{label: resourceGovernorRootLabel + " (Reconfiguration pending)", enabled: true, pending: true}
	}
	return bare
}

// resourceGovernorUnsupportedLabel is the one row an edition without Resource
// Governor expands to.
const resourceGovernorUnsupportedLabel = "Resource Governor is not supported on this edition"

// loadResourceGovernorChildren returns the Resource Governor node's two
// folders. External Resource Pools is listed on every supported instance, as
// SSMS lists it (docs/decisions.md): gosmo's floor, 2016, is where they arrived,
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

// The context menus for this family's nodes, looked up through nodeMenus
// (explorer_loaders.go). Properties and every New item open Resource Governor
// Properties, on the node's own page and row — SSMS's New Resource Pool and
// New Workload Group open that dialog too: one editor per concept. Delete and
// Script as come from objectOps and scriptables.
//
// Every write is Resource Governor DDL, which needs CONTROL SERVER and
// nothing less (W1).

// rgItem is one of this family's write items: gated on CONTROL SERVER, and
// withheld on an edition without Resource Governor.
func rgItem(sc *db.ServerConn, label string, action func()) controls.MenuItem {
	return gateResourceGovernorEdition(gate.Item(controls.MenuItem{Label: label, Action: action}, sc, "", gate.ControlServer), sc)
}

// resourceGovernorMenuItems is the Resource Governor node's menu: the
// governor's own commands, then Properties. Properties itself is not gated —
// the dialog shows read-only to a login without the right — but is withheld
// on an unsupported edition, where every read behind its pages would fail.
//
// Reconfigure is offered only while the label says "(Reconfiguration
// pending)": on a disabled governor RECONFIGURE is Enable, which has its own
// item, and with nothing pending it would do nothing.
func resourceGovernorMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	toggle := rgItem(sc, "Enable", func() { a.enableResourceGovernor(sc, node) })
	if node.data.IsEnabled {
		toggle = rgItem(sc, "Disable", func() { a.disableResourceGovernor(sc, node) })
	}
	reconfigure := controls.MenuItem{Label: "Reconfigure", Action: func() { a.reconfigureResourceGovernor(sc, node) },
		Enabled: func() bool { return node.data.IsEnabled && node.data.RGPending }}
	props := gateResourceGovernorEdition(controls.MenuItem{Label: "Properties...", Action: func() {
		a.showResourceGovernorPropertiesFor(sc, rgFocus{})
	}}, sc)
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		toggle,
		gateResourceGovernorEdition(gate.Item(reconfigure, sc, "", gate.ControlServer), sc),
		rgItem(sc, "Reset Statistics", func() { a.resetResourceGovernorStatistics(sc, node) }),
		{Divider: true},
		refresh,
		props,
	}
}

func resourcePoolsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		rgItem(sc, "New Resource Pool...", func() { a.showResourceGovernorPropertiesFor(sc, rgFocus{page: rgPagePools}) }),
		{Divider: true},
		refresh,
	}
}

// newWorkloadGroupItem opens Workload Groups on pool. Withheld on internal:
// the server refuses a user group there, and the page lets no group move in.
func newWorkloadGroupItem(a *App, sc *db.ServerConn, pool string) controls.MenuItem {
	item := rgItem(sc, "New Workload Group...", func() {
		a.showResourceGovernorPropertiesFor(sc, rgFocus{page: rgPageGroups, pool: pool})
	})
	if pool == "internal" {
		item.Enabled = func() bool { return false }
		item.Note = "internal pool"
		item.NoteWhen = func() bool { return true }
	}
	return item
}

func resourcePoolMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		newWorkloadGroupItem(a, sc, node.data.Name),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showResourceGovernorPropertiesFor(sc, rgFocus{page: rgPagePools, pool: node.data.Name})
		}},
	}
}

func workloadGroupsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		newWorkloadGroupItem(a, sc, node.data.RGPool),
		{Divider: true},
		refresh,
	}
}

func workloadGroupMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showResourceGovernorPropertiesFor(sc, rgFocus{page: rgPageGroups, pool: node.data.RGPool, group: node.data.Name})
	})
}

func externalResourcePoolsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		rgItem(sc, "New External Resource Pool...", func() {
			a.showResourceGovernorPropertiesFor(sc, rgFocus{page: rgPageExternalPools})
		}),
		{Divider: true},
		refresh,
	}
}

func externalResourcePoolMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showResourceGovernorPropertiesFor(sc, rgFocus{page: rgPageExternalPools, external: node.data.Name})
	})
}

// -- The governor's own commands ----------------------------------------------

// enableResourceGovernor enables the governor. Not confirmed, as no Enable
// is; but the same statement applies every stored change, so the status line
// says so when there were any.
func (a *App) enableResourceGovernor(sc *db.ServerConn, node *explorerNode) {
	a.runResourceGovernorCommand(sc, node, "Enable Resource Governor", "Enabling Resource Governor...",
		"Resource Governor enabled", func(ctx context.Context, rg *gosmo.ResourceGovernor) error { return rg.Enable(ctx) })
}

// disableResourceGovernor disables the governor, confirmed: every session
// from then on is classified into default and no pool limit applies.
func (a *App) disableResourceGovernor(sc *db.ServerConn, node *explorerNode) {
	a.confirmDialog.ShowConfirm("Disable Resource Governor",
		"Disable Resource Governor? New sessions stop being classified and run in the default group, and no pool or group limit applies until it is enabled again. The stored configuration is kept.",
		func(ok bool) {
			if ok {
				a.runResourceGovernorCommand(sc, node, "Disable Resource Governor", "Disabling Resource Governor...",
					"Resource Governor disabled", func(ctx context.Context, rg *gosmo.ResourceGovernor) error { return rg.Disable(ctx) })
			}
		})
}

// reconfigureResourceGovernor puts the stored changes in force.
func (a *App) reconfigureResourceGovernor(sc *db.ServerConn, node *explorerNode) {
	a.runResourceGovernorCommand(sc, node, "Reconfigure Resource Governor", "Applying the stored Resource Governor configuration...",
		"Resource Governor reconfigured", func(ctx context.Context, rg *gosmo.ResourceGovernor) error { return rg.Reconfigure(ctx) })
}

// resetResourceGovernorStatistics restarts the pools' and groups' cumulative
// counters, confirmed: nothing on screen brings them back.
func (a *App) resetResourceGovernorStatistics(sc *db.ServerConn, node *explorerNode) {
	a.confirmDialog.ShowConfirm("Reset Statistics",
		"Reset Resource Governor statistics? The cumulative counters of every pool and workload group restart from zero.",
		func(ok bool) {
			if ok {
				a.runResourceGovernorCommand(sc, node, "Reset Statistics", "Resetting Resource Governor statistics...",
					"Resource Governor statistics reset", func(ctx context.Context, rg *gosmo.ResourceGovernor) error { return rg.ResetStatistics(ctx) })
			}
		})
}

// runResourceGovernorCommand runs one ALTER RESOURCE GOVERNOR behind the
// progress dialog, then re-reads the node's label in place and the Details
// pane's view of it. A failure re-reads too: a cancel can land after the
// statement committed.
func (a *App) runResourceGovernorCommand(sc *db.ServerConn, node *explorerNode, title, message, done string,
	run func(ctx context.Context, rg *gosmo.ResourceGovernor) error) {
	if !a.requireConn(sc) {
		return
	}
	a.runWithProgress(progressJob{title: title, message: message, what: "running a Resource Governor command", sc: sc},
		func(ctx context.Context, _ progressReport) error {
			return run(ctx, sc.Server.ResourceGovernorRef())
		},
		func(err error, cancelled bool) {
			switch {
			case cancelled:
				a.setStatus(title + " cancelled")
			case err != nil:
				a.setStatus(fmt.Sprintf("%s failed: %v", title, withPermissionAdvice(err)))
			default:
				a.setStatus(done)
			}
			a.refreshResourceGovernorLabel(sc)
			a.detailBrowser.Invalidate(a, node)
		})
}

// resourceGovernorNodeOf is sc's Resource Governor node, or nil when the tree
// has not loaded one.
func (a *App) resourceGovernorNodeOf(sc *db.ServerConn) *explorerNode {
	for _, r := range a.explorer.roots {
		if r.data.conn == sc {
			return findDescendantByType(r, NodeResourceGovernor)
		}
	}
	return nil
}

// refreshResourceGovernorNodes brings sc's Resource Governor subtree up to
// date after Resource Governor Properties saved: the label (see
// refreshResourceGovernorLabel), and the two pool folders reloaded, since
// pools and groups may have come or gone.
func (a *App) refreshResourceGovernorNodes(sc *db.ServerConn) {
	a.explorer.ReloadFolders(sc, folderOf("", NodeResourcePools, NodeExternalResourcePools))
	a.refreshResourceGovernorLabel(sc)
}

// refreshResourceGovernorLabel re-reads the Resource Governor node's state
// into its label in place, the way the Agent node's "(Stopped)" is, so its
// expanded subtree stays open. Reloading Management instead would collapse
// it. A failed read leaves the label bare, as the loader does.
func (a *App) refreshResourceGovernorLabel(sc *db.ServerConn) {
	rgNode := a.resourceGovernorNodeOf(sc)
	if rgNode == nil {
		return
	}
	a.safego("refreshing the Resource Governor node", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		st := resourceGovernorState(ctx, sc)
		a.postAndWake(func() {
			if rgNode.retired {
				return
			}
			st.applyTo(rgNode)
			a.explorer.rebuild()
			a.detailBrowser.Retitle(rgNode)
		})
	})
}

// applyResourceGovernor puts stored Resource Governor changes in force without
// changing whether the governor is on: RECONFIGURE when it is enabled, and
// DISABLE when it is not — RECONFIGURE would enable it (W1), and DISABLE
// clears the pending flag, the next Enable applying everything stored. The
// state is read here, under a script context too, since reads still reach
// the server.
//
// It is how every write gossms makes outside the Properties dialog ends, so
// that a pool deleted from the tree is gone from what is in force, as one
// removed in the dialog is.
func applyResourceGovernor(ctx context.Context, sc *db.ServerConn) error {
	rg, err := sc.Server.ResourceGovernor(ctx)
	if err != nil {
		return fmt.Errorf("reading whether Resource Governor is enabled: %w", err)
	}
	if rg.IsEnabled {
		return rg.Reconfigure(ctx)
	}
	return rg.Disable(ctx)
}
