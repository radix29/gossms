package activity

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// ErrNoPermission is returned before any collection is attempted when the
// connection lacks VIEW SERVER STATE. The DMV queries would otherwise
// return empty result sets rather than errors, so a dashboard with no
// permission would be indistinguishable from a completely idle server.
var ErrNoPermission = errors.New("VIEW SERVER STATE permission is required to read activity")

// collectorState is the part of a collector only its own goroutine touches.
// Rate and pause changes arrive as functions over it on the control channel,
// so nothing here is read from two goroutines at once.
type collectorState struct {
	rate   time.Duration
	paused bool
}

// maxFailureBackoff caps how far a failing collector backs its retries off.
const maxFailureBackoff = 5 * time.Minute

// defaultRate is what a collector ticks at when it is asked for a
// non-positive interval. It matches the first entry of the panel's rate
// selector, so a fallback looks like the default the user would have seen
// anyway.
const defaultRate = 2 * time.Second

// normalizeRate keeps collectorState.rate positive.
//
// This is a precondition, not defensive padding: time.NewTicker and
// Ticker.Reset both *panic* on a non-positive duration, and a panic on the
// collector goroutine takes the application down, not the panel. backoff's
// doubling loop also only terminates because the rate it doubles is
// positive.
func normalizeRate(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRate
	}
	return d
}

// backoff is the interval a collector should tick at after n consecutive
// failed probes: the configured rate doubled once per failure, capped at
// maxFailureBackoff. n <= 0 is the rate itself.
//
// This exists because a probe failure does not stop the collector and
// usually should not: a single failed tick is worth retrying. But nothing
// else bounds the retries either — a dropped network connection leaves the
// collector's context very much alive, and the panel only reports
// ErrNoPermission as fatal, so without this a panel left open against an
// unreachable server fired a failing round trip every 2 seconds for as long
// as it stayed open.
//
// A non-positive rate is returned unchanged rather than doubled: doubling
// it would never reach the cap, so the loop below would never terminate.
// Run normalizes its rate so this cannot happen from there, but the guard
// is the loop's own precondition and stays with it.
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

// collector is the ticking half of Collector and TempDBCollector. The two
// differ only in what one tick reads (probe) and what it turns two readings
// into (derive); the permission prologue, the select loop, the control
// channel and the stop latch are one implementation shared by both, so a fix
// to any of them is written once. The exported types are thin wrappers over
// it, which is what keeps their callbacks concretely typed.
type collector[S, Snap any] struct {
	db       *sql.DB
	probe    func(context.Context, *sql.DB) (*Snap, error)
	derive   func(prev, cur *Snap) S
	onSample func(S)
	onError  func(error)

	control chan func(*collectorState)
	stop    chan struct{}
	stopped sync.Once
}

// newCollector builds the shared half. control's buffer absorbs a burst of
// toolbar clicks without making the UI goroutine wait on a tick.
func newCollector[S, Snap any](db *sql.DB,
	probe func(context.Context, *sql.DB) (*Snap, error),
	derive func(prev, cur *Snap) S,
	onSample func(S), onError func(error)) collector[S, Snap] {
	return collector[S, Snap]{
		db:       db,
		probe:    probe,
		derive:   derive,
		onSample: onSample,
		onError:  onError,
		control:  make(chan func(*collectorState), 8),
		stop:     make(chan struct{}),
	}
}

// Run collects until ctx is cancelled or Stop is called. It blocks, so the
// caller runs it on its own goroutine. A non-positive rate falls back to
// defaultRate rather than panicking the collector goroutine.
//
// The permission check happens first and once: without VIEW SERVER STATE
// every subsequent tick would read empty DMVs forever, so the collector
// reports ErrNoPermission and stops rather than drawing an idle-looking
// server.
//
// Stopping itself on the way out is what makes send's `case <-c.stop`
// escape hatch real: without it, a Run that returned early (the permission
// prologue below, or ctx being cancelled when the connection drops) leaves
// stop open forever, and SetRate/SetPaused — called straight from the UI
// goroutine — block for good once control's buffer fills. That froze the
// whole application on the ninth toolbar click.
//
// Every exit here is silent apart from the one that reports an error, so a
// caller that shows "collecting" has to learn from Run *returning* that it
// no longer is — see ActivityMonitor.startCollector, which posts its
// stopped-callback after this returns.
//
// A probe that fails does not end the run — one bad tick is worth retrying —
// but consecutive failures back the interval off (see backoff), so a panel
// left open against a server it can no longer reach stops hammering it. The
// first success resets the interval to the configured rate.
func (c *collector[S, Snap]) Run(ctx context.Context, rate time.Duration) {
	defer c.Stop()

	if ok, err := HasViewServerState(ctx, c.db); err != nil {
		c.fail(err)
		return
	} else if !ok {
		c.fail(ErrNoPermission)
		return
	}

	state := collectorState{rate: normalizeRate(rate)}
	ticker := time.NewTicker(state.rate)
	defer ticker.Stop()

	// fails counts consecutive failed probes, and running is the interval
	// the ticker was last set to. retune reconciles the two: it is the only
	// place the ticker is reset, so the backed-off interval and a rate the
	// user picked can't each clobber the other. It resets unguarded because
	// state.rate is normalized at both of the two places it is written.
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
		cur, err := c.probe(ctx, c.db)
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
	// The first reading is taken immediately: the panel would otherwise
	// show nothing at all for a whole interval after it opens, and the
	// first sample is the one that gives the second one something to be a
	// rate against.
	tick()
	retune()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case apply := <-c.control:
			// A rate change does not clear an active backoff: the rate
			// selector is not a Retry button, and treating it as one would
			// let a user hammer an unreachable server by clicking it. Retry
			// starts a whole new collector, which begins at fails == 0.
			apply(&state)
			state.rate = normalizeRate(state.rate)
			retune()
		case <-ticker.C:
			if !state.paused {
				tick()
				retune()
			}
		}
	}
}

// SetRate changes the collection interval, taking effect from the next
// tick. A non-positive interval falls back to defaultRate. Safe to call
// from any goroutine.
func (c *collector[S, Snap]) SetRate(d time.Duration) {
	c.send(func(s *collectorState) { s.rate = d })
}

// SetPaused stops or resumes collection without dropping what has already
// been collected. Safe to call from any goroutine.
func (c *collector[S, Snap]) SetPaused(v bool) {
	c.send(func(s *collectorState) { s.paused = v })
}

// Stop ends the collector. Idempotent, and safe to call from any goroutine.
func (c *collector[S, Snap]) Stop() {
	c.stopped.Do(func() { close(c.stop) })
}

// send queues a state change, dropping it if the collector has already
// stopped — a rate change racing with the panel closing is not worth
// blocking the UI goroutine on.
func (c *collector[S, Snap]) send(apply func(*collectorState)) {
	select {
	case c.control <- apply:
	case <-c.stop:
	}
}

// fail reports an error to the caller, if it wanted them.
func (c *collector[S, Snap]) fail(err error) {
	if c.onError != nil {
		c.onError(err)
	}
}

// Collector ticks against one connection, turning each reading into a
// Sample. It owns no UI: results are handed to the callbacks, which the
// caller is responsible for marshalling onto whatever goroutine its own
// display requires.
//
// Exactly one collector feeds both dashboards — the Sample tab draws the
// newest sample in the same store History plots — so its rate and its
// paused state govern both.
type Collector struct {
	collector[Sample, Snapshot]
}

// NewCollector creates a collector. onSample is called once per successful
// tick, onError once per failed one; either may be nil. The collection rate
// is Run's argument, not this one's — a rate here would have to be either
// ignored or silently overridden by Run's, and the first is what it was.
func NewCollector(db *sql.DB, onSample func(Sample), onError func(error)) *Collector {
	return new(Collector{newCollector(db, Collect, Derive, onSample, onError)})
}
