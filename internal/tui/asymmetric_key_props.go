package tui

import (
	"context"
	"errors"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// asymmetric_key_props.go is the Properties for an asymmetric key — the
// certificate's shape (certificate_props.go): General's one write is the
// owner, Signatures lists and edits the modules it signs, and Remove Private
// Key is an Object Explorer command. There is no BACKUP ASYMMETRIC KEY.

// findAsymmetricKey resolves name in dbName, rewording AsymmetricKeyByName's
// ErrNotFound for a key dropped since the tree was read — findCertificate's
// shape.
func findAsymmetricKey(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.AsymmetricKey, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	k, err := d.AsymmetricKeyByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, fmt.Errorf("asymmetric key %q no longer exists in %q", name, dbName)
	}
	return k, err
}

func asymmetricKeyPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	general := propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			k, err := findAsymmetricKey(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			owner, err := keyOwnerRow(ctx, sc, dbName, k.Owner, false)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Asymmetric key"),
				propsheet.Static("Name", k.Name),
				owner,
				propsheet.Static("Algorithm", k.Algorithm),
				propsheet.Static("Key length", keyLengthText(k.KeyLength)),
				propsheet.Static("Thumbprint", hexPreview(k.Thumbprint)),
				propsheet.Section("Private key"),
				propsheet.Static("Protection", privateKeyText(k.PvtKeyEncryptionType)),
				propsheet.Static("Provider", asymmetricKeyProviderText(k)),
				propsheet.Static("Attested by", k.AttestedBy),
				keyOwnerNote("asymmetric key"),
			)
			return f, keyOwnerApply(sc, dbName, owner, func(ctx context.Context, d *gosmo.Database, o string) error {
				return d.AsymmetricKeyRef(name).ChangeOwner(ctx, o)
			}), nil
		},
	}
	return []propPage{
		withRequiresOn(general, dbName, "", name, gate.ControlOnAsymmetricKey),
		asymmetricKeySignaturesPage(sc, dbName, name),
	}
}
