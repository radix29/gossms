package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_asymmetric_key_dialog.go is the New Asymmetric Key dialog (a database's
// Security > Asymmetric Keys folder), built on newObjectDialog — New
// Certificate's shape, with an algorithm in place of the subject and dates.
//
// A generated key (WITH ALGORITHM), or one an EKM provider holds (FROM
// PROVIDER, offered only on an instance with a provider — providerKeyFields).
// FROM FILE, EXECUTABLE FILE and ASSEMBLY read the server's own filesystem,
// and are left to a query window.
//
// The private key is protected by the database master key or by a password,
// through keyProtectionFields — the master key created first when the database
// has none.

// asymKeyAlgorithms is what the dialog offers, strongest last. RSA_512 and
// RSA_1024 are a syntax error (Msg 102) at compatibility level 130 and above,
// which every supported version defaults to, so they are not offered even
// though gosmo accepts them for a database at a lowered level.
var asymKeyAlgorithms = []gosmo.AsymmetricKeyAlgorithm{
	gosmo.AsymmetricKeyRSA2048, gosmo.AsymmetricKeyRSA3072, gosmo.AsymmetricKeyRSA4096,
}

// nasymPrefetch is what the dialog reads before it opens.
type nasymPrefetch struct {
	existingNames map[string]bool
	hasMasterKey  bool
	providers     []*gosmo.CryptographicProvider
}

func fetchNewAsymmetricKeyPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nasymPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	keys, err := dbObj.AsymmetricKeys(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(keys))
	for _, k := range keys {
		existing[strings.ToLower(k.Name)] = true
	}
	has, err := dbObj.HasMasterKey(ctx)
	if err != nil {
		return nil, err
	}
	return &nasymPrefetch{existingNames: existing, hasMasterKey: has, providers: readProviders(ctx, sc)}, nil
}

// NewAsymmetricKeyDialog is the New Asymmetric Key dialog.
type NewAsymmetricKeyDialog struct {
	newObjectDialog[nasymPrefetch]

	// dbName and node are set by show, before the embedded dialog's own show
	// runs the prefetch that reads them — NewCertificateDialog's arrangement.
	dbName string
	node   *explorerNode
}

// NewNewAsymmetricKeyDialog creates the dialog and wires its callbacks.
func NewNewAsymmetricKeyDialog(app *App) *NewAsymmetricKeyDialog {
	d := &NewAsymmetricKeyDialog{}
	d.init(app, newObjectConfig[nasymPrefetch]{
		title: "New Asymmetric Key",
		noun:  "Asymmetric Key",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nasymPrefetch, error) {
			return fetchNewAsymmetricKeyPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

// show opens the dialog for one database's Asymmetric Keys folder.
func (d *NewAsymmetricKeyDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewAsymmetricKeyDialog) buildPages(pf *nasymPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Key name", "", 30)
	algNames := make([]string, len(asymKeyAlgorithms))
	for i, a := range asymKeyAlgorithms {
		algNames[i] = string(a)
	}
	algRow := propsheet.Radio("Algorithm", algNames, 0)
	protection := newKeyProtectionFields(dbName, pf.hasMasterKey)

	rows := []propsheet.Row{
		propsheet.Section("Asymmetric key"),
		nameField,
		propsheet.Static("Database", dbName),
		algRow,
	}
	rows = append(rows, protection.rows("A key that has to be usable without anyone typing a password — a login or user mapped to it, a signed module — needs the master key. A password-protected private key is opened with that password each time it is used.")...)
	provider := newProviderKeyFields(pf.providers)
	rows = append(rows, provider.rows("the Private key section")...)
	d.forms[0] = propsheet.NewForm(rows...)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		if provider.spec() != nil {
			if err := validateNewAsymmetricKeyName(d.objectName(), pf.existingNames, dbName); err != nil {
				return err
			}
			return provider.validate()
		}
		return validateNewAsymmetricKey(d.objectName(), pf.existingNames, protection.input())
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// DatabaseRef, not DatabaseByName: every statement here addresses the
		// database by name, and the by-name read would not work under Script
		// Changes.
		dbObj := sc.Server.DatabaseRef(dbName)
		spec := gosmo.AsymmetricKeySpec{
			Name:      d.objectName(),
			Algorithm: asymKeyAlgorithms[algRow.Selected()],
		}
		if p := provider.spec(); p != nil {
			// The provider protects the key: no password, and no master key
			// to create first.
			spec.FromProvider = p
			if p.Disposition == gosmo.ProviderOpenExisting {
				spec.Algorithm = ""
			}
			return dbObj.CreateAsymmetricKey(ctx, spec)
		}
		var err error
		if spec.EncryptionPassword, err = protection.apply(ctx, dbObj); err != nil {
			return err
		}
		return dbObj.CreateAsymmetricKey(ctx, spec)
	}
}

// validateNewAsymmetricKey refuses a missing or taken name before the
// protection's own checks.
func validateNewAsymmetricKey(name string, existingNames map[string]bool, protection keyProtectionInput) error {
	if err := validateNewAsymmetricKeyName(name, existingNames, protection.dbName); err != nil {
		return err
	}
	return validateKeyProtection(protection)
}

// validateNewAsymmetricKeyName is the name half of validateNewAsymmetricKey,
// all a provider key needs.
func validateNewAsymmetricKeyName(name string, existingNames map[string]bool, dbName string) error {
	if name == "" {
		return fmt.Errorf("key name is required")
	}
	if existingNames[strings.ToLower(name)] {
		return fmt.Errorf("an asymmetric key named %q already exists in %s", name, dbName)
	}
	return nil
}

// showNewAsymmetricKeyDialog is the Asymmetric Keys folder's entry point.
func (a *App) showNewAsymmetricKeyDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newAsymmetricKeyDialog.show(sc, node)
}
