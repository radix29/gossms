package activity

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// TempDBRetention is how far back the tempdb store keeps samples. Four
// hours rather than Store's thirty minutes because this tab ticks in tens of
// seconds, not in seconds: at a 60-second rate a thirty-minute window is
// thirty columns, and the things it exists to show — a version store that
// never gets cleaned up, a file that fills over an afternoon — build over
// hours.
const TempDBRetention = 4 * time.Hour

// TempDBDetailWindow is how many of the newest samples keep their file,
// object, and session lists. Older samples keep the space and counter
// levels every chart plots; the lists are only ever read for the newest
// sample, and a session list on a busy server is the largest thing here.
const TempDBDetailWindow = 10

// TempDBStore is the in-memory history of tempdb samples, oldest first.
type TempDBStore struct {
	samples []TempDBSample
}

// Append adds a sample, drops the lists of any sample that has fallen out of
// the detail window, and prunes anything older than TempDBRetention.
func (s *TempDBStore) Append(sample TempDBSample) {
	s.samples = append(s.samples, sample)

	if n := len(s.samples) - TempDBDetailWindow - 1; n >= 0 {
		s.samples[n].Files = nil
		s.samples[n].Sessions = nil
	}
	cutoff := sample.At.Add(-TempDBRetention)
	drop := 0
	for drop < len(s.samples) && s.samples[drop].At.Before(cutoff) {
		drop++
	}
	if drop > 0 {
		// A fresh backing array, not s.samples[drop:] — see Store.Append.
		kept := make([]TempDBSample, len(s.samples)-drop)
		copy(kept, s.samples[drop:])
		s.samples = kept
	}
}

// Len is the number of samples held.
func (s *TempDBStore) Len() int { return len(s.samples) }

// Latest is the newest sample, and false if nothing has been collected yet.
func (s *TempDBStore) Latest() (TempDBSample, bool) {
	if len(s.samples) == 0 {
		return TempDBSample{}, false
	}
	return s.samples[len(s.samples)-1], true
}

// Samples returns the stored samples, oldest first. The slice is the store's
// own — read it, don't keep it past the next Append.
func (s *TempDBStore) Samples() []TempDBSample { return s.samples }

// Series extracts one value per stored sample, oldest first, for plotting.
func (s *TempDBStore) Series(f func(TempDBSample) float64) []float64 {
	out := make([]float64, len(s.samples))
	for i, sample := range s.samples {
		out[i] = f(sample)
	}
	return out
}

// Reset discards everything collected.
func (s *TempDBStore) Reset() { s.samples = nil }

// TempDBCollector ticks tempdb readings against one connection. It is a
// second collector rather than more work inside Collector because the two
// tabs run at different rates: tempdb is read in tens of seconds, and its
// object enumeration touches tempdb's own metadata, which is the last thing
// that should ride along on a 2-second tick.
type TempDBCollector struct {
	db       *sql.DB
	onSample func(TempDBSample)
	onError  func(error)

	control chan func(*tempdbCollectorState)
	stop    chan struct{}
	stopped sync.Once
}

type tempdbCollectorState struct {
	rate   time.Duration
	paused bool
}

// NewTempDBCollector creates a collector. onSample is called once per
// successful tick, onError once per failed one; either may be nil.
func NewTempDBCollector(db *sql.DB, onSample func(TempDBSample), onError func(error)) *TempDBCollector {
	return &TempDBCollector{
		db:       db,
		onSample: onSample,
		onError:  onError,
		control:  make(chan func(*tempdbCollectorState), 8),
		stop:     make(chan struct{}),
	}
}

// Run collects until ctx is cancelled or Stop is called. It blocks, so the
// caller runs it on its own goroutine.
func (c *TempDBCollector) Run(ctx context.Context, rate time.Duration) {
	if ok, err := HasViewServerState(ctx, c.db); err != nil {
		c.fail(err)
		return
	} else if !ok {
		c.fail(ErrNoPermission)
		return
	}

	state := tempdbCollectorState{rate: rate}
	ticker := time.NewTicker(state.rate)
	defer ticker.Stop()

	var prev *tempdbSnapshot
	tick := func() {
		cur, err := collectTempDB(ctx, c.db)
		if err != nil {
			c.fail(err)
			return
		}
		if c.onSample != nil {
			c.onSample(deriveTempDB(prev, cur))
		}
		prev = cur
	}
	// Immediately, like Collector: a tab that ticks every 30 seconds would
	// otherwise sit empty for half a minute after it is opened.
	tick()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case apply := <-c.control:
			was := state.rate
			apply(&state)
			if state.rate != was && state.rate > 0 {
				ticker.Reset(state.rate)
			}
		case <-ticker.C:
			if !state.paused {
				tick()
			}
		}
	}
}

// SetRate changes the collection interval, taking effect from the next tick.
// Safe to call from any goroutine.
func (c *TempDBCollector) SetRate(d time.Duration) {
	c.send(func(s *tempdbCollectorState) { s.rate = d })
}

// SetPaused stops or resumes collection without dropping what has already
// been collected. Safe to call from any goroutine.
func (c *TempDBCollector) SetPaused(v bool) {
	c.send(func(s *tempdbCollectorState) { s.paused = v })
}

// Stop ends the collector. Idempotent, and safe to call from any goroutine.
func (c *TempDBCollector) Stop() {
	c.stopped.Do(func() { close(c.stop) })
}

func (c *TempDBCollector) send(apply func(*tempdbCollectorState)) {
	select {
	case c.control <- apply:
	case <-c.stop:
	}
}

func (c *TempDBCollector) fail(err error) {
	if c.onError != nil {
		c.onError(err)
	}
}
