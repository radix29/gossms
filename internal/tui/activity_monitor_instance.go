package tui

import (
	"context"
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// activity_monitor_instance.go is the Instance tab: an Azure SQL Managed
// Instance's limits and usage, from sys.server_resource_stats,
// sys.dm_instance_resource_governance and sys.dm_os_job_object (Azure-only
// views).
//
// Its feed isn't a sampler. Other tabs difference counter readings; this reads
// a history the server already aggregated into 15-second windows (kept ~two
// weeks). Each tick replaces the view, there's no activity.Store, and the
// bucket interval is the server's window. Passing these through rates.go would
// average an average.

// instanceHistoryRows is how many 15-second windows a tick reads: an hour, more
// than the widest canvas plots, so panning never runs out between ticks.
const instanceHistoryRows = 240

// instanceWindow is a row's span when its own timestamps don't say.
const instanceWindow = 15 * time.Second

// amInstanceRates start at the source resolution; polling faster than 15
// seconds can't yield a new row.
var amInstanceRates = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var amInstanceRateLabels = []string{"15 s", "30 s", "60 s"}

// defaultInstanceRateIdx is 30 seconds: two server windows per tick.
const defaultInstanceRateIdx = 1

// amInstanceSample is one reading of the three views: the history plus the two
// single-row limit views. Limits change only on resize but are cheap, so
// they're read every tick and a resize shows without reconnecting.
type amInstanceSample struct {
	At         time.Time
	Stats      []*gosmo.ServerResourceStat
	Governance *gosmo.InstanceResourceGovernance
	JobObject  *gosmo.OSJobObject
}

// probeInstance is one tick. srv is captured by the caller: this runs on the
// poller's goroutine and mustn't touch the panel.
//
// Only the history read is fatal; the limit views are backdrop, and losing the
// tab over them would be worse.
func probeInstance(ctx context.Context, srv *gosmo.Server) (*amInstanceSample, error) {
	stats, err := srv.ServerResourceStatsContext(ctx, instanceHistoryRows)
	if err != nil {
		return nil, err
	}
	s := &amInstanceSample{At: time.Now(), Stats: stats}
	s.Governance, _ = srv.InstanceResourceGovernanceContext(ctx)
	s.JobObject, _ = srv.OSJobObjectContext(ctx)
	return s, nil
}

// buildInstanceView turns the last reading into the Instance dashboard's
// series, oldest first.
func (am *ActivityMonitor) buildInstanceView() dashboard.InstanceView {
	cyan, green, yellow, _, red, _, _ := chartColors()
	stats := am.inst.sample.Stats

	v := dashboard.InstanceView{
		Interval: instanceWindow,
		Times:    instanceTimes(stats),
	}
	if len(stats) > 0 {
		v.CPU = []charts.Series{{
			Label: "CPU %", Short: "CPU", Color: green,
			Values: instanceSeries(stats, func(s *gosmo.ServerResourceStat) float64 { return s.AvgCPUPercent }),
		}}
		v.Storage = []charts.Series{{
			Label: "Storage used MB", Short: "Used", Color: cyan,
			Values: instanceSeries(stats, func(s *gosmo.ServerResourceStat) float64 { return s.StorageSpaceUsedMB }),
		}}
		// Each value is a total over its own window, so divide by that window's
		// length: the first row after a restart covers under 15 seconds, and
		// dividing by 15 would understate the burst.
		v.IORequests = []charts.Series{{
			Label: "IO requests/sec", Short: "Requests", Color: yellow,
			Values: instanceRate(stats, func(s *gosmo.ServerResourceStat) float64 { return float64(s.IORequests) }),
		}}
		v.IOBytes = []charts.Series{
			{Label: "Bytes read/sec", Short: "Read", Color: cyan,
				Values: instanceRate(stats, func(s *gosmo.ServerResourceStat) float64 { return float64(s.IOBytesRead) })},
			{Label: "Bytes written/sec", Short: "Written", Color: red,
				Values: instanceRate(stats, func(s *gosmo.ServerResourceStat) float64 { return float64(s.IOBytesWritten) })},
		}
	}

	latest := instanceLatest(stats)
	if latest != nil {
		v.Shape = []charts.KPI{
			{Label: "SKU", Value: latest.SKU},
			{Label: "Hardware", Value: latest.HardwareGeneration},
			{Label: "vCores", Value: fmt.Sprintf("%d", latest.VirtualCoreCount)},
		}
		v.CPUKPIs = []charts.KPI{{Label: "CPU %", Value: fmt.Sprintf("%.1f", latest.AvgCPUPercent)}}
		v.StorageKPIs = []charts.KPI{
			{Label: "Used", Value: formatMB(latest.StorageSpaceUsedMB)},
			{Label: "Reserved", Value: formatMB(float64(latest.ReservedStorageMB))},
			{Label: "Used %", Value: fmt.Sprintf("%.1f", instanceStorageUsedPct(latest))},
		}
		// The quota is the axis, not the high-water mark: 192 MB on a 64 GB
		// instance draws as a sliver.
		v.StorageScale = charts.Scale{Min: 0, Max: float64(latest.ReservedStorageMB)}
	}
	v.IOKPIs = instanceIOKPIs(am.inst.sample.Governance)
	v.Limits = instanceLimitRows(am.inst.sample.Governance, am.inst.sample.JobObject)
	return v
}

// instanceLatest is the newest row, or nil for an empty history.
func instanceLatest(stats []*gosmo.ServerResourceStat) *gosmo.ServerResourceStat {
	if len(stats) == 0 {
		return nil
	}
	return stats[len(stats)-1]
}

// instanceStorageUsedPct is the reserved quota's used share; 0 when no quota is
// reported.
func instanceStorageUsedPct(s *gosmo.ServerResourceStat) float64 {
	if s.ReservedStorageMB <= 0 {
		return 0
	}
	return s.StorageSpaceUsedMB / float64(s.ReservedStorageMB) * 100
}

// instanceTimes are the plotted buckets' clock times, oldest first, for
// tooltips. A row's end is the moment its numbers describe.
func instanceTimes(stats []*gosmo.ServerResourceStat) []string {
	out := make([]string, len(stats))
	for i, s := range stats {
		out[i] = s.EndTime.Format("15:04:05")
	}
	return out
}

// instanceSeries reads one value per row as-is.
func instanceSeries(stats []*gosmo.ServerResourceStat, f func(*gosmo.ServerResourceStat) float64) []float64 {
	out := make([]float64, len(stats))
	for i, s := range stats {
		out[i] = f(s)
	}
	return out
}

// instanceRate divides each row's total by that row's window, giving per-second
// values.
func instanceRate(stats []*gosmo.ServerResourceStat, f func(*gosmo.ServerResourceStat) float64) []float64 {
	out := make([]float64, len(stats))
	for i, s := range stats {
		out[i] = f(s) / instanceWindowSeconds(s)
	}
	return out
}

// instanceWindowSeconds is a row's span, never <= 0: without valid timestamps
// it falls back to the documented window.
func instanceWindowSeconds(s *gosmo.ServerResourceStat) float64 {
	d := s.EndTime.Sub(s.StartTime)
	if d <= 0 {
		d = instanceWindow
	}
	return d.Seconds()
}

// instanceIOKPIs put IO ceilings on the section bar rather than chart axes: an
// axis pinned to a 6,000 IOPS limit flattens ordinary workloads onto the
// baseline.
func instanceIOKPIs(g *gosmo.InstanceResourceGovernance) []charts.KPI {
	if g == nil {
		return nil
	}
	return []charts.KPI{
		{Label: "Local IOPS limit", Value: core.FormatThousands(int64(g.LocalIOPS))},
		{Label: "Log rate limit", Value: formatBytes(g.MaxLogRate) + "/s"},
	}
}

// instanceLimitRows is the limits grid: resource governor ceilings on SQL
// Server, then job object ceilings on its process. Different layers (an
// instance within governor limits can still be squeezed by the host), so
// labelled separately.
func instanceLimitRows(g *gosmo.InstanceResourceGovernance, j *gosmo.OSJobObject) []dashboard.LimitRow {
	var out []dashboard.LimitRow
	add := func(label, value string) {
		out = append(out, dashboard.LimitRow{Label: label, Value: value})
	}
	if g != nil {
		add("Instance CPU cap", fmt.Sprintf("%d %%", g.CapCPU))
		add("Max worker threads", core.FormatThousands(int64(g.MaxWorkerThreads)))
		add("Max log rate", formatBytes(g.MaxLogRate)+"/s")
		add("Local volume IOPS", core.FormatThousands(int64(g.LocalIOPS)))
		add("Managed xStore IOPS", core.FormatThousands(int64(g.ManagedXStoreIOPS)))
		add("Local outstanding IO", core.FormatThousands(int64(g.LocalMaxOutstandingIO)))
		add("TempDB log files", core.FormatThousands(int64(g.TempDBLogFileNumber)))
		add("Data directory quota", formatMB(float64(g.DataDirectoryQuotaMB)))
		add("Data directory used", formatMB(float64(g.DataDirectoryUsageMB)))
		if g.BufferPoolExtensionSizeGB > 0 {
			add("Buffer pool extension", fmt.Sprintf("%d GB", g.BufferPoolExtensionSizeGB))
		}
	}
	if j != nil {
		add("Job CPU rate", core.FormatThousands(int64(j.CPURate)))
		add("Job memory limit", formatMB(float64(j.MemoryLimitMB)))
		add("Process memory limit", formatMB(float64(j.ProcessMemoryLimitMB)))
		// NULL on General Purpose, which sets no working-set ceiling; say so
		// rather than "0 MB".
		add("Working set limit", instanceOptionalMB(j.WorkingSetLimitMB))
		add("Low memory signal at", formatMB(float64(j.LowMemSignalThresholdMB)))
		add("Peak job memory used", formatMB(float64(j.PeakJobMemoryUsedMB)))
		add("Peak process memory used", formatMB(float64(j.PeakProcessMemoryUsedMB)))
		add("Job CPU time (user)", formatHMS(instanceFileTime(j.TotalUserTime)))
		add("Job CPU time (kernel)", formatHMS(instanceFileTime(j.TotalKernelTime)))
		add("Job IO reads", core.FormatThousands(j.ReadOperationCount))
		add("Job IO writes", core.FormatThousands(j.WriteOperationCount))
	}
	return out
}

// instanceOptionalMB spells a NULL limit (arriving as zero) as "not set".
func instanceOptionalMB(mb int64) string {
	if mb <= 0 {
		return "not set"
	}
	return formatMB(float64(mb))
}

// instanceFileTime converts sys.dm_os_job_object's CPU time from 100ns FILETIME
// ticks to a Duration.
func instanceFileTime(ticks int64) time.Duration {
	return time.Duration(ticks) * 100 * time.Nanosecond
}
