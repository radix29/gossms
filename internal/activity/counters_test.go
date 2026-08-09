package activity

import (
	"strings"
	"testing"
	"time"
)

func set(rows ...counterRow) counterSet {
	c := make(counterSet, len(rows))
	for _, r := range rows {
		c[counterKey{r.object, r.counter, r.instance}] = counterValue{value: r.value, typ: r.typ}
	}
	return c
}

type counterRow struct {
	object, counter, instance string
	value                     int64
	typ                       int
}

// Both reading queries drop every counter row belonging to a named instance
// — a database in the Databases object, a cache type in Plan Cache — because
// nothing reads one. Without this a 200-database server carried around a
// thousand rows per tick to use about forty.
func TestCounterQueriesFilterToTheInstancesRead(t *testing.T) {
	for name, q := range map[string]string{
		"activity": counterQuery,
		"tempdb":   tempdbCounterQuery,
	} {
		if !strings.Contains(q, "RTRIM(instance_name) IN ('', '_Total')") {
			t.Errorf("the %s counter query does not filter by instance:\n%s", name, q)
		}
	}
}

// The premise of that filter: every counter Derive reads lives either at the
// unnamed instance of a single-instance object or at the _Total aggregate,
// so a per-database row is never the one consulted. A wild per-database
// value is planted here to prove it is ignored rather than summed or
// preferred — if it ever were, the filter would be silently dropping the row
// the panel actually wanted.
func TestDeriveReadsOnlyUnnamedAndTotalInstances(t *testing.T) {
	start := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	rows := func(batches, transactions, logFlushes int64) []counterRow {
		return []counterRow{
			{objSQLStats, "Batch Requests/sec", "", batches, cntrPerSecond},
			{objDatabases, "Transactions/sec", totalInstance, transactions, cntrPerSecond},
			{objDatabases, "Log Flushes/sec", totalInstance, logFlushes, cntrPerSecond},
			// The per-database row the filter now drops. Present in both
			// snapshots so it would produce a rate if it were ever read.
			{objDatabases, "Transactions/sec", "AdventureWorks", transactions * 999, cntrPerSecond},
			// A ratio counter and its base, both at _Total.
			{objPlanCache, "Cache Hit Ratio", totalInstance, 90, cntrFraction},
			{objPlanCache, "Cache Hit Ratio Base", totalInstance, 100, cntrBase},
		}
	}
	prev := snapshotAt(start, set(rows(1000, 2000, 0)...))
	cur := snapshotAt(start.Add(2*time.Second), set(rows(1600, 4000, 200)...))

	s := Derive(prev, cur)

	for _, tc := range []struct {
		what string
		got  float64
		want float64
	}{
		{"batches", s.BatchesSec, 300},            // unnamed instance
		{"transactions", s.TransactionsSec, 1000}, // _Total, not the per-database row
		{"log flushes", s.LogFlushesSec, 100},     // _Total
		{"plan cache hit", s.PlanCacheHitPct, 90}, // _Total fraction over its base
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.what, tc.got, tc.want)
		}
	}
}

// A named instance publishes "MSSQL$INST:Buffer Manager" where a default
// instance publishes "SQLServer:Buffer Manager". Matching the whole string
// finds nothing on a named instance, and finds it silently — an empty
// dashboard rather than an error.
func TestObjectNameStripsTheInstancePrefix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"SQLServer:Buffer Manager", "Buffer Manager"},
		{"MSSQL$PROD01:Buffer Manager", "Buffer Manager"},
		{"MSSQL$PROD01:SQL Statistics ", "SQL Statistics"},
		{"Buffer Manager", "Buffer Manager"},
	} {
		if got := objectName(tc.in); got != tc.want {
			t.Errorf("objectName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Each cntr_type is read by its own rule. Read with the wrong rule these
// all produce plausible-looking numbers rather than errors, which is why
// the decode is driven by the type on the row.
func TestCounterValueDecodesByType(t *testing.T) {
	prev := set(
		counterRow{objSQLStats, "Batch Requests/sec", "", 1000, cntrPerSecond},
		counterRow{objBufferMgr, "Page life expectancy", "", 900, cntrRawGauge},
		counterRow{objDatabases, "Avg wait", totalInstance, 100, cntrAverageBulk},
		counterRow{objDatabases, "Avg wait base", totalInstance, 10, cntrBase},
	)
	cur := set(
		counterRow{objSQLStats, "Batch Requests/sec", "", 1200, cntrPerSecond},
		counterRow{objBufferMgr, "Page life expectancy", "", 1200, cntrRawGauge},
		counterRow{objBufferMgr, "Buffer cache hit ratio", "", 97, cntrFraction},
		counterRow{objBufferMgr, "Buffer cache hit ratio base", "", 100, cntrBase},
		counterRow{objDatabases, "Avg wait", totalInstance, 160, cntrAverageBulk},
		counterRow{objDatabases, "Avg wait base", totalInstance, 20, cntrBase},
	)

	// Cumulative: 200 requests over 4 seconds.
	if got := cur.value(prev, objSQLStats, "Batch Requests/sec", "", 4); got != 50 {
		t.Errorf("cumulative counter = %v, want 50/sec", got)
	}
	// Gauge: the reading is the value, not a delta.
	if got := cur.value(prev, objBufferMgr, "Page life expectancy", "", 4); got != 1200 {
		t.Errorf("gauge = %v, want the raw 1200", got)
	}
	// Fraction: ÷ base × 100.
	if got := cur.value(prev, objBufferMgr, "Buffer cache hit ratio", "", 4); got != 97 {
		t.Errorf("fraction counter = %v, want 97%%", got)
	}
	// Average bulk: delta ÷ base delta, independent of elapsed time.
	if got := cur.value(prev, objDatabases, "Avg wait", totalInstance, 4); got != 6 {
		t.Errorf("average-bulk counter = %v, want 60/10 = 6", got)
	}
}

// SQL Server spells the two base counters differently ("Buffer cache hit
// ratio base" but "Cache Hit Ratio Base"), so a case-sensitive match finds
// one of them and silently reports the other as zero.
func TestBaseCounterMatchIsCaseInsensitive(t *testing.T) {
	c := set(
		counterRow{objPlanCache, "Cache Hit Ratio", totalInstance, 88, cntrFraction},
		counterRow{objPlanCache, "Cache Hit Ratio Base", totalInstance, 100, cntrBase},
	)
	if got := c.value(nil, objPlanCache, "Cache Hit Ratio", totalInstance, 2); got != 88 {
		t.Errorf("plan cache hit ratio = %v, want 88%%", got)
	}
}

// Every counter resets to zero when the service restarts. Reading the
// difference straight through turns that into an enormous negative rate,
// or — worse, once the counter climbs again — a single enormous spike that
// rescales every chart it appears on.
func TestCounterValueIgnoresCountersThatWentBackwards(t *testing.T) {
	prev := set(counterRow{objSQLStats, "Batch Requests/sec", "", 5_000_000, cntrPerSecond})
	cur := set(counterRow{objSQLStats, "Batch Requests/sec", "", 40, cntrPerSecond})

	if got := cur.value(prev, objSQLStats, "Batch Requests/sec", "", 2); got != 0 {
		t.Errorf("rate across a counter reset = %v, want 0", got)
	}
}

func TestCounterValueHandlesMissingAndDegenerateInput(t *testing.T) {
	cur := set(counterRow{objSQLStats, "Batch Requests/sec", "", 100, cntrPerSecond})

	if got := cur.value(nil, objSQLStats, "Nothing Like This", "", 2); got != 0 {
		t.Errorf("missing counter = %v, want 0", got)
	}
	if got := cur.value(cur, objSQLStats, "Batch Requests/sec", "", 0); got != 0 {
		t.Errorf("rate over zero elapsed time = %v, want 0", got)
	}
	// A fraction counter with no base row is unreadable, not 100%.
	c := set(counterRow{objBufferMgr, "Buffer cache hit ratio", "", 97, cntrFraction})
	if got := c.value(nil, objBufferMgr, "Buffer cache hit ratio", "", 2); got != 0 {
		t.Errorf("fraction counter with no base = %v, want 0", got)
	}
}

// The query is built from a fixed list; a name that failed to quote would
// be a runtime syntax error rather than a compile failure.
func TestCounterQueryQuotesEveryName(t *testing.T) {
	if got := quotedList([]string{"a", "b's"}); got != "'a', 'b''s'" {
		t.Errorf("quotedList = %q, want the apostrophe doubled", got)
	}
	for _, name := range counterNames {
		if !strings.Contains(counterQuery, "'"+name+"'") {
			t.Errorf("counter %q is missing from the collection query", name)
		}
	}
}
