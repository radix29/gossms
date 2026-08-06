package activity

import "time"

// Sample is one interval's worth of activity: the gauges as read, and every
// cumulative counter converted into a per-second rate against the previous
// snapshot. This is what both dashboards draw — History plots a series of
// Samples, Sample draws the newest one.
type Sample struct {
	At       time.Time
	Interval time.Duration

	// SQL SERVER ACTIVITY.
	BatchesSec      float64
	TransactionsSec float64
	CompilesSec     float64
	RecompilesSec   float64
	IndexSearchSec  float64
	ForwardedRecSec float64
	BackupMBSec     float64
	UserConnections float64
	BlockedProcs    float64
	ActiveRequests  float64

	// SQL SERVER WAITS: milliseconds of wait per second, by category.
	Waits [waitCategoryCount]float64
	// WaitsSignal is the signal-wait part of Waits, category by category —
	// time already counted in Waits, not extra time. Waits[i]-WaitsSignal[i]
	// is the resource half, which is how the Sample tab splits each bar.
	WaitsSignal   [waitCategoryCount]float64
	CPUPctOfWaits float64

	// SQL SERVER MEMORY.
	PageLifeExpectancy  float64
	BufferCacheHitPct   float64
	PlanCacheHitPct     float64
	MemoryGrantsPending float64
	TotalServerMemoryMB float64
	TargetServerMemMB   float64
	PageReadsSec        float64
	PageWritesSec       float64
	LazyWritesSec       float64
	CheckpointPagesSec  float64

	// DATABASE IO and log.
	IOTotal            FileIO
	LogFlushesSec      float64
	LogBytesFlushedSec float64
	LogFlushWaitsSec   float64

	// CPU pressure, from the schedulers.
	Sched SchedStats

	// Host CPU split, from the scheduler-monitor ring buffer. Gauges, not
	// rates: the ring buffer already reports percentages.
	CPU CPUUsage

	// Detail carries the parts of a sample too large to keep for the whole
	// retention window: the per-database I/O breakdown and the memory
	// composition. Store drops it from all but the most recent samples, so
	// anything drawn from the whole window must come from the fields above.
	Detail *SampleDetail
}

// SampleDetail is the full-fidelity part of a Sample. See Sample.Detail.
type SampleDetail struct {
	PerDatabaseIO []FileIO
	Memory        []MemoryComponent
	// Load is the per-scheduler load factor, one entry per visible online
	// CPU. Kept here rather than on the Sample because its width is the
	// server's core count, which on a large box is more per sample than the
	// retention window can afford.
	Load []SchedulerLoad
}

// Derive turns two consecutive snapshots into one Sample. prev may be nil —
// the first tick after the panel opens has nothing to compare against — in
// which case only the gauges are populated and every rate reads zero, which
// is what the first column of a fresh history chart should show.
func Derive(prev, cur *Snapshot) Sample {
	var prevCounters counterSet
	var prevWaits waitSet
	var prevFiles fileSet
	var elapsed float64
	s := Sample{At: cur.At}
	if prev != nil {
		prevCounters, prevWaits, prevFiles = prev.Counters, prev.Waits, prev.Files
		s.Interval = cur.At.Sub(prev.At)
		elapsed = s.Interval.Seconds()
	}
	c := cur.Counters

	s.BatchesSec = c.value(prevCounters, objSQLStats, "Batch Requests/sec", "", elapsed)
	s.CompilesSec = c.value(prevCounters, objSQLStats, "SQL Compilations/sec", "", elapsed)
	s.RecompilesSec = c.value(prevCounters, objSQLStats, "SQL Re-Compilations/sec", "", elapsed)
	s.TransactionsSec = c.value(prevCounters, objDatabases, "Transactions/sec", totalInstance, elapsed)
	s.IndexSearchSec = c.value(prevCounters, objAccessMeth, "Index Searches/sec", "", elapsed)
	s.ForwardedRecSec = c.value(prevCounters, objAccessMeth, "Forwarded Records/sec", "", elapsed)
	// The backup counter is bytes per second, like every other throughput
	// counter in the Databases object.
	s.BackupMBSec = c.value(prevCounters, objDatabases, "Backup/Restore Throughput/sec", totalInstance, elapsed) / bytesPerMB
	s.UserConnections = c.value(prevCounters, objGeneralStats, "User Connections", "", elapsed)
	s.BlockedProcs = c.value(prevCounters, objGeneralStats, "Processes blocked", "", elapsed)
	s.ActiveRequests = cur.Sessions.ActiveRequests

	s.Waits, s.WaitsSignal, s.CPUPctOfWaits = waitDeltas(prevWaits, cur.Waits, elapsed)

	s.PageLifeExpectancy = c.value(prevCounters, objBufferMgr, "Page life expectancy", "", elapsed)
	s.BufferCacheHitPct = c.value(prevCounters, objBufferMgr, "Buffer cache hit ratio", "", elapsed)
	s.PlanCacheHitPct = c.value(prevCounters, objPlanCache, "Cache Hit Ratio", totalInstance, elapsed)
	s.MemoryGrantsPending = c.value(prevCounters, objMemoryMgr, "Memory Grants Pending", "", elapsed)
	s.TotalServerMemoryMB = c.value(prevCounters, objMemoryMgr, "Total Server Memory (KB)", "", elapsed) / 1024
	s.TargetServerMemMB = c.value(prevCounters, objMemoryMgr, "Target Server Memory (KB)", "", elapsed) / 1024
	s.PageReadsSec = c.value(prevCounters, objBufferMgr, "Page reads/sec", "", elapsed)
	s.PageWritesSec = c.value(prevCounters, objBufferMgr, "Page writes/sec", "", elapsed)
	s.LazyWritesSec = c.value(prevCounters, objBufferMgr, "Lazy writes/sec", "", elapsed)
	s.CheckpointPagesSec = c.value(prevCounters, objBufferMgr, "Checkpoint pages/sec", "", elapsed)

	perDB, total := fileDeltas(prevFiles, cur.Files, elapsed)
	s.IOTotal = total
	s.LogFlushesSec = c.value(prevCounters, objDatabases, "Log Flushes/sec", totalInstance, elapsed)
	s.LogBytesFlushedSec = c.value(prevCounters, objDatabases, "Log Bytes Flushed/sec", totalInstance, elapsed)
	s.LogFlushWaitsSec = c.value(prevCounters, objDatabases, "Log Flush Waits/sec", totalInstance, elapsed)

	s.Sched = cur.Sched
	s.CPU = cur.CPU
	s.Detail = &SampleDetail{PerDatabaseIO: perDB, Memory: cur.Memory, Load: cur.Load}
	return s
}
