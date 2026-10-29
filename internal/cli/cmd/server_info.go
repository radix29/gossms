package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// serverStateHint names the permission the sys.dm_os_* reads need.
const serverStateHint = "VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server 2022)"

// serverFields is the server facts connect read that agent context and
// server info both report, in this order.
func serverFields(info *gosmo.ServerInfo) []output.Field {
	return []output.Field{
		{Key: "serverName", Value: info.Name},
		{Key: "productVersion", Value: info.ProductVersion},
		{Key: "majorVersion", Value: strconv.Itoa(info.VersionMajor)},
		{Key: "edition", Value: info.Edition},
	}
}

// --- server info ------------------------------------------------------------

func serverInfoCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"server", "info"},
		Summary: "Show the version, edition, uptime, CPUs and memory",
		Description: "A record of the instance's facts: what the connect read (version, level, edition, engine " +
			"edition, platform, collation, clustered, HADR, the default paths) and the sys.dm_os_sys_info and " +
			"sys.dm_os_schedulers figures. startTime is the server's local wall clock, with no zone (the server " +
			"records none); uptimeSeconds is measured by the server's own clock. startTime, uptimeSeconds, " +
			"logicalCpus, numaNodes and physicalMemoryMb need " + serverStateHint + ": without it they are " +
			"null with one warning, and the rest of the record stands (exit 0).",
		Options: ConnectOptions(),
		Class:   spec.ClassRead,
		Run:     d.serverInfo,
		Examples: []string{
			"gossms server info -p dev",
			"gossms server info -p dev -o json",
		},
	}
}

func (d Deps) serverInfo(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	info := cat.ServerInfo()
	if info == nil {
		return res, output.NewError(output.CodeGeneralError, "the server's properties were not read at connect", "")
	}

	// The DMV figures: null where unread. A refusal joins one warning naming
	// the permission; any other failure is its own warning.
	var denied []string
	unread := func(err error, keys ...string) {
		if gosmo.IsPermissionDenied(err) {
			denied = append(denied, keys...)
			return
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s not read: %v", keysWere(keys), err))
	}
	start := output.Field{Key: "startTime", Null: true}
	uptime := output.Field{Key: "uptimeSeconds", Null: true}
	if t, up, err := cat.StartTime(ctx); err != nil {
		unread(err, start.Key, uptime.Key)
	} else {
		start = output.Field{Key: start.Key, Value: gridTime(t)}
		uptime = output.Field{Key: uptime.Key, Value: strconv.FormatInt(int64(up.Seconds()), 10)}
	}
	cpus := output.Field{Key: "logicalCpus", Null: true}
	physical := output.Field{Key: "physicalMemoryMb", Null: true}
	if info.SysInfoUnavailable {
		denied = append(denied, "logicalCpus", "physicalMemoryMb")
	} else {
		cpus = output.Field{Key: "logicalCpus", Value: strconv.Itoa(info.LogicalCPUCount)}
		physical = output.Field{Key: "physicalMemoryMb", Value: strconv.FormatInt(info.PhysicalMemoryMB, 10)}
	}
	numa := output.Field{Key: "numaNodes", Null: true}
	if p, err := cat.ProcessorInfo(ctx); err != nil {
		unread(err, numa.Key)
	} else {
		numa = output.Field{Key: "numaNodes", Value: strconv.Itoa(p.NUMANodeCount)}
	}
	if len(denied) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s not read: sys.dm_os_sys_info and sys.dm_os_schedulers need %s.",
			keysWere(denied), serverStateHint))
	}

	res.Fields = slices.Concat(serverFields(info), []output.Field{
		{Key: "productLevel", Value: info.ProductLevel},
		{Key: "engineEdition", Value: strconv.Itoa(info.EngineEdition)},
		{Key: "platform", Value: info.Platform, Null: info.Platform == ""},
		{Key: "collation", Value: info.Collation},
		{Key: "isClustered", Value: boolText(info.IsClustered)},
		{Key: "isHadrEnabled", Value: boolText(info.IsHADREnabled)},
		start, uptime, cpus, numa, physical,
		{Key: "defaultDataPath", Value: info.DefaultDataPath},
		{Key: "defaultLogPath", Value: info.DefaultLogPath},
		{Key: "defaultBackupPath", Value: info.DefaultBackupPath},
	})
	return res, nil
}

// keysWere is keys as a sentence subject with its verb:
// "a was", "a and b were".
func keysWere(keys []string) string {
	switch len(keys) {
	case 0:
		return ""
	case 1:
		return keys[0] + " was"
	}
	return strings.Join(keys[:len(keys)-1], ", ") + " and " + keys[len(keys)-1] + " were"
}

// --- server memory ----------------------------------------------------------

// The memory figures' sp_configure options, and max's default: unlimited.
const (
	maxServerMemory   = "max server memory (MB)"
	minServerMemory   = "min server memory (MB)"
	unlimitedMemoryMB = 2147483647
)

// The performance counters server memory reads: the instance-wide rows (the
// blank instance), not Buffer Node's per-node ones.
const (
	counterPLE           = "Page life expectancy"
	counterGrantsPending = "Memory Grants Pending"
)

// topClerks is how many clerk types server memory lists.
const topClerks = 10

// lowAvailablePct is the share of physical memory under which available OS
// memory is warned about.
const lowAvailablePct = 5

func serverMemoryCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"server", "memory"},
		Summary: "Show memory use, page life expectancy and top memory clerks",
		Description: fmt.Sprintf("Two sets. memory, one row: physical_mb and available_os_mb (sys.dm_os_sys_memory), "+
			"max_server_memory_mb and min_server_memory_mb (the running values, sys.configurations' value_in_use), "+
			"committed_mb and target_mb (Memory Manager's Total and Target Server Memory), page_life_expectancy_s "+
			"(Buffer Manager's, instance-wide) and memory_grants_pending (Memory Manager's); a counter the server "+
			"does not publish is null. clerks: the %d memory clerk types holding the most (sys.dm_os_memory_clerks, "+
			"summed over NUMA nodes), largest first. Every figure but min/max is a DMV, so without %s the command "+
			"fails, exit 9. Warnings, facts only: max server memory at its default (unlimited); available OS memory "+
			"under %d %% of physical; memory grants pending.",
			topClerks, serverStateHint, lowAvailablePct),
		Options: ConnectOptions(),
		Class:   spec.ClassRead,
		Run:     d.serverMemory,
		Examples: []string{
			"gossms server memory -p dev",
			"gossms server memory -p dev -o json",
		},
	}
}

// memoryDenied is server memory's refusal without the DMVs' permission.
func memoryDenied(err error) *output.Error {
	return output.NewError(output.CodePermissionDenied, err.Error(),
		"The memory DMVs need "+serverStateHint+". Use a login that has it, or have it granted.")
}

// memoryReadError is a server memory read's failure: memoryDenied for a
// refusal, else readError.
func memoryReadError(err error) *output.Error {
	if gosmo.IsPermissionDenied(err) {
		return memoryDenied(err)
	}
	return readError(err)
}

func (d Deps) serverMemory(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	stats, err := cat.MemoryStats(ctx)
	if err != nil {
		return res, memoryReadError(err)
	}
	counters, err := cat.PerformanceCounters(ctx, []string{counterPLE, counterGrantsPending}, []string{""})
	if err != nil {
		return res, memoryReadError(err)
	}
	clerks, err := cat.MemoryClerks(ctx)
	if err != nil {
		return res, memoryReadError(err)
	}
	opts, err := cat.Configurations(ctx)
	if err != nil {
		return res, readError(err)
	}

	var ple, grants *string
	for _, c := range counters {
		switch {
		case c.Object == "Buffer Manager" && c.Counter == counterPLE:
			ple = ptr(strconv.FormatInt(c.Value, 10))
		case c.Object == "Memory Manager" && c.Counter == counterGrantsPending:
			grants = ptr(strconv.FormatInt(c.Value, 10))
			if c.Value > 0 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%d memory grant(s) pending: queries are waiting for workspace memory.", c.Value))
			}
		}
	}
	var maxMem, minMem *string
	for _, o := range opts {
		var cell **string
		switch o.Name {
		case maxServerMemory:
			cell = &maxMem
			if o.ValueInUse == unlimitedMemoryMB {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s is at the default (%d = unlimited).", o.Name, int64(unlimitedMemoryMB)))
			}
		case minServerMemory:
			cell = &minMem
		default:
			continue
		}
		// The running value, with no warning when the configured one differs:
		// the engine itself runs min server memory 0 as 16 (13, 14, 17), so
		// the difference is not a pending RECONFIGURE.
		*cell = ptr(strconv.FormatInt(o.ValueInUse, 10))
	}
	if pct, ok := pctOf(stats.AvailableMemoryMB, stats.PhysicalMemoryMB); ok && pct < lowAvailablePct {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Available OS memory is %d MB (%s %% of physical memory, under %d %%).",
			stats.AvailableMemoryMB, mbf(pct), lowAvailablePct))
	}

	bigint := func(name string, nullable bool) output.Column {
		return output.Column{Name: name, Type: "bigint", Nullable: nullable}
	}
	memory := output.Set{Name: "memory", Columns: []output.Column{
		bigint("physical_mb", false), bigint("available_os_mb", false),
		bigint("max_server_memory_mb", true), bigint("min_server_memory_mb", true),
		bigint("committed_mb", false), bigint("target_mb", false),
		bigint("page_life_expectancy_s", true), bigint("memory_grants_pending", true),
	}, Rows: [][]*string{{
		ptr(strconv.FormatInt(stats.PhysicalMemoryMB, 10)), ptr(strconv.FormatInt(stats.AvailableMemoryMB, 10)),
		maxMem, minMem,
		ptr(strconv.FormatInt(stats.TotalServerMemoryMB, 10)), ptr(strconv.FormatInt(stats.TargetServerMemoryMB, 10)),
		ple, grants,
	}}, TotalRowCount: 1}
	res.Sets = []output.Set{memory, clerkSet(clerks)}
	return res, nil
}

// clerkSet is the topClerks clerk types holding the most, largest first
// (type breaks ties).
func clerkSet(clerks []gosmo.MemoryClerk) output.Set {
	clerks = slices.Clone(clerks)
	slices.SortFunc(clerks, func(a, b gosmo.MemoryClerk) int {
		return cmp.Or(cmp.Compare(b.MB, a.MB), cmp.Compare(a.Type, b.Type))
	})
	set := output.Set{Name: "clerks", Columns: []output.Column{
		{Name: "clerk", Type: "nvarchar(60)"}, {Name: "size_mb", Type: "decimal(18,2)"},
	}}
	for _, c := range clerks[:min(len(clerks), topClerks)] {
		set.Rows = append(set.Rows, []*string{ptr(c.Type), ptr(mbf(c.MB))})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
