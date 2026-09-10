package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// openFakeSession opens a Session over a one-connection fake pool serving
// batches in order, and returns the pool's connector so a test can see what
// the pool did with the connection.
func openFakeSession(t *testing.T, batches ...[]fakeMsg) (*Session, *fakeMsgConnector, *sql.DB) {
	t.Helper()
	fc := &fakeMsgConnector{conn: &fakeMsgConn{batches: batches, dbName: "testdb", spid: 57}}
	db := sql.OpenDB(fc)
	t.Cleanup(func() { db.Close() })
	s, st, err := Open(context.Background(), db, "testdb")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if st.Database != "testdb" || s.SPID() != 57 {
		t.Fatalf("Open state = %+v, SPID %d; want testdb, 57", st, s.SPID())
	}
	return s, fc, db
}

func oneRow(v string) []fakeMsg {
	return []fakeMsg{set([]string{"v"}, []driver.Value{v})}
}

// BUG-1: the whole reason Session exists. Two Executes on it reach the same
// physical connection and database/sql never resets it in between — the
// reset is what dropped temp tables, SET options and open transactions.
func TestSessionRunsEveryExecuteOnOneUnresetConnection(t *testing.T) {
	s, fc, _ := openFakeSession(t, oneRow("first"), oneRow("second"))
	defer s.Close()

	for _, want := range []string{"first", "second"} {
		res := s.Execute(context.Background(), "SELECT 1")
		if res.HasErrors() || len(res.Sets) != 1 || res.Sets[0].Rows[0][0] != want {
			t.Fatalf("Execute = %+v / %v, want one set holding %q", res.Sets, messageTexts(res), want)
		}
	}
	if fc.connects != 1 {
		t.Errorf("dialled %d connections, want 1", fc.connects)
	}
	if fc.conn.resets != 0 {
		t.Errorf("connection was reset %d time(s) between Executes, want 0", fc.conn.resets)
	}
}

// The control for the test above: the package-level Execute, on the same
// fake, does get its connection reset — so a zero count there means something.
func TestPooledExecuteResetsTheConnectionBetweenCalls(t *testing.T) {
	db := openFakeMsgDB(oneRow("first"), oneRow("second"))
	defer db.Close()
	Execute(context.Background(), db, "", "SELECT 1")
	Execute(context.Background(), db, "", "SELECT 1")
	// openFakeMsgDB keeps its connector to itself; the one connection is
	// reached through the pool instead.
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var resets int
	conn.Raw(func(dc any) error { resets = dc.(*fakeMsgConn).resets; return nil })
	if resets == 0 {
		t.Error("pooled Executes never reset the connection — the fake cannot tell the Session test's 0 apart")
	}
}

// A Session's database is wherever its last run left it, with no USE of its
// own, and its transaction count comes back after every run.
func TestSessionReportsStateAfterEachRun(t *testing.T) {
	s, fc, _ := openFakeSession(t, oneRow("x"))
	defer s.Close()
	fc.conn.dbName, fc.conn.tranCount = "otherdb", 2

	res := s.Execute(context.Background(), "USE otherdb")
	if res.State == nil || res.State.TranCount != 2 || res.State.Database != "otherdb" || res.Database != "otherdb" {
		t.Fatalf("State = %+v, Database = %q; want otherdb with 2 open transactions", res.State, res.Database)
	}
	for _, q := range fc.conn.execs {
		if strings.HasPrefix(q, "USE ") && q != "USE [testdb]" {
			t.Errorf("Session issued %q of its own; only Open may switch database", q)
		}
	}
}

// A cancelled run still reports the state it left: cancelling the batch
// after its BEGIN TRAN leaves the transaction open, and the close prompt
// depends on knowing that.
func TestSessionReadsStateAfterACancelledRun(t *testing.T) {
	s, fc, _ := openFakeSession(t, oneRow("x"))
	defer s.Close()
	fc.conn.tranCount = 1
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := s.Execute(ctx, "BEGIN TRAN")
	if res.State == nil || res.State.TranCount != 1 {
		t.Fatalf("State = %+v after a cancelled run, want TranCount 1", res.State)
	}
	if !hasMessage(res, cancelledMessage.Text) {
		t.Errorf("messages = %v, want the cancellation reported", messageTexts(res))
	}
}

// A connection that breaks during a run takes the session with it: the run
// says so, the connection is discarded rather than pooled, and the next run
// fails at once instead of landing on some other session.
func TestSessionLostWhenItsConnectionBreaks(t *testing.T) {
	s, fc, _ := openFakeSession(t, []fakeMsg{{kind: msgBreak}}, oneRow("never"))
	defer s.Close()

	res := s.Execute(context.Background(), "SELECT 1")
	if !res.SessionLost || res.State != nil {
		t.Fatalf("SessionLost = %v, State = %+v; want lost with no state", res.SessionLost, res.State)
	}
	if !fc.conn.closed {
		t.Error("the broken connection was not discarded")
	}
	next := s.Execute(context.Background(), "SELECT 1")
	if !next.SessionLost || !errorsContain(next, ErrSessionLost.Error()) {
		t.Errorf("next Execute = %v, want ErrSessionLost without running", messageTexts(next))
	}
	if fc.conn.next != 1 {
		t.Errorf("%d batches reached the driver, want only the first", fc.conn.next)
	}
}

// A SHOWPLAN_XML that cannot be switched off would turn every later run into
// a plan fetch that runs nothing, and nothing resets a Session's connection
// the way the pool resets a returned one — so the session is given up.
func TestSessionLostWhenPlanCaptureCannotBeSwitchedOff(t *testing.T) {
	s, fc, _ := openFakeSession(t, []fakeMsg{})
	defer s.Close()
	fc.conn.failExec = "SHOWPLAN_XML OFF"

	res := s.ExecuteEstimatedPlan(context.Background(), "SELECT 1")
	if !res.SessionLost {
		t.Fatal("SessionLost = false after SET SHOWPLAN_XML OFF failed")
	}
	if !errorsContain(res, "disable estimated execution plan capture") {
		t.Errorf("messages = %v, want the failed capture-off reported", messageTexts(res))
	}
}

// Close hands the connection back as broken, so database/sql closes it and
// the server ends the session — rolling back its open transaction. Returned
// to the pool instead it would sit idle holding that transaction's locks.
func TestSessionCloseDiscardsTheConnection(t *testing.T) {
	s, fc, db := openFakeSession(t)
	s.Close()
	s.Close() // idempotent

	if !fc.conn.closed {
		t.Error("Close returned the connection to the pool instead of closing it")
	}
	if idle := db.Stats().Idle; idle != 0 {
		t.Errorf("pool holds %d idle connection(s) after Close, want 0", idle)
	}
}

func TestSessionEndTransactions(t *testing.T) {
	for _, c := range []struct {
		commit bool
		want   string
	}{
		{true, "COMMIT TRANSACTION"},
		{false, "ROLLBACK TRANSACTION"},
	} {
		s, fc, _ := openFakeSession(t)
		if err := s.EndTransactions(context.Background(), c.commit); err != nil {
			t.Fatalf("EndTransactions(%v): %v", c.commit, err)
		}
		last := fc.conn.execs[len(fc.conn.execs)-1]
		if !strings.Contains(last, c.want) {
			t.Errorf("EndTransactions(%v) sent %q, want it to %s", c.commit, last, c.want)
		}
		s.Close()
		if err := s.EndTransactions(context.Background(), c.commit); !errors.Is(err, ErrSessionLost) {
			t.Errorf("EndTransactions on a closed session = %v, want ErrSessionLost", err)
		}
	}
}

func errorsContain(res *Result, text string) bool {
	for _, m := range res.Messages {
		if m.IsError && strings.Contains(m.Text, text) {
			return true
		}
	}
	return false
}
