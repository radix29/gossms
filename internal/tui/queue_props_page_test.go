package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Queue Properties is one of the two writable pages in the Service Broker
// tree, and its ACTIVATION block is the part with a shape to get wrong: the
// server refuses a partial one, so the four activation rows move together and
// are restated in full whenever any of them changed.

// loadQueuePage opens Queue Properties on one row of the queue fixture. row
// is that row's index in queueResp, since the by-name read has to be answered
// with the one queue the page asked for — see brokerRowByArg.
func loadQueuePage(t *testing.T, name string, row int) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, brokerDBResp(), brokerRowByArg(queueResp(), name, row))
	pages := brokerQueuePropPages(sc, brokerDB, "dbo", name)
	if len(pages) != 1 {
		t.Fatalf("want one page, got %d", len(pages))
	}
	form, apply := loadPage(t, pages[0], inst)
	return inst, apply, form
}

// TestQueuePageSendsOnlyTheSettingThatChanged. A nil field in QueueSettings
// means "leave this alone", and a page restating everything it read would
// rewrite an activation block the user never touched — over any change another
// session made between the load and the apply.
func TestQueuePageSendsOnlyTheSettingThatChanged(t *testing.T) {
	inst, apply, form := loadQueuePage(t, "ClaimQueue", 0)

	editCheck(t, form, "Queue enabled", true)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := oneBrokerStatement(t, inst)
	if !strings.Contains(stmt, "STATUS = ON") {
		t.Errorf("wrote:\n%s\nwant it to set STATUS = ON", stmt)
	}
	for _, unwanted := range []string{"ACTIVATION", "RETENTION", "POISON"} {
		if strings.Contains(stmt, unwanted) {
			t.Errorf("wrote:\n%s\nwant no %s clause — nothing on that row changed", stmt, unwanted)
		}
	}
}

// TestQueuePageRestatesTheWholeActivationBlock. Changing one activation field
// must send all four: the server refuses an ACTIVATION block missing
// PROCEDURE_NAME, MAX_QUEUE_READERS or EXECUTE AS ("the activation user is
// not specified").
func TestQueuePageRestatesTheWholeActivationBlock(t *testing.T) {
	inst, apply, form := loadQueuePage(t, "ClaimQueue", 0)

	editText(t, form, "Max queue readers", "8")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := oneBrokerStatement(t, inst)
	for _, want := range []string{"ACTIVATION (", "STATUS = OFF", "PROCEDURE_NAME = [dbo].[ProcessClaims]",
		"MAX_QUEUE_READERS = 8", "EXECUTE AS OWNER"} {
		if !strings.Contains(stmt, want) {
			t.Errorf("wrote:\n%s\nwant it to contain: %s", stmt, want)
		}
	}
}

// TestQueuePageDropsTheActivationBlockWhenTheProcedureIsCleared. Emptying the
// procedure is the only way this page removes an activation, and ACTIVATION
// (DROP) is the statement for it — a restated block with an empty
// PROCEDURE_NAME does not parse.
func TestQueuePageDropsTheActivationBlockWhenTheProcedureIsCleared(t *testing.T) {
	inst, apply, form := loadQueuePage(t, "ClaimQueue", 0)

	editText(t, form, "Activation procedure", "")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := oneBrokerStatement(t, inst)
	if !strings.Contains(stmt, "ACTIVATION (DROP)") {
		t.Errorf("wrote:\n%s\nwant ACTIVATION (DROP)", stmt)
	}
}

// TestQueuePageRefusesAnEmptyProcedureOnAQueueWithNoActivation. There is
// nothing to drop and nothing to restate, and ACTIVATION (DROP) on such a
// queue would be a statement sent for an edit that means nothing.
func TestQueuePageRefusesAnEmptyProcedureOnAQueueWithNoActivation(t *testing.T) {
	inst, apply, form := loadQueuePage(t, "IdleQueue", 2)

	editCheck(t, form, "Activation enabled", true)

	err := apply(context.Background())
	if err == nil {
		t.Fatal("apply succeeded with no activation procedure named")
	}
	if !strings.Contains(err.Error(), "activation procedure") {
		t.Errorf("error %q does not say what is missing", err)
	}
	if stmts := inst.StatementsIn(brokerDB); len(stmts) != 0 {
		t.Errorf("wrote %d statements for a refused edit:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
}

// TestQueuePageWritesNothingWhenNothingChanged. ALTER QUEUE with no clause
// does not parse, so a page that built one from a clean form would fail on
// OK rather than close.
func TestQueuePageWritesNothingWhenNothingChanged(t *testing.T) {
	inst, apply, _ := loadQueuePage(t, "ClaimQueue", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(brokerDB); len(stmts) != 0 {
		t.Errorf("wrote %d statements for an untouched page:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
}

// oneBrokerStatement is the single write a Service Broker page's apply made.
func oneBrokerStatement(t *testing.T, inst *fakeInstance) string {
	t.Helper()
	stmts := inst.StatementsIn(brokerDB)
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	return stmts[0]
}
