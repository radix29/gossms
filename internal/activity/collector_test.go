package activity

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// deadConnector fails every dial, so the permission prologue errors and Run
// returns before its select loop.
type deadConnector struct{}

func (deadConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("activity_test: no connection")
}

func (deadConnector) Driver() driver.Driver { return nil }

// probeOnceConnector answers the permission prologue and fails every other
// query: an unreachable server with a live context.
type probeOnceConnector struct{}

func (probeOnceConnector) Connect(context.Context) (driver.Conn, error) {
	return probeOnceConn{}, nil
}

func (probeOnceConnector) Driver() driver.Driver { return nil }

type probeOnceConn struct{}

func (probeOnceConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("activity_test: prepare unsupported")
}

func (probeOnceConn) Close() error { return nil }

func (probeOnceConn) Begin() (driver.Tx, error) {
	return nil, errors.New("activity_test: transactions unsupported")
}

func (probeOnceConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if q == permissionQuery {
		return &oneIntRows{v: 1}, nil
	}
	return nil, errors.New("activity_test: server unreachable")
}

// oneIntRows is a single row with a single integer column.
type oneIntRows struct {
	v    int64
	done bool
}

func (r *oneIntRows) Columns() []string { return []string{"ok"} }

func (r *oneIntRows) Close() error { return nil }

func (r *oneIntRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.v
	return nil
}

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
// rate forever (at 1ms, ~150 round trips in this window instead of ~8).
func TestCollectorBacksOffWhenEveryProbeFails(t *testing.T) {
	db := sql.OpenDB(probeOnceConnector{})
	defer db.Close()

	var mu sync.Mutex
	probes := 0
	c := NewCollector(db, nil, func(error) {
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
	// 1ms doubling reaches 128ms by the eighth failure; the bound allows for
	// jitter and is still far below ~150.
	if got > 20 {
		t.Errorf("%d failing probes in 150ms at a 1ms rate; the retries are not backing off", got)
	}
}

// After Run returns, SetRate/SetPaused must not block: they run on the UI
// goroutine and control's buffer is small.
func TestCollectorSendAfterRunReturns(t *testing.T) {
	db := sql.OpenDB(deadConnector{})
	defer db.Close()

	var failed error
	c := NewCollector(db, nil, func(err error) { failed = err })
	c.Run(context.Background(), time.Second)
	if failed == nil {
		t.Fatal("Run returned without reporting the failed prologue")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Well past control's buffer.
		for i := 0; i < 64; i++ {
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
	db := sql.OpenDB(deadConnector{})
	defer db.Close()

	var failed error
	c := NewTempDBCollector(db, nil, func(err error) { failed = err })
	c.Run(context.Background(), time.Second)
	if failed == nil {
		t.Fatal("Run returned without reporting the failed prologue")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 64; i++ {
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
	db := sql.OpenDB(probeOnceConnector{})
	defer db.Close()

	for _, rate := range []time.Duration{0, -time.Second} {
		var mu sync.Mutex
		probes := 0
		c := NewCollector(db, nil, func(error) {
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
}

// SetRate(0) reaches Ticker.Reset, which also panics; normalization on the
// control path is the only guard.
func TestCollectorSetRateSurvivesANonPositiveRate(t *testing.T) {
	db := sql.OpenDB(probeOnceConnector{})
	defer db.Close()

	c := NewCollector(db, nil, func(error) {})

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
}

// A SetRate must reach the ticker. Checked by timing, since a SetRate that
// updates state but never resets the ticker is otherwise invisible.
func TestCollectorSetRateReachesTheTicker(t *testing.T) {
	db := sql.OpenDB(probeOnceConnector{})
	defer db.Close()

	var mu sync.Mutex
	probes := 0
	c := NewCollector(db, nil, func(error) {
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
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	before := probes
	mu.Unlock()

	c.SetRate(time.Millisecond)
	time.Sleep(150 * time.Millisecond)

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
}

// Cancelling ctx (the dropped-connection path) must also close stop.
func TestCollectorSendAfterContextCancel(t *testing.T) {
	db := sql.OpenDB(deadConnector{})
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewCollector(db, nil, func(error) {})
	c.Run(ctx, time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 64; i++ {
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
	db := sql.OpenDB(probeOnceConnector{})
	defer db.Close()

	var mu sync.Mutex
	probes := 0
	c := NewCollector(db, nil, func(error) {
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
	time.Sleep(20 * time.Millisecond)
	paused := count()
	time.Sleep(100 * time.Millisecond)
	if got := count(); got != paused {
		t.Errorf("%d probes ran while paused (was %d); Pause did not reach the tick", got-paused, paused)
	}

	c.SetPaused(false)
	deadline := time.After(time.Second)
	for count() == paused {
		select {
		case <-deadline:
			t.Fatal("no probe ran after resuming; SetPaused(false) did not reach the tick")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	c.Stop()
	<-done
}

// Stop must end a paused collector too; pausing removes the tick, so Stop can't
// be handled only there.
func TestCollectorStopEndsAPausedRun(t *testing.T) {
	db := sql.OpenDB(probeOnceConnector{})
	defer db.Close()

	c := NewCollector(db, nil, func(error) {})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(context.Background(), time.Millisecond)
	}()

	c.SetPaused(true)
	time.Sleep(20 * time.Millisecond)
	c.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop while paused")
	}
	c.Stop() // idempotent: a second close would panic
}
