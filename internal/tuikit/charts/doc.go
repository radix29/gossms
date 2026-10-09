// Package charts renders terminal charts from generic series data.
//
// It is a leaf of tuikit: it knows tcell, tuikit/core and tuikit/theme, nothing
// else. It has no notion of SQL Server or where a number came from; callers
// transform their data into a Series.
//
// Every chart draws into a tcell.Screen through a core.Rect, so it can render
// straight to the terminal or into an off-screen Canvas (canvas.go) that the
// caller blits and scrolls. Rendering is deterministic (the golden tests rely on
// it). Charts clip to their rect and tolerate too-small rects and empty or
// all-zero series without panicking.
package charts
