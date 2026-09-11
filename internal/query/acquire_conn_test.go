package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

// fakeAcquireConn simulates one pooled connection: its first ExecContext
// (acquireConn's prologue) returns execErr, if set, and flips valid false as
// go-mssqldb's connectionGood does on a connection-level failure.
// sql.DB.putConn checks driver.Validator.IsValid to decide whether to keep a
// released connection — the mechanism acquireConn's eviction relies on.
type fakeAcquireConn struct {
	execErr error
	valid   bool
	used    bool
}

func (c *fakeAcquireConn) Prepare(string) (driver.Stmt, error) { return nil, errFakeAcquireUnsupported }
func (c *fakeAcquireConn) Close() error                        { return nil }
func (c *fakeAcquireConn) Begin() (driver.Tx, error)           { return nil, errFakeAcquireUnsupported }
func (c *fakeAcquireConn) IsValid() bool                       { return c.valid }

func (c *fakeAcquireConn) ExecContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	c.used = true
	if c.execErr != nil {
		c.valid = false
		return nil, c.execErr
	}
	return driver.RowsAffected(0), nil
}

var (
	_ driver.Conn          = (*fakeAcquireConn)(nil)
	_ driver.Validator     = (*fakeAcquireConn)(nil)
	_ driver.ExecerContext = (*fakeAcquireConn)(nil)
)

var errFakeAcquireUnsupported = errors.New("fakeAcquireConn: unsupported")

// fakeAcquireConnector hands out conns from a queue, one per dial, as sql.DB
// dials when no idle connection remains (e.g. after an eviction).
type fakeAcquireConnector struct {
	conns []*fakeAcquireConn
	next  int
}

func (f *fakeAcquireConnector) Connect(context.Context) (driver.Conn, error) {
	if f.next >= len(f.conns) {
		return nil, errors.New("fakeAcquireConnector: exhausted")
	}
	c := f.conns[f.next]
	f.next++
	return c, nil
}
func (f *fakeAcquireConnector) Driver() driver.Driver { return fakeAcquireDriver{} }

type fakeAcquireDriver struct{}

func (fakeAcquireDriver) Open(string) (driver.Conn, error) { return nil, errFakeAcquireUnsupported }

func TestAcquireConnRetriesDeadPooledConnection(t *testing.T) {
	dead := &fakeAcquireConn{valid: true, execErr: driver.ErrBadConn}
	healthy := &fakeAcquireConn{valid: true}
	db := sql.OpenDB(&fakeAcquireConnector{conns: []*fakeAcquireConn{dead, healthy}})
	defer db.Close()

	conn, err := acquireConn(context.Background(), db, "")
	if err != nil {
		t.Fatalf("acquireConn: %v", err)
	}
	defer conn.Close()

	if !dead.used {
		t.Error("dead conn was never tried")
	}
	if !healthy.used {
		t.Error("healthy conn was never tried — acquireConn didn't retry after the dead one")
	}
}

func TestAcquireConnUsesDatabase(t *testing.T) {
	healthy := &fakeAcquireConn{valid: true}
	db := sql.OpenDB(&fakeAcquireConnector{conns: []*fakeAcquireConn{healthy}})
	defer db.Close()

	conn, err := acquireConn(context.Background(), db, "mydb")
	if err != nil {
		t.Fatalf("acquireConn: %v", err)
	}
	defer conn.Close()

	if !healthy.used {
		t.Error("connection's prologue was never run")
	}
}

func TestAcquireConnExhaustsRetries(t *testing.T) {
	conns := make([]*fakeAcquireConn, acquireConnRetryAttempts)
	for i := range conns {
		conns[i] = &fakeAcquireConn{valid: true, execErr: driver.ErrBadConn}
	}
	db := sql.OpenDB(&fakeAcquireConnector{conns: conns})
	defer db.Close()

	_, err := acquireConn(context.Background(), db, "")
	if err == nil {
		t.Fatal("want an error after exhausting retries, got nil")
	}
	for i, c := range conns {
		if !c.used {
			t.Errorf("conn %d was never tried", i)
		}
	}
}

func TestAcquireConnExhaustsRetriesWrapsDatabaseError(t *testing.T) {
	conns := make([]*fakeAcquireConn, acquireConnRetryAttempts)
	for i := range conns {
		conns[i] = &fakeAcquireConn{valid: true, execErr: driver.ErrBadConn}
	}
	db := sql.OpenDB(&fakeAcquireConnector{conns: conns})
	defer db.Close()

	_, err := acquireConn(context.Background(), db, "mydb")
	if err == nil {
		t.Fatal("want an error after exhausting retries, got nil")
	}
	if want := "switch to database mydb"; err.Error()[:len(want)] != want {
		t.Errorf("err = %q, want prefix %q", err.Error(), want)
	}
}

func TestAcquireConnStopsOnNonRetryableError(t *testing.T) {
	fatal := &fakeAcquireConn{valid: true, execErr: errors.New("permission denied")}
	db := sql.OpenDB(&fakeAcquireConnector{conns: []*fakeAcquireConn{fatal}})
	defer db.Close()

	_, err := acquireConn(context.Background(), db, "")
	if err == nil {
		t.Fatal("want the non-retryable error back, got nil")
	}
	if !fatal.used {
		t.Error("conn was never tried")
	}
}
