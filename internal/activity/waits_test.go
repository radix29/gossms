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
			t.Errorf("categorize(%q) = %s, want %s", wait, WaitCategoryNames[got], WaitCategoryNames[want])
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

// Signal time stays against the waiting category (the bar splits
// resource/signal), not moved into CPU where it'd look like real CPU waits.
func TestWaitDeltasKeepSignalTimeAgainstItsCategory(t *testing.T) {
	prev := waits(map[string][3]int64{"PAGEIOLATCH_SH": {1000, 100, 5}})
	cur := waits(map[string][3]int64{"PAGEIOLATCH_SH": {3000, 500, 9}})

	byCat, signal, signalPct := waitDeltas(prev, cur, 2)

	// 2000ms of wait over 2s, 400ms of it signal.
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

// The resource half (total minus signal) must never go negative; excess signal
// is clamped.
func TestWaitDeltasClampSignalToTotalWait(t *testing.T) {
	prev := waits(map[string][3]int64{"LCK_M_X": {1000, 100, 1}})
	cur := waits(map[string][3]int64{"LCK_M_X": {1100, 900, 2}})

	byCat, signal, _ := waitDeltas(prev, cur, 1)
	if signal[WaitLock] != byCat[WaitLock] {
		t.Errorf("signal = %v against a total wait of %v; want it clamped to the total",
			signal[WaitLock], byCat[WaitLock])
	}
}

// A wait type absent from the previous sample has no delta.
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

// These waits accumulate on an idle server and one dwarfs the panel.
// PWAIT_EXTENSIBILITY_CLEANUP_TASK on SQL Server 2025 reports 300,000 ms in one
// 2s sample — hence family patterns.
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

// Real waits must survive; an over-broad pattern empties the panel.
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

// excludedByQuery mirrors the query's NOT IN / NOT LIKE clauses for one wait
// type.
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
