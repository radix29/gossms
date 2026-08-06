package activity

import (
	"context"
	"database/sql"
	"time"
)

// Snapshot is one tick's raw reading of the server: cumulative counters as
// SQL Server reports them, before any rate conversion. Snapshots are only
// meaningful in pairs — see Derive.
type Snapshot struct {
	At       time.Time
	Counters counterSet
	Waits    waitSet
	Files    fileSet
	Memory   []MemoryComponent
	Sched    SchedStats
	Sessions SessionStats
	CPU      CPUUsage
	Load     []SchedulerLoad
}

// Collect reads one full snapshot. The queries run in sequence on one
// connection: they are all metadata reads against DMVs, and running them
// concurrently would need a connection each while making the readings
// disagree about which instant they describe.
func Collect(ctx context.Context, db *sql.DB) (*Snapshot, error) {
	s := &Snapshot{At: time.Now()}

	var err error
	if s.Counters, err = collectCounters(ctx, db); err != nil {
		return nil, err
	}
	if s.Waits, err = collectWaits(ctx, db); err != nil {
		return nil, err
	}
	if s.Files, err = collectFileIO(ctx, db); err != nil {
		return nil, err
	}
	if s.Memory, err = collectMemory(ctx, db); err != nil {
		return nil, err
	}
	if s.Sched, err = collectSchedulers(ctx, db); err != nil {
		return nil, err
	}
	if s.Sessions, err = collectSessions(ctx, db); err != nil {
		return nil, err
	}
	if s.CPU, err = collectCPUUsage(ctx, db); err != nil {
		return nil, err
	}
	if s.Load, err = collectSchedulerLoad(ctx, db); err != nil {
		return nil, err
	}
	return s, nil
}

// permissionQuery checks the one permission everything here needs. Without
// it the DMV queries return an empty result set rather than an error, so a
// dashboard with no permission is indistinguishable from an idle server.
const permissionQuery = `SELECT CASE WHEN HAS_PERMS_BY_NAME(NULL, NULL, 'VIEW SERVER STATE') = 1 THEN 1 ELSE 0 END`

// HasViewServerState reports whether the connection may read the DMVs this
// package queries.
func HasViewServerState(ctx context.Context, db *sql.DB) (bool, error) {
	var ok int
	if err := db.QueryRowContext(ctx, permissionQuery).Scan(&ok); err != nil {
		return false, err
	}
	return ok == 1, nil
}
