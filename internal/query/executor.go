// Package query executes T-SQL scripts as SSMS does: split into GO batches, all
// run on one dedicated connection (so temp tables and SET options survive
// across batches), with PRINT output, "(n rows affected)" and errors captured
// into Result.Messages.
//
// A Session keeps its connection across scripts, as an SSMS query window does;
// the package-level Execute functions check one out of the pool per call, and
// the pool resets it before reuse.
package query

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang-sql/sqlexp"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/batch"
	gosmo "github.com/radix29/gosmo"
)

// ResultSet is one grid of data returned by a batch.
type ResultSet struct {
	Columns []string
	Rows    [][]string

	// ColumnTypes holds each column's declared type as SSMS writes it
	// ("nvarchar(50)", "decimal(18,2)"), parallel to Columns.
	ColumnTypes []string
}

// Message is one line of the Messages pane.
type Message struct {
	Text    string
	IsError bool
}

// Result is everything one Execute call produced, across all GO batches.
type Result struct {
	Sets     []ResultSet
	Messages []Message
	Elapsed  time.Duration

	// Database is the database in effect when execution finished, read off the
	// script's connection so a mid-script USE shows. Empty if unreadable (e.g.
	// cancelled).
	Database string

	// RowsWritten totals rows handed to a RowSink. Zero for Execute.
	RowsWritten int

	// sinkSets counts result sets streamed to a RowSink, empty ones included;
	// neither len(Sets) nor RowsWritten can say whether a set happened on that
	// path. See shouldReportSuccess.
	sinkSets int

	// progress is the caller's live row counter (see WithProgress), carried on
	// Result because it's already the run-scoped state runBatch/scanNext share.
	progress *Progress

	// PlanXML holds one <ShowPlanXML> document per captured statement/batch, in
	// execution order: actual plans from ExecuteWithPlan, estimated from
	// ExecuteEstimatedPlan. Execute never fills it.
	PlanXML []string

	// State is the session's state after a run on a Session, cancelled runs
	// included (they can leave a transaction open). Nil for package-level
	// functions, or when the read failed (SET NOEXEC ON left in force, a dead
	// session).
	State *SessionState

	// SessionLost reports that this run's Session is gone (connection broke, or
	// plan capture couldn't be switched off), taking its temp tables, SET
	// options and transactions; later runs fail at once. Always false outside a
	// Session.
	SessionLost bool
}

// TotalRows sums the row counts of all result sets.
func (r *Result) TotalRows() int {
	n := 0
	for _, s := range r.Sets {
		n += len(s.Rows)
	}
	return n
}

// HasErrors reports whether any message is an error.
func (r *Result) HasErrors() bool {
	for _, m := range r.Messages {
		if m.IsError {
			return true
		}
	}
	return false
}

func (r *Result) addError(err error) {
	r.Messages = append(r.Messages, ErrorMessages(err)...)
}

// ErrorMessages formats err as SSMS's Messages pane shows a failed batch: a SQL
// Server error becomes a "Msg 208, Level 16, State 1, Line 4" line plus the
// text; anything else one message. Exported so callers using gosmo directly
// report identically.
func ErrorMessages(err error) []Message {
	if se, ok := gosmo.AsSQLError(err); ok {
		msgs := []Message{{Text: se.Header(), IsError: true}}
		if se.Message != "" {
			msgs = append(msgs, Message{Text: se.Message, IsError: true})
		}
		return msgs
	}
	return []Message{{Text: err.Error(), IsError: true}}
}
func (r *Result) addNotice(s string) { r.Messages = append(r.Messages, Message{Text: s}) }

// shouldReportSuccess reports whether the run produced nothing else to say, so
// Messages gets SSMS's "Commands completed successfully."
//
// The test is whether any result set happened, not any row: Sets for Execute,
// sinkSets for ExecuteToSink. Using RowsWritten would make an empty set print
// both "(0 row(s) written)" and the success notice. planCaptureEstimated
// executes nothing, so never reports success.
func (r *Result) shouldReportSuccess(capture planCapture) bool {
	return len(r.Sets) == 0 && r.sinkSets == 0 && !r.HasErrors() && capture != planCaptureEstimated
}

// planCapture selects whether and how execute captures an execution plan.
type planCapture int

const (
	planCaptureNone      planCapture = iota
	planCaptureActual                // SET STATISTICS XML ON — batches really run
	planCaptureEstimated             // SET SHOWPLAN_XML ON — nothing really runs
)

// readsCurrentDatabase reports whether execute reads DB_NAME() back for
// Result.Database. Never under SHOWPLAN_XML: the SET ... OFF hasn't run yet, so
// the SELECT returns a showplan set and Database would get the plan XML.
// Estimated mode runs no USE anyway.
func (c planCapture) readsCurrentDatabase() bool { return c != planCaptureEstimated }

// Progress is a live row counter for a running script: the executor bumps it
// per scanned row so a caller (the query panel's "Executing..." status) can
// show progress before Result arrives. Pass it with WithProgress.
//
// Read from another goroutine, hence atomic. The zero value is ready and every
// method is nil-safe (nil means no count was requested).
type Progress struct {
	rows atomic.Int64
}

// Rows reports rows scanned so far across every result set. Safe while running.
func (p *Progress) Rows() int {
	if p == nil {
		return 0
	}
	return int(p.rows.Load())
}

// AddRow counts one scanned row. Exported so tests can advance a Progress
// without a server.
func (p *Progress) AddRow() {
	if p != nil {
		p.rows.Add(1)
	}
}

// Option adjusts one Execute call.
type Option func(*Result)

// WithProgress reports the scanned-row count into prog. Rows streamed to a
// RowSink count too.
func WithProgress(prog *Progress) Option {
	return func(res *Result) { res.progress = prog }
}

// Execute runs script against db, SSMS-style. A non-empty database is switched
// to first (USE). The script is split on GO; a failing batch is reported in
// Messages and execution continues. Cancelling ctx stops between and inside
// batches, returning the partial Result.
//
// Every row is retained in Result.Sets, uncapped; see cellArena.
func Execute(ctx context.Context, db *sql.DB, database, script string, opts ...Option) *Result {
	return execute(ctx, db, database, script, planCaptureNone, opts...)
}

// ExecuteWithPlan is Execute under SET STATISTICS XML ON, returning actual
// plans in Result.PlanXML.
func ExecuteWithPlan(ctx context.Context, db *sql.DB, database, script string, opts ...Option) *Result {
	return execute(ctx, db, database, script, planCaptureActual, opts...)
}

// ExecuteEstimatedPlan runs under SET SHOWPLAN_XML ON: SQL Server compiles each
// GO batch and returns its estimated plan in Result.PlanXML without executing,
// as SSMS's "Display Estimated Execution Plan".
func ExecuteEstimatedPlan(ctx context.Context, db *sql.DB, database, script string) *Result {
	return execute(ctx, db, database, script, planCaptureEstimated)
}

// RowSink receives result rows as scanned instead of retaining them in
// Result.Sets. Results To File writes each to CSV, so exports are bounded by
// the file, not memory.
//
// BeginSet is called before a set's first row, EndSet after its last with the
// row count. A returned error aborts that set and is reported in Messages; the
// script continues.
//
// EndSet is called for every set BeginSet was called for, including sets
// abandoned by a Row error or whose BeginSet failed, so it's the one place to
// finalise per-set state. Its count is rows that reached Row (0 if the set
// never opened).
type RowSink interface {
	BeginSet(columns []string) error
	Row(cells []string) error
	EndSet(rows int) error
}

// ExecuteToSink is Execute streaming every row to sink; Result.Sets comes back
// empty. Per-set row counts go to Messages and the total to RowsWritten.
func ExecuteToSink(ctx context.Context, db *sql.DB, database, script string, sink RowSink, opts ...Option) *Result {
	return executeWithSink(ctx, db, database, script, planCaptureNone, sink, opts...)
}

func execute(ctx context.Context, db *sql.DB, database, script string, capture planCapture, opts ...Option) *Result {
	return executeWithSink(ctx, db, database, script, capture, nil, opts...)
}

func executeWithSink(ctx context.Context, db *sql.DB, database, script string, capture planCapture, sink RowSink, opts ...Option) *Result {
	start := time.Now()
	res := newResult(opts)

	conn, err := acquireConn(ctx, db, database)
	if err != nil {
		res.addError(err)
		res.Elapsed = time.Since(start)
		return res
	}
	defer conn.Close()

	// The capture-off failure is dropped, as gosmo's capturePlan does: the
	// pool's reset on next checkout clears the SET option. A Session has no
	// reset, so treats it as fatal.
	if ran, _ := runScript(ctx, conn, script, capture, sink, res); ran {
		if ctx.Err() != nil {
			res.Messages = append(res.Messages, cancelledMessage)
		} else {
			if capture.readsCurrentDatabase() {
				if name, err := currentDatabase(ctx, conn); err == nil {
					res.Database = name
				}
			}
			if res.shouldReportSuccess(capture) {
				res.addNotice("Commands completed successfully.")
			}
		}
	}
	res.Elapsed = time.Since(start)
	return res
}

// cancelledMessage ends the Messages of a cancelled run.
var cancelledMessage = Message{Text: "Query was cancelled by user.", IsError: true}

func newResult(opts []Option) *Result {
	res := &Result{}
	for _, opt := range opts {
		opt(res)
	}
	return res
}

// planCleanupTimeout bounds the SET ... OFF ending a plan capture, which runs
// even after ctx is cancelled.
const planCleanupTimeout = 5 * time.Second

// runScript runs script's GO batches on conn in order, recording output on res,
// with the requested plan capture switched on around them. Returns false if the
// capture couldn't be switched on (already recorded on res).
//
// cleanupErr is the failure of the capture's SET ... OFF, if any. Deferred to
// run on every exit, detached from ctx's cancellation (a cancelled run most
// likely left it pending), bounded by the timeout.
func runScript(ctx context.Context, conn *sql.Conn, script string, capture planCapture, sink RowSink, res *Result) (ran bool, cleanupErr error) {
	if capture != planCaptureNone {
		setOpt, label := capture.setOption()
		if _, err := conn.ExecContext(ctx, "SET "+setOpt+" ON"); err != nil {
			res.addError(fmt.Errorf("enable %s execution plan capture: %w", label, err))
			return false, nil
		}
		defer func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), planCleanupTimeout)
			defer cancel()
			if _, err := conn.ExecContext(cctx, "SET "+setOpt+" OFF"); err != nil {
				cleanupErr = fmt.Errorf("disable %s execution plan capture: %w", label, err)
			}
		}()
	}

	for _, b := range batch.Split(script, "GO") {
		if strings.TrimSpace(b) == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		runBatch(ctx, conn, b, res, sink)
	}
	return true, nil
}

// setOption names the SET option that enables capture and its label for error
// messages. Only meaningful for capture != planCaptureNone.
func (c planCapture) setOption() (option, label string) {
	if c == planCaptureEstimated {
		return "SHOWPLAN_XML", "estimated"
	}
	return "STATISTICS XML", "actual"
}

// acquireConnRetryAttempts is acquireConn's total tries (initial + retries)
// when its liveness prologue fails transiently. Mirrors gosmo's
// readRetryAttempts (gosmo/retry.go).
const acquireConnRetryAttempts = 3

// acquireConnRetryDelay is the backoff before the nth retry (1-based). Mirrors
// gosmo's readRetryDelay.
func acquireConnRetryDelay(attempt int) time.Duration {
	return time.Duration(attempt) * 50 * time.Millisecond
}

// acquireConn returns a live pinned *sql.Conn, switched to database (USE) if
// non-empty, retrying on a fresh connection when the pool hands back a dead
// one. A script needs one connection for its whole run, and database/sql's
// bad-connection retry covers only *sql.DB calls, not a pinned *sql.Conn — so a
// connection dropped while idle (NAT timeout, killed session, failover) would
// fail the next Execute. gosmo's Database.query/queryRow do the same for reads.
//
// Only the USE/SELECT-1 prologue is retried, never a user batch, which might
// re-apply partial side effects.
func acquireConn(ctx context.Context, db *sql.DB, database string) (*sql.Conn, error) {
	prologue := "SELECT 1"
	if database != "" {
		prologue = "USE " + gosmo.QuoteName(database)
	}
	wrapErr := func(err error) error {
		if database != "" {
			return fmt.Errorf("switch to database %s: %w", database, err)
		}
		return err
	}

	// Bounded by the >= check below (== would spin forever at 0).
	for attempt := 1; ; attempt++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, prologue); err != nil {
			conn.Close() // dead — evicted from the pool via driver.Validator.IsValid
			if ctx.Err() != nil || attempt >= acquireConnRetryAttempts || !gosmo.IsRetryable(err) {
				return nil, wrapErr(err)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(acquireConnRetryDelay(attempt)):
			}
			continue
		}
		return conn, nil
	}
}

// currentDatabase reads DB_NAME() off the connection the batches ran on, so a
// mid-script USE is visible.
func currentDatabase(ctx context.Context, conn *sql.Conn) (string, error) {
	var name string
	err := conn.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&name)
	return name, err
}

// runBatch executes one GO batch and drains the sqlexp message stream into res.
// SQL errors are messages, not early returns: later statements may still
// produce output.
func runBatch(ctx context.Context, conn *sql.Conn, sqlText string, res *Result, sink RowSink) {
	retmsg := &sqlexp.ReturnMessage{}
	rows, err := conn.QueryContext(ctx, sqlText, retmsg)
	if err != nil {
		res.addError(err)
		return
	}
	defer rows.Close()

	for active := true; active; {
		switch m := retmsg.Message(ctx).(type) {
		case sqlexp.MsgNotice:
			res.addNotice(m.Message.String())
		case sqlexp.MsgError:
			res.addError(m.Error)
		case sqlexp.MsgRowsAffected:
			if m.Count == 1 {
				res.addNotice("(1 row affected)")
			} else {
				res.addNotice(fmt.Sprintf("(%d rows affected)", m.Count))
			}
		case sqlexp.MsgNext:
			if scanNext(rows, res, sink) {
				// scanNext abandoned the set with rows pending, and the message
				// loop can't advance past it. Drain here only: an extra Next()
				// on an exhausted set makes the driver swallow the message
				// retmsg awaits, losing the set.
				for rows.Next() {
				}
			}
		case sqlexp.MsgNextResultSet:
			active = rows.NextResultSet()
		default:
			// Unreachable with sqlexp's current message set; a future type
			// would otherwise spin this loop at 100% CPU.
			res.addError(fmt.Errorf("unexpected message type %T from the driver", m))
			active = false
		}
	}
	if err := rows.Err(); err != nil && ctx.Err() == nil {
		res.addError(err)
	}
}

// rowScanner holds one result set's per-column scan targets and formatting
// decisions, shared by scanResultSet (retains) and streamResultSet (writes
// out).
type rowScanner struct {
	cols        []string
	types       []string
	vals        []any
	ptrs        []any
	guids       []*mssql.NullUniqueIdentifier
	decimalLike []bool
	layouts     []string

	// buf renders one cell at a time, reused; bytes are copied out before the
	// next cell overwrites them.
	buf []byte
}

// newRowScanner analyses the result set's columns once.
//
// uniqueidentifier scans as 16 raw bytes in the wrong order for hex;
// NullUniqueIdentifier gives the canonical GUID and preserves NULL.
//
// decimal/numeric/money/smallmoney also scan as []byte, but already decoded to
// ASCII digits ("0.070312"), so render as text, not hex. (numeric reports as
// DECIMAL.)
//
// Every date/time type scans as time.Time, so the column type and scale decide
// what SSMS shows (date without time, datetime2(3) with three digits); layouts
// holds that per column.
func newRowScanner(rows *sql.Rows) (*rowScanner, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	sc := &rowScanner{
		cols:        cols,
		types:       columnTypeNames(types),
		vals:        make([]any, len(cols)),
		ptrs:        make([]any, len(cols)),
		guids:       make([]*mssql.NullUniqueIdentifier, len(cols)),
		decimalLike: make([]bool, len(cols)),
		layouts:     make([]string, len(cols)),
	}
	for i := range cols {
		typeName := types[i].DatabaseTypeName()
		switch typeName {
		case "UNIQUEIDENTIFIER":
			sc.guids[i] = &mssql.NullUniqueIdentifier{}
			sc.ptrs[i] = sc.guids[i]
			continue
		case "DECIMAL", "MONEY", "SMALLMONEY":
			sc.decimalLike[i] = true
		}
		_, scale, scaleKnown := types[i].DecimalSize()
		sc.layouts[i] = timeLayout(typeName, int(scale), scaleKnown)
		sc.ptrs[i] = &sc.vals[i]
	}
	return sc, nil
}

// scan renders the current row into row (one slot per column). A nil arena
// gives each cell its own string (streaming path); non-nil packs them.
func (sc *rowScanner) scan(rows *sql.Rows, row []string, a *cellArena) error {
	if err := rows.Scan(sc.ptrs...); err != nil {
		return err
	}
	for i := range sc.cols {
		sc.buf = sc.buf[:0]
		if g := sc.guids[i]; g != nil {
			sc.buf = appendGUID(sc.buf, *g)
		} else {
			sc.buf = appendValue(sc.buf, sc.vals[i], sc.decimalLike[i], sc.layouts[i])
		}
		row[i] = a.str(sc.buf)
		// Drop the driver's copy now the cell is rendered, or it lives until
		// the next row overwrites it — for the last row, until the Result is
		// dropped.
		sc.vals[i] = nil
	}
	return nil
}

// scanResultSet reads the whole current result set into string cells, packed
// into a cellArena since there's no row cap.
func scanResultSet(rows *sql.Rows, prog *Progress) (ResultSet, error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return ResultSet{}, err
	}
	rs := ResultSet{Columns: sc.cols, ColumnTypes: sc.types}
	a := &cellArena{}
	for rows.Next() {
		row := a.row(len(sc.cols))
		if err := sc.scan(rows, row, a); err != nil {
			return rs, err
		}
		rs.Rows = append(rs.Rows, row)
		prog.AddRow()
	}
	return rs, nil
}

// streamResultSet writes the current result set to sink, retaining nothing, and
// returns rows written.
//
// exhausted reports whether the loop reached the set's end; not derivable from
// err, since the deferred EndSet can fail on a fully read set, and draining
// that costs a message (see scanNext).
func streamResultSet(rows *sql.Rows, sink RowSink, prog *Progress) (n int, exhausted bool, err error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return 0, false, err
	}
	// Paired with BeginSet on every exit, so a mid-set failure still closes the
	// set. Registered before BeginSet: a sink that acquired state before its
	// BeginSet failed can only undo it in EndSet. Named returns so EndSet's
	// error surfaces only when nothing else failed.
	defer func() {
		if endErr := sink.EndSet(n); endErr != nil && err == nil {
			err = endErr
		}
	}()
	if err = sink.BeginSet(sc.cols); err != nil {
		return 0, false, err
	}
	// One row buffer for the set: sink.Row must consume it before returning.
	row := make([]string, len(sc.cols))
	for rows.Next() {
		if err = sc.scan(rows, row, nil); err != nil {
			return n, false, err
		}
		if err = sink.Row(row); err != nil {
			return n, false, err
		}
		n++
		prog.AddRow()
	}
	// Next() ended the set, so it's exhausted however EndSet goes.
	return n, true, nil
}

// showplanColumnName is SQL Server's column name for STATISTICS XML /
// SHOWPLAN_XML output. Mirrors gosmo's unexported showplanColumn.
const showplanColumnName = "Microsoft SQL Server 2005 XML Showplan"

// isShowplanResultSet reports whether cols is the single-column execution-plan
// shape.
func isShowplanResultSet(cols []string) bool {
	return len(cols) == 1 && cols[0] == showplanColumnName
}

// scanNext consumes the result set MsgNext just announced, appending it to res
// as showplan XML or a grid. Errors go to res, since the batch continues.
//
// It returns whether the set was abandoned with rows pending — the only case
// the caller may drain. That's not "did it fail": streamResultSet's deferred
// EndSet and scanPlanXML's trailing rows.Err() can fail on a fully read set,
// and draining that swallows the message retmsg awaits.
func scanNext(rows *sql.Rows, res *Result, sink RowSink) (abandoned bool) {
	cols, err := rows.Columns()
	if err != nil {
		res.addError(err)
		return true
	}
	if isShowplanResultSet(cols) {
		plans, exhausted, err := scanPlanXML(rows)
		if err != nil {
			res.addError(err)
			return !exhausted
		}
		res.PlanXML = append(res.PlanXML, plans...)
		return false
	}
	if sink != nil {
		n, exhausted, err := streamResultSet(rows, sink, res.progress)
		res.RowsWritten += n
		res.sinkSets++
		if err != nil {
			res.addError(err)
			return !exhausted
		}
		res.addNotice(fmt.Sprintf("(%d row(s) written)", n))
		return false
	}
	rs, err := scanResultSet(rows, res.progress)
	if err != nil {
		res.addError(err)
		return true
	}
	res.Sets = append(res.Sets, rs)
	return false
}

// scanPlanXML reads the current showplan set into one XML document per row.
// Every probed server sends one row per set (SHOWPLAN_XML one document per
// batch, STATISTICS XML one per statement, each in its own set); keeping every
// row is a tolerance, so a split set wouldn't lose plans. Mirrors gosmo's
// capturePlan.
//
// exhausted reports whether the loop reached the set's end; rows.Err() can't
// tell a failed end from a clean one, and neither needs draining.
func scanPlanXML(rows *sql.Rows) (plans []string, exhausted bool, err error) {
	for rows.Next() {
		var xml string
		if err := rows.Scan(&xml); err != nil {
			return nil, false, err
		}
		if xml != "" {
			plans = append(plans, xml)
		}
	}
	return plans, true, rows.Err()
}

// appendGUID appends a uniqueidentifier as SSMS renders it: NULL or uppercase
// dashed.
func appendGUID(dst []byte, g mssql.NullUniqueIdentifier) []byte {
	if !g.Valid {
		return append(dst, "NULL"...)
	}
	return append(dst, g.UUID.String()...)
}

// formatGUID is appendGUID's string form, for tests.
func formatGUID(g mssql.NullUniqueIdentifier) string {
	return string(appendGUID(nil, g))
}

// defaultTimeLayout is for a time.Time from a column with no known layout (e.g.
// sql_variant); matches plain "datetime".
const defaultTimeLayout = "2006-01-02 15:04:05.000"

// timeLayout returns SSMS's grid layout for a date/time column type, or "" for
// other types. Each type shows exactly the parts it stores — a single datetime
// layout would invent "00:00:00.000" for dates and truncate datetime2.
//
// datetime2, time and datetimeoffset have a scale (0-7) for fractional digits,
// from DecimalSize; unknown scale uses 7. Other types have fixed precision.
func timeLayout(databaseTypeName string, scale int, scaleKnown bool) string {
	if !scaleKnown {
		scale = 7
	}
	switch databaseTypeName {
	case "DATE":
		return "2006-01-02"
	case "TIME":
		return "15:04:05" + fracLayout(scale)
	case "SMALLDATETIME":
		return "2006-01-02 15:04:05"
	case "DATETIME":
		return defaultTimeLayout
	case "DATETIME2":
		return "2006-01-02 15:04:05" + fracLayout(scale)
	case "DATETIMEOFFSET":
		return "2006-01-02 15:04:05" + fracLayout(scale) + " -07:00"
	}
	return ""
}

// fracLayout returns the fractional-second layout fragment for scale; "" at 0
// (no decimal point, as SSMS shows time(0)).
func fracLayout(scale int) string {
	if scale <= 0 {
		return ""
	}
	return "." + strings.Repeat("0", min(scale, 7))
}

// formatValue renders a cell as SSMS does: NULL for nil, 1/0 for bit, 0x… for
// binary, date/time in its column's layout. isDecimalLike marks a []byte
// holding decoded decimal/money digits (render as text, not hex). layout is
// empty for non-date/time columns.
func formatValue(v any, isDecimalLike bool, layout string) string {
	return string(appendValue(nil, v, isDecimalLike, layout))
}

// appendValue is formatValue in append form, so cells render through one reused
// buffer.
func appendValue(dst []byte, v any, isDecimalLike bool, layout string) []byte {
	switch x := v.(type) {
	case nil:
		return append(dst, "NULL"...)
	case bool:
		if x {
			return append(dst, '1')
		}
		return append(dst, '0')
	case []byte:
		if isDecimalLike {
			return append(dst, x...)
		}
		return appendHexUpper(dst, x)
	case time.Time:
		if layout == "" {
			layout = defaultTimeLayout
		}
		return x.AppendFormat(dst, layout)
	case float64:
		return appendFloat(dst, x, 64)
	case float32:
		return appendFloat(dst, float64(x), 32)
	case string:
		return append(dst, x...)
	default:
		return fmt.Appendf(dst, "%v", x)
	}
}

// appendHexUpper appends b as SSMS's "0x…" uppercase hex in one pass.
func appendHexUpper(dst []byte, b []byte) []byte {
	dst = append(dst, '0', 'x')
	for _, c := range b {
		dst = append(dst, hexUpperDigits[c>>4], hexUpperDigits[c&0x0f])
	}
	return dst
}

const hexUpperDigits = "0123456789ABCDEF"

// appendFloat renders float/real as SSMS's grid does: plain decimal in the
// readable range, scientific outside it (Go's %g would show 1000000 as
// "1e+06"). Shortest round-trip precision, so pasted-back text reparses to the
// same float64.
func appendFloat(dst []byte, f float64, bits int) []byte {
	abs := math.Abs(f)
	if f != 0 && !math.IsInf(f, 0) && !math.IsNaN(f) && (abs < 1e-4 || abs >= 1e15) {
		return strconv.AppendFloat(dst, f, 'e', -1, bits)
	}
	return strconv.AppendFloat(dst, f, 'f', -1, bits)
}

// formatFloat is appendFloat's string form, for tests.
func formatFloat(f float64, bits int) string {
	return string(appendFloat(nil, f, bits))
}
