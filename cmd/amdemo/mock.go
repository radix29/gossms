package main

import (
	"fmt"
	"math"
	"time"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// buckets is how many samples the generated history carries — 30 minutes at
// a 2-second resolution, the Activity Monitor's own retention.
const buckets = 900

// mockStart is the timestamp the generated samples end at. It is fixed, not
// time.Now(), so two runs of the harness produce byte-identical screens and
// a screenshot can be compared against an earlier one.
var mockStart = time.Date(2026, 8, 5, 11, 56, 23, 0, time.UTC)

// The generated workload is two periodic waves plus five one-off events.
//
// The waves are periodic because the dashboard's panels show different
// spans of the same history — a 48-column panel shows the last ~40 samples,
// the full-width waits panel ~140 — and a workload shaped as a single bump
// would leave the narrow panels showing nothing but noise. The one-off
// events sit inside the last 140 samples so every panel that can show them
// does.
const (
	workloadPeriod  = 55.0 // samples per workload wave
	reportingPeriod = 130.0
)

// event is a one-off operational episode: a smooth bump centred a fixed
// number of samples back from the newest one.
type event struct {
	ago    float64 // samples back from the newest, at the peak
	width  float64 // samples, one standard deviation
	height float64
}

var (
	ioBurst     = event{ago: 96, width: 3, height: 1.0}
	reportRun   = event{ago: 62, width: 9, height: 1.0}
	backupRun   = event{ago: 45, width: 5, height: 1.0}
	maintenance = event{ago: 28, width: 6, height: 1.0}
	cpuSpike    = event{ago: 8, width: 2.5, height: 1.0}
)

// at evaluates the event's contribution at bucket i.
func (e event) at(i int) float64 {
	x := float64(buckets-1-i) - e.ago
	return e.height * math.Exp(-(x*x)/(2*e.width*e.width))
}

// wave is the periodic component: a general workload rise and fall every
// panel sees regardless of how many samples it shows.
func wave(i int, period float64) float64 {
	return 0.5 + 0.5*math.Sin(2*math.Pi*float64(i)/period)
}

func workload(i int) float64 { return wave(i, workloadPeriod) }

// wobble is the deterministic small-scale variation laid over everything.
// It is a fixed function of the bucket index rather than a random number,
// so the same bucket always gets the same value.
func wobble(i int, scale float64) float64 {
	return scale * (math.Sin(float64(i)*0.7) + 0.6*math.Sin(float64(i)*0.23+1.1))
}

// series builds one named series from a per-bucket function, clamping
// negatives away — every metric on these dashboards is a rate or a gauge
// that cannot go below zero.
func series(label, short string, color tcell.Color, f func(i int) float64) charts.Series {
	vals := make([]float64, buckets)
	for i := range vals {
		vals[i] = math.Max(f(i), 0)
	}
	return charts.Series{Label: label, Short: short, Color: color, Values: vals}
}

// MockHistory builds a deterministic HistoryView covering the five
// operational patterns.
func MockHistory() dashboard.HistoryView {
	pal := theme.Active()
	return dashboard.HistoryView{
		Header: mockHeader(),
		Activity: []charts.Series{
			series("Batches", "Batch", pal.ChartCyan, func(i int) float64 {
				return 9000 + 11000*workload(i) + 5000*reportRun.at(i) + wobble(i, 900)
			}),
			series("Transactions", "Trans", pal.ChartGreen, func(i int) float64 {
				return 3000 + 4500*workload(i) + 3500*reportRun.at(i) + wobble(i, 400)
			}),
			series("Compiles", "Comp", pal.ChartYellow, func(i int) float64 {
				return 300 + 400*workload(i) + 2500*maintenance.at(i) + wobble(i, 60)
			}),
			series("Recompiles", "Recomp", pal.ChartRed, func(i int) float64 {
				return 80 + 1200*maintenance.at(i) + wobble(i, 30)
			}),
		},
		Lookups: []charts.Series{
			series("Key lookups", "Lookups", pal.ChartCyan, func(i int) float64 {
				return 30000 + 40000*workload(i) + 45000*reportRun.at(i) + wobble(i, 4000)
			}),
			series("Forwarded recs", "Fwd recs", pal.ChartYellow, func(i int) float64 {
				return 400*maintenance.at(i) + wobble(i, 40)
			}),
		},
		Backup: []charts.Series{
			// Mostly flat with one isolated window, the shape a backup
			// actually has — a chart that never shows zero would hide it.
			series("Backup MB/sec", "Backup", pal.ChartBlue, func(i int) float64 {
				return 1.05 * backupRun.at(i)
			}),
		},
		CPU: []charts.Series{
			series("SQL Server %", "SQL", pal.ChartGreen, func(i int) float64 {
				return 25 + 20*workload(i) + 35*cpuSpike.at(i) + wobble(i, 3)
			}),
			series("Other processes %", "Other", pal.ChartYellow, func(i int) float64 {
				return 6 + 4*maintenance.at(i) + wobble(i, 1)
			}),
		},
		Waits: []charts.Series{
			series("Network", "Net", pal.ChartCyan, func(i int) float64 {
				return 600 + 900*workload(i) + wobble(i, 120)
			}),
			series("CPU", "CPU", pal.ChartGreen, func(i int) float64 {
				return 500 + 1100*workload(i) + 5000*cpuSpike.at(i) + wobble(i, 150)
			}),
			series("Memory", "Mem", pal.ChartBlue, func(i int) float64 {
				return 400 + 1500*reportRun.at(i) + wobble(i, 100)
			}),
			series("Disk", "Disk", pal.ChartYellow, func(i int) float64 {
				return 700 + 1000*workload(i) + 6000*ioBurst.at(i) + wobble(i, 200)
			}),
			series("Other", "Other", pal.ChartNeutral, func(i int) float64 {
				return 300 + 500*maintenance.at(i) + wobble(i, 80)
			}),
		},
		Memory: []charts.Series{
			series("Buffer", "Buf", pal.ChartBlue, func(i int) float64 {
				return 70000 + 8000*workload(i) + wobble(i, 900)
			}),
			series("Stolen Buffer", "Stolen", pal.ChartYellow, func(i int) float64 {
				return 14000 + 9000*reportRun.at(i) + wobble(i, 800)
			}),
			series("In-Mem OLTP", "OLTP", pal.ChartCyan, func(i int) float64 {
				return 6000 + wobble(i, 400)
			}),
			series("Query Grants", "Grants", pal.ChartRed, func(i int) float64 {
				return 2000 + 9000*reportRun.at(i) + wobble(i, 300)
			}),
			series("Free", "Free", pal.ChartNeutral, func(i int) float64 {
				return 9000 - 6000*reportRun.at(i) + wobble(i, 500)
			}),
		},
		CacheRatios: []charts.Series{
			series("Buffer cache hit ratio", "Buf cache", pal.ChartCyan, func(i int) float64 {
				return 38 - 6*ioBurst.at(i)
			}),
			series("Procedure cache hit ratio", "Proc cache", pal.ChartBlue, func(i int) float64 {
				return 46 - 8*maintenance.at(i)
			}),
			series("Page life expectancy", "PLE", pal.ChartPurple, func(i int) float64 {
				return 12 - 5*reportRun.at(i)
			}),
		},
		Pages: []charts.Series{
			series("Read", "Read", pal.ChartYellow, func(i int) float64 {
				return 200 + 2500*reportRun.at(i) + 11000*ioBurst.at(i) + wobble(i, 150)
			}),
			series("Write", "Write", pal.ChartRed, func(i int) float64 {
				return 150 + 1800*maintenance.at(i) + 6000*ioBurst.at(i) + wobble(i, 120)
			}),
		},
		DatabaseIO: []charts.Series{
			series("ms/Read", "ms/R", pal.ChartGreen, func(i int) float64 {
				return 3 + 180*ioBurst.at(i) + wobble(i, 1)
			}),
			series("ms/Write", "ms/W", pal.ChartCyan, func(i int) float64 {
				return 2 + 120*ioBurst.at(i) + 30*maintenance.at(i) + wobble(i, 1)
			}),
		},
		LogFlushes: []charts.Series{
			series("Log flushes", "Flushes", pal.ChartYellow, func(i int) float64 {
				return 180 + 350*workload(i) + 500*maintenance.at(i) + wobble(i, 90)
			}),
		},
		Checkpoints: []charts.Series{
			series("Checkpoint pages", "Ckpt pgs", pal.ChartYellow, func(i int) float64 {
				return 3.5 * maintenance.at(i)
			}),
			series("Lazy writes", "Lazy", pal.ChartNeutral, func(i int) float64 {
				return 2.0 * ioBurst.at(i)
			}),
		},
	}
}

// MockSample builds the Sample view from the newest bucket of the same
// generated history, which is exactly the relationship the real Activity
// Monitor has: the Sample tab shows the latest sample the collector stored.
func MockSample(h dashboard.HistoryView) dashboard.SampleView {
	pal := theme.Active()
	last := func(s []charts.Series, i int) float64 {
		if i >= len(s) || len(s[i].Values) == 0 {
			return 0
		}
		return s[i].Values[len(s[i].Values)-1]
	}
	bar := func(label, short string, v float64, color tcell.Color) charts.Bar {
		return charts.Bar{Label: label, Short: short, Value: v, Color: color}
	}

	return dashboard.SampleView{
		Header:           h.Header,
		UserConnections:  "1519",
		BlockedProcesses: "92",
		// Scales come from the work order's table rather than from the
		// bars: several of these panels hold one bar or one dominant bar,
		// and auto-scaling those would show a full-width bar at every
		// value the metric can take.
		Activity: dashboard.BarPanel{
			Scale: charts.Scale{Min: 0, Max: 30000},
			Bars: []charts.Bar{
				bar("Batches", "Batch", last(h.Activity, 0), pal.ChartCyan),
				bar("Transactions", "Trans", last(h.Activity, 1), pal.ChartGreen),
				bar("Compiles", "Comp", last(h.Activity, 2), pal.ChartYellow),
				bar("Recompiles", "Recomp", last(h.Activity, 3), pal.ChartRed),
			},
		},
		Lookups: dashboard.BarPanel{
			Scale: charts.Scale{Min: 0, Max: 120000},
			Bars: []charts.Bar{
				bar("Key lookups", "Lookups", last(h.Lookups, 0), pal.ChartCyan),
				bar("Forwarded recs", "Fwd recs", last(h.Lookups, 1), pal.ChartYellow),
			},
		},
		Backup: dashboard.BarPanel{
			Scale: charts.Scale{Min: 0, Max: 1.2},
			Bars: []charts.Bar{
				bar("Backup MB/sec", "Backup", last(h.Backup, 0), pal.ChartBlue),
			},
		},
		CPUPctOfWaits: "19",
		Waits:         dashboard.BarPanel{Bars: mockWaitBars(pal)},
		LoadFactor:    dashboard.BarPanel{Bars: mockLoadFactorBars(pal)},
		WaitLegend: []charts.LegendItem{
			{Label: "Resource", Color: pal.ChartRed},
			{Label: "Signal", Color: pal.ChartGreen},
		},
		PageLifeExpectancy:  "5761",
		MemoryGrantsPending: "0",
		Memory:              mockMemoryComposition(h),
		CacheRatios: dashboard.BarPanel{
			Bars: []charts.Bar{
				bar("Buffer cache hit ratio", "Buffer", 100, pal.ChartCyan),
				bar("Procedure cache hit ratio", "Procedure", 98.3, pal.ChartBlue),
			},
		},
		Pages: dashboard.BarPanel{
			Scale: charts.Scale{Min: 0, Max: 14000},
			Bars: []charts.Bar{
				bar("Read", "Read", last(h.Pages, 0), pal.ChartYellow),
				bar("Write", "Write", last(h.Pages, 1), pal.ChartRed),
			},
		},
		LogFlushes:      "512",
		CheckpointPages: "0",
		LazyWrites:      "0",
		DatabaseIO: dashboard.BarPanel{
			Scale: charts.Scale{Min: 0, Max: 50},
			Bars: []charts.Bar{
				{Label: "Total", Parts: msReadWrite(1, 14, pal)},
				{Label: "1: AdventureWorks_Data", Short: "AW_Data", Parts: msReadWrite(2, 40, pal)},
				{Label: "2: AdventureWorks_Log", Short: "AW_Log", Parts: msReadWrite(1, 2, pal)},
				{Label: "3: tempdb", Short: "tempdb", Parts: msReadWrite(0, 3, pal)},
			},
		},
	}
}

// mockWaitBars splits each wait category's newest value into a resource and
// a signal part, the way the snapshot mockup shows them.
func mockWaitBars(pal *theme.Palette) []charts.Bar {
	names := []string{"Disk IO", "Extended Events", "Latches: Buffer", "Latches: Buffer IO", "Latches: Non-Buffer", "Locking", "Memory"}
	shorts := []string{"Disk", "XEvents", "Latch: Buf", "Latch: BufIO", "Latch: Other", "Lock", "Mem"}
	values := []float64{9000, 1200, 3000, 2600, 1800, 190000, 4000}

	out := make([]charts.Bar, 0, len(names))
	for i, name := range names {
		out = append(out, charts.Bar{
			Label: name,
			Short: shorts[i],
			Parts: []charts.BarPart{
				{Value: values[i] * 0.92, Color: pal.ChartRed},
				{Value: values[i] * 0.08, Color: pal.ChartGreen},
			},
		})
	}
	return out
}

// mockLoadFactorBars is one bar per core on an eight-core demo host, uneven
// the way a real scheduler distribution is.
func mockLoadFactorBars(pal *theme.Palette) []charts.Bar {
	values := []float64{14, 9, 21, 6, 11, 17, 4, 12}
	out := make([]charts.Bar, 0, len(values))
	for i, v := range values {
		label := fmt.Sprintf("%d", i)
		out = append(out, charts.Bar{Label: label, Short: label, Value: v, Color: pal.ChartGreen})
	}
	return out
}

// msReadWrite splits one file's I/O latency into its read and write parts,
// the pair the DATABASE IO panel is read for.
func msReadWrite(read, write float64, pal *theme.Palette) []charts.BarPart {
	return []charts.BarPart{
		{Value: read, Color: pal.ChartGreen},
		{Value: write, Color: pal.ChartCyan},
	}
}

// mockMemoryComposition takes the newest value of every memory series, so
// the composition bar and the memory history agree.
func mockMemoryComposition(h dashboard.HistoryView) []charts.Series {
	out := make([]charts.Series, 0, len(h.Memory))
	for _, s := range h.Memory {
		v := 0.0
		if len(s.Values) > 0 {
			v = s.Values[len(s.Values)-1]
		}
		out = append(out, charts.Series{Label: s.Label, Short: s.Short, Color: s.Color, Values: []float64{v}})
	}
	return out
}

func mockHeader() dashboard.Header {
	return dashboard.Header{
		Instance:   "INSTANCE  SQLDEMO01\\MSSQLSERVER",
		Version:    "16.0.4165",
		Host:       "Microsoft Windows Server 2022 Standard [demo host]",
		SampleTime: fmt.Sprintf("Sample %s", mockStart.Format("02.01.2006 15:04:05")),
		Resolution: "2 sec",
	}
}
