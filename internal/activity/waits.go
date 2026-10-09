package activity

import (
	"context"
	"slices"
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

// benignFamilies are background-wait prefixes, excluded by family: names alone
// let PWAIT_EXTENSIBILITY_CLEANUP_TASK through on SQL Server 2025 (300,000 ms
// in one 2s sample, flattening the panel), and releases keep adding more.
var benignFamilies = []string{
	"SLEEP", "QDS_", "XE_", "BROKER_", "HADR_", "PWAIT_",
	"FT_", "PARALLEL_REDO_", "DBMIRROR", "SQLTRACE_", "CLR_",
	"WAIT_XTP_",
}

// isBenignWait reports a wait left out of the picture: one of benignWaits by
// name, or of benignFamilies by prefix. Case-insensitive, as the server's
// collation would be.
func isBenignWait(waitType string) bool {
	w := strings.ToUpper(waitType)
	if slices.Contains(benignWaits, w) {
		return true
	}
	return slices.ContainsFunc(benignFamilies, func(prefix string) bool { return strings.HasPrefix(w, prefix) })
}

// collectWaits reads cumulative wait totals, minus benign ones.
func collectWaits(ctx context.Context, src Source) (waitSet, error) {
	stats, err := src.WaitStats(ctx)
	if err != nil {
		return nil, err
	}
	set := make(waitSet, len(stats))
	for _, w := range stats {
		if isBenignWait(w.WaitType) {
			continue
		}
		set[w.WaitType] = waitRow{waitMs: w.WaitTimeMs, signalMs: w.SignalWaitTimeMs, tasks: w.WaitingTasks}
	}
	return set, nil
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
// (time runnable after the resource was ready: the DMV's view of CPU pressure).
//
// wait_time_ms includes signal_wait_time_ms, so resource time = byCategory -
// signalByCategory. Per-category signal time (not folded into WaitCPU) lets
// one bar show both halves; a mostly-signal category is queueing for CPU.
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
