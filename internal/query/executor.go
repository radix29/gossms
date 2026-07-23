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

func execute(ctx context.Context, db *sql.DB, database, script string, maxRows int, capture planCapture) *Result {
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
		// Error discarded on cleanup — matches gosmo's own capturePlan,
		// which does the same for its SET ... OFF.
		defer conn.ExecContext(context.Background(), "SET "+setOpt+" OFF")
	}

	for _, b := range batch.Split(script, "GO") {
		if strings.TrimSpace(b) == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		runBatch(ctx, conn, b, res, maxRows)
	}

	if ctx.Err() != nil {
		res.Messages = append(res.Messages, Message{Text: "Query was cancelled by user.", IsError: true})
	} else {
		if name, err := currentDatabase(ctx, conn); err == nil {
			res.Database = name
		}
		// planCaptureEstimated never really executes anything — "Commands
		// completed successfully" would be misleading there, not merely
		// redundant, since nothing did.
		if len(res.Sets) == 0 && !res.HasErrors() && capture != planCaptureEstimated {
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
// server killing an idle session, a failover) surfaced as an outright
// failure on the very next Execute, and only the one after that (which
// happened to dial a fresh connection) succeeded — this closes that gap
// the same way gosmo's own Database.query/queryRow already do for reads.
//
// Only this function's own USE/SELECT-1 prologue is ever retried, never one
// of the caller's actual batches — those still fail outright if the
// connection dies mid-script, exactly as before, since silently re-running
// arbitrary user SQL against a fresh connection could re-apply side effects
// that already partially ran.
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

	var lastErr error
	for attempt := 1; attempt <= acquireConnRetryAttempts; attempt++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, prologue); err != nil {
			conn.Close() // dead — evicted from the pool via driver.Validator.IsValid
			lastErr = err
			if ctx.Err() != nil || attempt == acquireConnRetryAttempts || !gosmo.IsRetryable(err) {
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
	return nil, wrapErr(lastErr)
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
func runBatch(ctx context.Context, conn *sql.Conn, sqlText string, res *Result, maxRows int) {
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
			cols, err := rows.Columns()
			if err != nil {
				res.addError(err)
				break
			}
			if isShowplanResultSet(cols) {
				xml, err := scanPlanXML(rows)
				switch {
				case err != nil:
					res.addError(err)
				case xml != "":
					res.PlanXML = append(res.PlanXML, xml)
				}
				break
			}
			rs, truncated, err := scanResultSet(rows, maxRows)
			if err != nil {
				res.addError(err)
			} else {
				res.Sets = append(res.Sets, rs)
				if truncated {
					res.addNotice(fmt.Sprintf("Only the first %d row(s) are shown — increase Max Result Rows in Tools > Options to see more.", maxRows))
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
	cols, err := rows.Columns()
	if err != nil {
		return ResultSet{}, false, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return ResultSet{}, false, err
	}
	rs := ResultSet{Columns: cols}

	// uniqueidentifier columns scan as a raw 16-byte []byte by default, which
	// would render as hex in the wrong byte order. Scan them into
	// NullUniqueIdentifier instead so they display as the canonical dashed
	// GUID (and NULL survives), matching SSMS.
	//
	// decimal/numeric/money/smallmoney columns also scan as []byte, but
	// unlike uniqueidentifier the driver already decodes them into the
	// literal ASCII digit string (e.g. "0.070312") rather than a binary
	// blob — formatValue must render that []byte as a plain string, not hex.
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	guids := make([]*mssql.NullUniqueIdentifier, len(cols))
	decimalLike := make([]bool, len(cols))
	for i := range cols {
		switch types[i].DatabaseTypeName() {
		case "UNIQUEIDENTIFIER":
			guids[i] = &mssql.NullUniqueIdentifier{}
			ptrs[i] = guids[i]
			continue
		case "DECIMAL", "MONEY", "SMALLMONEY":
			decimalLike[i] = true
		}
		ptrs[i] = &vals[i]
	}
	truncated := false
	for rows.Next() {
		if maxRows > 0 && len(rs.Rows) >= maxRows {
			truncated = true
			continue // keep draining the stream without scanning/retaining
		}
		if err := rows.Scan(ptrs...); err != nil {
			return rs, truncated, err
		}
		row := make([]string, len(cols))
		for i := range cols {
			if g := guids[i]; g != nil {
				row[i] = formatGUID(*g)
			} else {
				row[i] = formatValue(vals[i], decimalLike[i])
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, truncated, nil
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

// formatValue renders one cell the way SSMS displays it: NULL for nil,
// 1/0 for bit, 0x… for binary, "2006-01-02 15:04:05.000" for datetimes.
// isDecimalLike marks a []byte cell that actually holds a decoded
// decimal/money ASCII digit string rather than binary data (see
// scanResultSet), so it's rendered as text instead of hex.
func formatValue(v any, isDecimalLike bool) string {
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
		return x.Format("2006-01-02 15:04:05.000")
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}
