package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// database_snapshot_props.go is the read-only Properties for a database
// snapshot.
//
// Nothing here writes, and this is not the Database Properties dialog with
// its pages disabled: a snapshot is read-only by construction, has no
// transaction log and no recovery model to set, and the two operations it
// does have — revert and drop — are its context menu's, not a form's. Both
// pages are named in prop_page_requires_test.go's pagesThatOnlyRead.
//
// The Files page reads the snapshot's own sys.database_files, which lists the
// sparse files, not the source's. Their size is what the snapshot has
// actually copied so far — a snapshot starts near empty and grows as its
// source changes — so it is the one figure here that moves.

func findDatabaseSnapshot(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.DatabaseSnapshot, error) {
	return sc.Server.DatabaseSnapshotByNameContext(ctx, name)
}

func databaseSnapshotPropPages(sc *db.ServerConn, name string) []propPage {
	return []propPage{
		pageDatabaseSnapshotGeneral(sc, name),
		pageDatabaseSnapshotFiles(sc, name),
	}
}

func pageDatabaseSnapshotGeneral(sc *db.ServerConn, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := findDatabaseSnapshot(ctx, sc, name)
			if err != nil {
				return nil, nil, err
			}
			// A source that is gone leaves the snapshot in the catalog and
			// unusable — it cannot be reverted to, and only a drop is left.
			// Saying so beats an empty value that reads as "not read yet".
			source := s.SourceDatabase
			if source == "" {
				source = "(the source database has been dropped)"
			}
			f := propsheet.NewForm(
				propsheet.Section("Snapshot"),
				propsheet.Static("Name", s.Name),
				propsheet.Static("Source database", source),
				propsheet.Static("Created", formatSQLDate(s.CreateDate)),
				propsheet.Static("State", s.State),
				propsheet.Static("Database ID", core.FormatThousands(int64(s.DatabaseID))),
			)
			f.Add(propsheet.Note("A database snapshot is read-only and cannot be altered. Reverting its source database to it, and dropping it, are on its context menu."))
			return f, nil, nil
		},
	}
}

func pageDatabaseSnapshotFiles(sc *db.ServerConn, name string) propPage {
	return propPage{
		title: "Files",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			files, err := sc.Server.DatabaseFilesContext(ctx, name)
			if err != nil {
				return nil, nil, err
			}
			cols := []string{"Logical Name", "Type", "Size (MB)", "Path"}
			rows := make([][]string, 0, len(files))
			for _, fl := range files {
				rows = append(rows, []string{
					fl.Name, fl.Type, core.FormatThousands(fl.SizeKB / 1024), fl.PhysicalName,
				})
			}
			grid := controls.NewDataGrid()
			grid.SetData(cols, rows)
			f := propsheet.NewForm(
				propsheet.Section("Sparse files"),
				propsheet.NewGridRow(grid, 10),
			)
			f.Add(propsheet.Note("A snapshot's files are sparse: each starts near empty and grows as pages change in the source database. A snapshot has no transaction log."))
			return f, nil, nil
		},
	}
}
