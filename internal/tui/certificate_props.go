package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// certificate_props.go is the Properties for a certificate. General's one
// write is the owner (key_actions.go has the rights and the permission loss);
// Signatures lists and edits the modules it signs (key_signatures_page.go).
//
// Nothing else on the certificate is settable. There is no rename, and every
// other ALTER CERTIFICATE is a private-key operation: Remove Private Key and
// Back Up Certificate are Object Explorer commands (key_actions.go,
// certificate_backup_dialog.go), and re-protecting the key reads the server's
// filesystem. ACTIVE FOR BEGIN_DIALOG is the one flag, and is left to a
// scripted ALTER.

// findCertificate resolves name in dbName, rewording CertificateByName's
// ErrNotFound for a certificate dropped since the tree was read.
func findCertificate(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.Certificate, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	c, err := d.CertificateByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, fmt.Errorf("certificate %q no longer exists in %q", name, dbName)
	}
	return c, err
}

func certificatePropPages(sc *db.ServerConn, dbName, name string) []propPage {
	general := propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := findCertificate(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			owner, err := keyOwnerRow(ctx, sc, dbName, c.Owner, false)
			if err != nil {
				return nil, nil, err
			}
			backup := formatSQLDate(c.PvtKeyLastBackupDate)
			if backup == "" {
				backup = "Never"
			}
			// Read, never assumed: a generated certificate is 2048 bits on
			// 2016 and 3072 on 2025.
			keyLength := keyLengthText(c.KeyLength)
			f := propsheet.NewForm(
				propsheet.Section("Certificate"),
				propsheet.Static("Name", c.Name),
				owner,
				propsheet.Static("Subject", c.Subject),
				propsheet.Static("Issuer", c.IssuerName),
				propsheet.Static("Serial number", c.SerialNumber),
				propsheet.Static("Thumbprint", hexPreview(c.Thumbprint)),
				propsheet.Static("Key length", keyLength),
				propsheet.Section("Validity"),
				propsheet.Static("Valid from", formatSQLDate(c.StartDate)),
				propsheet.Static("Expiry date", formatSQLDate(c.ExpiryDate)),
				propsheet.Static("Expired", boolStr(certificateExpired(c, time.Now()))),
				propsheet.Section("Private key"),
				propsheet.Static("Protection", privateKeyText(c.PvtKeyEncryptionType)),
				propsheet.Static("Last backed up", backup),
				propsheet.Section("Usage"),
				propsheet.Static("Active for BEGIN_DIALOG", boolStr(c.IsActiveForBeginDialog)),
				propsheet.Static("Attested by", c.AttestedBy),
				keyOwnerNote("certificate"),
			)
			return f, keyOwnerApply(sc, dbName, owner, func(ctx context.Context, d *gosmo.Database, o string) error {
				return d.CertificateRef(name).ChangeOwner(ctx, o)
			}), nil
		},
	}
	return []propPage{
		withRequiresOn(general, dbName, "", name, gate.ControlOnCertificate),
		certificateSignaturesPage(sc, dbName, name),
	}
}
