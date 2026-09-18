package activity

import (
	"context"
	"database/sql"
	"strings"
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

// cntr_type values (WMI PERF_* types). Raw cntr_value is wrong for most:
// cumulative counters only grow, fractions need their base.
const (
	cntrRawGauge     = 65792      // PERF_COUNTER_LARGE_RAWCOUNT: use as-is
	cntrPerSecond    = 272696576  // PERF_COUNTER_BULK_COUNT: delta ÷ elapsed
	cntrPerSecondAlt = 272696320  // PERF_COUNTER_COUNTER: delta ÷ elapsed
	cntrFraction     = 537003264  // PERF_LARGE_RAW_FRACTION: ÷ base × 100
	cntrAverageBulk  = 1073874176 // PERF_AVERAGE_BULK: delta ÷ base delta
	cntrBase         = 1073939712 // PERF_LARGE_RAW_BASE: the divisor for the two above
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
	typ   int
}

// counterSet is one sample of every counter collected.
type counterSet map[counterKey]counterValue

// counterNames are the counters collected each tick; filtering in SQL keeps a
// few dozen of the thousands of rows.
//
// Logins/sec, Logouts/sec, Full Scans/sec, Page Splits/sec, Workfiles
// Created/sec, Worktables Created/sec, Page lookups/sec, Readahead pages/sec
// and Memory Grants Outstanding aren't shown yet; they cost nothing extra and
// are for increment 2.
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

// counterQuery reads the counters named above.
var counterQuery = counterQueryFor(counterNames)

// counterQueryFor builds the query for a fixed list of names; the IN list is
// built, not parameterised, because the names are constants.
//
// The instance filter matters: Databases publishes a row per database and Plan
// Cache one per cache type, but readers only use the unnamed instance or
// "_Total" (see Derive, deriveTempDB). Unfiltered, a 200-database server
// returns ~1000 rows per tick to use ~40. RTRIM because SQL Server blank-pads
// the column.
func counterQueryFor(names []string) string {
	return "SELECT RTRIM(object_name), RTRIM(counter_name), RTRIM(instance_name), cntr_value, cntr_type " +
		"FROM sys.dm_os_performance_counters " +
		"WHERE RTRIM(counter_name) IN (" + quotedList(names) + ") " +
		"AND RTRIM(instance_name) IN ('', '" + totalInstance + "')"
}

// quotedList renders names as a SQL string list, doubling apostrophes so a
// future name can't become a runtime syntax error.
func quotedList(names []string) string {
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("'")
		b.WriteString(strings.ReplaceAll(n, "'", "''"))
		b.WriteString("'")
	}
	return b.String()
}

// collectCounters reads one sample of every counter in counterNames.
func collectCounters(ctx context.Context, db *sql.DB) (counterSet, error) {
	return collectCounterSet(ctx, db, counterQuery)
}

// collectCounterSet reads one sample of the counters query selects.
func collectCounterSet(ctx context.Context, db *sql.DB, query string) (counterSet, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := make(counterSet)
	for rows.Next() {
		var object, counter, instance string
		var value int64
		var typ int
		if err := rows.Scan(&object, &counter, &instance, &value, &typ); err != nil {
			return nil, err
		}
		set[counterKey{object: objectName(object), counter: counter, instance: instance}] = counterValue{value: value, typ: typ}
	}
	return set, rows.Err()
}

// objectName strips the instance prefix: a default instance publishes
// "SQLServer:Buffer Manager", a named one "MSSQL$INST:Buffer Manager". Matching
// the full string silently finds nothing on a named instance.
func objectName(object string) string {
	if i := strings.Index(object, ":"); i >= 0 {
		return strings.TrimSpace(object[i+1:])
	}
	return strings.TrimSpace(object)
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
		// cur/base, never a delta, for both readers of this arm — measured
		// 2026-09-18 against SQL Server 17.0.1135.8, and neither one is the
		// cumulative-since-startup average the formula looks like:
		//
		// Buffer Manager's "Buffer cache hit ratio" is a window over recent page
		// lookups. Twelve readings three seconds apart across a DBCC
		// DROPCLEANBUFFERS and a 1.5 GB scan gave bases of 104, 3728, 33057,
		// 29511, 87411, 21375, 126, 218, so differencing two of them divides one
		// window by the change in another window's size — 100.09% across the
		// 29511 → 87411 pair.
		//
		// Plan Cache's "Cache Hit Ratio" at _Total is the sum of its five cache
		// stores' own rows, each of which restarts at zero when that store is
		// trimmed. So the sum steps backwards by an arbitrary mix of hits and
		// lookups under nothing worse than ordinary churn (-1229 value against
		// -1717 base, observed), and a delta reads 104% and 672% when only some
		// of the stores restart. Its average decays fast enough to be live
		// anyway: 90.68% to 54.24% across one burst of ad-hoc batches, because
		// the same trimming keeps the base small.
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
func (c counterSet) base(object, counter, instance string) (int64, bool) {
	want := strings.ToLower(counter + " base")
	for k, v := range c {
		if v.typ != cntrBase || k.object != object || k.instance != instance {
			continue
		}
		if strings.ToLower(k.counter) == want {
			return v.value, true
		}
	}
	return 0, false
}
