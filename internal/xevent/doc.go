// Package xevent is the Extended Events viewer's in-memory side: the bounded
// store of events a Watch Live Data or View Target Data panel holds, the
// registry of columns those events have shown, the dedupe that tells a
// re-read ring_buffer's new events from the ones already held, and the
// client-side filter expression.
//
// Pure data, like internal/showplan: no TUI and no database imports. The
// events come from gosmo's readers, converted by the caller, so everything
// here is testable from values built in a test.
package xevent
