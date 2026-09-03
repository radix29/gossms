// Package core provides the foundational types and drawing primitives used by
// every other tuikit sub-package. Its only tuikit dependency is theme, which
// Init reads the default style from.
//
//   - geometry.go    — Rect
//   - screen.go      — Init (tcell.Screen setup)
//   - clip_screen.go — ClipScreen, a Screen that drops writes outside a rect
//   - drawing.go     — DrawText/DrawBox/FillRect/DrawScrollbar/... primitives
//   - strutil.go     — DisplayWidth/Truncate/WrapText/PadRight/EvRune
//   - runecol.go     — rune-index <-> terminal-column conversion
//   - wordutil.go    — word-boundary helpers for Ctrl+arrow navigation
//   - clipboard.go   — the ClipboardTarget/ClipboardHost interfaces
//   - mathutil.go    — Clamp
package core
