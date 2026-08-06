package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// The work order's colour semantics, resolved once here so a metric keeps
// the same colour across refreshes and across the two tabs: cyan is primary
// activity, green transactions and CPU, yellow disk/log/compile work, blue
// memory and cache, red pressure and failures, purple the slower-moving
// memory indicators.
func chartColors() (cyan, green, yellow, blue, red, purple, neutral tcell.Color) {
	p := theme.Active()
	return p.ChartCyan, p.ChartGreen, p.ChartYellow, p.ChartBlue, p.ChartRed, p.ChartPurple, p.ChartNeutral
}

// waitColors give each wait category a fixed colour, indexed by
// activity.WaitCategory, so a category keeps its place in the stack.
func waitColors() []tcell.Color {
	cyan, green, yellow, blue, red, purple, neutral := chartColors()
	return []tcell.Color{
		green,                    // CPU
		yellow,                   // Disk IO
		red,                      // Locking
		purple,                   // Latches
		blue,                     // Memory
		cyan,                     // Log
		neutral,                  // Network
		theme.Active().ChartGrid, // Other
	}
}

// buildHistoryView turns the whole store into the History dashboard's
// series, oldest sample first.
func (am *ActivityMonitor) buildHistoryView() dashboard.HistoryView {
	cyan, green, yellow, blue, red, purple, neutral := chartColors()
	st := &am.store

	series := func(label, short string, color tcell.Color, f func(activity.Sample) float64) charts.Series {
		return charts.Series{Label: label, Short: short, Color: color, Values: st.Series(f)}
	}

	v := dashboard.HistoryView{
		Times: am.sampleTimes(),
		Activity: []charts.Series{
			series("Batches/sec", "Batch", cyan, func(s activity.Sample) float64 { return s.BatchesSec }),
			series("Transactions/sec", "Trans", green, func(s activity.Sample) float64 { return s.TransactionsSec }),
			series("Compiles/sec", "Comp", yellow, func(s activity.Sample) float64 { return s.CompilesSec }),
			series("Recompiles/sec", "Recomp", red, func(s activity.Sample) float64 { return s.RecompilesSec }),
		},
		Lookups: []charts.Series{
			series("Index searches/sec", "Idx", cyan, func(s activity.Sample) float64 { return s.IndexSearchSec }),
			series("Forwarded recs/sec", "Fwd", yellow, func(s activity.Sample) float64 { return s.ForwardedRecSec }),
		},
		Backup: []charts.Series{
			series("Backup MB/sec", "MB/s", green, func(s activity.Sample) float64 { return s.BackupMBSec }),
		},
		// Stacked SQL Server first: the column's height is how busy the host
		// is, and the split says whose work it was. What the column leaves
		// under the 0-100 axis is idle.
		CPU: []charts.Series{
			series("SQL Server %", "SQL", green, func(s activity.Sample) float64 { return s.CPU.SQLPct }),
			series("Other processes %", "Other", yellow, func(s activity.Sample) float64 { return s.CPU.OtherPct }),
		},
		Waits: am.waitSeries(),
		Memory: []charts.Series{
			series("Total server memory MB", "Total", blue, func(s activity.Sample) float64 { return s.TotalServerMemoryMB }),
			series("Target server memory MB", "Target", neutral, func(s activity.Sample) float64 { return s.TargetServerMemMB }),
		},
		CacheRatios: []charts.Series{
			series("Buffer cache hit %", "Buf", blue, func(s activity.Sample) float64 { return s.BufferCacheHitPct }),
			series("Plan cache hit %", "Plan", purple, func(s activity.Sample) float64 { return s.PlanCacheHitPct }),
		},
		Pages: []charts.Series{
			series("Page reads/sec", "Read", cyan, func(s activity.Sample) float64 { return s.PageReadsSec }),
			series("Page writes/sec", "Write", yellow, func(s activity.Sample) float64 { return s.PageWritesSec }),
		},
		DatabaseIO: []charts.Series{
			series("ms/Read", "Read", cyan, func(s activity.Sample) float64 { return s.IOTotal.MsPerRead }),
			series("ms/Write", "Write", yellow, func(s activity.Sample) float64 { return s.IOTotal.MsPerWrite }),
		},
		LogFlushes: []charts.Series{
			series("Log flushes/sec", "Flush", yellow, func(s activity.Sample) float64 { return s.LogFlushesSec }),
			series("Log flush waits/sec", "Wait", red, func(s activity.Sample) float64 { return s.LogFlushWaitsSec }),
		},
		Checkpoints: []charts.Series{
			series("Checkpoint pages/sec", "Ckpt", blue, func(s activity.Sample) float64 { return s.CheckpointPagesSec }),
			series("Lazy writes/sec", "Lazy", purple, func(s activity.Sample) float64 { return s.LazyWritesSec }),
		},
	}

	if latest, ok := st.Latest(); ok {
		v.ActivityKPIs = []charts.KPI{
			{Label: "User Connections", Value: number(latest.UserConnections)},
			{Label: "Active Requests", Value: number(latest.ActiveRequests)},
			{Label: "Blocked", Value: number(latest.BlockedProcs)},
		}
		v.WaitsKPIs = []charts.KPI{
			{Label: "CPU % of Waits", Value: fmt.Sprintf("%.0f", latest.CPUPctOfWaits)},
			{Label: "Runnable Tasks", Value: number(latest.Sched.RunnableTasks)},
		}
		v.MemoryKPIs = []charts.KPI{
			{Label: "Page Life Expectancy", Value: number(latest.PageLifeExpectancy)},
			{Label: "Grants Pending", Value: number(latest.MemoryGrantsPending)},
		}
	}
	return v
}

// sampleTimes are the clock times of the stored samples, oldest first and
// aligned with every plotted series, so a tooltip can name the moment the
// clicked column covers rather than just the newest one.
func (am *ActivityMonitor) sampleTimes() []string {
	samples := am.store.Samples()
	out := make([]string, len(samples))
	for i, s := range samples {
		out[i] = s.At.Format("15:04:05")
	}
	return out
}

// waitSeries builds one stacked series per wait category, in the order the
// categories are declared so the stack doesn't reshuffle between ticks.
func (am *ActivityMonitor) waitSeries() []charts.Series {
	colors := waitColors()
	out := make([]charts.Series, 0, len(activity.WaitCategoryNames))
	for i, name := range activity.WaitCategoryNames {
		out = append(out, charts.Series{
			Label:  name,
			Color:  colors[i],
			Values: am.store.Series(func(s activity.Sample) float64 { return s.Waits[i] }),
		})
	}
	return out
}

// number formats a count for a KPI readout: whole numbers, since these are
// connections, tasks, and seconds rather than rates.
func number(v float64) string { return fmt.Sprintf("%.0f", v) }
