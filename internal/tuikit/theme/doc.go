// Package theme defines the SSMS-inspired dark colour palette and base
// tcell styles used throughout the tuikit library and the gossms application.
// Consumers import this package to read colours and styles; they may also
// call SetPalette to substitute a different palette at start-up.
//
//   - palette.go — Palette struct, Default palette, SetPalette/Active
//   - styles.go  — Style* functions derived from the active palette
package theme
