// Package charts renders terminal charts from generic series data.
//
// It is a leaf of tuikit: it knows about tcell, tuikit/core, and
// tuikit/theme, and nothing else. It has no notion of SQL Server, of the
// Activity Monitor, or of where a number came from — a caller transforms
// whatever it collected into a Series and hands it over.
//
// Every chart draws into a tcell.Screen through a core.Rect, so a chart can
// render straight to the terminal or into an off-screen Canvas (canvas.go)
// that the caller then blits and scrolls. Rendering is deterministic:
// identical input produces an identical cell grid, which is what the golden
// tests in this package assert against.
//
// Charts clip to the rect they are given, tolerate rects too small to hold
// them, and tolerate empty or all-zero series. They never panic on any of
// those; they draw as much as fits and stop.
package charts
