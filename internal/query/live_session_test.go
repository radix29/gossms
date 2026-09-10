//go:build livedb

// Live verification of what a Session exists for (BUG-1): state left by one
// Execute is still there for the next, where the pooled Execute resets it.
// The reset is the server's — the TDS reset-connection bit — so the scripted
// driver can only show that no reset was asked for, never what one does.
//
//	go test -tags livedb ./internal/query/ -run TestLiveSession -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
package query

import (
	"database/sql"
	"strconv"
	"testing"
	"time"
)

func TestLiveSessionKeepsStateAcrossExecutes(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()

	s, st, err := Open(ctx, db, "tempdb")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if st.Database != "tempdb" || st.TranCount != 0 || s.SPID() == 0 {
		t.Fatalf("Open state = %+v, SPID %d", st, s.SPID())
	}

	setup := s.Execute(ctx, "CREATE TABLE #t (i int); INSERT #t VALUES (1);\n"+
		"SET LOCK_TIMEOUT 1234; USE master; BEGIN TRAN;")
	if setup.HasErrors() {
		t.Fatalf("setup: %v", messageTexts(setup))
	}
	if setup.State == nil || setup.State.TranCount != 1 || setup.Database != "master" {
		t.Fatalf("state after setup = %+v / %q, want master with 1 open transaction", setup.State, setup.Database)
	}

	res := s.Execute(ctx, "SELECT @@SPID, (SELECT COUNT(*) FROM #t), @@LOCK_TIMEOUT, @@TRANCOUNT, DB_NAME()")
	if res.HasErrors() || len(res.Sets) != 1 {
		t.Fatalf("second Execute: %v", messageTexts(res))
	}
	got := res.Sets[0].Rows[0]
	want := []string{strconv.Itoa(s.SPID()), "1", "1234", "1", "master"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d = %q, want %q (row %v) — session state did not survive", i, got[i], want[i], got)
		}
	}

	if err := s.EndTransactions(ctx, false); err != nil {
		t.Fatalf("EndTransactions: %v", err)
	}
	after := s.Execute(ctx, "SELECT 1")
	if after.State == nil || after.State.TranCount != 0 {
		t.Errorf("TranCount after rollback = %+v, want 0", after.State)
	}
}

// The control: two package-level Executes on the same pool lose the temp
// table, because the pool resets the connection in between.
func TestLiveSessionPooledExecuteLosesState(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	db.SetMaxOpenConns(1) // the same physical session both times

	if res := Execute(ctx, db, "tempdb", "CREATE TABLE #t (i int)"); res.HasErrors() {
		t.Fatalf("create: %v", messageTexts(res))
	}
	res := Execute(ctx, db, "tempdb", "SELECT * FROM #t")
	if !res.HasErrors() {
		t.Error("the pooled Execute kept #t — the reset this package works around is not happening")
	}
}

// A Session dropped with a transaction open does not linger holding it:
// Close discards the connection, and the server ends the session.
func TestLiveSessionCloseEndsTheServerSession(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()

	s, _, err := Open(ctx, db, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	spid := s.SPID()
	if res := s.Execute(ctx, "BEGIN TRAN"); res.HasErrors() {
		t.Fatalf("BEGIN TRAN: %v", messageTexts(res))
	}
	s.Close()

	// Asked through a pool of its own: on db the check would be handed the
	// very connection a pooling Close had returned, and its reset would roll
	// the transaction back — passing the test it exists to fail. The server
	// notices the closed socket on its own schedule; a second is ample.
	check, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var n int
	for range 20 {
		if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.dm_exec_sessions WHERE session_id = @p1 AND open_transaction_count > 0", spid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("SPID %d still holds an open transaction after Close", spid)
}
