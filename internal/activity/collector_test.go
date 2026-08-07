package activity

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
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
