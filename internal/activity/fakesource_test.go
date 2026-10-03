package activity

import (
	"context"
	"errors"

	gosmo "github.com/radix29/gosmo"
)

// fakeSource answers each reading from its fields. fail names a reading
// (by method name) that errors instead; all makes every reading error, and
// permErr the permission prologue. VIEW SERVER STATE is held unless noPerm.
type fakeSource struct {
	counters []gosmo.PerformanceCounter
	waits    []gosmo.WaitStat
	files    []gosmo.FileIOStat
	clerks   []gosmo.MemoryClerk
	scheds   []gosmo.Scheduler
	requests gosmo.RequestActivity
	cpu      gosmo.HostCPU

	tdSpace    gosmo.TempDBSpace
	tdFiles    []gosmo.TempDBFile
	tdObjects  []gosmo.TempDBObjects
	tdSessions []gosmo.TempDBSession
	info       *gosmo.ServerInfo

	noPerm  bool
	permErr error
	fail    map[string]error
	all     error

	// counterNames and counterInstances are the last PerformanceCounters
	// filter asked for.
	counterNames, counterInstances []string
}

var errUnreachable = errors.New("activity_test: server unreachable")

// deadSource fails the permission prologue, as an unreachable server does.
func deadSource() *fakeSource { return &fakeSource{permErr: errUnreachable} }

// probeOnceSource passes the prologue and fails every reading: an
// unreachable server with a live context.
func probeOnceSource() *fakeSource { return &fakeSource{all: errUnreachable} }

func (f *fakeSource) err(method string) error {
	if f.all != nil {
		return f.all
	}
	return f.fail[method]
}

func (f *fakeSource) HasViewServerState(context.Context) (bool, error) {
	if f.permErr != nil {
		return false, f.permErr
	}
	return !f.noPerm, nil
}

func (f *fakeSource) PerformanceCounters(_ context.Context, names, instances []string) ([]gosmo.PerformanceCounter, error) {
	f.counterNames, f.counterInstances = names, instances
	if err := f.err("PerformanceCounters"); err != nil {
		return nil, err
	}
	// The server filters; so does the fake, or a test could pass on a row the
	// real read would never have returned.
	var out []gosmo.PerformanceCounter
	for _, c := range f.counters {
		if contains(names, c.Counter) && contains(instances, c.Instance) {
			out = append(out, c)
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	if len(list) == 0 {
		return true
	}
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (f *fakeSource) WaitStats(context.Context) ([]gosmo.WaitStat, error) {
	return f.waits, f.err("WaitStats")
}

func (f *fakeSource) FileIOStats(context.Context) ([]gosmo.FileIOStat, error) {
	return f.files, f.err("FileIOStats")
}

func (f *fakeSource) MemoryClerks(context.Context) ([]gosmo.MemoryClerk, error) {
	return f.clerks, f.err("MemoryClerks")
}

func (f *fakeSource) Schedulers(context.Context) ([]gosmo.Scheduler, error) {
	return f.scheds, f.err("Schedulers")
}

func (f *fakeSource) RequestActivity(context.Context) (gosmo.RequestActivity, error) {
	return f.requests, f.err("RequestActivity")
}

func (f *fakeSource) HostCPU(context.Context) (gosmo.HostCPU, error) {
	return f.cpu, f.err("HostCPU")
}

func (f *fakeSource) TempDBSpace(context.Context) (gosmo.TempDBSpace, error) {
	return f.tdSpace, f.err("TempDBSpace")
}

func (f *fakeSource) TempDBFiles(context.Context) ([]gosmo.TempDBFile, error) {
	return f.tdFiles, f.err("TempDBFiles")
}

func (f *fakeSource) TempDBObjects(context.Context) ([]gosmo.TempDBObjects, error) {
	return f.tdObjects, f.err("TempDBObjects")
}

func (f *fakeSource) TempDBSessions(context.Context) ([]gosmo.TempDBSession, error) {
	return f.tdSessions, f.err("TempDBSessions")
}

func (f *fakeSource) Info() *gosmo.ServerInfo { return f.info }
