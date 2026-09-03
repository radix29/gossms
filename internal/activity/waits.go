package activity

import (
	"context"
	"database/sql"
	"strings"
)

// WaitCategory groups wait types into the handful of buckets the dashboard
// plots. The full list of wait types runs to several hundred, most of which
// never move; grouping is what makes a wait chart readable.
type WaitCategory int

const (
	WaitCPU WaitCategory = iota
	WaitDiskIO
	WaitLock
	WaitLatch
	WaitMemory
	WaitLog
	WaitNetwork
	WaitOther
	waitCategoryCount
)

// WaitCategoryNames label the categories, indexed by WaitCategory.
var WaitCategoryNames = [waitCategoryCount]string{
	"CPU", "Disk IO", "Locking", "Latches", "Memory", "Log", "Network", "Other",
}

// waitRow is one wait type's cumulative totals.
type waitRow struct {
	waitMs   int64
	signalMs int64
	tasks    int64
}

// waitSet is one sample of sys.dm_os_wait_stats, keyed by wait type.
type waitSet map[string]waitRow

// benignWaits are the idle and background waits every server accumulates by
// the hour whether or not it is doing anything. Left in, they dwarf every
// real wait on the chart and the panel shows nothing but their bar.
//
// These are the ones that don't belong to a family; benignFamilies below
// covers the families.
var benignWaits = []string{
	"CHECKPOINT_QUEUE", "CHKPT", "DIRTY_PAGE_POLL", "DISPATCHER_QUEUE_SEMAPHORE",
	"EXECSYNC", "FSAGENT", "KSOURCE_WAKEUP", "LAZYWRITER_SLEEP", "LOGMGR_QUEUE",
	"MEMORY_ALLOCATION_EXT", "ONDEMAND_TASK_QUEUE",
	"REDO_THREAD_PENDING_WORK", "REQUEST_FOR_DEADLOCK_SEARCH", "RESERVED_MEMORY_ALLOCATION_EXT",
	"RESOURCE_QUEUE", "SERVER_IDLE_CHECK", "SNI_HTTP_ACCEPT", "SOS_WORK_DISPATCHER",
	"SP_SERVER_DIAGNOSTICS_SLEEP", "WAIT_FOR_RESULTS", "WAITFOR", "WAITFOR_TASKSHUTDOWN",
	"WAIT_ON_SYNC_STATISTICS_REFRESH", "PREEMPTIVE_XE_GETTARGETSTATE",
	"PREEMPTIVE_OS_DMV_PDH_QUERY",
}

// benignFamilies are whole prefixes of background waits, excluded by
// pattern rather than by name.
//
// Naming them individually is what let PWAIT_EXTENSIBILITY_CLEANUP_TASK
// through on SQL Server 2025: it sleeps for five minutes and then reports
// all 300,000 ms of it against one two-second sample, which is 150,000 ms
// of "CPU wait" per second — two orders of magnitude above every real wait
// on the chart, flattening the whole waits panel to nothing. Every future
// release adds background waits, and a list of names can only ever be out
// of date; a family pattern is not.
var benignFamilies = []string{
	"SLEEP%", "QDS\\_%", "XE\\_%", "BROKER\\_%", "HADR\\_%", "PWAIT\\_%",
	"FT\\_%", "PARALLEL\\_REDO\\_%", "DBMIRROR%", "SQLTRACE\\_%", "CLR\\_%",
	"WAIT\\_XTP\\_%",
}

var waitQuery = buildWaitQuery()

// buildWaitQuery excludes the benign waits by name and the benign families
// by pattern. ESCAPE is needed because the family patterns contain
// underscores, which LIKE would otherwise treat as single-character
// wildcards — "SLEEP_%" would then also match SLEEPING_ANYTHING.
func buildWaitQuery() string {
	var b strings.Builder
	b.WriteString("SELECT wait_type, wait_time_ms, signal_wait_time_ms, waiting_tasks_count ")
	b.WriteString("FROM sys.dm_os_wait_stats WHERE wait_type NOT IN (")
	b.WriteString(quotedList(benignWaits))
	b.WriteString(")")
	for _, family := range benignFamilies {
		b.WriteString(" AND wait_type NOT LIKE '")
		b.WriteString(family)
		b.WriteString("' ESCAPE '\\'")
	}
	return b.String()
}

// collectWaits reads the cumulative wait totals, minus the benign ones.
func collectWaits(ctx context.Context, db *sql.DB) (waitSet, error) {
	rows, err := db.QueryContext(ctx, waitQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := make(waitSet, 256)
	for rows.Next() {
		var name string
		var r waitRow
		if err := rows.Scan(&name, &r.waitMs, &r.signalMs, &r.tasks); err != nil {
			return nil, err
		}
		set[name] = r
	}
	return set, rows.Err()
}

// categorize maps a wait type to the bucket it is plotted in. The rules are
// prefix-based because SQL Server names waits by family — every page latch
// is a PAGELATCH_*, every lock an LCK_M_* — and new members of a family
// appear with every release.
func categorize(waitType string) WaitCategory {
	w := strings.ToUpper(waitType)
	switch {
	case strings.HasPrefix(w, "LCK_M_"):
		return WaitLock
	case strings.HasPrefix(w, "PAGEIOLATCH_"), w == "IO_COMPLETION", w == "ASYNC_IO_COMPLETION",
		w == "BACKUPIO", w == "WRITE_COMPLETION", w == "DISKIO_SUSPEND":
		return WaitDiskIO
	case strings.HasPrefix(w, "PAGELATCH_"), strings.HasPrefix(w, "LATCH_"),
		strings.HasPrefix(w, "TREE"):
		return WaitLatch
	case strings.HasPrefix(w, "WRITELOG"), strings.HasPrefix(w, "LOGBUFFER"),
		strings.HasPrefix(w, "LOGMGR"), w == "LOG_RATE_GOVERNOR":
		return WaitLog
	case strings.HasPrefix(w, "RESOURCE_SEMAPHORE"), w == "CMEMTHREAD", w == "MEMORY_GRANT_UPDATE",
		strings.HasPrefix(w, "MEMORY_"):
		return WaitMemory
	case strings.HasPrefix(w, "ASYNC_NETWORK_IO"), strings.HasPrefix(w, "NETWORK_IO"),
		strings.HasPrefix(w, "PREEMPTIVE_OS_WAITFORSINGLEOBJECT"), w == "EXTERNAL_SCRIPT_NETWORK_IO":
		return WaitNetwork
	case strings.HasPrefix(w, "SOS_SCHEDULER_YIELD"), strings.HasPrefix(w, "THREADPOOL"),
		strings.HasPrefix(w, "CXPACKET"), strings.HasPrefix(w, "CXCONSUMER"),
		strings.HasPrefix(w, "CXSYNC_"):
		return WaitCPU
	default:
		return WaitOther
	}
}

// waitDeltas converts two cumulative wait samples into per-second wait time
// by category, the signal half of that time by category, and the share of
// all wait time spent as signal wait — the time a task spent runnable after
// its resource was ready, which is the DMV's own view of CPU pressure.
//
// wait_time_ms already includes signal_wait_time_ms, so byCategory is the
// whole wait and signalByCategory is the part of it inside: subtract to get
// the resource half. Keeping the signal time against the category that
// waited, rather than folding all of it into WaitCPU, is what lets one bar
// show both halves — a category whose waits are mostly signal is queueing
// for CPU, and that is invisible once the two are summed elsewhere.
//
// A wait type present now but not in prev contributes nothing: the server
// has been restarted, or the wait type has just appeared, and either way
// its whole cumulative total is not a delta.
func waitDeltas(prev, cur waitSet, elapsed float64) (byCategory, signalByCategory [waitCategoryCount]float64, signalPct float64) {
	if elapsed <= 0 {
		return byCategory, signalByCategory, 0
	}
	var totalMs, signalMs float64
	for name, c := range cur {
		p, ok := prev[name]
		if !ok {
			continue
		}
		d := float64(c.waitMs - p.waitMs)
		s := float64(c.signalMs - p.signalMs)
		if d < 0 || s < 0 {
			continue
		}
		if s > d {
			s = d
		}
		cat := categorize(name)
		byCategory[cat] += d / elapsed
		signalByCategory[cat] += s / elapsed
		totalMs += d
		signalMs += s
	}
	if totalMs > 0 {
		signalPct = signalMs / totalMs * 100
	}
	return byCategory, signalByCategory, signalPct
}
