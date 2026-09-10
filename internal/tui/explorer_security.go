package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// loadSecurityChildren returns the server-level Security folder's children:
// Logins, Server Roles, Credentials, Cryptographic Providers, Audits and
// Server Audit Specifications, in SSMS's order.
func loadSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Logins", NodeLogins, "", "", ""),
		l.node("Server Roles", NodeServerRoles, "", "", ""),
		l.node("Credentials", NodeCredentials, "", "", ""),
		l.node("Cryptographic Providers", NodeCryptographicProviders, "", "", ""),
		l.node("Audits", NodeAudits, "", "", ""),
		l.node("Server Audit Specifications", NodeServerAuditSpecifications, "", "", ""),
	}, nil
}

func loadLoginsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Login, error) { return l.sc.Server.LoginsContext(l.ctx) },
		func(login *gosmo.Login) *explorerNode {
			n := l.node(login.Name, NodeLogin, "", login.Name, "")
			n.data.CreateDate = login.CreateDate
			n.data.IsSystem = isSystemLogin(login)
			return n
		})
}

func loadServerRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.ServerRole, error) { return l.sc.Server.ServerRolesContext(l.ctx) },
		func(r *gosmo.ServerRole) *explorerNode {
			n := l.node(r.Name, NodeServerRole, "", r.Name, "")
			n.data.IsSystem = isSystemServerRole(r)
			return n
		})
}

func loadCredentialsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Credential, error) { return l.sc.Server.CredentialsContext(l.ctx) },
		func(c *gosmo.Credential) *explorerNode {
			n := l.node(c.Name, NodeCredential, "", c.Name, "")
			n.data.CreateDate = c.CreateDate
			return n
		})
}

// loadCryptographicProvidersChildren lists the EKM providers registered with
// CREATE CRYPTOGRAPHIC PROVIDER. A server with none — the ordinary case — has
// an empty folder, not an error; see gosmo's credential.go.
//
// The folder is read-only. Registering a provider needs a DLL path on the
// server's own filesystem, which SSMS answers with a file browser this build
// has no way to offer, so there is no New item and no Drop entry for it.
func loadCryptographicProvidersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(
		func() ([]*gosmo.CryptographicProvider, error) {
			return l.sc.Server.CryptographicProvidersContext(l.ctx)
		},
		func(p *gosmo.CryptographicProvider) *explorerNode {
			// A disabled provider decrypts nothing, and nothing else in the
			// row says so — the same label the Audits folder uses.
			label := p.Name
			if !p.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeCryptographicProvider, "", p.Name, "")
			n.data.IsEnabled = p.IsEnabled
			return n
		})
}

func loadAuditsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.ServerAudit, error) { return l.sc.Server.ServerAuditsContext(l.ctx) },
		func(a *gosmo.ServerAudit) *explorerNode {
			// A disabled audit records nothing, and nothing else in the row
			// says so — the same label the Triggers folder uses.
			label := a.Name
			if !a.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeAudit, "", a.Name, "")
			n.data.CreateDate = a.CreateDate
			n.data.IsEnabled = a.IsEnabled
			return n
		})
}

func loadServerAuditSpecificationsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(
		func() ([]*gosmo.ServerAuditSpecification, error) {
			return l.sc.Server.ServerAuditSpecificationsContext(l.ctx)
		},
		func(spec *gosmo.ServerAuditSpecification) *explorerNode {
			label := spec.Name
			if !spec.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeServerAuditSpecification, "", spec.Name, "")
			n.data.CreateDate = spec.CreateDate
			n.data.IsEnabled = spec.IsEnabled
			return n
		})
}

// The context menus for this family's nodes, looked up through nodeMenus
// (explorer_loaders.go).

func loginsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "New Login...", Action: func() { a.showNewLoginDialog(sc) }},
			sc, "", rightAlterAnyLogin),
		{Divider: true},
		refresh,
	}
}

func loginMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showLoginProperties(sc, node.data.Name)
	})
}

func serverRoleMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showServerRolePropertiesFor(sc, node.data.Name)
	})
}

func credentialsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "New Credential...", Action: func() { a.showNewCredentialDialog(sc) }},
			sc, "", rightAlterAnyCredential),
		{Divider: true},
		refresh,
	}
}

func credentialMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showCredentialPropertiesFor(sc, node.data.Name)
	})
}

func auditsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "New Audit...", Action: func() { a.showNewAuditDialog(sc) }},
			sc, "", rightAlterAnyAudit),
		{Divider: true},
		refresh,
	}
}

func auditMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: auditToggleLabel(node),
			Action: func() { a.toggleAudit(sc, node) }},
			sc, "", rightAlterAnyAudit),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() { a.showAuditPropertiesFor(sc, node.data.Name) }},
	}
}

func serverAuditSpecificationsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: "New Server Audit Specification...",
			Action: func() { a.showNewServerAuditSpecificationDialog(sc) }},
			sc, "", rightAlterAnyAudit),
		{Divider: true},
		refresh,
	}
}

func serverAuditSpecificationMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gate(controls.MenuItem{Label: auditToggleLabel(node),
			Action: func() { a.toggleServerAuditSpecification(sc, node) }},
			sc, "", rightAlterAnyAudit),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() { a.showServerAuditSpecificationPropertiesFor(sc, node.data.Name) }},
	}
}
