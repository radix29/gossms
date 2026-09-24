package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_symmetric_key_dialog.go is the New Symmetric Key dialog (a database's
// Security > Symmetric Keys folder), built on newObjectDialog — New Asymmetric
// Key's shape, with the key's encryptions in place of the private-key
// protection.
//
// A new key is encrypted by any of a certificate, an asymmetric key and a
// password, at least one. Encryption by another symmetric key is not offered
// here: it needs that key opened first, which is the decryptor prompt the
// Encryption page owns. No master key section either — every encryptor here
// uses a public key or a password, and none needs the master key (see
// master_key.go).
//
// An instance with an EKM provider registered also offers FROM PROVIDER
// (providerKeyFields), which takes no encryption and no key material.
//
// KEY_SOURCE and IDENTITY_VALUE are optional, and are the one way to create
// the same key in a second database (docs/decisions.md § Keys and
// certificates). Both are secrets: masked, confirmed, and scripted as
// placeholders.

// symKeyAlgorithms is what the dialog offers, strongest last and the default.
// Every other algorithm is deprecated, and at compatibility level 130 and
// above a syntax error (Msg 102), so only AES is offered even though gosmo
// accepts the rest.
var symKeyAlgorithms = []gosmo.SymmetricKeyAlgorithm{
	gosmo.SymmetricKeyAES128, gosmo.SymmetricKeyAES192, gosmo.SymmetricKeyAES256,
}

// symKeyNoEncryptor is the first item of the certificate and asymmetric-key
// pickers: not encrypted by one.
const symKeyNoEncryptor = "(none)"

// nsymPrefetch is what the dialog reads before it opens.
type nsymPrefetch struct {
	existingNames  *nameSet
	certificates   []string
	asymmetricKeys []string
	providers      []*gosmo.CryptographicProvider
}

func fetchNewSymmetricKeyPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nsymPrefetch, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	keys, err := dbObj.SymmetricKeys(ctx)
	if err != nil {
		return nil, err
	}
	pf := &nsymPrefetch{existingNames: newNameSet(databaseCollation(dbObj))}
	for _, k := range keys {
		pf.existingNames.Add(k.Name)
	}
	certs, err := dbObj.Certificates(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range certs {
		pf.certificates = append(pf.certificates, c.Name)
	}
	asym, err := dbObj.AsymmetricKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range asym {
		pf.asymmetricKeys = append(pf.asymmetricKeys, k.Name)
	}
	pf.providers = readProviders(ctx, sc)
	return pf, nil
}

// NewSymmetricKeyDialog is the New Symmetric Key dialog.
type NewSymmetricKeyDialog struct {
	newObjectDialog[nsymPrefetch]

	// dbName and node are set by show, before the embedded dialog's own show
	// runs the prefetch that reads them — NewCertificateDialog's arrangement.
	dbName string
	node   *explorerNode
}

// NewNewSymmetricKeyDialog creates the dialog and wires its callbacks.
func NewNewSymmetricKeyDialog(app *App) *NewSymmetricKeyDialog {
	d := &NewSymmetricKeyDialog{}
	d.init(app, newObjectConfig[nsymPrefetch]{
		title: "New Symmetric Key",
		noun:  "Symmetric Key",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nsymPrefetch, error) {
			return fetchNewSymmetricKeyPrefetch(ctx, sc, d.dbName)
		},
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

// show opens the dialog for one database's Symmetric Keys folder.
func (d *NewSymmetricKeyDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.dbName = node.data.DBName
	d.node = node
	d.scriptDatabase = d.dbName
	d.newObjectDialog.show(sc)
	d.SetHeader("Instance: "+sc.Opts.Server, "Database: "+d.dbName)
}

func (d *NewSymmetricKeyDialog) buildPages(pf *nsymPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Key name", "", 30)
	algNames := make([]string, len(symKeyAlgorithms))
	for i, a := range symKeyAlgorithms {
		algNames[i] = string(a)
	}
	algRow := propsheet.Radio("Algorithm", algNames, len(algNames)-1)

	certRow := propsheet.Select("Certificate", append([]string{symKeyNoEncryptor}, pf.certificates...), 0)
	asymRow := propsheet.Select("Asymmetric key", append([]string{symKeyNoEncryptor}, pf.asymmetricKeys...), 0)
	passField := propsheet.Password("Password", 20)
	passConfirm := propsheet.Password("Confirm password", 20)

	sourceField := propsheet.Password("Key source", 30)
	sourceConfirm := propsheet.Password("Confirm key source", 30)
	identityField := propsheet.Password("Identity value", 30)
	identityConfirm := propsheet.Password("Confirm identity value", 30)

	provider := newProviderKeyFields(pf.providers)
	rows := []propsheet.Row{
		propsheet.Section("Symmetric key"),
		nameField,
		propsheet.Static("Database", dbName),
		algRow,
		propsheet.Section("Encrypted by"),
		certRow,
		asymRow,
		passField, passConfirm,
		propsheet.Note("At least one. A key encrypted by a certificate or asymmetric key is opened with its private key, and one encrypted by a password with that password; any one of them opens it. A blank password adds no password encryption."),
		propsheet.Section("Key material (optional)"),
		sourceField, sourceConfirm,
		identityField, identityConfirm,
		propsheet.Note("Leave both blank for random key material. Given both, the same pair creates the same key in another database, and so decrypts data encrypted there — keep them as you would a password. Neither can be read back from the server."),
	}
	rows = append(rows, provider.rows("Encrypted by and Key material")...)
	d.forms[0] = propsheet.NewForm(rows...)

	// picked is a picker's choice, "" for (none).
	picked := func(r *propsheet.SelectRow) string {
		if r.Selected() <= 0 {
			return ""
		}
		return r.Value()
	}
	input := func() newSymmetricKeyInput {
		return newSymmetricKeyInput{
			name:            d.objectName(),
			certificate:     picked(certRow),
			asymmetricKey:   picked(asymRow),
			password:        passField.Value(),
			passwordConfirm: passConfirm.Value(),
			keySource:       sourceField.Value(),
			sourceConfirm:   sourceConfirm.Value(),
			identityValue:   identityField.Value(),
			identityConfirm: identityConfirm.Value(),
			existingNames:   pf.existingNames,
			dbName:          dbName,
		}
	}

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		in := input()
		in.provider = provider.spec() != nil
		if err := validateNewSymmetricKey(in); err != nil {
			return err
		}
		return provider.validate()
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// DatabaseRef, not DatabaseByName: the CREATE addresses the database
		// by name, and the by-name read would not work under Script Changes.
		in := input()
		if p := provider.spec(); p != nil {
			spec := gosmo.CreateSymmetricKeyRequest{Name: in.name, Algorithm: symKeyAlgorithms[algRow.Selected()], FromProvider: p}
			if p.Disposition == gosmo.ProviderOpenExisting {
				spec.Algorithm = ""
			}
			_, err := sc.Server.DatabaseRef(dbName).CreateSymmetricKey(ctx, spec)
			return err
		}
		spec := gosmo.CreateSymmetricKeyRequest{
			Name:          in.name,
			Algorithm:     symKeyAlgorithms[algRow.Selected()],
			KeySource:     scriptSafe(ctx, in.keySource, scriptedKeySourcePlaceholder),
			IdentityValue: scriptSafe(ctx, in.identityValue, scriptedIdentityValuePlaceholder),
		}
		// gosmo's order for the encryptions a key already has, so the script
		// reads the same as Script as ▸ CREATE's.
		if in.certificate != "" {
			spec.Encryptions = append(spec.Encryptions, gosmo.SymmetricKeyEncryptor{Kind: gosmo.SymmetricKeyByCertificate, Name: in.certificate})
		}
		if in.password != "" {
			spec.Encryptions = append(spec.Encryptions, gosmo.SymmetricKeyEncryptor{Kind: gosmo.SymmetricKeyByPassword, Password: scriptSafePassword(ctx, in.password)})
		}
		if in.asymmetricKey != "" {
			spec.Encryptions = append(spec.Encryptions, gosmo.SymmetricKeyEncryptor{Kind: gosmo.SymmetricKeyByAsymmetricKey, Name: in.asymmetricKey})
		}
		_, err := sc.Server.DatabaseRef(dbName).CreateSymmetricKey(ctx, spec)
		return err
	}
}

// newSymmetricKeyInput is the page's values, gathered for
// validateNewSymmetricKey; certificate and asymmetricKey are "" for none.
type newSymmetricKeyInput struct {
	name                           string
	certificate, asymmetricKey     string
	password, passwordConfirm      string
	keySource, sourceConfirm       string
	identityValue, identityConfirm string
	existingNames                  *nameSet
	dbName                         string
	// provider is set when an EKM provider is to hold the key, which then
	// takes no encryption and no key material.
	provider bool
}

// validateNewSymmetricKey refuses what the server would, naming the field.
//
// KEY_SOURCE and IDENTITY_VALUE are refused one without the other, which the
// server allows: KEY_SOURCE alone repeats the key material under a new GUID,
// and DECRYPTBYKEY finds its key by the GUID stored in the ciphertext, so the
// copy decrypts nothing; IDENTITY_VALUE alone repeats the GUID over new
// material. Neither makes the key re-creatable, which is the only reason to
// type them.
func validateNewSymmetricKey(in newSymmetricKeyInput) error {
	if in.name == "" {
		return fmt.Errorf("key name is required")
	}
	// A #name is a temporary key, dropped with the session that made it — and
	// the pooled connection that runs the CREATE is not one the user keeps; a
	// ##name is Msg 15316, "global temporary keys are not allowed".
	if strings.HasPrefix(in.name, "#") {
		return fmt.Errorf("a name beginning with # makes a temporary key, gone when its session ends — create one from a query window")
	}
	if in.existingNames.Has(in.name) {
		return fmt.Errorf("a symmetric key named %q already exists in %s", in.name, in.dbName)
	}
	if in.provider {
		if in.certificate != "" || in.asymmetricKey != "" || in.password != "" || in.keySource != "" || in.identityValue != "" {
			return fmt.Errorf("a key an EKM provider holds takes no encryption and no key material — clear Encrypted by and Key material")
		}
		return nil
	}
	if in.certificate == "" && in.asymmetricKey == "" && in.password == "" {
		return fmt.Errorf("choose a certificate, an asymmetric key or a password to encrypt the key by")
	}
	if in.password != in.passwordConfirm {
		return fmt.Errorf("passwords do not match")
	}
	if in.keySource != in.sourceConfirm {
		return fmt.Errorf("key sources do not match")
	}
	if in.identityValue != in.identityConfirm {
		return fmt.Errorf("identity values do not match")
	}
	if (in.keySource == "") != (in.identityValue == "") {
		return fmt.Errorf("give both a key source and an identity value, or neither — one alone does not re-create the key")
	}
	return nil
}

// showNewSymmetricKeyDialog is the Symmetric Keys folder's entry point.
func (a *App) showNewSymmetricKeyDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newSymmetricKeyDialog.show(sc, node)
}
