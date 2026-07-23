package tui

import gosmo "github.com/radix29/gosmo"

// loadManagementChildren returns the Server Objects folder's children:
// Linked Servers. SQL Server Agent lives one level up, as a sibling of
// Server Objects itself — see loadServerChildren. SQL Server Agent's own
// loaders live in agent_explorer.go.
func loadManagementChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Linked Servers", NodeLinkedServers, "", "", ""),
	}, nil
}

func loadLinkedServersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.LinkedServer, error) { return l.sc.Server.LinkedServersContext(l.ctx) },
		func(ls *gosmo.LinkedServer) *explorerNode {
			return l.node(ls.Name, NodeLinkedServer, "", ls.Name, "")
		})
}
