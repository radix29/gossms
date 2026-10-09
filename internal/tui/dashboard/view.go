package dashboard

import (
	"time"

	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Header is the identification strip both dashboards carry: the instance
// watched, where it runs, and the moment on screen.
type Header struct {
	Instance   string
	Version    string
	Host       string
	SampleTime string
	// Resolution names the sampling interval ("2 sec"), shown beside the sample
	// time so it's clear what one column covers.
	Resolution string
	// Status is a non-fatal collector message (failed tick, missing permission)
	// shown in the header so collected data stays readable.
	Status string
	// Paused marks the collector stopped: the numbers are retained, not live.
	Paused bool
}

// HistoryView is everything the History dashboard draws: one series set per
// panel, each ordered oldest sample first.
//
// Every field may be empty. A panel with no series renders its frame and axis
// only: one unavailable metric must not blank the dashboard around it.
type HistoryView struct {
	Header Header

	// Interval is the time one bucket covers, which the time scale under each
	// chart counts back in. Zero leaves the sample time alone, no scale.
	Interval time.Duration

	// Times are the buckets' clock times, oldest first, aligned with every
	// series. They label a tooltip's sample; an empty or short slice omits it.
	Times []string

	// SQL SERVER ACTIVITY section.
	Activity     []charts.Series // batches, transactions, compiles, recompiles — overlaid
	Lookups      []charts.Series // key lookups, forwarded records
	Backup       []charts.Series // backup MB/sec
	ActivityKPIs []charts.KPI    // readouts along the section bar

	// SQL SERVER WAITS section, split half and half between host CPU usage and
	// the wait categories. CPU carries SQL Server and other processes only,
	// stacked on a fixed 0-100 axis; idle is the remainder, deliberately not a
	// series (see activity.CPUUsage).
	CPU       []charts.Series
	Waits     []charts.Series // wait categories — stacked
	WaitsKPIs []charts.KPI

	// SQL SERVER MEMORY section.
	Memory      []charts.Series // total against target server memory
	CacheRatios []charts.Series // buffer and plan cache hit ratios
	Pages       []charts.Series // pages read, pages written
	MemoryKPIs  []charts.KPI

	// DATABASE IO section.
	DatabaseIO  []charts.Series // ms/read, ms/write
	LogFlushes  []charts.Series // log flushes/sec
	Checkpoints []charts.Series // checkpoint pages, lazy writes

	// File names the database or file the DATABASE IO section is scoped to,
	// shown in that section's bar. Empty reads as "Total".
	File string
}

// BarPanel is one group of bars plus the range they are read against. The zero
// Scale auto-scales to the largest bar, meaningful only for comparable bars: a
// single-bar panel needs an explicit range, or the bar fills it at any value.
type BarPanel struct {
	Bars  []charts.Bar
	Scale charts.Scale
}

// SampleView is everything the Sample dashboard draws: the current values,
// not a history of them.
type SampleView struct {
	Header Header

	// SQL SERVER ACTIVITY section.
	UserConnections  string
	BlockedProcesses string
	Activity         BarPanel // batches, trans, comp, recomp
	Lookups          BarPanel // key lookups, forwarded records
	Backup           BarPanel // backup MB/sec

	// SQL SERVER WAITS section. Each bar is one wait category, split into
	// its resource and signal parts.
	CPUPctOfWaits string
	Waits         BarPanel
	WaitLegend    []charts.LegendItem
	// LoadFactor is one bar per visible online scheduler, in cpu_id order.
	// The panel is sized from the bar count, so empty leaves waits the section.
	LoadFactor BarPanel

	// SQL SERVER MEMORY section.
	PageLifeExpectancy  string
	MemoryGrantsPending string
	Memory              []charts.Series // components of one composition bar
	CacheRatios         BarPanel        // buffer and procedure cache hit ratios, in percent
	Pages               BarPanel        // pages read, pages written

	// DATABASE IO section.
	LogFlushes      string
	CheckpointPages string
	LazyWrites      string
	DatabaseIO      BarPanel // per file/database read and write latency
}

// TempDBView is everything the TempDB dashboard draws. It mixes history panels
// (space and activity: levels worth watching move) and current-sample panels
// (file list and session grid: meaningful only for the newest reading).
//
// Every field may be empty, and an empty one blanks only its own panel.
type TempDBView struct {
	Header Header

	// Interval and Times describe the plotted buckets, as on HistoryView.
	Interval time.Duration
	Times    []string

	// TEMPDB SPACE section, full width.
	Space     []charts.Series // version store, user, internal, mixed, free — stacked
	SpaceKPIs []charts.KPI

	// TEMPDB ACTIVITY section.
	TempTables   []charts.Series // active temp tables, creation rate
	VersionTx    []charts.Series // snapshot, non-snapshot version transactions
	VersionRates []charts.Series // version generation against cleanup, KB/sec
	ActivityKPIs []charts.KPI

	// TEMPDB OBJECTS section: reserved space over time on the left, the
	// current object counts on the right.
	ObjectSpace  []charts.Series // reserved MB by object kind — stacked
	ObjectCounts BarPanel
	ObjectKPIs   []charts.KPI

	// TEMPDB FILES section, current sample. Each bar is one file split into
	// used and free.
	Files    BarPanel
	FileNote string // configuration advisory, empty when there is nothing to say
	FileKPIs []charts.KPI

	// TEMPDB SESSION USAGE section, current sample.
	Sessions []SessionRow
}

// SessionRow is one pre-formatted line of the session-usage grid: this package
// draws text; number formatting is the caller's.
type SessionRow struct {
	Session     string
	Login       string
	Host        string
	Application string
	UserMB      string
	InternalMB  string
	TotalMB     string
}

// ChartHit is where one History chart's plot area landed and what it plotted,
// returned by DrawHistory so a click maps back to a sample. Series is the
// chart's own slice: don't hold it past the next draw.
type ChartHit struct {
	Title  string
	Plot   core.Rect
	Series []charts.Series

	// TimeRow is the time-scale row directly under Plot, zero-sized on a chart
	// too short for one; where a caller names the pinned column's moment.
	TimeRow core.Rect

	// Snapshot marks a chart of the current sample, not a history: each series
	// has one value describing one instant, so every column resolves to index
	// 0 (else it would answer only on the rightmost column).
	Snapshot bool
}

// Bucket is the index of the bucket drawn at screen column x, or -1 when x
// is outside the plot or over a column that predates the data.
func (h ChartHit) Bucket(x int) int {
	if h.Snapshot {
		if x < h.Plot.X || x >= h.Plot.Right() {
			return -1
		}
		return 0
	}
	return charts.BucketAt(h.Plot, x, charts.BucketCount(h.Series))
}

// Column is where bucket idx is drawn now, the inverse of Bucket, for a caller
// holding a bucket across redraws. It returns -1 once newer samples push the
// bucket off the left edge. A snapshot chart always answers -1 (nothing drifts).
func (h ChartHit) Column(idx int) int {
	if h.Snapshot {
		return -1
	}
	return charts.ColumnAt(h.Plot, idx, charts.BucketCount(h.Series))
}
