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

// Fixed scales for single-bar and percentage panels; a lone auto-scaled bar is
// always full.
var (
	backupScale  = charts.Scale{Min: 0, Max: 100} // MB/sec
	latencyScale = charts.Scale{Min: 0, Max: 50}  // milliseconds
	percentScale = charts.Scale{Min: 0, Max: 100}
)

// buildSampleView draws the store's newest sample, so Sample and History
// describe the same instant.
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

// waitBars is one bar per wait category (ms of wait per second), split into
// resource and signal parts. The parts share one colour pair across bars; the
// legend names the split, categories are labelled under their bars.
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

// waitLegend names the two parts of every wait bar.
func waitLegend() []charts.LegendItem {
	_, green, _, _, red, _, _ := chartColors()
	return []charts.LegendItem{
		{Label: "Resource", Color: red},
		{Label: "Signal", Color: green},
	}
}

// loadFactorBars is one bar per visible online scheduler, by cpu_id, from the
// sample's Detail (kept on the newest samples, which this tab draws).
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

// memoryComposition is the stacked composition bar, from the newest sample's
// Detail.
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

// compositionColors cycle chart roles for memory composition, whose slices SQL
// Server names and so have no fixed colour.
func compositionColors() []tcell.Color {
	cyan, green, yellow, blue, red, purple, neutral := chartColors()
	return []tcell.Color{blue, cyan, green, purple, yellow, red, neutral}
}

// databaseIOBars shows total read and write latency, then the busiest databases
// that explain it.
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

// maxIOBars is how many database files the I/O panel names while bars stay
// readable.
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
