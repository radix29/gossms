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

// deadConnector fails every dial, so a collector's HasViewServerState
// prologue errors and Run returns before it ever reaches its select loop —
// the shape of a real permission failure or a dropped connection.
type deadConnector struct{}

func (deadConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("activity_test: no connection")
}

func (deadConnector) Driver() driver.Driver { return nil }

// probeOnceConnector answers the VIEW SERVER STATE prologue and fails every
// other query, so a collector gets past Run's permission check and then has
// every probe fail — the shape of a server that has become unreachable
// without the collector's context being cancelled.
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

// oneIntRows is a single row holding a single integer column.
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

// backoff's schedule is the whole of the retry policy, so it is pinned
// directly rather than inferred from timings.
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
	// A non-positive rate must come back unchanged: doubling it would never
	// reach the cap, so the loop that does the doubling would never end.
	if got := backoff(0, 5); got != 0 {
		t.Errorf("backoff(0, 5) = %v, want 0", got)
	}
	if got := backoff(-time.Second, 5); got != -time.Second {
		t.Errorf("backoff(-1s, 5) = %v, want -1s", got)
	}
}

// A collector whose probes all fail must back off rather than retry at the
// configured rate forever. A dropped connection does not cancel the
// collector's context and only ErrNoPermission stops the panel, so before
// the backoff this fired a failing round trip every rate interval for as
// long as the panel stayed open — at rate = 1ms, ~150 of them in the window
// below rather than the ~8 a doubling schedule allows.
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
	// 1ms doubling reaches 128ms by the eighth failure, so the window holds
	// eight retries. The bound is loose enough to absorb scheduler jitter and
	// still an order of magnitude below the un-backed-off ~150.
	if got > 20 {
		t.Errorf("%d failing probes in 150ms at a 1ms rate; the retries are not backing off", got)
	}
}

// A Run that has returned must not leave SetRate/SetPaused blocking. Both
// are called from the UI goroutine, and control's buffer is small, so a
// Run that returned without closing stop froze the whole application on the
// ninth toolbar click.
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
		// Well past control's buffer, so an unclosed stop blocks here.
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

// A zero rate must not take the process down. time.NewTicker panics on a
// non-positive duration, and Run is called on its own goroutine from
// App.safego, so the panic is the application's, not the panel's.
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
		// The immediate first reading still happens; only the ticker interval
		// changed, so a collector asked for a bad rate still collects.
		if got == 0 {
			t.Errorf("rate %v: Run took no reading at all", rate)
		}
	}
}

// SetRate(0) reaches the same ticker through Ticker.Reset, which panics on
// the same input. retune resets unguarded, so the normalization on the
// control path is the only thing standing between a caller's bad rate and a
// crashed collector goroutine.
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

// A rate the user picks has to reach the ticker. retune is the only place
// that reset happens and it is shared with the backoff, so this pins the
// control path by timing rather than by inspecting state: a SetRate that
// updates collectorState but never resets the ticker is invisible to any
// other kind of assertion.
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

	// Let the immediate first reading land, then count only what the ticker
	// produces after the rate change.
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

	// At a 1s rate the window holds no tick at all, so anything here is the
	// reset having taken effect. The probes fail, so the backoff stretches
	// the interval as they go — the bound is "more than none", not a count.
	if after <= before {
		t.Errorf("%d probes before the rate change, %d after; SetRate did not reset the ticker", before, after)
	}
}

// Run must also stop the collector when ctx is cancelled rather than Stop
// being called — the connection-dropped path, which is the other way stop
// was left open.
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

// A paused collector must stop reading and resume where it left off. The
// toolbar's Pause is the only thing between a user reading a stalled server's
// numbers and the panel replacing them a second later, so a SetPaused that
// updates the state but never reaches the tick is the whole feature missing.
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

	// The probes fail, so the interval backs off as they go; the pause has to
	// land while the collector is still ticking fast.
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

// Stop has to end a *paused* collector too. Pausing takes the tick out of
// the loop, so a Stop that were handled only inside the tick would leave the
// goroutine running for the process's lifetime and the panel's
// stopped-callback never posted.
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
