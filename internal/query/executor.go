// Package query executes T-SQL scripts as SSMS does: split into GO batches, all
// run on one dedicated connection (so temp tables and SET options survive
// across batches), with PRINT output, "(n rows affected)" and errors captured
// into Result.Messages.
//
// A Session keeps its connection across scripts and is the only path offering
// plan capture and row sinks; package-level Execute checks a connection out of
// the pool per call, and the pool resets it before reuse (or Execute discards
// it, when the run left it in another database).
package query

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/golang-sql/sqlexp"
	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ResultSet is one grid of data returned by a batch.
type ResultSet struct {
	Columns []string
	Rows    [][]string

	// ColumnTypes holds each column's declared type as SSMS writes it
	// ("nvarchar(50)", "decimal(18,2)"), parallel to Columns.
	ColumnTypes []string

	// nulls marks the cells holding a SQL NULL, one bit per cell in row-major
	// order (row*len(Columns)+col). A NULL's text is "NULL", so the text alone
	// cannot tell it from the string 'NULL'. Grown only to the last NULL.
	nulls []uint64

	// Truncated reports that the set had more rows than WithMaxRows allowed: Rows
	// holds the first max, and the rest were read off the wire and discarded.
	Truncated bool
}

// MarkNull records that cell (row, col) holds a SQL NULL.
func (rs *ResultSet) MarkNull(row, col int) {
	bit := row*len(rs.Columns) + col
	if w := bit / 64; w >= len(rs.nulls) {
		rs.nulls = append(rs.nulls, make([]uint64, w+1-len(rs.nulls))...)
	}
	rs.nulls[bit/64] |= 1 << (bit % 64)
}

// IsNull reports whether cell (row, col) holds a SQL NULL; false for a cell
// outside the set. With Len and Row it makes a ResultSet a controls.RowSource
// and controls.NullSource.
func (rs ResultSet) IsNull(row, col int) bool {
	if row < 0 || col < 0 || col >= len(rs.Columns) {
		return false
	}
	bit := row*len(rs.Columns) + col
	return bit/64 < len(rs.nulls) && rs.nulls[bit/64]&(1<<(bit%64)) != 0
}

// Len returns the number of rows.
func (rs ResultSet) Len() int { return len(rs.Rows) }

// Row returns row i's cells.
func (rs ResultSet) Row(i int) []string { return rs.Rows[i] }

// Message is one line of the Messages pane.
type Message struct {
	Text    string
	IsError bool

	// Line is the 1-based script line a SQL Server error points at (its "Line N"
	// counted from the script's start, see WithLineOffset), where go-to-line
	// jumps. 0 when there is none: not an error, an error inside a procedure,
	// function or trigger (its line is in that module), or one from
	// ErrorMessages, which has no script to count in.
	Line int

	// SQL is the structured SQL Server error on an error's first line (the "Msg
	// 208, Level 16, …" header), nil on every other message. Its LineNo is the
	// script line when Line is set, else the server's (a module's own line, or
	// counted from the batch's start for ErrorMessages).
	SQL *gosmo.SQLError

	// RowsAffected is set on a "(n rows affected)" message, nil on every other.
	RowsAffected *RowCount
}

// RowCount is a statement's structured row count, the number behind "(n rows
// affected)".
type RowCount struct {
	N int64

	// AfterSet reports that the statement returned a result set before its count:
	// a SELECT's rows, or a DML statement's OUTPUT rows — the protocol message
	// does not say which (go-mssqldb drops the DONE token's command). False is a
	// statement that returned no rows: INSERT, UPDATE, DELETE, MERGE, SELECT INTO.
	// SQL Server sends no count at all under SET NOCOUNT ON.
	AfterSet bool

	// AfterPlan reports that the set before the count was a captured plan
	// (Result.PlanXML), not rows: under SET STATISTICS XML ON each executed
	// statement's plan arrives as a one-row set with a count of its own.
	AfterPlan bool
}

// Result is everything one Execute call produced, across all GO batches.
type Result struct {
	Sets     []ResultSet
	Messages []Message
	Elapsed  time.Duration

	// Database is the database in effect when execution finished, read off the
	// script's connection so a mid-script USE shows. Empty if unreadable.
	Database string

	// RowsWritten totals rows handed to a RowSink. Zero for Execute.
	RowsWritten int

	// SinkSetsTruncated counts the sets streamed to a RowSink that WithMaxRows cut
	// short; the grid path marks ResultSet.Truncated instead.
	SinkSetsTruncated int

	// sinkSets counts result sets streamed to a RowSink, empty ones included;
	// neither len(Sets) nor RowsWritten says whether a set happened on that path.
	// See shouldReportSuccess.
	sinkSets int

	// progress is the caller's live row counter (see WithProgress), carried on
	// Result as the run-scoped state runBatch/scanNext share.
	progress *Progress

	// lineOffset is the 0-based editor line the script starts on (see
	// WithLineOffset), run-scoped like progress.
	lineOffset int

	// maxRows caps the rows kept per result set (see WithMaxRows); 0 is no cap.
	maxRows int

	// cut is set by scanNext when it stopped a set at maxRows, for runBatch to
	// cancel the batch instead of draining it; it then stays set, so runScript
	// runs no further batch.
	cut bool

	// PlanXML holds one <ShowPlanXML> document per captured statement/batch, in
	// execution order. Execute never fills it.
	PlanXML []string

	// State is the session's state after a run on a Session, cancelled runs
	// included (they can leave a transaction open). Nil for package-level Execute,
	// or when the read failed (SET NOEXEC ON left in force, a dead session).
	State *SessionState

	// SessionLost reports that this run's Session is gone (connection broke, or
	// plan capture couldn't be switched off), taking its temp tables, SET options
	// and transactions; later runs fail at once. Always false outside a Session.
	SessionLost bool

	// StoppedOnError reports that WithStopOnError ended the run after a failing
	// batch, so later batches (or repetitions of a GO n) were not sent.
	StoppedOnError bool

	// Batches counts the batches sent, each repetition of a GO n separately, and
	// FailedBatches those of them that reported an error: a caller tells "some
	// batches failed" from "all did" without mapping error lines to batches.
	Batches, FailedBatches int

	// stopOnError and noSinkNotices are WithStopOnError and WithoutSinkNotices.
	stopOnError   bool
	noSinkNotices bool
}

// MaxRows is the WithMaxRows cap the run used, 0 for none.
func (r *Result) MaxRows() int { return r.maxRows }

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

// errorCount counts the error messages so far; runScript compares it across a
// batch for WithStopOnError.
func (r *Result) errorCount() int {
	n := 0
	for _, m := range r.Messages {
		if m.IsError {
			n++
		}
	}
	return n
}

func (r *Result) addError(err error) {
	r.Messages = append(r.Messages, ErrorMessages(err)...)
}

// addBatchError records an error raised by a batch starting on 0-based script
// line firstLine (lineOffset included).
func (r *Result) addBatchError(err error, firstLine int) {
	r.Messages = append(r.Messages, batchErrorMessages(err, firstLine)...)
}

// ErrorMessages formats err as SSMS's Messages pane shows a failed batch: a SQL
// Server error becomes a "Msg 208, Level 16, State 1, Line 4" line plus the
// text; anything else one message. The line is the server's, counted from the
// batch's start; a batch run by Execute reports script lines instead.
func ErrorMessages(err error) []Message {
	return batchErrorMessages(err, -1)
}

// batchErrorMessages is ErrorMessages for an error raised by a batch starting on
// 0-based script line firstLine, or -1 for no script. SQL Server counts "Line N"
// from the batch's first line, so firstLine is added to make it the script line,
// as SSMS does since 2016.
//
// Not for an error raised inside a module (ProcName set): its line counts from
// the module's own text and stays as sent. Nor for Line 0.
func batchErrorMessages(err error, firstLine int) []Message {
	if se, ok := gosmo.AsSQLError(err); ok {
		line := 0
		if firstLine >= 0 && se.ProcName == "" && se.LineNo > 0 {
			se.LineNo += int32(firstLine)
			line = int(se.LineNo)
		}
		msgs := []Message{{Text: se.Header(), IsError: true, Line: line, SQL: se}}
		if se.Message != "" {
			msgs = append(msgs, Message{Text: se.Message, IsError: true, Line: line})
		}
		return msgs
	}
	return []Message{{Text: err.Error(), IsError: true}}
}
func (r *Result) addNotice(s string) { r.Messages = append(r.Messages, Message{Text: s}) }

// shouldReportSuccess reports whether the run produced nothing else to say, so
// Messages gets SSMS's "Commands completed successfully."
//
// The test is whether any result set happened, not any row (Sets for Execute,
// sinkSets for Session.ExecuteToSink): RowsWritten would make an empty set print
// both "(0 row(s) written)" and the success notice. planCaptureEstimated
// executes nothing, so never reports success.
func (r *Result) shouldReportSuccess(capture planCapture) bool {
	return len(r.Sets) == 0 && r.sinkSets == 0 && !r.HasErrors() && capture != planCaptureEstimated
}

// planCapture selects whether and how execute captures an execution plan: a
// gosmo.PlanMode, or none.
type planCapture = gosmo.PlanMode

const (
	planCaptureNone      planCapture = 0
	planCaptureActual                = gosmo.PlanActual    // SET STATISTICS XML ON — batches really run
	planCaptureEstimated             = gosmo.PlanEstimated // SET SHOWPLAN_XML ON — nothing really runs
)

// Progress is a live row counter for a running script: the executor bumps it
// per scanned row so a caller can show progress before Result arrives. Pass it
// with WithProgress.
//
// Read from another goroutine, hence atomic. The zero value is ready; every
// method is nil-safe.
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

// WithLineOffset says the script starts on 0-based editor line n, as an executed
// selection does, so error lines count from the editor's first line, not the
// script's.
func WithLineOffset(n int) Option {
	return func(res *Result) { res.lineOffset = n }
}

// WithMaxRows keeps at most n rows of each result set, retained in Result.Sets
// (marking a set that had more ResultSet.Truncated) or streamed to a RowSink
// (counted in Result.SinkSetsTruncated). n <= 0 means no cap.
//
// Reaching the cap cancels the rest of the script: the current batch with a TDS
// attention (a result set cannot be skipped without reading it, so its
// remaining statements cannot run), and every later GO batch by not sending it.
func WithMaxRows(n int) Option {
	return func(res *Result) { res.maxRows = max(n, 0) }
}

// WithStopOnError ends the run after the first batch that reports an error:
// later GO batches, and further repetitions of a GO n, are not sent, and
// Result.StoppedOnError says so. Within a batch SQL Server decides whether the
// statements after an error run. Without it every batch runs, as in SSMS.
func WithStopOnError() Option {
	return func(res *Result) { res.stopOnError = true }
}

// WithoutSinkNotices leaves out the Messages lines the RowSink path adds in the
// TUI's wording — "(n row(s) written)" and the Max fetch rows notices — for a
// caller that reports rows and truncation itself. The server's messages are
// unaffected.
func WithoutSinkNotices() Option {
	return func(res *Result) { res.noSinkNotices = true }
}

// Execute runs script against db, SSMS-style. A non-empty database is switched
// to first (USE), and the connection is then discarded rather than pooled, as
// it is when the script leaves its database or the run fails or is cancelled. The script is split on GO; a failing batch is reported in
// Messages and execution continues. Cancelling ctx stops between and inside
// batches, returning the partial Result. Every row is retained in Result.Sets,
// uncapped unless WithMaxRows says otherwise; see cellArena.
func Execute(ctx context.Context, db *sql.DB, database, script string, opts ...Option) *Result {
	start := time.Now()
	res := newResult(opts)

	conn, err := gosmo.AcquireConn(ctx, db, database)
	if err != nil {
		res.addError(err)
		res.Elapsed = time.Since(start)
		return res
	}
	// The pool's reset on next checkout clears SET options and transactions but
	// not the session's database: pooled inside one, an idle session holds a
	// shared database lock for up to ConnMaxIdleTime, failing someone else's
	// RESTORE, SET OFFLINE or SET SINGLE_USER. So the connection goes back to
	// the pool only when the run ends where an unswitched checkout started.
	var startDB string
	if database == "" {
		startDB, _ = currentDatabase(ctx, conn)
	}
	endDB := ""
	defer func() {
		if startDB == "" || endDB != startDB {
			discardConn(conn)
		}
		conn.Close()
	}()

	// Plan capture and row sinks are Session-only (both need a connection that
	// survives the run), so the capture-off failure a Session treats as fatal
	// cannot arise here; the pool's reset on next checkout clears any SET option.
	if ran, _ := runScript(ctx, conn, script, planCaptureNone, nil, res); ran {
		if ctx.Err() != nil {
			res.Messages = append(res.Messages, cancelledMessage)
		} else {
			if name, err := currentDatabase(ctx, conn); err == nil {
				res.Database, endDB = name, name
			}
			if res.shouldReportSuccess(planCaptureNone) {
				res.addNotice("Commands completed successfully.")
			}
		}
	}
	res.Elapsed = time.Since(start)
	return res
}

// RowSink receives result rows as scanned instead of retaining them in
// Result.Sets, so Results To File exports are bounded by the file, not memory.
//
// BeginSet is called before a set's first row, EndSet after its last with the
// row count. A returned error aborts that set and is reported in Messages; the
// script continues. A sink that also implements TypedRowSink gets BeginTypedSet
// instead of BeginSet.
//
// EndSet is called for every set BeginSet was called for, including sets
// abandoned by a Row error or whose BeginSet failed, so it's the one place to
// finalise per-set state. Its count is rows that reached Row (0 if the set never
// opened).
//
// Row's isNull parallels cells and marks SQL NULLs (text "NULL", as in a
// ResultSet). Both slices are reused for the next row: consume them before
// returning.
type RowSink interface {
	BeginSet(columns []string) error
	Row(cells []string, isNull []bool) error
	EndSet(rows int) error
}

// TypedRowSink is a RowSink that also wants each set's column types. types holds
// the declared types as ResultSet.ColumnTypes does ("nvarchar(50)"), nullable
// whether the column admits NULL as the server describes it; both parallel
// columns. Asserted per set, so a plain RowSink is unaffected.
type TypedRowSink interface {
	RowSink
	BeginTypedSet(columns, types []string, nullable []bool) error
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

// runScript runs script's GO batches on conn in order, recording output on res,
// with the requested plan capture switched on around them. Returns false if the
// capture couldn't be switched on (already recorded on res).
//
// cleanupErr is the failure of the capture's SET ... OFF, if any, deferred to run
// on every exit; gosmo.StartPlanCapture's stop detaches it from ctx's
// cancellation (a cancelled run most likely left it pending) and bounds it.
func runScript(ctx context.Context, conn *sql.Conn, script string, capture planCapture, sink RowSink, res *Result) (ran bool, cleanupErr error) {
	if capture != planCaptureNone {
		stop, err := gosmo.StartPlanCapture(ctx, conn, capture)
		if err != nil {
			res.addError(err)
			return false, nil
		}
		defer func() { cleanupErr = stop() }()
	}

	for _, b := range sqltext.SplitBatches(script) {
		for range b.Count {
			if ctx.Err() != nil || res.cut || res.StoppedOnError {
				return true, nil
			}
			errsBefore := res.errorCount()
			runBatch(ctx, conn, b.Text, res.lineOffset+b.StartLine, res, sink)
			res.Batches++
			if res.errorCount() > errsBefore {
				res.FailedBatches++
				if res.stopOnError {
					res.StoppedOnError = true
				}
			}
		}
		if b.Count > 1 {
			res.addNotice(fmt.Sprintf("Batch execution completed %d times.", b.Count))
		}
	}
	return true, nil
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
// produce output. firstLine is the 0-based editor line the batch starts on, which
// the batch's SQL errors count their lines from (batchErrorMessages).
func runBatch(ctx context.Context, conn *sql.Conn, sqlText string, firstLine int, res *Result, sink RowSink) {
	retmsg := &sqlexp.ReturnMessage{}
	rows, err := conn.QueryContext(ctx, sqlText, retmsg)
	if err != nil {
		res.addBatchError(err, firstLine)
		return
	}
	defer rows.Close()

	// afterSet mirrors the driver's own colsReceived: a set was announced since the
	// last DONE token, each of which the driver follows with MsgNextResultSet. A
	// count arriving meanwhile closes the statement that returned the set.
	afterSet, afterPlan := false, false
	for active := true; active; {
		switch m := retmsg.Message(ctx).(type) {
		case sqlexp.MsgNotice:
			res.addNotice(m.Message.String())
		case sqlexp.MsgError:
			res.addBatchError(m.Error, firstLine)
		case sqlexp.MsgRowsAffected:
			text := fmt.Sprintf("(%d rows affected)", m.Count)
			if m.Count == 1 {
				text = "(1 row affected)"
			}
			res.Messages = append(res.Messages, Message{Text: text, RowsAffected: &RowCount{N: m.Count, AfterSet: afterSet, AfterPlan: afterPlan}})
		case sqlexp.MsgNext:
			afterSet = true
			plans := len(res.PlanXML)
			abandoned := scanNext(rows, res, sink)
			afterPlan = len(res.PlanXML) > plans
			if abandoned {
				if res.cut {
					cancelBatch(rows, res, sink)
					return
				}
				// scanNext abandoned the set with rows pending, and the message loop
				// can't advance past it. Drain only here: an extra Next() on an
				// exhausted set makes the driver swallow the message retmsg awaits.
				for rows.Next() {
				}
			}
		case sqlexp.MsgNextResultSet:
			afterSet, afterPlan = false, false
			active = rows.NextResultSet()
		case sqlexp.MsgLastInsertID:
			// sqlexp's sixth type, never enqueued by go-mssqldb: SQL Server reports an
			// identity through SCOPE_IDENTITY(), not the protocol. Ignored explicitly
			// so only an unknown type reaches default.
		default:
			// Unreachable: go-mssqldb enqueues only the five types above. A guard
			// against a driver change; a new type would otherwise spin this loop at
			// 100% CPU. It ends the batch with an error, truncating the remaining
			// output, so a type that starts arriving wants its own arm.
			res.addError(fmt.Errorf("unexpected message type %T from the driver", m))
			active = false
		}
	}
	if err := rows.Err(); err != nil && ctx.Err() == nil {
		res.addBatchError(err, firstLine)
	}
}

// cancelBatch ends a batch whose current set reached the WithMaxRows cap, and
// with it the script (runScript stops on res.cut). Closing
// rows cancels the driver's request context, which sends the server an attention
// and reads up to its confirmation, so the connection (and with it the session)
// stays usable. The batch's trailing rows.Err() is skipped: the cancellation is
// not a failure. A Close error means the confirmation never came and the
// connection is likely gone.
func cancelBatch(rows *sql.Rows, res *Result, sink RowSink) {
	if err := rows.Close(); err != nil {
		res.addError(err)
	}
	if sink != nil && res.noSinkNotices {
		return
	}
	res.addNotice("The rest of the script was cancelled at Max fetch rows; its remaining statements and batches did not run.")
}

// rowScanner holds one result set's per-column scan targets and formatting
// decisions, shared by scanResultSet (retains) and streamResultSet (writes).
type rowScanner struct {
	cols        []string
	types       []string
	nullable    []bool
	vals        []any
	ptrs        []any
	guids       []*mssql.NullUniqueIdentifier
	decimalLike []bool
	isMoney     []bool
	isReal      []bool
	isVariant   []bool
	layouts     []string

	// isNull marks the cells of the row last scanned that hold a SQL NULL, reused
	// row to row.
	isNull []bool

	// buf renders one cell at a time, reused; bytes are copied out before the next
	// cell overwrites them.
	buf []byte
}

// newRowScanner analyses the result set's columns once.
//
// uniqueidentifier scans as 16 raw bytes in the wrong order for hex;
// NullUniqueIdentifier gives the canonical GUID and preserves NULL.
//
// decimal/numeric/money/smallmoney scan as []byte already decoded to ASCII digits
// ("0.070312"), so render as text, not hex (numeric reports as DECIMAL).
// money/smallmoney arrive with four decimals; isMoney trims them as SSMS does.
//
// real scans as float64(float32); formatting at 64 bits shows widening noise
// (0.1 -> 0.10000000149011612), so isReal marks those columns for 32-bit.
//
// Every date/time type scans as time.Time, so the column type and scale decide
// what SSMS shows (date without time, datetime2(3) with three digits); layouts
// holds that per column.
//
// sql_variant scans as its inner type's value with that type dropped; isVariant
// routes those columns through appendVariant.
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
		nullable:    make([]bool, len(cols)),
		vals:        make([]any, len(cols)),
		ptrs:        make([]any, len(cols)),
		guids:       make([]*mssql.NullUniqueIdentifier, len(cols)),
		decimalLike: make([]bool, len(cols)),
		isMoney:     make([]bool, len(cols)),
		isReal:      make([]bool, len(cols)),
		isVariant:   make([]bool, len(cols)),
		layouts:     make([]string, len(cols)),
		isNull:      make([]bool, len(cols)),
	}
	for i := range cols {
		// go-mssqldb answers from the column metadata's nullable flag for every
		// column; one it could not describe reads as nullable, the safe side for a
		// TypedRowSink that would skip NULL handling on a NOT NULL column.
		if n, ok := types[i].Nullable(); n || !ok {
			sc.nullable[i] = true
		}
		typeName := types[i].DatabaseTypeName()
		switch typeName {
		case "UNIQUEIDENTIFIER":
			sc.guids[i] = &mssql.NullUniqueIdentifier{}
			sc.ptrs[i] = sc.guids[i]
			continue
		case "DECIMAL":
			sc.decimalLike[i] = true
		case "MONEY", "SMALLMONEY":
			sc.decimalLike[i] = true
			sc.isMoney[i] = true
		case "REAL":
			sc.isReal[i] = true
		case "SQL_VARIANT":
			sc.isVariant[i] = true
		}
		_, scale, scaleKnown := types[i].DecimalSize()
		sc.layouts[i] = timeLayout(typeName, int(scale), scaleKnown)
		sc.ptrs[i] = &sc.vals[i]
	}
	return sc, nil
}

// scan renders the current row into row (one slot per column) and sc.isNull.
// A nil arena gives each cell its own string (streaming); non-nil packs them.
func (sc *rowScanner) scan(rows *sql.Rows, row []string, a *cellArena) error {
	if err := rows.Scan(sc.ptrs...); err != nil {
		return err
	}
	for i := range sc.cols {
		sc.buf = sc.buf[:0]
		if g := sc.guids[i]; g != nil {
			sc.isNull[i] = !g.Valid
			sc.buf = appendGUID(sc.buf, *g)
		} else if f, ok := sc.vals[i].(float64); ok && sc.isReal[i] {
			sc.isNull[i] = false
			sc.buf = appendFloat(sc.buf, f, 32)
		} else if b, ok := sc.vals[i].([]byte); ok && sc.isMoney[i] {
			sc.isNull[i] = false
			sc.buf = appendMoney(sc.buf, b)
		} else if sc.isVariant[i] {
			sc.isNull[i] = sc.vals[i] == nil
			sc.buf = appendVariant(sc.buf, sc.vals[i])
		} else {
			sc.isNull[i] = sc.vals[i] == nil
			sc.buf = appendValue(sc.buf, sc.vals[i], sc.decimalLike[i], sc.layouts[i])
		}
		row[i] = a.str(sc.buf)
		// Drop the driver's copy now the cell is rendered, or it lives until the next
		// row overwrites it (for the last row, until the Result is dropped).
		sc.vals[i] = nil
	}
	return nil
}

// scanResultSet reads the current result set into string cells, packed into a
// cellArena. A positive maxRows stops at that many rows; a further row marks the
// set Truncated and leaves it pending, for runBatch to cancel. The test
// is the loop's own Next(), never an extra call on an exhausted set (see
// runBatch).
//
// No trailing rows.Err(), deliberately, and likewise in streamResultSet:
// runBatch ends the message loop with one, so a truncated set is still reported
// from there. This keeps the partial set: the user gets the grid *and* the
// error, as SSMS shows a query that died on row 900. scanPlanXML checks and
// discards its partial result instead: half a showplan is not a plan. Both are
// reached only through scanNext, itself reached only from runBatch; a caller
// outside runBatch must bring its own check.
func scanResultSet(rows *sql.Rows, prog *Progress, maxRows int) (ResultSet, error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return ResultSet{}, err
	}
	rs := ResultSet{Columns: sc.cols, ColumnTypes: sc.types}
	a := &cellArena{}
	for rows.Next() {
		if maxRows > 0 && len(rs.Rows) == maxRows {
			rs.Truncated = true
			return rs, nil
		}
		row := a.row(len(sc.cols))
		if err := sc.scan(rows, row, a); err != nil {
			return rs, err
		}
		for i, null := range sc.isNull {
			if null {
				rs.MarkNull(len(rs.Rows), i)
			}
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
// err, since the deferred EndSet can fail on a fully read set, and draining that
// costs a message (see scanNext).
//
// A positive maxRows stops after that many rows, as scanResultSet: a further row
// leaves the set pending, reported as exhausted false with a nil err.
//
// Like scanResultSet, it ends without rows.Err(): runBatch's trailing one
// reports a set the server truncated, and rows already handed to the sink stay
// written.
func streamResultSet(rows *sql.Rows, sink RowSink, prog *Progress, maxRows int) (n int, exhausted bool, err error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return 0, false, err
	}
	// Paired with BeginSet on every exit, so a mid-set failure still closes the
	// set. Registered before BeginSet: a sink that acquired state before its
	// BeginSet failed can only undo it in EndSet. Named returns so EndSet's error
	// surfaces only when nothing else failed.
	defer func() {
		if endErr := sink.EndSet(n); endErr != nil && err == nil {
			err = endErr
		}
	}()
	if ts, ok := sink.(TypedRowSink); ok {
		err = ts.BeginTypedSet(sc.cols, sc.types, sc.nullable)
	} else {
		err = sink.BeginSet(sc.cols)
	}
	if err != nil {
		return 0, false, err
	}
	// One row buffer for the set: sink.Row must consume it before returning.
	row := make([]string, len(sc.cols))
	for rows.Next() {
		if maxRows > 0 && n == maxRows {
			return n, false, nil
		}
		if err = sc.scan(rows, row, nil); err != nil {
			return n, false, err
		}
		if err = sink.Row(row, sc.isNull); err != nil {
			return n, false, err
		}
		n++
		prog.AddRow()
	}
	// Next() ended the set, so it's exhausted however EndSet goes.
	return n, true, nil
}

// isShowplanResultSet reports whether cols is the single-column execution-plan
// shape.
func isShowplanResultSet(cols []string) bool {
	return len(cols) == 1 && cols[0] == gosmo.ShowplanColumn
}

// scanNext consumes the result set MsgNext just announced, appending it to res as
// showplan XML or a grid. Errors go to res, since the batch continues.
//
// It returns whether the set was abandoned with rows pending, the only case the
// caller may drain. Not "did it fail": streamResultSet's deferred EndSet and
// scanPlanXML's trailing rows.Err() can fail on a fully read set, and draining
// that swallows the message retmsg awaits. A set stopped at the WithMaxRows cap
// is abandoned too, with res.cut set: the caller cancels the batch instead.
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
		n, exhausted, err := streamResultSet(rows, sink, res.progress, res.maxRows)
		res.RowsWritten += n
		res.sinkSets++
		if err != nil {
			res.addError(err)
			return !exhausted
		}
		if !res.noSinkNotices {
			res.addNotice(fmt.Sprintf("(%d row(s) written)", n))
		}
		if !exhausted {
			res.SinkSetsTruncated++
			if !res.noSinkNotices {
				res.addNotice(truncatedNotice(res.sinkSets, res.maxRows))
			}
			res.cut = true
		}
		return !exhausted
	}
	rs, err := scanResultSet(rows, res.progress, res.maxRows)
	if rs.Columns != nil {
		// Kept on a scan error too (a conversion failure on row 900): the
		// grid *and* the error, like a Next() that fails part-way.
		res.Sets = append(res.Sets, rs)
	}
	if err != nil {
		res.addError(err)
		return true
	}
	if rs.Truncated {
		res.addNotice(truncatedNotice(len(res.Sets), res.maxRows))
		res.cut = true
	}
	return rs.Truncated
}

// truncatedNotice is the Messages line for the 1-based nth set cut at limit rows.
func truncatedNotice(nth, limit int) string {
	return fmt.Sprintf("Result set %d: only the first %d rows were fetched (Max fetch rows).", nth, limit)
}

// scanPlanXML reads the current showplan set into one XML document per row.
// Every probed server sends one row per set; keeping every row is a tolerance so
// a split set wouldn't lose plans (as in gosmo's capturePlan).
//
// exhausted reports whether the loop reached the set's end; rows.Err() can't
// tell a failed end from a clean one, and neither needs draining.
//
// Unlike the grid scanners it checks the trailing rows.Err(), so a truncated plan
// set yields *no* plans (half a showplan is not a plan; a grid cut at row 900 is
// still 900 rows). runBatch reports the same failure again in Messages, which is
// accepted.
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
