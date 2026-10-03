package activity

import (
	"context"
	"time"
)

// Snapshot is one tick's raw cumulative readings, before rate conversion;
// meaningful only in pairs (see Derive).
type Snapshot struct {
	At       time.Time
	Counters counterSet
	Waits    waitSet
	Files    fileSet
	Memory   []MemoryComponent
	Sched    SchedStats
	Sessions SessionStats
	CPU      CPUUsage
	Load     []SchedulerLoad
}

// Collect reads a full snapshot, one reading after another: concurrent reads
// would each need a connection, and would describe different instants.
func Collect(ctx context.Context, src Source) (*Snapshot, error) {
	s := &Snapshot{At: time.Now()}

	var err error
	if s.Counters, err = collectCounterSet(ctx, src, counterNames); err != nil {
		return nil, err
	}
	if s.Waits, err = collectWaits(ctx, src); err != nil {
		return nil, err
	}
	if s.Files, err = collectFileIO(ctx, src); err != nil {
		return nil, err
	}
	if s.Memory, err = collectMemory(ctx, src); err != nil {
		return nil, err
	}
	if s.Sched, s.Load, err = collectSchedulers(ctx, src); err != nil {
		return nil, err
	}
	if s.Sessions, err = collectSessions(ctx, src); err != nil {
		return nil, err
	}
	if s.CPU, err = collectCPUUsage(ctx, src); err != nil {
		return nil, err
	}
	return s, nil
}
