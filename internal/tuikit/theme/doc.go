// Package theme defines the SSMS-inspired dark palette and base tcell styles.
// SetPalette substitutes a palette at start-up or on a theme switch.
//
// The active palette is package-level state with no lock: SetPalette and
// Active belong to the UI goroutine, and Active's result is read-only.
//
//   - palette.go — Palette, Default, SetPalette/Active
//   - styles.go — Style* functions derived from the active palette
package theme
