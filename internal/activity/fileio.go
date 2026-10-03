package activity

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// fileKey identifies a database file across samples.
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

// FileIO is one database's data-file or log-file I/O between two samples. Log
// and data are separate rows so a latency spike's source is visible.
type FileIO struct {
	Database   string
	IsLog      bool
	ReadMBSec  float64
	WriteMBSec float64
	MsPerRead  float64
	MsPerWrite float64
}

// Label is the database name, plus the file kind for the log row.
func (f FileIO) Label() string {
	if f.IsLog {
		return f.Database + " (log)"
	}
	return f.Database
}

// collectFileIO reads every database file's cumulative I/O.
func collectFileIO(ctx context.Context, src Source) (fileSet, error) {
	stats, err := src.FileIOStats(ctx)
	if err != nil {
		return nil, err
	}
	set := make(fileSet, len(stats))
	for _, f := range stats {
		// A database the login can't see has no name; its I/O still counts,
		// under a usable one.
		name := f.Database
		if name == "" {
			name = "(unknown)"
		}
		set[fileKey{dbID: f.DatabaseID, fileID: f.FileID}] = fileRow{
			database: name, isLog: f.IsLog,
			reads: f.Reads, bytesRead: f.BytesRead, stallRead: f.IOStallReadMs,
			writes: f.Writes, bytesWrit: f.BytesWritten, stallWrit: f.IOStallWriteMs,
		}
	}
	return set, nil
}

// fileDeltas turns two cumulative samples into throughput and latency per
// database and file kind, plus totals. Latency is stall delta / operation-count
// delta; no operations reads 0. perDB is in name order, data before log, so
// consumers never inherit map-iteration order.
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
	for g, a := range byDB {
		perDB = append(perDB, toIO(g, *a))
	}
	slices.SortFunc(perDB, CompareFileIOByName)
	return perDB, toIO(groupKey{database: "Total"}, all)
}

// CompareFileIOByName orders by database name, then the data row before the
// log row: a total order over fileDeltas's rows, for a stable display.
func CompareFileIOByName(a, b FileIO) int {
	return cmp.Or(strings.Compare(a.Database, b.Database), compareBool(a.IsLog, b.IsLog))
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

// bytesPerMB converts the DMV's byte counts for display.
const bytesPerMB = 1024 * 1024
