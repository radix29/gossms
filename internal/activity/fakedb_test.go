package activity

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// A scripted *sql.DB for the DMV readers. Every collect* function in this
// package is a query and a scan, and the scan is where a column read in the
// wrong order or into the wrong field goes unnoticed: the numbers stay
// plausible and nothing errors. This answers each query with rows the test
// wrote, so an assertion can name the value it expects to come back.
//
// Replies are keyed by the query *constant* — cpuUsageQuery, schedQuery,
// the rest — so a test says which read it is answering rather than counting
// queries in order. An unscripted query is an error naming itself, not an
// empty result: a silently empty read is exactly the failure these tests
// exist to catch, so the fake must never produce one by accident.

// reply is one scripted answer. err, if set, is returned instead of rows.
type reply struct {
	cols []string
	rows [][]driver.Value
	err  error
}

// scriptedDB builds an *sql.DB answering from answers, and returns it with
// the log of statements it was asked for. The permission prologue is
// answered automatically — every collector run needs it and no test is
// about it.
func scriptedDB(t *testing.T, answers map[string]reply) (*sql.DB, *stmtLog) {
	t.Helper()
	log := &stmtLog{}
	full := map[string]reply{permissionQuery: {cols: []string{"ok"}, rows: [][]driver.Value{{int64(1)}}}}
	for q, r := range answers {
		full[q] = r
	}
	db := sql.OpenDB(scriptedConnector{answers: full, log: log})
	t.Cleanup(func() { db.Close() })
	return db, log
}

// stmtLog records every statement the fake was asked for, so a test can
// assert what was read as well as what came back.
type stmtLog struct {
	mu sync.Mutex
	qs []string
}

func (l *stmtLog) add(q string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.qs = append(l.qs, q)
}

// count returns how many statements contain needle.
func (l *stmtLog) count(needle string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, q := range l.qs {
		if strings.Contains(q, needle) {
			n++
		}
	}
	return n
}

type scriptedConnector struct {
	answers map[string]reply
	log     *stmtLog
}

func (c scriptedConnector) Connect(context.Context) (driver.Conn, error) {
	return scriptedConn(c), nil
}

func (scriptedConnector) Driver() driver.Driver { return nil }

type scriptedConn struct {
	answers map[string]reply
	log     *stmtLog
}

func (scriptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("activity_test: prepare unsupported")
}

func (scriptedConn) Close() error { return nil }

func (scriptedConn) Begin() (driver.Tx, error) {
	return nil, errors.New("activity_test: transactions unsupported")
}

func (c scriptedConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	c.log.add(q)
	r, ok := c.answers[q]
	if !ok {
		return nil, fmt.Errorf("activity_test: no scripted answer for query:\n%s", q)
	}
	if r.err != nil {
		return nil, r.err
	}
	return &scriptedRows{cols: r.cols, rows: r.rows}, nil
}

func (c scriptedConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	c.log.add(q)
	if r, ok := c.answers[q]; ok && r.err != nil {
		return nil, r.err
	}
	return driver.RowsAffected(0), nil
}

type scriptedRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *scriptedRows) Columns() []string { return r.cols }

func (r *scriptedRows) Close() error { return nil }

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}
