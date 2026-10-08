package activity

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNoPermission is returned before collecting when the connection lacks VIEW
// SERVER STATE. The DMVs return empty sets rather than errors, so otherwise
// no-permission looks like an idle server.
var ErrNoPermission = errors.New("VIEW SERVER STATE permission is required to read activity")

// collectorState is touched only by the collector's goroutine; rate and pause
// changes arrive through pending (see collector.pending).
type collectorState struct {
	rate   time.Duration
	paused bool
}

// maxFailureBackoff caps a failing collector's retry backoff.
const maxFailureBackoff = 5 * time.Minute

// defaultRate is used for a non-positive interval; it matches the panel's first
// rate option.
const defaultRate = 2 * time.Second

// normalizeRate keeps collectorState.rate positive. Required: time.NewTicker
// and Ticker.Reset panic on non-positive durations (taking the app down), and
// backoff's doubling only terminates for a positive rate.
func normalizeRate(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRate
	}
	return d
}

// backoff is the tick interval after n consecutive failed probes: rate doubled
// per failure, capped at maxFailureBackoff; n <= 0 is rate itself. It bounds
// retries against an unreachable server — a dropped connection doesn't cancel
// the context and only ErrNoPermission is fatal.
//
// A non-positive rate is returned unchanged, since doubling it never reaches
// the cap.
func backoff(rate time.Duration, n int) time.Duration {
	if rate <= 0 || n <= 0 {
		return rate
	}
	d := rate
	for range n {
		if d >= maxFailureBackoff {
			break
		}
		d *= 2
	}
	return min(d, maxFailureBackoff)
}

// collector is the ticking half of Collector and TempDBCollector, which differ
// only in what a tick reads (probe) and what two readings become (derive). The
// exported types are thin wrappers so their callbacks stay concretely typed.
type collector[S, Snap any] struct {
	src      Source
	probe    func(context.Context, Source) (*Snap, error)
	derive   func(prev, cur *Snap) S
	onSample func(S)
	onError  func(error)

	// pending holds the latest SetRate/SetPaused not yet taken by Run, and
	// changed (one slot, sent to without blocking) says there is some. A
	// burst of toolbar clicks coalesces into the newest values instead of
	// queueing: a queue drained only between probes filled during a stalled
	// DMV read, and the next click then blocked the UI goroutine until the
	// read returned.
	mu      sync.Mutex
	pending pendingState
	changed chan struct{}

	stop    chan struct{}
	stopped sync.Once
}

// pendingState is the changes asked for since Run last looked; nil leaves
// that part of the state alone.
type pendingState struct {
	rate   *time.Duration
	paused *bool
}

// newCollector builds the shared half.
func newCollector[S, Snap any](src Source,
	probe func(context.Context, Source) (*Snap, error),
	derive func(prev, cur *Snap) S,
	onSample func(S), onError func(error)) collector[S, Snap] {
	return collector[S, Snap]{
		src:      src,
		probe:    probe,
		derive:   derive,
		onSample: onSample,
		onError:  onError,
		changed:  make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}
}

// Run collects until ctx is cancelled or Stop is called; it blocks. A
// non-positive rate falls back to defaultRate.
//
// The permission check runs once, first: without VIEW SERVER STATE the
// collector reports ErrNoPermission and stops rather than draw an idle-looking
// server.
//
// SetRate/SetPaused never block, running or not (see pending). Exits are
// silent except the error one, so callers learn from Run returning.
//
// Failed probes don't end the run, but consecutive failures back off the
// interval (see backoff); the first success resets it.
func (c *collector[S, Snap]) Run(ctx context.Context, rate time.Duration) {
	defer c.Stop()

	if ok, err := c.src.HasViewServerState(ctx); err != nil {
		c.fail(err)
		return
	} else if !ok {
		c.fail(ErrNoPermission)
		return
	}

	state := collectorState{rate: normalizeRate(rate)}
	ticker := time.NewTicker(state.rate)
	defer ticker.Stop()

	// fails counts consecutive failed probes; running is the ticker's current
	// interval. retune is the only place the ticker is reset, so backoff and
	// user-picked rate can't clobber each other. Unguarded because state.rate
	// is normalized wherever written.
	fails, running := 0, state.rate
	retune := func() {
		d := backoff(state.rate, fails)
		if d == running {
			return
		}
		running = d
		ticker.Reset(d)
	}

	var prev *Snap
	tick := func() {
		cur, err := c.probe(ctx, c.src)
		if err != nil {
			c.fail(err)
			fails++
			return
		}
		fails = 0
		if c.onSample != nil {
			c.onSample(c.derive(prev, cur))
		}
		prev = cur
	}
	// Read immediately so the panel isn't blank for an interval, and so the
	// second sample has a baseline.
	tick()
	retune()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-c.changed:
			// A rate change doesn't clear backoff; Retry starts a new
			// collector.
			c.mu.Lock()
			p := c.pending
			c.pending = pendingState{}
			c.mu.Unlock()
			if p.rate != nil {
				state.rate = normalizeRate(*p.rate)
			}
			if p.paused != nil {
				state.paused = *p.paused
			}
			retune()
		case <-ticker.C:
			if !state.paused {
				tick()
				retune()
			}
		}
	}
}

// SetRate changes the interval from the next tick; non-positive falls back to
// defaultRate. Safe from any goroutine.
func (c *collector[S, Snap]) SetRate(d time.Duration) {
	c.mu.Lock()
	c.pending.rate = &d
	c.mu.Unlock()
	c.signal()
}

// SetPaused stops or resumes collection, keeping what's been collected. Safe
// from any goroutine.
func (c *collector[S, Snap]) SetPaused(v bool) {
	c.mu.Lock()
	c.pending.paused = &v
	c.mu.Unlock()
	c.signal()
}

// Stop ends the collector. Idempotent, and safe to call from any goroutine.
func (c *collector[S, Snap]) Stop() {
	c.stopped.Do(func() { close(c.stop) })
}

// signal tells Run there is a pending change. Never blocks: a full slot
// already says so, and Run reads every pending value when it takes it.
func (c *collector[S, Snap]) signal() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// fail reports err to the caller's onError, if set.
func (c *collector[S, Snap]) fail(err error) {
	if c.onError != nil {
		c.onError(err)
	}
}

// Collector ticks against one connection, turning readings into Samples
// delivered to callbacks; the caller marshals them onto its UI goroutine. One
// collector feeds both dashboards, so its rate and pause govern both.
type Collector struct {
	collector[Sample, Snapshot]
}

// NewCollector creates a collector. onSample is called per successful tick,
// onError per failed one; either may be nil. The rate is Run's argument.
func NewCollector(src Source, onSample func(Sample), onError func(error)) *Collector {
	return new(Collector{newCollector(src, Collect, Derive, onSample, onError)})
}
