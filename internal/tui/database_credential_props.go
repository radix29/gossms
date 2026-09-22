package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
)

// databaseScopedCredentialPropPages builds the page set for Database Scoped
// Credential Properties — one page, the shape credentialPropPages has one
// scope up. There is no rename (no ALTER DATABASE SCOPED CREDENTIAL ... WITH
// NAME and no sp_rename class for one), so the name is static and the page
// does not set renames.
//
// The rights are CONTROL on the database alone, not the wider database set —
// see gate.DBScopedCredentialRights, which records the live probe behind that.
func databaseScopedCredentialPropPages(sc *db.ServerConn, dbName, credName string) []propPage {
	name := credName
	return []propPage{
		withRequires(pageDatabaseScopedCredentialGeneral(sc, dbName, &name), dbName,
			gate.DBScopedCredentialRights()...),
	}
}

// findDatabaseScopedCredential resolves credName to a
// *gosmo.DatabaseScopedCredential in dbName.
func findDatabaseScopedCredential(ctx context.Context, sc *db.ServerConn, dbName, credName string) (*gosmo.DatabaseScopedCredential, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return dbObj.DatabaseScopedCredentialByName(ctx, credName)
}

// pageDatabaseScopedCredentialGeneral is Database Scoped Credential
// Properties > General — credentialGeneralPage's shape with the database named
// under the credential, since the identity/secret rule is the same statement
// one scope down. The reasoning lives on credentialGeneralPage.
func pageDatabaseScopedCredentialGeneral(sc *db.ServerConn, dbName string, credName *string) propPage {
	return credentialGeneralPage(dbName,
		func(ctx context.Context) (credentialFacts, error) {
			c, err := findDatabaseScopedCredential(ctx, sc, dbName, *credName)
			if err != nil {
				return credentialFacts{}, err
			}
			return credentialFacts{
				name:         c.Name,
				identity:     c.Identity,
				credentialID: c.CredentialID,
				created:      c.CreateDate,
				modified:     c.ModifyDate,
			}, nil
		},
		func(ctx context.Context, identity string, secret *string) error {
			// DatabaseRef, not DatabaseByName: the identity comes from the
			// form and the write addresses the credential by name, so the
			// extra round trip buys nothing and the lookup would not work
			// under Script Changes.
			return sc.Server.DatabaseRef(dbName).DatabaseScopedCredentialRef(*credName).
				Alter(ctx, identity, secret)
		})
}
