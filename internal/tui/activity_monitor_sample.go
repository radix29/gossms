package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// Fixed scales for the Sample panels holding a single bar or a percentage.
// A lone auto-scaled bar fills its panel at every value it can ever have,
// so it needs a range to be read against.
var (
	backupScale  = charts.Scale{Min: 0, Max: 100} // MB/sec
	latencyScale = charts.Scale{Min: 0, Max: 50}  // milliseconds
	percentScale = charts.Scale{Min: 0, Max: 100}
)

// buildSampleView draws the newest sample in the store. Sample is History's
// most recent sample rather than a second collection of its own, so the two
// tabs always describe the same instant.
func (am *ActivityMonitor) buildSampleView() dashboard.SampleView {
	latest, ok := am.store.Latest()
	if !ok {
		return dashboard.SampleView{}
	}
	cyan, green, yellow, blue, red, purple, _ := chartColors()

	v := dashboard.SampleView{
		UserConnections:  number(latest.UserConnections),
		BlockedProcesses: number(latest.BlockedProcs),
		Activity: dashboard.BarPanel{Bars: []charts.Bar{
			{Label: "Batches/sec", Short: "Batch", Value: latest.BatchesSec, Color: cyan},
			{Label: "Transactions/sec", Short: "Trans", Value: latest.TransactionsSec, Color: green},
			{Label: "Compiles/sec", Short: "Comp", Value: latest.CompilesSec, Color: yellow},
			{Label: "Recompiles/sec", Short: "Recomp", Value: latest.RecompilesSec, Color: red},
		}},
		Lookups: dashboard.BarPanel{Bars: []charts.Bar{
			{Label: "Index searches/sec", Short: "Idx", Value: latest.IndexSearchSec, Color: cyan},
			{Label: "Forwarded recs/sec", Short: "Fwd", Value: latest.ForwardedRecSec, Color: yellow},
		}},
		Backup: dashboard.BarPanel{
			Bars:  []charts.Bar{{Label: "Backup MB/sec", Short: "MB/s", Value: latest.BackupMBSec, Color: green}},
			Scale: backupScale,
		},
		CPUPctOfWaits: fmt.Sprintf("%.0f", latest.CPUPctOfWaits),
		Waits:         dashboard.BarPanel{Bars: waitBars(latest)},
		WaitLegend:    waitLegend(),
		LoadFactor:    dashboard.BarPanel{Bars: loadFactorBars(latest)},

		PageLifeExpectancy:  number(latest.PageLifeExpectancy),
		MemoryGrantsPending: number(latest.MemoryGrantsPending),
		Memory:              memoryComposition(latest),
		CacheRatios: dashboard.BarPanel{
			Bars: []charts.Bar{
				{Label: "Buffer cache hit %", Short: "Buf", Value: latest.BufferCacheHitPct, Color: blue},
				{Label: "Plan cache hit %", Short: "Plan", Value: latest.PlanCacheHitPct, Color: purple},
			},
			Scale: percentScale,
		},
		Pages: dashboard.BarPanel{Bars: []charts.Bar{
			{Label: "Page reads/sec", Short: "Read", Value: latest.PageReadsSec, Color: cyan},
			{Label: "Page writes/sec", Short: "Write", Value: latest.PageWritesSec, Color: yellow},
		}},

		LogFlushes:      fmt.Sprintf("%.0f", latest.LogFlushesSec),
		CheckpointPages: fmt.Sprintf("%.0f", latest.CheckpointPagesSec),
		LazyWrites:      fmt.Sprintf("%.0f", latest.LazyWritesSec),
		DatabaseIO: dashboard.BarPanel{
			Bars:  databaseIOBars(latest),
			Scale: latencyScale,
		},
	}
	return v
}

// waitBars is one bar per wait category, milliseconds of wait per second,
// each split into the resource half and the signal half. The two parts share
// one colour pair across every bar because the legend names the split, not
// the categories — those are labelled under their own bars.
func waitBars(s activity.Sample) []charts.Bar {
	_, green, _, _, red, _, _ := chartColors()
	bars := make([]charts.Bar, 0, len(activity.WaitCategoryNames))
	for i, name := range activity.WaitCategoryNames {
		signal := s.WaitsSignal[i]
		resource := s.Waits[i] - signal
		if resource < 0 {
			resource = 0
		}
		bars = append(bars, charts.Bar{Label: name, Parts: []charts.BarPart{
			{Value: resource, Color: red},
			{Value: signal, Color: green},
		}})
	}
	return bars
}

// waitLegend names the two parts every wait bar is split into.
func waitLegend() []charts.LegendItem {
	_, green, _, _, red, _, _ := chartColors()
	return []charts.LegendItem{
		{Label: "Resource", Color: red},
		{Label: "Signal", Color: green},
	}
}

// loadFactorBars is one bar per visible online scheduler, in cpu_id order.
// Like the memory composition it comes from the sample's Detail, which only
// the newest samples keep — which is the sample this tab draws.
func loadFactorBars(s activity.Sample) []charts.Bar {
	if s.Detail == nil {
		return nil
	}
	_, green, _, _, _, _, _ := chartColors()
	bars := make([]charts.Bar, 0, len(s.Detail.Load))
	for _, l := range s.Detail.Load {
		label := strconv.Itoa(l.CPUID)
		bars = append(bars, charts.Bar{Label: label, Short: label, Value: l.LoadFactor, Color: green})
	}
	return bars
}

// memoryComposition is the stacked composition bar. It comes from the
// sample's Detail, which only the newest samples keep — which is exactly
// the sample this tab draws.
func memoryComposition(s activity.Sample) []charts.Series {
	if s.Detail == nil {
		return nil
	}
	palette := compositionColors()
	out := make([]charts.Series, 0, len(s.Detail.Memory))
	for i, c := range s.Detail.Memory {
		out = append(out, charts.Series{
			Label:  c.Name,
			Color:  palette[i%len(palette)],
			Values: []float64{c.MB},
		})
	}
	return out
}

// compositionColors cycle through the chart roles for the memory
// composition bar, whose slices are named by SQL Server rather than by this
// application and so have no fixed semantic colour.
func compositionColors() []tcell.Color {
	cyan, green, yellow, blue, red, purple, neutral := chartColors()
	return []tcell.Color{blue, cyan, green, purple, yellow, red, neutral}
}

// databaseIOBars shows total read and write latency, then the busiest
// databases behind it — the totals are what the panel is read for, and the
// per-database rows explain them.
func databaseIOBars(s activity.Sample) []charts.Bar {
	cyan, _, yellow, _, _, _, _ := chartColors()
	bars := []charts.Bar{
		{Label: "ms/Read (Total)", Short: "Read", Value: s.IOTotal.MsPerRead, Color: cyan},
		{Label: "ms/Write (Total)", Short: "Write", Value: s.IOTotal.MsPerWrite, Color: yellow},
	}
	if s.Detail == nil {
		return bars
	}
	for _, io := range topDatabases(s.Detail.PerDatabaseIO, maxIOBars) {
		name := io.Label()
		bars = append(bars,
			charts.Bar{Label: name + " ms/Read", Short: name, Value: io.MsPerRead, Color: cyan},
			charts.Bar{Label: name + " ms/Write", Short: name, Value: io.MsPerWrite, Color: yellow})
	}
	return bars
}

// maxIOBars is how many database files the I/O panel names before the bars
// stop being individually readable.
const maxIOBars = 3

// topDatabases picks the n busiest rows by throughput.
func topDatabases(all []activity.FileIO, n int) []activity.FileIO {
	busiest := slices.Clone(all)
	slices.SortFunc(busiest, func(a, b activity.FileIO) int {
		return cmp.Compare(throughput(b), throughput(a))
	})
	if len(busiest) > n {
		busiest = busiest[:n]
	}
	return busiest
}

func throughput(io activity.FileIO) float64 { return io.ReadMBSec + io.WriteMBSec }
