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
// Instance's own accounting of what it is allowed and what it has used, from
// three views no on-premises instance has — sys.server_resource_stats,
// sys.dm_instance_resource_governance and sys.dm_os_job_object.
//
// It is the one tab whose feed is not a sampler. The other three derive
// per-second rates by differencing two readings of a counter; this one reads a
// history the *server* has already aggregated into fixed 15-second windows and
// keeps for about two weeks. So each tick replaces the whole view rather than
// appending to a store, there is no activity.Store here, and the charts' bucket
// interval is the server's window rather than the panel's refresh rate. Running
// these numbers through internal/activity's rates.go would average an average.

// instanceHistoryRows is how many 15-second windows one tick reads: an hour,
// comfortably more than the widest canvas plots, so panning left inside the
// dashboard never runs out of history between ticks.
const instanceHistoryRows = 240

// instanceWindow is what one row of sys.server_resource_stats covers when its
// own start/end timestamps do not say — the server's fixed window.
const instanceWindow = 15 * time.Second

// amInstanceRates are the Instance tab's refresh intervals. They start at the
// source's own resolution: polling faster than 15 seconds cannot produce a new
// row, it only re-reads the same ones.
var amInstanceRates = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var amInstanceRateLabels = []string{"15 s", "30 s", "60 s"}

// defaultInstanceRateIdx is 30 seconds — two of the server's own windows per
// tick, which keeps the newest bucket fresh without re-reading an hour of
// history twice as often as anything changes.
const defaultInstanceRateIdx = 1

// amInstanceSample is one reading of the three views: the history, and the two
// single-row limit views beside it. The limits change only when the instance is
// resized, but they are read on every tick anyway — one row each, against a
// history read of hundreds — so a resize shows up without a reconnect.
type amInstanceSample struct {
	At         time.Time
	Stats      []*gosmo.ServerResourceStat
	Governance *gosmo.InstanceResourceGovernance
	JobObject  *gosmo.OSJobObject
}

// probeInstance is one tick. srv is captured by the caller rather than read off
// the panel: this runs on the poller's goroutine, where the panel's fields must
// not be touched.
//
// Only the history read is fatal. The two limit views are a static backdrop —
// an instance that answers the history but refuses dm_os_job_object is still
// worth drawing, and losing the whole tab to a missing backdrop would be the
// worse outcome.
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
// series, oldest bucket first.
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
		// Every value is a total over its own window, so each is divided by
		// that window's own length rather than by a constant: the first row
		// after a restart covers less than 15 seconds, and dividing it by 15
		// would understate exactly the burst worth seeing.
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
		// The quota is the axis, not the high-water mark: a 192 MB database on
		// a 64 GB instance has to draw as a sliver, because that is what it is.
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

// instanceStorageUsedPct is how much of the reserved quota is in use, 0 for an
// instance reporting no quota rather than a division by zero.
func instanceStorageUsedPct(s *gosmo.ServerResourceStat) float64 {
	if s.ReservedStorageMB <= 0 {
		return 0
	}
	return s.StorageSpaceUsedMB / float64(s.ReservedStorageMB) * 100
}

// instanceTimes are the clock times of the plotted buckets, oldest first —
// what a tooltip names the clicked column with. Each row's *end* is the moment
// its numbers describe.
func instanceTimes(stats []*gosmo.ServerResourceStat) []string {
	out := make([]string, len(stats))
	for i, s := range stats {
		out[i] = s.EndTime.Format("15:04:05")
	}
	return out
}

// instanceSeries reads one value per row, as it stands.
func instanceSeries(stats []*gosmo.ServerResourceStat, f func(*gosmo.ServerResourceStat) float64) []float64 {
	out := make([]float64, len(stats))
	for i, s := range stats {
		out[i] = f(s)
	}
	return out
}

// instanceRate reads one *total* per row and divides it by that row's own
// window, turning it into a per-second figure.
func instanceRate(stats []*gosmo.ServerResourceStat, f func(*gosmo.ServerResourceStat) float64) []float64 {
	out := make([]float64, len(stats))
	for i, s := range stats {
		out[i] = f(s) / instanceWindowSeconds(s)
	}
	return out
}

// instanceWindowSeconds is how long one row covers, never zero or negative: a
// row whose timestamps do not describe a window falls back to the server's
// documented one rather than dividing by nothing.
func instanceWindowSeconds(s *gosmo.ServerResourceStat) float64 {
	d := s.EndTime.Sub(s.StartTime)
	if d <= 0 {
		d = instanceWindow
	}
	return d.Seconds()
}

// instanceIOKPIs put the IO ceilings on the section bar rather than on the
// charts' axes. An axis pinned to a 6,000 IOPS limit draws an ordinary
// workload as a flat line on the baseline: true, and useless for reading the
// shape of the IO. The number that says how much headroom is left belongs
// beside the chart, not as its scale.
func instanceIOKPIs(g *gosmo.InstanceResourceGovernance) []charts.KPI {
	if g == nil {
		return nil
	}
	return []charts.KPI{
		{Label: "Local IOPS limit", Value: core.FormatThousands(int64(g.LocalIOPS))},
		{Label: "Log rate limit", Value: formatBytes(g.MaxLogRate) + "/s"},
	}
}

// instanceLimitRows is the limits grid: the resource governor's ceilings on
// SQL Server, then the job object's ceilings on the process SQL Server runs in.
// The two are different layers — an instance can be inside its governor limits
// and still be squeezed by the host — so they are labelled apart rather than
// merged into one list of numbers.
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
		// NULL on a live General Purpose instance, where the job object sets no
		// working-set ceiling — said outright rather than drawn as "0 MB".
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

// instanceOptionalMB spells a limit the view reports as NULL — which arrives
// here as zero — as "not set" rather than as a ceiling of nothing.
func instanceOptionalMB(mb int64) string {
	if mb <= 0 {
		return "not set"
	}
	return formatMB(float64(mb))
}

// instanceFileTime converts sys.dm_os_job_object's cumulative CPU time from
// the Windows FILETIME tick — 100 nanoseconds — into a Duration.
func instanceFileTime(ticks int64) time.Duration {
	return time.Duration(ticks) * 100 * time.Nanosecond
}
