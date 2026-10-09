// Package activity collects what the Activity Monitor draws: performance
// counters, waits, file I/O, memory, schedulers and session counts.
//
// It knows nothing about the TUI and sends no DMV query of its own: the
// readings are gosmo's (Source, source.go), so gosmo's TestLiveVersionSweep
// runs them on every supported major. A Collector ticks against a Source,
// turns each Snapshot into a Sample of per-second rates and gauges against the
// previous one, and appends it to an in-memory Store of the last 30 minutes.
// The helper procedures behind the Sessions and Block tabs (proc.go) are the
// exception: scripts this package installs and runs.
//
// One easy mistake gives plausible wrong numbers rather than errors, so tests
// pin it: sys.dm_os_performance_counters must be decoded by cntr_type (see
// counters.go). The other, object_name's instance prefix ("SQLServer:" /
// "MSSQL$INST:"), gosmo strips.
package activity
