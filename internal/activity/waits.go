package activity

import (
	"context"
	"database/sql"
	"strings"
)

// WaitCategory groups the hundreds of wait types into the few buckets the
// dashboard plots.
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

// benignWaits are idle/background waits that accumulate constantly; left in
// they dwarf every real wait. benignFamilies covers whole families.
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

// benignFamilies are background-wait prefixes, excluded by pattern. Names alone
// let PWAIT_EXTENSIBILITY_CLEANUP_TASK through on SQL Server 2025: it reports
// 300,000 ms after a five-minute sleep in one 2s sample, flattening the waits
// panel. New releases keep adding background waits; a family pattern stays
// current.
var benignFamilies = []string{
	"SLEEP%", "QDS\\_%", "XE\\_%", "BROKER\\_%", "HADR\\_%", "PWAIT\\_%",
	"FT\\_%", "PARALLEL\\_REDO\\_%", "DBMIRROR%", "SQLTRACE\\_%", "CLR\\_%",
	"WAIT\\_XTP\\_%",
}

var waitQuery = buildWaitQuery()

// buildWaitQuery excludes benign waits by name and families by pattern. ESCAPE
// keeps pattern underscores literal; unescaped "QDS_%" would match
// QDSXANYTHING.
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

// collectWaits reads cumulative wait totals, minus benign ones.
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

// categorize maps a wait type to its plot bucket. Prefix-based because waits
// are named by family (PAGELATCH_*, LCK_M_*) and families gain members every
// release.
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

// waitDeltas turns two cumulative samples into per-second wait time by
// category, the signal part of it by category, and the overall signal share
// (time runnable after the resource was ready — the DMV's view of CPU
// pressure).
//
// wait_time_ms includes signal_wait_time_ms, so resource time = byCategory -
// signalByCategory. Keeping signal time per category, rather than folding it
// into WaitCPU, lets one bar show both halves; a mostly-signal category is
// queueing for CPU.
//
// A wait type absent from prev contributes nothing (restart or new type): its
// cumulative total isn't a delta.
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
