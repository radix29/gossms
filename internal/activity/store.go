package activity

import "time"

// Retention is how far back the store keeps samples; older ones are pruned on
// append. Time-based, so the refresh rate changes how many samples fit, not how
// far back.
const Retention = 30 * time.Minute

// DetailWindow is how many newest samples keep their Detail. Older samples keep
// only aggregate series, which makes 900 samples (30 min at 2s) affordable.
const DetailWindow = 60

// sampleStore is the history shared by Store and TempDBStore, which differ only
// in element type, retention, and which field an out-of-window sample drops.
//
// Config is passed to appendSample rather than held, so the zero value is
// usable (ActivityMonitor holds both stores as plain fields).
type sampleStore[T any] struct {
	samples []T
}

// appendSample adds a sample, calls dropDetail on the one that just left the
// newest-window, and prunes anything older than retention. at reads a sample's
// timestamp.
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
		// Copy into a fresh array; re-slicing would keep pruned samples alive.
		kept := make([]T, len(s.samples)-drop)
		copy(kept, s.samples[drop:])
		s.samples = kept
	}
}

// Len is the number of samples held.
func (s *sampleStore[T]) Len() int { return len(s.samples) }

// Latest is the newest sample, and false if none yet.
func (s *sampleStore[T]) Latest() (T, bool) {
	if len(s.samples) == 0 {
		var zero T
		return zero, false
	}
	return s.samples[len(s.samples)-1], true
}

// Samples returns the stored samples, oldest first. The slice is the store's
// own; don't keep it past the next Append.
func (s *sampleStore[T]) Samples() []T { return s.samples }

// Series extracts one value per sample, oldest first.
func (s *sampleStore[T]) Series(f func(T) float64) []float64 {
	out := make([]float64, len(s.samples))
	for i, sample := range s.samples {
		out[i] = f(sample)
	}
	return out
}

// Reset discards everything collected.
func (s *sampleStore[T]) Reset() { s.samples = nil }

// sampleTime reads a Sample's timestamp for pruning.
func sampleTime(s Sample) time.Time { return s.At }

// Store is the in-memory sample history, oldest first. The panel drops it on
// close.
type Store struct {
	sampleStore[Sample]
}

// Append adds a sample, drops Detail from samples leaving the detail window,
// and prunes anything older than Retention.
func (s *Store) Append(sample Sample) {
	s.appendSample(sample, sampleTime, Retention, DetailWindow,
		func(old *Sample) { old.Detail = nil })
}
