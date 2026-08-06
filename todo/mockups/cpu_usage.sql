

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
    EventTime,
    SQLServerCPUPercent,
    100 - SystemIdlePercent - SQLServerCPUPercent AS OtherProcessCPUPercent,
    SystemIdlePercent
FROM CpuUsage
ORDER BY EventTime DESC;
