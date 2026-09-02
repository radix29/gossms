package tui

import gosmo "github.com/radix29/gosmo"

// loadSecurityChildren returns the server-level Security folder's children:
// Logins, Server Roles, Credentials, Audits and Server Audit Specifications,
// in SSMS's order.
func loadSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Logins", NodeLogins, "", "", ""),
		l.node("Server Roles", NodeServerRoles, "", "", ""),
		l.node("Credentials", NodeCredentials, "", "", ""),
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
