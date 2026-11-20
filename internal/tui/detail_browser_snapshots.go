package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/core"
)

// detail_browser_snapshots.go is the Detail Browser's view of the Database
// Snapshots folder and of one snapshot.
//
// Like every other detail view here, the leaf reuses the finder its
// Properties page uses (findDatabaseSnapshot), so the pane and the dialog
// cannot disagree about what a snapshot is.

// databaseSnapshotsFolderDetail lists the server's snapshots. The source is a
// column rather than a label suffix: it is the only thing separating two
// snapshots of different databases taken minutes apart, and the tree shows
// the name alone.
func databaseSnapshotsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	snaps, err := sc.Server.DatabaseSnapshots(ctx)
	if err != nil {
		return nil, nil, err
	}
	snaps = filterObjects(node.data.Filter, snaps, func(s *gosmo.DatabaseSnapshot) nodeData {
		return nodeData{Name: s.Name, CreateDate: s.CreateDate}
	})
	rows := make([][]string, 0, len(snaps))
	for _, s := range snaps {
		rows = append(rows, []string{s.Name, s.SourceDatabase, string(s.State), formatSQLDate(s.CreateDate)})
		// The mapping is what gives the pane its own Delete; without it the
		// item is withheld with no explanation.
		*objs = append(*objs, nodeData{
			Type: NodeDatabaseSnapshot, DBName: s.Name, Name: s.Name,
			SourceDatabase: s.SourceDatabase,
		})
	}
	return []string{"Name", "Source", "State", "Created"}, rows, nil
}

// databaseSnapshotDetail is one snapshot's own view: the catalog row, plus
// the sparse files, which are what a snapshot actually costs.
func databaseSnapshotDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	s, err := findDatabaseSnapshot(ctx, sc, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	source := s.SourceDatabase
	if source == "" {
		source = "(dropped)"
	}
	rows := [][]string{
		{"Name", s.Name},
		{"Source database", source},
		{"State", string(s.State)},
		{"Created", formatSQLDate(s.CreateDate)},
	}
	// A file read that fails says so in its own row rather than failing the
	// pane: the catalog row above is already worth showing, and a snapshot
	// whose files cannot be read is exactly the one worth looking at.
	files, err := sc.Server.DatabaseFiles(ctx, s.Name)
	if err != nil {
		rows = append(rows, []string{"Files", "N/A"})
		return []string{"Property", "Value"}, rows, nil
	}
	onDisk, maxSize := snapshotFileSizes(files)
	rows = append(rows,
		[]string{"Sparse files", strconv.Itoa(len(files))},
		[]string{"Size on disk", onDisk},
		[]string{"Maximum size", maxSize},
	)
	return []string{"Property", "Value"}, rows, nil
}

// snapshotFileSizes totals a snapshot's sparse files two ways. The catalog
// size is only the ceiling (the source's size when the snapshot was taken —
// what a 64 MB source's snapshot reports while occupying 128 KB), so the
// on-disk figure is the one that says what the snapshot costs. It is "N/A"
// when any file's is unknown: a partial sum would read as the whole.
func snapshotFileSizes(files []*gosmo.DatabaseFileInfo) (onDisk, maxSize string) {
	var diskKB, maxKB int64
	known := len(files) > 0
	for _, f := range files {
		diskKB += f.SizeOnDiskKB
		maxKB += f.SizeKB
		known = known && f.SizeOnDiskKB > 0
	}
	onDisk = "N/A"
	if known {
		onDisk = core.FormatBytes(diskKB * 1024)
	}
	return onDisk, core.FormatBytes(maxKB * 1024)
}

// snapshotFileSizeOnDisk is one file's on-disk size, "N/A" when unknown.
func snapshotFileSizeOnDisk(f *gosmo.DatabaseFileInfo) string {
	if f.SizeOnDiskKB <= 0 {
		return "N/A"
	}
	return core.FormatBytes(f.SizeOnDiskKB * 1024)
}
