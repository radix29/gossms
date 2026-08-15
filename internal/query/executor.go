// Package query executes T-SQL scripts the way SSMS does: split into GO
// batches, all run on one dedicated connection (so temp tables and SET options
// survive across batches), with the driver's message stream — PRINT output,
// "(n rows affected)", SQL errors — captured into Result.Messages.
package query

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
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

	// ColumnTypes holds each column's declared SQL Server type as SSMS writes
	// it ("nvarchar(50)", "decimal(18,2)"), parallel to Columns. Shown by the
	// "Output Column Metadata" toggle.
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
	// same connection the script ran on, so a mid-script "USE otherdb" is
	// reflected back. Empty if it couldn't be read (a cancelled query, say).
	Database string

	// RowsWritten totals the rows handed to a RowSink across every result set.
	// Zero for Execute, which retains rows in Sets.
	RowsWritten int

	// sinkSets counts the result sets streamed to a RowSink, empty ones
	// included. On the ExecuteToSink path Sets is always empty and an empty
	// set writes no rows, so neither len(Sets) nor RowsWritten can answer "did
	// a result set happen" — see shouldReportSuccess.
	sinkSets int

	// PlanXML holds one <ShowPlanXML> document per captured statement/batch, in
	// execution order: actual plans from ExecuteWithPlan, estimated ones from
	// ExecuteEstimatedPlan. Execute never populates this.
	PlanXML []string
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

// ErrorMessages formats err the way SSMS's Messages pane shows a failed batch:
// a SQL Server error becomes two messages, the "Msg 208, Level 16, State 1,
// Line 4" status line and the message text; anything else becomes one message
// from err.Error(). Exported so callers talking to gosmo directly (the
// execution-plan paths) report identically.
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

// shouldReportSuccess reports whether the run ended with nothing else to say,
// so the Messages pane gets SSMS's bare "Commands completed successfully."
//
// The test is "did any result set happen", not "did any row" — Sets answers
// that for Execute, sinkSets for ExecuteToSink. Substituting RowsWritten makes
// a query returning an *empty* set look like one returning none, and the
// export then prints both "(0 row(s) written)" and "Commands completed
// successfully.". planCaptureEstimated never executes, so the notice would
// mislead.
func (r *Result) shouldReportSuccess(capture planCapture) bool {
	return len(r.Sets) == 0 && r.sinkSets == 0 && !r.HasErrors() && capture != planCaptureEstimated
}

// planCapture selects whether execute captures an execution plan alongside a
// script's ordinary batches, and if so in which SQL Server mode.
type planCapture int

const (
	planCaptureNone      planCapture = iota
	planCaptureActual                // SET STATISTICS XML ON — batches really run
	planCaptureEstimated             // SET SHOWPLAN_XML ON — nothing really runs
)

// readsCurrentDatabase reports whether execute should read DB_NAME() back off
// the script's connection to populate Result.Database.
//
// Never under SHOWPLAN_XML: the SET ... OFF is deferred and hasn't run, so
// SELECT DB_NAME() comes back as a one-column showplan set and Scan puts the
// whole XML document into Result.Database. Estimated mode also never runs a
// mid-script USE, so there is nothing to report.
func (c planCapture) readsCurrentDatabase() bool { return c != planCaptureEstimated }

// Execute runs script against db, SSMS-style. If database is non-empty the
// connection switches to it first ("USE [database]"). The script is split on
// GO separators; a failing batch is reported in Messages and execution
// continues with the next, matching SSMS. Cancelling ctx stops between (and
// inside) batches; the partial Result is still returned.
//
// Every row is retained in Result.Sets — there is no cap; see cellArena.
func Execute(ctx context.Context, db *sql.DB, database, script string) *Result {
	return execute(ctx, db, database, script, planCaptureNone)
}

// ExecuteWithPlan behaves like Execute but runs with SET STATISTICS XML ON, so
// the script's actual execution plan — captured after it really runs, not just
// compiled — comes back in Result.PlanXML. Everything else is identical.
func ExecuteWithPlan(ctx context.Context, db *sql.DB, database, script string) *Result {
	return execute(ctx, db, database, script, planCaptureActual)
}

// ExecuteEstimatedPlan behaves like Execute but runs with SET SHOWPLAN_XML ON
// instead of running the script: SQL Server compiles every GO batch and
// returns its estimated plan in Result.PlanXML without executing it, matching
// SSMS's "Display Estimated Execution Plan".
func ExecuteEstimatedPlan(ctx context.Context, db *sql.DB, database, script string) *Result {
	return execute(ctx, db, database, script, planCaptureEstimated)
}

// RowSink receives a script's result rows as they are scanned, instead of
// retaining them in Result.Sets. Results To File is the caller: it writes each
// row to a CSV file and keeps none, so an export is bounded by the file rather
// than by memory.
//
// BeginSet is called once per result set before its first row, EndSet once
// after its last with the number of rows written. A returned error aborts that
// result set and is reported in Result.Messages; the rest of the script still
// runs, as Execute treats a failed batch.
//
// EndSet is called for every set BeginSet was called for, including one
// abandoned part-way by a Row error and one whose own BeginSet failed — so a
// sink finalising anything per set (a flush, a footer) can do it there and
// nowhere else. Its count is how many rows reached Row, not how many the set
// held, and is 0 for a set that never opened.
type RowSink interface {
	BeginSet(columns []string) error
	Row(cells []string) error
	EndSet(rows int) error
}

// ExecuteToSink behaves like Execute but streams every row to sink instead of
// accumulating it in Result.Sets, which comes back empty. Row counts are
// reported per set in Result.Messages, and Result.RowsWritten totals them.
// Nothing is retained, so an unbounded result set costs unbounded file rather
// than unbounded memory.
func ExecuteToSink(ctx context.Context, db *sql.DB, database, script string, sink RowSink) *Result {
	return executeWithSink(ctx, db, database, script, planCaptureNone, sink)
}

func execute(ctx context.Context, db *sql.DB, database, script string, capture planCapture) *Result {
	return executeWithSink(ctx, db, database, script, capture, nil)
}

func executeWithSink(ctx context.Context, db *sql.DB, database, script string, capture planCapture, sink RowSink) *Result {
	start := time.Now()
	res := &Result{}

	conn, err := acquireConn(ctx, db, database)
	if err != nil {
		res.addError(err)
		res.Elapsed = time.Since(start)
		return res
	}
	defer conn.Close()

	if capture != planCaptureNone {
		setOpt, label := "STATISTICS XML", "actual"
		if capture == planCaptureEstimated {
			setOpt, label = "SHOWPLAN_XML", "estimated"
		}
		if _, err := conn.ExecContext(ctx, "SET "+setOpt+" ON"); err != nil {
			res.addError(fmt.Errorf("enable %s execution plan capture: %w", label, err))
			res.Elapsed = time.Since(start)
			return res
		}
		// Cleanup must still run — returning conn to the pool in a known
		// state — even if ctx is already cancelled. context.WithoutCancel
		// keeps ctx's values without its cancellation, and the timeout bounds
		// how long an unresponsive connection can block it. Mirrors gosmo's
		// capturePlan, discarding the cleanup error the same way.
		defer func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			conn.ExecContext(cctx, "SET "+setOpt+" OFF")
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

	if ctx.Err() != nil {
		res.Messages = append(res.Messages, Message{Text: "Query was cancelled by user.", IsError: true})
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
	res.Elapsed = time.Since(start)
	return res
}

// acquireConnRetryAttempts is the total number of tries (initial + retries)
// acquireConn makes when its connection-liveness prologue fails transiently.
// Mirrors gosmo's readRetryAttempts (gosmo/retry.go).
const acquireConnRetryAttempts = 3

// acquireConnRetryDelay is the backoff before the nth retry (1-based).
// Mirrors gosmo's readRetryDelay.
func acquireConnRetryDelay(attempt int) time.Duration {
	return time.Duration(attempt) * 50 * time.Millisecond
}

// acquireConn returns a live pinned *sql.Conn for execute to run a script's GO
// batches on, already switched to database (via "USE") if non-empty, retrying
// against a fresh connection when the pool hands back a dead one. A batch
// script needs one dedicated connection for its whole run, since temp tables
// and SET options must survive across batches, and database/sql's automatic
// bad-connection retry covers only *sql.DB-level calls, never an already
// pinned *sql.Conn. Without this, a connection silently dropped while idle
// (firewall/NAT timeout, the server killing an idle session, a failover) fails
// the next Execute outright and only the one after it succeeds. gosmo's
// Database.query/queryRow close the same gap for reads.
//
// Only the USE/SELECT-1 prologue is retried, never a caller's batch: silently
// re-running arbitrary user SQL on a fresh connection could re-apply side
// effects that already partially ran.
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

	// Bounded by the attempt >= check below; an `attempt <= N` loop needs an
	// unreachable return after it just to compile. >= not ==, so the bound
	// holds for any acquireConnRetryAttempts; == would spin forever at 0.
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

// currentDatabase reads DB_NAME() off conn — the same connection the script's
// batches just ran on, not a fresh pooled one — so a mid-script "USE otherdb"
// is visible despite being session-scoped state.
func currentDatabase(ctx context.Context, conn *sql.Conn) (string, error) {
	var name string
	err := conn.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&name)
	return name, err
}

// runBatch executes one GO batch and drains the sqlexp message stream,
// appending result sets and messages to res. SQL errors are messages, not
// early returns — later statements in the batch may still have produced
// output, and SSMS reports it all.
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
			if !scanNext(rows, res, sink) {
				// scanNext gave up part-way through the set. The message
				// loop can't advance past one with rows still pending, so
				// finish reading it here — and only here: an extra Next()
				// once the set is exhausted makes the driver swallow the
				// message retmsg is waiting for, and the result set never
				// reaches res at all.
				for rows.Next() {
				}
			}
		case sqlexp.MsgNextResultSet:
			active = rows.NextResultSet()
		default:
			// Unreachable against sqlexp's current closed message set.
			// Without it, a type added by a future sqlexp spins this loop at
			// 100% CPU with no way out; reporting and stopping turns that
			// hang into something the Messages tab names.
			res.addError(fmt.Errorf("unexpected message type %T from the driver", m))
			active = false
		}
	}
	if err := rows.Err(); err != nil && ctx.Err() == nil {
		res.addError(err)
	}
}

// rowScanner holds the per-column scan targets and formatting decisions for
// one result set, so a row is read and rendered without redoing the
// column-type analysis. Shared by scanResultSet (which retains rows) and
// streamResultSet (which writes them straight out).
type rowScanner struct {
	cols        []string
	types       []string
	vals        []any
	ptrs        []any
	guids       []*mssql.NullUniqueIdentifier
	decimalLike []bool
	layouts     []string

	// buf renders one cell at a time, reused for every cell of every row: the
	// bytes are copied out by cellArena.str (or into a fresh string) before
	// the next cell overwrites them.
	buf []byte
}

// newRowScanner analyses the current result set's columns once.
//
// uniqueidentifier scans as a raw 16-byte []byte, which renders as hex in the
// wrong byte order; NullUniqueIdentifier gives the canonical dashed GUID and
// preserves NULL.
//
// decimal/numeric/money/smallmoney also scan as []byte, but the driver has
// already decoded them to an ASCII digit string ("0.070312"), so formatValue
// must render that []byte as text, not hex. (numeric reports as DECIMAL.)
//
// Every date/time type scans as a time.Time, so only the column type and
// declared scale say how much SSMS shows: a date has no time part, a time no
// date, a datetime2(3) three fractional digits. layouts carries that per
// column.
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

// scan reads the current row and renders it as display strings into row,
// which must have one slot per column.
//
// A nil arena makes every cell its own ordinary string — right for the
// streaming path, which hands each row to a sink and keeps nothing. A non-nil
// one packs the cells instead, so a retained result set costs a handful of
// large allocations rather than one per cell.
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
		// Drop the driver's copy now the cell is rendered: without this a
		// []byte or string column keeps its per-row allocation alive until
		// the next row overwrites vals[i] — for the last row of a huge set,
		// until the whole Result is dropped.
		sc.vals[i] = nil
	}
	return nil
}

// scanResultSet reads the whole of rows' current result set into string cells.
// There is no row cap, so the cells and per-row slices are packed into a
// cellArena to keep a very large set as close to the size of its text as
// possible.
func scanResultSet(rows *sql.Rows) (ResultSet, error) {
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
	}
	return rs, nil
}

// streamResultSet writes the current result set straight to sink, retaining
// nothing, and returns how many rows it wrote. This is what makes Results To
// File independent of result size: scanResultSet holds every row for the
// lifetime of the panel, this holds one at a time.
func streamResultSet(rows *sql.Rows, sink RowSink) (n int, err error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return 0, err
	}
	// Paired with BeginSet on every exit, so a scan or Row failure part-way
	// through still closes the set out. Registered *before* BeginSet: every
	// begun set must end, and a sink that took a lock or allocated per-set
	// state before the failure that aborted its BeginSet has EndSet as its
	// only place to undo it. Named returns because the BeginSet/scan/Row
	// error is the one worth reporting, and EndSet's must surface only when
	// nothing else failed.
	defer func() {
		if endErr := sink.EndSet(n); endErr != nil && err == nil {
			err = endErr
		}
	}()
	if err = sink.BeginSet(sc.cols); err != nil {
		return 0, err
	}
	// One row buffer for the whole set: sink.Row must consume what it is
	// given before returning, so it can be overwritten.
	row := make([]string, len(sc.cols))
	for rows.Next() {
		if err = sc.scan(rows, row, nil); err != nil {
			return n, err
		}
		if err = sink.Row(row); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// showplanColumnName is the fixed column name SQL Server has used for SET
// STATISTICS XML / SHOWPLAN_XML output since SQL Server 2005. Mirrors gosmo's
// unexported showplanColumn constant.
const showplanColumnName = "Microsoft SQL Server 2005 XML Showplan"

// isShowplanResultSet reports whether cols is the single-column shape SQL
// Server uses for execution-plan output, rather than a real result set.
func isShowplanResultSet(cols []string) bool {
	return len(cols) == 1 && cols[0] == showplanColumnName
}

// scanNext consumes the result set sqlexp's MsgNext just announced, appending
// it to res as either a showplan XML document or a grid of rows. Errors are
// recorded on res rather than returned — a batch keeps running after one, as
// SSMS does — and reported as a false return, meaning the set was abandoned
// with rows still pending for the caller to drain.
func scanNext(rows *sql.Rows, res *Result, sink RowSink) bool {
	cols, err := rows.Columns()
	if err != nil {
		res.addError(err)
		return false
	}
	if isShowplanResultSet(cols) {
		xml, err := scanPlanXML(rows)
		if err != nil {
			res.addError(err)
			return false
		}
		if xml != "" {
			res.PlanXML = append(res.PlanXML, xml)
		}
		return true
	}
	if sink != nil {
		n, err := streamResultSet(rows, sink)
		res.RowsWritten += n
		res.sinkSets++
		if err != nil {
			res.addError(err)
			return false
		}
		res.addNotice(fmt.Sprintf("(%d row(s) written)", n))
		return true
	}
	rs, err := scanResultSet(rows)
	if err != nil {
		res.addError(err)
		return false
	}
	res.Sets = append(res.Sets, rs)
	return true
}

// scanPlanXML reads the current single-column showplan result set into one XML
// string. Mirrors gosmo's capturePlan scan loop.
func scanPlanXML(rows *sql.Rows) (string, error) {
	var xml string
	for rows.Next() {
		if err := rows.Scan(&xml); err != nil {
			return "", err
		}
	}
	return xml, rows.Err()
}

// appendGUID appends a uniqueidentifier as SSMS renders it: NULL, or the
// canonical uppercase dashed form.
func appendGUID(dst []byte, g mssql.NullUniqueIdentifier) []byte {
	if !g.Valid {
		return append(dst, "NULL"...)
	}
	return append(dst, g.UUID.String()...)
}

// formatGUID is appendGUID's standalone form.
func formatGUID(g mssql.NullUniqueIdentifier) string {
	return string(appendGUID(nil, g))
}

// defaultTimeLayout renders a time.Time from a column whose type didn't name a
// layout — a sql_variant holding a datetime, say. Matches plain "datetime".
const defaultTimeLayout = "2006-01-02 15:04:05.000"

// timeLayout returns the layout SSMS's grid uses for a date/time column of the
// given SQL Server type, or "" for a type that isn't one. Each type shows
// exactly the parts it stores: one fixed "datetime" layout for all of them
// invents a "00:00:00.000" for every date column and truncates a datetime2's
// last four digits.
//
// datetime2, time and datetimeoffset carry a declared scale of 0-7 setting
// their fractional-second digits, so scale comes from DecimalSize (reported
// for exactly those three); scaleKnown false falls back to 7. The other
// types' precision is fixed by the type.
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

// fracLayout returns the fractional-second fragment of a Go time layout for
// the given scale — "" at scale 0, which prints no decimal point, the way SSMS
// shows a time(0).
func fracLayout(scale int) string {
	if scale <= 0 {
		return ""
	}
	return "." + strings.Repeat("0", min(scale, 7))
}

// formatValue renders one cell the way SSMS displays it: NULL for nil, 1/0 for
// bit, 0x… for binary, and a date/time in its column type's layout.
// isDecimalLike marks a []byte cell holding a decoded decimal/money ASCII
// digit string rather than binary data, so it renders as text instead of hex.
// layout is the column's time layout, empty for a non-date/time column.
func formatValue(v any, isDecimalLike bool, layout string) string {
	return string(appendValue(nil, v, isDecimalLike, layout))
}

// appendValue is formatValue in append form, so a caller scanning many rows
// renders every cell through one reused buffer instead of allocating a string
// per cell on the way to retaining a packed copy.
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

// appendHexUpper appends b as SSMS's "0x…" uppercase hex literal in one pass;
// hex.EncodeToString plus strings.ToUpper builds two throwaway copies of every
// binary cell.
func appendHexUpper(dst []byte, b []byte) []byte {
	dst = append(dst, '0', 'x')
	for _, c := range b {
		dst = append(dst, hexUpperDigits[c>>4], hexUpperDigits[c&0x0f])
	}
	return dst
}

const hexUpperDigits = "0123456789ABCDEF"

// appendFloat renders a float/real column the way SSMS's grid does: plain
// decimal across the range a person actually reads, scientific notation only
// outside it. Go's "%v"/%g rule switches to an exponent as soon as the exponent
// reaches the number of significant digits, so a float holding 1000000 shows
// as "1e+06". Shortest-round-trip precision (-1) is kept either way: it makes
// the text reparse to the same float64, which matters for a value copied out
// of the grid and pasted back into a query.
func appendFloat(dst []byte, f float64, bits int) []byte {
	abs := math.Abs(f)
	if f != 0 && !math.IsInf(f, 0) && !math.IsNaN(f) && (abs < 1e-4 || abs >= 1e15) {
		return strconv.AppendFloat(dst, f, 'e', -1, bits)
	}
	return strconv.AppendFloat(dst, f, 'f', -1, bits)
}

// formatFloat is appendFloat's standalone form.
func formatFloat(f float64, bits int) string {
	return string(appendFloat(nil, f, bits))
}
