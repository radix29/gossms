package activity

import (
	"context"
	"database/sql"
	"errors"
)

// CPUUsage is the host's busy CPU at the newest scheduler-monitor record,
// split between SQL Server and everything else on the machine. Idle is what
// the two leave under 100 and is deliberately not carried: a chart band for
// "nothing happened" fills the panel on a quiet server and pushes the two
// bands that matter into a few rows.
//
// This is host-wide CPU, not the scheduler pressure SchedStats reports —
// the two answer different questions, and a server pinned by another
// process shows up only here.
type CPUUsage struct {
	SQLPct   float64
	OtherPct float64
}

// SchedulerLoad is one visible online scheduler's load factor — SQL
// Server's own measure of how loaded that CPU is, used by the engine to
// place new tasks.
type SchedulerLoad struct {
	CPUID      int
	LoadFactor float64
}

// RING_BUFFER_SCHEDULER_MONITOR carries one record per minute, so a faster
// collection rate simply reads the same record again until the next one is
// written. The record is XML in a varchar column; the LIKE narrows the scan
// to the health records before the cast, which is what keeps this cheap
// enough to run every tick.
const cpuUsageQuery = `
WITH CpuUsage AS
(
    SELECT
        DATEADD(ms, -1 * (osi.ms_ticks - rb.[timestamp]), SYSDATETIME()) AS EventTime,
        x.value('(./Record/SchedulerMonitorEvent/SystemHealth/ProcessUtilization)[1]', 'int') AS SQLServerCPUPercent,
        x.value('(./Record/SchedulerMonitorEvent/SystemHealth/SystemIdle)[1]', 'int') AS SystemIdlePercent
    FROM sys.dm_os_ring_buffers rb
    CROSS JOIN sys.dm_os_sys_info osi
    CROSS APPLY (SELECT CAST(rb.record AS xml)) AS r(x)
    WHERE rb.ring_buffer_type = N'RING_BUFFER_SCHEDULER_MONITOR'
      AND rb.record LIKE '%<SystemHealth>%'
)
SELECT TOP (1)
    SQLServerCPUPercent,
    100 - SystemIdlePercent - SQLServerCPUPercent AS OtherProcessCPUPercent
FROM CpuUsage
ORDER BY EventTime DESC`

// collectCPUUsage reads the newest scheduler-monitor record. An instance
// that has just started has no record yet, which is a zero reading rather
// than an error — one empty chart must not stop the whole tick.
func collectCPUUsage(ctx context.Context, db *sql.DB) (CPUUsage, error) {
	var c CPUUsage
	err := db.QueryRowContext(ctx, cpuUsageQuery).Scan(&c.SQLPct, &c.OtherPct)
	if errors.Is(err, sql.ErrNoRows) {
		return CPUUsage{}, nil
	}
	if err != nil {
		return CPUUsage{}, err
	}
	return c, nil
}

// Only VISIBLE ONLINE schedulers run user work, so only they have a load
// factor worth showing — the same filter SchedStats uses.
const loadFactorQuery = `
SELECT cpu_id, load_factor
FROM sys.dm_os_schedulers
WHERE status = 'VISIBLE ONLINE'
ORDER BY cpu_id`

func collectSchedulerLoad(ctx context.Context, db *sql.DB) ([]SchedulerLoad, error) {
	rows, err := db.QueryContext(ctx, loadFactorQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SchedulerLoad
	for rows.Next() {
		var l SchedulerLoad
		if err := rows.Scan(&l.CPUID, &l.LoadFactor); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
