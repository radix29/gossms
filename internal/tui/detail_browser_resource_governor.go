package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_resource_governor.go is the Details pane for Management ▸
// Resource Governor: the governor's configuration, the pool and group grids
// with their live counters, and one pool, group or external pool's rows.
//
// The live counters come from the dm_resource_governor_* DMVs, which need
// VIEW SERVER STATE where the catalog views need only VIEW ANY DEFINITION
// (W1). A login with the one and not the other gets the configuration with
// the live columns blank, never an error: the configuration is still worth
// showing, and a blank reads as "not known" where a 0 would read as "idle".

// resourceGovernorDetail is the Resource Governor node's view: what is stored
// beside what is in force. The two differ exactly while a change awaits
// RECONFIGURE, which is the question a DBA brings to this node.
func resourceGovernorDetail(ctx context.Context, sc *dbconn.ServerConn) ([]string, [][]string, error) {
	if !resourceGovernorSupported(sc.Server.Info()) {
		return propertyRows("Supported", "No — "+resourceGovernorUnsupportedLabel)
	}
	// The two halves need different rights (W1), so each degrades alone: a
	// login with VIEW SERVER STATE and not VIEW ANY DEFINITION still sees
	// what is in force.
	var pairs []string
	rg, err := sc.Server.ResourceGovernor(ctx)
	switch {
	case errors.Is(err, gosmo.ErrNotFound):
		pairs = []string{"Configuration", "Not visible — VIEW ANY DEFINITION is needed"}
	case err != nil:
		return nil, nil, err
	default:
		pairs = []string{
			"Enabled", yesNo(rg.IsEnabled),
			"Classifier function", classifierText(rg.ClassifierSchema, rg.ClassifierName, rg.ClassifierFunctionID),
			"Max outstanding I/O per volume", ioPerVolumeText(rg.MaxOutstandingIOPerVolume),
		}
	}
	st, err := sc.Server.ResourceGovernorStatus(ctx)
	if err != nil {
		if rg == nil {
			return nil, nil, err
		}
		return propertyRows(append(pairs, "In force", "Not visible — VIEW SERVER STATE is needed")...)
	}
	pending := yesNo(st.IsReconfigurationPending)
	// While disabled the flag means "changed since DISABLE": nothing waits
	// on a RECONFIGURE, which would enable the governor too (W1).
	if st.IsReconfigurationPending && rg != nil && !rg.IsEnabled {
		pending = "Yes — applied when enabled"
	}
	pairs = append(pairs,
		"Reconfiguration pending", pending,
		"Classifier in force", classifierText(st.ClassifierSchema, st.ClassifierName, st.ClassifierFunctionID),
		"I/O per volume in force", strconv.Itoa(st.MaxOutstandingIOPerVolume))
	// Every pool's counters restart together (RESET STATISTICS, or a
	// restart), so any one pool's start time is the governor's.
	if stats, err := sc.Server.ResourcePoolStats(ctx); err == nil && len(stats) > 0 {
		pairs = append(pairs, "Statistics since", formatSQLDate(stats[0].StatisticsStartTime))
	}
	return propertyRows(pairs...)
}

// classifierText names a classifier function, "(none)" for none. A function
// the login cannot see resolves to no name but still has an id, and says so
// rather than claiming there is none.
func classifierText(schema, name string, id int) string {
	switch {
	case id == 0:
		return "(none)"
	case name == "":
		return fmt.Sprintf("object_id %d (not visible)", id)
	}
	return schema + "." + name
}

// ioPerVolumeText renders the stored MAX_OUTSTANDING_IO_PER_VOLUME, whose 0
// is DEFAULT — the server chooses.
func ioPerVolumeText(n int) string {
	if n == 0 {
		return "Default"
	}
	return strconv.Itoa(n)
}

// zeroAs renders n, or word when n is 0 — for the settings whose 0 means
// "unlimited" or "the server decides" rather than a quantity.
func zeroAs(n int, word string) string {
	if n == 0 {
		return word
	}
	return strconv.Itoa(n)
}

// liveStats is the pool and group DMVs keyed by id, each nil when the login
// cannot read them.
type liveStats struct {
	pools  map[int]*gosmo.ResourcePoolStats
	groups map[int]*gosmo.WorkloadGroupStats
}

// readLiveStats reads both DMVs. A failure of either leaves its map nil,
// which every caller renders as blank counters.
func readLiveStats(ctx context.Context, sc *dbconn.ServerConn) liveStats {
	var ls liveStats
	if ps, err := sc.Server.ResourcePoolStats(ctx); err == nil {
		ls.pools = make(map[int]*gosmo.ResourcePoolStats, len(ps))
		for _, p := range ps {
			ls.pools[p.PoolID] = p
		}
	}
	if gs, err := sc.Server.WorkloadGroupStats(ctx); err == nil {
		ls.groups = make(map[int]*gosmo.WorkloadGroupStats, len(gs))
		for _, g := range gs {
			ls.groups[g.GroupID] = g
		}
	}
	return ls
}

// poolRequests sums the active and queued requests of the pool's groups —
// the pool DMV counts memory grants, not requests. ok is false when the group
// DMV is unreadable, or the pool has no row in it: one created and not yet
// applied by RECONFIGURE.
func (ls liveStats) poolRequests(poolID int) (active, queued int, ok bool) {
	for _, g := range ls.groups {
		if g.PoolID == poolID {
			active += g.ActiveRequestCount
			queued += g.QueuedRequestCount
			ok = true
		}
	}
	return active, queued, ok
}

// resourcePoolsFolderDetail is every pool's limits beside its live load — the
// one place to answer "which pool is starved": queued requests and memory
// grant waiters are the starvation, CPU and memory the consumption.
func resourcePoolsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	pools, err := sc.Server.ResourcePools(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(pools) == 0 {
		return nil, nil, errResourceGovernorNotVisible
	}
	pools = filterObjects(node.data.Filter, pools, func(p *gosmo.ResourcePool) nodeData {
		return nodeData{Name: p.Name}
	})
	ls := readLiveStats(ctx, sc)
	rows := make([][]string, 0, len(pools))
	for _, p := range pools {
		row := []string{
			systemSuffix(p.Name, p.IsSystem()),
			strconv.Itoa(p.MinCPUPercent), strconv.Itoa(p.MaxCPUPercent), strconv.Itoa(p.CapCPUPercent),
			strconv.Itoa(p.MinMemoryPercent), strconv.Itoa(p.MaxMemoryPercent),
			"", "", "", "", "",
		}
		if active, queued, ok := ls.poolRequests(p.ID); ok {
			row[6], row[7] = strconv.Itoa(active), strconv.Itoa(queued)
		}
		if st := ls.pools[p.ID]; st != nil {
			row[8] = strconv.Itoa(st.MemgrantWaiterCount)
			row[9] = strconv.FormatInt(st.TotalCPUUsageMS, 10)
			row[10] = strconv.FormatInt(st.UsedMemoryKB, 10)
		}
		rows = append(rows, row)
	}
	return []string{"Name", "Min CPU %", "Max CPU %", "Cap CPU %", "Min mem %", "Max mem %",
		"Active", "Queued", "Grant waits", "CPU ms", "Used KB"}, rows, nil
}

// errResourceGovernorNotVisible is a pool or group listing that came back
// empty — see resourceGovernorNotVisible for why that is never "none".
var errResourceGovernorNotVisible = errors.New("resource governor catalog is not visible — VIEW ANY DEFINITION is needed")

// resourcePoolDetail is one pool's settings, then its live counters.
func resourcePoolDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	p, err := sc.Server.ResourcePoolByName(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	pairs := []string{
		"Name", p.Name,
		"System pool", yesNo(p.IsSystem()),
		"Min CPU %", strconv.Itoa(p.MinCPUPercent),
		"Max CPU %", strconv.Itoa(p.MaxCPUPercent),
		"Cap CPU %", strconv.Itoa(p.CapCPUPercent),
		"Min memory %", strconv.Itoa(p.MinMemoryPercent),
		"Max memory %", strconv.Itoa(p.MaxMemoryPercent),
		"Min IOPS per volume", zeroAs(p.MinIOPSPerVolume, "None"),
		"Max IOPS per volume", zeroAs(p.MaxIOPSPerVolume, "Unlimited"),
		"Scheduler affinity", poolAffinityText(p.Affinity),
	}
	ls := readLiveStats(ctx, sc)
	if active, queued, ok := ls.poolRequests(p.ID); ok {
		pairs = append(pairs, "Active requests", strconv.Itoa(active), "Queued requests", strconv.Itoa(queued))
	}
	if st := ls.pools[p.ID]; st != nil {
		pairs = append(pairs,
			"CPU used (ms)", strconv.FormatInt(st.TotalCPUUsageMS, 10),
			"Used memory", formatBytes(st.UsedMemoryKB*1024),
			"Target memory", formatBytes(st.TargetMemoryKB*1024),
			"Max memory", formatBytes(st.MaxMemoryKB*1024),
			"Active memory grants", strconv.Itoa(st.ActiveMemgrantCount),
			"Memory grant waiters", strconv.Itoa(st.MemgrantWaiterCount),
			"Out-of-memory count", strconv.FormatInt(st.OutOfMemoryCount, 10),
			"Read", formatBytes(st.ReadBytesTotal),
			"Written", formatBytes(st.WriteBytesTotal),
			"Statistics since", formatSQLDate(st.StatisticsStartTime))
	}
	return propertyRows(pairs...)
}

// workloadGroupsFolderDetail is one pool's groups, their limits beside their
// live load.
func workloadGroupsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	all, err := sc.Server.WorkloadGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(all) == 0 {
		return nil, nil, errResourceGovernorNotVisible
	}
	var groups []*gosmo.WorkloadGroup
	for _, g := range all {
		if g.PoolName == node.data.RGPool {
			groups = append(groups, g)
		}
	}
	groups = filterObjects(node.data.Filter, groups, func(g *gosmo.WorkloadGroup) nodeData {
		return nodeData{Name: g.Name}
	})
	ls := readLiveStats(ctx, sc)
	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		row := []string{
			systemSuffix(g.Name, g.IsSystem()), g.Importance,
			grantPercentText(g.RequestMaxMemoryGrantPercent),
			zeroAs(g.RequestMaxCPUTimeSec, "Unlimited"), strconv.Itoa(g.MaxDOP),
			zeroAs(g.GroupMaxRequests, "Unlimited"),
			"", "", "", "",
		}
		if st := ls.groups[g.ID]; st != nil {
			row[6] = strconv.Itoa(st.ActiveRequestCount)
			row[7] = strconv.Itoa(st.QueuedRequestCount)
			row[8] = strconv.FormatInt(st.TotalRequestCount, 10)
			row[9] = strconv.FormatInt(st.TotalCPUUsageMS, 10)
		}
		rows = append(rows, row)
	}
	return []string{"Name", "Importance", "Max grant %", "Max CPU sec", "MAXDOP", "Max requests",
		"Active", "Queued", "Requests", "CPU ms"}, rows, nil
}

// grantPercentText renders REQUEST_MAX_MEMORY_GRANT_PERCENT, fractional from
// 2019 ("12.5") and whole before it ("25", never "25.0").
func grantPercentText(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// workloadGroupDetail is one group's settings, then its live counters.
func workloadGroupDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	g, err := sc.Server.WorkloadGroupByName(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	pairs := []string{
		"Name", g.Name,
		"System group", yesNo(g.IsSystem()),
		"Resource pool", g.PoolName,
		"External resource pool", g.ExternalPoolName,
		"Importance", g.Importance,
		"Max memory grant %", grantPercentText(g.RequestMaxMemoryGrantPercent),
		"Max CPU time (sec)", zeroAs(g.RequestMaxCPUTimeSec, "Unlimited"),
		"Grant timeout (sec)", zeroAs(g.RequestMemoryGrantTimeoutSec, "Server default"),
		"MAXDOP", zeroAs(g.MaxDOP, "Server default"),
		"Max requests", zeroAs(g.GroupMaxRequests, "Unlimited"),
	}
	// SQL Server 2025's tempdb governance; both nil before it, where the
	// rows would only say "not limited" of a limit that cannot be set.
	if g.GroupMaxTempdbDataPercent != nil || g.GroupMaxTempdbDataMB != nil || serverMajor(sc) >= int(gosmo.SQLServer2025) {
		pairs = append(pairs,
			"Max tempdb data %", optionalFloatText(g.GroupMaxTempdbDataPercent),
			"Max tempdb data MB", optionalFloatText(g.GroupMaxTempdbDataMB))
	}
	if st := readLiveStats(ctx, sc).groups[g.ID]; st != nil {
		pairs = append(pairs,
			"Active requests", strconv.Itoa(st.ActiveRequestCount),
			"Queued requests", strconv.Itoa(st.QueuedRequestCount),
			"Total requests", strconv.FormatInt(st.TotalRequestCount, 10),
			"Total queued", strconv.FormatInt(st.TotalQueuedRequestCount, 10),
			"CPU used (ms)", strconv.FormatInt(st.TotalCPUUsageMS, 10),
			"Longest request CPU (ms)", strconv.FormatInt(st.MaxRequestCPUTimeMS, 10),
			"CPU limit violations", strconv.FormatInt(st.TotalCPULimitViolationCount, 10),
			"Blocked tasks", strconv.Itoa(st.BlockedTaskCount),
			"Effective MAXDOP", strconv.Itoa(st.EffectiveMaxDOP),
			"Statistics since", formatSQLDate(st.StatisticsStartTime))
	}
	return propertyRows(pairs...)
}

// optionalFloatText renders a nullable limit, "Not limited" for NULL.
func optionalFloatText(v *float64) string {
	if v == nil {
		return "Not limited"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// externalResourcePoolsFolderDetail is every external pool's limits. The
// DMV behind external pools is not read by gosmo, so there are no live
// columns here.
func externalResourcePoolsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	pools, err := sc.Server.ExternalResourcePools(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(pools) == 0 {
		return nil, nil, errResourceGovernorNotVisible
	}
	pools = filterObjects(node.data.Filter, pools, func(p *gosmo.ExternalResourcePool) nodeData {
		return nodeData{Name: p.Name}
	})
	rows := make([][]string, 0, len(pools))
	for _, p := range pools {
		rows = append(rows, []string{
			systemSuffix(p.Name, p.IsSystem()),
			strconv.Itoa(p.MaxCPUPercent), strconv.Itoa(p.MaxMemoryPercent),
			zeroAs(p.MaxProcesses, "Unlimited"), externalPoolAffinityText(p.Affinity),
		})
	}
	return []string{"Name", "Max CPU %", "Max mem %", "Max processes", "CPU affinity"}, rows, nil
}

// externalResourcePoolDetail is one external pool's settings.
func externalResourcePoolDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	p, err := sc.Server.ExternalResourcePoolByName(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", p.Name,
		"System pool", yesNo(p.IsSystem()),
		"Max CPU %", strconv.Itoa(p.MaxCPUPercent),
		"Max memory %", strconv.Itoa(p.MaxMemoryPercent),
		"Max processes", zeroAs(p.MaxProcesses, "Unlimited"),
		"CPU affinity", externalPoolAffinityText(p.Affinity),
	)
}

// poolAffinityText renders a pool's AFFINITY SCHEDULER as one mask per
// processor group, "Auto" when the catalog holds none.
func poolAffinityText(a []gosmo.ResourcePoolAffinity) string {
	if len(a) == 0 {
		return "Auto"
	}
	parts := make([]string, 0, len(a))
	for _, g := range a {
		parts = append(parts, fmt.Sprintf("group %d: 0x%X", g.ProcessorGroup, g.SchedulerMask))
	}
	return strings.Join(parts, ", ")
}

// externalPoolAffinityText is poolAffinityText for an external pool's
// AFFINITY CPU.
func externalPoolAffinityText(a []gosmo.ExternalResourcePoolAffinity) string {
	if len(a) == 0 {
		return "Auto"
	}
	parts := make([]string, 0, len(a))
	for _, g := range a {
		parts = append(parts, fmt.Sprintf("group %d: 0x%X", g.ProcessorGroup, g.CPUMask))
	}
	return strings.Join(parts, ", ")
}
