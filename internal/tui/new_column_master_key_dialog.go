package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_column_master_key_dialog.go is the New Column Master Key creation
// dialog (a database's Security > Always Encrypted Keys > Column Master Keys
// folder). Built on newObjectDialog like every other create dialog; the one
// thing it adds is a database — the two Always Encrypted creates are
// database-scoped, where New Job/Login/Database are server-scoped.
//
// The key itself is never here: SQL Server stores only the provider name and
// the path into that provider's store, and the enclave signature is computed
// client-side from the master key's private key. gossms cannot reach a
// Windows certificate store or a Key Vault from a portable no-CGO build, so
// an enclave-enabled key is created by pasting the signature SSMS or the
// SqlColumnMasterKey cmdlets produced.

// cmkProviders are the key store providers SQL Server ships with, listed in
// the dialog's note. The field is free text rather than a dropdown because a
// custom provider name is legal — the server stores whatever it is given and
// only the client driver ever resolves it.
var cmkProviders = []string{
	"MSSQL_CERTIFICATE_STORE", "AZURE_KEY_VAULT",
	"MSSQL_CSP_PROVIDER", "MSSQL_CNG_STORE", "MSSQL_JAVA_KEYSTORE",
}

// ncmkPrefetch holds the one read this dialog needs: the existing key names,
// for the name-uniqueness preflight.
type ncmkPrefetch struct {
	existingNames map[string]bool
}

// NewColumnMasterKeyDialog is the New Column Master Key creation dialog.
type NewColumnMasterKeyDialog struct {
	newObjectDialog[ncmkPrefetch]

	// dbName is the database the key is created in, and node the folder to
	// refresh afterwards. Both are set by show, before the embedded dialog's
	// own show runs the prefetch that reads them.
	dbName string
	node   *explorerNode
}

// NewNewColumnMasterKeyDialog creates the dialog and wires its callbacks.
func NewNewColumnMasterKeyDialog(app *App) *NewColumnMasterKeyDialog {
	d := &NewColumnMasterKeyDialog{}
	d.init(app, newObjectConfig[ncmkPrefetch]{
		title:   "New Column Master Key",
		noun:    "Column master key",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	return d
}

// show opens the dialog for one database's Column Master Keys folder.
func (d *NewColumnMasterKeyDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	// Script Changes opens its query window in the database the statement
	// runs in, not the connection's default.
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewColumnMasterKeyDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*ncmkPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, d.dbName)
	if err != nil {
		return nil, err
	}
	keys, err := dbObj.ColumnMasterKeysContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(keys))
	for _, k := range keys {
		existing[strings.ToLower(k.Name)] = true
	}
	return &ncmkPrefetch{existingNames: existing}, nil
}

func (d *NewColumnMasterKeyDialog) buildPages(pf *ncmkPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Name", "", 30)
	providerField := propsheet.Text("Key store provider", cmkProviders[0], 30)
	pathField := propsheet.Text("Key path", "", 40)
	enclaveRow := propsheet.Check("Allow enclave computations", false)
	signatureField := propsheet.Text("Signature (hex)", "", 40)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Column master key"),
		nameField, providerField, pathField,
		propsheet.Note("Provider names SQL Server ships with: "+strings.Join(cmkProviders, ", ")+". The key path is that provider's own — e.g. CurrentUser/My/<thumbprint> for a certificate store."),
		propsheet.Section("Secure enclaves"),
		enclaveRow, signatureField,
		propsheet.Note("ENCLAVE_COMPUTATIONS takes a signature over this key's metadata, computed from the master key's private key. Only a client holding that key can produce it — create the key in SSMS or with the SqlColumnMasterKey cmdlets and paste the signature here."),
	)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("column master key name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a column master key named %q already exists in %s", name, dbName)
		}
		if strings.TrimSpace(providerField.Value()) == "" {
			return fmt.Errorf("key store provider is required")
		}
		if strings.TrimSpace(pathField.Value()) == "" {
			return fmt.Errorf("key path is required")
		}
		sig := strings.TrimSpace(signatureField.Value())
		if enclaveRow.Checked() {
			if sig == "" {
				return fmt.Errorf("a key that allows enclave computations needs its signature, which SSMS or the SqlColumnMasterKey cmdlets produce")
			}
			if _, err := parseHexBytes(sig); err != nil {
				return fmt.Errorf("signature: %w", err)
			}
			return nil
		}
		// A pasted signature with the box unticked is refused rather than
		// dropped: the statement would succeed and create a key that is not
		// the one the user was setting up.
		if sig != "" {
			return fmt.Errorf("a signature only applies to a key that allows enclave computations — tick that box or clear the signature")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// Server.Database, not DatabaseByName: the create reads nothing off
		// the handle and this one also works under Script Changes.
		dbObj := sc.Server.Database(dbName)
		name := d.objectName()
		provider := strings.TrimSpace(providerField.Value())
		path := strings.TrimSpace(pathField.Value())
		if !enclaveRow.Checked() {
			return dbObj.CreateColumnMasterKeyContext(ctx, name, provider, path, false)
		}
		sig, err := parseHexBytes(strings.TrimSpace(signatureField.Value()))
		if err != nil {
			return fmt.Errorf("signature: %w", err)
		}
		return dbObj.CreateColumnMasterKeyWithSignatureContext(ctx, name, provider, path, sig)
	}
}
