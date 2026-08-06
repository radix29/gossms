// Package dashboard lays out the Activity Monitor's History and Sample
// dashboards.
//
// It is a leaf of the application layer, like planview and sqlparse: it
// depends on tuikit and the standard library, never on internal/tui itself,
// and it knows nothing about SQL Server, connections, or collection. Its
// input is a plain view model (view.go) of already-computed numbers, which
// is what lets both the Activity Monitor panel and cmd/amdemo draw the same
// dashboards — the panel maps collected samples into the view model, the
// demo harness generates one.
//
// A dashboard draws into a fixed-size canvas rather than into whatever the
// terminal happens to be: at the density the mockups call for, a full
// dashboard is taller and wider than most terminals, so the caller renders
// once at canvas size and scrolls a viewport over the result. HistoryCanvas
// and SampleCanvas are those sizes.
package dashboard
