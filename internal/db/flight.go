package db

import "context"

// flight is one in-flight keyed call (a capability probe, a peer dial) that
// later callers wait on rather than repeat. Fields other than done are written
// by the leader before done closes and read by waiters after.
type flight[V any] struct {
	done chan struct{}
	val  V // the zero value if the call failed
	err  error
	// abandoned means the leader's context ended, which says nothing about
	// what was asked; a waiter still wanting an answer starts again.
	abandoned bool
	// waiters counts joined callers; tests read it.
	waiters int
}

// flights is the in-flight call per key. No lock of its own: every method but
// flight.wait runs under the owner's mutex that also guards the cache, so "not
// cached" and "joined" are one step.
type flights[K comparable, V any] map[K]*flight[V]

// join returns key's in-flight call, counting the caller as a waiter, or starts
// one and makes the caller its leader: it makes the call, sets the fields, then
// forgets and lands it.
func (fs *flights[K, V]) join(key K) (f *flight[V], leader bool) {
	if f, ok := (*fs)[key]; ok {
		f.waiters++
		return f, false
	}
	if *fs == nil {
		*fs = flights[K, V]{}
	}
	f = &flight[V]{done: make(chan struct{})}
	(*fs)[key] = f
	return f, true
}

// forget drops f unless key already names a newer call (one started after a
// cache clear emptied the set).
func (fs flights[K, V]) forget(key K, f *flight[V]) {
	if fs[key] == f {
		delete(fs, key)
	}
}

// land releases f's waiters. Called once, by the leader, after setting fields.
func (f *flight[V]) land() { close(f.done) }

// wait blocks until f lands or ctx ends, reporting whether it landed. Never
// under the owner's mutex: the leader takes it to land.
func (f *flight[V]) wait(ctx context.Context) bool {
	select {
	case <-f.done:
		return true
	case <-ctx.Done():
		return false
	}
}
