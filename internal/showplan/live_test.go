package showplan

import (
	"math"
	"testing"
)

// liveSnapshot is a real sys.dm_exec_query_profiles read (win10cli, 2025,
// 2026-10-06) of a DOP-4 cross join of a 3000-row #temp table with itself,
// grouped — ~4 s in. Parallel operators carry the all-zero thread-0 row;
// nested loops, its outer scan and its spool have closed on three workers
// but not the fourth.
var liveSnapshot = []ProfileRow{
	{NodeID: 0, ThreadID: 0, PhysicalOperator: "Parallelism", EstimateRowCount: 7, OpenTime: 2345797063},
	{NodeID: 1, ThreadID: 0, PhysicalOperator: "Hash Match"},
	{NodeID: 1, ThreadID: 1, PhysicalOperator: "Hash Match", EstimateRowCount: 1, OpenTime: 2345797064},
	{NodeID: 1, ThreadID: 2, PhysicalOperator: "Hash Match", EstimateRowCount: 1, OpenTime: 2345797064},
	{NodeID: 1, ThreadID: 3, PhysicalOperator: "Hash Match", EstimateRowCount: 1, OpenTime: 2345797064},
	{NodeID: 1, ThreadID: 4, PhysicalOperator: "Hash Match", EstimateRowCount: 5, OpenTime: 2345797064},
	{NodeID: 2, ThreadID: 0, PhysicalOperator: "Parallelism"},
	{NodeID: 2, ThreadID: 1, PhysicalOperator: "Parallelism", RowCount: 1208844, EstimateRowCount: 750, OpenTime: 2345797064, ElapsedMs: 3166, CPUMs: 102},
	{NodeID: 2, ThreadID: 2, PhysicalOperator: "Parallelism", RowCount: 2423686, EstimateRowCount: 750, OpenTime: 2345797064, ElapsedMs: 2864, CPUMs: 217},
	{NodeID: 2, ThreadID: 3, PhysicalOperator: "Parallelism", RowCount: 2422563, EstimateRowCount: 750, OpenTime: 2345797064, ElapsedMs: 2855, CPUMs: 214},
	{NodeID: 2, ThreadID: 4, PhysicalOperator: "Parallelism", RowCount: 2423753, EstimateRowCount: 750, OpenTime: 2345797064, ElapsedMs: 2833, CPUMs: 188},
	{NodeID: 3, ThreadID: 0, PhysicalOperator: "Nested Loops"},
	{NodeID: 3, ThreadID: 1, PhysicalOperator: "Nested Loops", RowCount: 2881898, EstimateRowCount: 750, OpenTime: 2345797065, ElapsedMs: 2012, CPUMs: 1805},
	{NodeID: 3, ThreadID: 2, PhysicalOperator: "Nested Loops", RowCount: 1866000, EstimateRowCount: 750, OpenTime: 2345797064, CloseTime: 2345800018, ElapsedMs: 1694, CPUMs: 1374},
	{NodeID: 3, ThreadID: 3, PhysicalOperator: "Nested Loops", RowCount: 1866000, EstimateRowCount: 750, OpenTime: 2345797065, CloseTime: 2345800056, ElapsedMs: 1643, CPUMs: 1346},
	{NodeID: 3, ThreadID: 4, PhysicalOperator: "Nested Loops", RowCount: 1865997, EstimateRowCount: 750, OpenTime: 2345797065, CloseTime: 2345800025, ElapsedMs: 1569, CPUMs: 1285},
	{NodeID: 5, ThreadID: 0, PhysicalOperator: "Table Scan"},
	{NodeID: 5, ThreadID: 1, PhysicalOperator: "Table Scan", RowCount: 961, EstimateRowCount: 750, OpenTime: 2345797065, ElapsedMs: 7, CPUMs: 6},
	{NodeID: 5, ThreadID: 2, PhysicalOperator: "Table Scan", RowCount: 622, EstimateRowCount: 750, OpenTime: 2345797064, CloseTime: 2345800018, ElapsedMs: 4, CPUMs: 3},
	{NodeID: 5, ThreadID: 3, PhysicalOperator: "Table Scan", RowCount: 622, EstimateRowCount: 750, OpenTime: 2345797065, CloseTime: 2345800056, ElapsedMs: 4, CPUMs: 4},
	{NodeID: 5, ThreadID: 4, PhysicalOperator: "Table Scan", RowCount: 622, EstimateRowCount: 750, OpenTime: 2345797065, CloseTime: 2345800025, ElapsedMs: 3, CPUMs: 3},
	{NodeID: 6, ThreadID: 0, PhysicalOperator: "Table Spool"},
	{NodeID: 6, ThreadID: 1, PhysicalOperator: "Table Spool", RowCount: 2881898, EstimateRowCount: 2250000, OpenTime: 2345800570, ElapsedMs: 876, CPUMs: 873},
	{NodeID: 6, ThreadID: 2, PhysicalOperator: "Table Spool", RowCount: 1866000, EstimateRowCount: 2250000, OpenTime: 2345800016, CloseTime: 2345800018, ElapsedMs: 692, CPUMs: 680},
	{NodeID: 6, ThreadID: 3, PhysicalOperator: "Table Spool", RowCount: 1866000, EstimateRowCount: 2250000, OpenTime: 2345800053, CloseTime: 2345800056, ElapsedMs: 649, CPUMs: 646},
	{NodeID: 6, ThreadID: 4, PhysicalOperator: "Table Spool", RowCount: 1866000, EstimateRowCount: 2250000, OpenTime: 2345800024, CloseTime: 2345800025, ElapsedMs: 632, CPUMs: 628},
	{NodeID: 7, ThreadID: 0, PhysicalOperator: "Table Scan"},
	{NodeID: 7, ThreadID: 1, PhysicalOperator: "Table Scan", RowCount: 3000, EstimateRowCount: 3000, OpenTime: 2345797065, ElapsedMs: 4, CPUMs: 4},
	{NodeID: 7, ThreadID: 2, PhysicalOperator: "Table Scan", RowCount: 3000, EstimateRowCount: 3000, OpenTime: 2345797065, ElapsedMs: 3, CPUMs: 3},
	{NodeID: 7, ThreadID: 3, PhysicalOperator: "Table Scan", RowCount: 3000, EstimateRowCount: 3000, OpenTime: 2345797066, ElapsedMs: 3, CPUMs: 3},
	{NodeID: 7, ThreadID: 4, PhysicalOperator: "Table Scan", RowCount: 3000, EstimateRowCount: 3000, OpenTime: 2345797065, ElapsedMs: 5, CPUMs: 5},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestMergeProfilesParallelThreads(t *testing.T) {
	m := MergeProfiles(liveSnapshot)
	if len(m) != 7 {
		t.Fatalf("got %d nodes, want 7", len(m))
	}
	c := m[2]
	want := LiveCounters{
		NodeID: 2, PhysicalOp: "Parallelism", State: LiveRunning, Threads: 4,
		Rows: 8478846, EstRows: 3000, ElapsedMS: 3166, CPUMS: 217,
	}
	if c != want {
		t.Errorf("node 2 = %+v\nwant     %+v", c, want)
	}
	// The phantom thread-0 row neither counts as a thread nor keeps a
	// parallel operator from being judged on its workers.
	if got := m[1]; got.Threads != 4 || got.State != LiveRunning || got.EstRows != 8 {
		t.Errorf("node 1 = %+v, want 4 running threads, est 8", got)
	}
	// Three of four workers closed: still running.
	if got := m[3].State; got != LiveRunning {
		t.Errorf("node 3 state = %v, want running", got)
	}
	// A serial operator (thread 0 only, opened).
	if got := m[0]; got.Threads != 1 || got.State != LiveRunning {
		t.Errorf("node 0 = %+v, want 1 running thread", got)
	}
}

func TestMergeProfilesStates(t *testing.T) {
	m := MergeProfiles([]ProfileRow{
		// Never opened, not even by a worker.
		{NodeID: 1, ThreadID: 0, EstimateRowCount: 100},
		// Parallel, every worker closed; thread 0 is the phantom.
		{NodeID: 2, ThreadID: 0},
		{NodeID: 2, ThreadID: 1, RowCount: 40, EstimateRowCount: 50, OpenTime: 10, CloseTime: 20},
		{NodeID: 2, ThreadID: 2, RowCount: 45, EstimateRowCount: 50, OpenTime: 10, CloseTime: 21},
	})
	if got := m[1].State; got != LiveNotStarted {
		t.Errorf("node 1 state = %v, want not started", got)
	}
	if got := m[2]; got.State != LiveDone || got.Rows != 85 || got.EstRows != 100 {
		t.Errorf("node 2 = %+v, want done, 85 of 100", got)
	}
	if len(MergeProfiles(nil)) != 0 {
		t.Error("no rows should merge to no nodes")
	}
}

func TestNodeProgress(t *testing.T) {
	m := MergeProfiles(liveSnapshot)
	for _, tc := range []struct {
		name string
		c    LiveCounters
		want float64
	}{
		{"partial", m[5], 2827.0 / 3000},
		{"at estimate, still open: capped", m[7], liveCap},
		{"over estimate: capped", m[2], liveCap},
		{"opened, no rows yet", m[1], 0},
		{"not started", LiveCounters{State: LiveNotStarted, EstRows: 10}, 0},
		{"running, no estimate", LiveCounters{State: LiveRunning, Rows: 5}, 0},
		{"done under estimate", LiveCounters{State: LiveDone, Rows: 3, EstRows: 10}, 1},
		{"done, zero rows", LiveCounters{State: LiveDone}, 1},
	} {
		if got := NodeProgress(tc.c); !near(got, tc.want) {
			t.Errorf("%s: NodeProgress = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !m[2].Over() || m[5].Over() || m[7].Over() {
		t.Errorf("Over: node 2 %v (want true), node 5 %v, node 7 %v (want false)",
			m[2].Over(), m[5].Over(), m[7].Over())
	}
}

func TestStatementProgress(t *testing.T) {
	// Rows so far over Σ max(rows, est): node 2 and 3 are over their
	// estimates, so they count as caught up; the spool (9M est) and the
	// partial scan keep the total short.
	got := StatementProgress(MergeProfiles(liveSnapshot))
	if want := 25453466.0 / 25973756; !near(got, want) {
		t.Errorf("snapshot: StatementProgress = %v, want %v", got, want)
	}

	for _, tc := range []struct {
		name string
		m    map[int]LiveCounters
		want float64
	}{
		{"empty", nil, 0},
		{"all done", map[int]LiveCounters{
			0: {State: LiveDone, Rows: 1, EstRows: 1},
			1: {State: LiveDone, Rows: 0, EstRows: 500},
		}, 1},
		{"nothing estimated yet", map[int]LiveCounters{
			0: {State: LiveRunning},
			1: {State: LiveNotStarted},
		}, 0},
		{"done node counts its actual rows, not its estimate", map[int]LiveCounters{
			0: {State: LiveDone, Rows: 10, EstRows: 1000},
			1: {State: LiveRunning, Rows: 10, EstRows: 30},
		}, 20.0 / 40},
		{"every running node over its estimate: capped", map[int]LiveCounters{
			0: {State: LiveRunning, Rows: 900, EstRows: 10},
			1: {State: LiveDone, Rows: 5, EstRows: 5},
		}, liveCap},
		{"not-started node weighs in by its estimate", map[int]LiveCounters{
			0: {State: LiveRunning, Rows: 25, EstRows: 50},
			1: {State: LiveNotStarted, EstRows: 50},
		}, 25.0 / 100},
	} {
		if got := StatementProgress(tc.m); !near(got, tc.want) {
			t.Errorf("%s: StatementProgress = %v, want %v", tc.name, got, tc.want)
		}
	}
}
