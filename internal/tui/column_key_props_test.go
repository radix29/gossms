package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Column Encryption Key page is the only writable Always Encrypted page,
// and what it writes is a master-key rotation: ADD VALUE puts the key under a
// second master key, DROP VALUE retires the first. Both are irreversible in
// the direction that matters — nothing can regenerate an encrypted value, so
// a drop aimed at the wrong master key makes columns unreadable with no way
// back. These drive the page's real load and apply closures against the
// scripted instance and read back the statement that reached the server.

// cekPageResponses scripts a database with three column master keys and two
// column encryption keys. CEK2 is the one under test and is not the key that
// sorts first: a page that ignored the name it was opened for would read CEK1
// and every assertion below would still be about CEK2.
//
// values names the master keys CEK2 is currently encrypted under.
func cekPageResponses(values ...string) []fakeResponse {
	rows := make([][]driver.Value, 0, len(values))
	for i, cmk := range values {
		rows = append(rows, []driver.Value{"CEK2", int64(2), cmk, "RSA_OAEP", []byte{byte(i + 1)}})
	}
	return []fakeResponse{
		{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
			"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now(),
		}}},
		// Scoped by arg, and ahead of the fallback: both reads are the same
		// SELECT a WHERE apart, so without this the by-name read is served
		// the other key's rows and the page silently rotates CEK1.
		{match: "cekv.encrypted_value", arg: "CEK2", cols: 5, rows: rows},
		{match: "cekv.encrypted_value", cols: 5, rows: [][]driver.Value{
			{"CEK1", int64(1), "CMK1", "RSA_OAEP", []byte{0x09}},
		}},
		{match: "key_store_provider_name", cols: 6, rows: [][]driver.Value{
			{"CMK1", int64(1), "MSSQL_CERTIFICATE_STORE", "CurrentUser/my/aa", false, nil},
			{"CMK2", int64(2), "MSSQL_CERTIFICATE_STORE", "CurrentUser/my/bb", false, nil},
			{"CMK3", int64(3), "MSSQL_CERTIFICATE_STORE", "CurrentUser/my/cc", false, nil},
		}},
	}
}

// TestColumnEncryptionKeyPageAddsAValueUnderTheChosenMasterKey is the first
// half of a rotation: the value the user pastes goes out under the master key
// they picked, against the key the page was opened for.
func TestColumnEncryptionKeyPageAddsAValueUnderTheChosenMasterKey(t *testing.T) {
	sc, inst := newFakeConn(t, cekPageResponses("CMK1")...)
	form, apply := loadPage(t, columnEncryptionKeyPropPages(sc, "appdb", "CEK2")[0], inst)

	// A master key the key already has a value under is not offered: ADD
	// VALUE for it is an error, so listing it is a dead option.
	if items := selectRow(t, form, "Encrypt under master key").Items(); slices.Contains(items, "CMK1") {
		t.Errorf("the master key CEK2 is already encrypted under is offered: %q", items)
	}

	editSelect(t, form, "Encrypt under master key", "CMK2")
	editText(t, form, "New encrypted value (hex)", "0x0AFF")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("want one statement in appdb, got %d: %q", len(stmts), stmts)
	}
	for _, want := range []string{
		"ALTER COLUMN ENCRYPTION KEY [CEK2]", "ADD VALUE",
		"COLUMN_MASTER_KEY = [CMK2]", "ENCRYPTED_VALUE = 0x0AFF",
	} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("statement is missing %q:\n%s", want, stmts[0])
		}
	}
}

// TestColumnEncryptionKeyPageDropsTheChosenValue is the second half, on a key
// mid-rotation. The dropdown offers only the master keys this key actually
// has a value under, and the drop names the one the user picked — the other
// value's data is what stays readable.
func TestColumnEncryptionKeyPageDropsTheChosenValue(t *testing.T) {
	sc, inst := newFakeConn(t, cekPageResponses("CMK1", "CMK2")...)
	form, apply := loadPage(t, columnEncryptionKeyPropPages(sc, "appdb", "CEK2")[0], inst)

	if items := selectRow(t, form, "Drop value under master key").Items(); slices.Contains(items, "CMK3") {
		t.Errorf("a master key this key has no value under is offered for dropping: %q", items)
	}

	editSelect(t, form, "Drop value under master key", "CMK1")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("want one statement in appdb, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "DROP VALUE") || !strings.Contains(stmts[0], "COLUMN_MASTER_KEY = [CMK1]") {
		t.Errorf("the wrong value was dropped:\n%s", stmts[0])
	}
	if strings.Contains(stmts[0], "CMK2") {
		t.Errorf("the value that must survive the rotation was named:\n%s", stmts[0])
	}
}

// TestColumnEncryptionKeyPageWritesNothingUntouched pins the dropdowns'
// "(none)" opening: both are Select rows, which have no unset state, so a
// page whose first item meant a real master key would rotate the key on an
// OK the user pressed to close a page they only read.
func TestColumnEncryptionKeyPageWritesNothingUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, cekPageResponses("CMK1")...)
	_, apply := loadPage(t, columnEncryptionKeyPropPages(sc, "appdb", "CEK2")[0], inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, "appdb")
}

// TestColumnEncryptionKeyPageRefusesAHalfRotation covers the three requests
// that are not a rotation. The last one the server also refuses (Msg 33275);
// the page's own answer names the value and the reason, where the server's
// arrives after a round trip and names neither.
func TestColumnEncryptionKeyPageRefusesAHalfRotation(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(t *testing.T, f *propsheet.Form)
		want string
	}{
		{"a value with no master key", func(t *testing.T, f *propsheet.Form) {
			editText(t, f, "New encrypted value (hex)", "0x0AFF")
		}, "choose the column master key"},
		{"a master key with no value", func(t *testing.T, f *propsheet.Form) {
			editSelect(t, f, "Encrypt under master key", "CMK2")
		}, "value encrypted under CMK2 is required"},
		{"dropping the only value", func(t *testing.T, f *propsheet.Form) {
			editSelect(t, f, "Drop value under master key", "CMK1")
		}, "leaves the key with none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sc, inst := newFakeConn(t, cekPageResponses("CMK1")...)
			form, apply := loadPage(t, columnEncryptionKeyPropPages(sc, "appdb", "CEK2")[0], inst)
			c.edit(t, form)

			err := apply(context.Background())
			if err == nil {
				t.Fatalf("no error; statements: %q", inst.StatementsIn("appdb"))
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
			assertNoStatementsIn(t, inst, "appdb")
		})
	}
}

// TestColumnEncryptionKeyPageAddsBeforeItDrops pins the order of a rotation
// done in one Apply. Dropping first would leave the key with no value at all
// if the add then failed — every column encrypted with it unreadable — where
// adding first leaves the rotation merely half done.
func TestColumnEncryptionKeyPageAddsBeforeItDrops(t *testing.T) {
	sc, inst := newFakeConn(t, cekPageResponses("CMK1")...)
	form, apply := loadPage(t, columnEncryptionKeyPropPages(sc, "appdb", "CEK2")[0], inst)

	editSelect(t, form, "Encrypt under master key", "CMK2")
	editText(t, form, "New encrypted value (hex)", "0x0AFF")
	editSelect(t, form, "Drop value under master key", "CMK1")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 2 {
		t.Fatalf("want two statements in appdb, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "ADD VALUE") || !strings.Contains(stmts[1], "DROP VALUE") {
		t.Errorf("the drop did not follow the add:\n%s", strings.Join(stmts, "\n---\n"))
	}
}
