package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The monitor waits options (D3). --top is db table-sizes' name.
const (
	optInterval = "interval"
	optAllWaits = "all"
)

// maxWaitInterval bounds --interval, in seconds.
const maxWaitInterval = 300

// sleepCtx waits d or until ctx is done; a test replaces it.
var sleepCtx = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func monitorWaitsCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "waits"},
		Summary: "Show what the server waits on: the top wait types and the time per category",
		Description: "sys.dm_os_wait_stats. By default the totals since the instance started (since, server local " +
			"time; elapsedMs the uptime) — or since the statistics were last cleared with DBCC SQLPERF, " +
			"which the server does not record. --interval n (1 to 300 seconds) reads twice, n seconds apart, and reports the difference: " +
			"what the server waits on now; since is then the first read's server time, and elapsedMs the " +
			"server time between the two reads. Idle and background waits (sleeps, queue waits, Extended " +
			"Events, Query Store, Service Broker, the idle HADR waits — the same list the TUI's Waits chart " +
			"leaves out) are left out unless --all. Set waits: one row per wait type with any wait time or " +
			"waiting task, most wait time first, --top rows (default 20, 0 for all); resource_ms = wait_ms - " +
			"signal_ms (signal_ms is time runnable, waiting for a CPU); pct is the share of the wait time of " +
			"every wait type counted, not only the rows shown (NULL when none had any); avg_ms is wait_ms per waiting task, NULL with " +
			"none. Set categories: the eight categories (CPU, Disk IO, Locking, Latches, Memory, Log, Network, " +
			"Other) summed over every wait type counted, most wait time first. Needs VIEW SERVER STATE (VIEW " +
			"SERVER PERFORMANCE STATE from SQL Server 2022): without it, exit 9.",
		Options: append(ConnectOptions(),
			spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultTop),
				Help: "Keep this many wait types, the most wait time first; 0 for every one"},
			spec.Option{Name: optInterval, Type: spec.TypeInt, Default: "0",
				Help: "Seconds between two reads, 1 to 300, reporting the difference; 0 for the totals since start"},
			spec.Option{Name: optAllWaits, Type: spec.TypeBool, Help: "Keep the idle and background waits"},
		),
		Class: spec.ClassRead,
		Run:   d.monitorWaits,
		Examples: []string{
			"gossms monitor waits --top 10 -p prod-ro",
			"gossms monitor waits --interval 10 -p prod-ro -o json",
		},
	}
}

// waitSample is one read of sys.dm_os_wait_stats with the server's clock
// around it: the instance's start and the uptime when read.
type waitSample struct {
	start  time.Time
	uptime time.Duration
	stats  []gosmo.WaitStat
}

func readWaits(ctx context.Context, cat Catalog) (waitSample, error) {
	start, up, err := cat.StartTime(ctx)
	if err != nil {
		return waitSample{}, readError(err)
	}
	stats, err := cat.WaitStats(ctx)
	if err != nil {
		return waitSample{}, readError(err)
	}
	return waitSample{start, up, stats}, nil
}

func (d Deps) monitorWaits(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	top := inv.Values[optTop].(int)
	if top < 0 {
		return nil, usageError(fmt.Sprintf("--top: %d is negative", top),
			"Give the number of wait types to keep, or --top 0 for every one.")
	}
	interval := inv.Values[optInterval].(int)
	if interval < 0 || interval > maxWaitInterval {
		return nil, usageError(fmt.Sprintf("--interval: %d is not between 1 and %d", interval, maxWaitInterval),
			"Give the seconds between the two reads, or leave --interval out for the totals since the instance started.")
	}
	all := inv.Values[optAllWaits].(bool)

	res := &output.Result{}
	closeConn, cat, err := d.connectServerState(ctx, inv, res, "monitor waits")
	if err != nil {
		return res, err
	}
	defer closeConn()

	first, err := readWaits(ctx, cat)
	if err != nil {
		return res, err
	}
	since, elapsed, stats := first.start, first.uptime, first.stats
	if interval > 0 {
		if err := sleepCtx(ctx, time.Duration(interval)*time.Second); err != nil {
			return res, err
		}
		second, err := readWaits(ctx, cat)
		if err != nil {
			return res, err
		}
		var cleared bool
		stats, cleared = waitDelta(first.stats, second.stats)
		if cleared {
			res.Warnings = append(res.Warnings, "Some wait totals went down between the two reads: the statistics "+
				"were cleared (DBCC SQLPERF) in between, so those wait types are left out.")
		}
		since, elapsed = first.start.Add(first.uptime), second.uptime-first.uptime
	}

	rows, cats, total := waitRows(stats, all)
	res.Fields = []output.Field{
		{Key: "since", Value: gridTime(since)},
		{Key: "elapsedMs", Value: strconv.FormatInt(elapsed.Milliseconds(), 10)},
	}
	if top > 0 && len(rows) > top {
		rows = rows[:top]
	}
	res.Sets = []output.Set{waitsSet(rows, total), categoriesSet(cats, total)}
	return res, nil
}

// waitDelta is cur minus prev per wait type. A type prev lacks counts from
// zero; one whose totals went down (cleared in between) is left out, and
// cleared says so.
func waitDelta(prev, cur []gosmo.WaitStat) (delta []gosmo.WaitStat, cleared bool) {
	before := make(map[string]gosmo.WaitStat, len(prev))
	for _, w := range prev {
		before[w.WaitType] = w
	}
	for _, w := range cur {
		p := before[w.WaitType]
		d := gosmo.WaitStat{WaitType: w.WaitType, WaitingTasks: w.WaitingTasks - p.WaitingTasks,
			WaitTimeMs: w.WaitTimeMs - p.WaitTimeMs, SignalWaitTimeMs: w.SignalWaitTimeMs - p.SignalWaitTimeMs}
		if d.WaitingTasks < 0 || d.WaitTimeMs < 0 || d.SignalWaitTimeMs < 0 {
			cleared = true
			continue
		}
		delta = append(delta, d)
	}
	return delta, cleared
}

// waitRow is one wait type counted, with its category.
type waitRow struct {
	gosmo.WaitStat
	Category string
}

// categoryRow is one category's sums.
type categoryRow struct {
	Name                    string
	Tasks, WaitMs, SignalMs int64
}

// waitRows is the wait types counted — benign ones left out unless all, and
// those with neither wait time nor a waiting task — most wait time first,
// wait type breaking ties; and every category's sums, most wait time first,
// WaitCategoryNames order breaking ties; and the wait time counted, which
// the shares are of.
func waitRows(stats []gosmo.WaitStat, all bool) (rows []waitRow, cats []categoryRow, total int64) {
	cats = make([]categoryRow, len(activity.WaitCategoryNames))
	for i, n := range activity.WaitCategoryNames {
		cats[i].Name = n
	}
	for _, w := range stats {
		if (!all && activity.IsBenignWait(w.WaitType)) || (w.WaitTimeMs == 0 && w.WaitingTasks == 0) {
			continue
		}
		// The DMV's wait and signal totals are not updated together, so a
		// difference can show more signal time than wait time: clamped, as
		// the TUI's Waits chart does, or resource_ms goes negative.
		w.SignalWaitTimeMs = min(w.SignalWaitTimeMs, w.WaitTimeMs)
		c := &cats[activity.WaitCategoryOf(w.WaitType)]
		c.Tasks += w.WaitingTasks
		c.WaitMs += w.WaitTimeMs
		c.SignalMs += w.SignalWaitTimeMs
		total += w.WaitTimeMs
		rows = append(rows, waitRow{WaitStat: w, Category: c.Name})
	}
	slices.SortFunc(rows, func(a, b waitRow) int {
		return cmp.Or(cmp.Compare(b.WaitTimeMs, a.WaitTimeMs), cmp.Compare(a.WaitType, b.WaitType))
	})
	slices.SortStableFunc(cats, func(a, b categoryRow) int { return cmp.Compare(b.WaitMs, a.WaitMs) })
	return rows, cats, total
}

// waitsSet is monitor waits' waits set: D3's columns.
func waitsSet(rows []waitRow, total int64) output.Set {
	set := output.Set{Name: "waits", Columns: []output.Column{
		{Name: "wait_type", Type: "nvarchar(60)"}, {Name: "category", Type: "nvarchar(20)"},
		{Name: "waiting_tasks", Type: "bigint"}, {Name: "wait_ms", Type: "bigint"},
		{Name: "signal_ms", Type: "bigint"}, {Name: "resource_ms", Type: "bigint"},
		{Name: "pct", Type: "decimal(5,2)", Nullable: true}, {Name: "avg_ms", Type: "decimal(19,2)", Nullable: true},
	}}
	for _, r := range rows {
		var avg *string
		if r.WaitingTasks > 0 {
			avg = ptr(mbf(float64(r.WaitTimeMs) / float64(r.WaitingTasks)))
		}
		set.Rows = append(set.Rows, []*string{
			ptr(r.WaitType), ptr(r.Category), int64Cell(r.WaitingTasks), int64Cell(r.WaitTimeMs),
			int64Cell(r.SignalWaitTimeMs), int64Cell(r.WaitTimeMs - r.SignalWaitTimeMs), pctCell(r.WaitTimeMs, total), avg,
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// categoriesSet is monitor waits' categories set: every category, summed;
// pct NULL when no wait time was counted at all.
func categoriesSet(cats []categoryRow, total int64) output.Set {
	set := output.Set{Name: "categories", Columns: []output.Column{
		{Name: "category", Type: "nvarchar(20)"}, {Name: "waiting_tasks", Type: "bigint"},
		{Name: "wait_ms", Type: "bigint"}, {Name: "signal_ms", Type: "bigint"},
		{Name: "resource_ms", Type: "bigint"}, {Name: "pct", Type: "decimal(5,2)", Nullable: true},
	}}
	for _, c := range cats {
		set.Rows = append(set.Rows, []*string{
			ptr(c.Name), int64Cell(c.Tasks), int64Cell(c.WaitMs), int64Cell(c.SignalMs),
			int64Cell(c.WaitMs - c.SignalMs), pctCell(c.WaitMs, total),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
