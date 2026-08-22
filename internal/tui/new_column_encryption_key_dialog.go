package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_column_encryption_key_dialog.go is the New Column Encryption Key
// creation dialog (a database's Security > Always Encrypted Keys > Column
// Encryption Keys folder). Database-scoped like New Column Master Key — see
// that file for why both carry a database the other create dialogs don't.
//
// The encrypted value is pasted, not generated: it is a random key encrypted
// under the column master key, which only a client that can reach that
// master key's store can produce. The dialog creates a key with one value;
// a second value is what a master-key rotation adds, and stays out.

// cekAlgorithm is the only encryption algorithm SQL Server accepts for a
// column encryption key value. Shown as a static row rather than a dropdown
// of one.
const cekAlgorithm = "RSA_OAEP"

// ncekPrefetch holds the two reads this dialog needs: the master keys to
// choose from, and the existing key names for the uniqueness preflight.
type ncekPrefetch struct {
	masterKeys    []string
	existingNames map[string]bool
}

// NewColumnEncryptionKeyDialog is the New Column Encryption Key creation
// dialog.
type NewColumnEncryptionKeyDialog struct {
	newObjectDialog[ncekPrefetch]

	dbName string
	node   *explorerNode
}

// NewNewColumnEncryptionKeyDialog creates the dialog and wires its callbacks.
func NewNewColumnEncryptionKeyDialog(app *App) *NewColumnEncryptionKeyDialog {
	d := &NewColumnEncryptionKeyDialog{}
	d.init(app, newObjectConfig[ncekPrefetch]{
		title:   "New Column Encryption Key",
		noun:    "Column encryption key",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	return d
}

// show opens the dialog for one database's Column Encryption Keys folder.
func (d *NewColumnEncryptionKeyDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewColumnEncryptionKeyDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*ncekPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, d.dbName)
	if err != nil {
		return nil, err
	}
	masters, err := dbObj.ColumnMasterKeysContext(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(masters))
	for i, m := range masters {
		names[i] = m.Name
	}
	keys, err := dbObj.ColumnEncryptionKeysContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(keys))
	for _, k := range keys {
		existing[strings.ToLower(k.Name)] = true
	}
	return &ncekPrefetch{masterKeys: names, existingNames: existing}, nil
}

func (d *NewColumnEncryptionKeyDialog) buildPages(pf *ncekPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Name", "", 30)
	masterRow := propsheet.Select("Column master key", pf.masterKeys, 0)
	valueField := propsheet.Text("Encrypted value (hex)", "", 40)

	rows := []propsheet.Row{
		propsheet.Section("Column encryption key"),
		nameField, masterRow,
		propsheet.Static("Algorithm", cekAlgorithm),
		propsheet.Section("Encrypted value"),
		valueField,
		propsheet.Note("The value is this key's material encrypted under the master key above, produced by SSMS or the SqlColumnEncryptionKey cmdlets. SQL Server stores it without checking it — a wrong value is only discovered when a client first decrypts a column."),
	}
	if len(pf.masterKeys) == 0 {
		// Without a master key there is nothing to encrypt the value under,
		// and the dropdown would be empty with no explanation.
		rows = append(rows,
			propsheet.Section("Note"),
			propsheet.Note("This database has no column master key yet. Create one first — a column encryption key is always encrypted under one."))
	}
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("column encryption key name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a column encryption key named %q already exists in %s", name, dbName)
		}
		if len(pf.masterKeys) == 0 {
			return fmt.Errorf("%s has no column master key — create one first", dbName)
		}
		value := strings.TrimSpace(valueField.Value())
		if value == "" {
			return fmt.Errorf("the encrypted value is required, and only a client holding the master key can produce it")
		}
		if _, err := parseHexBytes(value); err != nil {
			return fmt.Errorf("encrypted value: %w", err)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		value, err := parseHexBytes(strings.TrimSpace(valueField.Value()))
		if err != nil {
			return fmt.Errorf("encrypted value: %w", err)
		}
		// Server.Database, not DatabaseByName — see New Column Master Key.
		return sc.Server.Database(dbName).CreateColumnEncryptionKeyContext(ctx, d.objectName(),
			[]gosmo.ColumnEncryptionKeyValue{{
				MasterKeyName:       masterRow.Value(),
				EncryptionAlgorithm: cekAlgorithm,
				EncryptedValue:      value,
			}})
	}
}
