package activity

import (
	"context"
	"database/sql"
	"strings"
)

// Counter object and counter names, as they appear in
// sys.dm_os_performance_counters once the instance prefix is stripped.
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

// totalInstance is the aggregate instance row the Databases and Plan Cache
// objects publish alongside their per-database/per-cache rows.
const totalInstance = "_Total"

// cntr_type values, from WMI's PERF_* counter types. Reading cntr_value
// without decoding these is the standard way to get numbers that look
// plausible and are wrong: a cumulative counter read raw is a number that
// only ever goes up, and a fraction counter read raw is meaningless without
// its base.
const (
	cntrRawGauge     = 65792      // PERF_COUNTER_LARGE_RAWCOUNT: use as-is
	cntrPerSecond    = 272696576  // PERF_COUNTER_BULK_COUNT: delta ÷ elapsed
	cntrPerSecondAlt = 272696320  // PERF_COUNTER_COUNTER: delta ÷ elapsed
	cntrFraction     = 537003264  // PERF_LARGE_RAW_FRACTION: ÷ base × 100
	cntrAverageBulk  = 1073874176 // PERF_AVERAGE_BULK: delta ÷ base delta
	cntrBase         = 1073939712 // PERF_LARGE_RAW_BASE: the divisor for the two above
)

// counterKey identifies one counter row: the object name with its instance
// prefix stripped, the counter name, and the counter's own instance (a
// database name, "_Total", or empty).
type counterKey struct {
	object   string
	counter  string
	instance string
}

// counterValue is one row's reading and the type that says how to read it.
type counterValue struct {
	value int64
	typ   int
}

// counterSet is one sample of every counter collected.
type counterSet map[counterKey]counterValue

// counterNames are the counters collected each tick. Filtering in SQL keeps
// the result to a few dozen rows rather than the couple of thousand
// sys.dm_os_performance_counters holds.
//
// Nine of these are not surfaced by any panel yet — Logins/sec, Logouts/sec,
// Full Scans/sec, Page Splits/sec, Workfiles Created/sec, Worktables
// Created/sec, Page lookups/sec, Readahead pages/sec, and Memory Grants
// Outstanding. They are kept deliberately: the whole set costs one query
// however long it is, and the panels that read them are increment 2.
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

// counterQueryFor builds the reading query for a fixed list of counter
// names. The IN list is built rather than parameterised because these are
// compile-time constants, not user input.
func counterQueryFor(names []string) string {
	return "SELECT RTRIM(object_name), RTRIM(counter_name), RTRIM(instance_name), cntr_value, cntr_type " +
		"FROM sys.dm_os_performance_counters WHERE RTRIM(counter_name) IN (" + quotedList(names) + ")"
}

// quotedList renders names as a SQL string list. The names are constants in
// this file, so the only quoting hazard is a stray apostrophe someone adds
// later — doubled here so it can never become a syntax error at runtime.
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

// collectCounterSet reads one sample of whatever counters query selects.
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

// objectName strips the instance prefix from a counter's object name. A
// default instance publishes "SQLServer:Buffer Manager"; a named one
// publishes "MSSQL$INST:Buffer Manager". Matching the whole string against
// "SQLServer:..." therefore finds nothing at all on a named instance — and
// finds it silently, as an empty dashboard rather than an error.
func objectName(object string) string {
	if i := strings.Index(object, ":"); i >= 0 {
		return strings.TrimSpace(object[i+1:])
	}
	return strings.TrimSpace(object)
}

// value decodes one counter row into the number to display, choosing how
// to read it from its own cntr_type rather than from the caller's
// expectations — a counter read with the wrong rule produces a
// plausible-looking wrong number, never an error. prev is the previous
// sample and elapsed the seconds between the two; both are ignored for
// counter types that don't need them.
//
// A counter missing from either sample, a non-positive elapsed time, or a
// counter that went backwards (a service restart resets them all to zero)
// reads as 0 rather than as a spike.
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
		// cntrRawGauge and anything new: the reading is the value.
		return float64(cur.value)
	}
}

// base finds the PERF_LARGE_RAW_BASE row belonging to counter. The base row
// carries the same object and instance and the counter's own name with
// " base" appended — which SQL Server spells inconsistently ("Buffer cache
// hit ratio base" but "Cache Hit Ratio Base"), so it is matched
// case-insensitively.
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
