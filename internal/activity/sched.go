package activity

import "context"

// SchedStats is sys.dm_os_schedulers' CPU-pressure picture: work queued for
// user schedulers. Host CPU percentage is CPUUsage (cpu.go), a different
// question.
type SchedStats struct {
	RunnableTasks float64
	CurrentTasks  float64
	ActiveWorkers float64
	WorkQueue     float64
	Schedulers    int
}

// collectSchedulers reads the schedulers once for both their summed pressure
// and each one's load factor. Only online ones count: an offline scheduler
// (outside the affinity mask) runs no user work.
func collectSchedulers(ctx context.Context, src Source) (SchedStats, []SchedulerLoad, error) {
	scheds, err := src.Schedulers(ctx)
	if err != nil {
		return SchedStats{}, nil, err
	}
	var s SchedStats
	var load []SchedulerLoad
	for _, sc := range scheds {
		if !sc.IsOnline {
			continue
		}
		s.Schedulers++
		s.RunnableTasks += float64(sc.RunnableTasks)
		s.CurrentTasks += float64(sc.CurrentTasks)
		s.ActiveWorkers += float64(sc.ActiveWorkers)
		s.WorkQueue += float64(sc.WorkQueue)
		load = append(load, SchedulerLoad{CPUID: sc.CPUID, LoadFactor: float64(sc.LoadFactor)})
	}
	return s, load, nil
}
