package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// loadSequenceGeneralPage loads General for [sales].[OrderNo], an int sequence
// at 42 stepping by 1 between 1 and 1000, not cycling, with a cache of 20.
func loadSequenceGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t,
		dbByNameResp(principalDatabase, 5),
		fakeResponse{match: "FROM   sys.sequences s", arg: "OrderNo", cols: 17, rows: [][]driver.Value{{
			"OrderNo", principalSchema, int64(901), "int", "sys", int64(10), int64(0),
			"1", "1", "1", "1000", false, true, int64(20), "42", "42", false,
		}}},
	)
	form, apply := loadPage(t, pageSequenceGeneral(sc, principalDatabase, principalSchema, "OrderNo"), inst)
	return inst, apply, form
}

// An untouched page writes nothing; the edited rows, and only those, become one
// ALTER SEQUENCE in the statement's clause order.
func TestSequenceGeneralAltersOnlyWhatChanged(t *testing.T) {
	inst, apply, form := loadSequenceGeneralPage(t)
	if got := staticValue(t, form, "Current value"); got != "42" {
		t.Errorf("Current value = %q, want 42", got)
	}
	if got := selectRow(t, form, "Cache").Value(); got != nseqCacheSize {
		t.Errorf("Cache = %q for a cache of 20, want %q", got, nseqCacheSize)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("untouched apply: %v", err)
	}
	assertNoStatementsIn(t, inst, principalDatabase)

	inst, apply, form = loadSequenceGeneralPage(t)
	editText(t, form, "Maximum value", "5000")
	editText(t, form, "Restart with", "100")
	editCheck(t, form, "Cycle", true)
	editText(t, form, "Cache size", "64")
	if err := form.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn(principalDatabase)
	want := "ALTER SEQUENCE [sales].[OrderNo] RESTART WITH 100 MAXVALUE 5000 CYCLE CACHE 64"
	if len(stmts) != 1 || !strings.Contains(stmts[0], want) {
		t.Errorf("statements:\n%s\nwant one containing: %s", strings.Join(stmts, "\n"), want)
	}
}

// Switching the cache off and back to the server's default each name the clause
// on their own; a value outside bigint is refused by Validate before any apply.
func TestSequenceGeneralCacheFormsAndRefusals(t *testing.T) {
	for item, want := range map[string]string{
		nseqCacheNone:    "ALTER SEQUENCE [sales].[OrderNo] NO CACHE",
		nseqCacheDefault: "ALTER SEQUENCE [sales].[OrderNo] CACHE",
	} {
		inst, apply, form := loadSequenceGeneralPage(t)
		editSelect(t, form, "Cache", item)
		if err := apply(t.Context()); err != nil {
			t.Fatalf("%s: apply: %v", item, err)
		}
		if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 1 || !strings.HasSuffix(strings.TrimSpace(stmts[0]), want) {
			t.Errorf("%s: statements %q, want one ending %q", item, stmts, want)
		}
	}

	_, _, form := loadSequenceGeneralPage(t)
	editText(t, form, "Increment by", "99999999999999999999")
	if err := form.Validate(); err == nil || !strings.Contains(err.Error(), "increment by must be a whole number") {
		t.Errorf("Validate with an increment past bigint: %v", err)
	}
}
