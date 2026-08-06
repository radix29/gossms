package activity

import (
	"testing"
	"time"
)

func snapshotAt(at time.Time, counters counterSet) *Snapshot {
	return &Snapshot{At: at, Counters: counters, Waits: waitSet{}, Files: fileSet{}}
}

// The first tick has nothing to compare against. Every rate has to read
// zero rather than the cumulative total the server has accumulated since it
// started, which would be an opening spike of millions per second.
func TestDeriveWithNoPreviousSnapshot(t *testing.T) {
	cur := snapshotAt(time.Now(), set(
		counterRow{objSQLStats, "Batch Requests/sec", "", 8_000_000, cntrPerSecond},
		counterRow{objBufferMgr, "Page life expectancy", "", 5761, cntrRawGauge},
	))

	s := Derive(nil, cur)
	if s.BatchesSec != 0 {
		t.Errorf("first sample's batch rate = %v, want 0 with nothing to compare against", s.BatchesSec)
	}
	// A gauge needs no previous sample and should be readable immediately.
	if s.PageLifeExpectancy != 5761 {
		t.Errorf("first sample's PLE = %v, want the gauge read straight through", s.PageLifeExpectancy)
	}
}

func TestDeriveComputesRatesAndUnits(t *testing.T) {
	start := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	prev := snapshotAt(start, set(
		counterRow{objSQLStats, "Batch Requests/sec", "", 1000, cntrPerSecond},
		counterRow{objDatabases, "Backup/Restore Throughput/sec", totalInstance, 0, cntrPerSecond},
		counterRow{objMemoryMgr, "Total Server Memory (KB)", "", 2 * 1024 * 1024, cntrRawGauge},
	))
	cur := snapshotAt(start.Add(2*time.Second), set(
		counterRow{objSQLStats, "Batch Requests/sec", "", 1600, cntrPerSecond},
		counterRow{objDatabases, "Backup/Restore Throughput/sec", totalInstance, 200 * bytesPerMB, cntrPerSecond},
		counterRow{objMemoryMgr, "Total Server Memory (KB)", "", 3 * 1024 * 1024, cntrRawGauge},
	))

	s := Derive(prev, cur)

	if s.Interval != 2*time.Second {
		t.Errorf("interval = %v, want 2s", s.Interval)
	}
	if s.BatchesSec != 300 {
		t.Errorf("batches = %v/sec, want 600 over 2s", s.BatchesSec)
	}
	// The backup counter is bytes per second; the panel is megabytes.
	if s.BackupMBSec != 100 {
		t.Errorf("backup throughput = %v MB/sec, want 200MB over 2s", s.BackupMBSec)
	}
	// The memory counters are kilobytes; the panel is megabytes.
	if s.TotalServerMemoryMB != 3072 {
		t.Errorf("total server memory = %v MB, want 3 GB", s.TotalServerMemoryMB)
	}
}

// The CPU split is a gauge the ring buffer already reports as percentages:
// deriving a rate from it would turn a steady 40% into 0.
func TestDeriveReadsCPUUsageAsAGauge(t *testing.T) {
	start := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	prev := snapshotAt(start, set())
	prev.CPU = CPUUsage{SQLPct: 40, OtherPct: 5}
	cur := snapshotAt(start.Add(2*time.Second), set())
	cur.CPU = CPUUsage{SQLPct: 40, OtherPct: 5}
	cur.Load = []SchedulerLoad{{CPUID: 0, LoadFactor: 12}, {CPUID: 1, LoadFactor: 3}}

	s := Derive(prev, cur)

	if s.CPU != (CPUUsage{SQLPct: 40, OtherPct: 5}) {
		t.Errorf("CPU split = %+v, want the current reading passed straight through", s.CPU)
	}
	if s.Detail == nil || len(s.Detail.Load) != 2 || s.Detail.Load[1].LoadFactor != 3 {
		t.Errorf("scheduler load = %+v, want both cores carried into the detail", s.Detail)
	}
}

// Every sample carries its own detail; Store is what decides how long to
// keep it.
func TestDeriveAlwaysProducesDetail(t *testing.T) {
	cur := snapshotAt(time.Now(), set())
	cur.Memory = []MemoryComponent{{Name: memBuffer, MB: 512}}

	s := Derive(nil, cur)
	if s.Detail == nil || len(s.Detail.Memory) != 1 {
		t.Fatalf("sample detail = %+v, want the memory composition carried through", s.Detail)
	}
}
