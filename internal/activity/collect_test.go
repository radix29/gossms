package activity

import (
	"context"
	"errors"
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// snapshotSource answers every Collect reading, with values distinctive
// enough that a field mapped from the wrong one shows as a wrong number.
func snapshotSource() *fakeSource {
	return &fakeSource{
		counters: []gosmo.PerformanceCounter{
			{Object: "SQL Statistics", Counter: "Batch Requests/sec", Value: 4200, Type: gosmo.CounterBulkCount},
			// Neither the unnamed instance nor _Total: the read must not ask for it.
			{Object: "Databases", Counter: "Transactions/sec", Instance: "AppDB", Value: 1, Type: gosmo.CounterBulkCount},
		},
		waits: []gosmo.WaitStat{
			{WaitType: "PAGEIOLATCH_SH", WaitTimeMs: 900, SignalWaitTimeMs: 30, WaitingTasks: 12},
			{WaitType: "LCK_M_X", WaitTimeMs: 400, SignalWaitTimeMs: 10, WaitingTasks: 3},
			{WaitType: "LAZYWRITER_SLEEP", WaitTimeMs: 999999},
		},
		files: []gosmo.FileIOStat{
			{DatabaseID: 5, FileID: 1, Database: "AppDB",
				Reads: 11, BytesRead: 12, IOStallReadMs: 13, Writes: 14, BytesWritten: 15, IOStallWriteMs: 16},
			{DatabaseID: 5, FileID: 2, IsLog: true,
				Reads: 21, BytesRead: 22, IOStallReadMs: 23, Writes: 24, BytesWritten: 25, IOStallWriteMs: 26},
		},
		clerks: []gosmo.MemoryClerk{
			{Type: "MEMORYCLERK_SQLBUFFERPOOL", MB: 1024},
			{Type: "SOMETHING_NEW_IN_A_LATER_RELEASE", MB: 7},
		},
		scheds: []gosmo.Scheduler{
			{CPUID: 0, IsOnline: true, RunnableTasks: 1, CurrentTasks: 10, ActiveWorkers: 15, WorkQueue: 1, LoadFactor: 12},
			{CPUID: 1, IsOnline: true, RunnableTasks: 1, CurrentTasks: 20, ActiveWorkers: 25, LoadFactor: 34},
			// Outside the affinity mask: runs no user work, so counts for nothing.
			{CPUID: 2, IsOnline: false, RunnableTasks: 50, CurrentTasks: 50, ActiveWorkers: 50, WorkQueue: 50, LoadFactor: 99},
		},
		requests: gosmo.RequestActivity{UserSessions: 17, ActiveRequests: 5, RunnableRequests: 2, SuspendedRequests: 1, BlockedRequests: 3},
		cpu:      gosmo.HostCPU{SQLServerPercent: 63, OtherPercent: 11},
	}
}

// Drives all of Collect against a fake source. A field mapped from the wrong
// reading gives a plausible number and no error, so each is checked against a
// value only its source carries.
func TestCollectReadsEveryPartOfASnapshot(t *testing.T) {
	src := snapshotSource()

	snap, err := Collect(context.Background(), src)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.At.IsZero() {
		t.Error("snapshot has no time: nothing can be a rate against it")
	}

	got := snap.Counters[counterKey{object: "SQL Statistics", counter: "Batch Requests/sec", instance: ""}]
	if got.value != 4200 || got.typ != gosmo.CounterBulkCount {
		t.Errorf("Batch Requests/sec = %+v, want 4200 of type bulk count", got)
	}
	// The read asks for just the counters Derive uses, at the unnamed instance
	// and _Total: a per-database row is ~1000 rows a tick on a big server.
	if !slices.Equal(src.counterNames, counterNames) || !slices.Equal(src.counterInstances, []string{"", "_Total"}) {
		t.Errorf("counter filter = %q at %q", src.counterNames, src.counterInstances)
	}
	if len(snap.Counters) != 1 {
		t.Errorf("counters = %v, want only the Batch Requests/sec row", snap.Counters)
	}

	if w, ok := snap.Waits["PAGEIOLATCH_SH"]; !ok {
		t.Error("PAGEIOLATCH_SH missing from the wait set")
	} else if w.waitMs != 900 || w.signalMs != 30 || w.tasks != 12 {
		t.Errorf("PAGEIOLATCH_SH = %+v, want waitMs 900, signalMs 30, tasks 12", w)
	}
	if _, ok := snap.Waits["LAZYWRITER_SLEEP"]; ok {
		t.Error("a background wait reached the wait set; its hours of sleep would flatten the chart")
	}

	data, ok := snap.Files[fileKey{dbID: 5, fileID: 1}]
	if !ok {
		t.Fatal("the data file is missing from the file set")
	}
	if data.database != "AppDB" || data.isLog {
		t.Errorf("data file = %q isLog=%v, want AppDB, false", data.database, data.isLog)
	}
	if data.reads != 11 || data.bytesRead != 12 || data.stallRead != 13 ||
		data.writes != 14 || data.bytesWrit != 15 || data.stallWrit != 16 {
		t.Errorf("data file counters = %+v, want reads 11, bytesRead 12, stallRead 13, writes 14, bytesWrit 15, stallWrit 16", data)
	}
	// A database the connection can't see has no name; its I/O still counts,
	// under a usable one.
	logFile, ok := snap.Files[fileKey{dbID: 5, fileID: 2}]
	if !ok {
		t.Fatal("the log file is missing from the file set")
	}
	if logFile.database != "(unknown)" || !logFile.isLog {
		t.Errorf("log file = %q isLog=%v, want (unknown), true", logFile.database, logFile.isLog)
	}

	// An unknown clerk lands in Other.
	mem := map[string]float64{}
	for _, c := range snap.Memory {
		mem[c.Name] = c.MB
	}
	if mem[memBuffer] != 1024 {
		t.Errorf("%s = %v MB, want 1024", memBuffer, mem[memBuffer])
	}
	if mem[memOther] != 7 {
		t.Errorf("%s = %v MB, want 7 — an unknown clerk must still be counted", memOther, mem[memOther])
	}

	if snap.Sched != (SchedStats{Schedulers: 2, RunnableTasks: 2, CurrentTasks: 30, ActiveWorkers: 40, WorkQueue: 1}) {
		t.Errorf("SchedStats = %+v, want the two online schedulers summed", snap.Sched)
	}
	if snap.Sessions != (SessionStats{UserSessions: 17, ActiveRequests: 5, RunnableRequests: 2, SuspendedTasks: 1, BlockedRequests: 3}) {
		t.Errorf("SessionStats = %+v", snap.Sessions)
	}
	if snap.CPU != (CPUUsage{SQLPct: 63, OtherPct: 11}) {
		t.Errorf("CPUUsage = %+v, want 63/11", snap.CPU)
	}
	want := []SchedulerLoad{{CPUID: 0, LoadFactor: 12}, {CPUID: 1, LoadFactor: 34}}
	if len(snap.Load) != len(want) || snap.Load[0] != want[0] || snap.Load[1] != want[1] {
		t.Errorf("SchedulerLoad = %+v, want %+v", snap.Load, want)
	}
}

// A failed reading fails the tick; a zero-valued part would look like a
// genuinely idle server.
func TestCollectStopsAtAFailedRead(t *testing.T) {
	boom := errors.New("activity_test: DMV unavailable")
	for _, method := range []string{
		"PerformanceCounters", "WaitStats", "FileIOStats", "MemoryClerks",
		"Schedulers", "RequestActivity", "HostCPU",
	} {
		t.Run(method, func(t *testing.T) {
			src := snapshotSource()
			src.fail = map[string]error{method: boom}

			snap, err := Collect(context.Background(), src)
			if err == nil {
				t.Fatalf("Collect returned a snapshot with %s failing: %+v", method, snap)
			}
			if !errors.Is(err, boom) {
				t.Errorf("Collect error = %v, want the reading's own error", err)
			}
		})
	}
}

// WaitCategoryNames labels the chart series; a blank entry empties the legend.
func TestWaitCategoryNames(t *testing.T) {
	for i := range waitCategoryCount {
		if WaitCategoryNames[i] == "" {
			t.Errorf("WaitCategory(%d) has no name; the chart legend would be blank", i)
		}
	}
}
