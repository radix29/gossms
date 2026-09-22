package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// master_key.go is the database-master-key precondition the dialogs that
// create key material share. A certificate or asymmetric key whose private key
// is to open without a password is protected by the database master key, and
// SQL Server refuses to create one in a database that has none (Msg 15581) —
// so New Database Mirroring Endpoint and the New Certificate / Asymmetric Key
// dialogs create the master key first when it is absent. New Symmetric Key
// does not: a symmetric key is encrypted by a certificate's or asymmetric
// key's public half, or by a password, and none of those needs the master key
// (probed 2026-09-22).
//
// This is the only place gossms creates one; the master key's own node
// (master_key_props.go) works on one that exists.

// ensureMasterKey creates d's database master key, protected by password, if
// it has none; a database that already has one is left alone and password is
// not used.
//
// The check is a read, so under Script Changes it still asks the real server
// and only the CREATE is collected — a script for a database that already has
// a master key does not try to make a second one.
func ensureMasterKey(ctx context.Context, d *gosmo.Database, password string) error {
	has, err := d.HasMasterKeyContext(ctx)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	return d.CreateMasterKeyContext(ctx, password)
}

// Private-key protection choices, in the order keyProtectionFields' radio
// lists them.
const (
	keyByMasterKey = iota
	keyByPassword
)

var keyProtections = []string{"Database master key", "Password"}

// keyProtectionFields is the "Private key" and "Master key" sections New
// Certificate and New Asymmetric Key share: a private key protected by the
// master key or by a password, and — when the database has no master key and
// the first is chosen — the password pair that creates one.
type keyProtectionFields struct {
	dbName       string
	hasMasterKey bool

	by                  *propsheet.RadioRow
	keyPass, keyConfirm *propsheet.TextRow
	mkPass, mkConfirm   *propsheet.TextRow
}

func newKeyProtectionFields(dbName string, hasMasterKey bool) *keyProtectionFields {
	k := &keyProtectionFields{
		dbName:       dbName,
		hasMasterKey: hasMasterKey,
		by:           propsheet.Radio("Private key encrypted by", keyProtections, keyByMasterKey),
		keyPass:      propsheet.Password("Password", 20),
		keyConfirm:   propsheet.Password("Confirm password", 20),
		mkPass:       propsheet.Password("Master key password", 20),
		mkConfirm:    propsheet.Password("Confirm master key password", 20),
	}
	// Only the fields the chosen protection uses can be typed into; the master
	// key's are dead altogether when the database already has one.
	sync := func() {
		byPassword := k.byPassword()
		k.keyPass.SetEnabled(byPassword)
		k.keyConfirm.SetEnabled(byPassword)
		k.mkPass.SetEnabled(!byPassword && !hasMasterKey)
		k.mkConfirm.SetEnabled(!byPassword && !hasMasterKey)
	}
	k.by.SetOnChange(func(int) { sync() })
	sync()
	return k
}

func (k *keyProtectionFields) byPassword() bool { return k.by.Selected() == keyByPassword }

// rows is the two sections, usage being the note under the private-key
// choice that says what each protection means for this kind of key.
func (k *keyProtectionFields) rows(usage string) []propsheet.Row {
	rows := []propsheet.Row{
		propsheet.Section("Private key"),
		k.by,
		k.keyPass, k.keyConfirm,
		propsheet.Note(usage),
	}
	if k.hasMasterKey {
		return append(rows, propsheet.Note(k.dbName+" has a database master key."))
	}
	return append(rows,
		propsheet.Section("Master key"),
		k.mkPass, k.mkConfirm,
		propsheet.Note(k.dbName+" has no database master key, so one is created first, protected by this password. The key is also protected by the service master key, so nothing has to type the password again — keep it anyway: it is the only way back in if the database is restored onto another instance."),
	)
}

// input gathers the fields for validateKeyProtection.
func (k *keyProtectionFields) input() keyProtectionInput {
	return keyProtectionInput{
		byPassword:   k.byPassword(),
		keyPassword:  k.keyPass.Value(),
		keyConfirm:   k.keyConfirm.Value(),
		hasMasterKey: k.hasMasterKey,
		mkPassword:   k.mkPass.Value(),
		mkConfirm:    k.mkConfirm.Value(),
		dbName:       k.dbName,
	}
}

// apply is the protection's share of the apply: it creates the master key
// when the master key is chosen and absent, and returns the private-key
// password — script-safe, and empty for master-key protection — for the
// CREATE's ENCRYPTION BY PASSWORD.
func (k *keyProtectionFields) apply(ctx context.Context, d *gosmo.Database) (string, error) {
	if k.byPassword() {
		return scriptSafePassword(ctx, k.keyPass.Value()), nil
	}
	return "", ensureMasterKey(ctx, d, scriptSafePassword(ctx, k.mkPass.Value()))
}

// keyProtectionInput is keyProtectionFields' values, apart from the form so
// validateKeyProtection is testable on its own.
type keyProtectionInput struct {
	byPassword              bool
	keyPassword, keyConfirm string
	hasMasterKey            bool
	mkPassword, mkConfirm   string
	dbName                  string
}

// validateKeyProtection refuses what the server would, naming the field — Msg
// 15581 for a missing master key password says nothing about the dialog it
// came from.
func validateKeyProtection(in keyProtectionInput) error {
	if in.byPassword {
		if in.keyPassword == "" {
			return fmt.Errorf("a password is required to protect the private key")
		}
		if in.keyPassword != in.keyConfirm {
			return fmt.Errorf("passwords do not match")
		}
		return nil
	}
	if !in.hasMasterKey {
		if in.mkPassword == "" {
			return fmt.Errorf("%s has no database master key — type a password for one, or protect the private key with a password instead", in.dbName)
		}
		if in.mkPassword != in.mkConfirm {
			return fmt.Errorf("master key passwords do not match")
		}
	}
	return nil
}
