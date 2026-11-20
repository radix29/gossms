package tui

import "github.com/radix29/tuikit/loop"

// latest is tuikit's latest-only load lifecycle (loop.Latest): Begin
// supersedes and cancels the run in flight, Done drops a stale result and
// releases the current run's context, Cancel stops without superseding,
// Abandon does both. The rules for using it here — derive from the
// connection's Context(), never re-zero, wrap rather than grow — are
// ARCHITECTURE.md § Latest-only loads: latest.
//
// An alias, not a copy, so the many sites holding one keep their short field
// type; the type and its tests live in tuikit.
type latest = loop.Latest
