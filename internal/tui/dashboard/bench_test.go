package dashboard

import (
	"math"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// benchHistory is a History view at the store's full 30 minutes at the
// fastest rate — 900 samples, the worst case the panel ever draws.
func benchHistory() HistoryView {
	const n = 900
	series := func(labels ...string) []charts.Series {
		out := make([]charts.Series, len(labels))
		for i, label := range labels {
			vals := make([]float64, n)
			for j := range vals {
				vals[j] = 50 + 40*math.Sin(float64(j)/17+float64(i))
			}
			out[i] = charts.Series{Label: label, Color: tcell.NewRGBColor(9, 99, 199), Values: vals}
		}
		return out
	}
	return HistoryView{
		Header:      Header{Instance: "SQLDEMO01", SampleTime: "11:56:23", Resolution: "2 sec"},
		Interval:    2 * time.Second,
		Activity:    series("Batches", "Transactions", "Compilations"),
		Lookups:     series("Key lookups", "Forwarded records"),
		Backup:      series("Backup MB/sec"),
		CPU:         series("SQL Server %", "Other processes %"),
		Waits:       series("Network", "Buffer I/O", "Lock", "Memory"),
		Memory:      series("Total server memory", "Target server memory"),
		CacheRatios: series("Buffer cache hit ratio", "Plan cache hit ratio"),
		Pages:       series("Read", "Written"),
		DatabaseIO:  series("ms/Read", "ms/Write"),
		LogFlushes:  series("Log flushes"),
		Checkpoints: series("Checkpoint pages", "Lazy writes"),
	}
}

// BenchmarkDrawHistory is one full render of the History dashboard onto a
// fresh canvas — the panel's per-frame cost whenever its cache misses.
func BenchmarkDrawHistory(b *testing.B) {
	v := benchHistory()
	for b.Loop() {
		c := charts.NewCanvas(HistoryCanvasW, HistoryCanvasH)
		DrawHistory(c, c.Rect(), v)
	}
}
