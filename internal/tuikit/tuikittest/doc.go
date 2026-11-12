// Package tuikittest is the test kit for tuikit's controls and their hosts:
// a Buffer-backed screen to draw into and read back, event builders, and row
// snapshots.
//
// It is imported by _test files only. core's own tests cannot use it (it
// imports core), so they draw into a core.Buffer directly.
//
//   - screen.go — Screen, a core.Buffer with search and snapshot helpers
//   - events.go — Key/Rune/Mouse/Press/Release event builders
package tuikittest
