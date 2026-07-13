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
	// SQL Server errors get the SSMS treatment: the "Msg 208, Level 16,
	// State 1, Line 4" status line and the message text on separate lines,
	// so the Messages pane matches SSMS and the line number is visible.
	if se, ok := gosmo.AsSQLError(err); ok {
		r.Messages = append(r.Messages, Message{Text: se.Header(), IsError: true})
		if se.Message != "" {
			r.Messages = append(r.Messages, Message{Text: se.Message, IsError: true})
		}
		return
	}
	r.Messages = append(r.Messages, Message{Text: err.Error(), IsError: true})
}
func (r *Result) addNotice(s string) { r.Messages = append(r.Messages, Message{Text: s}) }

// Execute runs script against db, SSMS-style. If database is non-empty the
// connection switches to it first ("USE [database]"), so the script runs in
// that database context. The script is split on GO separators; a failing
// batch is reported in Messages and execution continues with the next batch,
// matching SSMS. Cancelling ctx stops between (and inside) batches; the
// partial Result is still returned.
func Execute(ctx context.Context, db *sql.DB, database, script string) *Result {
	start := time.Now()
	res := &Result{}

	conn, err := db.Conn(ctx)
	if err != nil {
		res.addError(err)
		res.Elapsed = time.Since(start)
		return res
	}
	defer conn.Close()

	if database != "" {
		if _, err := conn.ExecContext(ctx, "USE "+gosmo.QuoteName(database)); err != nil {
			res.addError(fmt.Errorf("switch to database %s: %w", database, err))
			res.Elapsed = time.Since(start)
			return res
		}
	}

	for _, b := range batch.Split(script, "GO") {
		if strings.TrimSpace(b) == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		runBatch(ctx, conn, b, res)
	}

	if ctx.Err() != nil {
		res.Messages = append(res.Messages, Message{Text: "Query was cancelled by user.", IsError: true})
	} else {
		if name, err := currentDatabase(ctx, conn); err == nil {
			res.Database = name
		}
		if len(res.Sets) == 0 && !res.HasErrors() {
			res.addNotice("Commands completed successfully.")
		}
	}
	res.Elapsed = time.Since(start)
	return res
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
// output, and SSMS reports it all.
func runBatch(ctx context.Context, conn *sql.Conn, sqlText string, res *Result) {
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
			rs, err := scanResultSet(rows)
			if err != nil {
				res.addError(err)
			} else {
				res.Sets = append(res.Sets, rs)
			}
		case sqlexp.MsgNextResultSet:
			active = rows.NextResultSet()
		}
	}
	if err := rows.Err(); err != nil && ctx.Err() == nil {
		res.addError(err)
	}
}

// scanResultSet reads the current result set of rows into string cells.
func scanResultSet(rows *sql.Rows) (ResultSet, error) {
	cols, err := rows.Columns()
	if err != nil {
		return ResultSet{}, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return ResultSet{}, err
	}
	rs := ResultSet{Columns: cols}

	// uniqueidentifier columns scan as a raw 16-byte []byte by default, which
	// would render as hex in the wrong byte order. Scan them into
	// NullUniqueIdentifier instead so they display as the canonical dashed
	// GUID (and NULL survives), matching SSMS.
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	guids := make([]*mssql.NullUniqueIdentifier, len(cols))
	for i := range cols {
		if types[i].DatabaseTypeName() == "UNIQUEIDENTIFIER" {
			guids[i] = &mssql.NullUniqueIdentifier{}
			ptrs[i] = guids[i]
		} else {
			ptrs[i] = &vals[i]
		}
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return rs, err
		}
		row := make([]string, len(cols))
		for i := range cols {
			if g := guids[i]; g != nil {
				row[i] = formatGUID(*g)
			} else {
				row[i] = formatValue(vals[i])
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, nil
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
func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if x {
			return "1"
		}
		return "0"
	case []byte:
		return "0x" + strings.ToUpper(hex.EncodeToString(x))
	case time.Time:
		return x.Format("2006-01-02 15:04:05.000")
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}
