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

// databaseScopedCredentialPropPages builds the page set for Database Scoped
// Credential Properties — one page, the shape credentialPropPages has one
// scope up. There is no rename (no ALTER DATABASE SCOPED CREDENTIAL ... WITH
// NAME and no sp_rename class for one), so the name is static and the page
// does not set renames.
//
// The rights are CONTROL on the database alone, not the wider database set —
// see dbScopedCredentialRights, which records the live probe behind that.
func databaseScopedCredentialPropPages(sc *db.ServerConn, dbName, credName string) []propPage {
	name := credName
	return []propPage{
		withRequires(pageDatabaseScopedCredentialGeneral(sc, dbName, &name), dbName,
			dbScopedCredentialRights()...),
	}
}

// findDatabaseScopedCredential resolves credName to a
// *gosmo.DatabaseScopedCredential in dbName.
func findDatabaseScopedCredential(ctx context.Context, sc *db.ServerConn, dbName, credName string) (*gosmo.DatabaseScopedCredential, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return dbObj.DatabaseScopedCredentialByNameContext(ctx, credName)
}

// pageDatabaseScopedCredentialGeneral is Database Scoped Credential
// Properties > General.
//
// Identity and password are one unit here for the reason spelled out on the
// server-level page: ALTER DATABASE SCOPED CREDENTIAL resets both halves every
// time and an omitted SECRET sets the stored secret to NULL. There is no
// statement that changes the identity while keeping the secret, and the secret
// cannot be read back to re-supply it — so changing the identity with the
// password blank is refused rather than applied, which would silently destroy
// the secret.
func pageDatabaseScopedCredentialGeneral(sc *db.ServerConn, dbName string, credName *string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := findDatabaseScopedCredential(ctx, sc, dbName, *credName)
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

			f := propsheet.NewForm(
				propsheet.Section("Credential identity"),
				propsheet.Static("Credential name", c.Name),
				propsheet.Static("Database", dbName),
				identityRow,
				propsheet.Section("Secret"),
				passwordRow, confirmRow,
				propsheet.Note("The stored secret can never be read back. Leave both blank to keep the credential exactly as it is; changing the identity requires re-entering the password, because SQL Server clears the secret on any ALTER that omits it."),
				propsheet.Section("Summary"),
				propsheet.Static("Credential ID", strconv.Itoa(c.CredentialID)),
				propsheet.Static("Created", formatSQLDate(c.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(c.ModifyDate)),
			)

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
				// Server.Database, not DatabaseByName: the identity comes from
				// the form and the write addresses the credential by name, so
				// the extra round trip buys nothing and the lookup would not
				// work under Script Changes.
				return sc.Server.Database(dbName).DatabaseScopedCredential(*credName).
					AlterContext(ctx, identity, &typed)
			}
			return f, apply, nil
		},
	}
}
