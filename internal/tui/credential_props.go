package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// credentialPropPages builds the page set for Credential Properties — one
// page, the way SSMS's own credential dialog is. A credential has no rename
// (there is no ALTER CREDENTIAL ... WITH NAME and no sp_rename class for one),
// so the name is static and the page does not set renames.
func credentialPropPages(sc *db.ServerConn, credName string) []propPage {
	name := credName
	return []propPage{
		withRequires(pageCredentialGeneral(sc, &name), "", gate.AlterAnyCredential),
	}
}

// findCredential resolves credName to a *gosmo.Credential.
func findCredential(ctx context.Context, sc *db.ServerConn, credName string) (*gosmo.Credential, error) {
	return sc.Server.CredentialByName(ctx, credName)
}

// credentialFacts is everything a Credential Properties > General page shows,
// however the scope obtained it. A database-scoped credential has no
// cryptographic provider binding, so it leaves targetType and provider empty
// and the provider section is dropped.
type credentialFacts struct {
	name         string
	identity     string
	credentialID int
	created      time.Time
	modified     time.Time
	targetType   string
	provider     string
}

// credentialGeneralPage builds Credential Properties > General for either
// scope: dbName is "" at the server level and names the database otherwise,
// which is the only row that differs. The two scopes are one page shape on
// purpose — docs/decisions.md § Database-scoped credentials requires them to
// state the same rule in the same wording, and when each wrote its own copy
// the two explanations had already drifted apart while saying the same thing.
//
// Identity and password are one unit here, and that is not a UI preference:
// ALTER CREDENTIAL resets both halves every time, and SQL Server documents an
// omitted SECRET as setting the stored secret to NULL. There is no statement
// that changes the identity while keeping the secret, and the secret cannot be
// read back to re-supply it — so changing the identity with the password blank
// is refused rather than applied, which would silently destroy the secret.
//
// alter addresses the credential by name from the caller's own handle: the
// identity comes from the form, so the extra by-name read buys nothing and
// would not work under Script Changes.
func credentialGeneralPage(dbName string, load func(context.Context) (credentialFacts, error),
	alter func(ctx context.Context, identity string, secret *string) error) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := load(ctx)
			if err != nil {
				return nil, nil, err
			}

			identityRow := propsheet.Text("Identity", c.identity, 40)
			passwordRow := propsheet.Password("Password", 20)
			confirmRow := propsheet.Password("Confirm password", 20)
			// On passwordRow, not confirmRow, for the reason spelled out on
			// Login Properties' matching pair: Form.Validate runs a row's
			// validator only while that row is dirty, and Confirm left at its
			// blank baseline would skip the check.
			passwordRow.SetValidate(func(v string) error {
				if v != confirmRow.Value() {
					return fmt.Errorf("passwords do not match")
				}
				return nil
			})

			rows := []propsheet.Row{
				propsheet.Section("Credential identity"),
				propsheet.Static("Credential name", c.name),
			}
			if dbName != "" {
				rows = append(rows, propsheet.Static("Database", dbName))
			}
			rows = append(rows,
				identityRow,
				propsheet.Section("Secret"),
				passwordRow, confirmRow,
				propsheet.Note("The stored secret can never be read back. Leave both blank to keep the credential exactly as it is; changing the identity requires re-entering the password, because SQL Server clears the secret on any ALTER that omits it."),
			)
			if c.targetType != "" {
				provider := c.provider
				if provider == "" {
					provider = "<not visible to this login>"
				}
				rows = append(rows,
					propsheet.Section("Cryptographic provider"),
					propsheet.Static("Provider", provider),
					propsheet.Note("A credential's provider binding is fixed when it is created and can't be changed here."),
				)
			}
			rows = append(rows,
				propsheet.Section("Summary"),
				propsheet.Static("Credential ID", strconv.Itoa(c.credentialID)),
				propsheet.Static("Created", formatSQLDate(c.created)),
				propsheet.Static("Modified", formatSQLDate(c.modified)),
			)

			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				typed := passwordRow.Value()
				if typed == "" && !identityRow.Dirty() {
					return nil
				}
				if typed == "" {
					return fmt.Errorf("changing the identity clears the stored secret — re-enter the password to change it")
				}
				if typed != confirmRow.Value() {
					return fmt.Errorf("passwords do not match")
				}
				// Trimmed for the reason New Credential trims it: SQL Server
				// stores IDENTITY verbatim, so a pasted trailing space becomes
				// part of the account name and the credential then fails to
				// authenticate with nothing on the page saying why.
				identity := strings.TrimSpace(identityRow.Value())
				if identity == "" {
					return fmt.Errorf("identity is required")
				}
				secret := scriptSafeSecret(ctx, typed)
				return alter(ctx, identity, &secret)
			}
			return f, apply, nil
		},
	}
}

// pageCredentialGeneral is Credential Properties > General at the server
// level; credentialGeneralPage holds the shape and the reasoning.
func pageCredentialGeneral(sc *db.ServerConn, credName *string) propPage {
	return credentialGeneralPage("",
		func(ctx context.Context) (credentialFacts, error) {
			c, err := findCredential(ctx, sc, *credName)
			if err != nil {
				return credentialFacts{}, err
			}
			return credentialFacts{
				name:         c.Name,
				identity:     c.Identity,
				credentialID: c.CredentialID,
				created:      c.CreateDate,
				modified:     c.ModifyDate,
				targetType:   c.TargetType,
				provider:     c.CryptographicProvider,
			}, nil
		},
		func(ctx context.Context, identity string, secret *string) error {
			// CredentialRef, the name-only handle, not the by-name read.
			return sc.Server.CredentialRef(*credName).Alter(ctx, identity, secret)
		})
}
