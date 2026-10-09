package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// new_backup_device_dialog.go is the New Backup Device dialog (Object
// Explorer's Server Objects > Backup Devices folder), built on newObjectDialog.
//
// Disk only, as SSMS's is: sp_addumpdevice still takes a tape device, but tape
// backup is gone from every supported version, so a tape device is listed and
// dropped here and never created.

// nbackupDevicePrefetch holds what the dialog needs before it opens: existing
// device names for the uniqueness preflight, and the server's default backup
// directory to seed the path.
type nbackupDevicePrefetch struct {
	existingNames *nameSet
	defaultDir    string
}

func fetchNewBackupDevicePrefetch(ctx context.Context, sc *db.ServerConn) (*nbackupDevicePrefetch, error) {
	devices, err := sc.Server.BackupDevices(ctx)
	if err != nil {
		return nil, err
	}
	existing := newNameSet(serverCollation(sc))
	for _, d := range devices {
		existing.Add(d.Name)
	}
	return &nbackupDevicePrefetch{
		existingNames: existing,
		defaultDir:    sc.Server.Info().DefaultBackupPath,
	}, nil
}

// NewBackupDeviceDialog is the New Backup Device creation dialog.
type NewBackupDeviceDialog struct {
	newObjectDialog[nbackupDevicePrefetch]
}

// NewNewBackupDeviceDialog creates the dialog and wires its callbacks.
func NewNewBackupDeviceDialog(app *App) *NewBackupDeviceDialog {
	d := &NewBackupDeviceDialog{}
	d.init(app, newObjectConfig[nbackupDevicePrefetch]{
		title:   "New Backup Device",
		noun:    "Backup Device",
		pages:   []string{"General"},
		fetch:   fetchNewBackupDevicePrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.ReloadFolders(sc, folderOf("", NodeBackupDevices)) },
	})
	return d
}

func (d *NewBackupDeviceDialog) buildPages(pf *nbackupDevicePrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Device name", "", 30)
	fileField := propsheet.Text("File", "", 45)

	// The path follows the name as typed, as in SSMS, until the user edits the path
	// themselves.
	pathEdited := false
	fileField.SetOnChange(func(string) { pathEdited = true })
	nameField.SetOnChange(func(v string) {
		if pathEdited || pf.defaultDir == "" {
			return
		}
		fileField.SetValue(backupDevicePath(pf.defaultDir, strings.TrimSpace(v)))
	})

	browseBtn := widgets.NewButton("Browse...", func() { d.browseFile(fileField, pf) })

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Device"),
		nameField,
		propsheet.Section("Destination"),
		fileField,
		propsheet.Buttons(browseBtn),
		propsheet.Note("The path is resolved on the SQL Server host, not on this machine, and the service account must be able to write it. A device's name and path are fixed once it exists — there is no ALTER for one."),
	)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("device name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a backup device named %q already exists", name)
		}
		if strings.TrimSpace(fileField.Value()) == "" {
			return fmt.Errorf("file path is required")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		_, err := sc.Server.CreateBackupDevice(ctx, gosmo.CreateBackupDeviceRequest{
			Name: d.objectName(), Type: gosmo.BackupDeviceDisk, PhysicalName: strings.TrimSpace(fileField.Value()),
		})
		return err
	}
}

// browseFile picks the device's path off the *server's* filesystem: the path
// resolves on the SQL Server host, so one from the client's disks names a
// directory the server cannot write.
func (d *NewBackupDeviceDialog) browseFile(fileField *propsheet.TextRow, pf *nbackupDevicePrefetch) {
	fs, ok := newServerFS(d.sc)
	if !ok {
		d.SetMessage("Not connected — cannot browse the server's filesystem.", true)
		return
	}
	start := strings.TrimSpace(fileField.Value())
	if start == "" {
		start = backupDevicePath(pf.defaultDir, d.objectName())
	}
	d.app.fileDialog.ShowSaveOn(fs, "Backup Device File", start, func(path string) {
		fileField.SetValue(path)
	})
}

// backupDevicePath joins the server's default backup directory and a device
// name into a suggested path with the separator the *server's* paths use (a
// Linux client naming a file for a Windows instance still joins with a
// backslash).
func backupDevicePath(dir, name string) string {
	if dir == "" || name == "" {
		return ""
	}
	sep := "\\"
	if strings.HasPrefix(dir, "/") {
		sep = "/"
	}
	return strings.TrimRight(dir, "\\/") + sep + name + ".bak"
}
