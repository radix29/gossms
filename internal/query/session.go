package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"time"
)

// Session is one SQL Server session held for as long as its owner wants it —
// what an SSMS query window runs on. Every Execute on it lands on the same
// session, so a temp table, a SET option, an open transaction or a USE left by
// one run is still there for the next.
//
// The package-level Execute functions cannot give that: each checks a
// connection out of the pool and returns it, and database/sql marks a returned
// connection for reset, which go-mssqldb carries out on the next checkout by
// setting the TDS reset-connection bit on the first batch — every SET option
// back to the login default, temp tables dropped, an open transaction rolled
// back. The pool reuses the most recently returned connection first, so the
// next Execute usually lands on the very session it just reset; when it lands
// on another, the first sits idle holding the transaction's locks.
//
// A Session runs one call at a time; its owner must not start a run while
// another is in flight. Close may be called from any goroutine.
type Session struct {
	conn *sql.Conn
	spid int

	// lost is set once the session is known to be unusable. Atomic because
	// Close can come from a goroutine other than the one running.
	lost atomic.Bool
}

// SessionState is what a query window shows of its session between runs.
type SessionState struct {
	// Database is DB_NAME() — where the next run starts, a mid-script USE
	// included.
	Database string

	// TranCount is @@TRANCOUNT: non-zero means a transaction is open, which
	// closing the session rolls back.
	TranCount int
}

// ErrSessionLost is what a run on a lost Session reports instead of running.
var ErrSessionLost = errors.New("the session's connection to the server was lost")

// stateReadTimeout bounds the read of DB_NAME()/@@TRANCOUNT after each run.
// It runs even when the run was cancelled, so it cannot use the run's ctx.
const stateReadTimeout = 5 * time.Second

// stateQuery reads a session's state. @@SPID is fixed for its lifetime, and
// read here only because Open needs it from the same round trip.
const stateQuery = "SELECT @@SPID, DB_NAME(), @@TRANCOUNT"

// Open checks a connection out of db for the Session's exclusive use, in
// database if non-empty (else wherever the login lands), and reads its SPID
// and starting state. The connection never goes back to db: Close discards it.
//
// A dead pooled connection is retried against a fresh one, as for Execute —
// see acquireConn.
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

// SPID is the session's server process id, @@SPID — what SSMS shows in
// brackets after the login.
func (s *Session) SPID() int { return s.spid }

// Lost reports whether the session is known to be unusable. Only a run, or
// Close, finds that out.
func (s *Session) Lost() bool { return s.lost.Load() }

// Execute is the package-level Execute on this session: no USE is issued, the
// script runs wherever the session currently is.
func (s *Session) Execute(ctx context.Context, script string, opts ...Option) *Result {
	return s.execute(ctx, script, planCaptureNone, nil, opts...)
}

// ExecuteWithPlan is Execute with the actual plan captured — see the
// package-level ExecuteWithPlan.
func (s *Session) ExecuteWithPlan(ctx context.Context, script string, opts ...Option) *Result {
	return s.execute(ctx, script, planCaptureActual, nil, opts...)
}

// ExecuteEstimatedPlan compiles script without running it — see the
// package-level ExecuteEstimatedPlan.
func (s *Session) ExecuteEstimatedPlan(ctx context.Context, script string) *Result {
	return s.execute(ctx, script, planCaptureEstimated, nil)
}

// ExecuteToSink is Execute streaming its rows to sink — see the package-level
// ExecuteToSink.
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
		// Unlike a pooled connection, nothing resets this one: a
		// SHOWPLAN_XML left on would turn every later Execute into a plan
		// fetch that runs nothing. The session has to go.
		res.addError(cleanupErr)
		s.markLost()
	}
	if ran && ctx.Err() != nil {
		res.Messages = append(res.Messages, cancelledMessage)
	}

	// After runScript's deferred SET ... OFF, never before: under
	// SHOWPLAN_XML the read would come back as a showplan document.
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

// EndTransactions commits or rolls back every transaction open on the session
// — what a query window does when it is closed with one open and the user
// answers the "commit these transactions?" prompt. Nested BEGIN TRANs need one
// COMMIT each; the loop is bounded by the count at the start, so a COMMIT that
// fails ends it with the error rather than spinning.
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

// alive reports whether the driver still considers the connection usable —
// go-mssqldb clears its connectionGood flag on any I/O or protocol failure,
// a killed session or a failover included. A connection database/sql has
// already closed (it does on driver.ErrBadConn) reports sql.ErrConnDone here.
func (s *Session) alive() bool {
	return s.conn.Raw(func(dc any) error {
		if v, ok := dc.(driver.Validator); ok && !v.IsValid() {
			return driver.ErrBadConn
		}
		return nil
	}) == nil
}

func (s *Session) markLost() { s.lost.Store(true) }

// Close ends the session. The connection is discarded rather than returned to
// the pool — returned, it would sit idle with the session's open transaction
// and its locks until the pool happened to reuse or expire it; discarded, the
// server rolls the transaction back as the session ends.
//
// Close waits for a run still in flight to return, so cancel that run first,
// and call Close off the UI goroutine if it may be one. Safe to call more than
// once.
func (s *Session) Close() {
	s.markLost()
	// A driver.ErrBadConn out of Raw is database/sql's one way to have a
	// connection closed instead of pooled. Raw on a Conn already closed
	// returns sql.ErrConnDone and does nothing, which is the idempotence.
	_ = s.conn.Raw(func(any) error { return driver.ErrBadConn })
}
