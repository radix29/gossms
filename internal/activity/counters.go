package activity

import (
	"context"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// Counter object and counter names as in sys.dm_os_performance_counters,
// instance prefix stripped.
const (
	objSQLStats     = "SQL Statistics"
	objGeneralStats = "General Statistics"
	objAccessMeth   = "Access Methods"
	objBufferMgr    = "Buffer Manager"
	objMemoryMgr    = "Memory Manager"
	objDatabases    = "Databases"
	objPlanCache    = "Plan Cache"
	objTransactions = "Transactions"
)

// totalInstance is the aggregate row the Databases and Plan Cache objects
// publish beside their per-instance rows.
const totalInstance = "_Total"

// cntr_type values, by gosmo's names for them. Raw cntr_value is wrong for
// most: cumulative counters only grow, fractions need their base.
const (
	cntrRawGauge     = gosmo.CounterRawCount    // use as-is
	cntrPerSecond    = gosmo.CounterBulkCount   // delta ÷ elapsed
	cntrPerSecondAlt = gosmo.CounterCounter     // delta ÷ elapsed
	cntrFraction     = gosmo.CounterRawFraction // ÷ base × 100
	cntrAverageBulk  = gosmo.CounterAverageBulk // delta ÷ base delta
	cntrBase         = gosmo.CounterRawBase     // the divisor for the two above
)

// counterKey identifies a counter row: object name without instance prefix,
// counter name, and the counter's instance (database name, "_Total", or empty).
type counterKey struct {
	object   string
	counter  string
	instance string
}

// counterValue is one row's reading and its cntr_type.
type counterValue struct {
	value int64
	typ   gosmo.CounterType
}

// counterSet is one sample of every counter collected.
type counterSet map[counterKey]counterValue

// counterNames are the counters collected each tick; filtering in SQL keeps a
// few dozen of the thousands of rows.
//
// Logins/sec, Logouts/sec, Full Scans/sec, Page Splits/sec, Workfiles
// Created/sec, Worktables Created/sec, Page lookups/sec, Readahead pages/sec
// and Memory Grants Outstanding aren't shown yet (increment 2).
var counterNames = []string{
	// SQL Statistics
	"Batch Requests/sec", "SQL Compilations/sec", "SQL Re-Compilations/sec",
	// General Statistics
	"User Connections", "Logins/sec", "Logouts/sec", "Processes blocked",
	// Access Methods
	"Index Searches/sec", "Forwarded Records/sec", "Full Scans/sec",
	"Page Splits/sec", "Workfiles Created/sec", "Worktables Created/sec",
	// Buffer Manager
	"Page life expectancy", "Buffer cache hit ratio", "Buffer cache hit ratio base",
	"Page reads/sec", "Page writes/sec", "Page lookups/sec",
	"Readahead pages/sec", "Lazy writes/sec", "Checkpoint pages/sec",
	// Memory Manager
	"Total Server Memory (KB)", "Target Server Memory (KB)",
	"Memory Grants Pending", "Memory Grants Outstanding",
	// Databases (_Total)
	"Transactions/sec", "Log Flushes/sec", "Log Bytes Flushed/sec",
	"Log Flush Waits/sec", "Backup/Restore Throughput/sec",
	// Plan Cache (_Total)
	"Cache Hit Ratio", "Cache Hit Ratio Base",
}

// counterInstances are the instance names read. Databases publishes a row
// per database and Plan Cache one per cache type, but Derive and deriveTempDB
// use only the unnamed instance or "_Total".
var counterInstances = []string{"", totalInstance}

// collectCounterSet reads one sample of the named counters. gosmo strips the
// object name's instance prefix ("SQLServer:" on a default instance,
// "MSSQL$INST:" on a named one), so the keys match on either.
func collectCounterSet(ctx context.Context, src Source, names []string) (counterSet, error) {
	rows, err := src.PerformanceCounters(ctx, names, counterInstances)
	if err != nil {
		return nil, err
	}
	set := make(counterSet, len(rows))
	for _, r := range rows {
		set[counterKey{object: r.Object, counter: r.Counter, instance: r.Instance}] = counterValue{value: r.Value, typ: r.Type}
	}
	return set, nil
}

// value decodes a counter row by its own cntr_type (the wrong rule gives a
// plausible wrong number). prev and elapsed (seconds) are used only by types
// that need them.
//
// A counter missing from either sample, non-positive elapsed, or a counter that
// went backwards (restart resets to zero) reads 0, not a spike.
func (c counterSet) value(prev counterSet, object, counter, instance string, elapsed float64) float64 {
	key := counterKey{object, counter, instance}
	cur, ok := c[key]
	if !ok {
		return 0
	}
	switch cur.typ {
	case cntrPerSecond, cntrPerSecondAlt:
		old, ok := prev[key]
		if !ok || elapsed <= 0 {
			return 0
		}
		delta := cur.value - old.value
		if delta < 0 {
			return 0
		}
		return float64(delta) / elapsed

	case cntrFraction:
		// cur/base, never a delta, for both readers of this arm (measured on
		// SQL Server 17.0.1135.8); neither is the cumulative-since-startup
		// average the formula looks like:
		//
		// Buffer Manager's "Buffer cache hit ratio" is a window over recent
		// page lookups. Bases across a DBCC DROPCLEANBUFFERS and a 1.5 GB scan
		// read 104, 3728, 33057, 29511, 87411, 21375, 126, 218, so a delta
		// divides one window by the change in another's size (100.09% across
		// 29511 -> 87411).
		//
		// Plan Cache's "Cache Hit Ratio" at _Total sums five cache stores' rows,
		// each restarting at zero when its store is trimmed. The sum steps
		// backwards by an arbitrary mix of hits and lookups under ordinary
		// churn (-1229 value against -1717 base), and a delta reads 104% and
		// 672% when only some stores restart. The average decays fast enough
		// to be live anyway (90.68% to 54.24% over one ad-hoc burst).
		//
		// docs/decisions.md § Activity Monitor has the full readings.
		base, ok := c.base(object, counter, instance)
		if !ok || base == 0 {
			return 0
		}
		return float64(cur.value) / float64(base) * 100

	case cntrAverageBulk:
		old, ok := prev[key]
		if !ok {
			return 0
		}
		base, ok := c.base(object, counter, instance)
		oldBase, okOld := prev.base(object, counter, instance)
		if !ok || !okOld || base-oldBase <= 0 {
			return 0
		}
		delta := cur.value - old.value
		if delta < 0 {
			return 0
		}
		return float64(delta) / float64(base-oldBase)

	default:
		// cntrRawGauge and unknown types: the reading is the value.
		return float64(cur.value)
	}
}

// base finds counter's PERF_LARGE_RAW_BASE row: same object and instance, name
// + " base". Matched case-insensitively because SQL Server spells it
// inconsistently ("Buffer cache hit ratio base", "Cache Hit Ratio Base").
//
// A trailing " (ms)" is dropped from some base names and kept in others:
// "Average Wait Time (ms)" has "Average Wait Time Base"; "Avg Disk Read IO (ms)"
// has "Avg Disk Read IO (ms) Base". Either is accepted, or the first reads 0.
func (c counterSet) base(object, counter, instance string) (int64, bool) {
	want := strings.ToLower(counter + " base")
	unitless := strings.ToLower(strings.TrimSuffix(counter, " (ms)") + " base")
	for k, v := range c {
		if v.typ != cntrBase || k.object != object || k.instance != instance {
			continue
		}
		if name := strings.ToLower(k.counter); name == want || name == unitless {
			return v.value, true
		}
	}
	return 0, false
}
