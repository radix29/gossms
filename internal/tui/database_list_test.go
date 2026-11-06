package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"

	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/db"
)

// databasesOnGosmotest is a ServerConn over gosmotest answering
// Server.Databases with rows — the first test file off gosmotest_fixtures_test.go (plan
// step 18). Rows are databaseRow's, positional in gosmo's SELECT order.
func databasesOnGosmotest(t *testing.T, rows ...[]driver.Value) *db.ServerConn {
	t.Helper()
	f := gosmotest.New(t)
	out := make([][]any, len(rows))
	for i, r := range rows {
		out[i] = make([]any, len(r))
		for j, v := range r {
			out[i][j] = v
		}
	}
	f.On("compatibility_level, collation_name", gosmotest.Rows(out...))
	return &db.ServerConn{Server: f.Server()}
}

// backupDatabases offers only what BACKUP DATABASE accepts: tempdb, a
// non-ONLINE database and a database snapshot (Msg 3002) are each a certain
// refusal, so none is listed.
func TestBackupDatabaseNamesDropsWhatBackupRefuses(t *testing.T) {
	offline := databaseRow(8, "offlinedb", 0)
	offline[2] = "OFFLINE"
	sc := databasesOnGosmotest(t,
		databaseRow(1, "master", 0),
		databaseRow(2, "tempdb", 0),
		databaseRow(7, "appdb", 0),
		offline,
		databaseRow(9, "appdb_snap", 7),
	)
	dbs, err := backupDatabases(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := namesOf(dbs), []string{"master", "appdb"}; !slices.Equal(got, want) {
		t.Errorf("backupDatabases = %v, want %v", got, want)
	}
}

// On a case-sensitive server TEMPDB is a legal user database distinct from
// tempdb (ID 2); only the real tempdb is excluded.
func TestBackupDatabasesKeepsCaseVariantOfTempdb(t *testing.T) {
	sc := databasesOnGosmotest(t,
		databaseRow(2, "tempdb", 0),
		databaseRow(9, "TEMPDB", 0),
	)
	dbs, err := backupDatabases(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := namesOf(dbs), []string{"TEMPDB"}; !slices.Equal(got, want) {
		t.Errorf("backupDatabases = %v, want %v", got, want)
	}
}
