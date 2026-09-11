// Package activity collects what the Activity Monitor draws: performance
// counters, waits, file I/O, memory, schedulers and session counts.
//
// It knows nothing about the TUI. A Collector ticks against a *sql.DB, turns
// each Snapshot into a Sample of per-second rates and gauges against the
// previous one, and appends it to a Store holding the last 30 minutes, in
// memory only.
//
// Two easy mistakes give plausible wrong numbers rather than errors, so both
// are pinned by tests: sys.dm_os_performance_counters must be decoded by
// cntr_type (see counters.go), and named instances prefix object_name with
// "MSSQL$INST:" instead of "SQLServer:", so counters match on the part after
// the colon.
package activity
