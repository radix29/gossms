package activity

import (
	"context"
	"database/sql"
)

// fileKey identifies one database file across samples.
type fileKey struct {
	dbID   int
	fileID int
}

// fileRow is one file's cumulative I/O totals.
type fileRow struct {
	database  string
	isLog     bool
	reads     int64
	bytesRead int64
	stallRead int64
	writes    int64
	bytesWrit int64
	stallWrit int64
}

// fileSet is one sample of sys.dm_io_virtual_file_stats.
type fileSet map[fileKey]fileRow

// FileIO is one database's data-file or log-file I/O over the interval
// between two samples. A database with both contributes one of each: log
// writes and data writes have different shapes and a combined row hides
// which of the two a latency spike came from.
type FileIO struct {
	Database   string
	IsLog      bool
	ReadMBSec  float64
	WriteMBSec float64
	MsPerRead  float64
	MsPerWrite float64
}

// Label names the row the way a panel shows it: the database, plus the file
// kind when the row is the log half.
func (f FileIO) Label() string {
	if f.IsLog {
		return f.Database + " (log)"
	}
	return f.Database
}

// The DMV reports by file; sys.master_files supplies the database name and
// whether the file is a log file, which fileDeltas keeps separated from
// data-file I/O. DB_NAME() is used rather than a join to sys.databases so a
// database the connection can't see still contributes its I/O to the total.
const fileIOQuery = `
SELECT vfs.database_id, vfs.file_id, DB_NAME(vfs.database_id),
       CASE WHEN mf.type = 1 THEN 1 ELSE 0 END,
       vfs.num_of_reads, vfs.num_of_bytes_read, vfs.io_stall_read_ms,
       vfs.num_of_writes, vfs.num_of_bytes_written, vfs.io_stall_write_ms
FROM sys.dm_io_virtual_file_stats(NULL, NULL) AS vfs
LEFT JOIN sys.master_files AS mf
  ON mf.database_id = vfs.database_id AND mf.file_id = vfs.file_id`

// collectFileIO reads the cumulative per-file I/O totals.
func collectFileIO(ctx context.Context, db *sql.DB) (fileSet, error) {
	rows, err := db.QueryContext(ctx, fileIOQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := make(fileSet, 32)
	for rows.Next() {
		var k fileKey
		var r fileRow
		var name sql.NullString
		var isLog int
		if err := rows.Scan(&k.dbID, &k.fileID, &name, &isLog,
			&r.reads, &r.bytesRead, &r.stallRead,
			&r.writes, &r.bytesWrit, &r.stallWrit); err != nil {
			return nil, err
		}
		r.database = name.String
		if r.database == "" {
			r.database = "(unknown)"
		}
		r.isLog = isLog == 1
		set[k] = r
	}
	return set, rows.Err()
}

// fileDeltas turns two cumulative samples into throughput and latency per
// database and file kind, plus the totals across every file. Latency is the
// stall delta divided by the operation-count delta — an interval with no
// reads has no read latency to report, and reads as 0 rather than as a
// division by zero.
func fileDeltas(prev, cur fileSet, elapsed float64) (perDB []FileIO, total FileIO) {
	total.Database = "Total"
	if elapsed <= 0 {
		return nil, total
	}
	type acc struct {
		bytesRead, bytesWrit  float64
		reads, writes         float64
		stallRead, stallWrite float64
	}
	type groupKey struct {
		database string
		isLog    bool
	}
	byDB := map[groupKey]*acc{}
	order := []groupKey{}
	var all acc

	for k, c := range cur {
		p, ok := prev[k]
		if !ok {
			continue
		}
		d := acc{
			bytesRead:  float64(c.bytesRead - p.bytesRead),
			bytesWrit:  float64(c.bytesWrit - p.bytesWrit),
			reads:      float64(c.reads - p.reads),
			writes:     float64(c.writes - p.writes),
			stallRead:  float64(c.stallRead - p.stallRead),
			stallWrite: float64(c.stallWrit - p.stallWrit),
		}
		if d.reads < 0 || d.writes < 0 || d.bytesRead < 0 || d.bytesWrit < 0 {
			continue // the file was detached and reattached, or the server restarted
		}
		g := groupKey{database: c.database, isLog: c.isLog}
		a, ok := byDB[g]
		if !ok {
			a = &acc{}
			byDB[g] = a
			order = append(order, g)
		}
		a.bytesRead += d.bytesRead
		a.bytesWrit += d.bytesWrit
		a.reads += d.reads
		a.writes += d.writes
		a.stallRead += d.stallRead
		a.stallWrite += d.stallWrite

		all.bytesRead += d.bytesRead
		all.bytesWrit += d.bytesWrit
		all.reads += d.reads
		all.writes += d.writes
		all.stallRead += d.stallRead
		all.stallWrite += d.stallWrite
	}

	toIO := func(g groupKey, a acc) FileIO {
		io := FileIO{
			Database:   g.database,
			IsLog:      g.isLog,
			ReadMBSec:  a.bytesRead / bytesPerMB / elapsed,
			WriteMBSec: a.bytesWrit / bytesPerMB / elapsed,
		}
		if a.reads > 0 {
			io.MsPerRead = a.stallRead / a.reads
		}
		if a.writes > 0 {
			io.MsPerWrite = a.stallWrite / a.writes
		}
		return io
	}
	for _, g := range order {
		perDB = append(perDB, toIO(g, *byDB[g]))
	}
	return perDB, toIO(groupKey{database: "Total"}, all)
}

// bytesPerMB converts the DMV's byte counts for display.
const bytesPerMB = 1024 * 1024
