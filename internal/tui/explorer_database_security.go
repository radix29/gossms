package tui

import (
	"context"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// loadDatabaseSecurityChildren returns a database's security folders, in
// SSMS's order.
func loadDatabaseSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Users", NodeUsers, "", "", node.data.DBName),
		l.node("Roles", NodeRoles, "", "", node.data.DBName),
		l.node("Schemas", NodeSchemas, "", "", node.data.DBName),
		l.node("Asymmetric Keys", NodeAsymmetricKeys, "", "", node.data.DBName),
		l.node("Certificates", NodeCertificates, "", "", node.data.DBName),
		l.node("Symmetric Keys", NodeSymmetricKeys, "", "", node.data.DBName),
		l.node("Database Audit Specifications", NodeDatabaseAuditSpecifications, "", "", node.data.DBName),
		l.node("Database Scoped Credentials", NodeDatabaseScopedCredentials, "", "", node.data.DBName),
		l.node("Security Policies", NodeSecurityPolicies, "", "", node.data.DBName),
		l.node("Always Encrypted Keys", NodeAlwaysEncryptedKeys, "", "", node.data.DBName),
	}, nil
}

// loadDatabaseAuditSpecificationsChildren lists a database's audit
// specifications, each labelled with its state — the same "(Disabled)" suffix
// the server-scope folder uses, and for the same reason: a disabled
// specification records nothing and nothing else in the row says so.
func loadDatabaseAuditSpecificationsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByName(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.DatabaseAuditSpecification, error) {
			return dbObj.DatabaseAuditSpecifications(l.ctx)
		},
		func(spec *gosmo.DatabaseAuditSpecification) *explorerNode {
			return l.enabledNode(NodeDatabaseAuditSpecification, node.data.DBName, spec.Name, spec.IsEnabled, spec.CreateDate)
		})
}

// loadDatabaseScopedCredentialsChildren lists a database's own credentials —
// sys.database_scoped_credentials, a separate securable from the server-level
// family under Security > Credentials.
func loadDatabaseScopedCredentialsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByName(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.DatabaseScopedCredential, error) {
			return dbObj.DatabaseScopedCredentials(l.ctx)
		},
		func(c *gosmo.DatabaseScopedCredential) *explorerNode {
			n := l.node(c.Name, NodeDatabaseScopedCredential, "", c.Name, node.data.DBName)
			n.data.CreateDate = c.CreateDate
			return n
		})
}

// loadAsymmetricKeysChildren lists a database's asymmetric keys, the ##…##
// ones SQL Server makes for itself excluded (gosmo's AsymmetricKeys does
// that). No suffix: an asymmetric key has no expiry or state to flag.
func loadAsymmetricKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByName(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.AsymmetricKey, error) {
			return dbObj.AsymmetricKeys(l.ctx)
		},
		func(k *gosmo.AsymmetricKey) *explorerNode {
			n := l.node(k.Name, NodeAsymmetricKey, "", k.Name, node.data.DBName)
			n.data.HasPrivateKey = k.HasPrivateKey()
			return n
		})
}

// loadCertificatesChildren lists a database's certificates, the ##…## ones
// SQL Server makes for itself excluded (gosmo's Certificates does that). An
// expired certificate is labelled "(Expired)", the "(Disabled)" precedent: it
// still serves an endpoint or a signed module, but BEGIN DIALOG and anything
// else that checks the date refuses it, and nothing else in the row says so.
func loadCertificatesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByName(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return listChildren(
		func() ([]*gosmo.Certificate, error) {
			return dbObj.Certificates(l.ctx)
		},
		func(c *gosmo.Certificate) *explorerNode {
			n := l.node(certificateLabel(c, now), NodeCertificate, "", c.Name, node.data.DBName)
			n.data.HasPrivateKey = c.HasPrivateKey()
			return n
		})
}

// loadSymmetricKeysChildren lists a database's symmetric keys, the database
// master key first when there is one. gosmo's SymmetricKeys leaves out the
// ##…## keys, the master key among them, so it is read on its own — and only
// a principal with a right on it sees it (MasterKey answers nil otherwise),
// which is who could use the node anyway. The one key family with a
// create_date, carried for the filter.
func loadSymmetricKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByName(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	keys, err := listChildren(
		func() ([]*gosmo.SymmetricKey, error) {
			return dbObj.SymmetricKeys(l.ctx)
		},
		func(k *gosmo.SymmetricKey) *explorerNode {
			n := l.node(k.Name, NodeSymmetricKey, "", k.Name, node.data.DBName)
			n.data.CreateDate = k.CreateDate
			return n
		})
	if err != nil {
		return nil, err
	}
	mk, err := dbObj.MasterKey(l.ctx)
	if err != nil {
		return nil, err
	}
	if mk == nil {
		return keys, nil
	}
	n := l.node(masterKeyNodeLabel, NodeMasterKey, "", "", node.data.DBName)
	n.data.CreateDate = mk.CreateDate
	return append([]*explorerNode{n}, keys...), nil
}

// certificateLabel is a certificate's tree label: its name, suffixed
// "(Expired)" once expiry_date is behind now.
func certificateLabel(c *gosmo.Certificate, now time.Time) string {
	if c.IsExpired(now) {
		return c.Name + " (Expired)"
	}
	return c.Name
}

// loadSecurityPoliciesChildren lists a database's row-level security
// policies, each labelled with its state — a disabled policy filters
// nothing, which is the first thing to know about one.
func loadSecurityPoliciesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).SecurityPolicies,
		func(p *gosmo.SecurityPolicy) *explorerNode {
			label := disabledLabel(p.Schema+"."+p.Name, p.IsEnabled)
			n := l.node(label, NodeSecurityPolicy, p.Schema, p.Name, node.data.DBName)
			n.data.IsEnabled = p.IsEnabled
			return n
		})
}

// loadAlwaysEncryptedKeysChildren returns the two Always Encrypted key
// folders.
func loadAlwaysEncryptedKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Column Master Keys", NodeColumnMasterKeys, "", "", node.data.DBName),
		l.node("Column Encryption Keys", NodeColumnEncryptionKeys, "", "", node.data.DBName),
	}, nil
}

func loadColumnMasterKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).ColumnMasterKeys,
		func(k *gosmo.ColumnMasterKey) *explorerNode {
			return l.node(k.Name, NodeColumnMasterKey, "", k.Name, node.data.DBName)
		})
}

func loadColumnEncryptionKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).ColumnEncryptionKeys,
		func(k *gosmo.ColumnEncryptionKey) *explorerNode {
			return l.node(k.Name, NodeColumnEncryptionKey, "", k.Name, node.data.DBName)
		})
}

func loadUsersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).Users,
		func(u *gosmo.User) *explorerNode {
			n := l.node(u.Name, NodeUser, "", u.Name, node.data.DBName)
			n.data.IsSystem = isSystemUser(u.Name)
			return n
		})
}

// loadRolesChildren returns the Roles folder's two folders, as SSMS has them:
// database roles, which principals are members of, and application roles,
// which a session takes on with sp_setapprole.
func loadRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Database Roles", NodeDatabaseRoles, "", "", node.data.DBName),
		l.node("Application Roles", NodeApplicationRoles, "", "", node.data.DBName),
	}, nil
}

// applicationRolesNotVisibleLabel is the row an empty Application Roles folder
// shows when the login holds neither right that sees every role. Not "none":
// sys.database_principals lists an application role only to a login with
// VIEW DEFINITION or ALTER ANY APPLICATION ROLE on the database, or some
// permission on that role (each verified live on 17, 2026-11-05, a
// db_datareader user reading zero rows beside one role), and answers the rest
// with zero rows and no error. The label names VIEW DEFINITION alone, the
// read-only right, to fit the tree's width.
const applicationRolesNotVisibleLabel = "None visible — VIEW DEFINITION is needed"

func loadApplicationRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	kids, err := listDatabaseChildren(l, node, (*gosmo.Database).ApplicationRoles,
		func(r *gosmo.ApplicationRole) *explorerNode {
			return l.node(r.Name, NodeApplicationRole, "", r.Name, node.data.DBName)
		})
	if err != nil || len(kids) > 0 {
		return kids, err
	}
	// knownDenied fails open: an unprobed or failed probe leaves the folder
	// empty as before, rather than claiming a limit nobody measured.
	c := l.sc.DatabaseCapabilities(l.ctx, node.data.DBName)
	if knownDenied(c, "VIEW DEFINITION") && knownDenied(c, "ALTER ANY APPLICATION ROLE") {
		return []*explorerNode{l.node(applicationRolesNotVisibleLabel, NodeError, "", "", node.data.DBName)}, nil
	}
	return kids, nil
}

func loadDatabaseRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).Roles,
		func(r *gosmo.DatabaseRole) *explorerNode {
			n := l.node(r.Name, NodeDatabaseRole, "", r.Name, node.data.DBName)
			n.data.IsSystem = isSystemDatabaseRole(r)
			return n
		})
}

func loadSchemasChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).Schemas,
		func(s *gosmo.Schema) *explorerNode {
			n := l.node(s.Name, NodeSchema, s.Name, s.Name, node.data.DBName)
			n.data.IsSystem = isFixedSchema(s.Name)
			return n
		})
}

// The context menus for this family's nodes, looked up through nodeMenus
// (explorer_loaders.go).

// dbCreateFolderMenu is the menu of a database folder whose one command
// creates its object: label gated on rights, then on ALTER and CONTROL on the
// database, which imply each of them. The narrow right comes first so the
// gate's note names it.
func dbCreateFolderMenu(sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem,
	label string, show func(), rights ...gate.Right) []controls.MenuItem {
	rights = append(rights[:len(rights):len(rights)], gate.AlterDatabase, gate.ControlDB)
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: label, Action: show}, sc, node.data.DBName, rights...))
}

// usersMenuItems is the Users folder's menu. New User takes the right set a
// user's own writes do (explorerObjectRights' NodeUser entry): CREATE USER
// needs ALTER ANY USER, which ALTER and CONTROL on the database both imply.
func usersMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New User...",
		func() { a.showNewUserDialog(sc, node) }, gate.AlterAnyUser)
}

func userMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showUserPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func databaseAuditSpecificationsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Database Audit Specification...",
			Action: func() { a.showNewDatabaseAuditSpecificationDialog(sc, node) }},
			sc, node.data.DBName, gate.AlterAnyDBAudit),
	)
}

func databaseAuditSpecificationMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate.Item(controls.MenuItem{Label: auditToggleLabel(node),
			Action: func() { a.toggleDatabaseAuditSpecification(sc, node) }},
			sc, node.data.DBName, gate.AlterAnyDBAudit),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showDatabaseAuditSpecificationPropertiesFor(sc, node.data.DBName, node.data.Name)
		}},
	}
}

func databaseScopedCredentialsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Database Scoped Credential...",
			Action: func() { a.showNewDatabaseScopedCredentialDialog(sc, node) }},
			sc, node.data.DBName, gate.DBScopedCredentialRights()...),
	)
}

func databaseScopedCredentialMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showDatabaseScopedCredentialPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func asymmetricKeysMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New Asymmetric Key...",
		func() { a.showNewAsymmetricKeyDialog(sc, node) }, gate.CreateAsymmetricKey, gate.AlterAnyAsymmetricKey)
}

func asymmetricKeyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		removePrivateKeyItem(sc, node, gate.AlterOnAsymmetricKey, func() {
			a.removePrivateKey(sc, node, "asymmetric key", func(ctx context.Context, d *gosmo.Database) error {
				return d.AsymmetricKeyRef(node.data.Name).RemovePrivateKey(ctx)
			})
		}),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showAsymmetricKeyPropertiesFor(sc, node.data.DBName, node.data.Name)
		}},
	}
}

func symmetricKeysMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New Symmetric Key...",
		func() { a.showNewSymmetricKeyDialog(sc, node) }, gate.CreateSymmetricKey, gate.AlterAnySymmetricKey)
}

func symmetricKeyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showSymmetricKeyPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

// masterKeyMenuItems is the database master key's menu. Delete is left to a
// query window: DROP MASTER KEY is refused while anything is encrypted by it
// (Msg 15580), and when nothing is, dropping it is rarely what anyone wants.
func masterKeyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate.Item(controls.MenuItem{Label: "Regenerate...",
			Action: func() { a.showRegenerateMasterKeyDialog(sc, node) }},
			sc, node.data.DBName, masterKeyRights...),
		gate.Item(controls.MenuItem{Label: "Back Up...",
			Action: func() { a.showBackupMasterKeyDialog(sc, node) }},
			sc, node.data.DBName, masterKeyRights...),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() { a.showMasterKeyPropertiesFor(sc, node.data.DBName) }},
	}
}

func certificatesMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New Certificate...",
		func() { a.showNewCertificateDialog(sc, node) }, gate.CreateCertificate, gate.AlterAnyCertificate)
}

func certificateMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		// Not gated: the public certificate backs up for anyone who can see
		// it, and the dialog says the private key needs CONTROL.
		{Label: "Back Up Certificate...", Action: func() { a.showBackupCertificateDialog(sc, node) }},
		removePrivateKeyItem(sc, node, gate.AlterOnCertificate, func() {
			a.removePrivateKey(sc, node, "certificate", func(ctx context.Context, d *gosmo.Database) error {
				return d.CertificateRef(node.data.Name).RemovePrivateKey(ctx)
			})
		}),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showCertificatePropertiesFor(sc, node.data.DBName, node.data.Name)
		}},
	}
}

// databaseRolesMenuItems is the Roles folder's menu. New Database Role asks
// CREATE ROLE, which every right that permits the CREATE reads 1 for
// (gate.CreateRole); the wider ones are listed so the note names the narrow
// right first.
func databaseRolesMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New Database Role...",
		func() { a.showNewRoleDialog(sc, node) }, gate.CreateRole, gate.AlterAnyDBRole)
}

func databaseRoleMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showRolePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

// applicationRolesMenuItems is the Application Roles folder's menu. There is no
// CREATE APPLICATION ROLE permission: the CREATE checks ALTER ANY APPLICATION
// ROLE, which ALTER and CONTROL on the database imply (gate.AlterAnyApplicationRole).
func applicationRolesMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New Application Role...",
		func() { a.showNewApplicationRoleDialog(sc, node) }, gate.AlterAnyApplicationRole)
}

func applicationRoleMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showApplicationRolePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

// schemasMenuItems is the Schemas folder's menu. New Schema asks CREATE SCHEMA,
// which every right that permits the CREATE reads 1 for (gate.CreateSchema);
// the wider ones are listed so the note names the narrow right first.
func schemasMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return dbCreateFolderMenu(sc, node, newQuery, refresh, "New Schema...",
		func() { a.showNewSchemaDialog(sc, node) }, gate.CreateSchema, gate.AlterAnySchema)
}

func schemaMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showSchemaPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

// schemaGroupMenuItems is a Group by Schema bucket's menu: New Query and
// Refresh only, as Security > Schemas owns the schema itself. Refresh reloads
// the folder the group was built from: Reload redirects a group there, as the
// group has no loader and reloading it would drop its children for good.
func schemaGroupMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{newQuery, {Divider: true}, refresh}
}

func securityPolicyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	toggleLabel := "Disable"
	if !node.data.IsEnabled {
		toggleLabel = "Enable"
	}
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		// The drop's groups: ALTER SECURITY POLICY ... WITH (STATE = ...)
		// needs the same two rights DROP does — see conjoinedOpRights.
		gate.ItemOnAll(controls.MenuItem{Label: toggleLabel, Action: func() { a.toggleSecurityPolicy(sc, node) }},
			sc, node.data.DBName, node.data.Schema, node.data.Name, objectDataRightGroups(node.data)...),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showSecurityPolicyPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
		}},
	}
}

func columnMasterKeysMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Column Master Key...",
			Action: func() { a.showNewColumnMasterKeyDialog(sc, node) }},
			sc, node.data.DBName, gate.AlterAnyCMK),
	)
}

func columnEncryptionKeysMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Column Encryption Key...",
			Action: func() { a.showNewColumnEncryptionKeyDialog(sc, node) }},
			sc, node.data.DBName, gate.AlterAnyCEK),
	)
}

func columnMasterKeyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showColumnMasterKeyPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func columnEncryptionKeyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showColumnEncryptionKeyPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}
