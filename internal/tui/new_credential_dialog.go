package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_credential_dialog.go is the New Credential creation dialog (Object
// Explorer's Security > Credentials folder, "New Credential..."). One page,
// built on newObjectDialog like every other New-X so OK/Cancel/Apply/Script
// Changes behave the same.

// ncredentialPrefetch holds what the dialog needs before it opens: existing
// credential names for the uniqueness preflight, and the EKM providers
// installed on the server for the "Use Encryption Provider" dropdown.
type ncredentialPrefetch struct {
	existingNames map[string]bool
	providers     []string
}

func fetchNewCredentialPrefetch(ctx context.Context, sc *db.ServerConn) (*ncredentialPrefetch, error) {
	creds, err := sc.Server.CredentialsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(creds))
	for _, c := range creds {
		existing[strings.ToLower(c.Name)] = true
	}
	// A server with no EKM provider configured is the ordinary case, and the
	// read needs rights this dialog does not otherwise require — so a failure
	// here leaves the dropdown empty rather than failing the dialog.
	var providerNames []string
	if providers, err := sc.Server.CryptographicProvidersContext(ctx); err == nil {
		for _, p := range providers {
			providerNames = append(providerNames, p.Name)
		}
	}
	return &ncredentialPrefetch{existingNames: existing, providers: providerNames}, nil
}

// NewCredentialDialog is the New Credential creation dialog.
type NewCredentialDialog struct {
	newObjectDialog[ncredentialPrefetch]
}

// NewNewCredentialDialog creates the dialog and wires its callbacks.
func NewNewCredentialDialog(app *App) *NewCredentialDialog {
	d := &NewCredentialDialog{}
	d.init(app, newObjectConfig[ncredentialPrefetch]{
		title:   "New Credential",
		noun:    "Credential",
		pages:   []string{"General"},
		fetch:   fetchNewCredentialPrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeCredentials) },
	})
	return d
}

func (d *NewCredentialDialog) buildPages(pf *ncredentialPrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Credential name", "", 30)
	identityField := propsheet.Text("Identity", "", 40)
	passwordField := propsheet.Password("Password", 20)
	confirmField := propsheet.Password("Confirm password", 20)
	passwordField.SetValidate(func(v string) error {
		if v != confirmField.Value() {
			return fmt.Errorf("passwords do not match")
		}
		return nil
	})
	rows := []propsheet.Row{
		propsheet.Section("Credential identity"),
		nameField, identityField,
		propsheet.Section("Secret"),
		passwordField, confirmField,
		propsheet.Note("The secret is optional — an identity that needs no password can be left blank. It can never be read back afterwards."),
	}
	// The provider section appears only on a server that has one registered.
	// A dropdown whose only entry is "<none>" offers nothing to choose, and an
	// empty control the user can focus reads as a broken one.
	var providerRow *propsheet.SelectRow
	if len(pf.providers) > 0 {
		providerRow = propsheet.Select("Encryption provider", append([]string{noneItem}, pf.providers...), 0)
		rows = append(rows,
			propsheet.Section("Cryptographic provider"),
			providerRow,
			propsheet.Note("The binding is fixed once the credential exists and can't be changed afterwards."),
		)
	}

	d.forms[0] = propsheet.NewForm(rows...)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("credential name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a credential named %q already exists", name)
		}
		// CREATE CREDENTIAL has no form without IDENTITY, and the server's own
		// error for the omission is a syntax error naming nothing useful.
		if strings.TrimSpace(identityField.Value()) == "" {
			return fmt.Errorf("identity is required")
		}
		if passwordField.Value() != confirmField.Value() {
			return fmt.Errorf("passwords do not match")
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		spec := gosmo.CredentialSpec{
			Name:     d.objectName(),
			Identity: identityField.Value(),
			Secret:   passwordField.Value(),
		}
		if providerRow != nil && providerRow.Selected() != 0 {
			spec.CryptographicProvider = providerRow.Value()
		}
		_, err := sc.Server.CreateCredentialContext(ctx, spec)
		return err
	}
}
