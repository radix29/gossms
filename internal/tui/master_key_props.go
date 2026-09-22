package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// master_key_props.go is the database master key's node: its Properties
// (General, read-only; Encryption, which adds and drops the service master
// key's encryption and password encryptions) and its Details view. The node
// is the first child of the Symmetric Keys folder, and only when the key
// exists — docs/decisions.md § Keys and certificates.
//
// Every write on the master key needs CONTROL on the database; ALTER on it,
// ALTER ANY SYMMETRIC KEY and CONTROL on everything else are refused (Msg
// 15151, "Cannot find the symmetric key 'master key'"), probed on 13 and 17
// (2026-09-22). A master key the service master key no longer encrypts has to
// be opened by password for each of them (Msg 15581 without), which the
// Encryption page and the two dialogs ask for.

// masterKeyNodeLabel is the master key's tree label.
const masterKeyNodeLabel = "Database Master Key"

// masterKeyRights gate every master key write.
var masterKeyRights = []gate.Right{gate.ControlDB}

// findMasterKey reads the master key of dbName, or fails when there is none
// the caller can see — MasterKey's (nil, nil).
func findMasterKey(ctx context.Context, sc *db.ServerConn, dbName string) (*gosmo.MasterKey, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	m, err := d.MasterKey(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%s has no database master key, or it is not visible to this login", dbName)
	}
	return m, nil
}

// masterKeyEncryptionText names one of the master key's encryptions. Its
// "ENCRYPTION BY MASTER KEY" row is the *service* master key's.
func masterKeyEncryptionText(e gosmo.SymmetricKeyEncryption) string {
	switch e.Kind {
	case gosmo.SymmetricKeyByMasterKey:
		return "Service master key"
	case gosmo.SymmetricKeyByPassword:
		return "Password"
	}
	return e.CryptTypeDesc
}

// masterKeyPasswordCount is how many password encryptions the key has.
func masterKeyPasswordCount(m *gosmo.MasterKey) int {
	n := 0
	for _, e := range m.Encryptions {
		if e.Kind == gosmo.SymmetricKeyByPassword {
			n++
		}
	}
	return n
}

// masterKeyDetail is the master key's Property/Value view.
func masterKeyDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	m, err := findMasterKey(ctx, sc, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	kv := []string{
		"Name", masterKeyNodeLabel,
		"Database", node.data.DBName,
		"Algorithm", m.Algorithm,
		"Key length", keyLengthText(m.KeyLength),
		"Key GUID", m.KeyGUID,
		"Created", formatSQLDate(m.CreateDate),
		"Modified", formatSQLDate(m.ModifyDate),
		"Encrypted by service master key", yesNo(m.EncryptedByServer),
	}
	for _, e := range m.Encryptions {
		kv = append(kv, "Encrypted by", masterKeyEncryptionText(e))
	}
	return propertyRows(kv...)
}

func masterKeyPropPages(sc *db.ServerConn, dbName string) []propPage {
	general := propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			m, err := findMasterKey(ctx, sc, dbName)
			if err != nil {
				return nil, nil, err
			}
			return propsheet.NewForm(
				propsheet.Section("Database master key"),
				propsheet.Static("Database", dbName),
				propsheet.Static("Algorithm", m.Algorithm),
				propsheet.Static("Key length", keyLengthText(m.KeyLength)),
				propsheet.Static("Key GUID", m.KeyGUID),
				propsheet.Static("Created", formatSQLDate(m.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(m.ModifyDate)),
				propsheet.Note("The master key protects the private keys of the database's certificates and asymmetric keys "+
					"that are not protected by a password of their own. Regenerate and Back Up are on its context menu."),
			), nil, nil
		},
	}
	encryption := propPage{
		title: "Encryption",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			m, err := findMasterKey(ctx, sc, dbName)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildMasterKeyEncryptionForm(m, sc.Server.DatabaseRef(dbName).MasterKeyRef())
			return f, apply, nil
		},
	}
	return []propPage{general, withRequires(encryption, dbName, masterKeyRights...)}
}

// buildMasterKeyEncryptionForm is the master key's Encryption page. A
// password encryption is removed by typing it — the server finds it by value
// (Msg 15313 on a miss) — and the last one cannot be removed (Msg 15558).
//
// The apply adds before it drops, and drops the service master key's
// encryption last: until then SQL Server opens the key on its own, and after
// it every statement needs the key's password.
func buildMasterKeyEncryptionForm(m *gosmo.MasterKey, ref *gosmo.MasterKey) (*propsheet.Form, propApply) {
	smk := propsheet.Check("Encrypted by the service master key", m.EncryptedByServer)
	addPass := propsheet.Password("Add password", 20)
	addConfirm := propsheet.Password("Confirm password", 20)
	dropPass := propsheet.Password("Remove password", 20)
	openPass := propsheet.Password("Master key password", 20)
	// The confirmation and the key's own password change nothing by
	// themselves; only the fields that name a change make the page dirty.
	addConfirm.SetDirtyTracked(false)
	openPass.SetDirtyTracked(false)

	rows := []propsheet.Row{
		propsheet.Section("Service master key"),
		smk,
		propsheet.Note("While the service master key encrypts it, SQL Server opens the master key on its own. Without it, " +
			"every use of a key or certificate the master key protects needs OPEN MASTER KEY with a password first."),
		propsheet.Section("Passwords"),
		propsheet.Static("Password encryptions", strconv.Itoa(masterKeyPasswordCount(m))),
		addPass, addConfirm,
		dropPass,
		propsheet.Note("A password is removed by typing it: the server finds the encryption by its value. The last password cannot be removed."),
	}
	if !m.EncryptedByServer {
		rows = append(rows,
			propsheet.Section("Open the key"),
			openPass,
			propsheet.Note("The service master key does not encrypt this key, so every change here opens it with one of its passwords first."))
	}
	f := propsheet.NewForm(rows...)

	apply := func(ctx context.Context) error {
		open := ""
		if !m.EncryptedByServer {
			if openPass.Value() == "" {
				return fmt.Errorf("type one of the master key's passwords in Master key password")
			}
			open = scriptSafePassword(ctx, openPass.Value())
		}
		if v := addPass.Value(); v != "" {
			if v != addConfirm.Value() {
				return fmt.Errorf("the password to add and its confirmation do not match")
			}
			if err := ref.AddEncryption(ctx, gosmo.MasterKeyEncryptor{Password: scriptSafePassword(ctx, v)}, open); err != nil {
				return err
			}
		}
		if smk.Dirty() && smk.Checked() {
			if err := ref.AddEncryption(ctx, gosmo.MasterKeyEncryptor{ServiceMasterKey: true}, open); err != nil {
				return err
			}
		}
		if v := dropPass.Value(); v != "" {
			if err := ref.DropEncryption(ctx, gosmo.MasterKeyEncryptor{Password: scriptSafePassword(ctx, v)}, open); err != nil {
				return err
			}
		}
		if smk.Dirty() && !smk.Checked() {
			if err := ref.DropEncryption(ctx, gosmo.MasterKeyEncryptor{ServiceMasterKey: true}, open); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}
