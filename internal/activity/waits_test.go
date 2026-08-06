package activity

import (
	"strings"
	"testing"
)

func TestCategorizeGroupsByWaitFamily(t *testing.T) {
	for wait, want := range map[string]WaitCategory{
		"LCK_M_X":             WaitLock,
		"LCK_M_SCH_M":         WaitLock,
		"PAGEIOLATCH_SH":      WaitDiskIO,
		"IO_COMPLETION":       WaitDiskIO,
		"PAGELATCH_EX":        WaitLatch,
		"LATCH_EX":            WaitLatch,
		"WRITELOG":            WaitLog,
		"LOGBUFFER":           WaitLog,
		"RESOURCE_SEMAPHORE":  WaitMemory,
		"CMEMTHREAD":          WaitMemory,
		"ASYNC_NETWORK_IO":    WaitNetwork,
		"SOS_SCHEDULER_YIELD": WaitCPU,
		"CXPACKET":            WaitCPU,
		"SOMETHING_BRAND_NEW": WaitOther,
	} {
		if got := categorize(wait); got != want {
			t.Errorf("categorize(%q) = %s, want %s", wait, got, want)
		}
	}
}

func waits(rows map[string][3]int64) waitSet {
	set := make(waitSet, len(rows))
	for name, v := range rows {
		set[name] = waitRow{waitMs: v[0], signalMs: v[1], tasks: v[2]}
	}
	return set
}

// Signal time is the part of a wait spent runnable once the resource was
// ready. It stays against the category that waited — the bar splits into a
// resource and a signal part — and is not moved into the CPU category,
// where it would be indistinguishable from real CPU waits.
func TestWaitDeltasKeepSignalTimeAgainstItsCategory(t *testing.T) {
	prev := waits(map[string][3]int64{"PAGEIOLATCH_SH": {1000, 100, 5}})
	cur := waits(map[string][3]int64{"PAGEIOLATCH_SH": {3000, 500, 9}})

	byCat, signal, signalPct := waitDeltas(prev, cur, 2)

	// 2000ms of wait over 2s, of which 400ms was signal.
	if got := byCat[WaitDiskIO]; got != 1000 {
		t.Errorf("disk I/O waits = %v ms/sec, want the whole wait (2000ms/2s)", got)
	}
	if got := signal[WaitDiskIO]; got != 200 {
		t.Errorf("disk I/O signal waits = %v ms/sec, want 400ms/2s", got)
	}
	if got := byCat[WaitCPU]; got != 0 {
		t.Errorf("CPU waits = %v ms/sec, want 0 — no CPU wait type moved", got)
	}
	if signalPct != 20 {
		t.Errorf("CPU %% of waits = %v, want 400/2000 = 20", signalPct)
	}
}

// wait_time_ms includes signal_wait_time_ms, so the resource half a caller
// computes by subtraction must never go negative — a wait type whose signal
// delta somehow exceeds its wait delta is clamped, not reported as a
// negative bar segment.
func TestWaitDeltasClampSignalToTotalWait(t *testing.T) {
	prev := waits(map[string][3]int64{"LCK_M_X": {1000, 100, 1}})
	cur := waits(map[string][3]int64{"LCK_M_X": {1100, 900, 2}})

	byCat, signal, _ := waitDeltas(prev, cur, 1)
	if signal[WaitLock] != byCat[WaitLock] {
		t.Errorf("signal = %v against a total wait of %v; want it clamped to the total",
			signal[WaitLock], byCat[WaitLock])
	}
}

// A wait type absent from the previous sample has no delta: its whole
// cumulative total is not two seconds' worth of waiting.
func TestWaitDeltasIgnoreUnpairedAndResetWaits(t *testing.T) {
	prev := waits(map[string][3]int64{"LCK_M_X": {9_000_000, 0, 1}})
	cur := waits(map[string][3]int64{
		"LCK_M_X":          {10, 0, 1},          // the server restarted
		"ASYNC_NETWORK_IO": {4_000_000, 0, 100}, // first seen this sample
	})

	byCat, _, _ := waitDeltas(prev, cur, 2)
	if byCat[WaitLock] != 0 {
		t.Errorf("lock waits across a restart = %v, want 0", byCat[WaitLock])
	}
	if byCat[WaitNetwork] != 0 {
		t.Errorf("a newly appeared wait type contributed %v, want 0", byCat[WaitNetwork])
	}
}

func TestWaitDeltasWithNoElapsedTime(t *testing.T) {
	cur := waits(map[string][3]int64{"WRITELOG": {100, 0, 1}})
	if byCat, _, pct := waitDeltas(nil, cur, 0); byCat[WaitLog] != 0 || pct != 0 {
		t.Error("a zero-length interval produced wait rates")
	}
}

// The exclusions are what keep the chart readable: these waits accumulate
// constantly on a completely idle server, and one of them dwarfs every real
// wait on the panel.
//
// PWAIT_EXTENSIBILITY_CLEANUP_TASK is the one that proved it: on SQL Server
// 2025 it sleeps for five minutes and reports all 300,000 ms of it against
// a single two-second sample — 150,000 ms of wait per second, against real
// waits of a few hundred. It flattened the whole waits panel to a single
// column, live, and it is why families are excluded by pattern rather than
// one name at a time.
func TestBackgroundWaitsAreExcludedFromTheQuery(t *testing.T) {
	for _, w := range []string{
		"PWAIT_EXTENSIBILITY_CLEANUP_TASK", "LAZYWRITER_SLEEP", "XE_TIMER_EVENT",
		"WAITFOR", "SLEEP_TASK", "QDS_ASYNC_QUEUE", "HADR_WORK_QUEUE",
		"BROKER_TO_FLUSH", "SQLTRACE_WAIT_ENTRIES", "CLR_AUTO_EVENT",
		"WAIT_XTP_HOST_WAIT", "CHECKPOINT_QUEUE",
	} {
		if !excludedByQuery(w) {
			t.Errorf("%s is not excluded; it accumulates constantly on an idle server", w)
		}
	}
}

// Real waits must survive the exclusions — an over-broad family pattern
// would empty the panel just as effectively as a missing one floods it.
func TestRealWaitsSurviveTheExclusions(t *testing.T) {
	for _, w := range []string{
		"LCK_M_X", "PAGEIOLATCH_SH", "WRITELOG", "SOS_SCHEDULER_YIELD",
		"CXPACKET", "RESOURCE_SEMAPHORE", "ASYNC_NETWORK_IO", "THREADPOOL",
	} {
		if excludedByQuery(w) {
			t.Errorf("%s is excluded, but it is a wait worth showing", w)
		}
	}
}

// excludedByQuery mirrors what the collection query's NOT IN and NOT LIKE
// clauses do to one wait type.
func excludedByQuery(name string) bool {
	for _, b := range benignWaits {
		if b == name {
			return true
		}
	}
	for _, family := range benignFamilies {
		prefix := strings.TrimSuffix(strings.ReplaceAll(family, "\\_", "_"), "%")
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
