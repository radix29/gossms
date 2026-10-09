package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// backupDevicePropPages builds the page set for Backup Device Properties: General
// and Media Contents, both read-only, as SSMS's own dialog is.
//
// Nothing here writes, by the object's shape: sp_addumpdevice and sp_dropdevice
// are a backup device's whole write surface, so its name, type and path are fixed
// at creation. Changing one means dropping and re-adding the device, which is
// Delete's job. Neither page declares requires for that reason — both are named
// in prop_page_requires_test.go's pagesThatOnlyRead.
func backupDevicePropPages(sc *db.ServerConn, devName string) []propPage {
	return []propPage{
		pageBackupDeviceGeneral(sc, devName),
		pageBackupDeviceMediaContents(sc, devName),
	}
}

// pageBackupDeviceGeneral is Backup Device Properties > General.
func pageBackupDeviceGeneral(sc *db.ServerConn, devName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.BackupDeviceByName(ctx, devName)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Device"),
				propsheet.Static("Device name", d.Name),
				propsheet.Static("Type", d.Type),
				propsheet.Static("Destination", d.PhysicalName),
				propsheet.Note("A backup device's name, type and path are fixed when it is created — SQL Server has no ALTER for one. To change any of them, delete the device and add it again."),
			)
			return f, nil, nil
		},
	}
}

// pageBackupDeviceMediaContents is Backup Device Properties > Media Contents: the
// backup sets the device holds, as RESTORE HEADERONLY reports them.
//
// The read is addressed to the *logical device*, not its physical path (what
// gosmo's BackupTarget is for); reading by path would go behind the alias the page
// is about.
//
// A media read that fails is shown on the page rather than failing it: the file
// may be gone, the tape offline, the share unreachable, and none of that makes the
// device itself unreadable. The General page still has everything the catalog
// knows.
func pageBackupDeviceMediaContents(sc *db.ServerConn, devName string) propPage {
	return propPage{
		title: "Media Contents",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.BackupDeviceByName(ctx, devName)
			if err != nil {
				return nil, nil, err
			}

			headers, readErr := d.Headers(ctx)
			if readErr != nil {
				return propsheet.NewForm(
					propsheet.Section("Media contents"),
					propsheet.Static("Media", d.PhysicalName),
					propsheet.Note("The media could not be read: "+readErr.Error()),
				), nil, nil
			}

			cols := []string{"Position", "Name", "Type", "Database", "Server", "Backup Finished", "Size"}
			rows := make([][]string, len(headers))
			for i, h := range headers {
				rows[i] = []string{
					strconv.Itoa(h.Position),
					h.BackupName,
					backupSetTypeName(h.SetType),
					h.DatabaseName,
					h.ServerName,
					formatSQLDate(h.BackupFinish),
					formatBytes(h.BackupSize),
				}
			}
			grid := controls.NewDataGrid()
			grid.SetData(cols, rows)

			f := propsheet.NewForm(
				propsheet.Section("Media contents"),
				propsheet.Static("Media", d.PhysicalName),
				propsheet.Static("Backup sets", strconv.Itoa(len(headers))),
				propsheet.NewGridRow(grid, 12),
			)
			return f, nil, nil
		},
	}
}

// backupSetTypeName names a backup set's type the way SSMS's Media Contents
// grid does.
func backupSetTypeName(t gosmo.BackupSetType) string {
	switch t {
	case gosmo.BackupSetDatabase:
		return "Database"
	case gosmo.BackupSetLog:
		return "Transaction Log"
	case gosmo.BackupSetDifferential:
		return "Differential"
	case gosmo.BackupSetFile:
		return "File or Filegroup"
	case gosmo.BackupSetDifferentialFile:
		return "Differential File"
	case gosmo.BackupSetPartial:
		return "Partial"
	case gosmo.BackupSetDifferentialPartial:
		return "Differential Partial"
	default:
		return "Unknown"
	}
}
