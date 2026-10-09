package dashboard

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Instance canvas size. Same width as the other dashboards so horizontal
// scrolling behaves identically; the height is its four sections' sum.
const (
	InstanceCanvasW = 150
	InstanceCanvasH = 59
)

// Instance section geometry: three chart sections at the history bodies'
// height, then the limits grid.
const (
	instanceHeaderH = 1
	instanceBodyH   = historyBodyH
	instanceLimitsH = 12
)

// InstanceView is everything the Instance dashboard draws: an Azure SQL
// Managed Instance's own accounting of the resources it is allowed and the
// resources it has used.
//
// Unlike the other dashboards (per-second deltas between counter readings),
// every series here comes from sys.server_resource_stats, which the *server*
// pre-aggregates into fixed 15-second windows kept about two weeks. One tick
// reads the whole history, and Interval is the server's window, not the
// panel's refresh rate.
//
// Every field may be empty, and an empty one blanks its own panel only.
type InstanceView struct {
	Header Header

	// Interval is one bucket's time: 15 seconds, the server's own window.
	Interval time.Duration
	// Times are the clock times of the plotted buckets, oldest first.
	Times []string

	// Shape is the instance's identity, drawn along the first section bar:
	// SKU, hardware generation, vCores.
	Shape []charts.KPI

	// INSTANCE CPU section: avg_cpu_percent on a fixed 0-100 axis, the only
	// honest scale (auto-scaling makes a 3% idle instance look busy).
	CPU     []charts.Series
	CPUKPIs []charts.KPI

	// INSTANCE STORAGE section: storage used against the instance's reserved
	// quota. StorageScale carries the quota as its maximum, so the plot reads
	// as a proportion of what's provisioned, not of its own high-water mark.
	StorageScale charts.Scale
	Storage      []charts.Series
	StorageKPIs  []charts.KPI

	// INSTANCE IO section: requests per second on the left, bytes per second
	// on the right. Both auto-scale and the ceilings live on the section bar:
	// an axis pinned to a 6,000 IOPS limit renders an ordinary workload as a
	// flat baseline.
	IORequests []charts.Series
	IOBytes    []charts.Series
	IOKPIs     []charts.KPI

	// INSTANCE LIMITS section: the fixed ceilings, as a key/value grid.
	// Pre-formatted: this package draws text, not byte-rate spelling.
	Limits []LimitRow
}

// LimitRow is one key/value pair of the limits grid.
type LimitRow struct {
	Label string
	Value string
}

// DrawInstance renders the Instance dashboard into r, top to bottom: CPU,
// storage, IO, limits. The returned hits describe where each chart plotted, in
// r's coordinates — see DrawHistory.
func DrawInstance(s tcell.Screen, r core.Rect, v InstanceView) []ChartHit {
	if r.W <= 0 || r.H <= 0 {
		return nil
	}
	drawHeader(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: instanceHeaderH}, v.Header)

	hits := make([]ChartHit, 0, 4)
	y := r.Y + instanceHeaderH
	y = instanceCPU(s, r, y, v, &hits)
	y = instanceStorage(s, r, y, v, &hits)
	y = instanceIO(s, r, y, v, &hits)
	instanceLimits(s, r, y, v)
	return hits
}

func instanceCPU(s tcell.Screen, r core.Rect, y int, v InstanceView, hits *[]ChartHit) int {
	// The shape rides on the CPU section's bar, not a strip of its own: SKU,
	// generation and cores are what the CPU percentage is read against, and a
	// row of three static things doesn't earn a line on a scrolling canvas.
	body, next := section(s, r, y, instanceBodyH, "INSTANCE", append(append([]charts.KPI{}, v.Shape...), v.CPUKPIs...))
	drawChart(s, body, "CPU %", charts.HistoryChart{
		Series:    v.CPU,
		Scale:     charts.Scale{Min: 0, Max: 100},
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)
	return next
}

func instanceStorage(s tcell.Screen, r core.Rect, y int, v InstanceView, hits *[]ChartHit) int {
	body, next := section(s, r, y, instanceBodyH, "INSTANCE STORAGE", v.StorageKPIs)
	drawChart(s, body, "STORAGE USED (MB) AGAINST THE RESERVED QUOTA", charts.HistoryChart{
		Series:    v.Storage,
		Scale:     v.StorageScale,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)
	return next
}

func instanceIO(s tcell.Screen, r core.Rect, y int, v InstanceView, hits *[]ChartHit) int {
	body, next := section(s, r, y, instanceBodyH, "INSTANCE IO", v.IOKPIs)
	cols := splitColumns(body, 2)

	drawChart(s, cols[0], "IO REQUESTS/SEC", charts.HistoryChart{
		Series:    v.IORequests,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)
	if len(cols) > 1 {
		drawChart(s, cols[1], "IO BYTES/SEC", charts.HistoryChart{
			Series:    v.IOBytes,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	return next
}

// instanceLimitColumnW is one label/value pair's share of the grid. Fixed, not
// measured, like the tempdb session grid: self-sizing columns make the table
// jump between readings.
const instanceLimitColumnW = 48

func instanceLimits(s tcell.Screen, r core.Rect, y int, v InstanceView) int {
	body, next := section(s, r, y, instanceLimitsH, "INSTANCE LIMITS", nil)
	if body.W <= 0 || body.H <= 0 {
		return next
	}
	pal := theme.Active()
	core.FillRect(s, body, ' ', theme.StylePanel())
	if len(v.Limits) == 0 {
		core.DrawTextClipped(s, body.X+1, body.Y, body.W-2,
			theme.StylePanel().Foreground(pal.TextDim), "No limits reported yet.")
		return next
	}

	// Column-major: a grid one row short drops the tail of the *last* column,
	// not the second half of every one.
	cols := max((body.W-2)/instanceLimitColumnW, 1)
	rows := (len(v.Limits) + cols - 1) / cols
	rows = min(rows, body.H)
	if rows == 0 {
		return next
	}
	labelStyle := theme.StylePanel().Foreground(pal.TextDim)
	valueStyle := theme.StylePanel()
	for i, lim := range v.Limits {
		col, row := i/rows, i%rows
		x := body.X + 1 + col*instanceLimitColumnW
		if row >= rows || x >= body.Right() {
			continue
		}
		w := min(instanceLimitColumnW-1, body.Right()-x)
		labelW := min(30, w)
		core.DrawTextClipped(s, x, body.Y+row, labelW, labelStyle, lim.Label)
		if w > labelW {
			core.DrawTextClipped(s, x+labelW, body.Y+row, w-labelW, valueStyle, lim.Value)
		}
	}
	return next
}
