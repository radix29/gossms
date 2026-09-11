package activity

import "time"

// Sample is one interval's activity: gauges as read, cumulative counters as
// per-second rates against the previous snapshot. History plots a series of
// Samples; Sample draws the newest.
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
	// WaitsSignal is the signal-wait part of Waits (included in it, not extra).
	// Waits[i]-WaitsSignal[i] is the resource part, as the Sample tab splits
	// each bar.
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

	// Host CPU split from the scheduler-monitor ring buffer; gauges, already
	// percentages.
	CPU CPUUsage

	// Detail holds data too large to keep for the whole retention window:
	// per-database I/O and memory composition. Store drops it from all but the
	// newest samples, so whole-window charts must use the fields above.
	Detail *SampleDetail
}

// SampleDetail is the full-fidelity part of a Sample; see Sample.Detail.
type SampleDetail struct {
	PerDatabaseIO []FileIO
	Memory        []MemoryComponent
	// Load is per-scheduler load factor, one entry per visible online CPU. Kept
	// in Detail because its width is the core count.
	Load []SchedulerLoad
}

// Derive turns two consecutive snapshots into a Sample. prev may be nil (first
// tick): only gauges are set and every rate is zero.
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
	// The backup counter is bytes per second.
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
