package dashboard

import (
	"time"

	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Header is the identification strip both dashboards carry: which instance
// is being watched, where it runs, and which moment is on screen.
type Header struct {
	Instance   string
	Version    string
	Host       string
	SampleTime string
	// Resolution names the sampling interval ("2 sec"), shown beside the
	// sample time so a reading is never ambiguous about what one column
	// covers.
	Resolution string
	// Status is a non-fatal message from the collector — a failed tick, a
	// missing permission — shown in the header rather than replacing the
	// dashboard, so previously collected data stays readable.
	Status string
	// Paused marks the collector as stopped, so the header can say the
	// numbers are retained rather than live.
	Paused bool
}

// HistoryView is everything the History dashboard draws: one series set per
// panel, each ordered oldest sample first.
//
// Every field may be empty. A panel with no series renders its frame, its
// axis, and nothing else — one unavailable metric must not blank out the
// dashboard around it.
type HistoryView struct {
	Header Header

	// Interval is how much time one plotted bucket covers, which is what the
	// time scale under each chart counts back in. Zero leaves the charts with
	// the sample time alone and no scale.
	Interval time.Duration

	// Times are the clock times of the plotted buckets, oldest first and
	// aligned with every series' values. They label the sample a tooltip
	// reports; an empty or short slice simply leaves that line off.
	Times []string

	// SQL SERVER ACTIVITY section.
	Activity     []charts.Series // batches, transactions, compiles, recompiles — overlaid
	Lookups      []charts.Series // key lookups, forwarded records
	Backup       []charts.Series // backup MB/sec
	ActivityKPIs []charts.KPI    // readouts along the section bar

	// SQL SERVER WAITS section, split half and half between host CPU usage
	// and the wait categories.
	// CPU carries SQL Server and other processes only, stacked against a
	// fixed 0-100 axis; idle is what they leave under it and is deliberately
	// not a series of its own (see activity.CPUUsage).
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

// BarPanel is one group of bars plus the range they are read against. The
// zero Scale auto-scales to the largest bar, which is only meaningful when
// the bars are comparable to each other — a panel holding a single bar
// needs an explicit range, or that bar fills the panel at every value it
// can ever have.
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
	// The panel is sized from the number of bars, so an empty slice leaves
	// the waits chart the whole section.
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

// TempDBView is everything the TempDB dashboard draws. It mixes history and
// current-sample panels: space and activity are levels worth watching move,
// while the file list and the session grid only ever mean anything for the
// newest reading.
//
// Every field may be empty, and an empty one blanks its own panel only.
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

// SessionRow is one line of the session-usage grid, pre-formatted: the
// dashboard package draws text, and deciding how many decimals a megabyte
// gets is the caller's business.
type SessionRow struct {
	Session     string
	Login       string
	Host        string
	Application string
	UserMB      string
	InternalMB  string
	TotalMB     string
}

// ChartHit is where one History chart's plot area landed and what it
// plotted, returned by DrawHistory so a click can be turned back into the
// sample under it. Series is the chart's own slice — read it, don't hold it
// past the next draw.
type ChartHit struct {
	Title  string
	Plot   core.Rect
	Series []charts.Series
}

// Bucket is the index of the bucket drawn at screen column x, or -1 when x
// is outside the plot or over a column that predates the data.
func (h ChartHit) Bucket(x int) int {
	return charts.BucketAt(h.Plot, x, charts.BucketCount(h.Series))
}
