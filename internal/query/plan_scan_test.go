package query

import (
	"database/sql/driver"
	"testing"
)

// SET SHOWPLAN_XML ON returns a multi-statement batch's plans as consecutive
// rows of one result set. scanNext used to keep only the last, so an
// estimated plan for a three-statement batch showed one statement's plan
// standing in for the whole batch.
func TestScanNextKeepsEveryShowplanRow(t *testing.T) {
	db := openFakeRowsDB([]string{showplanColumnName}, [][]driver.Value{
		{"<plan1/>"}, {"<plan2/>"}, {"<plan3/>"},
	})
	defer db.Close()
	rows := queryFakeRows(t, db)
	defer rows.Close()

	var res Result
	if !scanNext(rows, &res, nil) {
		t.Fatalf("scanNext reported the set abandoned: %+v", res.Messages)
	}
	want := []string{"<plan1/>", "<plan2/>", "<plan3/>"}
	if len(res.PlanXML) != len(want) {
		t.Fatalf("PlanXML = %v, want %v", res.PlanXML, want)
	}
	for i := range want {
		if res.PlanXML[i] != want[i] {
			t.Errorf("PlanXML[%d] = %q, want %q", i, res.PlanXML[i], want[i])
		}
	}
	if len(res.Sets) != 0 {
		t.Errorf("a showplan set was also scanned as a grid: %d sets", len(res.Sets))
	}
}

// A single-column set that is not the showplan column is a real result set
// and must reach Sets, not PlanXML.
func TestScanNextTreatsANonShowplanColumnAsAGrid(t *testing.T) {
	db := openFakeRowsDB([]string{"name"}, [][]driver.Value{{"not a plan"}})
	defer db.Close()
	rows := queryFakeRows(t, db)
	defer rows.Close()

	var res Result
	if !scanNext(rows, &res, nil) {
		t.Fatalf("scanNext reported the set abandoned: %+v", res.Messages)
	}
	if len(res.PlanXML) != 0 {
		t.Errorf("PlanXML = %v, want empty", res.PlanXML)
	}
	if len(res.Sets) != 1 {
		t.Fatalf("Sets = %d, want 1", len(res.Sets))
	}
}
