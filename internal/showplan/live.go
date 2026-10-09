package showplan

import "math"

// ============================================================
// Live Query Statistics counters
// ============================================================
//
// sys.dm_exec_query_profiles reports a running statement one row per operator
// per thread; Live Query Statistics shows one figure per operator. MergeProfiles
// folds the rows into node-keyed LiveCounters, and NodeProgress /
// StatementProgress turn those into percentages. The package stays free of
// database dependencies, so ProfileRow mirrors only the gosmo.QueryProfile
// fields the merge reads.
//
// Parallel plans (observed on 2025, DOP 4): an operator in a parallel zone has
// a row per worker thread 1..DOP *and* a thread-0 row that stays all zeros
// (never opened, no estimate). Treating that phantom as an unstarted thread
// would keep every parallel operator "running" forever, so threads that never
// opened are ignored once any thread has. estimate_row_count is split across
// threads (750 each of a 3000-row scan) and already covers every execution of
// an inner-side operator (a spool under nested loops estimated 750 × 3000), so
// the per-node estimate is a plain sum, like the rows.
//
// Lightweight profiling (on by default from 2019; what Activity Monitor's Show
// Live Execution Plan watches) counts rows only: every time column stays 0,
// open and close included (observed on 2025). A thread that has produced rows
// counts as open without an open time; it is never seen to close, so the
// operator stays running, and LiveCounters.Timed is false so the zero times
// are not drawn as measured.

// ProfileRow is one row of sys.dm_exec_query_profiles: one operator on one
// thread. The caller filters to the statement shown (gosmo's PlanHandle and
// statement offsets) before merging: the merge keys on NodeID alone, and node
// ids repeat across statements.
type ProfileRow struct {
	NodeID           int
	ThreadID         int
	PhysicalOperator string

	RowCount         int64
	EstimateRowCount int64
	RebindCount      int64
	RewindCount      int64
	EndOfScanCount   int64

	// Server millisecond ticks, 0 while the event has not happened.
	OpenTime  int64
	CloseTime int64

	ElapsedMs int64
	CPUMs     int64

	ScanCount     int64
	LogicalReads  int64
	PhysicalReads int64
	ReadAheads    int64
}

// LiveState is where an operator is in its run.
type LiveState int

const (
	LiveNotStarted LiveState = iota // no thread has opened it yet
	LiveRunning                     // opened, and some opened thread has not closed
	LiveDone                        // every thread that opened it has closed
)

func (s LiveState) String() string {
	switch s {
	case LiveRunning:
		return "running"
	case LiveDone:
		return "done"
	default:
		return "not started"
	}
}

// LiveCounters is one operator's counters so far, merged over its threads.
// Rows, estimates, reads and CPUMS are summed and ElapsedMS comes from the
// slowest thread, as Runtime's do, so figures don't jump when the actual plan
// replaces the live view.
type LiveCounters struct {
	NodeID     int
	PhysicalOp string
	State      LiveState
	Threads    int // threads that have opened the operator

	// Timed is set when some thread has an open time. Lightweight profiling
	// has none, so ElapsedMS and CPUMS are then unmeasured, not a measured 0.
	Timed bool

	Rows       int64
	EstRows    int64
	Rebinds    int64
	Rewinds    int64
	EndOfScans int64

	ElapsedMS int64
	CPUMS     int64

	ScanCount     int64
	LogicalReads  int64
	PhysicalReads int64
	ReadAheads    int64
}

// Over reports whether the operator has produced more rows than estimated —
// SSMS's "n of m (over)", where the percentage stops meaning anything.
func (c LiveCounters) Over() bool { return c.Rows > c.EstRows }

// MergeProfiles folds per-thread profile rows into one LiveCounters per
// NodeID.
func MergeProfiles(rows []ProfileRow) map[int]LiveCounters {
	out := make(map[int]LiveCounters)
	// open/closed count the threads that opened, and of those that closed,
	// per node; the state is settled once every row is in.
	type tally struct{ open, closed int }
	tallies := make(map[int]tally)
	for _, r := range rows {
		c := out[r.NodeID]
		c.NodeID = r.NodeID
		if c.PhysicalOp == "" {
			c.PhysicalOp = r.PhysicalOperator
		}
		c.Rows += r.RowCount
		c.EstRows += r.EstimateRowCount
		c.Rebinds += r.RebindCount
		c.Rewinds += r.RewindCount
		c.EndOfScans += r.EndOfScanCount
		c.ElapsedMS = max(c.ElapsedMS, r.ElapsedMs)
		c.CPUMS += r.CPUMs
		c.ScanCount += r.ScanCount
		c.LogicalReads += r.LogicalReads
		c.PhysicalReads += r.PhysicalReads
		c.ReadAheads += r.ReadAheads
		if r.OpenTime != 0 {
			c.Timed = true
		}
		if r.OpenTime != 0 || r.RowCount > 0 {
			t := tallies[r.NodeID]
			t.open++
			if r.CloseTime != 0 {
				t.closed++
			}
			tallies[r.NodeID] = t
		}
		out[r.NodeID] = c
	}
	for id, c := range out {
		t := tallies[id]
		c.Threads = t.open
		switch {
		case t.open == 0:
			c.State = LiveNotStarted
		case t.closed == t.open:
			c.State = LiveDone
		default:
			c.State = LiveRunning
		}
		out[id] = c
	}
	return out
}

// FillPlanEstimates gives each operator in m that the DMV reported no estimate
// for the plan's own over all executions: EstimateRows × (1 + EstimateRebinds +
// EstimateRewinds), the figure estimate_row_count otherwise carries. The DMV
// leaves it 0 at times (observed on 2025 under lightweight profiling, on a
// re-run of a cached plan). The plan is the in-flight single statement; its
// first statement with an operator tree is used.
func FillPlanEstimates(p *Plan, m map[int]LiveCounters) {
	if p == nil || len(m) == 0 {
		return
	}
	for _, st := range p.Statements {
		if st.Root == nil {
			continue
		}
		for _, n := range st.Nodes() {
			c, ok := m[n.ID]
			if !ok || c.EstRows != 0 {
				continue
			}
			c.EstRows = int64(math.Round(n.EstRows * (1 + n.EstRebinds + n.EstRewinds)))
			m[n.ID] = c
		}
		return
	}
}

// liveCap is the most a still-running operator or statement shows: SSMS never
// claims 100 % until the work has actually closed.
const liveCap = 0.99

// NodeProgress is the operator's completion in [0, 1]: 1 once done, 0 before
// it starts, otherwise rows over the estimate capped at 99 %. A running
// operator with no estimate reports 0.
func NodeProgress(c LiveCounters) float64 {
	switch {
	case c.State == LiveDone:
		return 1
	case c.State == LiveNotStarted || c.EstRows <= 0:
		return 0
	}
	return min(float64(c.Rows)/float64(c.EstRows), liveCap)
}

// StatementProgress is the statement's overall completion in [0, 1], row-
// weighted across operators: each contributes its rows so far against the
// larger of its estimate and its rows (a finished operator, its actual rows
// alone). Capped at 99 % until every operator is done. An operator past its
// estimate thus counts as caught up; nothing says how much more it will produce.
func StatementProgress(m map[int]LiveCounters) float64 {
	if len(m) == 0 {
		return 0
	}
	var done, total float64
	allDone := true
	for _, c := range m {
		rows := float64(c.Rows)
		if c.State == LiveDone {
			done += rows
			total += rows
			continue
		}
		allDone = false
		done += rows
		total += max(rows, float64(c.EstRows))
	}
	if allDone {
		return 1
	}
	if total <= 0 {
		return 0
	}
	return min(done/total, liveCap)
}
