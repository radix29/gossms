package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// credentialPropPages builds the page set for Credential Properties — one
// page, the way SSMS's own credential dialog is. A credential has no rename
// (there is no ALTER CREDENTIAL ... WITH NAME and no sp_rename class for one),
// so the name is static and the page does not set renames.
func credentialPropPages(sc *db.ServerConn, credName string) []propPage {
	name := credName
	return []propPage{
		withRequires(pageCredentialGeneral(sc, &name), "", rightAlterAnyCredential),
	}
}

// findCredential resolves credName to a *gosmo.Credential.
func findCredential(ctx context.Context, sc *db.ServerConn, credName string) (*gosmo.Credential, error) {
	return sc.Server.CredentialByNameContext(ctx, credName)
}

// pageCredentialGeneral is Credential Properties > General.
//
// Identity and password are one unit here, and that is not a UI preference:
// ALTER CREDENTIAL resets both halves every time, and SQL Server documents an
// omitted SECRET as setting the stored secret to NULL. There is no statement
// that changes the identity while keeping the secret, and the secret cannot be
// read back to re-supply it — so changing the identity with the password blank
// is refused rather than applied, which would silently destroy the secret.
func pageCredentialGeneral(sc *db.ServerConn, credName *string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := findCredential(ctx, sc, *credName)
			if err != nil {
				return nil, nil, err
			}

			identityRow := propsheet.Text("Identity", c.Identity, 40)
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
				propsheet.Static("Credential name", c.Name),
				identityRow,
				propsheet.Section("Secret"),
				passwordRow, confirmRow,
				propsheet.Note("The stored secret can never be read back. Leave both blank to keep the credential exactly as it is; changing the identity requires re-entering the password, because SQL Server clears the secret on any ALTER that omits it."),
			}
			if c.TargetType != "" {
				provider := c.CryptographicProvider
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
				propsheet.Static("Credential ID", strconv.Itoa(c.CredentialID)),
				propsheet.Static("Created", formatSQLDate(c.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(c.ModifyDate)),
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
				// The name-only handle, not the by-name read: the identity is
				// taken from the form and the write addresses the credential
				// by name, so the extra round trip buys nothing and would not
				// work under Script Changes.
				// Trimmed for the reason New Credential trims it: SQL Server
				// stores IDENTITY verbatim, so trailing whitespace silently
				// breaks authentication.
				identity := strings.TrimSpace(identityRow.Value())
				if identity == "" {
					return fmt.Errorf("identity is required")
				}
				return sc.Server.Credential(*credName).AlterContext(ctx, identity, &typed)
			}
			return f, apply, nil
		},
	}
}
