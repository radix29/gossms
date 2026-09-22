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

// master_key_dialogs.go is the database master key's two action dialogs —
// Back Up Master Key and Regenerate Master Key — built on newObjectDialog for
// its OK / Script Changes pipeline, as Back Up Certificate is. Both need
// CONTROL on the database (master_key_props.go), and both need the key's
// password when the service master key does not encrypt it.

// masterKeyPrefetch is what both dialogs read before they open.
type masterKeyPrefetch struct {
	key        *gosmo.MasterKey
	defaultDir string
}

func fetchMasterKeyPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*masterKeyPrefetch, error) {
	m, err := findMasterKey(ctx, sc, dbName)
	if err != nil {
		return nil, err
	}
	return &masterKeyPrefetch{key: m, defaultDir: sc.Server.Info().DefaultBackupPath}, nil
}

// masterKeyOpenRows are the rows asking for the key's password, when the
// service master key does not open it.
func masterKeyOpenRows(m *gosmo.MasterKey, openPass *propsheet.TextRow) []propsheet.Row {
	if m.EncryptedByServer {
		return nil
	}
	return []propsheet.Row{
		propsheet.Section("Open the key"),
		openPass,
		propsheet.Note("The service master key does not encrypt this key, so it is opened with one of its passwords first."),
	}
}

// masterKeyOpenPassword is the open password to pass gosmo, or an error when
// one is needed and missing.
func masterKeyOpenPassword(ctx context.Context, m *gosmo.MasterKey, openPass *propsheet.TextRow) (string, error) {
	if m.EncryptedByServer {
		return "", nil
	}
	if openPass.Value() == "" {
		return "", fmt.Errorf("type one of the master key's passwords in Master key password")
	}
	return scriptSafePassword(ctx, openPass.Value()), nil
}

// BackupMasterKeyDialog is Back Up Master Key.
type BackupMasterKeyDialog struct {
	newObjectDialog[masterKeyPrefetch]
	dbName string
	node   *explorerNode
}

// NewBackupMasterKeyDialog creates the dialog and wires its callbacks.
func NewBackupMasterKeyDialog(app *App) *BackupMasterKeyDialog {
	d := &BackupMasterKeyDialog{}
	d.init(app, newObjectConfig[masterKeyPrefetch]{
		title: "Back Up Master Key",
		noun:  "Master key of",
		verb:  "backed up",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*masterKeyPrefetch, error) {
			return fetchMasterKeyPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) {},
	})
	return d
}

func (d *BackupMasterKeyDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName, d.node = node.data.DBName, node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *BackupMasterKeyDialog) buildPages(pf *masterKeyPrefetch) {
	suggested := backupFilePath(pf.defaultDir, d.dbName+"_master_key", ".key")
	fileField := propsheet.Text("File", suggested, 45)
	encPass := propsheet.Password("Encryption password", 20)
	encConfirm := propsheet.Password("Confirm password", 20)
	openPass := propsheet.Password("Master key password", 20)

	rows := []propsheet.Row{
		propsheet.Section("Destination"),
		fileField,
		propsheet.Buttons(widgets.NewButton("Browse...", func() {
			fs, ok := newServerFS(d.sc)
			if !ok {
				d.SetMessage("Not connected — cannot browse the server's filesystem.", true)
				return
			}
			start := strings.TrimSpace(fileField.Value())
			if start == "" {
				start = suggested
			}
			d.app.fileDialog.ShowSaveOn(fs, "Master Key File", start, func(path string) { fileField.SetValue(path) })
		})),
		propsheet.Section("Protect the file with"),
		encPass, encConfirm,
		propsheet.Note("Restoring the key needs this password. The path is on the SQL Server host, written by its service account, " +
			"and the file comes out readable by that account alone."),
	}
	rows = append(rows, masterKeyOpenRows(pf.key, openPass)...)
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return d.dbName }
	d.preflight = func() error {
		switch {
		case strings.TrimSpace(fileField.Value()) == "":
			return fmt.Errorf("file is required")
		case encPass.Value() == "":
			return fmt.Errorf("type a password to encrypt the file with")
		case encPass.Value() != encConfirm.Value():
			return fmt.Errorf("passwords do not match")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		open, err := masterKeyOpenPassword(ctx, pf.key, openPass)
		if err != nil {
			return err
		}
		return d.sc.Server.DatabaseRef(d.dbName).MasterKeyRef().BackupContext(ctx,
			strings.TrimSpace(fileField.Value()), scriptSafePassword(ctx, encPass.Value()), open)
	}
}

// RegenerateMasterKeyDialog is Regenerate Master Key.
type RegenerateMasterKeyDialog struct {
	newObjectDialog[masterKeyPrefetch]
	dbName string
	node   *explorerNode
}

// NewRegenerateMasterKeyDialog creates the dialog and wires its callbacks.
func NewRegenerateMasterKeyDialog(app *App) *RegenerateMasterKeyDialog {
	d := &RegenerateMasterKeyDialog{}
	d.init(app, newObjectConfig[masterKeyPrefetch]{
		title: "Regenerate Master Key",
		noun:  "Master key of",
		verb:  "regenerated",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*masterKeyPrefetch, error) {
			return fetchMasterKeyPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.detailBrowser.Invalidate(d.app, d.node) },
	})
	return d
}

func (d *RegenerateMasterKeyDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName, d.node = node.data.DBName, node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *RegenerateMasterKeyDialog) buildPages(pf *masterKeyPrefetch) {
	newPass := propsheet.Password("New password", 20)
	confirm := propsheet.Password("Confirm password", 20)
	force := propsheet.Check("Force (lose what cannot be decrypted)", false)
	openPass := propsheet.Password("Master key password", 20)

	rows := []propsheet.Row{
		propsheet.Section("Regenerate"),
		newPass, confirm,
		force,
		propsheet.Note("Regenerating replaces the key material, decrypting and re-encrypting everything the master key protects. " +
			"It leaves the key encrypted by the new password and, where it was before, the service master key; the other " +
			"passwords are dropped. Force goes ahead even when something cannot be decrypted, losing it — use it only to recover a damaged key."),
	}
	rows = append(rows, masterKeyOpenRows(pf.key, openPass)...)
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return d.dbName }
	d.preflight = func() error {
		switch {
		case newPass.Value() == "":
			return fmt.Errorf("type the new password")
		case newPass.Value() != confirm.Value():
			return fmt.Errorf("passwords do not match")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		open, err := masterKeyOpenPassword(ctx, pf.key, openPass)
		if err != nil {
			return err
		}
		return d.sc.Server.DatabaseRef(d.dbName).MasterKeyRef().RegenerateContext(ctx,
			scriptSafePassword(ctx, newPass.Value()), force.Checked(), open)
	}
}

// showBackupMasterKeyDialog and showRegenerateMasterKeyDialog are the master
// key node's entry points.
func (a *App) showBackupMasterKeyDialog(sc *db.ServerConn, node *explorerNode) {
	if a.requireConn(sc) {
		a.backupMasterKeyDialog.show(sc, node)
	}
}

func (a *App) showRegenerateMasterKeyDialog(sc *db.ServerConn, node *explorerNode) {
	if a.requireConn(sc) {
		a.regenerateMasterKeyDialog.show(sc, node)
	}
}
