package tui

import (
	"context"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// The rule for which databases a dropdown offers, in one place rather than
// decided separately at each of thirteen call sites.
//
// It turns on *when* the name is resolved, not on what looks tidy:
//
//   - A name stored now and resolved later — a job step's database, an
//     alert's database, a login's default database — lists every database,
//     system and non-ONLINE alike. The database is opened when the job runs
//     or the login connects, so one that is offline today is a legitimate
//     choice, and filtering it out would silently drop a name the user
//     already has configured on the server.
//   - A name acted on now lists only what the action can actually run
//     against. Backup is the only such dialog; see backupDatabaseNames.
//
// databaseNames is the first case: every database, in the server's order.
func databaseNames(ctx context.Context, sc *db.ServerConn) ([]string, error) {
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	return namesOf(dbs), nil
}

// backupDatabaseNames is the second case: the databases BACKUP DATABASE will
// accept right now.
//
// Both exclusions are hard server restrictions verified on win10cli, not
// preferences — `BACKUP DATABASE tempdb` and a backup of an OFFLINE database
// each fail with "BACKUP DATABASE is terminating abnormally". Offering either
// gives the user a dropdown entry whose only outcome is that error.
func backupDatabaseNames(ctx context.Context, sc *db.ServerConn) ([]string, error) {
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(dbs))
	for _, d := range dbs {
		if strings.EqualFold(d.Name(), "tempdb") || d.State() != "ONLINE" {
			continue
		}
		out = append(out, d.Name())
	}
	return out, nil
}

// namesOf is the loop that five prefetches had a byte-identical copy of.
func namesOf(dbs []*gosmo.Database) []string {
	out := make([]string, len(dbs))
	for i, d := range dbs {
		out[i] = d.Name()
	}
	return out
}
