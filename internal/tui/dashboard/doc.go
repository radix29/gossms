// Package dashboard lays out the Activity Monitor's dashboards: History,
// Sample, TempDB, and — on an Azure engine edition — Instance.
//
// It is a leaf of the application layer, like planview and sqlparse: it
// depends on tuikit and the standard library, never on internal/tui, and knows
// nothing about SQL Server, connections or collection. Its input is a plain
// view model (view.go) of computed numbers, so the Activity Monitor panel and
// cmd/amdemo draw the same dashboards (the panel maps collected samples into
// it, the demo generates one).
//
// A dashboard draws into a fixed-size canvas, not the terminal: at the
// mockups' density a full dashboard exceeds most terminals, so the caller
// renders once at canvas size and scrolls a viewport. The *CanvasW/*CanvasH
// constants are those sizes.
//
// InstanceView differs: HistoryView, SampleView and TempDBView are built from
// samples the *caller* collected and differenced, but InstanceView comes from
// a history the server already aggregated into fixed windows, so its Interval
// is the server's window, not the caller's collection rate. A caller scaling
// its time axis by its own refresh rate would mislabel every column.
package dashboard
