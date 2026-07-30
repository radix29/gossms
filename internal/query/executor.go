// Package query executes T-SQL scripts the way SSMS does: the script is
// split into GO batches, every batch runs on one dedicated connection (so
// temp tables and SET options survive across batches), and the driver's
// message stream is captured alongside the result sets — PRINT output,
// "(n rows affected)" counts, and SQL errors all land in Result.Messages.
package query

import (
	"context"
	"database/sql"
	"encoding/hex"
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

	// Database is the database in effect when execution finished — read
	// off the same connection the script ran on (see Execute), so a
	// mid-script "USE otherdb" is reflected back to the caller. Empty if
	// it couldn't be read (e.g. the query was cancelled first).
	Database string

	// RowsWritten totals the rows handed to a RowSink (see ExecuteToSink),
	// across every result set. Zero for Execute and friends, which retain
	// rows in Sets instead.
	RowsWritten int

	// sinkSets counts the result sets streamed to a RowSink, whether or not
	// they held any rows. It is what the "Commands completed successfully."
	// decision needs on the ExecuteToSink path: Sets is always empty there,
	// so len(Sets) can't answer "did a result set happen", and RowsWritten
	// can't either — an empty set writes no rows, and reading that as "no
	// set" printed both "(0 row(s) written)" and "Commands completed
	// successfully." for a query that plainly did return a result set.
	sinkSets int

	// PlanXML holds one complete <ShowPlanXML> document per statement/batch
	// whose execution plan was captured, in execution order — the actual
	// plan from ExecuteWithPlan, or the estimated (compile-only) plan from
	// ExecuteEstimatedPlan. Execute itself never populates this.
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

// ErrorMessages formats err the way SSMS's Messages pane shows a failed
// batch: a SQL Server error becomes the "Msg 208, Level 16, State 1, Line 4"
// status line and the message text as two separate messages; anything else
// becomes a single message from err.Error(). Exported so a caller that talks
// to gosmo directly instead of through Execute (QueryPanel's execution-plan
// paths) can report an error identically.
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
// The test is "did any result set happen", not "did any row". Sets answers
// that for Execute, sinkSets for ExecuteToSink — where Sets is always empty
// because the rows went to the sink instead. Reading RowsWritten as the
// stand-in (which it was) made a query that returned an *empty* result set
// look like a script that returned none, so exporting one printed both
// "(0 row(s) written)" and "Commands completed successfully.", while the
// same query through Execute printed neither.
//
// planCaptureEstimated never really executes anything, so the notice would be
// misleading there rather than merely redundant.
//
// Split out of executeWithSink to be testable at all: ExecuteToSink can't be
// driven end to end by a fake driver (see stream_test.go), so this decision
// is the part of that path a unit test can still reach.
func (r *Result) shouldReportSuccess(capture planCapture) bool {
	return len(r.Sets) == 0 && r.sinkSets == 0 && !r.HasErrors() && capture != planCaptureEstimated
}

// planCapture selects whether execute additionally captures an execution
// plan alongside a script's ordinary batches, and if so, in which SQL
// Server mode.
type planCapture int

const (
	planCaptureNone      planCapture = iota
	planCaptureActual                // SET STATISTICS XML ON — batches really run
	planCaptureEstimated             // SET SHOWPLAN_XML ON — nothing really runs
)

// Execute runs script against db, SSMS-style. If database is non-empty the
// connection switches to it first ("USE [database]"), so the script runs in
// that database context. The script is split on GO separators; a failing
// batch is reported in Messages and execution continues with the next batch,
// matching SSMS. Cancelling ctx stops between (and inside) batches; the
// partial Result is still returned.
//
// maxRows caps how many rows each result set keeps in memory (0 or negative
// means unlimited) — a query is still executed and drained to completion
// either way, so row/rows-affected counts and later batches are unaffected;
// rows past the cap are simply not scanned or retained, and a Messages
// notice reports the truncation. Meant for the interactive Grid/Text views;
// pass 0 for a caller (e.g. Results To File) that wants every row.
func Execute(ctx context.Context, db *sql.DB, database, script string, maxRows int) *Result {
	return execute(ctx, db, database, script, maxRows, planCaptureNone)
}

// ExecuteWithPlan behaves like Execute but additionally runs with SET
// STATISTICS XML ON, so the script's actual execution plan — captured
// after it really runs, not just compiled — comes back in Result.PlanXML.
// Everything else (GO-batch splitting, message capture, row scanning,
// cancellation) is identical to Execute.
func ExecuteWithPlan(ctx context.Context, db *sql.DB, database, script string, maxRows int) *Result {
	return execute(ctx, db, database, script, maxRows, planCaptureActual)
}

// ExecuteEstimatedPlan behaves like Execute but runs with SET SHOWPLAN_XML
// ON instead of actually running the script — SQL Server compiles every GO
// batch and returns its estimated plan in Result.PlanXML without executing
// it, matching SSMS's "Display Estimated Execution Plan". GO-batch
// splitting and cancellation are identical to Execute; unlike Execute/
// ExecuteWithPlan, nothing in the script produces real rows or "rows
// affected" messages while SHOWPLAN_XML is on, so there's no meaningful
// row cap to accept.
func ExecuteEstimatedPlan(ctx context.Context, db *sql.DB, database, script string) *Result {
	return execute(ctx, db, database, script, 0, planCaptureEstimated)
}

// RowSink receives a script's result rows as they are scanned, instead of
// them being retained in Result.Sets. Results To File is the caller: it
// writes each row to a CSV file and keeps none, so an export is bounded by
// the file rather than by memory.
//
// BeginSet is called once per result set before its first row, EndSet once
// after its last with the number of rows written. A returned error aborts
// that result set and is reported in Result.Messages like any other failure;
// the rest of the script still runs, matching how Execute treats a failed
// batch.
//
// EndSet is called for every set BeginSet was called for, including one
// abandoned part-way by a Row error — so a sink that finalises anything per
// set (a flush, a footer) can do it there and nowhere else. Its row count is
// how many rows actually reached Row, not how many the set held.
type RowSink interface {
	BeginSet(columns []string) error
	Row(cells []string) error
	EndSet(rows int) error
}

// ExecuteToSink behaves like Execute but streams every row to sink instead of
// accumulating it in Result.Sets, which comes back empty. Row counts are
// reported per set in Result.Messages, and Result.RowsWritten totals them.
//
// There is no row cap: the point of this path is to write everything the
// query returned. Nothing is retained, so an unbounded result set costs
// unbounded file, not unbounded memory.
func ExecuteToSink(ctx context.Context, db *sql.DB, database, script string, sink RowSink) *Result {
	return executeWithSink(ctx, db, database, script, 0, planCaptureNone, sink)
}

func execute(ctx context.Context, db *sql.DB, database, script string, maxRows int, capture planCapture) *Result {
	return executeWithSink(ctx, db, database, script, maxRows, capture, nil)
}

func executeWithSink(ctx context.Context, db *sql.DB, database, script string, maxRows int, capture planCapture, sink RowSink) *Result {
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
		// Cleanup must still run (and return conn to the pool in a known
		// state) even if ctx is already canceled by the time execute
		// returns — context.WithoutCancel keeps ctx's values without its
		// cancellation, and the timeout bounds how long a genuinely
		// unresponsive connection can block it, unlike context.Background()
		// which never times out. Mirrors gosmo's own capturePlan
		// (executionplan.go), error discarded on cleanup same as there.
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
		runBatch(ctx, conn, b, res, maxRows, sink)
	}

	if ctx.Err() != nil {
		res.Messages = append(res.Messages, Message{Text: "Query was cancelled by user.", IsError: true})
	} else {
		if name, err := currentDatabase(ctx, conn); err == nil {
			res.Database = name
		}
		if res.shouldReportSuccess(capture) {
			res.addNotice("Commands completed successfully.")
		}
	}
	res.Elapsed = time.Since(start)
	return res
}

// acquireConnRetryAttempts is the total number of tries (initial + retries)
// acquireConn makes when its connection-liveness prologue fails transiently
// — mirrors gosmo's own readRetryAttempts (gosmo/retry.go), the same
// tuning already trusted for gosmo's Database.query/queryRow.
const acquireConnRetryAttempts = 3

// acquireConnRetryDelay is the backoff before the nth retry (1-based) —
// mirrors gosmo's own readRetryDelay.
func acquireConnRetryDelay(attempt int) time.Duration {
	return time.Duration(attempt) * 50 * time.Millisecond
}

// acquireConn returns a live pinned *sql.Conn for execute to run a script's
// GO batches on, already switched to database (via "USE") if non-empty —
// retrying against a fresh connection when the pool hands back one that's
// dead. A batch script needs one dedicated connection for its whole run
// (temp tables and SET options must survive across batches — see execute),
// so unlike every other call in this package it can't just go through db's
// own pool-level Query/ExecContext, which retries a dead pooled connection
// automatically; a *sql.Conn pinned via db.Conn gets none of that —
// database/sql's automatic bad-connection retry only covers *sql.DB-level
// calls, never one already pinned out of the pool. Without this, a
// connection silently dropped while idle (a firewall/NAT timeout, the
// server killing an idle session, a failover) fails the very next Execute
// outright, and only the one after it — which happens to dial fresh —
// succeeds. gosmo's own Database.query/queryRow close the same gap for
// reads.
//
// Only this function's own USE/SELECT-1 prologue is ever retried, never one
// of the caller's actual batches: those still fail outright if the
// connection dies mid-script, since silently re-running arbitrary user SQL
// against a fresh connection could re-apply side effects that already
// partially ran.
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

	// Unbounded in form, bounded in fact: the attempt >= last check below is
	// the only way out other than success, which is also what makes this
	// total — a `attempt <= N` loop needs an unreachable return after it just
	// to compile. >= rather than ==, so the bound holds by construction for
	// any value of acquireConnRetryAttempts rather than by its happening to
	// be 3; == would spin forever if it were ever set to 0.
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

// currentDatabase reads DB_NAME() off conn — the same connection the
// script's batches just ran on, not a fresh pooled one — so a mid-script
// "USE otherdb" is visible here even though it's session-scoped state that
// wouldn't survive onto a different connection.
func currentDatabase(ctx context.Context, conn *sql.Conn) (string, error) {
	var name string
	err := conn.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&name)
	return name, err
}

// runBatch executes one GO batch and drains the sqlexp message stream,
// appending result sets and messages to res. SQL errors are messages, not
// early returns — later statements in the batch may still have produced
// output, and SSMS reports it all. maxRows is passed straight to
// scanResultSet — see Execute's doc comment.
func runBatch(ctx context.Context, conn *sql.Conn, sqlText string, res *Result, maxRows int, sink RowSink) {
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
			if !scanNext(rows, res, maxRows, sink) {
				// scanNext gave up part-way through the result set. The
				// message loop can't advance past one with rows still
				// pending, so finish reading it here. Only on this path:
				// an extra Next() once the set is already exhausted makes
				// the driver swallow the message retmsg is waiting for,
				// and the result set never reaches res at all.
				for rows.Next() {
				}
			}
		case sqlexp.MsgNextResultSet:
			active = rows.NextResultSet()
		}
	}
	if err := rows.Err(); err != nil && ctx.Err() == nil {
		res.addError(err)
	}
}

// rowScanner holds the per-column scan targets and formatting decisions for
// one result set, so a row can be read and rendered without redoing the
// column-type analysis on every one. Shared by scanResultSet (which retains
// rows) and streamResultSet (which writes them straight out).
type rowScanner struct {
	cols        []string
	vals        []any
	ptrs        []any
	guids       []*mssql.NullUniqueIdentifier
	decimalLike []bool
	layouts     []string
}

// newRowScanner analyses the current result set's columns once.
//
// uniqueidentifier columns scan as a raw 16-byte []byte by default, which
// would render as hex in the wrong byte order. Scan them into
// NullUniqueIdentifier instead so they display as the canonical dashed GUID
// (and NULL survives), matching SSMS.
//
// decimal/numeric/money/smallmoney columns also scan as []byte, but unlike
// uniqueidentifier the driver already decodes them into the literal ASCII
// digit string (e.g. "0.070312") rather than a binary blob — formatValue must
// render that []byte as a plain string, not hex. (numeric reports itself as
// DECIMAL; the driver maps both to the same name.)
//
// Every date/time type scans as a time.Time, so only the column type and its
// declared scale say how much of it SSMS actually shows — a date column has no
// time part to print, a time column no date part, and a datetime2(3) three
// fractional digits rather than seven. layouts carries that per column; see
// timeLayout.
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

// scan reads the current row and renders it as display strings.
func (sc *rowScanner) scan(rows *sql.Rows) ([]string, error) {
	if err := rows.Scan(sc.ptrs...); err != nil {
		return nil, err
	}
	row := make([]string, len(sc.cols))
	for i := range sc.cols {
		if g := sc.guids[i]; g != nil {
			row[i] = formatGUID(*g)
		} else {
			row[i] = formatValue(sc.vals[i], sc.decimalLike[i], sc.layouts[i])
		}
	}
	return row, nil
}

// scanResultSet reads the current result set of rows into string cells, up
// to maxRows of them (0 or negative means unlimited) — reporting whether
// the result set actually had more than that. rows.Next() is still called
// until the driver's row stream for this set is exhausted regardless of
// the cap; only the (comparatively expensive) Scan, string-formatting, and
// retention past the cap are skipped, so the sqlexp message-based
// iteration runBatch drives — which expects a result set fully drained
// before the next message can be read — still behaves correctly, and a
// huge SELECT can't grow one ResultSet past what the cap allows.
func scanResultSet(rows *sql.Rows, maxRows int) (ResultSet, bool, error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return ResultSet{}, false, err
	}
	rs := ResultSet{Columns: sc.cols}
	truncated := false
	for rows.Next() {
		if maxRows > 0 && len(rs.Rows) >= maxRows {
			truncated = true
			continue // keep draining the stream without scanning/retaining
		}
		row, err := sc.scan(rows)
		if err != nil {
			return rs, truncated, err
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, truncated, nil
}

// streamResultSet writes the current result set straight to sink, retaining
// nothing, and returns how many rows it wrote.
//
// This is what makes Results To File independent of result size: the
// buffering path above holds every row as a [][]string for the lifetime of
// the panel, so exporting a large table meant materialising all of it (twice
// — once in Result.Sets, once in the grid) before a single byte reached the
// file. Streaming holds one row at a time.
func streamResultSet(rows *sql.Rows, sink RowSink) (n int, err error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return 0, err
	}
	if err := sink.BeginSet(sc.cols); err != nil {
		return 0, err
	}
	// Paired with BeginSet on every exit, so a scan or Row failure part-way
	// through still closes the set out — see RowSink. Named returns, because
	// the scan/Row error is the one worth reporting and EndSet's must only
	// surface when nothing else already failed.
	defer func() {
		if endErr := sink.EndSet(n); endErr != nil && err == nil {
			err = endErr
		}
	}()
	for rows.Next() {
		var row []string
		if row, err = sc.scan(rows); err != nil {
			return n, err
		}
		if err = sink.Row(row); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// showplanColumnName is the fixed column name SQL Server has used for
// SET STATISTICS XML / SHOWPLAN_XML output since SQL Server 2005 — mirrors
// gosmo's own (unexported) showplanColumn constant.
const showplanColumnName = "Microsoft SQL Server 2005 XML Showplan"

// isShowplanResultSet reports whether cols is the single-column shape SQL
// Server uses for execution-plan output, rather than a real result set.
func isShowplanResultSet(cols []string) bool {
	return len(cols) == 1 && cols[0] == showplanColumnName
}

// scanNext consumes the result set sqlexp's MsgNext just announced,
// appending it to res as either a showplan XML document or a grid of rows.
// Errors are recorded on res rather than returned — a batch keeps running
// after one, the way SSMS does — and reported as a false return, meaning
// the set was abandoned with rows still pending for the caller to drain.
func scanNext(rows *sql.Rows, res *Result, maxRows int, sink RowSink) bool {
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
	rs, truncated, err := scanResultSet(rows, maxRows)
	if err != nil {
		res.addError(err)
		return false
	}
	res.Sets = append(res.Sets, rs)
	if truncated {
		res.addNotice(fmt.Sprintf("Only the first %d row(s) are shown — increase Max Result Rows in Tools > Options to see more.", maxRows))
	}
	return true
}

// scanPlanXML reads the current (single-column, showplan) result set into
// one XML string — mirrors gosmo's capturePlan scan loop.
func scanPlanXML(rows *sql.Rows) (string, error) {
	var xml string
	for rows.Next() {
		if err := rows.Scan(&xml); err != nil {
			return "", err
		}
	}
	return xml, rows.Err()
}

// formatGUID renders a uniqueidentifier as SSMS does: NULL, or the canonical
// uppercase dashed form.
func formatGUID(g mssql.NullUniqueIdentifier) string {
	if !g.Valid {
		return "NULL"
	}
	return g.UUID.String()
}

// defaultTimeLayout renders a time.Time that arrived from a column whose
// type didn't name one — a sql_variant holding a datetime, say. Matches
// what plain "datetime" gets, the most common case by far.
const defaultTimeLayout = "2006-01-02 15:04:05.000"

// timeLayout returns the layout SSMS's grid uses for a date/time column of
// the given SQL Server type, or "" for a type that isn't one. Each type
// shows exactly the parts it stores and no more: a date column has no time
// of day, a time column no date. Rendering them all as "datetime" — which
// is what a single fixed layout does — invents a "00:00:00.000" for every
// date column and silently truncates a datetime2's last four digits.
//
// datetime2, time and datetimeoffset carry a declared scale of 0-7 that
// sets how many fractional-second digits they show, so scale comes from
// the column's own DecimalSize (the driver reports it for exactly these
// three types); scaleKnown false falls back to the type's 7-digit maximum.
// The other types' precision is fixed by the type itself.
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

// fracLayout returns the fractional-second fragment of a Go time layout
// for the given scale — "" at scale 0, which prints no decimal point at
// all, the way SSMS shows a time(0).
func fracLayout(scale int) string {
	if scale <= 0 {
		return ""
	}
	return "." + strings.Repeat("0", min(scale, 7))
}

// formatValue renders one cell the way SSMS displays it: NULL for nil,
// 1/0 for bit, 0x… for binary, and a date/time in its own column type's
// layout. isDecimalLike marks a []byte cell that actually holds a decoded
// decimal/money ASCII digit string rather than binary data (see
// scanResultSet), so it's rendered as text instead of hex. layout is that
// column's time layout (see timeLayout), empty for a non-date/time column.
func formatValue(v any, isDecimalLike bool, layout string) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if x {
			return "1"
		}
		return "0"
	case []byte:
		if isDecimalLike {
			return string(x)
		}
		return "0x" + strings.ToUpper(hex.EncodeToString(x))
	case time.Time:
		if layout == "" {
			layout = defaultTimeLayout
		}
		return x.Format(layout)
	case float64:
		return formatFloat(x, 64)
	case float32:
		return formatFloat(float64(x), 32)
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

// formatFloat renders a float/real column the way SSMS's grid does: plain
// decimal across the range a person actually reads, scientific notation only
// outside it.
//
// The default "%v" this replaces uses Go's %g rule, which switches to an
// exponent as soon as the exponent reaches the number of significant digits —
// so a float column holding 1000000 displayed as "1e+06". Shortest-round-trip
// precision (-1) is kept either way: it is what makes the text reparse to the
// same float64, which matters for a value copied out of the grid and pasted
// back into a query.
func formatFloat(f float64, bits int) string {
	abs := math.Abs(f)
	if f != 0 && !math.IsInf(f, 0) && !math.IsNaN(f) && (abs < 1e-4 || abs >= 1e15) {
		return strconv.FormatFloat(f, 'e', -1, bits)
	}
	return strconv.FormatFloat(f, 'f', -1, bits)
}
