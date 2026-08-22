package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Key Properties is Index Properties' pages pointed at the index behind a
// PRIMARY KEY or UNIQUE constraint, and its two differences are what these
// pin. The General page can rename the constraint, which changes the identity
// every sibling page resolves its object by — the boxed name has to move with
// it, or the next page's lookup fails on a name the server no longer has. The
// Options page drops Ignore duplicate keys entirely, because SQL Server
// rejects IGNORE_DUP_KEY on a constraint-backing index even when the value is
// unchanged: send it and the user's lock-option edit fails with it.

// keyIndex is the constraint-backing index the tests below load — unique,
// clustered, is_primary_key, which is what makes IGNORE_DUP_KEY illegal on it.
func keyIndex() indexFixture {
	idx := plainIndex()
	idx.typeDesc = "CLUSTERED"
	idx.unique = true
	idx.primaryKey = true
	return idx
}

// loadKeyPage loads one Key Properties page over the PK_Orders constraint, and
// hands back the boxed name a rename has to update.
func loadKeyPage(t *testing.T, build func(sc *db.ServerConn, name *string) propPage) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	sc, inst := newFakeConn(t,
		dbByNameResp(idxDatabase, 5),
		idxTableResp(),
		idxListResp("PK_Orders", 1, keyIndex()),
		idxColumnsResp([]driver.Value{int64(1), "OrderID", false, false}),
	)
	name := "PK_Orders"
	form, apply := loadPage(t, build(sc, &name), inst)
	return inst, apply, form, &name
}

func keyGeneralPage(sc *db.ServerConn, name *string) propPage {
	return pageKeyGeneral(sc, idxDatabase, idxSchema, idxTable, name)
}

func keyOptionsPage(sc *db.ServerConn, name *string) propPage {
	return pageKeyOptions(sc, idxDatabase, idxSchema, idxTable, name)
}

// TestKeyRenameNamesTheConstraintAndItsNewName. sp_rename takes both names as
// parameters, so the statement text alone proves nothing: a page that passed
// them in the wrong order renames some other object to the constraint's name,
// and one that dropped the schema/table qualification renames whatever object
// of that name the default schema has.
func TestKeyRenameNamesTheConstraintAndItsNewName(t *testing.T) {
	inst, apply, form, name := loadKeyPage(t, keyGeneralPage)

	editText(t, form, "Key name", "PK_Orders_OrderID")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "sp_rename")
	assertArgs(t, inst, "sp_rename", "[sales].[Orders].[PK_Orders]", "PK_Orders_OrderID")

	// The boxed name is what every sibling page — Options, Storage,
	// Fragmentation, Extended Properties — looks the object up by on its next
	// load. Left at the old name, each of them fails with "not found" for the
	// life of the dialog.
	if *name != "PK_Orders_OrderID" {
		t.Errorf("the shared name is still %q after the rename", *name)
	}
}

// TestKeyGeneralWritesNothingWhenUntouched. The name row is populated from the
// server, so a page that mistook loaded for edited would sp_rename an object
// to the name it already has on every Apply.
func TestKeyGeneralWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _, _ := loadKeyPage(t, keyGeneralPage)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, idxDatabase)
}

// TestKeyOptionsHasNoIgnoreDuplicateKeysRow is the whole reason this page
// exists rather than reusing pageIndexOptions: the row must not be there to
// edit, and the write must not carry the option.
func TestKeyOptionsHasNoIgnoreDuplicateKeysRow(t *testing.T) {
	inst, apply, form, _ := loadKeyPage(t, keyOptionsPage)

	for _, r := range form.Rows() {
		if cr, ok := r.(*propsheet.CheckRow); ok && strings.Contains(cr.Label(), "Ignore duplicate") {
			t.Fatal("Key Options offers Ignore duplicate keys, which SQL Server rejects on a constraint-backing index")
		}
	}

	editCheck(t, form, "Allow page locks", false)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "ALLOW_PAGE_LOCKS = OFF")
	if stmt := inst.StatementsIn(idxDatabase)[0]; strings.Contains(stmt, "IGNORE_DUP_KEY") {
		t.Errorf("wrote IGNORE_DUP_KEY on a constraint-backing index:\n%s", stmt)
	}
}

// TestKeyOptionsFillFactorRebuilds. Same rebuild rule as an index — and the
// rebuild here is of the index enforcing the constraint, so getting it wrong
// is not a cosmetic setting.
func TestKeyOptionsFillFactorRebuilds(t *testing.T) {
	inst, apply, form, _ := loadKeyPage(t, keyOptionsPage)

	editText(t, form, "Fill factor", "90")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, idxDatabase, "REBUILD WITH")
	stmt := inst.StatementsIn(idxDatabase)[0]
	if !strings.Contains(stmt, "FILLFACTOR = 90") {
		t.Errorf("wrote:\n%s\nwant FILLFACTOR = 90", stmt)
	}
	if !strings.Contains(stmt, "[PK_Orders]") {
		t.Errorf("wrote:\n%s\nwant it to rebuild the constraint's own index", stmt)
	}
}

// TestKeyOptionsWritesNothingWhenUntouched. An apply here can issue a rebuild
// of the table's clustered index; an untouched page must not.
func TestKeyOptionsWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _, _ := loadKeyPage(t, keyOptionsPage)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, idxDatabase)
}
