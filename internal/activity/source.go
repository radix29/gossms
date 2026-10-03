package activity

import (
	"context"

	gosmo "github.com/radix29/gosmo"
)

// Source is what the collectors read: the DMV readings gosmo's *Server
// provides, where TestLiveVersionSweep runs them on every supported major.
// The app passes a *gosmo.Server; tests pass a fake.
//
// Every reading is raw — cumulative counters and current levels. Turning two
// of them into rates is this package's work (Derive, deriveTempDB).
type Source interface {
	HasViewServerState(ctx context.Context) (bool, error)
	PerformanceCounters(ctx context.Context, counters, instances []string) ([]gosmo.PerformanceCounter, error)
	WaitStats(ctx context.Context) ([]gosmo.WaitStat, error)
	FileIOStats(ctx context.Context) ([]gosmo.FileIOStat, error)
	MemoryClerks(ctx context.Context) ([]gosmo.MemoryClerk, error)
	Schedulers(ctx context.Context) ([]gosmo.Scheduler, error)
	RequestActivity(ctx context.Context) (gosmo.RequestActivity, error)
	HostCPU(ctx context.Context) (gosmo.HostCPU, error)

	TempDBSpace(ctx context.Context) (gosmo.TempDBSpace, error)
	TempDBFiles(ctx context.Context) ([]gosmo.TempDBFile, error)
	TempDBObjects(ctx context.Context) ([]gosmo.TempDBObjects, error)
	TempDBSessions(ctx context.Context) ([]gosmo.TempDBSession, error)

	// Info is read for the core count, which connect already loaded.
	Info() *gosmo.ServerInfo
}

var _ Source = (*gosmo.Server)(nil)
