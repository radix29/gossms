package activity

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The timing tests run in a synctest bubble: the clock is fake and advances
// only when every goroutine is blocked, so a window holds exactly the ticks
// the schedule predicts, regardless of machine load or -race, and costs no
// wall time. The fake source does no I/O, which a bubble requires.

// backoff's schedule is the retry policy, so it's pinned directly.
func TestBackoffDoublesUntilCapped(t *testing.T) {
	const rate = time.Second
	for _, tc := range []struct {
		fails int
		want  time.Duration
	}{
		{0, rate},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{8, 256 * time.Second}, // the last doubling under the cap
		{9, maxFailureBackoff}, // 512s would exceed it
		{10, maxFailureBackoff},
		{1000, maxFailureBackoff},
	} {
		if got := backoff(rate, tc.fails); got != tc.want {
			t.Errorf("backoff(%v, %d) = %v, want %v", rate, tc.fails, got, tc.want)
		}
	}
	// A non-positive rate comes back unchanged; doubling it would loop forever.
	if got := backoff(0, 5); got != 0 {
		t.Errorf("backoff(0, 5) = %v, want 0", got)
	}
	if got := backoff(-time.Second, 5); got != -time.Second {
		t.Errorf("backoff(-1s, 5) = %v, want -1s", got)
	}
}

// A collector whose probes all fail must back off, not retry at the configured
// rate forever (at 1ms, ~150 round trips in this window instead of 7).
func TestCollectorBacksOffWhenEveryProbeFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := probeOnceSource()

		var mu sync.Mutex
		probes := 0
		c := NewCollector(src, nil, func(error) {
			mu.Lock()
			probes++
			mu.Unlock()
		})

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		c.Run(ctx, time.Millisecond)

		mu.Lock()
		got := probes
		mu.Unlock()

		if got == 0 {
			t.Fatal("no probe ran: the permission prologue did not pass")
		}
		// The immediate probe, then ticks at 2, 6, 14, 30, 62 and 126ms; the
		// next is due at 254ms.
		if got != 7 {
			t.Errorf("%d failing probes in 150ms at a 1ms rate, want 7; the retries are not backing off on schedule", got)
		}
	})
}

// After Run returns, SetRate/SetPaused must not block: they run on the UI
// goroutine.
func TestCollectorSendAfterRunReturns(t *testing.T) {
	src := deadSource()

	var failed error
	c := NewCollector(src, nil, func(err error) { failed = err })
	c.Run(context.Background(), time.Second)
	if failed == nil {
		t.Fatal("Run returned without reporting the failed prologue")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Many, as a burst of toolbar clicks would be.
		for i := range 64 {
			c.SetPaused(i%2 == 0)
			c.SetRate(time.Duration(i+1) * time.Second)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetPaused/SetRate blocked after Run returned")
	}
}

func TestTempDBCollectorSendAfterRunReturns(t *testing.T) {
	src := deadSource()

	var failed error
	c := NewTempDBCollector(src, nil, func(err error) { failed = err })
	c.Run(context.Background(), time.Second)
	if failed == nil {
		t.Fatal("Run returned without reporting the failed prologue")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 64 {
			c.SetPaused(i%2 == 0)
			c.SetRate(time.Duration(i+1) * time.Second)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetPaused/SetRate blocked after Run returned")
	}
}

func TestNormalizeRate(t *testing.T) {
	for _, tc := range []struct {
		in, want time.Duration
	}{
		{time.Second, time.Second},
		{time.Millisecond, time.Millisecond}, // a fast rate is honoured, not floored
		{time.Hour, time.Hour},
		{0, defaultRate},
		{-time.Second, defaultRate},
	} {
		if got := normalizeRate(tc.in); got != tc.want {
			t.Errorf("normalizeRate(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if defaultRate <= 0 {
		t.Fatalf("defaultRate = %v; the fallback itself must be positive", defaultRate)
	}
}

// A zero rate must not panic: time.NewTicker panics on it, and Run's goroutine
// panicking takes down the app.
func TestCollectorRunSurvivesANonPositiveRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := probeOnceSource()

		for _, rate := range []time.Duration{0, -time.Second} {
			var mu sync.Mutex
			probes := 0
			c := NewCollector(src, nil, func(error) {
				mu.Lock()
				probes++
				mu.Unlock()
			})

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			c.Run(ctx, rate) // panicked here before the rate was normalized
			cancel()

			mu.Lock()
			got := probes
			mu.Unlock()
			// The immediate first reading still happens.
			if got == 0 {
				t.Errorf("rate %v: Run took no reading at all", rate)
			}
		}
	})
}

// SetRate(0) reaches Ticker.Reset, which also panics; normalization on the
// control path is the only guard.
func TestCollectorSetRateSurvivesANonPositiveRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := probeOnceSource()

		c := NewCollector(src, nil, func(error) {})

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			c.Run(ctx, time.Millisecond)
		}()

		c.SetRate(0)
		c.SetRate(-time.Second)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after a non-positive SetRate")
		}
	})
}

// A SetRate must reach the ticker. Checked by timing, since a SetRate that
// updates state but never resets the ticker is otherwise invisible.
func TestCollectorSetRateReachesTheTicker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := probeOnceSource()

		var mu sync.Mutex
		probes := 0
		c := NewCollector(src, nil, func(error) {
			mu.Lock()
			probes++
			mu.Unlock()
		})

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			c.Run(ctx, time.Second) // far slower than the window below
		}()

		// Let the first reading land, then count only ticks after the rate change.
		synctest.Sleep(20 * time.Millisecond)
		mu.Lock()
		before := probes
		mu.Unlock()

		c.SetRate(time.Millisecond)
		synctest.Sleep(150 * time.Millisecond)

		mu.Lock()
		after := probes
		mu.Unlock()
		c.Stop()
		<-done

		// At 1s the window holds no tick, so any is the reset taking effect.
		// Failing probes back off, so the bound is "more than none".
		if after <= before {
			t.Errorf("%d probes before the rate change, %d after; SetRate did not reset the ticker", before, after)
		}
	})
}

// Cancelling ctx (the dropped-connection path) must also close stop.
func TestCollectorSendAfterContextCancel(t *testing.T) {
	src := deadSource()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewCollector(src, nil, func(error) {})
	c.Run(ctx, time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 64 {
			c.SetPaused(true)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetPaused blocked after a cancelled Run returned")
	}
}

// Pause must stop reading and resume must restart it.
func TestCollectorPauseStopsCollectionAndResumeRestartsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := probeOnceSource()

		var mu sync.Mutex
		probes := 0
		c := NewCollector(src, nil, func(error) {
			mu.Lock()
			probes++
			mu.Unlock()
		})
		count := func() int {
			mu.Lock()
			defer mu.Unlock()
			return probes
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.Run(ctx, time.Millisecond)
		}()

		// Probes fail and back off, so pause while still ticking fast.
		c.SetPaused(true)
		synctest.Sleep(20 * time.Millisecond)
		paused := count()
		synctest.Sleep(100 * time.Millisecond)
		if got := count(); got != paused {
			t.Errorf("%d probes ran while paused (was %d); Pause did not reach the tick", got-paused, paused)
		}

		c.SetPaused(false)
		synctest.Sleep(100 * time.Millisecond)
		if count() == paused {
			t.Fatal("no probe ran after resuming; SetPaused(false) did not reach the tick")
		}

		c.Stop()
		<-done
	})
}

// Stop must end a paused collector too; pausing removes the tick, so Stop can't
// be handled only there.
func TestCollectorStopEndsAPausedRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := probeOnceSource()

		c := NewCollector(src, nil, func(error) {})
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.Run(context.Background(), time.Millisecond)
		}()

		c.SetPaused(true)
		synctest.Sleep(20 * time.Millisecond)
		c.Stop()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after Stop while paused")
		}
		c.Stop() // idempotent: a second close would panic
	})
}

// SetRate/SetPaused run on the UI goroutine. Run takes changes only between
// probes, so during a stalled DMV read a queue of them filled and the next
// toolbar click froze the UI until the read returned. They coalesce instead:
// never blocking, and the newest values win.
func TestCollectorControlNeverBlocksDuringAStalledProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		entered := make(chan struct{}, 1)
		var mu sync.Mutex
		probes := 0
		c := newCollector(probeOnceSource(), func(ctx context.Context, _ Source) (*int, error) {
			mu.Lock()
			probes++
			n := probes
			mu.Unlock()
			if n == 1 {
				entered <- struct{}{}
				<-release
			}
			return new(0), nil
		}, func(_, _ *int) int { return 0 }, nil, nil)

		done := make(chan struct{})
		go func() {
			defer close(done)
			c.Run(context.Background(), time.Hour)
		}()
		<-entered

		clicked := make(chan struct{})
		go func() {
			defer close(clicked)
			for i := range 64 {
				c.SetPaused(i%2 == 0)
				c.SetRate(time.Duration(i+1) * time.Hour)
			}
			c.SetPaused(false)
			c.SetRate(time.Millisecond)
		}()
		select {
		case <-clicked:
		case <-time.After(5 * time.Second):
			t.Fatal("SetPaused/SetRate blocked while a probe was stalled")
		}

		// The newest rate (1ms, unpaused) wins once the probe returns: the
		// stalled probe, then one per millisecond. A winning 1h rate or pause
		// would leave it at 1.
		close(release)
		synctest.Sleep(10 * time.Millisecond)
		mu.Lock()
		n := probes
		mu.Unlock()
		if n != 11 {
			t.Errorf("%d probes 10ms after the stall, want 11; the newest rate did not take effect", n)
		}
		c.Stop()
		<-done
	})
}
