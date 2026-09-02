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

// new_backup_device_dialog.go is the New Backup Device creation dialog (Object
// Explorer's Server Objects > Backup Devices folder, "New Backup Device..."),
// built on newObjectDialog like every other New-X.
//
// Disk only, as SSMS's own dialog is: sp_addumpdevice still takes a tape
// device, but tape backup is gone from every supported version, so a tape
// device is listed and dropped here and never created.

// nbackupDevicePrefetch holds what the dialog needs before it opens: the
// existing device names for the uniqueness preflight, and the server's default
// backup directory to seed the path with.
type nbackupDevicePrefetch struct {
	existingNames map[string]bool
	defaultDir    string
}

func fetchNewBackupDevicePrefetch(ctx context.Context, sc *db.ServerConn) (*nbackupDevicePrefetch, error) {
	devices, err := sc.Server.BackupDevicesContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(devices))
	for _, d := range devices {
		existing[strings.ToLower(d.Name)] = true
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
		refresh: func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeBackupDevices) },
	})
	return d
}

func (d *NewBackupDeviceDialog) buildPages(pf *nbackupDevicePrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Device name", "", 30)
	fileField := propsheet.Text("File", "", 45)

	// The path is filled in as the name is typed, the way SSMS does it, until
	// the user edits the path themselves — after which their value stands.
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
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a backup device named %q already exists", name)
		}
		if strings.TrimSpace(fileField.Value()) == "" {
			return fmt.Errorf("file path is required")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		_, err := sc.Server.CreateBackupDeviceContext(ctx, d.objectName(),
			gosmo.BackupDeviceDisk, strings.TrimSpace(fileField.Value()))
		return err
	}
}

// browseFile picks the device's path off the *server's* filesystem: the path
// is resolved on the SQL Server host, so one picked from the client's own
// disks names a directory the server cannot write.
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
// name into a suggested file path, with the separator the *server's* paths
// use — a Linux client naming a file for a Windows instance still joins with
// a backslash.
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
