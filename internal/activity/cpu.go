package activity

import "context"

// CPUUsage is host busy CPU at the newest scheduler-monitor record, split into
// SQL Server and other processes. Idle is omitted: its band would fill the
// chart on a quiet server and squeeze the two that matter.
//
// This is host-wide CPU, unlike SchedStats' scheduler pressure; a server pinned
// by another process shows only here.
type CPUUsage struct {
	SQLPct   float64
	OtherPct float64
}

// SchedulerLoad is one visible online scheduler's load factor, the engine's
// measure used to place new tasks.
type SchedulerLoad struct {
	CPUID      int
	LoadFactor float64
}

// collectCPUUsage reads the newest scheduler-monitor record. The ring buffer
// gets one a minute, so faster ticks reread it, and a freshly started instance
// has none: gosmo reads that as zero, not an error.
func collectCPUUsage(ctx context.Context, src Source) (CPUUsage, error) {
	c, err := src.HostCPU(ctx)
	if err != nil {
		return CPUUsage{}, err
	}
	return CPUUsage{SQLPct: float64(c.SQLServerPercent), OtherPct: float64(c.OtherPercent)}, nil
}
