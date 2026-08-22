package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Database Properties > General has only two editable rows, and both are
// server-scoped writes against the database as a whole: ALTER AUTHORIZATION,
// which hands the database to another principal, and SET RECOVERY, which on
// the way to SIMPLE breaks the log backup chain — the change that quietly
// costs point-in-time recovery until the next full backup.
//
// The Owner row is also one of the selectPreserving dropdowns: an owner the
// login list does not contain is shown as a stand-in, and the write is gated
// on changedTo so the stand-in itself can never be sent as a principal name.

const genDatabase = "appdb"

// databaseGeneralResponses scripts the six reads the page runs. The owner
// (appwriter) is deliberately neither the first login nor the last.
func databaseGeneralResponses(owner string) []fakeResponse {
	return []fakeResponse{
		dbByNameResp(genDatabase, 5),
		{match: "AS total_mb", db: genDatabase, cols: 5, rows: [][]driver.Value{
			{12.0, 8.0, 4.0, 3.0, 2.0},
		}},
		{match: "page_verify_option_desc", cols: 25, rows: [][]driver.Value{{
			owner, "CHECKSUM", "MULTI_USER", "NONE", false, "OFF",
			false, false, true,
			true, false,
			false, true, true,
			true, true, true,
			false, true, false,
			false, false,
			false, false, false,
		}}},
		{match: "WHERE  type IN ('S','U','G')", db: genDatabase, cols: 7, rows: [][]driver.Value{
			{"appreader", int64(5), "SQL_USER", "dbo", time.Time{}, time.Time{}, "INSTANCE"},
		}},
		{match: "msdb.dbo.backupset", cols: 12, rows: nil},
		loginListResponse(),
	}
}

func loadDatabaseGeneral(t *testing.T, owner string) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, databaseGeneralResponses(owner)...)
	form, apply := loadPage(t, pageDatabaseGeneral(sc, genDatabase), inst)
	return inst, apply, form
}

// TestDatabaseGeneralOwnerChangeNamesTheDatabaseAndThePrincipal. ALTER
// AUTHORIZATION hands over CONTROL of the database; naming the wrong one of
// the two identifiers gives it away to somebody else, and neither mistake
// shows on the page that made it.
func TestDatabaseGeneralOwnerChangeNamesTheDatabaseAndThePrincipal(t *testing.T) {
	inst, apply, form := loadDatabaseGeneral(t, "appuser")

	editSelect(t, form, "Owner", "otheruser")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AUTHORIZATION ON DATABASE::[appdb] TO [otheruser]")
}

// TestDatabaseGeneralRecoveryModelWritesTheKeyword insists the model sent is
// the one the row is showing. The dropdown's items are the T-SQL keywords
// themselves, so there is no separate label table to fall out of step — what
// this catches is the write reading the wrong row, which on the way to SIMPLE
// breaks the log backup chain and costs point-in-time recovery until the next
// full backup.
func TestDatabaseGeneralRecoveryModelWritesTheKeyword(t *testing.T) {
	for _, model := range []string{"SIMPLE", "BULK_LOGGED"} {
		t.Run(model, func(t *testing.T) {
			inst, apply, form := loadDatabaseGeneral(t, "appuser")

			editSelect(t, form, "Recovery model", model)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertOneStatement(t, inst, "ALTER DATABASE [appdb] SET RECOVERY "+model)
		})
	}
}

// TestDatabaseGeneralKeepsAnOwnerTheLoginListDoesNotHave. An owner with no
// matching login — a Windows principal removed from AD, an msdb restored from
// another instance — must not be reported as one of the logins that *is*
// listed, and above all must not be written back. The stand-in is what the row
// shows instead, and changedTo is what keeps it out of the statement.
func TestDatabaseGeneralKeepsAnOwnerTheLoginListDoesNotHave(t *testing.T) {
	inst, apply, form := loadDatabaseGeneral(t, "OLDDOMAIN\\dba")

	if got := selectRow(t, form, "Owner").Value(); got != "OLDDOMAIN\\dba" {
		t.Errorf("Owner shows %q, want the real owner the server reported", got)
	}

	// Applying an untouched page must not write the stand-in — or anything.
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestDatabaseGeneralWritesNothingWhenUntouched.
func TestDatabaseGeneralWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _ := loadDatabaseGeneral(t, "appuser")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
