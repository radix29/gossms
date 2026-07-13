package tui

import gosmo "github.com/radix29/gosmo"

// loadSecurityChildren returns the server-level Security folder's children:
// Logins and Server Roles.
func loadSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Logins", NodeLogins, "", "", ""),
		l.node("Server Roles", NodeServerRoles, "", "", ""),
	}, nil
}

func loadLoginsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Login, error) { return l.sc.Server.LoginsContext(l.ctx) },
		func(login *gosmo.Login) *explorerNode {
			return l.node(login.Name, NodeLogin, "", login.Name, "")
		})
}

func loadServerRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.ServerRole, error) { return l.sc.Server.ServerRolesContext(l.ctx) },
		func(r *gosmo.ServerRole) *explorerNode {
			return l.node(r.Name, NodeServerRole, "", r.Name, "")
		})
}
