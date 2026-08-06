package activity

import (
	"context"
	"database/sql"
	"time"
)

// pagesPerMB converts SQL Server's 8KB page counts to megabytes.
const pagesPerMB = 128

// TempDBSpace is how tempdb's data files are being used, in megabytes. The
// four allocated parts plus Free add up to Total, which is what lets them be
// drawn as one stacked column.
type TempDBSpace struct {
	VersionStoreMB   float64
	UserObjectMB     float64
	InternalObjectMB float64
	MixedExtentMB    float64
	FreeMB           float64
	TotalMB          float64
}

// TempDBFile is one of tempdb's files. Log files are collected alongside the
// data files but carry no allocation breakdown — dm_db_file_space_usage
// reports data files only.
type TempDBFile struct {
	FileID int
	Name   string
	Type   string // "ROWS" or "LOG"
	SizeMB float64
	UsedMB float64
	// GrowthMB is the autogrowth increment, or the percentage when
	// PercentGrowth is set. Zero means growth is disabled.
	GrowthMB      float64
	PercentGrowth bool
}

// TempDBObjectKind groups what lives in tempdb by who made it, which is the
// distinction that decides who to go and talk to about it.
type TempDBObjectKind int

const (
	TempDBUserTemp TempDBObjectKind = iota
	TempDBGlobalTemp
	TempDBUserTable
	TempDBInternal
	TempDBSystem
	tempDBObjectKindCount
)

// TempDBObjectKindNames name the kinds in declaration order, so a chart's
// series and its legend can't drift apart.
var TempDBObjectKindNames = [tempDBObjectKindCount]string{
	"Local temp tables",
	"Global temp tables",
	"User tables",
	"Internal tables",
	"System tables",
}

// TempDBObjects is one kind's footprint in tempdb.
type TempDBObjects struct {
	Kind       TempDBObjectKind
	Count      int
	ReservedMB float64
	UsedMB     float64
	Rows       int64
}

// TempDBSession is one session's tempdb footprint, allocations net of
// deallocations. A session that has given its space back reads as zero
// rather than as the total it once held.
type TempDBSession struct {
	SessionID  int
	Host       string
	Program    string
	Login      string
	UserMB     float64
	InternalMB float64
	TotalMB    float64
}

// TempDBSample is one tick of the TempDB tab. Unlike Sample it holds no
// derived rates beyond the counters that are rates by nature: tempdb space
// is a level, and the question it answers is what is holding it.
type TempDBSample struct {
	At       time.Time
	Interval time.Duration

	Space    TempDBSpace
	Files    []TempDBFile
	Objects  [tempDBObjectKindCount]TempDBObjects
	Sessions []TempDBSession

	// Activity counters.
	ActiveTempTables    float64
	TempTableCreateSec  float64
	SnapshotTx          float64
	NonSnapshotTx       float64
	VersionStoreMB      float64
	VersionGenKBSec     float64
	VersionCleanupKBSec float64
	LongestTxSec        float64

	// Cores is the host's logical CPU count, kept beside the file list
	// because the one configuration rule this tab can check — one data file
	// per core up to eight — needs both.
	Cores int
}

// DataFiles are the tempdb files that hold data, which is what the file
// count rule and the space breakdown are about.
func (s TempDBSample) DataFiles() []TempDBFile {
	out := make([]TempDBFile, 0, len(s.Files))
	for _, f := range s.Files {
		if f.Type == "ROWS" {
			out = append(out, f)
		}
	}
	return out
}

// tempdbCounterNames are the counters this tab reads. They live in two
// objects that the main dashboard doesn't collect, so they get their own
// query rather than widening every 2-second tick.
var tempdbCounterNames = []string{
	// General Statistics
	"Active Temp Tables", "Temp Tables Creation Rate",
	// Transactions
	"Snapshot Transactions", "NonSnapshot Version Transactions",
	"Version Store Size (KB)", "Version Generation rate (KB/s)",
	"Version Cleanup rate (KB/s)", "Longest Transaction Running Time",
}

var tempdbCounterQuery = counterQueryFor(tempdbCounterNames)

// Three-part naming rather than a USE: dm_db_file_space_usage is
// database-scoped and would otherwise report whatever database the pooled
// connection happens to be sitting in.
const tempdbSpaceQuery = `
SELECT
    SUM(CAST(total_page_count AS bigint)),
    SUM(CAST(unallocated_extent_page_count AS bigint)),
    SUM(CAST(version_store_reserved_page_count AS bigint)),
    SUM(CAST(user_object_reserved_page_count AS bigint)),
    SUM(CAST(internal_object_reserved_page_count AS bigint)),
    SUM(CAST(mixed_extent_page_count AS bigint))
FROM tempdb.sys.dm_db_file_space_usage`

// tempdb.sys.database_files, never sys.master_files: master_files reports
// tempdb's *configured* size, the one it will be recreated at on the next
// restart, while database_files reports what it has actually grown to. On a
// server whose tempdb has grown — the case this tab exists for — the two
// disagree by the entire amount that matters.
//
// The allocation view is joined on so a file's used space sits beside the
// size it is used out of. Log files have no row there, hence the outer join.
const tempdbFileQuery = `
SELECT df.file_id, df.name, df.type_desc,
       CAST(df.size AS bigint),
       CAST(ISNULL(fsu.total_page_count - fsu.unallocated_extent_page_count, 0) AS bigint),
       df.growth, df.is_percent_growth
FROM tempdb.sys.database_files df
LEFT JOIN tempdb.sys.dm_db_file_space_usage fsu ON fsu.file_id = df.file_id
ORDER BY df.type_desc DESC, df.file_id`

// Objects are classified by who created them: a name beginning with ## is a
// global temp table, one # a session's own, an IT row an internal table the
// engine made for a query, and is_ms_shipped the catalog itself.
const tempdbObjectQuery = `
SELECT kind, COUNT(DISTINCT object_id),
       SUM(reserved_page_count), SUM(used_page_count), SUM(row_count)
FROM (
    SELECT o.object_id,
           CASE WHEN o.type = 'IT' THEN 3
                WHEN o.is_ms_shipped = 1 THEN 4
                WHEN o.name LIKE '##%' THEN 1
                WHEN o.name LIKE '#%' THEN 0
                ELSE 2 END AS kind,
           ps.reserved_page_count, ps.used_page_count, ps.row_count
    FROM tempdb.sys.objects o
    JOIN tempdb.sys.dm_db_partition_stats ps ON ps.object_id = o.object_id
) x
GROUP BY kind`

// Sessions are reported net of deallocation: a session that has released its
// work tables is not still holding the space. Idle sessions holding nothing
// are dropped here rather than in the UI so the grid never has to page
// through hundreds of zero rows.
//
// Task usage is added to session usage, and that is not optional: a batch's
// allocations live in dm_db_task_space_usage until the batch *finishes* and
// only then roll into dm_db_session_space_usage. Reading sessions alone
// shows an empty grid for exactly the case this panel exists for — a
// long-running query filling tempdb right now.
const tempdbSessionQuery = `
SELECT su.session_id,
       ISNULL(s.host_name, ''), ISNULL(s.program_name, ''), ISNULL(s.login_name, ''),
       CAST(su.user_objects_alloc_page_count - su.user_objects_dealloc_page_count
            + ISNULL(tu.user_alloc, 0) - ISNULL(tu.user_dealloc, 0) AS bigint),
       CAST(su.internal_objects_alloc_page_count - su.internal_objects_dealloc_page_count
            + ISNULL(tu.internal_alloc, 0) - ISNULL(tu.internal_dealloc, 0) AS bigint)
FROM sys.dm_db_session_space_usage su
JOIN sys.dm_exec_sessions s ON s.session_id = su.session_id
LEFT JOIN (
    SELECT session_id,
           SUM(user_objects_alloc_page_count) AS user_alloc,
           SUM(user_objects_dealloc_page_count) AS user_dealloc,
           SUM(internal_objects_alloc_page_count) AS internal_alloc,
           SUM(internal_objects_dealloc_page_count) AS internal_dealloc
    FROM sys.dm_db_task_space_usage
    GROUP BY session_id
) tu ON tu.session_id = su.session_id
WHERE su.user_objects_alloc_page_count - su.user_objects_dealloc_page_count
    + su.internal_objects_alloc_page_count - su.internal_objects_dealloc_page_count
    + ISNULL(tu.user_alloc, 0) - ISNULL(tu.user_dealloc, 0)
    + ISNULL(tu.internal_alloc, 0) - ISNULL(tu.internal_dealloc, 0) > 0
ORDER BY su.user_objects_alloc_page_count - su.user_objects_dealloc_page_count
    + su.internal_objects_alloc_page_count - su.internal_objects_dealloc_page_count
    + ISNULL(tu.user_alloc, 0) - ISNULL(tu.user_dealloc, 0)
    + ISNULL(tu.internal_alloc, 0) - ISNULL(tu.internal_dealloc, 0) DESC`

const tempdbCoreQuery = `SELECT cpu_count FROM sys.dm_os_sys_info`

// tempdbSnapshot is one raw reading, before the counter rates are derived.
type tempdbSnapshot struct {
	at       time.Time
	counters counterSet
	sample   TempDBSample
}

// collectTempDB reads one full tempdb picture. The queries run in sequence
// on one connection for the same reason Collect's do: they describe one
// instant, and running them concurrently would make them disagree about
// which.
func collectTempDB(ctx context.Context, db *sql.DB) (*tempdbSnapshot, error) {
	snap := &tempdbSnapshot{at: time.Now()}
	var err error

	if snap.counters, err = collectCounterSet(ctx, db, tempdbCounterQuery); err != nil {
		return nil, err
	}
	if snap.sample.Space, err = collectTempDBSpace(ctx, db); err != nil {
		return nil, err
	}
	if snap.sample.Files, err = collectTempDBFiles(ctx, db); err != nil {
		return nil, err
	}
	if snap.sample.Objects, err = collectTempDBObjects(ctx, db); err != nil {
		return nil, err
	}
	if snap.sample.Sessions, err = collectTempDBSessions(ctx, db); err != nil {
		return nil, err
	}
	if err = db.QueryRowContext(ctx, tempdbCoreQuery).Scan(&snap.sample.Cores); err != nil {
		return nil, err
	}
	return snap, nil
}

func collectTempDBSpace(ctx context.Context, db *sql.DB) (TempDBSpace, error) {
	var total, free, version, user, internal, mixed sql.NullInt64
	err := db.QueryRowContext(ctx, tempdbSpaceQuery).Scan(&total, &free, &version, &user, &internal, &mixed)
	if err != nil {
		return TempDBSpace{}, err
	}
	mb := func(v sql.NullInt64) float64 { return float64(v.Int64) / pagesPerMB }
	return TempDBSpace{
		TotalMB:          mb(total),
		FreeMB:           mb(free),
		VersionStoreMB:   mb(version),
		UserObjectMB:     mb(user),
		InternalObjectMB: mb(internal),
		MixedExtentMB:    mb(mixed),
	}, nil
}

func collectTempDBFiles(ctx context.Context, db *sql.DB) ([]TempDBFile, error) {
	rows, err := db.QueryContext(ctx, tempdbFileQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TempDBFile
	for rows.Next() {
		var f TempDBFile
		var sizePages, usedPages int64
		var growth int64
		if err := rows.Scan(&f.FileID, &f.Name, &f.Type, &sizePages, &usedPages, &growth, &f.PercentGrowth); err != nil {
			return nil, err
		}
		f.SizeMB = float64(sizePages) / pagesPerMB
		f.UsedMB = float64(usedPages) / pagesPerMB
		if f.PercentGrowth {
			f.GrowthMB = float64(growth) // already a percentage
		} else {
			f.GrowthMB = float64(growth) / pagesPerMB
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func collectTempDBObjects(ctx context.Context, db *sql.DB) ([tempDBObjectKindCount]TempDBObjects, error) {
	var out [tempDBObjectKindCount]TempDBObjects
	for i := range out {
		out[i].Kind = TempDBObjectKind(i)
	}

	rows, err := db.QueryContext(ctx, tempdbObjectQuery)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var kind, count int
		var reserved, used, rowCount sql.NullInt64
		if err := rows.Scan(&kind, &count, &reserved, &used, &rowCount); err != nil {
			return out, err
		}
		if kind < 0 || kind >= int(tempDBObjectKindCount) {
			continue
		}
		out[kind] = TempDBObjects{
			Kind:       TempDBObjectKind(kind),
			Count:      count,
			ReservedMB: float64(reserved.Int64) / pagesPerMB,
			UsedMB:     float64(used.Int64) / pagesPerMB,
			Rows:       rowCount.Int64,
		}
	}
	return out, rows.Err()
}

func collectTempDBSessions(ctx context.Context, db *sql.DB) ([]TempDBSession, error) {
	rows, err := db.QueryContext(ctx, tempdbSessionQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TempDBSession
	for rows.Next() {
		var s TempDBSession
		var userPages, internalPages int64
		if err := rows.Scan(&s.SessionID, &s.Host, &s.Program, &s.Login, &userPages, &internalPages); err != nil {
			return nil, err
		}
		// Clamped at zero: session and task usage are summed, and a task that
		// releases pages its session already accounted for can drive one part
		// briefly negative. "Holding -0.4 MB" is not a thing a reader can act
		// on, so it reads as holding none.
		s.UserMB = nonNegativeMB(userPages)
		s.InternalMB = nonNegativeMB(internalPages)
		s.TotalMB = s.UserMB + s.InternalMB
		out = append(out, s)
	}
	return out, rows.Err()
}

// nonNegativeMB converts a page count to megabytes, floored at zero.
func nonNegativeMB(pages int64) float64 {
	if pages <= 0 {
		return 0
	}
	return float64(pages) / pagesPerMB
}

// deriveTempDB finishes a snapshot into a sample by decoding the counters
// against the previous reading. prev may be nil, in which case the rate
// counters read zero — the same rule Derive follows, and for the same
// reason.
func deriveTempDB(prev, cur *tempdbSnapshot) TempDBSample {
	s := cur.sample
	s.At = cur.at

	var prevCounters counterSet
	var elapsed float64
	if prev != nil {
		prevCounters = prev.counters
		s.Interval = cur.at.Sub(prev.at)
		elapsed = s.Interval.Seconds()
	}
	c := cur.counters

	s.ActiveTempTables = c.value(prevCounters, objGeneralStats, "Active Temp Tables", "", elapsed)
	s.TempTableCreateSec = c.value(prevCounters, objGeneralStats, "Temp Tables Creation Rate", "", elapsed)
	s.SnapshotTx = c.value(prevCounters, objTransactions, "Snapshot Transactions", "", elapsed)
	s.NonSnapshotTx = c.value(prevCounters, objTransactions, "NonSnapshot Version Transactions", "", elapsed)
	s.VersionStoreMB = c.value(prevCounters, objTransactions, "Version Store Size (KB)", "", elapsed) / 1024
	s.VersionGenKBSec = c.value(prevCounters, objTransactions, "Version Generation rate (KB/s)", "", elapsed)
	s.VersionCleanupKBSec = c.value(prevCounters, objTransactions, "Version Cleanup rate (KB/s)", "", elapsed)
	s.LongestTxSec = c.value(prevCounters, objTransactions, "Longest Transaction Running Time", "", elapsed)
	return s
}
