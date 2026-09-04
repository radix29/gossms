package activity

import (
	"context"
	"database/sql"
)

// SchedStats is the CPU-pressure picture sys.dm_os_schedulers gives: how much
// work is queued for the schedulers that run user tasks. It is not the host's
// CPU percentage — that is CPUUsage, shredded out of the scheduler-monitor
// ring buffer in cpu.go — and the two answer different questions.
type SchedStats struct {
	RunnableTasks float64
	CurrentTasks  float64
	ActiveWorkers float64
	WorkQueue     float64
	Schedulers    int
}

// Only VISIBLE ONLINE schedulers run user work; the hidden ones serve
// internal tasks and their queue depths say nothing about user CPU
// pressure.
const schedQuery = `
SELECT COUNT(*), SUM(runnable_tasks_count), SUM(current_tasks_count),
       SUM(active_workers_count), SUM(work_queue_count)
FROM sys.dm_os_schedulers
WHERE status = 'VISIBLE ONLINE'`

func collectSchedulers(ctx context.Context, db *sql.DB) (SchedStats, error) {
	var s SchedStats
	var runnable, current, workers, queue sql.NullFloat64
	err := db.QueryRowContext(ctx, schedQuery).Scan(&s.Schedulers, &runnable, &current, &workers, &queue)
	if err != nil {
		return SchedStats{}, err
	}
	s.RunnableTasks = runnable.Float64
	s.CurrentTasks = current.Float64
	s.ActiveWorkers = workers.Float64
	s.WorkQueue = queue.Float64
	return s, nil
}
