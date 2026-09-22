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

// certificate_backup_dialog.go is Back Up Certificate (a certificate's context
// menu), built on newObjectDialog for its OK / Script Changes pipeline: BACKUP
// CERTIFICATE to a file on the server, with the private key to a second file
// when one is named.
//
// Rights, probed on 17 (2026-09-22): the public certificate backs up for
// anyone who can see the certificate, db_securityadmin included; the private
// key needs CONTROL on it (Msg 15247 otherwise), which the server is left to
// say. Both files are written by the SQL Server service account, which must be
// able to write the directory (Msg 15240 otherwise, as the 2016 instance on
// the test host could not write C:\temp), and they come out readable by that
// account alone — nothing but an administrator on the host can delete them.

// certBackupPrefetch is what the dialog reads before it opens.
type certBackupPrefetch struct {
	cert       *gosmo.Certificate
	defaultDir string
}

// BackupCertificateDialog is the Back Up Certificate dialog.
type BackupCertificateDialog struct {
	newObjectDialog[certBackupPrefetch]

	// dbName, certName and node are set by show, before the embedded
	// dialog's own show runs the prefetch that reads them.
	dbName, certName string
	node             *explorerNode
}

// NewBackupCertificateDialog creates the dialog and wires its callbacks.
func NewBackupCertificateDialog(app *App) *BackupCertificateDialog {
	d := &BackupCertificateDialog{}
	d.init(app, newObjectConfig[certBackupPrefetch]{
		title: "Back Up Certificate",
		noun:  "Certificate",
		verb:  "backed up",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*certBackupPrefetch, error) {
			c, err := findCertificate(ctx, sc, d.dbName, d.certName)
			if err != nil {
				return nil, err
			}
			return &certBackupPrefetch{cert: c, defaultDir: sc.Server.Info().DefaultBackupPath}, nil
		},
		build: d.buildPages,
		// The Details pane shows when the private key was last backed up.
		refresh: func(*db.ServerConn) { d.app.detailBrowser.Invalidate(d.app, d.node) },
	})
	return d
}

// show opens the dialog for one certificate node.
func (d *BackupCertificateDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName, d.certName = node.data.DBName, node.data.Name
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Certificate: "+d.certName, "Database: "+d.dbName)
}

func (d *BackupCertificateDialog) buildPages(pf *certBackupPrefetch) {
	c := pf.cert
	fileField := propsheet.Text("Certificate file", backupFilePath(pf.defaultDir, c.Name, ".cer"), 45)
	keyFileField := propsheet.Text("Private key file", "", 45)
	encPass := propsheet.Password("Encryption password", 20)
	encConfirm := propsheet.Password("Confirm password", 20)
	decPass := propsheet.Password("Decryption password", 20)

	rows := []propsheet.Row{
		propsheet.Section("Certificate"),
		propsheet.Static("Name", c.Name),
		propsheet.Static("Private key", privateKeyText(c.PvtKeyEncryptionType)),
		propsheet.Section("Destination"),
		fileField,
		propsheet.Buttons(widgets.NewButton("Browse...", func() {
			d.browse("Certificate File", fileField, backupFilePath(pf.defaultDir, c.Name, ".cer"))
		})),
	}
	if c.HasPrivateKey() {
		rows = append(rows,
			propsheet.Section("Private key"),
			keyFileField,
			propsheet.Buttons(widgets.NewButton("Browse...", func() {
				d.browse("Private Key File", keyFileField, backupFilePath(pf.defaultDir, c.Name, ".pvk"))
			})),
			encPass, encConfirm,
		)
		if c.PvtKeyEncryptionType == "ENCRYPTED_BY_PASSWORD" {
			rows = append(rows, decPass,
				propsheet.Note("The private key is protected by a password: type it in Decryption password to export it."))
		}
		rows = append(rows, propsheet.Note("Leave Private key file blank to back up the public certificate only. "+
			"The private key file is encrypted by Encryption password, which restoring it needs. Exporting it needs CONTROL on the certificate."))
	}
	rows = append(rows, propsheet.Note("Paths are on the SQL Server host, written by its service account. "+
		"The server does not overwrite an existing file, and the files it writes are readable by its service account alone."))
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return c.Name }
	d.preflight = func() error {
		return validateCertificateBackup(strings.TrimSpace(fileField.Value()), strings.TrimSpace(keyFileField.Value()),
			encPass.Value(), encConfirm.Value(), c.PvtKeyEncryptionType == "ENCRYPTED_BY_PASSWORD" && decPass.Value() == "")
	}
	d.applyFns[0] = func(ctx context.Context) error {
		spec := gosmo.CertificateBackupSpec{File: strings.TrimSpace(fileField.Value())}
		if f := strings.TrimSpace(keyFileField.Value()); f != "" {
			spec.PrivateKeyFile = f
			spec.EncryptionPassword = scriptSafePassword(ctx, encPass.Value())
			spec.DecryptionPassword = scriptSafePassword(ctx, decPass.Value())
		}
		return d.sc.Server.DatabaseRef(d.dbName).CertificateRef(c.Name).Backup(ctx, spec)
	}
}

// validateCertificateBackup checks the fields before anything is sent.
// needsDecryption is a password-protected private key whose password was not
// typed: the server cannot open it to export it.
func validateCertificateBackup(file, keyFile, encPass, encConfirm string, needsDecryption bool) error {
	switch {
	case file == "":
		return fmt.Errorf("certificate file is required")
	case keyFile == "":
		return nil
	case keyFile == file:
		return fmt.Errorf("the private key needs a file of its own")
	case encPass == "":
		return fmt.Errorf("type a password to encrypt the private key file with")
	case encPass != encConfirm:
		return fmt.Errorf("passwords do not match")
	case needsDecryption:
		return fmt.Errorf("type the private key's password in Decryption password to export it")
	}
	return nil
}

// browse picks a file on the *server's* filesystem into field.
func (d *BackupCertificateDialog) browse(title string, field *propsheet.TextRow, fallback string) {
	fs, ok := newServerFS(d.sc)
	if !ok {
		d.SetMessage("Not connected — cannot browse the server's filesystem.", true)
		return
	}
	start := strings.TrimSpace(field.Value())
	if start == "" {
		start = fallback
	}
	d.app.fileDialog.ShowSaveOn(fs, title, start, func(path string) { field.SetValue(path) })
}

// backupFilePath joins the server's default backup directory and a name into
// a suggested file path, with the separator the server's own paths use —
// backupDevicePath's rule, with the extension given.
func backupFilePath(dir, name, ext string) string {
	if dir == "" || name == "" {
		return ""
	}
	sep := "\\"
	if strings.HasPrefix(dir, "/") {
		sep = "/"
	}
	return strings.TrimRight(dir, "\\/") + sep + name + ext
}

// showBackupCertificateDialog is the certificate node's entry point.
func (a *App) showBackupCertificateDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.backupCertificateDialog.show(sc, node)
}
