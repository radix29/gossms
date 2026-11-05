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
	return loadSequenceGeneralPageOf(t, "", []driver.Value{
		"OrderNo", principalSchema, int64(901), "int", "sys", int64(10), int64(0),
		"1", "1", "1", "1000", false, true, int64(20), "42", "42", false,
	})
}

// loadSequenceGeneralPageOf loads General for the sys.sequences row given, on
// an instance at version ("" for the fake's default, a current one).
func loadSequenceGeneralPageOf(t *testing.T, version string, row []driver.Value) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	responses := []fakeResponse{
		dbByNameResp(principalDatabase, 5),
		{match: "FROM   sys.sequences s", arg: row[0].(string), cols: 17, rows: [][]driver.Value{row}},
	}
	sc, inst := newFakeConn(t, responses...)
	if version != "" {
		sc, inst = newFakeConnAtVersion(t, version, responses...)
	}
	form, apply := loadPage(t, pageSequenceGeneral(sc, principalDatabase, principalSchema, row[0].(string)), inst)
	return inst, apply, form
}

// wideSequenceRow is a decimal(38, 0) sequence whose values lie past bigint,
// with lastUsed as last_used_value (nil for NULL).
func wideSequenceRow(lastUsed driver.Value) []driver.Value {
	return []driver.Value{
		"Wide", principalSchema, int64(902), "decimal", "sys", int64(38), int64(0),
		"10000000000000000000", "1", "-99999999999999999999999999999999999999", "99999999999999999999999999999999999999",
		false, false, int64(0), "10000000000000000000", lastUsed, false,
	}
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

// Last used value tells a sequence that has handed out nothing apart from one
// that has (Current value reads the same for both), and says so before 2017,
// which has no last_used_value.
func TestSequenceGeneralLastUsedValue(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		lastUsed      driver.Value
		want          string
	}{
		{"handed out", "", "10000000000000000000", "10000000000000000000"},
		{"none yet", "", nil, "(none since created or restarted)"},
		{"before 2017", "13.0.6500.1", nil, "(not tracked before SQL Server 2017)"},
	} {
		_, _, form := loadSequenceGeneralPageOf(t, tc.version, wideSequenceRow(tc.lastUsed))
		if got := staticValue(t, form, "Last used value"); got != tc.want {
			t.Errorf("%s: Last used value = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A decimal(38, 0) sequence's values past bigint are written back as typed,
// and its range is the type's: 38 nines, not bigint's, bounds a row.
func TestSequenceGeneralWritesPastBigint(t *testing.T) {
	inst, apply, form := loadSequenceGeneralPageOf(t, "", wideSequenceRow(nil))
	editText(t, form, "Restart with", "-10000000000000000000")
	editText(t, form, "Maximum value", "10000000000000000000000000000000000000")
	if err := form.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "ALTER SEQUENCE [sales].[Wide] RESTART WITH -10000000000000000000 MAXVALUE 10000000000000000000000000000000000000"
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 1 || !strings.Contains(stmts[0], want) {
		t.Errorf("statements:\n%s\nwant one containing: %s", strings.Join(stmts, "\n"), want)
	}

	_, _, form = loadSequenceGeneralPageOf(t, "", wideSequenceRow(nil))
	editText(t, form, "Maximum value", "100000000000000000000000000000000000000") // 10^38
	if err := form.Validate(); err == nil || !strings.Contains(err.Error(),
		"maximum value must be a whole number from -99999999999999999999999999999999999999 to 99999999999999999999999999999999999999") {
		t.Errorf("Validate with a maximum past decimal(38, 0): %v", err)
	}
}

// An alias-typed sequence has no range in the dialog (its base type is not
// read): any whole number passes to the server, anything else is refused.
func TestSequenceGeneralAliasTypeTakesAnyWholeNumber(t *testing.T) {
	row := wideSequenceRow(nil)
	row[3], row[4] = "OrderNumber", "app"
	_, _, form := loadSequenceGeneralPageOf(t, "", row)
	editText(t, form, "Increment by", "100000000000000000000000000000000000000000")
	if err := form.Validate(); err != nil {
		t.Errorf("Validate: %v, want the server to judge an alias type's range", err)
	}
	editText(t, form, "Increment by", "1.5")
	if err := form.Validate(); err == nil || !strings.Contains(err.Error(), "increment by must be a whole number") {
		t.Errorf("Validate with a fraction: %v", err)
	}
}
