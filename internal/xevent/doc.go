// Package xevent is the Extended Events viewer's in-memory side: the bounded
// event store, the registry of columns events have shown, the ring_buffer
// dedupe, and the client-side filter expression.
//
// Pure data, like internal/showplan: no TUI or database imports. Events come
// from gosmo's readers, converted by the caller.
package xevent
