// Package core provides the foundational types and drawing primitives used
// by every other tuikit sub-package.  It has no dependency on any other
// tuikit package and can be imported in isolation.
//
//   - geometry.go — Rect
//   - screen.go   — Init (tcell.Screen setup)
//   - drawing.go  — DrawText/DrawBox/FillRect/DrawScrollbar/... primitives
//   - strutil.go  — DisplayWidth/Truncate/PadRight/Itoa/JoinPath/EvRune
//   - mathutil.go — Min/Max/Clamp/ClampF
package core
