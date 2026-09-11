package query

import (
	"database/sql/driver"
	"errors"
	"testing"
)

// runBatch drains a result set scanNext abandoned part-way, and must not drain
// one read to its end: an extra Next() past an exhausted set makes the driver
// swallow the message retmsg awaits, and the set never reaches Result (empty
// grid, no error, no Messages).
//
// "Failed" and "rows pending" are different questions. Two callees can fail on
// a fully read set: streamResultSet's deferred EndSet (a Results To File export
// whose last write or Close fails) and scanPlanXML's trailing rows.Err().
// Neither may drain.
//
// These pin exhausted and scanNext's use of it. Whether the drain matters on
// the wire is go-mssqldb's protocol behaviour, covered by live_drain_test.go.

// endFailSink accepts every row and then fails to close the set out.
type endFailSink struct {
	recordingSink
}

func (s *endFailSink) EndSet(n int) error {
	s.recordingSink.EndSet(n)
	return errors.New("end failed")
}

func TestStreamResultSetIsExhaustedWhenOnlyEndSetFailed(t *testing.T) {
	db := openFakeRowsDB(streamTestCols, streamTestRows())
	defer db.Close()

	r := queryFakeRows(t, db)
	sink := &endFailSink{}
	n, exhausted, err := streamResultSet(r, sink, nil)
	r.Close()

	if err == nil {
		t.Fatal("streamResultSet returned nil error after EndSet failed")
	}
	if n != 3 {
		t.Errorf("n = %d, want 3 — every row was written before EndSet failed", n)
	}
	if !exhausted {
		t.Error("exhausted = false on a set whose row loop ran to the end — " +
			"the caller will drain it, which is the forbidden extra Next()")
	}
}

// A genuine mid-set abandon must still report rows pending.
func TestStreamResultSetIsNotExhaustedWhenASinkRowFailed(t *testing.T) {
	db := openFakeRowsDB(streamTestCols, streamTestRows())
	defer db.Close()

	r := queryFakeRows(t, db)
	sink := &recordingSink{failOn: 2}
	n, exhausted, err := streamResultSet(r, sink, nil)
	r.Close()

	if err == nil {
		t.Fatal("streamResultSet returned nil error after the sink failed")
	}
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
	if exhausted {
		t.Error("exhausted = true after abandoning the set at row 2 — the rows " +
			"left pending will never be drained and the message loop stalls on them")
	}
}

// An export failing only at EndSet has nothing left to drain.
func TestScanNextDoesNotAbandonASetOnlyEndSetFailed(t *testing.T) {
	db := openFakeRowsDB(streamTestCols, streamTestRows())
	defer db.Close()

	rows := queryFakeRows(t, db)
	defer rows.Close()

	var res Result
	sink := &endFailSink{}
	abandoned := scanNext(rows, &res, sink)

	if abandoned {
		t.Error("scanNext reported the set abandoned because EndSet failed — " +
			"the rows were all read, and draining spends the extra Next()")
	}
	if len(res.Messages) == 0 {
		t.Error("the EndSet failure was not recorded on the Result")
	}
	if res.RowsWritten != 3 {
		t.Errorf("RowsWritten = %d, want 3", res.RowsWritten)
	}
}

func TestScanPlanXMLIsExhaustedAfterTheLastRow(t *testing.T) {
	db := openFakeRowsDB([]string{showplanColumnName}, [][]driver.Value{
		{"<plan1/>"}, {"<plan2/>"},
	})
	defer db.Close()

	rows := queryFakeRows(t, db)
	defer rows.Close()

	plans, exhausted, err := scanPlanXML(rows)
	if err != nil {
		t.Fatalf("scanPlanXML: %v", err)
	}
	if !exhausted {
		t.Error("exhausted = false after reading every row of the plan set")
	}
	if len(plans) != 2 {
		t.Errorf("plans = %v, want 2", plans)
	}
}

// A NULL plan row can't scan into a string and leaves rows pending: a real
// abandon, which the drain is for.
func TestScanNextAbandonsAPlanSetThatFailsMidScan(t *testing.T) {
	db := openFakeRowsDB([]string{showplanColumnName}, [][]driver.Value{
		{"<plan1/>"}, {nil}, {"<plan3/>"},
	})
	defer db.Close()

	rows := queryFakeRows(t, db)
	defer rows.Close()

	var res Result
	if !scanNext(rows, &res, nil) {
		t.Fatal("scanNext read a plan set to the end despite a row it could not scan")
	}
	if len(res.Messages) == 0 {
		t.Error("the scan failure was not recorded on the Result")
	}
}
