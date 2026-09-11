package activity

import (
	"context"
	"database/sql"
	"errors"
)

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

// RING_BUFFER_SCHEDULER_MONITOR writes one record per minute, so faster ticks
// reread it. The LIKE narrows to health records before the XML cast, keeping
// this cheap enough per tick.
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

// collectCPUUsage reads the newest scheduler-monitor record. A freshly started
// instance has none: a zero reading, not an error.
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

// Only VISIBLE ONLINE schedulers run user work (same filter as SchedStats).
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
