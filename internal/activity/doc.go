// Package activity collects the SQL Server activity the Activity Monitor
// panel draws: performance counters, wait statistics, file I/O, memory,
// schedulers, and session counts.
//
// The package knows nothing about the TUI. A Collector ticks against a
// *sql.DB, turns each raw Snapshot into a Sample of per-second rates and
// gauges by comparing it with the previous one, and appends it to a Store
// holding the last 30 minutes. Everything is in memory and nothing is
// persisted.
//
// Two rules here are easy to get wrong and produce plausible-looking wrong
// numbers rather than errors, so both are pinned by tests over fixture
// rows: sys.dm_os_performance_counters must be decoded by cntr_type rather
// than read raw (see counters.go), and on a named instance object_name
// carries an "MSSQL$INST:" prefix instead of "SQLServer:", so counters are
// matched on the portion after the colon.
package activity
