//go:build livedb

// Live verification of runBatch's drain gate against a real SQL Server.
//
// The gate is the `for rows.Next() {}` loop runBatch runs when scanNext
// abandons a set part-way through, plus the rule that it must NOT run when
// the set was read to exhaustion. Neither half is reproducible with a fake
// driver: `executor_sink_test.go`'s fake implements the sqlexp contract but
// not TDS, so deleting the drain loop still passes it. Both are properties
// of go-mssqldb's protocol handling and only a live run can settle them.
//
//	go test -tags livedb ./internal/query/ -run TestLive -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
//
// Skipped entirely without -livedb, so `go test ./...` is unaffected.
package query

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/golang-sql/sqlexp"
	_ "github.com/microsoft/go-mssqldb"
)

var liveDSN = flag.String("livedb", "", "SQL Server DSN for the live drain-gate tests")

func liveConn(t *testing.T) (*sql.Conn, context.Context, func()) {
	t.Helper()
	if *liveDSN == "" {
		t.Skip("no -livedb DSN given")
	}
	db, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := db.Conn(ctx)
	if err != nil {
		cancel()
		db.Close()
		t.Fatalf("conn: %v", err)
	}
	return conn, ctx, func() { conn.Close(); db.Close(); cancel() }
}

// trace is what one sqlexp message loop saw, in order.
type trace struct {
	events []string
	sets   [][]string // first column of every row actually read, per set
}

func (tr *trace) add(s string) { tr.events = append(tr.events, s) }

// runLoop is runBatch reduced to what the gate affects: it reads readRows
// rows of each result set (-1 meaning all), then either drains the rest or
// doesn't, per drain. Everything else mirrors runBatch exactly.
func runLoop(ctx context.Context, t *testing.T, conn *sql.Conn, sqlText string, readRows int, drain bool, extraNext bool) *trace {
	t.Helper()
	tr := &trace{}
	retmsg := &sqlexp.ReturnMessage{}
	rows, err := conn.QueryContext(ctx, sqlText, retmsg)
	if err != nil {
		tr.add("query error: " + err.Error())
		return tr
	}
	defer rows.Close()

	for active := true; active; {
		switch m := retmsg.Message(ctx).(type) {
		case sqlexp.MsgNotice:
			tr.add("notice: " + m.Message.String())
		case sqlexp.MsgError:
			tr.add("error: " + m.Error.Error())
		case sqlexp.MsgRowsAffected:
			tr.add("rowsAffected")
		case sqlexp.MsgNext:
			var got []string
			exhausted := true
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					tr.add("scan error: " + err.Error())
					exhausted = false
					break
				}
				got = append(got, v)
				if readRows >= 0 && len(got) >= readRows {
					exhausted = false
					break
				}
			}
			tr.sets = append(tr.sets, got)
			tr.add("set of " + strings.Join(got, ","))
			if !exhausted && drain {
				for rows.Next() {
				}
			}
			if exhausted && extraNext {
				// The shipped bug this guards against: one Next() past an
				// exhausted set.
				rows.Next()
			}
		case sqlexp.MsgNextResultSet:
			active = rows.NextResultSet()
			tr.add("nextResultSet")
		}
	}
	if err := rows.Err(); err != nil {
		tr.add("rows.Err: " + err.Error())
	}
	return tr
}

// liveBatch produces, in order: a 5-row set, a PRINT, and a second 2-row
// set. Everything after the first set is what an undrained abandon could
// lose.
const liveBatch = `
SELECT CAST(v AS varchar(10)) FROM (VALUES ('a'),('b'),('c'),('d'),('e')) t(v);
PRINT 'between the sets';
SELECT CAST(v AS varchar(10)) FROM (VALUES ('x'),('y')) t(v);
`

// Does an abandoned set have to be read out before the message loop can
// advance? Runs the same batch with the drain loop and without it.
func TestLiveDrainGateAfterAnAbandonedSet(t *testing.T) {
	conn, ctx, done := liveConn(t)
	defer done()

	withDrain := runLoop(ctx, t, conn, liveBatch, 2, true, false)
	t.Logf("with drain:    %v", withDrain.events)

	noDrain := runLoop(ctx, t, conn, liveBatch, 2, false, false)
	t.Logf("without drain: %v", noDrain.events)

	// Whatever the driver turns out to do, the drained run is the shipped
	// behaviour and must deliver everything after the abandoned set.
	if len(withDrain.sets) != 2 {
		t.Errorf("with drain: got %d result sets, want 2 — output after the abandoned set was lost", len(withDrain.sets))
	}
	if !hasNotice(withDrain, "between the sets") {
		t.Error("with drain: the PRINT between the two sets never arrived")
	}
	if len(withDrain.sets) > 0 && len(withDrain.sets[0]) != 2 {
		t.Errorf("with drain: read %d rows of the first set, want 2 — test premise is wrong", len(withDrain.sets[0]))
	}
	// The interesting half: report what dropping the drain actually costs,
	// as an assertion so the answer is recorded rather than eyeballed.
	if len(noDrain.sets) == 2 && hasNotice(noDrain, "between the sets") {
		t.Log("RESULT: the drain loop is NOT load-bearing on this server/driver — " +
			"go-mssqldb advanced past the abandoned set on its own. Keep it anyway " +
			"(it is what makes the behaviour independent of that), but a future " +
			"review may stop treating its removal as a live-only risk.")
	} else {
		t.Logf("RESULT: the drain loop IS load-bearing — without it: %d sets, events %v",
			len(noDrain.sets), noDrain.events)
	}
}

// The other half of the gate: an extra Next() past an exhausted set makes
// the driver swallow the message retmsg is waiting for. This is the shipped
// bug (empty grid, no error, no Messages tab) that CLAUDE.md forbids
// reintroducing — pinned here against the real driver.
func TestLiveExtraNextPastAnExhaustedSetSwallowsTheMessage(t *testing.T) {
	conn, ctx, done := liveConn(t)
	defer done()

	clean := runLoop(ctx, t, conn, liveBatch, -1, false, false)
	t.Logf("no extra Next(): %v", clean.events)
	if len(clean.sets) != 2 || !hasNotice(clean, "between the sets") {
		t.Fatalf("the control run already lost output: %v — test premise is wrong", clean.events)
	}

	greedy := runLoop(ctx, t, conn, liveBatch, -1, false, true)
	t.Logf("extra Next():    %v", greedy.events)

	if len(greedy.sets) == len(clean.sets) && len(greedy.events) == len(clean.events) {
		t.Log("RESULT: an extra Next() past an exhausted set cost nothing on this " +
			"server/driver. The prohibition stands regardless — it cost a shipped " +
			"empty grid once — but it is not currently reproducible here.")
	} else {
		t.Logf("RESULT: confirmed — an extra Next() swallowed output. clean=%d sets, greedy=%d sets",
			len(clean.sets), len(greedy.sets))
	}
}

func hasNotice(tr *trace, want string) bool {
	for _, e := range tr.events {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}

// liveEndFailSink accepts every row of every set and then fails to close the
// set out — what csvSink does when the flush at the end of a set hits a full
// disk or a network share that has gone away.
type liveEndFailSink struct {
	sets int
	rows int
}

func (s *liveEndFailSink) BeginSet([]string) error { s.sets++; return nil }
func (s *liveEndFailSink) Row([]string) error      { s.rows++; return nil }
func (s *liveEndFailSink) EndSet(int) error        { return errors.New("end failed") }

// The export bug the drain gate hides: a sink that fails only at EndSet has
// nonetheless read its set to the end, so there is nothing to drain — and
// draining it anyway spends the extra Next() that eats the rest of the batch.
// scanNext used to infer "rows still pending" from "returned an error", so
// every set after the first vanished on the one run the user most needs the
// output of.
//
// Runs the real runBatch over the real driver, which is the only place the
// swallowing happens: the fake driver in stream_test.go implements the sqlexp
// contract but not TDS, so the unit tests in executor_drain_test.go pin what
// scanNext decides and this pins what that decision costs.
func TestLiveExportThatFailsAtEndSetKeepsTheRestOfTheBatch(t *testing.T) {
	conn, ctx, done := liveConn(t)
	defer done()

	var res Result
	sink := &liveEndFailSink{}
	runBatch(ctx, conn, liveBatch, &res, sink)

	t.Logf("sets begun=%d rows=%d RowsWritten=%d messages=%d",
		sink.sets, sink.rows, res.RowsWritten, len(res.Messages))
	for _, m := range res.Messages {
		t.Logf("  message: %s", m.Text)
	}

	// Both sets must have been offered to the sink. Before the fix the second
	// never was: the drain after the first set's EndSet failure swallowed
	// everything that followed it.
	if sink.sets != 2 {
		t.Errorf("the sink was offered %d result sets, want 2 — the batch's later "+
			"output was lost to the drain after the first EndSet failure", sink.sets)
	}
	if sink.rows != 7 {
		t.Errorf("the sink was handed %d rows, want 7 (5 + 2)", sink.rows)
	}
	// The PRINT between the sets is the other casualty, and the one a user
	// would notice as "no messages at all".
	found := false
	for _, m := range res.Messages {
		if strings.Contains(m.Text, "between the sets") {
			found = true
		}
	}
	if !found {
		t.Error("the PRINT between the two sets never reached Messages")
	}
}
