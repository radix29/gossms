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

// Store is the in-memory history of collected samples, oldest first.
// Nothing here is persisted; the panel drops the whole store when it
// closes.
type Store struct {
	samples []Sample
}

// Append adds a sample, drops the Detail of any sample that has fallen out
// of the detail window, and prunes anything older than Retention.
func (s *Store) Append(sample Sample) {
	s.samples = append(s.samples, sample)

	if n := len(s.samples) - DetailWindow - 1; n >= 0 {
		s.samples[n].Detail = nil
	}
	cutoff := sample.At.Add(-Retention)
	drop := 0
	for drop < len(s.samples) && s.samples[drop].At.Before(cutoff) {
		drop++
	}
	if drop > 0 {
		// Re-sliced into a fresh backing array rather than s.samples[drop:]:
		// the latter keeps every pruned sample alive behind the slice header
		// for as long as the panel is open, which is the whole point of
		// pruning.
		kept := make([]Sample, len(s.samples)-drop)
		copy(kept, s.samples[drop:])
		s.samples = kept
	}
}

// Len is the number of samples held.
func (s *Store) Len() int { return len(s.samples) }

// Latest is the newest sample, and false if nothing has been collected yet.
func (s *Store) Latest() (Sample, bool) {
	if len(s.samples) == 0 {
		return Sample{}, false
	}
	return s.samples[len(s.samples)-1], true
}

// Samples returns the stored samples, oldest first. The slice is the
// store's own — read it, don't keep it past the next Append.
func (s *Store) Samples() []Sample { return s.samples }

// Series extracts one value per stored sample, oldest first, for plotting.
func (s *Store) Series(f func(Sample) float64) []float64 {
	out := make([]float64, len(s.samples))
	for i, sample := range s.samples {
		out[i] = f(sample)
	}
	return out
}

// Reset discards everything collected.
func (s *Store) Reset() { s.samples = nil }
