package activity

import "time"

// Retention is how far back the store keeps samples. Everything older is
// discarded on the next append — the window is time-based, so changing the
// refresh rate changes how many samples fit in it, not how far back it
// reaches.
const Retention = 30 * time.Minute

// DetailWindow is how many of the newest samples keep their full-fidelity
// Detail (per-database I/O, memory composition). Older samples keep only
// the aggregate series every History chart plots, which is what makes a
// 30-minute window at a 2-second rate — 900 samples — affordable.
const DetailWindow = 60

// sampleStore is the history half of Store and TempDBStore: the two differ
// only in their element type, their retention window, and which field a
// sample outside the detail window gives up. Everything else — the pruning,
// the fresh backing array, Len/Latest/Samples/Series/Reset — is one
// implementation shared by both.
//
// Config is passed to appendSample rather than held in fields, so the zero
// value of a store is usable: ActivityMonitor holds both stores as plain
// value fields and never constructs them.
type sampleStore[T any] struct {
	samples []T
}

// appendSample adds a sample, calls dropDetail on the sample that has just
// fallen out of the newest window entries, and prunes anything older than
// retention. at reads a sample's own timestamp.
func (s *sampleStore[T]) appendSample(sample T, at func(T) time.Time, retention time.Duration, window int, dropDetail func(*T)) {
	s.samples = append(s.samples, sample)

	if n := len(s.samples) - window - 1; n >= 0 {
		dropDetail(&s.samples[n])
	}
	cutoff := at(sample).Add(-retention)
	drop := 0
	for drop < len(s.samples) && at(s.samples[drop]).Before(cutoff) {
		drop++
	}
	if drop > 0 {
		// Re-sliced into a fresh backing array rather than s.samples[drop:]:
		// the latter keeps every pruned sample alive behind the slice header
		// for as long as the panel is open, which is the whole point of
		// pruning.
		kept := make([]T, len(s.samples)-drop)
		copy(kept, s.samples[drop:])
		s.samples = kept
	}
}

// Len is the number of samples held.
func (s *sampleStore[T]) Len() int { return len(s.samples) }

// Latest is the newest sample, and false if nothing has been collected yet.
func (s *sampleStore[T]) Latest() (T, bool) {
	if len(s.samples) == 0 {
		var zero T
		return zero, false
	}
	return s.samples[len(s.samples)-1], true
}

// Samples returns the stored samples, oldest first. The slice is the
// store's own — read it, don't keep it past the next Append.
func (s *sampleStore[T]) Samples() []T { return s.samples }

// Series extracts one value per stored sample, oldest first, for plotting.
func (s *sampleStore[T]) Series(f func(T) float64) []float64 {
	out := make([]float64, len(s.samples))
	for i, sample := range s.samples {
		out[i] = f(sample)
	}
	return out
}

// Reset discards everything collected.
func (s *sampleStore[T]) Reset() { s.samples = nil }

// sampleTime reads a Sample's own timestamp, for sampleStore's pruning.
func sampleTime(s Sample) time.Time { return s.At }

// Store is the in-memory history of collected samples, oldest first.
// Nothing here is persisted; the panel drops the whole store when it
// closes.
type Store struct {
	sampleStore[Sample]
}

// Append adds a sample, drops the Detail of any sample that has fallen out
// of the detail window, and prunes anything older than Retention.
func (s *Store) Append(sample Sample) {
	s.appendSample(sample, sampleTime, Retention, DetailWindow,
		func(old *Sample) { old.Detail = nil })
}
