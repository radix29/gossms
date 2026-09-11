package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"time"
)

// Session is one SQL Server session held as long as its owner wants — what an
// SSMS query window runs on. Temp tables, SET options, open transactions and
// USE persist between runs.
//
// Package-level Execute can't do that: database/sql marks a returned connection
// for reset, and go-mssqldb sets the TDS reset-connection bit on its next use —
// SET options to login defaults, temp tables dropped, open transactions rolled
// back. If the next Execute lands on another connection, the first sits idle
// holding the transaction's locks.
//
// One run at a time; the owner must not overlap runs. Close is safe from any
// goroutine.
type Session struct {
	conn *sql.Conn
	spid int

	// lost is set once the session is known unusable. Atomic because Close can
	// run on another goroutine.
	lost atomic.Bool
}

// SessionState is what a query window shows of its session between runs.
type SessionState struct {
	// Database is DB_NAME(): where the next run starts.
	Database string

	// TranCount is @@TRANCOUNT; non-zero means an open transaction, which
	// closing the session rolls back.
	TranCount int
}

// ErrSessionLost is what a run on a lost Session reports instead of running.
var ErrSessionLost = errors.New("the session's connection to the server was lost")

// stateReadTimeout bounds the post-run DB_NAME()/@@TRANCOUNT read, which also
// runs after a cancelled run, so can't use the run's ctx.
const stateReadTimeout = 5 * time.Second

// stateQuery reads a session's state; @@SPID is only needed by Open.
const stateQuery = "SELECT @@SPID, DB_NAME(), @@TRANCOUNT"

// Open checks a connection out of db for the Session's exclusive use, in
// database if non-empty, and reads its SPID and state. The connection is never
// returned to db; Close discards it. A dead pooled connection is retried (see
// acquireConn).
func Open(ctx context.Context, db *sql.DB, database string) (*Session, SessionState, error) {
	conn, err := acquireConn(ctx, db, database)
	if err != nil {
		return nil, SessionState{}, err
	}
	s := &Session{conn: conn}
	var st SessionState
	if err := conn.QueryRowContext(ctx, stateQuery).Scan(&s.spid, &st.Database, &st.TranCount); err != nil {
		s.Close()
		return nil, SessionState{}, err
	}
	return s, st, nil
}

// SPID is @@SPID, which SSMS shows in brackets after the login.
func (s *Session) SPID() int { return s.spid }

// Lost reports whether the session is known unusable; only a run or Close finds
// out.
func (s *Session) Lost() bool { return s.lost.Load() }

// Execute is the package-level Execute on this session, without USE: the script
// runs wherever the session is.
func (s *Session) Execute(ctx context.Context, script string, opts ...Option) *Result {
	return s.execute(ctx, script, planCaptureNone, nil, opts...)
}

// ExecuteWithPlan is Execute with the actual plan captured; see the
// package-level ExecuteWithPlan.
func (s *Session) ExecuteWithPlan(ctx context.Context, script string, opts ...Option) *Result {
	return s.execute(ctx, script, planCaptureActual, nil, opts...)
}

// ExecuteEstimatedPlan compiles script without running it; see the
// package-level version.
func (s *Session) ExecuteEstimatedPlan(ctx context.Context, script string) *Result {
	return s.execute(ctx, script, planCaptureEstimated, nil)
}

// ExecuteToSink is Execute streaming rows to sink; see the package-level
// version.
func (s *Session) ExecuteToSink(ctx context.Context, script string, sink RowSink, opts ...Option) *Result {
	return s.execute(ctx, script, planCaptureNone, sink, opts...)
}

func (s *Session) execute(ctx context.Context, script string, capture planCapture, sink RowSink, opts ...Option) *Result {
	start := time.Now()
	res := newResult(opts)
	defer func() { res.Elapsed = time.Since(start) }()

	if s.lost.Load() {
		res.addError(ErrSessionLost)
		res.SessionLost = true
		return res
	}

	ran, cleanupErr := runScript(ctx, s.conn, script, capture, sink, res)
	if cleanupErr != nil {
		// Nothing resets this connection, so a SHOWPLAN_XML left on would make
		// every later run a plan fetch. The session has to go.
		res.addError(cleanupErr)
		s.markLost()
	}
	if ran && ctx.Err() != nil {
		res.Messages = append(res.Messages, cancelledMessage)
	}

	// After runScript's deferred SET ... OFF: under SHOWPLAN_XML the read would
	// return a showplan document.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateReadTimeout)
	defer cancel()
	if st, err := s.readState(sctx); err == nil {
		res.State = &st
		res.Database = st.Database
	}

	if ran && ctx.Err() == nil && res.shouldReportSuccess(capture) {
		res.addNotice("Commands completed successfully.")
	}
	if !s.alive() {
		s.markLost()
	}
	if s.lost.Load() {
		res.SessionLost = true
		res.State = nil
	}
	return res
}

// readState reads the session's database and transaction count.
func (s *Session) readState(ctx context.Context) (SessionState, error) {
	var spid int
	var st SessionState
	err := s.conn.QueryRowContext(ctx, stateQuery).Scan(&spid, &st.Database, &st.TranCount)
	return st, err
}

// EndTransactions commits or rolls back every open transaction — what closing a
// query window does after the "commit these transactions?" prompt. Nested BEGIN
// TRANs need one COMMIT each; the loop is bounded by the starting count, so a
// failing COMMIT ends it with the error.
func (s *Session) EndTransactions(ctx context.Context, commit bool) error {
	if s.lost.Load() {
		return ErrSessionLost
	}
	stmt := "IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION"
	if commit {
		stmt = "DECLARE @n int = @@TRANCOUNT\n" +
			"WHILE @n > 0 AND @@TRANCOUNT > 0\n" +
			"BEGIN\n" +
			"    COMMIT TRANSACTION\n" +
			"    SET @n -= 1\n" +
			"END"
	}
	_, err := s.conn.ExecContext(ctx, stmt)
	if !s.alive() {
		s.markLost()
	}
	return err
}

// alive reports whether the driver considers the connection usable (go-mssqldb
// clears connectionGood on I/O or protocol failure, killed session, failover).
// A connection database/sql already closed reports sql.ErrConnDone.
func (s *Session) alive() bool {
	return s.conn.Raw(func(dc any) error {
		if v, ok := dc.(driver.Validator); ok && !v.IsValid() {
			return driver.ErrBadConn
		}
		return nil
	}) == nil
}

func (s *Session) markLost() { s.lost.Store(true) }

// Close ends the session. The connection is discarded, not pooled: pooled, it
// would sit idle holding the open transaction's locks; discarded, the server
// rolls it back.
//
// Close waits for an in-flight run, so cancel it first and call Close off the
// UI goroutine if needed. Idempotent.
func (s *Session) Close() {
	s.markLost()
	// driver.ErrBadConn from Raw is database/sql's way to close rather than
	// pool a connection. Raw on a closed Conn returns sql.ErrConnDone, giving
	// idempotence.
	_ = s.conn.Raw(func(any) error { return driver.ErrBadConn })
}
